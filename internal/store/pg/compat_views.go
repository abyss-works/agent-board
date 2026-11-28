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
	"github.com/wsk0715/agent-board/internal/store"
)

// CompatQueue returns the priority queue: issues without the external and user-task
// labels, ordered by priority, readiness, the milestone, the number of issues they
// block and creation.
func (s *Store) CompatQueue(ctx context.Context, f CompatIssueFilter) ([]model.QueueItem, int, error) {
	f.ExcludeLabels = []string{model.ExternalLabel, model.UserTaskLabel}
	var args []any
	where := s.compatWhere(f, &args)
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*)`+issueFrom+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT ` + issueCols + `, ` + lastActivityExpr + `, ` + openDepsExpr + issueFrom + cond +
		` ORDER BY i.priority, (` + openDepsExpr + ` > 0), (m.status IS DISTINCT FROM 'active'), i.milestone_id IS NULL, i.milestone_id, ` +
		blocksExpr + ` DESC, i.created_at, i.id`
	args = append(args, f.Limit, f.Offset)
	q += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	var details []IssueDetail
	var last []time.Time
	var deps []int
	for rows.Next() {
		var la time.Time
		var od int
		r, err := scanIssueRowExtra(rows, &la, &od)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		details = append(details, r.detail)
		last = append(last, la)
		deps = append(deps, od)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	ptrs := make([]*IssueDetail, len(details))
	for i := range details {
		ptrs[i] = &details[i]
	}
	if err := hydrateIssueCollections(ctx, s.pool, ptrs); err != nil {
		return nil, 0, err
	}
	blocked, err := s.openDependencyKeys(ctx, ptrs)
	if err != nil {
		return nil, 0, err
	}
	now := model.NewTime(s.now())
	items := make([]model.QueueItem, len(details))
	for i := range details {
		details[i].Dormant = model.IsDormant(details[i].Status, model.NewTime(last[i]), now)
		items[i] = model.QueueItem{IssueSummary: details[i].Issue.Summary(), Ready: deps[i] == 0, BlockedBy: blocked[details[i].ID]}
		if items[i].BlockedBy == nil {
			items[i].BlockedBy = []string{}
		}
	}
	return items, total, nil
}

func (s *Store) openDependencyKeys(ctx context.Context, details []*IssueDetail) (map[int64][]string, error) {
	out := map[int64][]string{}
	if len(details) == 0 {
		return out, nil
	}
	ids := make([]int64, len(details))
	for i, d := range details {
		ids[i] = d.ID
	}
	rows, err := s.pool.Query(ctx, `SELECT d.source_id, di.key
		FROM graph_edge d JOIN issue di ON di.id = d.target_id
		WHERE d.kind = 'depends_on' AND d.status = 'confirmed' AND d.source_type = 'issue' AND d.target_type = 'issue'
		  AND d.source_id = ANY($1) AND di.status NOT IN ('done', 'cancelled', 'deleted')
		ORDER BY di.key`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			return nil, err
		}
		out[id] = append(out[id], key)
	}
	return out, rows.Err()
}

// CompatBoard returns one column per status in fixed order. Closed columns only hold
// issues closed within the last closedDays days.
func (s *Store) CompatBoard(ctx context.Context, projects []string, closedDays, limit int) (*store.Board, error) {
	b := &store.Board{Columns: make([]store.BoardColumn, 0, len(model.IssueStatuses))}
	if len(projects) > 0 {
		joined := strings.Join(projects, ",")
		b.Project = &joined
	}
	var closedSince *time.Time
	if closedDays > 0 {
		t := s.now().Add(-time.Duration(closedDays) * 24 * time.Hour)
		closedSince = &t
	}
	for _, st := range model.IssueStatuses {
		f := CompatIssueFilter{Projects: projects, Statuses: []model.IssueStatus{st}, Limit: limit}
		if st.Closed() {
			f.ClosedSince = closedSince
		}
		res, err := s.CompatListIssues(ctx, f)
		if err != nil {
			return nil, err
		}
		b.Columns = append(b.Columns, store.BoardColumn{Status: st, Total: res.Total, Issues: nonNilSummaries(res.Items)})
	}
	return b, nil
}

func nonNilSummaries(v []model.IssueSummary) []model.IssueSummary {
	if v == nil {
		return []model.IssueSummary{}
	}
	return v
}

// CompatLabels counts labels over open issues, most used first.
func (s *Store) CompatLabels(ctx context.Context, projects []string) ([]store.LabelCount, error) {
	var args []any
	cond := ` WHERE i.status IN ('backlog','doing','review')`
	if len(projects) > 0 {
		args = append(args, projects)
		cond += ` AND p.key = ANY($1)`
	}
	rows, err := s.pool.Query(ctx, `SELECT l.label, count(*) FROM issue_label l JOIN issue i ON i.id = l.issue_id JOIN project p ON p.id = i.project_id`+
		cond+` GROUP BY l.label ORDER BY count(*) DESC, l.label`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []store.LabelCount{}
	for rows.Next() {
		var lc store.LabelCount
		if err := rows.Scan(&lc.Label, &lc.Count); err != nil {
			return nil, err
		}
		out = append(out, lc)
	}
	return out, rows.Err()
}

var activeForVerification = []model.IssueStatus{model.StatusBacklog, model.StatusDoing, model.StatusReview, model.StatusDone}

// CompatOverview builds the project status view. Each list holds at most limit items
// and reports its full count.
func (s *Store) CompatOverview(ctx context.Context, key string, limit int) (*store.Overview, error) {
	project, err := s.GetProject(ctx, key)
	if err != nil {
		return nil, err
	}
	o := &store.Overview{Project: project, GeneratedAt: model.NewTime(s.now())}
	o.Counts.ByStatus = map[model.IssueStatus]int{}
	for _, st := range model.IssueStatuses {
		o.Counts.ByStatus[st] = 0
	}
	rows, err := s.pool.Query(ctx, `SELECT i.status, count(*) FROM issue i JOIN project p ON p.id = i.project_id WHERE p.key = $1 GROUP BY i.status`, key)
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
	rows, err = s.pool.Query(ctx, `SELECT i.type, i.priority, count(*) FROM issue i JOIN project p ON p.id = i.project_id
		WHERE p.key = $1 AND i.status IN ('backlog','doing','review') GROUP BY i.type, i.priority`, key)
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
	if o.Doing, err = s.compatDoing(ctx, projects, limit); err != nil {
		return nil, err
	}
	rev, err := s.CompatListIssues(ctx, CompatIssueFilter{Projects: projects, Statuses: []model.IssueStatus{model.StatusReview}, Limit: limit})
	if err != nil {
		return nil, err
	}
	o.Review = store.Section[model.IssueSummary]{Total: rev.Total, Items: nonNilSummaries(rev.Items)}
	blocked, total, err := s.CompatQueue(ctx, CompatIssueFilter{Projects: projects, Statuses: model.OpenStatuses, Ready: boolPtr(false), Limit: limit})
	if err != nil {
		return nil, err
	}
	o.Blocked = store.Section[model.QueueItem]{Total: total, Items: nonNilQueue(blocked)}
	if o.Decisions, err = s.compatDecisions(ctx, projects, limit); err != nil {
		return nil, err
	}
	next, total, err := s.CompatQueue(ctx, CompatIssueFilter{Projects: projects, Statuses: []model.IssueStatus{model.StatusBacklog}, Ready: boolPtr(true), Limit: limit})
	if err != nil {
		return nil, err
	}
	o.Next = store.Section[model.QueueItem]{Total: total, Items: nonNilQueue(next)}
	unv, err := s.CompatListIssues(ctx, CompatIssueFilter{
		Projects: projects, Statuses: activeForVerification, VerificationStates: []model.VerificationState{model.VerificationPending},
		Sort: []CompatSort{{Field: "updated_at", Desc: true}}, Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	o.Unverified = store.Section[model.IssueSummary]{Total: unv.Total, Items: nonNilSummaries(unv.Items)}
	acts, total, err := s.CompatListActivity(ctx, CompatActivityFilter{Projects: projects, Limit: limit})
	if err != nil {
		return nil, err
	}
	o.RecentActivity = store.Section[model.Activity]{Total: total, Items: acts}
	return o, nil
}

func boolPtr(b bool) *bool { return &b }

func nonNilQueue(v []model.QueueItem) []model.QueueItem {
	if v == nil {
		return []model.QueueItem{}
	}
	return v
}

// CompatAgenda builds the agenda of the given projects, or of every active project
// when keys is empty.
func (s *Store) CompatAgenda(ctx context.Context, keys []string, limit int) (*store.Agenda, error) {
	if len(keys) == 0 {
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
	a := &store.Agenda{GeneratedAt: model.NewTime(s.now()), Projects: make([]store.AgendaProject, 0, len(keys))}
	dormant := true
	for _, key := range keys {
		o, err := s.CompatOverview(ctx, key, limit)
		if err != nil {
			return nil, err
		}
		ext, err := s.CompatListIssues(ctx, CompatIssueFilter{Projects: []string{key}, Statuses: model.OpenStatuses, Labels: []string{model.ExternalLabel}, Limit: limit})
		if err != nil {
			return nil, err
		}
		tasks, err := s.CompatListIssues(ctx, CompatIssueFilter{Projects: []string{key}, Statuses: model.OpenStatuses, Labels: []string{model.UserTaskLabel}, Limit: limit})
		if err != nil {
			return nil, err
		}
		dorm, err := s.CompatListIssues(ctx, CompatIssueFilter{Projects: []string{key}, Statuses: model.OpenStatuses, Dormant: &dormant, Limit: 1})
		if err != nil {
			return nil, err
		}
		waiting, err := s.compatWaitingSessions(ctx, key, limit)
		if err != nil {
			return nil, err
		}
		var p store.AgendaProject
		p.Project = o.Project
		p.Dormant = dorm.Total
		p.UserTurn.Review, p.UserTurn.Decisions, p.UserTurn.Unverified = o.Review, o.Decisions, o.Unverified
		p.UserTurn.Tasks = store.Section[model.IssueSummary]{Total: tasks.Total, Items: nonNilSummaries(tasks.Items)}
		p.UserTurn.WaitingSessions = waiting
		p.Agent.Doing, p.Agent.Next = o.Doing, o.Next
		p.Waiting.Blocked = o.Blocked
		p.Waiting.External = store.Section[model.IssueSummary]{Total: ext.Total, Items: nonNilSummaries(ext.Items)}
		a.Projects = append(a.Projects, p)
	}
	return a, nil
}

// waitingSessions lists the sessions that are waiting within the hold time, oldest
// state first.
func (s *Store) waitingSessions(ctx context.Context) ([]model.Session, error) {
	waiting := true
	list, _, err := s.ListSessions(ctx, SessionFilter{Waiting: &waiting})
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := list[:0:0]
	for _, sess := range list {
		if sess.WaitingValid(now) {
			out = append(out, sess)
		}
	}
	sortSessionsByState(out)
	return out, nil
}

func sortSessionsByState(v []model.Session) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && lessSession(v[j], v[j-1]); j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

func lessSession(a, b model.Session) bool {
	if !a.StateAt.Time().Equal(b.StateAt.Time()) {
		return a.StateAt.Time().Before(b.StateAt.Time())
	}
	if a.Actor != b.Actor {
		return a.Actor < b.Actor
	}
	return a.SessionID < b.SessionID
}

func (s *Store) compatWaitingSessions(ctx context.Context, project string, limit int) (store.Section[model.Session], error) {
	all, err := s.waitingSessions(ctx)
	if err != nil {
		return store.Section[model.Session]{}, err
	}
	issueProject := map[string]string{}
	var keys []string
	for _, sess := range all {
		if sess.Issue != nil {
			keys = append(keys, *sess.Issue)
		}
	}
	if len(keys) > 0 {
		rows, err := s.pool.Query(ctx, `SELECT i.key, p.key FROM issue i JOIN project p ON p.id = i.project_id WHERE i.key = ANY($1)`, keys)
		if err != nil {
			return store.Section[model.Session]{}, err
		}
		for rows.Next() {
			var ik, pk string
			if err := rows.Scan(&ik, &pk); err != nil {
				rows.Close()
				return store.Section[model.Session]{}, err
			}
			issueProject[ik] = pk
		}
		rows.Close()
	}
	sec := store.Section[model.Session]{Items: []model.Session{}}
	for _, sess := range all {
		owner := ""
		if sess.Issue != nil {
			owner = issueProject[*sess.Issue]
		}
		if owner == "" && sess.LastProject != nil {
			owner = *sess.LastProject
		}
		if owner != project {
			continue
		}
		sec.Total++
		if len(sec.Items) < limit {
			sec.Items = append(sec.Items, sess)
		}
	}
	return sec, nil
}

func (s *Store) compatDecisions(ctx context.Context, projects []string, limit int) (store.Section[model.DecisionItem], error) {
	res, err := s.CompatListIssues(ctx, CompatIssueFilter{Projects: projects, Statuses: model.OpenStatuses, DecisionNeeded: true, Limit: limit})
	if err != nil {
		return store.Section[model.DecisionItem]{}, err
	}
	out := make([]model.DecisionItem, len(res.Items))
	for i, it := range res.Items {
		out[i] = model.DecisionItem{IssueSummary: it, WaitingSessions: []model.Session{}}
	}
	if len(out) > 0 {
		all, err := s.waitingSessions(ctx)
		if err != nil {
			return store.Section[model.DecisionItem]{}, err
		}
		byKey := map[string][]model.Session{}
		for _, sess := range all {
			if sess.Issue != nil {
				byKey[*sess.Issue] = append(byKey[*sess.Issue], sess)
			}
		}
		for i := range out {
			if ws := byKey[out[i].Key]; len(ws) > 0 {
				out[i].WaitingSessions = ws
			}
		}
	}
	return store.Section[model.DecisionItem]{Total: res.Total, Items: out}, nil
}

// compatDoing lists the doing issues with the session holding each: an open claim
// first, otherwise the session that last moved the issue to doing.
func (s *Store) compatDoing(ctx context.Context, projects []string, limit int) (store.Section[model.DoingItem], error) {
	res, err := s.CompatListIssues(ctx, CompatIssueFilter{Projects: projects, Statuses: []model.IssueStatus{model.StatusDoing}, Limit: limit})
	if err != nil {
		return store.Section[model.DoingItem]{}, err
	}
	now := s.now()
	out := make([]model.DoingItem, len(res.Items))
	for n, it := range res.Items {
		var h model.Holder
		var claimedAt, lastSeen *time.Time
		err := s.pool.QueryRow(ctx, `SELECT c.actor_id, c.session_id, c.claimed_at, se.last_seen_at
			FROM issue_claim c JOIN issue i ON i.id = c.issue_id
			LEFT JOIN session se ON se.actor_id = c.actor_id AND se.session_id = c.session_id
			WHERE i.key = $1 AND c.released_at IS NULL ORDER BY c.id DESC LIMIT 1`, it.Key).Scan(&h.Actor, &h.SessionID, &claimedAt, &lastSeen)
		switch {
		case err == nil:
			active := *claimedAt
			if lastSeen != nil {
				active = *lastSeen
			}
			h.LastActiveAt = model.NewTime(active)
			h.Stale = now.Sub(active) > model.SessionStaleAfter
			h.Source = model.HolderSourceClaim
		case errors.Is(err, pgx.ErrNoRows):
			var last time.Time
			err = s.pool.QueryRow(ctx, `SELECT a.actor_id, a.session_id, (SELECT max(b.at) FROM activity b WHERE b.session_id = a.session_id)
				FROM activity a JOIN issue i ON i.id = a.issue_id
				WHERE i.key = $1 AND a.changes->'status'->>'to' = 'doing'
				ORDER BY a.at DESC, a.id DESC LIMIT 1`, it.Key).Scan(&h.Actor, &h.SessionID, &last)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return store.Section[model.DoingItem]{}, fmt.Errorf("holder of %s: %w", it.Key, err)
			}
			if err == nil {
				h.LastActiveAt = model.NewTime(last)
				h.Stale = now.Sub(last) > model.StaleAfter
				h.Source = model.HolderSourceActivity
			}
		default:
			return store.Section[model.DoingItem]{}, fmt.Errorf("holder of %s: %w", it.Key, err)
		}
		out[n] = model.DoingItem{IssueSummary: it, Holder: h}
	}
	return store.Section[model.DoingItem]{Total: res.Total, Items: out}, nil
}

// CompatActivityFilter selects activity records. Slice filters are OR within the
// slice; different filters are ANDed.
type CompatActivityFilter struct {
	Projects     []string
	Issues       []string
	Actors       []string
	Sessions     []string
	ClientHosts  []string
	Kinds        []model.ActivityKind
	CommentKinds []model.CommentKind
	Field        string
	Since        *time.Time
	Until        *time.Time
	Asc          bool
	Limit        int
	Offset       int
}

func strs[T ~string](v []T) []string {
	out := make([]string, len(v))
	for i, x := range v {
		out[i] = string(x)
	}
	return out
}

// CompatListActivity returns a page of activity records and the total matching count.
func (s *Store) CompatListActivity(ctx context.Context, f CompatActivityFilter) ([]model.Activity, int, error) {
	var where []string
	var args []any
	add := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	in := func(col string, vals []string) {
		if len(vals) > 0 {
			where = append(where, col+" = ANY("+add(vals)+")")
		}
	}
	in("p.key", f.Projects)
	in("i.key", f.Issues)
	in("a.actor_id", f.Actors)
	in("a.session_id", f.Sessions)
	in("a.client_host", f.ClientHosts)
	in("a.kind", strs(f.Kinds))
	in("a.comment_kind", strs(f.CommentKinds))
	if f.Field != "" {
		where = append(where, "a.changes ? "+add(f.Field))
	}
	if f.Since != nil {
		where = append(where, "a.at >= "+add(*f.Since))
	}
	if f.Until != nil {
		where = append(where, "a.at < "+add(*f.Until))
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	const from = ` FROM activity a JOIN issue i ON i.id = a.issue_id JOIN project p ON p.id = i.project_id`
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*)`+from+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	dir := "DESC"
	if f.Asc {
		dir = "ASC"
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT a.id, i.key, p.key, a.kind, a.actor_id, a.session_id, a.client_host, a.at, a.changes, a.summary, a.comment_kind, a.body`+
		from+cond+fmt.Sprintf(" ORDER BY a.at %s, a.id %s LIMIT %s OFFSET %s", dir, dir, add(limit), add(f.Offset)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.Activity{}
	for rows.Next() {
		a, err := scanCompatActivity(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, a)
	}
	return items, total, rows.Err()
}

func scanCompatActivity(row pgx.Row) (model.Activity, error) {
	var a model.Activity
	var at time.Time
	var changes []byte
	var commentKind *string
	if err := row.Scan(&a.ID, &a.Issue, &a.Project, &a.Kind, &a.Actor, &a.SessionID, &a.ClientHost, &at, &changes, &a.Summary, &commentKind, &a.Body); err != nil {
		return a, err
	}
	a.At = model.NewTime(at)
	if len(changes) > 0 {
		if err := json.Unmarshal(changes, &a.Changes); err != nil {
			return a, err
		}
		// The classic API names the milestone field "milestone".
		if c, ok := a.Changes["milestone_id"]; ok {
			delete(a.Changes, "milestone_id")
			a.Changes["milestone"] = c
		}
	}
	if commentKind != nil {
		k := model.CommentKind(*commentKind)
		a.CommentKind = &k
	}
	return a, nil
}

// CompatListIssueClaims lists the claims of an issue, newest first.
func (s *Store) CompatListIssueClaims(ctx context.Context, key string, limit, offset int) ([]model.IssueClaim, int, error) {
	var issueID int64
	if err := s.pool.QueryRow(ctx, `SELECT id FROM issue WHERE key = $1`, key).Scan(&issueID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM issue_claim WHERE issue_id = $1`, issueID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT ic.id, ic.actor_id, ic.session_id, ic.claimed_at, ic.released_at, ic.outcome, ic.reason, se.last_seen_at
		FROM issue_claim ic LEFT JOIN session se ON se.actor_id = ic.actor_id AND se.session_id = ic.session_id
		WHERE ic.issue_id = $1 ORDER BY ic.id DESC LIMIT $2 OFFSET $3`, issueID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	now := s.now()
	items := []model.IssueClaim{}
	for rows.Next() {
		var c model.IssueClaim
		c.Issue = key
		var claimed time.Time
		var released, lastSeen *time.Time
		if err := rows.Scan(&c.ID, &c.Actor, &c.SessionID, &claimed, &released, &c.Outcome, &c.Reason, &lastSeen); err != nil {
			return nil, 0, err
		}
		c.ClaimedAt = model.NewTime(claimed)
		c.ReleasedAt = toModelTimePtr(released)
		if released == nil {
			active := claimed
			if lastSeen != nil {
				active = *lastSeen
			}
			c.Active = now.Sub(active) <= model.SessionStaleAfter
		}
		items = append(items, c)
	}
	return items, total, rows.Err()
}

// CompatListProjects lists projects with the classic paging and ordering: sort by
// key, created_at or updated_at; the reserved global project is left out.
func (s *Store) CompatListProjects(ctx context.Context, statuses []model.ProjectStatus, sorts []CompatSort, limit, offset int) ([]model.Project, int, error) {
	vals := make([]string, len(statuses))
	for i, st := range statuses {
		vals[i] = string(st)
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM project WHERE scope_kind <> 'global' AND status = ANY($1)`, vals).Scan(&total); err != nil {
		return nil, 0, err
	}
	var terms []string
	for _, so := range sorts {
		col := map[string]string{"key": "key", "created_at": "created_at", "updated_at": "updated_at"}[so.Field]
		if col == "" {
			continue
		}
		if so.Desc {
			col += " DESC"
		}
		terms = append(terms, col)
	}
	if len(terms) == 0 {
		terms = append(terms, "key")
	}
	tie := "id"
	if len(sorts) > 0 && sorts[0].Desc {
		tie += " DESC"
	}
	rows, err := s.pool.Query(ctx, `SELECT `+projectCols+` FROM project WHERE scope_kind <> 'global' AND status = ANY($1) ORDER BY `+
		strings.Join(append(terms, tie), ", ")+` LIMIT $2 OFFSET $3`, vals, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, p)
	}
	return items, total, rows.Err()
}
