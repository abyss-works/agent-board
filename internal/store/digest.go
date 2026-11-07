package store

import (
	"context"
	"database/sql"

	"github.com/wsk0715/agent-board/internal/model"
)

// DigestEvent is one change of an issue in the digest window.
type DigestEvent struct {
	Issue    string            `json:"issue"`
	Title    string            `json:"title"`
	Status   model.IssueStatus `json:"status"` // the issue's status now
	From     *string           `json:"from,omitempty"`
	To       *string           `json:"to,omitempty"`
	Question *string           `json:"question,omitempty"`
	Actor    string            `json:"actor"`
	At       model.Time        `json:"at"`
}

// DigestDocument is a record document registered in the digest window that is
// current now. Supersedes names the documents it replaces.
type DigestDocument struct {
	Name       string     `json:"name"`
	Kind       string     `json:"kind"`
	Title      *string    `json:"title"`
	Date       *string    `json:"date"`
	Supersedes []string   `json:"supersedes"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  model.Time `json:"created_at"`
}

// DigestProject is what changed in one project.
type DigestProject struct {
	Project   model.Project           `json:"project"`
	Activity  int                     `json:"activity"` // every record in the window
	Comments  int                     `json:"comments"`
	Created   Section[DigestEvent]    `json:"created"`
	Status    Section[DigestEvent]    `json:"status_changes"`
	Decisions Section[DigestEvent]    `json:"decisions_asked"`
	Documents Section[DigestDocument] `json:"documents"`
}

// Digest is what changed in a time window, per project.
type Digest struct {
	Since       model.Time      `json:"since"`
	Until       model.Time      `json:"until"`
	GeneratedAt model.Time      `json:"generated_at"`
	Projects    []DigestProject `json:"projects"`
}

// Digest summarizes the changes from since up to until (now when nil) in the given
// projects, oldest first. Without keys it covers every active project and
// leaves out those where nothing happened. Each list holds at most limit
// items and reports its full count.
func (s *Store) Digest(ctx context.Context, keys []string, since model.Time, until *model.Time, limit int) (*Digest, error) {
	end := s.stamp()
	if until != nil {
		end = *until
	}
	all := len(keys) == 0
	if all {
		projects, _, err := s.ListProjects(ctx, ProjectFilter{Statuses: []model.ProjectStatus{model.ProjectActive}, Limit: -1})
		if err != nil {
			return nil, err
		}
		for _, p := range projects {
			keys = append(keys, p.Key)
		}
	}
	d := &Digest{Since: since, Until: end, GeneratedAt: end, Projects: []DigestProject{}}
	window := []any{since.String(), end.String()}
	for _, key := range keys {
		project, err := s.GetProject(ctx, key)
		if err != nil {
			return nil, err
		}
		p := DigestProject{Project: *project}
		args := append([]any{key}, window...)
		if err := s.db.QueryRowContext(ctx, `select count(*), coalesce(sum(a.kind = 'commented'), 0)`+activityFrom+`
			where p.key = ? and a.at >= ? and a.at < ?`, args...).Scan(&p.Activity, &p.Comments); err != nil {
			return nil, err
		}
		events := func(cond string) (Section[DigestEvent], error) {
			return digestEvents(ctx, s.db, cond, args, limit)
		}
		if p.Created, err = events(`a.kind = 'created'`); err != nil {
			return nil, err
		}
		if p.Status, err = events(`a.kind = 'updated' and json_type(a.changes, '$.status') is not null`); err != nil {
			return nil, err
		}
		if p.Decisions, err = events(`json_extract(a.changes, '$.decision_needed.to') is not null`); err != nil {
			return nil, err
		}
		if p.Documents, err = digestDocuments(ctx, s.db, args, limit); err != nil {
			return nil, err
		}
		if all && p.Activity == 0 && p.Documents.Total == 0 {
			continue
		}
		d.Projects = append(d.Projects, p)
	}
	return d, nil
}

func digestEvents(ctx context.Context, db *sql.DB, cond string, args []any, limit int) (Section[DigestEvent], error) {
	where := ` where p.key = ? and a.at >= ? and a.at < ? and ` + cond
	sec := Section[DigestEvent]{Items: []DigestEvent{}}
	if err := db.QueryRowContext(ctx, `select count(*)`+activityFrom+where, args...).Scan(&sec.Total); err != nil {
		return sec, err
	}
	rows, err := db.QueryContext(ctx, `select i.key, i.title, i.status,
		json_extract(a.changes, '$.status.from'), json_extract(a.changes, '$.status.to'),
		json_extract(a.changes, '$.decision_needed.to'), a.actor_id, a.at`+activityFrom+where+
		` order by a.at, a.id limit ?`, append(args, limit)...)
	if err != nil {
		return sec, err
	}
	defer rows.Close()
	for rows.Next() {
		var e DigestEvent
		var from, to, question sql.NullString
		var at string
		if err := rows.Scan(&e.Issue, &e.Title, &e.Status, &from, &to, &question, &e.Actor, &at); err != nil {
			return sec, err
		}
		e.From, e.To, e.Question = nullable(from), nullable(to), nullable(question)
		if e.At, err = model.ParseTime(at); err != nil {
			return sec, err
		}
		sec.Items = append(sec.Items, e)
	}
	return sec, rows.Err()
}

func digestDocuments(ctx context.Context, db *sql.DB, args []any, limit int) (Section[DigestDocument], error) {
	const from = ` from document d join project p on p.id = d.project_id where p.key = ? and d.created_at >= ? and d.created_at < ?
		and (` + documentState + `) = 'current'`
	sec := Section[DigestDocument]{Items: []DigestDocument{}}
	if err := db.QueryRowContext(ctx, `select count(*)`+from, args...).Scan(&sec.Total); err != nil {
		return sec, err
	}
	rows, err := db.QueryContext(ctx, `select d.id, d.name, d.kind, d.title, d.date, d.created_by, d.created_at`+from+` order by d.created_at, d.id limit ?`, append(args, limit)...)
	if err != nil {
		return sec, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var doc DigestDocument
		var id int64
		var title, date sql.NullString
		var at string
		if err := rows.Scan(&id, &doc.Name, &doc.Kind, &title, &date, &doc.CreatedBy, &at); err != nil {
			return sec, err
		}
		doc.Title, doc.Date = nullable(title), nullable(date)
		if doc.CreatedAt, err = model.ParseTime(at); err != nil {
			return sec, err
		}
		sec.Items = append(sec.Items, doc)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return sec, err
	}
	rows.Close()
	rels, err := loadRelations(ctx, db, ids)
	if err != nil {
		return sec, err
	}
	for i, id := range ids {
		sec.Items[i].Supersedes = rels[id].supersedes
	}
	return sec, nil
}

func nullable(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}
