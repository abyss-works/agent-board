package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// Optional is a tri-state patch value: absent, set to null, or set to a value.
type Optional[T any] struct {
	Set   bool
	Value *T
}

// IssueInput is the server-side shape of a create request. Zero enum values
// take the defaults.
type IssueInput struct {
	Project          string
	Title            string
	Body             string
	Type             model.IssueType
	Status           model.IssueStatus
	Priority         model.Priority
	Labels           []string
	Parent           *string
	DependsOn        []string
	Refs             []string
	RecheckCondition *string
	Topic            *int64
	Milestone        *int64
}

// IssuePatch is a JSON merge patch. Nil pointers are unchanged; Optional
// fields distinguish "clear" from "keep".
type IssuePatch struct {
	Title            *string
	Body             *string
	Type             *model.IssueType
	Status           *model.IssueStatus
	Priority         *model.Priority
	Labels           *[]string
	Parent           Optional[string]
	DependsOn        *[]string
	Refs             *[]string
	RecheckCondition Optional[string]
	Topic            Optional[int64]
	Milestone        Optional[int64]
	StatusReason     *string // only valid together with a status change
}

// IssueFilter selects, searches and orders issues.
type IssueFilter struct {
	Projects        []string
	Statuses        []model.IssueStatus
	Types           []model.IssueType
	Priorities      []model.Priority
	Labels          []string // AND
	Parent          *string  // "none" selects issues without a parent
	CreatedBy       []string
	UpdatedBy       []string
	StatusChangedBy []string
	Acknowledged    *bool // true: acknowledged by the user, false: not yet
	Topics          []int64
	TopicNone       bool // also select issues without a topic
	Milestones      []int64
	MilestoneNone   bool // also select issues without a milestone
	Query           string
	Sort            []Sort // priority, created_at, updated_at, key, relevance
	Limit           int
	Offset          int

	closedSince *model.Time // board: only issues closed at or after this time
}

// issueRow is an issue with the internal ids needed inside transactions.
type issueRow struct {
	model.Issue
	id            int64
	projectID     int64
	parentID      sql.NullInt64
	projectStatus model.ProjectStatus
}

// issueJoins attach the project, the parent issue, the topic with its
// top-level topic and the milestone to issue rows aliased i.
const issueJoins = ` join project p on p.id = i.project_id left join issue par on par.id = i.parent_id
	left join topic tp on tp.id = i.topic_id left join topic tpp on tpp.id = tp.parent_id
	left join milestone ms on ms.id = i.milestone_id`

const issueFrom = ` from issue i` + issueJoins

func issueCols(withBody bool) string {
	body := "i.body"
	if !withBody {
		body = "''"
	}
	return `i.id, i.project_id, p.key, p.status, i.number, i.key, i.title, ` + body + `, i.type, i.status, i.priority,
		i.parent_id, par.key, i.recheck_condition, i.topic_id,
		case when tp.id is null then null when tpp.id is null then tp.name else tpp.name || '/' || tp.name end,
		i.milestone_id, ms.name,
		i.status_reason, i.acknowledged_at, i.acknowledged_by, i.version, i.created_at, i.updated_at, i.closed_at,
		i.status_changed_at, i.status_changed_by, i.created_by, i.updated_by`
}

type rowScanner interface{ Scan(...any) error }

// issueScan holds the columns of issueCols that need conversion after the
// scan.
type issueScan struct {
	parentKey, recheck, reason, closedAt sql.NullString
	topicPath, ackAt, ackBy              sql.NullString
	milestoneName                        sql.NullString
	topicID, milestoneID                 sql.NullInt64
	created, updated, statusChanged      string
}

// scanIssue scans the columns of issueCols followed by the extra
// destinations of columns a query appended.
func scanIssue(sc rowScanner, extra ...any) (*issueRow, error) {
	var r issueRow
	var raw issueScan
	dest := []any{&r.id, &r.projectID, &r.Project, &r.projectStatus, &r.Number, &r.Key, &r.Title, &r.Body, &r.Type, &r.Status, &r.Priority,
		&r.parentID, &raw.parentKey, &raw.recheck, &raw.topicID, &raw.topicPath, &raw.milestoneID, &raw.milestoneName,
		&raw.reason, &raw.ackAt, &raw.ackBy, &r.Version, &raw.created, &raw.updated, &raw.closedAt,
		&raw.statusChanged, &r.StatusChangedBy, &r.CreatedBy, &r.UpdatedBy}
	if err := sc.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	if err := r.finishScan(raw); err != nil {
		return nil, err
	}
	return &r, nil
}

// finishScan converts the nullable and timestamp columns of a scanned row.
func (r *issueRow) finishScan(raw issueScan) error {
	if raw.parentKey.Valid {
		r.Parent = &raw.parentKey.String
	}
	if raw.recheck.Valid {
		r.RecheckCondition = &raw.recheck.String
	}
	if raw.topicID.Valid {
		r.Topic = &raw.topicID.Int64
	}
	if raw.topicPath.Valid {
		r.TopicPath = &raw.topicPath.String
	}
	if raw.milestoneID.Valid {
		r.Milestone = &raw.milestoneID.Int64
	}
	if raw.milestoneName.Valid {
		r.MilestoneName = &raw.milestoneName.String
	}
	if raw.reason.Valid {
		r.StatusReason = &raw.reason.String
	}
	if raw.ackBy.Valid {
		r.AcknowledgedBy = &raw.ackBy.String
	}
	if raw.ackAt.Valid {
		t, err := model.ParseTime(raw.ackAt.String)
		if err != nil {
			return err
		}
		r.AcknowledgedAt = &t
	}
	var err error
	if r.CreatedAt, err = model.ParseTime(raw.created); err != nil {
		return err
	}
	if r.UpdatedAt, err = model.ParseTime(raw.updated); err != nil {
		return err
	}
	if r.StatusChangedAt, err = model.ParseTime(raw.statusChanged); err != nil {
		return err
	}
	if raw.closedAt.Valid {
		t, err := model.ParseTime(raw.closedAt.String)
		if err != nil {
			return err
		}
		r.ClosedAt = &t
	}
	r.Labels, r.DependsOn, r.Refs = []string{}, []string{}, []string{}
	return nil
}

func getIssueRow(ctx context.Context, q querier, key string) (*issueRow, error) {
	r, err := scanIssue(q.QueryRowContext(ctx, `select `+issueCols(true)+issueFrom+` where i.key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := loadLinks(ctx, q, []*issueRow{r}); err != nil {
		return nil, err
	}
	return r, nil
}

// loadLinks fills labels, depends_on and refs for the given rows.
func loadLinks(ctx context.Context, q querier, rows []*issueRow) error {
	if len(rows) == 0 {
		return nil
	}
	byID := make(map[int64]*issueRow, len(rows))
	ids := make([]any, 0, len(rows))
	for _, r := range rows {
		byID[r.id] = r
		ids = append(ids, r.id)
	}
	ph := placeholders(len(ids))
	fill := func(query string, assign func(r *issueRow, v string)) error {
		rs, err := q.QueryContext(ctx, query, ids...)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var id int64
			var v string
			if err := rs.Scan(&id, &v); err != nil {
				return err
			}
			assign(byID[id], v)
		}
		return rs.Err()
	}
	if err := fill(`select issue_id, label from issue_label where issue_id in (`+ph+`) order by label`,
		func(r *issueRow, v string) { r.Labels = append(r.Labels, v) }); err != nil {
		return err
	}
	if err := fill(`select d.issue_id, t.key from issue_dependency d join issue t on t.id = d.depends_on_id where d.issue_id in (`+ph+`) order by t.key`,
		func(r *issueRow, v string) { r.DependsOn = append(r.DependsOn, v) }); err != nil {
		return err
	}
	return fill(`select issue_id, ref from issue_ref where issue_id in (`+ph+`) order by ref`,
		func(r *issueRow, v string) { r.Refs = append(r.Refs, v) })
}

// GetIssue returns an issue by key regardless of its status.
func (s *Store) GetIssue(ctx context.Context, key string) (*model.Issue, error) {
	r, err := getIssueRow(ctx, s.db, key)
	if err != nil {
		return nil, err
	}
	return &r.Issue, nil
}

// CreateIssue validates, issues the next number in the project and inserts
// the issue with its created activity record, all in one transaction.
func (s *Store) CreateIssue(ctx context.Context, tr model.Trace, in IssueInput) (*model.Issue, error) {
	kind, err := actorKind(ctx, s.db, tr.Actor)
	if err != nil {
		return nil, err
	}
	now := s.stamp()
	issue := model.Issue{
		Project: in.Project, Title: in.Title, Body: in.Body,
		Type: in.Type, Status: in.Status, Priority: in.Priority,
		Labels: in.Labels, Parent: in.Parent, DependsOn: in.DependsOn, Refs: in.Refs, RecheckCondition: in.RecheckCondition,
		Topic: in.Topic, Milestone: in.Milestone, Version: 1, CreatedAt: now, UpdatedAt: now, StatusChangedAt: now,
		StatusChangedBy: tr.Actor, CreatedBy: tr.Actor, UpdatedBy: tr.Actor,
	}
	if issue.Type == "" {
		issue.Type = model.DefaultIssueType
	}
	if issue.Status == "" {
		issue.Status = model.DefaultIssueStatus
	}
	if issue.Priority == "" {
		issue.Priority = model.DefaultPriority
	}
	model.NormalizeIssue(&issue)
	checks := []error{model.ValidateIssue(&issue)}
	if kind == model.ActorAgent {
		// An agent writes every body to its type's template; the user writes freely.
		checks = append(checks, model.CheckTemplate(issue.Type, issue.Body))
	}
	if err := model.MergeValidation(checks...); err != nil {
		return nil, err
	}
	if issue.Status.Closed() {
		issue.ClosedAt = &now
	}

	var key string
	err = s.write(ctx, func(tx *sql.Tx) error {
		var projectID int64
		var status string
		err := tx.QueryRowContext(ctx, `select id, status from project where key = ?`, in.Project).Scan(&projectID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if model.ProjectStatus(status) != model.ProjectActive {
			return ErrProjectNotActive
		}
		parentID, depIDs, err := resolveLinks(ctx, tx, issue.Parent, issue.DependsOn)
		if err != nil {
			return err
		}
		var refErrs []error
		if issue.Topic != nil {
			refErrs = append(refErrs, checkIssueTopic(ctx, tx, projectID, *issue.Topic))
		}
		if issue.Milestone != nil {
			refErrs = append(refErrs, checkIssueMilestone(ctx, tx, projectID, *issue.Milestone))
		}
		if err := model.MergeValidation(refErrs...); err != nil {
			return err
		}
		var number int64
		if err := tx.QueryRowContext(ctx, `update project set next_number = next_number + 1 where id = ? returning next_number - 1`, projectID).Scan(&number); err != nil {
			return err
		}
		key = model.IssueKey(in.Project, number)
		// What the user creates the user already knows; what an agent creates waits for the user.
		var ackAt, ackBy any
		if kind == model.ActorUser {
			ackAt, ackBy = now.String(), tr.Actor
		}
		res, err := tx.ExecContext(ctx, `insert into issue(project_id, number, key, title, body, type, status, priority, parent_id, recheck_condition, topic_id, milestone_id,
			acknowledged_at, acknowledged_by, version, created_at, updated_at, closed_at, status_changed_at, status_changed_by, created_by, updated_by)
			values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?)`,
			projectID, number, key, issue.Title, issue.Body, string(issue.Type), string(issue.Status), string(issue.Priority), parentID, issue.RecheckCondition, issue.Topic, issue.Milestone,
			ackAt, ackBy, now.String(), now.String(), timePtr(issue.ClosedAt), now.String(), tr.Actor, tr.Actor, tr.Actor)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := writeLinks(ctx, tx, id, issue.Labels, depIDs, issue.Refs); err != nil {
			return err
		}
		changes := map[string]model.Change{}
		for f, v := range fieldValues(&issue) {
			b, _ := json.Marshal(v)
			changes[f] = model.Change{To: b}
		}
		_, err = insertActivity(ctx, tx, id, model.ActivityCreated, tr, now, changes, nil, nil)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetIssue(ctx, key)
}

// UpdateIssue applies a merge patch. It returns the resulting issue and
// whether anything changed; an unchanged issue gets no activity record and
// keeps its version. ifMatch, when non-nil, must equal the current version.
func (s *Store) UpdateIssue(ctx context.Context, tr model.Trace, key string, patch IssuePatch, ifMatch *int64) (*model.Issue, bool, error) {
	kind, err := actorKind(ctx, s.db, tr.Actor)
	if err != nil {
		return nil, false, err
	}
	changed := false
	err = s.write(ctx, func(tx *sql.Tx) error {
		cur, err := getIssueRow(ctx, tx, key)
		if err != nil {
			return err
		}
		if ifMatch != nil && *ifMatch != cur.Version {
			return ErrVersionMismatch
		}
		next := cur.Issue
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
		if patch.Labels != nil {
			next.Labels = *patch.Labels
		}
		if patch.Parent.Set {
			next.Parent = patch.Parent.Value
		}
		if patch.DependsOn != nil {
			next.DependsOn = *patch.DependsOn
		}
		if patch.Refs != nil {
			next.Refs = *patch.Refs
		}
		if patch.RecheckCondition.Set {
			next.RecheckCondition = patch.RecheckCondition.Value
		}
		if patch.Topic.Set {
			next.Topic = patch.Topic.Value
		}
		if patch.Milestone.Set {
			next.Milestone = patch.Milestone.Value
		}
		model.NormalizeIssue(&next)
		reason, reasonErr := model.ResolveStatusReason(cur.Status, next.Status, patch.StatusReason)
		checks := []error{model.ValidateIssue(&next), reasonErr}
		if kind == model.ActorAgent && (next.Body != cur.Body || next.Type != cur.Type) {
			// A new body or a new type puts the body to the template of the type it ends up with.
			checks = append(checks, model.CheckTemplate(next.Type, next.Body))
		}
		if err := model.MergeValidation(checks...); err != nil {
			return err
		}
		statusChanged := next.Status != cur.Status
		if statusChanged {
			next.StatusReason = reason
		}
		changes := diff(&cur.Issue, &next)
		if len(changes) == 0 {
			return nil
		}
		if statusChanged {
			// Always listed with the status, even when the text repeats.
			changes["status_reason"] = model.Change{From: rawJSON(cur.StatusReason), To: rawJSON(next.StatusReason)}
		}
		if cur.projectStatus != model.ProjectActive {
			return ErrProjectNotActive
		}
		parentID, depIDs, err := resolveLinks(ctx, tx, next.Parent, next.DependsOn)
		if err != nil {
			return err
		}
		// A topic or milestone deleted after the issue got it stays; only a changed one is checked.
		var refErrs []error
		if next.Topic != nil && (cur.Topic == nil || *cur.Topic != *next.Topic) {
			refErrs = append(refErrs, checkIssueTopic(ctx, tx, cur.projectID, *next.Topic))
		}
		if next.Milestone != nil && (cur.Milestone == nil || *cur.Milestone != *next.Milestone) {
			refErrs = append(refErrs, checkIssueMilestone(ctx, tx, cur.projectID, *next.Milestone))
		}
		if err := model.MergeValidation(refErrs...); err != nil {
			return err
		}
		if err := checkCycles(ctx, tx, cur.id, parentID, depIDs); err != nil {
			return err
		}
		now := s.stamp()
		next.Version = cur.Version + 1
		next.UpdatedAt = now
		next.UpdatedBy = tr.Actor
		if statusChanged {
			next.StatusChangedAt = now
			next.StatusChangedBy = tr.Actor
			switch {
			case !next.Status.Closed():
				next.ClosedAt = nil
			case !cur.Status.Closed():
				next.ClosedAt = &now
			}
		}
		if _, err := tx.ExecContext(ctx, `update issue set title = ?, body = ?, type = ?, status = ?, priority = ?, parent_id = ?, recheck_condition = ?,
			topic_id = ?, milestone_id = ?, status_reason = ?, version = ?, updated_at = ?, updated_by = ?, closed_at = ?, status_changed_at = ?, status_changed_by = ? where id = ?`,
			next.Title, next.Body, string(next.Type), string(next.Status), string(next.Priority), parentID, next.RecheckCondition,
			next.Topic, next.Milestone, next.StatusReason, next.Version, now.String(), tr.Actor, timePtr(next.ClosedAt), next.StatusChangedAt.String(), next.StatusChangedBy, cur.id); err != nil {
			return err
		}
		for _, table := range []string{"issue_label", "issue_dependency", "issue_ref"} {
			if _, err := tx.ExecContext(ctx, `delete from `+table+` where issue_id = ?`, cur.id); err != nil {
				return err
			}
		}
		if err := writeLinks(ctx, tx, cur.id, next.Labels, depIDs, next.Refs); err != nil {
			return err
		}
		if _, err := insertActivity(ctx, tx, cur.id, model.ActivityUpdated, tr, now, changes, nil, nil); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	issue, err := s.GetIssue(ctx, key)
	return issue, changed, err
}

func timePtr(t *model.Time) any {
	if t == nil {
		return nil
	}
	return t.String()
}

// rawJSON encodes a value for an activity change.
func rawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// fieldValues lists the user-facing fields recorded in activity changes.
func fieldValues(i *model.Issue) map[string]any {
	return map[string]any{
		"title": i.Title, "body": i.Body, "type": i.Type, "status": i.Status, "priority": i.Priority,
		"labels": i.Labels, "parent": i.Parent, "depends_on": i.DependsOn, "refs": i.Refs, "recheck_condition": i.RecheckCondition, "topic": i.Topic,
		"milestone": i.Milestone,
	}
}

func diff(old, next *model.Issue) map[string]model.Change {
	ov, nv := fieldValues(old), fieldValues(next)
	changes := map[string]model.Change{}
	for f, o := range ov {
		n := nv[f]
		if reflect.DeepEqual(o, n) {
			continue
		}
		ob, _ := json.Marshal(o)
		nb, _ := json.Marshal(n)
		if string(ob) == string(nb) {
			continue
		}
		changes[f] = model.Change{From: ob, To: nb}
	}
	return changes
}

// resolveLinks maps the parent key and dependency keys to ids.
func resolveLinks(ctx context.Context, tx *sql.Tx, parent *string, deps []string) (any, []int64, error) {
	lookup := func(key string) (int64, bool, error) {
		var id int64
		err := tx.QueryRowContext(ctx, `select id from issue where key = ?`, key).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return id, err == nil, err
	}
	var parentID any
	if parent != nil {
		id, ok, err := lookup(*parent)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, &RefError{Field: "parent", Key: *parent}
		}
		parentID = id
	}
	depIDs := make([]int64, 0, len(deps))
	for n, k := range deps {
		id, ok, err := lookup(k)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, &RefError{Field: fmt.Sprintf("depends_on[%d]", n), Key: k}
		}
		depIDs = append(depIDs, id)
	}
	return parentID, depIDs, nil
}

func writeLinks(ctx context.Context, tx *sql.Tx, id int64, labels []string, depIDs []int64, refs []string) error {
	for _, l := range labels {
		if _, err := tx.ExecContext(ctx, `insert into issue_label(issue_id, label) values (?, ?)`, id, l); err != nil {
			return err
		}
	}
	for _, d := range depIDs {
		if _, err := tx.ExecContext(ctx, `insert into issue_dependency(issue_id, depends_on_id) values (?, ?)`, id, d); err != nil {
			return err
		}
	}
	for _, r := range refs {
		if _, err := tx.ExecContext(ctx, `insert into issue_ref(issue_id, ref) values (?, ?)`, id, r); err != nil {
			return err
		}
	}
	return nil
}

// checkCycles rejects a parent chain or dependency graph that would lead
// back to the issue itself.
func checkCycles(ctx context.Context, tx *sql.Tx, id int64, parentID any, depIDs []int64) error {
	if parentID != nil {
		var n int
		if err := tx.QueryRowContext(ctx, `with recursive anc(id) as (
			select ? union select i.parent_id from issue i join anc on i.id = anc.id where i.parent_id is not null
		) select count(*) from anc where id = ?`, parentID, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrCycle
		}
	}
	if len(depIDs) > 0 {
		args := make([]any, 0, len(depIDs)+1)
		for _, d := range depIDs {
			if d == id {
				return ErrCycle
			}
			args = append(args, d)
		}
		args = append(args, id)
		var n int
		if err := tx.QueryRowContext(ctx, `with recursive r(id) as (
			select depends_on_id from issue_dependency where issue_id in (`+placeholders(len(depIDs))+`)
			union select d.depends_on_id from issue_dependency d join r on d.issue_id = r.id
		) select count(*) from r where id = ?`, args...).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrCycle
		}
	}
	return nil
}

var issueSortCols = map[string][]string{
	"priority":   {"i.priority"},
	"created_at": {"i.created_at"},
	"updated_at": {"i.updated_at"},
	"key":        {"p.key", "i.number"},
	"relevance":  {"issue_fts.rank"},
}

// ListIssues returns a page of issue summaries and the total count. With a
// Query it searches the FTS index and attaches snippets.
func (s *Store) ListIssues(ctx context.Context, f IssueFilter) ([]model.IssueSummary, int, error) {
	match, err := BuildMatch(f.Query)
	if err != nil {
		return nil, 0, err
	}
	var where []string
	var args []any
	from := issueFrom
	cols := issueCols(false)
	if match != "" {
		from = ` from issue_fts join issue i on i.id = issue_fts.rowid` + issueJoins
		cols += `, snippet(issue_fts, -1, '[', ']', '...', 16)`
		where = append(where, "issue_fts match ?")
		args = append(args, match)
	}
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
	in("i.status", enumStrings(f.Statuses))
	in("i.type", enumStrings(f.Types))
	in("i.priority", enumStrings(f.Priorities))
	in("i.created_by", f.CreatedBy)
	in("i.updated_by", f.UpdatedBy)
	in("i.status_changed_by", f.StatusChangedBy)
	for _, l := range f.Labels {
		where = append(where, "exists (select 1 from issue_label l where l.issue_id = i.id and l.label = ?)")
		args = append(args, l)
	}
	if f.Parent != nil {
		if *f.Parent == "none" {
			where = append(where, "i.parent_id is null")
		} else {
			where = append(where, "par.key = ?")
			args = append(args, *f.Parent)
		}
	}
	if clause, topicArgs := topicFilterClause(f.Topics, f.TopicNone); clause != "" {
		where = append(where, clause)
		args = append(args, topicArgs...)
	}
	if clause, milestoneArgs := milestoneFilterClause(f.Milestones, f.MilestoneNone); clause != "" {
		where = append(where, clause)
		args = append(args, milestoneArgs...)
	}
	if f.Acknowledged != nil {
		if *f.Acknowledged {
			where = append(where, "i.acknowledged_at is not null")
		} else {
			where = append(where, "i.acknowledged_at is null")
		}
	}
	if f.closedSince != nil {
		where = append(where, "i.closed_at >= ?")
		args = append(args, f.closedSince.String())
	}
	cond := ""
	if len(where) > 0 {
		cond = " where " + strings.Join(where, " and ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*)`+from+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	def := "i.priority, i.created_at"
	if match != "" {
		def = "issue_fts.rank"
	}
	order, err := orderBy(f.Sort, issueSortCols, def, "i.id")
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `select `+cols+from+cond+order+` limit ? offset ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var list []*issueRow
	var snippets []string
	for rows.Next() {
		var r *issueRow
		if match != "" {
			r, err = scanIssueWithSnippet(rows, &snippets)
		} else {
			r, err = scanIssue(rows)
		}
		if err != nil {
			return nil, 0, err
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := loadLinks(ctx, s.db, list); err != nil {
		return nil, 0, err
	}
	items := make([]model.IssueSummary, len(list))
	for n, r := range list {
		items[n] = r.Summary()
		if match != "" {
			sn := snippets[n]
			items[n].Snippet = &sn
		}
	}
	return items, total, nil
}

// scanIssueWithSnippet scans an issue row followed by a snippet column.
func scanIssueWithSnippet(rows *sql.Rows, snippets *[]string) (*issueRow, error) {
	var snippet string
	r, err := scanIssue(rows, &snippet)
	if err != nil {
		return nil, err
	}
	*snippets = append(*snippets, snippet)
	return r, nil
}

func enumStrings[T ~string](vals []T) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
}
