package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// ResourceActivityFilter selects the records of one project: those of the
// project, its topics and its milestones. Resource and ResourceID narrow them;
// zero values select all.
type ResourceActivityFilter struct {
	Project    string
	Resource   model.ResourceKind
	ResourceID int64
	Limit      int
	Offset     int
}

// resourceChanges lists the fields whose value differs between before and
// after as {from, to}. A field missing from before has the value null, which
// makes the changes of a creation.
func resourceChanges(before, after map[string]any) map[string]model.Change {
	changes := map[string]model.Change{}
	for f, n := range after {
		ob, _ := json.Marshal(before[f])
		nb, _ := json.Marshal(n)
		if string(ob) == string(nb) {
			continue
		}
		changes[f] = model.Change{From: ob, To: nb}
	}
	return changes
}

// insertResourceActivity appends a record. It does nothing when there are no
// changes.
func insertResourceActivity(ctx context.Context, tx *sql.Tx, projectID int64, resource model.ResourceKind, resourceID int64,
	kind model.ResourceActivityKind, tr model.Trace, at model.Time, changes map[string]model.Change) error {
	if len(changes) == 0 {
		return nil
	}
	b, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `insert into resource_activity(project_id, resource, resource_id, kind, actor_id, session_id, client_host, at, changes)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?)`, projectID, string(resource), resourceID, string(kind), tr.Actor, tr.SessionID, tr.ClientHost, at.String(), string(b))
	return err
}

// ListResourceActivity returns a page of the records of a project, newest
// first, and the total matching count.
func (s *Store) ListResourceActivity(ctx context.Context, f ResourceActivityFilter) ([]model.ResourceActivity, int, error) {
	var projectID int64
	err := s.db.QueryRowContext(ctx, `select id from project where key = ?`, f.Project).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	where := []string{"project_id = ?"}
	args := []any{projectID}
	if f.Resource != "" {
		where = append(where, "resource = ?")
		args = append(args, string(f.Resource))
	}
	if f.ResourceID != 0 {
		where = append(where, "resource_id = ?")
		args = append(args, f.ResourceID)
	}
	cond := " where " + strings.Join(where, " and ")
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*) from resource_activity`+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `select id, resource, resource_id, kind, actor_id, session_id, client_host, at, changes
		from resource_activity`+cond+` order by id desc limit ? offset ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.ResourceActivity{}
	for rows.Next() {
		a := model.ResourceActivity{Project: f.Project}
		var at, changes string
		if err := rows.Scan(&a.ID, &a.Resource, &a.ResourceID, &a.Kind, &a.Actor, &a.SessionID, &a.ClientHost, &at, &changes); err != nil {
			return nil, 0, err
		}
		if a.At, err = model.ParseTime(at); err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal([]byte(changes), &a.Changes); err != nil {
			return nil, 0, err
		}
		items = append(items, a)
	}
	return items, total, rows.Err()
}
