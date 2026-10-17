package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// ActivityFilter selects activity records. Slice filters are OR within the
// slice; different filters are ANDed.
type ActivityFilter struct {
	Projects    []string
	Issues      []string
	Actors      []string
	Sessions    []string
	ClientHosts []string
	Kinds       []model.ActivityKind
	Field       string // only records whose changes contain this field
	Since       *model.Time
	Until       *model.Time
	Asc         bool // default is newest first
	Limit       int
	Offset      int
}

const activityCols = `a.id, i.key, p.key, a.kind, a.actor_id, a.session_id, a.client_host, a.at, a.changes, a.body`

const activityFrom = ` from activity a join issue i on i.id = a.issue_id join project p on p.id = i.project_id`

func scanActivity(sc interface{ Scan(...any) error }) (*model.Activity, error) {
	var a model.Activity
	var at string
	var changes, body sql.NullString
	if err := sc.Scan(&a.ID, &a.Issue, &a.Project, &a.Kind, &a.Actor, &a.SessionID, &a.ClientHost, &at, &changes, &body); err != nil {
		return nil, err
	}
	var err error
	if a.At, err = model.ParseTime(at); err != nil {
		return nil, err
	}
	if changes.Valid {
		if err := json.Unmarshal([]byte(changes.String), &a.Changes); err != nil {
			return nil, err
		}
	}
	if body.Valid {
		a.Body = &body.String
	}
	return &a, nil
}

func insertActivity(ctx context.Context, tx *sql.Tx, issueID int64, kind model.ActivityKind, tr model.Trace, at model.Time, changes map[string]model.Change, body *string) (int64, error) {
	var changesJSON any
	if changes != nil {
		b, err := json.Marshal(changes)
		if err != nil {
			return 0, err
		}
		changesJSON = string(b)
	}
	var bodyVal any
	if body != nil {
		bodyVal = *body
	}
	res, err := tx.ExecContext(ctx,
		`insert into activity(issue_id, kind, actor_id, session_id, client_host, at, changes, body) values (?, ?, ?, ?, ?, ?, ?, ?)`,
		issueID, string(kind), tr.Actor, tr.SessionID, tr.ClientHost, at.String(), changesJSON, bodyVal)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AddComment appends a commented record to an issue. Comments are allowed
// on issues in any status, including deleted, but not in a project that is
// archived or deleted.
func (s *Store) AddComment(ctx context.Context, tr model.Trace, key, body string) (*model.Activity, error) {
	if err := model.ValidateCommentBody(body); err != nil {
		return nil, err
	}
	var id int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		var issueID int64
		var projectStatus string
		err := tx.QueryRowContext(ctx, `select i.id, p.status from issue i join project p on p.id = i.project_id where i.key = ?`, key).Scan(&issueID, &projectStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if model.ProjectStatus(projectStatus) != model.ProjectActive {
			return ErrProjectNotActive
		}
		id, err = insertActivity(ctx, tx, issueID, model.ActivityCommented, tr, s.stamp(), nil, &body)
		return err
	})
	if err != nil {
		return nil, err
	}
	return scanActivity(s.db.QueryRowContext(ctx, `select `+activityCols+activityFrom+` where a.id = ?`, id))
}

// ListActivity returns a page of records and the total matching count.
func (s *Store) ListActivity(ctx context.Context, f ActivityFilter) ([]model.Activity, int, error) {
	var where []string
	var args []any
	in := func(col string, vals []string) {
		if len(vals) == 0 {
			return
		}
		where = append(where, col+" in ("+placeholders(len(vals))+")")
		for _, v := range vals {
			args = append(args, v)
		}
	}
	in("p.key", f.Projects)
	in("i.key", f.Issues)
	in("a.actor_id", f.Actors)
	in("a.session_id", f.Sessions)
	in("a.client_host", f.ClientHosts)
	kinds := make([]string, len(f.Kinds))
	for n, k := range f.Kinds {
		kinds[n] = string(k)
	}
	in("a.kind", kinds)
	if f.Field != "" {
		where = append(where, "json_type(a.changes, ?) is not null")
		args = append(args, "$."+f.Field)
	}
	if f.Since != nil {
		where = append(where, "a.at >= ?")
		args = append(args, f.Since.String())
	}
	if f.Until != nil {
		where = append(where, "a.at < ?")
		args = append(args, f.Until.String())
	}
	cond := ""
	if len(where) > 0 {
		cond = " where " + strings.Join(where, " and ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*)`+activityFrom+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := " order by a.at desc, a.id desc"
	if f.Asc {
		order = " order by a.at, a.id"
	}
	rows, err := s.db.QueryContext(ctx, `select `+activityCols+activityFrom+cond+order+` limit ? offset ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.Activity{}
	for rows.Next() {
		a, err := scanActivity(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *a)
	}
	return items, total, rows.Err()
}
