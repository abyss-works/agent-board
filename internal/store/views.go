package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

// BoardParams selects the issues shown on the board.
type BoardParams struct {
	Projects   []string
	ClosedDays int // 0 means no window
	Limit      int // per column
}

// BoardColumn is one status column.
type BoardColumn struct {
	Status model.IssueStatus    `json:"status"`
	Total  int                  `json:"total"`
	Issues []model.IssueSummary `json:"issues"`
}

// Board groups issues by status.
type Board struct {
	Project *string       `json:"project"`
	Columns []BoardColumn `json:"columns"`
}

// Board returns one column per status in fixed order. Closed columns only
// hold issues closed within the last ClosedDays days.
func (s *Store) Board(ctx context.Context, p BoardParams) (*Board, error) {
	b := &Board{Columns: make([]BoardColumn, 0, len(model.IssueStatuses))}
	if len(p.Projects) > 0 {
		joined := strings.Join(p.Projects, ",")
		b.Project = &joined
	}
	var closedSince *model.Time
	if p.ClosedDays > 0 {
		t := model.NewTime(s.now().Add(-time.Duration(p.ClosedDays) * 24 * time.Hour))
		closedSince = &t
	}
	for _, st := range model.IssueStatuses {
		f := IssueFilter{Projects: p.Projects, Statuses: []model.IssueStatus{st}, Limit: p.Limit}
		if st.Closed() {
			f.closedSince = closedSince
		}
		items, total, err := s.ListIssues(ctx, f)
		if err != nil {
			return nil, err
		}
		b.Columns = append(b.Columns, BoardColumn{Status: st, Total: total, Issues: items})
	}
	return b, nil
}

// QueueFilter selects issues for the priority queue.
type QueueFilter struct {
	Projects    []string
	Statuses    []model.IssueStatus
	OnlyBlocked bool // only issues with at least one open dependency
	OnlyReady   bool // only issues whose dependencies are all closed
	Limit       int
	Offset      int
}

const openDepsExpr = `(select count(*) from issue_dependency d join issue di on di.id = d.depends_on_id
	where d.issue_id = i.id and di.status not in ('done','cancelled','deleted'))`

// Queue orders issues by priority, readiness (ready first) and creation.
func (s *Store) Queue(ctx context.Context, f QueueFilter) ([]model.QueueItem, int, error) {
	var where []string
	var args []any
	if len(f.Projects) > 0 {
		where = append(where, "p.key in ("+placeholders(len(f.Projects))+")")
		for _, v := range f.Projects {
			args = append(args, v)
		}
	}
	if len(f.Statuses) > 0 {
		where = append(where, "i.status in ("+placeholders(len(f.Statuses))+")")
		for _, v := range f.Statuses {
			args = append(args, string(v))
		}
	}
	if f.OnlyBlocked {
		where = append(where, openDepsExpr+" > 0")
	}
	if f.OnlyReady {
		where = append(where, openDepsExpr+" = 0")
	}
	cond := ""
	if len(where) > 0 {
		cond = " where " + strings.Join(where, " and ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*)`+issueFrom+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `select `+issueCols(false)+`, `+openDepsExpr+` as open_deps`+issueFrom+cond+
		` order by i.priority, (open_deps > 0), i.created_at, i.id limit ? offset ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var list []*issueRow
	var openDeps []int
	for rows.Next() {
		r, n, err := scanIssueExtraInt(rows)
		if err != nil {
			return nil, 0, err
		}
		list = append(list, r)
		openDeps = append(openDeps, n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := loadLinks(ctx, s.db, list); err != nil {
		return nil, 0, err
	}
	blocked, err := s.openDependencies(ctx, list)
	if err != nil {
		return nil, 0, err
	}
	items := make([]model.QueueItem, len(list))
	for n, r := range list {
		items[n] = model.QueueItem{IssueSummary: r.Summary(), Ready: openDeps[n] == 0, BlockedBy: blocked[r.id]}
		if items[n].BlockedBy == nil {
			items[n].BlockedBy = []string{}
		}
	}
	return items, total, nil
}

// openDependencies maps each issue id to the keys of its still-open deps.
func (s *Store) openDependencies(ctx context.Context, rows []*issueRow) (map[int64][]string, error) {
	out := map[int64][]string{}
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]any, len(rows))
	for n, r := range rows {
		ids[n] = r.id
	}
	rs, err := s.db.QueryContext(ctx, `select d.issue_id, di.key from issue_dependency d join issue di on di.id = d.depends_on_id
		where d.issue_id in (`+placeholders(len(ids))+`) and di.status not in ('done','cancelled','deleted') order by di.key`, ids...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	for rs.Next() {
		var id int64
		var key string
		if err := rs.Scan(&id, &key); err != nil {
			return nil, err
		}
		out[id] = append(out[id], key)
	}
	return out, rs.Err()
}

// scanIssueExtraInt scans an issue row followed by one integer column.
func scanIssueExtraInt(rows *sql.Rows) (*issueRow, int, error) {
	var r issueRow
	var parentKey, recheck, closedAt sql.NullString
	var created, updated, statusChanged string
	var extra int
	if err := rows.Scan(&r.id, &r.projectID, &r.Project, &r.projectStatus, &r.Number, &r.Key, &r.Title, &r.Body, &r.Type, &r.Status, &r.Priority,
		&r.parentID, &parentKey, &recheck, &r.Version, &created, &updated, &closedAt,
		&statusChanged, &r.StatusChangedBy, &r.CreatedBy, &r.UpdatedBy, &extra); err != nil {
		return nil, 0, err
	}
	if err := r.finishScan(parentKey, recheck, closedAt, created, updated, statusChanged); err != nil {
		return nil, 0, err
	}
	return &r, extra, nil
}

// LabelCount is a label and the number of open issues carrying it.
type LabelCount struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// Labels counts labels over open issues, most used first.
func (s *Store) Labels(ctx context.Context, projects []string) ([]LabelCount, error) {
	cond := " where i.status in ('backlog','todo','doing','review')"
	var args []any
	if len(projects) > 0 {
		cond += " and p.key in (" + placeholders(len(projects)) + ")"
		for _, v := range projects {
			args = append(args, v)
		}
	}
	rows, err := s.db.QueryContext(ctx, `select l.label, count(*) from issue_label l join issue i on i.id = l.issue_id join project p on p.id = i.project_id`+
		cond+` group by l.label order by count(*) desc, l.label`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LabelCount{}
	for rows.Next() {
		var lc LabelCount
		if err := rows.Scan(&lc.Label, &lc.Count); err != nil {
			return nil, err
		}
		out = append(out, lc)
	}
	return out, rows.Err()
}

// Section is a capped list with its full count.
type Section[T any] struct {
	Total int `json:"total"`
	Items []T `json:"items"`
}

// OverviewCounts summarizes a project's issues.
type OverviewCounts struct {
	Total      int                       `json:"total"`
	Open       int                       `json:"open"`
	ByStatus   map[model.IssueStatus]int `json:"by_status"`
	ByType     map[model.IssueType]int   `json:"by_type"`
	ByPriority map[model.Priority]int    `json:"by_priority"`
}

// Overview is the project status view.
type Overview struct {
	Project        model.Project               `json:"project"`
	GeneratedAt    model.Time                  `json:"generated_at"`
	Counts         OverviewCounts              `json:"counts"`
	Doing          Section[model.IssueSummary] `json:"doing"`
	Review         Section[model.IssueSummary] `json:"review"`
	Blocked        Section[model.QueueItem]    `json:"blocked"`
	Decisions      Section[model.IssueSummary] `json:"decisions"`
	Next           Section[model.QueueItem]    `json:"next"`
	RecentActivity Section[model.Activity]     `json:"recent_activity"`
}

// Overview builds the project status view. Each list holds at most limit
// items and reports its full count.
func (s *Store) Overview(ctx context.Context, key string, limit int) (*Overview, error) {
	project, err := s.GetProject(ctx, key)
	if err != nil {
		return nil, err
	}
	o := &Overview{Project: *project, GeneratedAt: s.stamp()}
	o.Counts.ByStatus = map[model.IssueStatus]int{}
	for _, st := range model.IssueStatuses {
		o.Counts.ByStatus[st] = 0
	}
	rows, err := s.db.QueryContext(ctx, `select i.status, count(*) from issue i join project p on p.id = i.project_id where p.key = ? group by i.status`, key)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			rows.Close()
			return nil, err
		}
		o.Counts.ByStatus[model.IssueStatus(st)] = n
		o.Counts.Total += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	o.Counts.ByType = map[model.IssueType]int{}
	for _, t := range model.IssueTypes {
		o.Counts.ByType[t] = 0
	}
	o.Counts.ByPriority = map[model.Priority]int{}
	for _, p := range model.Priorities {
		o.Counts.ByPriority[p] = 0
	}
	rows, err = s.db.QueryContext(ctx, `select i.type, i.priority, count(*) from issue i join project p on p.id = i.project_id
		where p.key = ? and i.status in ('backlog','todo','doing','review') group by i.type, i.priority`, key)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var typ, prio string
		var n int
		if err := rows.Scan(&typ, &prio, &n); err != nil {
			rows.Close()
			return nil, err
		}
		o.Counts.ByType[model.IssueType(typ)] += n
		o.Counts.ByPriority[model.Priority(prio)] += n
		o.Counts.Open += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	projects := []string{key}
	byStatus := func(st model.IssueStatus) (Section[model.IssueSummary], error) {
		items, total, err := s.ListIssues(ctx, IssueFilter{Projects: projects, Statuses: []model.IssueStatus{st}, Limit: limit})
		return Section[model.IssueSummary]{Total: total, Items: items}, err
	}
	if o.Doing, err = byStatus(model.StatusDoing); err != nil {
		return nil, err
	}
	if o.Review, err = byStatus(model.StatusReview); err != nil {
		return nil, err
	}
	items, total, err := s.Queue(ctx, QueueFilter{Projects: projects, Statuses: model.OpenStatuses, OnlyBlocked: true, Limit: limit})
	if err != nil {
		return nil, err
	}
	o.Blocked = Section[model.QueueItem]{Total: total, Items: items}
	decisions, total, err := s.ListIssues(ctx, IssueFilter{Projects: projects, Statuses: model.OpenStatuses, Types: []model.IssueType{model.TypeDecision}, Limit: limit})
	if err != nil {
		return nil, err
	}
	o.Decisions = Section[model.IssueSummary]{Total: total, Items: decisions}
	next, total, err := s.Queue(ctx, QueueFilter{Projects: projects, Statuses: []model.IssueStatus{model.StatusTodo}, OnlyReady: true, Limit: limit})
	if err != nil {
		return nil, err
	}
	o.Next = Section[model.QueueItem]{Total: total, Items: next}
	acts, total, err := s.ListActivity(ctx, ActivityFilter{Projects: projects, Limit: limit})
	if err != nil {
		return nil, err
	}
	o.RecentActivity = Section[model.Activity]{Total: total, Items: acts}
	return o, nil
}
