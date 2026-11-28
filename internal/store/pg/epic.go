package pg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/wsk0715/agent-board/internal/model"
)

const epicCols = `e.id, p.key, e.key, e.title, e.description, e.status, e.source_issue_id, e.source_issue_key, e.closed_at, e.created_at, e.updated_at, e.created_by, e.updated_by`

func scanEpic(row pgx.Row) (model.Epic, error) {
	var e model.Epic
	var status string
	var closedAt *time.Time
	var createdAt, updatedAt time.Time
	if err := row.Scan(
		&e.ID, &e.Project, &e.Key, &e.Title, &e.Description, &status,
		&e.SourceIssueID, &e.SourceIssueKey, &closedAt,
		&createdAt, &updatedAt, &e.CreatedBy, &e.UpdatedBy,
	); err != nil {
		return model.Epic{}, err
	}
	e.Status = model.EpicStatus(status)
	e.ClosedAt = toModelTimePtr(closedAt)
	e.CreatedAt = model.NewTime(createdAt)
	e.UpdatedAt = model.NewTime(updatedAt)
	return e, nil
}

// CreateEpic creates an epic under an active project. If Key is omitted, a
// sequential "<PROJECT>-EPIC-<N>" key is generated.
func (s *Store) CreateEpic(ctx context.Context, tr Trace, projectKey string, in CreateEpicInput) (model.Epic, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	title := strings.TrimSpace(in.Title)
	if n := utf8.RuneCountInString(title); n < 1 || n > model.MaxTitleRunes {
		return model.Epic{}, violation("title", "invalid_value", fmt.Sprintf("1~%d자여야 합니다.", model.MaxTitleRunes))
	}
	var out model.Epic
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		proj, err := getProjectTx(ctx, tx, projectKey, true)
		if err != nil {
			return err
		}
		if proj.Status != model.ProjectActive {
			return ErrProjectNotActive
		}
		key := strings.ToUpper(strings.TrimSpace(in.Key))
		if key == "" {
			var count int64
			if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM epic WHERE project_id = $1`, proj.ID).Scan(&count); err != nil {
				return err
			}
			for seq := count + 1; ; seq++ {
				cand := fmt.Sprintf("%s-EPIC-%d", proj.Key, seq)
				var exists bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM epic WHERE key = $1)`, cand).Scan(&exists); err != nil {
					return err
				}
				if !exists {
					key = cand
					break
				}
			}
		}
		now := s.now()
		var id int64
		err = tx.QueryRow(ctx,
			`INSERT INTO epic (project_id, key, title, description, status, source_issue_id, source_issue_key, created_at, updated_at, created_by, updated_by)
			 VALUES ($1, $2, $3, $4, 'open', $5, $6, $7, $7, $8, $8)
			 RETURNING id`,
			proj.ID, key, title, in.Description, in.SourceIssueID, in.SourceIssueKey, now, tr.Actor,
		).Scan(&id)
		if err != nil {
			if isUniqueViolation(err) {
				return ErrDuplicateKey
			}
			return err
		}
		e, err := scanEpic(tx.QueryRow(ctx,
			`SELECT `+epicCols+` FROM epic e JOIN project p ON p.id = e.project_id WHERE e.id = $1`, id))
		if err != nil {
			return err
		}
		changes := map[string]model.Change{
			"key":         makeChange(nil, e.Key),
			"title":       makeChange(nil, e.Title),
			"description": makeChange(nil, e.Description),
			"status":      makeChange(nil, e.Status),
		}
		actID, err := insertResourceActivityTx(ctx, tx, proj.ID, model.ResourceEpic, e.ID, model.ResourceCreated, tr, now, changes)
		if err != nil {
			return err
		}
		pk := proj.Key
		if err := emitEventTx(ctx, tx, events, now, &proj.ID, &pk, model.EventEpicCreated, e.Key, tr.Actor, tr.SessionID, map[string]any{
			"activity_id": actID,
		}); err != nil {
			return err
		}
		out = e
		return nil
	})
	return out, err
}

// GetEpic returns an epic by key.
func (s *Store) GetEpic(ctx context.Context, key string) (model.Epic, error) {
	key = strings.ToUpper(strings.TrimSpace(key))
	e, err := scanEpic(s.pool.QueryRow(ctx,
		`SELECT `+epicCols+` FROM epic e JOIN project p ON p.id = e.project_id WHERE e.key = $1`, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Epic{}, ErrNotFound
	}
	return e, err
}

// ListEpics returns epics of a project matching filter.
func (s *Store) ListEpics(ctx context.Context, projectKey string, filter EpicFilter) ([]model.Epic, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	if _, err := s.GetProject(ctx, projectKey); err != nil {
		return nil, err
	}
	args := []any{projectKey}
	cond := ` WHERE p.key = $1`
	if len(filter.Statuses) > 0 {
		vals := make([]string, len(filter.Statuses))
		for i, st := range filter.Statuses {
			vals[i] = string(st)
		}
		args = append(args, vals)
		cond += fmt.Sprintf(` AND e.status = ANY($%d)`, len(args))
	}
	q := `SELECT ` + epicCols + ` FROM epic e JOIN project p ON p.id = e.project_id` + cond + ` ORDER BY e.id ASC`
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		q += fmt.Sprintf(` LIMIT $%d`, len(args))
	}
	if filter.Offset > 0 {
		args = append(args, filter.Offset)
		q += fmt.Sprintf(` OFFSET $%d`, len(args))
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Epic{}
	for rows.Next() {
		e, err := scanEpic(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, rows.Err()
}

// UpdateEpic applies a partial update to an epic.
func (s *Store) UpdateEpic(ctx context.Context, tr Trace, key string, patch EpicPatch) (model.Epic, error) {
	key = strings.ToUpper(strings.TrimSpace(key))
	var out model.Epic
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		var projectID int64
		var projectStatus string
		var cur model.Epic
		var status string
		var closedAt *time.Time
		var createdAt, updatedAt time.Time
		err := tx.QueryRow(ctx,
			`SELECT e.id, e.project_id, p.key, p.status, e.key, e.title, e.description, e.status,
			        e.source_issue_id, e.source_issue_key, e.closed_at, e.created_at, e.updated_at, e.created_by, e.updated_by
			 FROM epic e JOIN project p ON p.id = e.project_id
			 WHERE e.key = $1 FOR UPDATE OF e`, key,
		).Scan(
			&cur.ID, &projectID, &cur.Project, &projectStatus, &cur.Key, &cur.Title, &cur.Description, &status,
			&cur.SourceIssueID, &cur.SourceIssueKey, &closedAt, &createdAt, &updatedAt, &cur.CreatedBy, &cur.UpdatedBy,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		cur.Status = model.EpicStatus(status)
		cur.ClosedAt = toModelTimePtr(closedAt)
		cur.CreatedAt = model.NewTime(createdAt)
		cur.UpdatedAt = model.NewTime(updatedAt)

		next := cur
		if patch.Title != nil {
			next.Title = strings.TrimSpace(*patch.Title)
		}
		if patch.Description != nil {
			next.Description = *patch.Description
		}
		if patch.Status != nil {
			next.Status = *patch.Status
		}
		if next.Title == cur.Title && next.Description == cur.Description && next.Status == cur.Status {
			out = cur
			return nil
		}
		if n := utf8.RuneCountInString(next.Title); n < 1 || n > model.MaxTitleRunes {
			return violation("title", "invalid_value", fmt.Sprintf("1~%d자여야 합니다.", model.MaxTitleRunes))
		}
		if _, ok := model.ParseEpicStatus(string(next.Status)); !ok {
			return violation("status", "invalid_value", "open, closed, deleted 중 하나여야 합니다.")
		}
		if model.ProjectStatus(projectStatus) != model.ProjectActive {
			return ErrProjectNotActive
		}
		now := s.now()
		nextClosed := closedAt
		switch {
		case next.Status == model.EpicOpen:
			nextClosed = nil
		case next.Status == model.EpicClosed && cur.Status != model.EpicClosed:
			nextClosed = &now
		}
		if _, err := tx.Exec(ctx,
			`UPDATE epic SET title = $1, description = $2, status = $3, closed_at = $4, updated_at = $5, updated_by = $6
			 WHERE id = $7`,
			next.Title, next.Description, string(next.Status), nextClosed, now, tr.Actor, cur.ID,
		); err != nil {
			return err
		}
		updated, err := scanEpic(tx.QueryRow(ctx,
			`SELECT `+epicCols+` FROM epic e JOIN project p ON p.id = e.project_id WHERE e.id = $1`, cur.ID))
		if err != nil {
			return err
		}
		changes := map[string]model.Change{}
		if cur.Title != updated.Title {
			changes["title"] = makeChange(cur.Title, updated.Title)
		}
		if cur.Description != updated.Description {
			changes["description"] = makeChange(cur.Description, updated.Description)
		}
		if cur.Status != updated.Status {
			changes["status"] = makeChange(cur.Status, updated.Status)
		}
		actID, err := insertResourceActivityTx(ctx, tx, projectID, model.ResourceEpic, cur.ID, model.ResourceUpdated, tr, now, changes)
		if err != nil {
			return err
		}
		pk := cur.Project
		if err := emitEventTx(ctx, tx, events, now, &projectID, &pk, model.EventEpicUpdated, cur.Key, tr.Actor, tr.SessionID, map[string]any{
			"activity_id": actID,
		}); err != nil {
			return err
		}
		out = updated
		return nil
	})
	return out, err
}

// DeleteEpic marks an epic deleted.
func (s *Store) DeleteEpic(ctx context.Context, tr Trace, key string) (model.Epic, error) {
	deleted := model.EpicDeleted
	return s.UpdateEpic(ctx, tr, key, EpicPatch{Status: &deleted})
}
