package pg

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/wsk0715/agent-board/internal/model"
)

const projectCols = `id, key, name, scope_kind, status, created_at, updated_at, created_by, updated_by`

func scanProject(row pgx.Row) (model.Project, error) {
	var p model.Project
	var scopeKind, status string
	var createdAt, updatedAt time.Time
	if err := row.Scan(
		&p.ID, &p.Key, &p.Name, &scopeKind, &status,
		&createdAt, &updatedAt, &p.CreatedBy, &p.UpdatedBy,
	); err != nil {
		return model.Project{}, err
	}
	p.ScopeKind = model.ScopeKind(scopeKind)
	p.Status = model.ProjectStatus(status)
	p.CreatedAt = model.NewTime(createdAt)
	p.UpdatedAt = model.NewTime(updatedAt)
	return p, nil
}

func getProjectTx(ctx context.Context, tx pgx.Tx, key string, forUpdate bool) (model.Project, error) {
	q := `SELECT ` + projectCols + ` FROM project WHERE key = $1`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	p, err := scanProject(tx.QueryRow(ctx, q, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Project{}, ErrNotFound
	}
	return p, err
}

// CreateProject creates a new project-scoped project.
func (s *Store) CreateProject(ctx context.Context, tr Trace, in CreateProjectInput) (model.Project, error) {
	key := model.NormalizeProjectKey(in.Key)
	if err := model.ValidateProject(key, in.Name); err != nil {
		return model.Project{}, err
	}
	if key == model.GlobalProjectKey {
		return model.Project{}, ErrDuplicateKey
	}
	var out model.Project
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		now := s.now()
		p, err := scanProject(tx.QueryRow(ctx,
			`INSERT INTO project (key, name, scope_kind, status, next_number, created_at, updated_at, created_by, updated_by)
			 VALUES ($1, $2, 'project', 'active', 1, $3, $3, $4, $4)
			 RETURNING `+projectCols,
			key, in.Name, now, tr.Actor,
		))
		if err != nil {
			if isUniqueViolation(err) {
				return ErrDuplicateKey
			}
			return err
		}
		changes := map[string]model.Change{
			"key":    makeChange(nil, p.Key),
			"name":   makeChange(nil, p.Name),
			"status": makeChange(nil, p.Status),
		}
		actID, err := insertResourceActivityTx(ctx, tx, p.ID, model.ResourceProject, p.ID, model.ResourceCreated, tr, now, changes)
		if err != nil {
			return err
		}
		pk := p.Key
		if err := emitEventTx(ctx, tx, events, now, &p.ID, &pk, model.EventProjectCreated, p.Key, tr.Actor, tr.SessionID, map[string]any{
			"activity_id": actID,
		}); err != nil {
			return err
		}
		out = p
		return nil
	})
	return out, err
}

// GetProject returns a project by key (including the GLOBAL scope).
func (s *Store) GetProject(ctx context.Context, key string) (model.Project, error) {
	key = model.NormalizeProjectKey(key)
	p, err := scanProject(s.pool.QueryRow(ctx, `SELECT `+projectCols+` FROM project WHERE key = $1`, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Project{}, ErrNotFound
	}
	return p, err
}

// ListProjects returns projects matching statuses (all when empty), ordered by id.
func (s *Store) ListProjects(ctx context.Context, statuses []model.ProjectStatus) ([]model.Project, error) {
	var rows pgx.Rows
	var err error
	if len(statuses) == 0 {
		rows, err = s.pool.Query(ctx, `SELECT `+projectCols+` FROM project ORDER BY id ASC`)
	} else {
		vals := make([]string, len(statuses))
		for i, st := range statuses {
			vals[i] = string(st)
		}
		rows, err = s.pool.Query(ctx, `SELECT `+projectCols+` FROM project WHERE status = ANY($1) ORDER BY id ASC`, vals)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

// UpdateProject applies a partial update to a project.
func (s *Store) UpdateProject(ctx context.Context, tr Trace, key string, patch ProjectPatch) (model.Project, error) {
	key = model.NormalizeProjectKey(key)
	var out model.Project
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		cur, err := getProjectTx(ctx, tx, key, true)
		if err != nil {
			return err
		}
		next := cur
		if patch.Name != nil {
			next.Name = *patch.Name
		}
		if patch.Status != nil {
			next.Status = *patch.Status
		}
		if cur.ID == model.GlobalProjectID && next.Status == model.ProjectDeleted {
			return ErrGlobalProjectProtected
		}
		if next.Name == cur.Name && next.Status == cur.Status {
			out = cur
			return nil
		}
		var statusErr error
		if _, ok := model.ParseProjectStatus(string(next.Status)); !ok {
			statusErr = violation("status", "invalid_value", "active, archived, deleted 중 하나여야 합니다.")
		}
		if err := model.MergeValidation(model.ValidateProjectName(next.Name), statusErr); err != nil {
			return err
		}
		now := s.now()
		updated, err := scanProject(tx.QueryRow(ctx,
			`UPDATE project SET name = $1, status = $2, updated_at = $3, updated_by = $4
			 WHERE id = $5
			 RETURNING `+projectCols,
			next.Name, string(next.Status), now, tr.Actor, cur.ID,
		))
		if err != nil {
			return err
		}
		changes := map[string]model.Change{}
		if cur.Name != updated.Name {
			changes["name"] = makeChange(cur.Name, updated.Name)
		}
		if cur.Status != updated.Status {
			changes["status"] = makeChange(cur.Status, updated.Status)
		}
		actID, err := insertResourceActivityTx(ctx, tx, cur.ID, model.ResourceProject, cur.ID, model.ResourceUpdated, tr, now, changes)
		if err != nil {
			return err
		}
		pk := cur.Key
		if err := emitEventTx(ctx, tx, events, now, &cur.ID, &pk, model.EventProjectUpdated, cur.Key, tr.Actor, tr.SessionID, map[string]any{
			"activity_id": actID,
		}); err != nil {
			return err
		}
		out = updated
		return nil
	})
	return out, err
}

// DeleteProject marks a project deleted. Deleting the GLOBAL scope project is rejected.
func (s *Store) DeleteProject(ctx context.Context, tr Trace, key string) (model.Project, error) {
	key = model.NormalizeProjectKey(key)
	if key == model.GlobalProjectKey {
		return model.Project{}, ErrGlobalProjectProtected
	}
	deleted := model.ProjectDeleted
	return s.UpdateProject(ctx, tr, key, ProjectPatch{Status: &deleted})
}
