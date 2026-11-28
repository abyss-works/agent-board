package pg

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/wsk0715/agent-board/internal/model"
)

const milestoneCols = `m.id, p.key, m.name, m.description, m.status, m.closed_at, m.created_at, m.updated_at, m.created_by, m.updated_by,
	(SELECT COUNT(*) FROM issue i WHERE i.milestone_id = m.id AND i.status NOT IN ('cancelled', 'deleted')),
	(SELECT COUNT(*) FROM issue i WHERE i.milestone_id = m.id AND i.status = 'done')`

const milestoneFrom = ` FROM milestone m JOIN project p ON p.id = m.project_id`

func scanMilestone(row pgx.Row) (model.Milestone, error) {
	var m model.Milestone
	var status string
	var closedAt *time.Time
	var createdAt, updatedAt time.Time
	if err := row.Scan(
		&m.ID, &m.Project, &m.Name, &m.Description, &status, &closedAt,
		&createdAt, &updatedAt, &m.CreatedBy, &m.UpdatedBy,
		&m.Progress.Total, &m.Progress.Done,
	); err != nil {
		return model.Milestone{}, err
	}
	m.Status = model.MilestoneStatus(status)
	m.ClosedAt = toModelTimePtr(closedAt)
	m.CreatedAt = model.NewTime(createdAt)
	m.UpdatedAt = model.NewTime(updatedAt)
	return m, nil
}

func validateStageMilestone(name, description string) error {
	var e model.ValidationError
	if n := utf8.RuneCountInString(name); n < 1 || n > model.MaxMilestoneNameRunes {
		e.Fields = append(e.Fields, model.FieldError{
			Field:   "name",
			Reason:  "invalid_value",
			Message: fmt.Sprintf("1~%d자여야 합니다.", model.MaxMilestoneNameRunes),
		})
	}
	if utf8.RuneCountInString(description) > model.MaxMilestoneDescriptionRunes {
		e.Fields = append(e.Fields, model.FieldError{
			Field:   "description",
			Reason:  "invalid_value",
			Message: fmt.Sprintf("%d자를 넘을 수 없습니다.", model.MaxMilestoneDescriptionRunes),
		})
	}
	if len(e.Fields) > 0 {
		return &e
	}
	return nil
}

// CreateMilestone adds a three-stage milestone (default status: planned) to an active project.
func (s *Store) CreateMilestone(ctx context.Context, tr Trace, projectKey string, in CreateMilestoneInput) (model.Milestone, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	status := in.Status
	if status == "" {
		status = model.MilestonePlanned
	}
	var statusErr error
	if status != model.MilestonePlanned && status != model.MilestoneActive {
		statusErr = violation("status", "invalid_value", "생성 시 상태는 planned 또는 active여야 합니다.")
	}
	if err := model.MergeValidation(validateStageMilestone(in.Name, in.Description), statusErr); err != nil {
		return model.Milestone{}, err
	}

	var out model.Milestone
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		proj, err := getProjectTx(ctx, tx, projectKey, false)
		if err != nil {
			return err
		}
		if proj.Status != model.ProjectActive {
			return ErrProjectNotActive
		}
		now := s.now()
		var id int64
		err = tx.QueryRow(ctx,
			`INSERT INTO milestone (project_id, name, description, status, closed_at, created_at, updated_at, created_by, updated_by)
			 VALUES ($1, $2, $3, $4, NULL, $5, $5, $6, $6)
			 RETURNING id`,
			proj.ID, in.Name, in.Description, string(status), now, tr.Actor,
		).Scan(&id)
		if err != nil {
			if isUniqueViolation(err) {
				return ErrDuplicateMilestoneName
			}
			return err
		}
		m, err := scanMilestone(tx.QueryRow(ctx, `SELECT `+milestoneCols+milestoneFrom+` WHERE m.id = $1`, id))
		if err != nil {
			return err
		}
		changes := map[string]model.Change{
			"name":        makeChange(nil, m.Name),
			"description": makeChange(nil, m.Description),
			"status":      makeChange(nil, m.Status),
		}
		actID, err := insertResourceActivityTx(ctx, tx, proj.ID, model.ResourceMilestone, m.ID, model.ResourceCreated, tr, now, changes)
		if err != nil {
			return err
		}
		pk := proj.Key
		if err := emitEventTx(ctx, tx, events, now, &proj.ID, &pk, model.EventMilestoneCreated, strconv.FormatInt(m.ID, 10), tr.Actor, tr.SessionID, map[string]any{
			"activity_id": actID,
		}); err != nil {
			return err
		}
		out = m
		return nil
	})
	return out, err
}

// GetMilestone returns a milestone by ID.
func (s *Store) GetMilestone(ctx context.Context, id int64) (model.Milestone, error) {
	m, err := scanMilestone(s.pool.QueryRow(ctx, `SELECT `+milestoneCols+milestoneFrom+` WHERE m.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Milestone{}, ErrNotFound
	}
	return m, err
}

// ListMilestones returns milestones of a project matching statuses (all when empty).
func (s *Store) ListMilestones(ctx context.Context, projectKey string, statuses []model.MilestoneStatus) ([]model.Milestone, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	if _, err := s.GetProject(ctx, projectKey); err != nil {
		return nil, err
	}
	args := []any{projectKey}
	cond := ` WHERE p.key = $1`
	if len(statuses) > 0 {
		vals := make([]string, len(statuses))
		for i, st := range statuses {
			vals[i] = string(st)
		}
		args = append(args, vals)
		cond += ` AND m.status = ANY($2)`
	}
	rows, err := s.pool.Query(ctx, `SELECT `+milestoneCols+milestoneFrom+cond+` ORDER BY m.id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Milestone{}
	for rows.Next() {
		m, err := scanMilestone(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

// UpdateMilestone applies a merge patch to a milestone. Closing a milestone
// fails with *UnverifiedIssuesError if any non-cancelled, non-deleted issue in
// the milestone still has verification_state = 'pending'.
func (s *Store) UpdateMilestone(ctx context.Context, tr Trace, id int64, patch MilestonePatch) (model.Milestone, error) {
	var out model.Milestone
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		var projectID int64
		var projectKey, projectStatus string
		var cur model.Milestone
		var status string
		var closedAt *time.Time
		var createdAt, updatedAt time.Time
		err := tx.QueryRow(ctx,
			`SELECT m.id, m.project_id, p.key, p.status, m.name, m.description, m.status, m.closed_at,
			        m.created_at, m.updated_at, m.created_by, m.updated_by
			 FROM milestone m JOIN project p ON p.id = m.project_id
			 WHERE m.id = $1 FOR UPDATE OF m`, id,
		).Scan(
			&cur.ID, &projectID, &projectKey, &projectStatus, &cur.Name, &cur.Description,
			&status, &closedAt, &createdAt, &updatedAt, &cur.CreatedBy, &cur.UpdatedBy,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		cur.Project = projectKey
		cur.Status = model.MilestoneStatus(status)
		cur.ClosedAt = toModelTimePtr(closedAt)
		cur.CreatedAt = model.NewTime(createdAt)
		cur.UpdatedAt = model.NewTime(updatedAt)

		next := cur
		if patch.Name != nil {
			next.Name = *patch.Name
		}
		if patch.Description != nil {
			next.Description = *patch.Description
		}
		if patch.Status != nil {
			next.Status = *patch.Status
		}
		if next.Name == cur.Name && next.Description == cur.Description && next.Status == cur.Status {
			m, err := scanMilestone(tx.QueryRow(ctx, `SELECT `+milestoneCols+milestoneFrom+` WHERE m.id = $1`, cur.ID))
			if err != nil {
				return err
			}
			out = m
			return nil
		}
		var statusErr error
		if _, ok := model.ParseMilestoneStageStatus(string(next.Status)); !ok {
			statusErr = violation("status", "invalid_value", "planned, active, closed, deleted 중 하나여야 합니다.")
		}
		if err := model.MergeValidation(validateStageMilestone(next.Name, next.Description), statusErr); err != nil {
			return err
		}
		if model.ProjectStatus(projectStatus) != model.ProjectActive {
			return ErrProjectNotActive
		}
		if next.Status == model.MilestoneClosed && cur.Status != model.MilestoneClosed {
			pending, err := listUnverifiedMilestoneIssuesTx(ctx, tx, cur.ID)
			if err != nil {
				return err
			}
			if len(pending) > 0 {
				return &UnverifiedIssuesError{Issues: pending}
			}
		}
		now := s.now()
		nextClosed := closedAt
		switch {
		case next.Status == model.MilestonePlanned || next.Status == model.MilestoneActive:
			nextClosed = nil
		case next.Status == model.MilestoneClosed && cur.Status != model.MilestoneClosed:
			nextClosed = &now
		}
		if _, err := tx.Exec(ctx,
			`UPDATE milestone SET name = $1, description = $2, status = $3, closed_at = $4, updated_at = $5, updated_by = $6
			 WHERE id = $7`,
			next.Name, next.Description, string(next.Status), nextClosed, now, tr.Actor, cur.ID,
		); err != nil {
			if isUniqueViolation(err) {
				return ErrDuplicateMilestoneName
			}
			return err
		}
		updated, err := scanMilestone(tx.QueryRow(ctx, `SELECT `+milestoneCols+milestoneFrom+` WHERE m.id = $1`, cur.ID))
		if err != nil {
			return err
		}
		changes := map[string]model.Change{}
		if cur.Name != updated.Name {
			changes["name"] = makeChange(cur.Name, updated.Name)
		}
		if cur.Description != updated.Description {
			changes["description"] = makeChange(cur.Description, updated.Description)
		}
		if cur.Status != updated.Status {
			changes["status"] = makeChange(cur.Status, updated.Status)
		}
		actID, err := insertResourceActivityTx(ctx, tx, projectID, model.ResourceMilestone, cur.ID, model.ResourceUpdated, tr, now, changes)
		if err != nil {
			return err
		}
		pk := projectKey
		if err := emitEventTx(ctx, tx, events, now, &projectID, &pk, model.EventMilestoneUpdated, strconv.FormatInt(cur.ID, 10), tr.Actor, tr.SessionID, map[string]any{
			"activity_id": actID,
		}); err != nil {
			return err
		}
		out = updated
		return nil
	})
	return out, err
}

// DeleteMilestone marks a milestone deleted.
func (s *Store) DeleteMilestone(ctx context.Context, tr Trace, id int64) (model.Milestone, error) {
	deleted := model.MilestoneDeleted
	return s.UpdateMilestone(ctx, tr, id, MilestonePatch{Status: &deleted})
}

func listUnverifiedMilestoneIssuesTx(ctx context.Context, tx pgx.Tx, milestoneID int64) ([]model.IssueSummary, error) {
	rows, err := tx.Query(ctx,
		`SELECT i.key, p.key, i.number, i.title, i.type, i.status, i.priority,
		        i.verification_state, i.verification_note, i.version,
		        i.created_at, i.updated_at, i.closed_at, i.status_changed_at,
		        i.status_changed_by, i.created_by, i.updated_by
		 FROM issue i
		 JOIN project p ON p.id = i.project_id
		 WHERE i.milestone_id = $1
		   AND i.status NOT IN ('cancelled', 'deleted')
		   AND i.verification_state = 'pending'
		 ORDER BY i.updated_at DESC, i.id DESC`, milestoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.IssueSummary{}
	for rows.Next() {
		var it model.IssueSummary
		var typ, status, prio, vState string
		var createdAt, updatedAt, statusChangedAt time.Time
		var closedAt *time.Time
		if err := rows.Scan(
			&it.Key, &it.Project, &it.Number, &it.Title, &typ, &status, &prio,
			&vState, &it.VerificationNote, &it.Version,
			&createdAt, &updatedAt, &closedAt, &statusChangedAt,
			&it.StatusChangedBy, &it.CreatedBy, &it.UpdatedBy,
		); err != nil {
			return nil, err
		}
		it.Type = model.IssueType(typ)
		it.Status = model.IssueStatus(status)
		it.Priority = model.Priority(prio)
		it.VerificationState = model.VerificationState(vState)
		it.CreatedAt = model.NewTime(createdAt)
		it.UpdatedAt = model.NewTime(updatedAt)
		it.ClosedAt = toModelTimePtr(closedAt)
		it.StatusChangedAt = model.NewTime(statusChangedAt)
		it.Labels = []string{}
		it.DependsOn = []string{}
		it.Refs = []string{}
		items = append(items, it)
	}
	return items, rows.Err()
}

func checkMilestoneForIssueTx(ctx context.Context, tx pgx.Tx, projectID, milestoneID int64, priority model.Priority, issueType model.IssueType, force bool, reason string) (forcedReason string, err error) {
	var msProjectID int64
	var msStatus string
	err = tx.QueryRow(ctx, `SELECT project_id, status FROM milestone WHERE id = $1`, milestoneID).Scan(&msProjectID, &msStatus)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && msProjectID != projectID) {
		return "", violation("milestone_id", "invalid_value", "같은 프로젝트의 마일스톤이어야 합니다.")
	}
	if err != nil {
		return "", err
	}
	st := model.MilestoneStatus(msStatus)
	if st == model.MilestoneDeleted {
		return "", violation("milestone_id", "invalid_value", "삭제된 마일스톤은 붙일 수 없습니다.")
	}
	if st == model.MilestoneActive && priority != model.P0 && issueType != model.TypeBug {
		if !force {
			return "", ErrMilestoneScopeLocked
		}
		trimmed := strings.TrimSpace(reason)
		if trimmed == "" {
			return "", violation("reason", "required", "force=true로 진행 중 마일스톤에 예외 편입할 때는 reason이 필요합니다.")
		}
		return trimmed, nil
	}
	return "", nil
}
