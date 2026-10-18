package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// ErrDuplicateMilestoneName reports a name that another milestone of the
// project, one that is not deleted, already has. HTTP maps it to 409.
var ErrDuplicateMilestoneName = errors.New("duplicate milestone name")

// MilestoneInput is a create request. An empty Description is the default and
// a nil TargetDate sets no date.
type MilestoneInput struct {
	Name        string
	Description string
	TargetDate  *string
}

// MilestonePatch is a merge patch for a milestone. Nil fields are unchanged;
// a TargetDate that is set to nil clears the date.
type MilestonePatch struct {
	Name        *string
	Description *string
	TargetDate  Optional[string]
	Status      *model.MilestoneStatus
}

// milestoneRow is a milestone with the internal ids needed inside transactions.
type milestoneRow struct {
	model.Milestone
	projectID     int64
	projectStatus model.ProjectStatus
}

// milestoneCols are the milestone columns followed by its progress: the issues
// that count, which are all but the cancelled and deleted ones, and the done
// ones among them.
const milestoneCols = `m.id, m.project_id, p.key, p.status, m.name, m.description, m.target_date, m.status, m.closed_at,
	m.created_at, m.updated_at, m.created_by, m.updated_by,
	(select count(*) from issue i where i.milestone_id = m.id and i.status not in ('cancelled','deleted')),
	(select count(*) from issue i where i.milestone_id = m.id and i.status = 'done')`

const milestoneFrom = ` from milestone m join project p on p.id = m.project_id`

func scanMilestone(sc rowScanner) (*milestoneRow, error) {
	var r milestoneRow
	var target, closed sql.NullString
	var created, updated string
	if err := sc.Scan(&r.ID, &r.projectID, &r.Project, &r.projectStatus, &r.Name, &r.Description, &target, &r.Status, &closed,
		&created, &updated, &r.CreatedBy, &r.UpdatedBy, &r.Progress.Total, &r.Progress.Done); err != nil {
		return nil, err
	}
	if target.Valid {
		r.TargetDate = &target.String
	}
	if closed.Valid {
		t, err := model.ParseTime(closed.String)
		if err != nil {
			return nil, err
		}
		r.ClosedAt = &t
	}
	var err error
	if r.CreatedAt, err = model.ParseTime(created); err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = model.ParseTime(updated); err != nil {
		return nil, err
	}
	return &r, nil
}

func getMilestoneRow(ctx context.Context, q querier, id int64) (*milestoneRow, error) {
	r, err := scanMilestone(q.QueryRowContext(ctx, `select `+milestoneCols+milestoneFrom+` where m.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// GetMilestone returns a milestone by id regardless of its status.
func (s *Store) GetMilestone(ctx context.Context, id int64) (*model.Milestone, error) {
	r, err := getMilestoneRow(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	return &r.Milestone, nil
}

// ListMilestones returns a project's milestones with the given statuses (all
// when empty): by target date with the milestones that have none last, then
// by id.
func (s *Store) ListMilestones(ctx context.Context, project string, statuses []model.MilestoneStatus) ([]model.Milestone, error) {
	if _, err := getProject(ctx, s.db, project); err != nil {
		return nil, err
	}
	cond := ` where p.key = ?`
	args := []any{project}
	if len(statuses) > 0 {
		cond += ` and m.status in (` + placeholders(len(statuses)) + `)`
		for _, st := range statuses {
			args = append(args, string(st))
		}
	}
	rows, err := s.db.QueryContext(ctx, `select `+milestoneCols+milestoneFrom+cond+` order by (m.target_date is null), m.target_date, m.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Milestone{}
	for rows.Next() {
		r, err := scanMilestone(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, r.Milestone)
	}
	return items, rows.Err()
}

// checkMilestoneName rejects a name another milestone of the project that is
// not deleted already has. exceptID is the milestone being written, 0 for a
// new one.
func checkMilestoneName(ctx context.Context, q querier, projectID int64, name string, exceptID int64) error {
	var n int
	if err := q.QueryRowContext(ctx, `select count(*) from milestone where project_id = ? and name = ? and status != 'deleted' and id != ?`,
		projectID, name, exceptID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrDuplicateMilestoneName
	}
	return nil
}

// CreateMilestone adds an open milestone to a project. A milestone has no
// activity record; created_by and updated_by name the actor.
func (s *Store) CreateMilestone(ctx context.Context, tr model.Trace, project string, in MilestoneInput) (*model.Milestone, error) {
	invalid := model.ValidateMilestone(in.Name, in.Description, in.TargetDate)
	now := s.stamp().String()
	var id int64
	err := s.write(ctx, func(tx *sql.Tx) error {
		var projectID int64
		var status string
		err := tx.QueryRowContext(ctx, `select id, status from project where key = ?`, project).Scan(&projectID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if model.ProjectStatus(status) != model.ProjectActive {
			return ErrProjectNotActive
		}
		if invalid != nil {
			return invalid
		}
		if err := checkMilestoneName(ctx, tx, projectID, in.Name, 0); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `insert into milestone(project_id, name, description, target_date, status, closed_at, created_at, updated_at, created_by, updated_by)
			values (?, ?, ?, ?, 'open', null, ?, ?, ?, ?)`, projectID, in.Name, in.Description, in.TargetDate, now, now, tr.Actor, tr.Actor)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetMilestone(ctx, id)
}

func sameString(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// UpdateMilestone applies a merge patch. A patch that changes nothing leaves
// updated_at and updated_by untouched. Closing a milestone records closed_at
// and reopening it clears the time; a milestone that is deleted keeps whatever
// closed_at it had.
func (s *Store) UpdateMilestone(ctx context.Context, tr model.Trace, id int64, patch MilestonePatch) (*model.Milestone, error) {
	err := s.write(ctx, func(tx *sql.Tx) error {
		cur, err := getMilestoneRow(ctx, tx, id)
		if err != nil {
			return err
		}
		next := cur.Milestone
		if patch.Name != nil {
			next.Name = *patch.Name
		}
		if patch.Description != nil {
			next.Description = *patch.Description
		}
		if patch.TargetDate.Set {
			next.TargetDate = patch.TargetDate.Value
		}
		if patch.Status != nil {
			next.Status = *patch.Status
		}
		if next.Name == cur.Name && next.Description == cur.Description && sameString(next.TargetDate, cur.TargetDate) && next.Status == cur.Status {
			return nil
		}
		var statusErr error
		if _, ok := model.ParseMilestoneStatus(string(next.Status)); !ok {
			statusErr = violation("status", "open, closed, deleted 중 하나여야 합니다.")
		}
		if err := model.MergeValidation(model.ValidateMilestone(next.Name, next.Description, next.TargetDate), statusErr); err != nil {
			return err
		}
		if cur.projectStatus != model.ProjectActive {
			return ErrProjectNotActive
		}
		if next.Status != model.MilestoneDeleted {
			if err := checkMilestoneName(ctx, tx, cur.projectID, next.Name, cur.ID); err != nil {
				return err
			}
		}
		now := s.stamp()
		closedAt := cur.ClosedAt
		switch {
		case next.Status == model.MilestoneOpen:
			closedAt = nil
		case next.Status == model.MilestoneClosed && cur.Status != model.MilestoneClosed:
			closedAt = &now
		}
		_, err = tx.ExecContext(ctx, `update milestone set name = ?, description = ?, target_date = ?, status = ?, closed_at = ?, updated_at = ?, updated_by = ? where id = ?`,
			next.Name, next.Description, next.TargetDate, string(next.Status), timePtr(closedAt), now.String(), tr.Actor, cur.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetMilestone(ctx, id)
}

// checkIssueMilestone verifies that an issue of the project may carry the
// milestone: it must exist in the same project and not be deleted.
func checkIssueMilestone(ctx context.Context, q querier, projectID, milestone int64) error {
	var projectOf int64
	var status string
	err := q.QueryRowContext(ctx, `select project_id, status from milestone where id = ?`, milestone).Scan(&projectOf, &status)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && projectOf != projectID) {
		return violation("milestone", "같은 프로젝트의 마일스톤이어야 합니다.")
	}
	if err != nil {
		return err
	}
	if model.MilestoneStatus(status) == model.MilestoneDeleted {
		return violation("milestone", "삭제된 마일스톤은 붙일 수 없습니다.")
	}
	return nil
}

// milestoneFilterClause builds the condition of the issue milestone filter:
// any of the milestones, and none matching issues without a milestone.
func milestoneFilterClause(milestones []int64, none bool) (string, []any) {
	var parts []string
	var args []any
	if len(milestones) > 0 {
		parts = append(parts, "i.milestone_id in ("+placeholders(len(milestones))+")")
		for _, id := range milestones {
			args = append(args, id)
		}
	}
	if none {
		parts = append(parts, "i.milestone_id is null")
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "(" + strings.Join(parts, " or ") + ")", args
}
