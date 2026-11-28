package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/wsk0715/agent-board/internal/model"
)

const issueCols = `i.id, i.project_id, p.key, i.number, i.key, i.epic_id, e.key, i.milestone_id, m.name,
	i.title, i.body, i.type, i.status, i.priority,
	i.recheck_condition, i.decision_needed, i.status_reason,
	i.verification_state, i.verification_note, i.verified_at, i.verified_by,
	i.version, i.created_at, i.updated_at, i.closed_at,
	i.status_changed_at, i.status_changed_by, i.created_by, i.updated_by`

const issueFrom = ` FROM issue i
	JOIN project p ON p.id = i.project_id
	LEFT JOIN epic e ON e.id = i.epic_id
	LEFT JOIN milestone m ON m.id = i.milestone_id`

type issueRow struct {
	detail    IssueDetail
	projectID int64
}

func scanIssueRow(row pgx.Row) (issueRow, error) {
	var r issueRow
	var typ, status, prio, verState string
	var verifiedAt, closedAt *time.Time
	var createdAt, updatedAt, statusChangedAt time.Time
	if err := row.Scan(
		&r.detail.ID, &r.projectID, &r.detail.Project, &r.detail.Number, &r.detail.Key,
		&r.detail.EpicID, &r.detail.EpicKey, &r.detail.Milestone, &r.detail.MilestoneName,
		&r.detail.Title, &r.detail.Body, &typ, &status, &prio,
		&r.detail.RecheckCondition, &r.detail.DecisionNeeded, &r.detail.StatusReason,
		&verState, &r.detail.VerificationNote, &verifiedAt, &r.detail.VerifiedBy,
		&r.detail.Version, &createdAt, &updatedAt, &closedAt,
		&statusChangedAt, &r.detail.StatusChangedBy, &r.detail.CreatedBy, &r.detail.UpdatedBy,
	); err != nil {
		return issueRow{}, err
	}
	r.detail.Type = model.IssueType(typ)
	r.detail.Status = model.IssueStatus(status)
	r.detail.Priority = model.Priority(prio)
	r.detail.VerificationState = model.VerificationState(verState)
	r.detail.VerifiedAt = toModelTimePtr(verifiedAt)
	r.detail.CreatedAt = model.NewTime(createdAt)
	r.detail.UpdatedAt = model.NewTime(updatedAt)
	r.detail.ClosedAt = toModelTimePtr(closedAt)
	r.detail.StatusChangedAt = model.NewTime(statusChangedAt)
	r.detail.Labels = []string{}
	r.detail.DependsOn = []string{}
	r.detail.Refs = []string{}
	return r, nil
}

type pgQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func hydrateIssueCollections(ctx context.Context, q pgQuerier, details []*IssueDetail) error {
	if len(details) == 0 {
		return nil
	}
	ids := make([]int64, len(details))
	byID := make(map[int64]*IssueDetail, len(details))
	for i, d := range details {
		ids[i] = d.ID
		byID[d.ID] = d
		d.Labels = []string{}
		d.Refs = []string{}
		d.DependsOn = []string{}
	}
	rows, err := q.Query(ctx, `SELECT issue_id, label FROM issue_label WHERE issue_id = ANY($1) ORDER BY label ASC`, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var iid int64
		var label string
		if err := rows.Scan(&iid, &label); err != nil {
			rows.Close()
			return err
		}
		if d := byID[iid]; d != nil {
			d.Labels = append(d.Labels, label)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	refRows, err := q.Query(ctx, `SELECT issue_id, ref FROM issue_ref WHERE issue_id = ANY($1) ORDER BY ref ASC`, ids)
	if err != nil {
		return err
	}
	for refRows.Next() {
		var iid int64
		var ref string
		if err := refRows.Scan(&iid, &ref); err != nil {
			refRows.Close()
			return err
		}
		if d := byID[iid]; d != nil {
			d.Refs = append(d.Refs, ref)
		}
	}
	refRows.Close()
	return refRows.Err()
}

func checkEpicForIssueTx(ctx context.Context, tx pgx.Tx, projectID, epicID int64) error {
	var epicProjectID int64
	var status string
	err := tx.QueryRow(ctx, `SELECT project_id, status FROM epic WHERE id = $1`, epicID).Scan(&epicProjectID, &status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && epicProjectID != projectID) {
		return violation("epic_id", "invalid_value", "같은 프로젝트의 에픽이어야 합니다.")
	}
	if err != nil {
		return err
	}
	if model.EpicStatus(status) == model.EpicDeleted {
		return violation("epic_id", "invalid_value", "삭제된 에픽은 붙일 수 없습니다.")
	}
	return nil
}

func issueContentHash(title, body string, labels, refs []string) string {
	return sha256Hex(title + "\n" + body + "\n" + strings.Join(labels, ",") + "\n" + strings.Join(refs, ","))
}

// CreateIssue creates an issue inside a transaction, enforces active milestone
// scope locks, records an activity row and event, and enqueues an ingest job.
func (s *Store) CreateIssue(ctx context.Context, tr Trace, projectKey string, in CreateIssueInput) (IssueWriteResult, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	cand := model.Issue{
		Title:             in.Title,
		Body:              in.Body,
		Type:              in.Type,
		Status:            in.Status,
		Priority:          in.Priority,
		Labels:            in.Labels,
		Refs:              in.Refs,
		RecheckCondition:  in.RecheckCondition,
		DecisionNeeded:    in.DecisionNeeded,
		VerificationState: in.VerificationState,
		VerificationNote:  in.VerificationNote,
	}
	if cand.Type == "" {
		cand.Type = model.DefaultIssueType
	}
	if cand.Status == "" {
		cand.Status = model.DefaultIssueStatus
	}
	if cand.Priority == "" {
		cand.Priority = model.DefaultPriority(cand.Type)
	}
	if cand.VerificationState == "" {
		cand.VerificationState = model.DefaultVerificationState
	}
	model.NormalizeIssue(&cand)
	if err := model.ValidateIssue(&cand); err != nil {
		return IssueWriteResult{}, err
	}

	var res IssueWriteResult
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		var projectID int64
		var projectStatus string
		var nextNum int64
		err := tx.QueryRow(ctx,
			`SELECT id, status, next_number FROM project WHERE key = $1 FOR UPDATE`, projectKey,
		).Scan(&projectID, &projectStatus, &nextNum)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if model.ProjectStatus(projectStatus) != model.ProjectActive {
			return ErrProjectNotActive
		}
		if in.EpicID != nil {
			if err := checkEpicForIssueTx(ctx, tx, projectID, *in.EpicID); err != nil {
				return err
			}
		}
		var forcedReason string
		if in.MilestoneID != nil {
			forcedReason, err = checkMilestoneForIssueTx(ctx, tx, projectID, *in.MilestoneID, cand.Priority, cand.Type, in.Force, in.Reason)
			if err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE project SET next_number = next_number + 1 WHERE id = $1`, projectID); err != nil {
			return err
		}
		now := s.now()
		var closedAt *time.Time
		if cand.Status.Closed() {
			closedAt = &now
		}
		var verifiedAt *time.Time
		var verifiedBy *string
		if cand.VerificationState == model.VerificationVerified {
			verifiedAt = &now
			actor := tr.Actor
			verifiedBy = &actor
		}
		issueKey := model.IssueKey(projectKey, nextNum)
		var issueID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO issue (
				project_id, number, key, epic_id, milestone_id,
				title, body, type, status, priority,
				recheck_condition, decision_needed, status_reason,
				verification_state, verification_note, verified_at, verified_by,
				version, created_at, updated_at, closed_at,
				status_changed_at, status_changed_by, created_by, updated_by
			) VALUES (
				$1, $2, $3, $4, $5,
				$6, $7, $8, $9, $10,
				$11, $12, NULL,
				$13, $14, $15, $16,
				1, $17, $17, $18,
				$17, $19, $19, $19
			) RETURNING id`,
			projectID, nextNum, issueKey, in.EpicID, in.MilestoneID,
			cand.Title, cand.Body, string(cand.Type), string(cand.Status), string(cand.Priority),
			cand.RecheckCondition, cand.DecisionNeeded,
			string(cand.VerificationState), cand.VerificationNote, verifiedAt, verifiedBy,
			now, closedAt, tr.Actor,
		).Scan(&issueID); err != nil {
			return err
		}
		for _, lbl := range cand.Labels {
			if _, err := tx.Exec(ctx, `INSERT INTO issue_label (issue_id, label) VALUES ($1, $2)`, issueID, lbl); err != nil {
				return err
			}
		}
		for _, ref := range cand.Refs {
			if _, err := tx.Exec(ctx, `INSERT INTO issue_ref (issue_id, ref) VALUES ($1, $2)`, issueID, ref); err != nil {
				return err
			}
		}
		changes := map[string]model.Change{
			"title":              makeChange(nil, cand.Title),
			"type":               makeChange(nil, cand.Type),
			"status":             makeChange(nil, cand.Status),
			"priority":           makeChange(nil, cand.Priority),
			"verification_state": makeChange(nil, cand.VerificationState),
		}
		if in.EpicID != nil {
			changes["epic_id"] = makeChange(nil, *in.EpicID)
		}
		if in.MilestoneID != nil {
			changes["milestone_id"] = makeChange(nil, *in.MilestoneID)
		}
		if forcedReason != "" {
			changes["milestone_scope_override"] = makeChange(nil, map[string]any{
				"force":  true,
				"reason": forcedReason,
			})
		}
		changesJSON, err := json.Marshal(changes)
		if err != nil {
			return err
		}
		var actID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO activity (issue_id, kind, actor_id, session_id, client_host, at, changes)
			 VALUES ($1, 'created', $2, $3, $4, $5, $6::jsonb)
			 RETURNING id`,
			issueID, tr.Actor, tr.SessionID, tr.ClientHost, now, string(changesJSON),
		).Scan(&actID); err != nil {
			return err
		}
		pk := projectKey
		if err := emitEventTx(ctx, tx, events, now, &projectID, &pk, model.EventIssueCreated, issueKey, tr.Actor, tr.SessionID, map[string]any{
			"activity_id": actID,
		}); err != nil {
			return err
		}
		jobID, err := enqueueIngestJobTx(ctx, tx, model.IngestSourceIssue, issueID, issueContentHash(cand.Title, cand.Body, cand.Labels, cand.Refs))
		if err != nil {
			return err
		}
		row, err := scanIssueRow(tx.QueryRow(ctx, `SELECT `+issueCols+issueFrom+` WHERE i.id = $1`, issueID))
		if err != nil {
			return err
		}
		if err := hydrateIssueCollections(ctx, tx, []*IssueDetail{&row.detail}); err != nil {
			return err
		}
		res = IssueWriteResult{Issue: row.detail, IngestJobID: jobID}
		return nil
	})
	return res, err
}

// GetIssue returns an issue by key.
func (s *Store) GetIssue(ctx context.Context, key string) (IssueDetail, error) {
	row, err := scanIssueRow(s.pool.QueryRow(ctx, `SELECT `+issueCols+issueFrom+` WHERE i.key = $1`, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return IssueDetail{}, ErrNotFound
	}
	if err != nil {
		return IssueDetail{}, err
	}
	if err := hydrateIssueCollections(ctx, s.pool, []*IssueDetail{&row.detail}); err != nil {
		return IssueDetail{}, err
	}
	return row.detail, nil
}

// ListIssues returns issues for a project matching filter and the total count before pagination.
func (s *Store) ListIssues(ctx context.Context, projectKey string, filter IssueFilter) ([]IssueDetail, int, error) {
	projectKey = model.NormalizeProjectKey(projectKey)
	if projectKey != "" {
		if _, err := s.GetProject(ctx, projectKey); err != nil {
			return nil, 0, err
		}
	}
	var conds []string
	var args []any
	addArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if projectKey != "" {
		conds = append(conds, "p.key = "+addArg(projectKey))
	}
	if len(filter.Statuses) > 0 {
		vals := make([]string, len(filter.Statuses))
		for i, st := range filter.Statuses {
			vals[i] = string(st)
		}
		conds = append(conds, "i.status = ANY("+addArg(vals)+")")
	}
	if len(filter.Types) > 0 {
		vals := make([]string, len(filter.Types))
		for i, t := range filter.Types {
			vals[i] = string(t)
		}
		conds = append(conds, "i.type = ANY("+addArg(vals)+")")
	}
	if len(filter.Priorities) > 0 {
		vals := make([]string, len(filter.Priorities))
		for i, p := range filter.Priorities {
			vals[i] = string(p)
		}
		conds = append(conds, "i.priority = ANY("+addArg(vals)+")")
	}
	if filter.EpicID != nil {
		conds = append(conds, "i.epic_id = "+addArg(*filter.EpicID))
	}
	if filter.MilestoneID != nil {
		conds = append(conds, "i.milestone_id = "+addArg(*filter.MilestoneID))
	}
	if len(filter.VerificationStates) > 0 {
		vals := make([]string, len(filter.VerificationStates))
		for i, v := range filter.VerificationStates {
			vals[i] = string(v)
		}
		conds = append(conds, "i.verification_state = ANY("+addArg(vals)+")")
	}
	for _, lbl := range filter.Labels {
		conds = append(conds, "EXISTS (SELECT 1 FROM issue_label il WHERE il.issue_id = i.id AND il.label = "+addArg(lbl)+")")
	}

	whereClause := ""
	if len(conds) > 0 {
		whereClause = ` WHERE ` + strings.Join(conds, ` AND `)
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*)`+issueFrom+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	q := `SELECT ` + issueCols + issueFrom + whereClause + ` ORDER BY i.priority ASC, i.created_at ASC, i.id ASC`
	if filter.Limit > 0 {
		q += ` LIMIT ` + addArg(filter.Limit)
	}
	if filter.Offset > 0 {
		q += ` OFFSET ` + addArg(filter.Offset)
	}

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []IssueDetail{}
	for rows.Next() {
		r, err := scanIssueRow(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, r.detail)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	ptrs := make([]*IssueDetail, len(items))
	for i := range items {
		ptrs[i] = &items[i]
	}
	if err := hydrateIssueCollections(ctx, s.pool, ptrs); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func sameInt64Ptr(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func sameStringPtr(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// UpdateIssue applies a partial update with optional optimistic locking,
// active milestone scope enforcement, activity logging, and ingest enqueuing.
func (s *Store) UpdateIssue(ctx context.Context, tr Trace, key string, expectedVersion *int64, patch IssuePatch) (IssueWriteResult, error) {
	var res IssueWriteResult
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		curRow, err := scanIssueRow(tx.QueryRow(ctx, `SELECT `+issueCols+issueFrom+` WHERE i.key = $1 FOR UPDATE OF i`, key))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := hydrateIssueCollections(ctx, tx, []*IssueDetail{&curRow.detail}); err != nil {
			return err
		}
		var projectStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM project WHERE id = $1`, curRow.projectID).Scan(&projectStatus); err != nil {
			return err
		}
		if model.ProjectStatus(projectStatus) != model.ProjectActive {
			return ErrProjectNotActive
		}
		cur := curRow.detail
		if expectedVersion != nil && cur.Version != *expectedVersion {
			return ErrVersionMismatch
		}

		next := cur
		if patch.Title != nil {
			next.Title = *patch.Title
		}
		if patch.Body != nil {
			next.Body = *patch.Body
		}
		if patch.Type != nil {
			next.Type = *patch.Type
		}
		if patch.Status != nil {
			next.Status = *patch.Status
		}
		if patch.Priority != nil {
			next.Priority = *patch.Priority
		}
		if patch.EpicID.Set {
			next.EpicID = patch.EpicID.Value
		}
		if patch.MilestoneID.Set {
			next.Milestone = patch.MilestoneID.Value
		}
		if patch.Labels != nil {
			next.Labels = append([]string(nil), (*patch.Labels)...)
		}
		if patch.Refs != nil {
			next.Refs = append([]string(nil), (*patch.Refs)...)
		}
		if patch.RecheckCondition.Set {
			next.RecheckCondition = patch.RecheckCondition.Value
		}
		if patch.DecisionNeeded.Set {
			next.DecisionNeeded = patch.DecisionNeeded.Value
		}
		if patch.VerificationState != nil {
			next.VerificationState = *patch.VerificationState
		}
		if patch.VerificationNote.Set {
			next.VerificationNote = patch.VerificationNote.Value
		}
		model.NormalizeIssue(&next.Issue)

		reason, reasonErr := model.ResolveStatusReason(cur.Status, next.Status, patch.StatusReason)
		if err := model.MergeValidation(model.ValidateIssue(&next.Issue), reasonErr); err != nil {
			return err
		}
		if next.EpicID != nil && !sameInt64Ptr(cur.EpicID, next.EpicID) {
			if err := checkEpicForIssueTx(ctx, tx, curRow.projectID, *next.EpicID); err != nil {
				return err
			}
		}
		var forcedReason string
		if next.Milestone != nil && !sameInt64Ptr(cur.Milestone, next.Milestone) {
			forcedReason, err = checkMilestoneForIssueTx(ctx, tx, curRow.projectID, *next.Milestone, next.Priority, next.Type, patch.Force, patch.Reason)
			if err != nil {
				return err
			}
		}

		changes := map[string]model.Change{}
		if cur.Title != next.Title {
			changes["title"] = makeChange(cur.Title, next.Title)
		}
		if cur.Body != next.Body {
			changes["body"] = makeChange(cur.Body, next.Body)
		}
		if cur.Type != next.Type {
			changes["type"] = makeChange(cur.Type, next.Type)
		}
		if cur.Status != next.Status {
			changes["status"] = makeChange(cur.Status, next.Status)
			if reason != nil {
				changes["status_reason"] = makeChange(cur.StatusReason, *reason)
			}
		}
		if cur.Priority != next.Priority {
			changes["priority"] = makeChange(cur.Priority, next.Priority)
		}
		if !sameInt64Ptr(cur.EpicID, next.EpicID) {
			changes["epic_id"] = makeChange(cur.EpicID, next.EpicID)
		}
		if !sameInt64Ptr(cur.Milestone, next.Milestone) {
			changes["milestone_id"] = makeChange(cur.Milestone, next.Milestone)
		}
		if !sameStringSlice(cur.Labels, next.Labels) {
			changes["labels"] = makeChange(cur.Labels, next.Labels)
		}
		if !sameStringSlice(cur.Refs, next.Refs) {
			changes["refs"] = makeChange(cur.Refs, next.Refs)
		}
		if !sameStringPtr(cur.RecheckCondition, next.RecheckCondition) {
			changes["recheck_condition"] = makeChange(cur.RecheckCondition, next.RecheckCondition)
		}
		if !sameStringPtr(cur.DecisionNeeded, next.DecisionNeeded) {
			changes["decision_needed"] = makeChange(cur.DecisionNeeded, next.DecisionNeeded)
		}
		if cur.VerificationState != next.VerificationState {
			changes["verification_state"] = makeChange(cur.VerificationState, next.VerificationState)
		}
		if !sameStringPtr(cur.VerificationNote, next.VerificationNote) {
			changes["verification_note"] = makeChange(cur.VerificationNote, next.VerificationNote)
		}
		if forcedReason != "" {
			changes["milestone_scope_override"] = makeChange(nil, map[string]any{
				"force":  true,
				"reason": forcedReason,
			})
		}
		if len(changes) == 0 {
			res = IssueWriteResult{Issue: cur}
			return nil
		}

		now := s.now()
		statusChangedAt := cur.StatusChangedAt.Time()
		statusChangedBy := cur.StatusChangedBy
		statusReason := cur.StatusReason
		var closedAt *time.Time
		if cur.ClosedAt != nil {
			t := cur.ClosedAt.Time()
			closedAt = &t
		}
		if cur.Status != next.Status {
			statusChangedAt = now
			statusChangedBy = tr.Actor
			statusReason = reason
			if next.Status.Closed() {
				closedAt = &now
			} else {
				closedAt = nil
			}
		}
		var verifiedAt *time.Time
		if cur.VerifiedAt != nil {
			t := cur.VerifiedAt.Time()
			verifiedAt = &t
		}
		verifiedBy := cur.VerifiedBy
		if cur.VerificationState != next.VerificationState {
			if next.VerificationState == model.VerificationVerified {
				verifiedAt = &now
				a := tr.Actor
				verifiedBy = &a
			} else {
				verifiedAt = nil
				verifiedBy = nil
			}
		}

		if _, err := tx.Exec(ctx,
			`UPDATE issue SET
				epic_id = $1, milestone_id = $2, title = $3, body = $4,
				type = $5, status = $6, priority = $7,
				recheck_condition = $8, decision_needed = $9, status_reason = $10,
				verification_state = $11, verification_note = $12, verified_at = $13, verified_by = $14,
				version = version + 1, updated_at = $15, closed_at = $16,
				status_changed_at = $17, status_changed_by = $18, updated_by = $19
			 WHERE id = $20`,
			next.EpicID, next.Milestone, next.Title, next.Body,
			string(next.Type), string(next.Status), string(next.Priority),
			next.RecheckCondition, next.DecisionNeeded, statusReason,
			string(next.VerificationState), next.VerificationNote, verifiedAt, verifiedBy,
			now, closedAt, statusChangedAt, statusChangedBy, tr.Actor, cur.ID,
		); err != nil {
			return err
		}
		if _, ok := changes["labels"]; ok {
			if _, err := tx.Exec(ctx, `DELETE FROM issue_label WHERE issue_id = $1`, cur.ID); err != nil {
				return err
			}
			for _, lbl := range next.Labels {
				if _, err := tx.Exec(ctx, `INSERT INTO issue_label (issue_id, label) VALUES ($1, $2)`, cur.ID, lbl); err != nil {
					return err
				}
			}
		}
		if _, ok := changes["refs"]; ok {
			if _, err := tx.Exec(ctx, `DELETE FROM issue_ref WHERE issue_id = $1`, cur.ID); err != nil {
				return err
			}
			for _, ref := range next.Refs {
				if _, err := tx.Exec(ctx, `INSERT INTO issue_ref (issue_id, ref) VALUES ($1, $2)`, cur.ID, ref); err != nil {
					return err
				}
			}
		}

		changesJSON, err := json.Marshal(changes)
		if err != nil {
			return err
		}
		var actID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO activity (issue_id, kind, actor_id, session_id, client_host, at, changes)
			 VALUES ($1, 'updated', $2, $3, $4, $5, $6::jsonb)
			 RETURNING id`,
			cur.ID, tr.Actor, tr.SessionID, tr.ClientHost, now, string(changesJSON),
		).Scan(&actID); err != nil {
			return err
		}
		pk := cur.Project
		if err := emitEventTx(ctx, tx, events, now, &curRow.projectID, &pk, model.EventIssueUpdated, cur.Key, tr.Actor, tr.SessionID, map[string]any{
			"activity_id": actID,
		}); err != nil {
			return err
		}
		jobID, err := enqueueIngestJobTx(ctx, tx, model.IngestSourceIssue, cur.ID, issueContentHash(next.Title, next.Body, next.Labels, next.Refs))
		if err != nil {
			return err
		}
		updated, err := scanIssueRow(tx.QueryRow(ctx, `SELECT `+issueCols+issueFrom+` WHERE i.id = $1`, cur.ID))
		if err != nil {
			return err
		}
		if err := hydrateIssueCollections(ctx, tx, []*IssueDetail{&updated.detail}); err != nil {
			return err
		}
		res = IssueWriteResult{Issue: updated.detail, IngestJobID: jobID}
		return nil
	})
	return res, err
}

// DeleteIssue marks an issue deleted with the required status reason and optional If-Match version.
func (s *Store) DeleteIssue(ctx context.Context, tr Trace, key string, expectedVersion *int64, reason string) (IssueDetail, error) {
	deleted := model.StatusDeleted
	patch := IssuePatch{Status: &deleted}
	if reason != "" {
		patch.StatusReason = &reason
	}
	res, err := s.UpdateIssue(ctx, tr, key, expectedVersion, patch)
	return res.Issue, err
}

// VerifyIssue transitions a pending issue to verified. Issues with
// verification_state "none" are rejected with a validation error.
func (s *Store) VerifyIssue(ctx context.Context, tr Trace, key string, expectedVersion *int64) (IssueDetail, error) {
	return s.setIssueVerificationState(ctx, tr, key, model.VerificationVerified, expectedVersion)
}

// UnverifyIssue transitions a verified issue back to pending. Issues with
// verification_state "none" are rejected with a validation error.
func (s *Store) UnverifyIssue(ctx context.Context, tr Trace, key string, expectedVersion *int64) (IssueDetail, error) {
	return s.setIssueVerificationState(ctx, tr, key, model.VerificationPending, expectedVersion)
}

func (s *Store) setIssueVerificationState(ctx context.Context, tr Trace, key string, target model.VerificationState, expectedVersion *int64) (IssueDetail, error) {
	var out IssueDetail
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		curRow, err := scanIssueRow(tx.QueryRow(ctx, `SELECT `+issueCols+issueFrom+` WHERE i.key = $1 FOR UPDATE OF i`, key))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := hydrateIssueCollections(ctx, tx, []*IssueDetail{&curRow.detail}); err != nil {
			return err
		}
		cur := curRow.detail
		if expectedVersion != nil && cur.Version != *expectedVersion {
			return ErrVersionMismatch
		}
		if cur.VerificationState == model.VerificationNone {
			return violation("verification_state", "invalid_value", "검증이 필요하지 않은 이슈입니다.")
		}
		if cur.VerificationState == target {
			out = cur
			return nil
		}
		var projectStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM project WHERE id = $1`, curRow.projectID).Scan(&projectStatus); err != nil {
			return err
		}
		if model.ProjectStatus(projectStatus) != model.ProjectActive {
			return ErrProjectNotActive
		}
		now := s.now()
		var verifiedAt *time.Time
		var verifiedBy *string
		if target == model.VerificationVerified {
			verifiedAt = &now
			actor := tr.Actor
			verifiedBy = &actor
		}
		if _, err := tx.Exec(ctx,
			`UPDATE issue
			 SET verification_state = $1, verified_at = $2, verified_by = $3,
			     version = version + 1, updated_at = $4, updated_by = $5
			 WHERE id = $6`,
			string(target), verifiedAt, verifiedBy, now, tr.Actor, cur.ID,
		); err != nil {
			return err
		}
		changes := map[string]model.Change{
			"verification_state": makeChange(cur.VerificationState, target),
		}
		changesJSON, err := json.Marshal(changes)
		if err != nil {
			return err
		}
		var actID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO activity (issue_id, kind, actor_id, session_id, client_host, at, changes)
			 VALUES ($1, 'updated', $2, $3, $4, $5, $6::jsonb)
			 RETURNING id`,
			cur.ID, tr.Actor, tr.SessionID, tr.ClientHost, now, string(changesJSON),
		).Scan(&actID); err != nil {
			return err
		}
		pk := cur.Project
		if err := emitEventTx(ctx, tx, events, now, &curRow.projectID, &pk, model.EventIssueUpdated, cur.Key, tr.Actor, tr.SessionID, map[string]any{
			"activity_id": actID,
		}); err != nil {
			return err
		}
		if _, err := enqueueIngestJobTx(ctx, tx, model.IngestSourceIssue, cur.ID, issueContentHash(cur.Title, cur.Body, cur.Labels, cur.Refs)); err != nil {
			return err
		}
		updated, err := scanIssueRow(tx.QueryRow(ctx, `SELECT `+issueCols+issueFrom+` WHERE i.id = $1`, cur.ID))
		if err != nil {
			return err
		}
		if err := hydrateIssueCollections(ctx, tx, []*IssueDetail{&updated.detail}); err != nil {
			return err
		}
		out = updated.detail
		return nil
	})
	return out, err
}

// AddComment appends a comment activity row, emits an event, and enqueues an ingest job.
func (s *Store) AddComment(ctx context.Context, tr Trace, issueKey string, in AddCommentInput) (ActivityWriteResult, error) {
	if err := model.ValidateComment(in.Body, in.Summary); err != nil {
		return ActivityWriteResult{}, err
	}
	var res ActivityWriteResult
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		var issueID, projectID int64
		var projectKey, projectStatus string
		err := tx.QueryRow(ctx,
			`SELECT i.id, i.project_id, p.key, p.status
			 FROM issue i JOIN project p ON p.id = i.project_id
			 WHERE i.key = $1`, issueKey,
		).Scan(&issueID, &projectID, &projectKey, &projectStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if model.ProjectStatus(projectStatus) != model.ProjectActive {
			return ErrProjectNotActive
		}
		now := s.now()
		var ck *string
		if in.CommentKind != nil {
			s := string(*in.CommentKind)
			ck = &s
		}
		var actID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO activity (issue_id, kind, actor_id, session_id, client_host, at, body, summary, comment_kind)
			 VALUES ($1, 'commented', $2, $3, $4, $5, $6, $7, $8)
			 RETURNING id`,
			issueID, tr.Actor, tr.SessionID, tr.ClientHost, now, in.Body, in.Summary, ck,
		).Scan(&actID); err != nil {
			return err
		}
		pk := projectKey
		if err := emitEventTx(ctx, tx, events, now, &projectID, &pk, model.EventIssueCommented, issueKey, tr.Actor, tr.SessionID, map[string]any{
			"activity_id": actID,
		}); err != nil {
			return err
		}
		jobID, err := enqueueIngestJobTx(ctx, tx, model.IngestSourceActivity, actID, sha256Hex(in.Body))
		if err != nil {
			return err
		}
		body := in.Body
		res = ActivityWriteResult{
			Activity: model.Activity{
				ID:          actID,
				Issue:       issueKey,
				Project:     projectKey,
				Kind:        model.ActivityCommented,
				Actor:       tr.Actor,
				SessionID:   tr.SessionID,
				ClientHost:  tr.ClientHost,
				At:          model.NewTime(now),
				Summary:     in.Summary,
				CommentKind: in.CommentKind,
				Body:        &body,
			},
			IngestJobID: jobID,
		}
		return nil
	})
	return res, err
}

// ListIssueActivities returns all activities of an issue ordered oldest first.
func (s *Store) ListIssueActivities(ctx context.Context, issueKey string, limit, offset int) ([]model.Activity, int, error) {
	var issueID int64
	var projectKey string
	err := s.pool.QueryRow(ctx,
		`SELECT i.id, p.key FROM issue i JOIN project p ON p.id = i.project_id WHERE i.key = $1`, issueKey,
	).Scan(&issueID, &projectKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM activity WHERE issue_id = $1`, issueID).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, kind, actor_id, session_id, client_host, at, changes, body, summary, comment_kind
		 FROM activity
		 WHERE issue_id = $1
		 ORDER BY id ASC
		 LIMIT $2 OFFSET $3`, issueID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.Activity{}
	for rows.Next() {
		var a model.Activity
		var kind string
		var at time.Time
		var changesRaw []byte
		var commentKind *string
		if err := rows.Scan(
			&a.ID, &kind, &a.Actor, &a.SessionID, &a.ClientHost, &at,
			&changesRaw, &a.Body, &a.Summary, &commentKind,
		); err != nil {
			return nil, 0, err
		}
		a.Issue = issueKey
		a.Project = projectKey
		a.Kind = model.ActivityKind(kind)
		a.At = model.NewTime(at)
		if len(changesRaw) > 0 {
			_ = json.Unmarshal(changesRaw, &a.Changes)
		}
		if commentKind != nil {
			ck := model.CommentKind(*commentKind)
			a.CommentKind = &ck
		}
		items = append(items, a)
	}
	return items, total, rows.Err()
}
