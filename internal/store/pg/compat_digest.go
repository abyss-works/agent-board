package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

// CompatDigest summarizes the changes from since up to until (now when nil) in the
// given projects, oldest first. Without keys it covers every active project and
// leaves out those where nothing happened.
func (s *Store) CompatDigest(ctx context.Context, keys []string, since time.Time, until *time.Time, limit int) (*store.Digest, error) {
	end := s.now()
	if until != nil {
		end = *until
	}
	all := len(keys) == 0
	if all {
		projects, err := s.ListProjects(ctx, []model.ProjectStatus{model.ProjectActive})
		if err != nil {
			return nil, err
		}
		for _, p := range projects {
			if p.ScopeKind == "global" {
				continue
			}
			keys = append(keys, p.Key)
		}
	}
	d := &store.Digest{Since: model.NewTime(since), Until: model.NewTime(end), GeneratedAt: model.NewTime(end), Projects: []store.DigestProject{}}
	for _, key := range keys {
		project, err := s.GetProject(ctx, key)
		if err != nil {
			return nil, err
		}
		p := store.DigestProject{Project: project}
		if err := s.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE a.kind = 'commented')
			FROM activity a JOIN issue i ON i.id = a.issue_id JOIN project p ON p.id = i.project_id
			WHERE p.key = $1 AND a.at >= $2 AND a.at < $3`, key, since, end).Scan(&p.Activity, &p.Comments); err != nil {
			return nil, err
		}
		events := func(cond string) (store.Section[store.DigestEvent], error) {
			return s.compatDigestEvents(ctx, cond, key, since, end, limit)
		}
		if p.Created, err = events(`a.kind = 'created'`); err != nil {
			return nil, err
		}
		if p.Status, err = events(`a.kind = 'updated' AND a.changes ? 'status'`); err != nil {
			return nil, err
		}
		if p.Decisions, err = events(`a.changes->'decision_needed'->>'to' IS NOT NULL`); err != nil {
			return nil, err
		}
		if p.Documents, err = s.compatDigestDocuments(ctx, key, since, end, limit); err != nil {
			return nil, err
		}
		if all && p.Activity == 0 && p.Documents.Total == 0 {
			continue
		}
		d.Projects = append(d.Projects, p)
	}
	return d, nil
}

func (s *Store) compatDigestEvents(ctx context.Context, cond, key string, since, end time.Time, limit int) (store.Section[store.DigestEvent], error) {
	const from = ` FROM activity a JOIN issue i ON i.id = a.issue_id JOIN project p ON p.id = i.project_id`
	where := ` WHERE p.key = $1 AND a.at >= $2 AND a.at < $3 AND ` + cond
	sec := store.Section[store.DigestEvent]{Items: []store.DigestEvent{}}
	if err := s.pool.QueryRow(ctx, `SELECT count(*)`+from+where, key, since, end).Scan(&sec.Total); err != nil {
		return sec, err
	}
	rows, err := s.pool.Query(ctx, `SELECT i.key, i.title, i.status, a.changes->'status'->>'from', a.changes->'status'->>'to',
		a.changes->'decision_needed'->>'to', a.actor_id, a.at`+from+where+` ORDER BY a.at, a.id LIMIT $4`, key, since, end, limit)
	if err != nil {
		return sec, err
	}
	defer rows.Close()
	for rows.Next() {
		var e store.DigestEvent
		var at time.Time
		if err := rows.Scan(&e.Issue, &e.Title, &e.Status, &e.From, &e.To, &e.Question, &e.Actor, &at); err != nil {
			return sec, err
		}
		e.At = model.NewTime(at)
		sec.Items = append(sec.Items, e)
	}
	return sec, rows.Err()
}

func (s *Store) compatDigestDocuments(ctx context.Context, key string, since, end time.Time, limit int) (store.Section[store.DigestDocument], error) {
	const from = ` FROM record_document d JOIN project p ON p.id = d.project_id
		WHERE p.key = $1 AND d.created_at >= $2 AND d.created_at < $3 AND NOT d.archived
		  AND NOT EXISTS (SELECT 1 FROM graph_edge ge WHERE ge.kind = 'supersedes' AND ge.status = 'confirmed' AND ge.target_type = 'record' AND ge.target_id = d.id)`
	sec := store.Section[store.DigestDocument]{Items: []store.DigestDocument{}}
	if err := s.pool.QueryRow(ctx, `SELECT count(*)`+from, key, since, end).Scan(&sec.Total); err != nil {
		return sec, err
	}
	rows, err := s.pool.Query(ctx, `SELECT d.id, d.name, d.kind, NULLIF(d.title, ''), d.recorded_at, d.created_by, d.created_at`+from+` ORDER BY d.created_at, d.id LIMIT $4`, key, since, end, limit)
	if err != nil {
		return sec, err
	}
	var ids []int64
	for rows.Next() {
		var doc store.DigestDocument
		var id int64
		var recorded *time.Time
		var created time.Time
		if err := rows.Scan(&id, &doc.Name, &doc.Kind, &doc.Title, &recorded, &doc.CreatedBy, &created); err != nil {
			rows.Close()
			return sec, err
		}
		doc.Date = model.DateFromName(doc.Name)
		if doc.Date == nil && recorded != nil {
			ds := recorded.UTC().Format("2006-01-02 15:04")
			doc.Date = &ds
		}
		doc.CreatedAt = model.NewTime(created)
		doc.Supersedes = []string{}
		sec.Items = append(sec.Items, doc)
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return sec, err
	}
	for i, id := range ids {
		r, err := s.pool.Query(ctx, `SELECT t.name FROM graph_edge ge JOIN record_document t ON t.id = ge.target_id
			WHERE ge.kind = 'supersedes' AND ge.status = 'confirmed' AND ge.source_type = 'record' AND ge.source_id = $1 AND ge.target_type = 'record' ORDER BY t.name`, id)
		if err != nil {
			return sec, fmt.Errorf("supersedes of %s: %w", sec.Items[i].Name, err)
		}
		for r.Next() {
			var n string
			if err := r.Scan(&n); err != nil {
				r.Close()
				return sec, err
			}
			sec.Items[i].Supersedes = append(sec.Items[i].Supersedes, n)
		}
		r.Close()
	}
	return sec, nil
}

// CompatListResourceActivity lists the records of project, epic and milestone writes
// of a project, newest first.
func (s *Store) CompatListResourceActivity(ctx context.Context, project string, resource model.ResourceKind, resourceID int64, limit, offset int) ([]model.ResourceActivity, int, error) {
	var projectID int64
	if err := s.pool.QueryRow(ctx, `SELECT id FROM project WHERE key = $1`, model.NormalizeProjectKey(project)).Scan(&projectID); err != nil {
		return nil, 0, ErrNotFound
	}
	where := []string{"project_id = $1"}
	args := []any{projectID}
	if resource != "" {
		args = append(args, string(resource))
		where = append(where, fmt.Sprintf("resource = $%d", len(args)))
	}
	if resourceID != 0 {
		args = append(args, resourceID)
		where = append(where, fmt.Sprintf("resource_id = $%d", len(args)))
	}
	cond := " WHERE " + strings.Join(where, " AND ")
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM resource_activity`+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.pool.Query(ctx, `SELECT id, resource, resource_id, kind, actor_id, session_id, client_host, at, changes
		FROM resource_activity`+cond+fmt.Sprintf(" ORDER BY id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.ResourceActivity{}
	for rows.Next() {
		a := model.ResourceActivity{Project: model.NormalizeProjectKey(project)}
		var at time.Time
		var changes []byte
		if err := rows.Scan(&a.ID, &a.Resource, &a.ResourceID, &a.Kind, &a.Actor, &a.SessionID, &a.ClientHost, &at, &changes); err != nil {
			return nil, 0, err
		}
		a.At = model.NewTime(at)
		if err := json.Unmarshal(changes, &a.Changes); err != nil {
			return nil, 0, err
		}
		items = append(items, a)
	}
	return items, total, rows.Err()
}
