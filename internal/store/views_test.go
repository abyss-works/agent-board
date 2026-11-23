package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

func keysOf[T any](items []T, key func(T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = key(it)
	}
	return out
}

func TestBoard(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "b1", Status: model.StatusBacklog, Priority: model.P3})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "b2", Status: model.StatusBacklog, Priority: model.P1})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "t1", Status: model.StatusReview})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "d1", Status: model.StatusDone})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "x1", Status: model.StatusDeleted})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "o1", Project: "OTH", Status: model.StatusDoing})
	// An issue closed 10 days ago falls outside the default 7-day window.
	old := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "old", Status: model.StatusBacklog})
	base := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return base.Add(-10 * 24 * time.Hour) })
	done := model.StatusDone
	s.UpdateIssue(ctx, trOwner, old.Key, IssuePatch{Status: &done}, nil)
	s.SetClock(func() time.Time { return base })

	b, err := s.Board(ctx, BoardParams{Projects: []string{"AGB"}, ClosedDays: 7, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Columns) != 6 {
		t.Fatalf("columns = %d", len(b.Columns))
	}
	for i, st := range model.IssueStatuses {
		if b.Columns[i].Status != st {
			t.Errorf("column %d = %s, want %s", i, b.Columns[i].Status, st)
		}
	}
	backlog := b.Columns[0]
	if backlog.Total != 2 || !reflect.DeepEqual(keysOf(backlog.Issues, func(i model.IssueSummary) string { return i.Key }), []string{"AGB-2", "AGB-1"}) {
		t.Errorf("backlog = %+v", backlog)
	}
	if b.Columns[1].Total != 0 || b.Columns[2].Total != 1 || b.Columns[3].Total != 1 || b.Columns[5].Total != 1 {
		t.Errorf("totals = %v", keysOf(b.Columns, func(c BoardColumn) string { return string(c.Status) + ":" + itoa(c.Total) }))
	}
	if b.Columns[1].Issues == nil {
		t.Error("empty column must have [] not null")
	}
	b, _ = s.Board(ctx, BoardParams{Projects: []string{"AGB"}, ClosedDays: 0, Limit: 100})
	if b.Columns[3].Total != 2 {
		t.Errorf("closed_days=0 must include old closed issues: %d", b.Columns[3].Total)
	}
	b, _ = s.Board(ctx, BoardParams{Projects: []string{"AGB"}, ClosedDays: 7, Limit: 1})
	if b.Columns[0].Total != 2 || len(b.Columns[0].Issues) != 1 {
		t.Errorf("limit per column: total %d items %d", b.Columns[0].Total, len(b.Columns[0].Issues))
	}
	b, _ = s.Board(ctx, BoardParams{ClosedDays: 7, Limit: 100})
	if b.Columns[1].Total != 1 || b.Columns[1].Issues[0].Key != "OTH-1" {
		t.Errorf("all projects doing = %+v", b.Columns[1])
	}
}

func itoa(n int) string { return string(rune('0' + n)) }

func TestQueue(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	dep := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "dep", Status: model.StatusDoing})
	doneDep := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "done dep", Status: model.StatusDone})
	p1blocked := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "p1 blocked", Status: model.StatusBacklog, Priority: model.P1, DependsOn: []string{dep.Key, doneDep.Key}})
	p1ready := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "p1 ready", Status: model.StatusBacklog, Priority: model.P1, DependsOn: []string{doneDep.Key}})
	p0 := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "p0", Status: model.StatusBacklog, Priority: model.P0})
	p2 := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "p2", Status: model.StatusBacklog, Priority: model.P2})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "in review", Status: model.StatusReview, Priority: model.P0})

	items, total, err := s.Queue(ctx, QueueFilter{Statuses: []model.IssueStatus{model.StatusBacklog}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := keysOf(items, func(q model.QueueItem) string { return q.Key })
	want := []string{p0.Key, p1ready.Key, p1blocked.Key, p2.Key}
	if total != 4 || !reflect.DeepEqual(got, want) {
		t.Errorf("queue = %v (total %d), want %v", got, total, want)
	}
	if !items[1].Ready || len(items[1].BlockedBy) != 0 || items[1].BlockedBy == nil {
		t.Errorf("ready item = %+v", items[1])
	}
	if items[2].Ready || !reflect.DeepEqual(items[2].BlockedBy, []string{dep.Key}) {
		t.Errorf("blocked item = %+v", items[2])
	}
	items, _, _ = s.Queue(ctx, QueueFilter{Statuses: []model.IssueStatus{model.StatusBacklog}, Limit: 1})
	if len(items) != 1 || items[0].Key != p0.Key {
		t.Errorf("next = %+v", items)
	}
	items, total, _ = s.Queue(ctx, QueueFilter{Statuses: []model.IssueStatus{model.StatusBacklog, model.StatusReview}, Limit: 10})
	if total != 5 || items[0].Key != p0.Key || items[1].Key != "AGB-7" {
		t.Errorf("with backlog = %v", keysOf(items, func(q model.QueueItem) string { return q.Key }))
	}
	items, total, _ = s.Queue(ctx, QueueFilter{Statuses: model.OpenStatuses, OnlyBlocked: true, Limit: 10})
	if total != 1 || items[0].Key != p1blocked.Key {
		t.Errorf("only blocked = %v", keysOf(items, func(q model.QueueItem) string { return q.Key }))
	}
	// Closing the open dependency makes the issue ready without writing to it.
	done := model.StatusDone
	s.UpdateIssue(ctx, trOwner, dep.Key, IssuePatch{Status: &done}, nil)
	items, _, _ = s.Queue(ctx, QueueFilter{Statuses: []model.IssueStatus{model.StatusBacklog}, Limit: 10})
	if items[1].Key != p1blocked.Key && items[2].Key != p1blocked.Key || !items[2].Ready {
		t.Errorf("after dep closed = %+v", items)
	}
	v, _ := s.GetIssue(ctx, p1blocked.Key)
	if v.Version != 1 {
		t.Errorf("dependent issue was written: version %d", v.Version)
	}
}

// Within a priority, issues in the earliest open milestone come first, then
// those blocking more open issues, then older ones.
func TestQueueTiebreak(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	later, err := s.CreateMilestone(ctx, trOwner, "AGB", MilestoneInput{Name: "later"})
	if err != nil {
		t.Fatal(err)
	}
	dated, err := s.CreateMilestone(ctx, trOwner, "AGB", MilestoneInput{Name: "dated", TargetDate: str("2026-12-01")})
	if err != nil {
		t.Fatal(err)
	}
	closed, err := s.CreateMilestone(ctx, trOwner, "AGB", MilestoneInput{Name: "closed"})
	if err != nil {
		t.Fatal(err)
	}
	in := func(title string, m *model.Milestone) IssueInput {
		i := IssueInput{Title: title, Status: model.StatusBacklog, Priority: model.P2}
		if m != nil {
			i.Milestone = &m.ID
		}
		return i
	}
	old := mustIssue(t, s, ctx, trOwner, in("old", nil))
	blocker := mustIssue(t, s, ctx, trOwner, in("blocker", nil))
	inClosed := mustIssue(t, s, ctx, trOwner, in("in closed", closed))
	inLater := mustIssue(t, s, ctx, trOwner, in("in later", later))
	inDated := mustIssue(t, s, ctx, trOwner, in("in dated", dated))
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "waits", Status: model.StatusBacklog, Priority: model.P3, DependsOn: []string{blocker.Key}})
	closedStatus := model.MilestoneClosed
	if _, err := s.UpdateMilestone(ctx, trOwner, closed.ID, MilestonePatch{Status: &closedStatus}); err != nil {
		t.Fatal(err)
	}

	items, _, err := s.Queue(ctx, QueueFilter{Statuses: []model.IssueStatus{model.StatusBacklog}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := keysOf(items, func(q model.QueueItem) string { return q.Key })[:5]
	want := []string{inDated.Key, inLater.Key, blocker.Key, old.Key, inClosed.Key}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("queue = %v, want %v", got, want)
	}
}

func TestLabels(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "a", Labels: []string{"api", "cli"}})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "b", Labels: []string{"api"}})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "c", Labels: []string{"api", "zed"}, Status: model.StatusDone})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "d", Project: "OTH", Labels: []string{"ops"}})

	labels, err := s.Labels(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []LabelCount{{"api", 2}, {"cli", 1}, {"ops", 1}}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("labels = %+v, want %+v (open issues only, count desc then label asc)", labels, want)
	}
	labels, _ = s.Labels(ctx, []string{"OTH"})
	if !reflect.DeepEqual(labels, []LabelCount{{"ops", 1}}) {
		t.Errorf("project labels = %+v", labels)
	}
	labels, _ = s.Labels(ctx, []string{"NONE"})
	if labels == nil || len(labels) != 0 {
		t.Errorf("empty must be [] not nil: %#v", labels)
	}
}

func TestOverview(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	dep := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "dep", Status: model.StatusDoing, Priority: model.P1})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "blocked", Status: model.StatusBacklog, DependsOn: []string{dep.Key}})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "ready", Status: model.StatusBacklog, Priority: model.P0})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "decide", Status: model.StatusBacklog, DecisionNeeded: str("어느 쪽으로 할지")})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "decided", Status: model.StatusDone, DecisionNeeded: str("이미 답을 받은 질문")})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "review", Status: model.StatusReview, Type: model.TypeBug})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "gone", Status: model.StatusDeleted})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "other", Project: "OTH", Status: model.StatusDoing})
	s.AddComment(ctx, trClaude, dep.Key, "hi", nil, nil)

	o, err := s.Overview(ctx, "AGB", 10)
	if err != nil {
		t.Fatal(err)
	}
	if o.Project.Key != "AGB" || o.GeneratedAt.IsZero() {
		t.Errorf("header = %+v", o)
	}
	c := o.Counts
	if c.Total != 7 || c.Open != 5 {
		t.Errorf("counts = %+v", c)
	}
	if c.ByStatus[model.StatusDoing] != 1 || c.ByStatus[model.StatusBacklog] != 3 || c.ByStatus[model.StatusDeleted] != 1 || c.ByStatus[model.StatusCancelled] != 0 || len(c.ByStatus) != 6 {
		t.Errorf("by_status = %v", c.ByStatus)
	}
	if c.ByType[model.TypeFeature] != 4 || c.ByType[model.TypeBug] != 1 || len(c.ByType) != 5 {
		t.Errorf("by_type (open only) = %v", c.ByType)
	}
	if c.ByPriority[model.P0] != 1 || c.ByPriority[model.P1] != 2 || c.ByPriority[model.P2] != 2 || len(c.ByPriority) != 4 {
		t.Errorf("by_priority (open only) = %v", c.ByPriority)
	}
	if o.Doing.Total != 1 || o.Doing.Items[0].Key != dep.Key {
		t.Errorf("doing = %+v", o.Doing)
	}
	if o.Review.Total != 1 || o.Review.Items[0].Title != "review" {
		t.Errorf("review = %+v", o.Review)
	}
	if o.Blocked.Total != 1 || o.Blocked.Items[0].Title != "blocked" || !reflect.DeepEqual(o.Blocked.Items[0].BlockedBy, []string{dep.Key}) {
		t.Errorf("blocked = %+v", o.Blocked)
	}
	if o.Decisions.Total != 1 || o.Decisions.Items[0].Title != "decide" {
		t.Errorf("decisions = %+v", o.Decisions)
	}
	// The next issues are the ready ones in backlog, in queue order; a question does not take an issue out of them.
	if o.Next.Total != 2 || o.Next.Items[0].Title != "ready" || o.Next.Items[1].Title != "decide" || !o.Next.Items[0].Ready || !o.Next.Items[1].Ready {
		t.Errorf("next = %+v", o.Next)
	}
	// 7 created records + 1 comment, newest first; OTH activity excluded.
	if o.RecentActivity.Total != 8 || o.RecentActivity.Items[0].Kind != model.ActivityCommented || len(o.RecentActivity.Items) != 8 {
		t.Errorf("recent_activity = total %d, %d items, first %s", o.RecentActivity.Total, len(o.RecentActivity.Items), o.RecentActivity.Items[0].Kind)
	}
	o, _ = s.Overview(ctx, "AGB", 2)
	if len(o.RecentActivity.Items) != 2 || o.RecentActivity.Total != 8 {
		t.Errorf("limit = %d items, total %d", len(o.RecentActivity.Items), o.RecentActivity.Total)
	}
	if _, err := s.Overview(ctx, "NOPE", 10); err == nil {
		t.Error("missing project must fail")
	}
}

// A doing issue with an open claim uses the stored claim (30m stale, 24h while
// waiting) with source "claim"; a doing issue without an open claim falls back
// to activity history (2h stale) with source "activity".
func TestOverviewDoingHolder(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	clockAt(s, &now)
	held := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "held"})
	other := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "other"})
	born := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "born doing", Status: model.StatusDoing})
	doing := model.StatusDoing
	if _, _, err := s.UpdateIssue(ctx, trClaude, held.Key, IssuePatch{Status: &doing}, nil); err != nil {
		t.Fatal(err)
	}
	holders := func() map[string]model.Holder {
		t.Helper()
		o, err := s.Overview(ctx, "AGB", 10)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]model.Holder{}
		for _, it := range o.Doing.Items {
			m[it.Key] = it.Holder
		}
		return m
	}

	// Claimed issues report source="claim" and session last_seen_at.
	start := model.NewTime(now)
	now = now.Add(20 * time.Minute)
	mustTouch(t, s, trClaude, now)
	seen := model.NewTime(now)
	now = now.Add(20 * time.Minute) // 20m after seen (active), 40m after start (owner stale)
	h := holders()
	if got := h[held.Key]; got.Actor != "claude" || got.SessionID != "s-claude" || got.LastActiveAt != seen || got.Stale || got.Source != model.HolderSourceClaim {
		t.Errorf("claimed active = %+v, want claude s-claude last active %s not stale claim", got, seen)
	}
	if got := h[born.Key]; got.Actor != "owner" || got.SessionID != "s-owner" || got.LastActiveAt != start || !got.Stale || got.Source != model.HolderSourceClaim {
		t.Errorf("claimed stale = %+v, want owner s-owner last active %s stale claim", got, start)
	}

	// Waiting session keeps claimed issue non-stale within 24h, stale after 24h.
	stWait := model.SessionStateWaitingInput
	if _, err := s.UpdateSession(ctx, trClaude, trClaude.Actor, trClaude.SessionID, SessionPatch{
		State: Optional[string]{Set: true, Value: &stWait},
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(23 * time.Hour)
	if got := holders()[held.Key]; got.Stale || got.Source != model.HolderSourceClaim {
		t.Errorf("waiting claimed at 23h = %+v, want not stale claim", got)
	}
	ag, err := s.Agenda(ctx, []string{"AGB"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var agHeld model.Holder
	for _, it := range ag.Projects[0].Agent.Doing.Items {
		if it.Key == held.Key {
			agHeld = it.Holder
		}
	}
	if agHeld.Stale || agHeld.Source != model.HolderSourceClaim {
		t.Errorf("agenda waiting claimed at 23h = %+v, want not stale claim", agHeld)
	}
	now = now.Add(2 * time.Hour)
	if got := holders()[held.Key]; !got.Stale || got.Source != model.HolderSourceClaim {
		t.Errorf("waiting claimed at 25h = %+v, want stale claim", got)
	}

	// Releasing the claim on a doing issue leaves it in doing without an open claim,
	// so it falls back to activity history (2h stale window) with source="activity".
	if _, err := s.ReleaseClaim(ctx, trClaude, held.Key, model.ClaimOutcomeAbandoned, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddComment(ctx, trClaude, other.Key, "다른 이슈 활동", nil, nil); err != nil {
		t.Fatal(err)
	}
	alive := model.NewTime(now)
	now = now.Add(time.Hour)
	if got := holders()[held.Key]; got.Actor != "claude" || got.SessionID != "s-claude" || got.LastActiveAt != alive || got.Stale || got.Source != model.HolderSourceActivity {
		t.Errorf("activity fallback after 1h = %+v, want claude s-claude %s not stale activity", got, alive)
	}
	now = now.Add(90 * time.Minute)
	if got := holders()[held.Key]; !got.Stale || got.Source != model.HolderSourceActivity {
		t.Errorf("activity fallback after 2.5h = %+v, want stale activity", got)
	}
}

func TestOverviewDecisions(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	ask := func(title string, in IssueInput) *model.Issue {
		in.Title, in.DecisionNeeded = title, str(title+"인지")
		return mustIssue(t, s, ctx, trOwner, in)
	}
	first := ask("p2 first", IssueInput{})
	review := ask("p0 in review", IssueInput{Type: model.TypeBug, Status: model.StatusReview, Priority: model.P0})
	later := ask("p2 doing", IssueInput{Type: model.TypeDesign, Status: model.StatusDoing})
	for _, st := range model.ClosedStatuses {
		ask("closed "+string(st), IssueInput{Status: st})
	}
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "no question"})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "other project", Project: "OTH", DecisionNeeded: str("다른 프로젝트의 질문")})

	keys := func(o *Overview) []string {
		return keysOf(o.Decisions.Items, func(i model.DecisionItem) string { return i.Key })
	}
	o, err := s.Overview(ctx, "AGB", 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{review.Key, first.Key, later.Key}; o.Decisions.Total != 3 || !reflect.DeepEqual(keys(o), want) {
		t.Errorf("decisions = %v (total %d), want %v by priority, then creation", keys(o), o.Decisions.Total, want)
	}
	if q := o.Decisions.Items[0].DecisionNeeded; q == nil || *q != "p0 in review인지" {
		t.Errorf("an item carries its question: %v", q)
	}
	o, _ = s.Overview(ctx, "AGB", 2)
	if o.Decisions.Total != 3 || len(o.Decisions.Items) != 2 {
		t.Errorf("limit 2 = total %d, %d items", o.Decisions.Total, len(o.Decisions.Items))
	}

	// Clearing the question takes the issue out; asking again puts it back.
	if _, _, err := s.UpdateIssue(ctx, trOwner, first.Key, IssuePatch{DecisionNeeded: Optional[string]{Set: true}}, nil); err != nil {
		t.Fatal(err)
	}
	if o, _ = s.Overview(ctx, "AGB", 10); !reflect.DeepEqual(keys(o), []string{review.Key, later.Key}) {
		t.Errorf("after clearing the question = %v", keys(o))
	}
	if _, _, err := s.UpdateIssue(ctx, trOwner, first.Key, IssuePatch{DecisionNeeded: Optional[string]{Set: true, Value: str("다시 묻는다")}}, nil); err != nil {
		t.Fatal(err)
	}
	if o, _ = s.Overview(ctx, "AGB", 10); !reflect.DeepEqual(keys(o), []string{review.Key, first.Key, later.Key}) {
		t.Errorf("after asking again = %v", keys(o))
	}

	// A project without questions shows an empty list, not null.
	mustProject(t, s, ctx, "NEW")
	none, _ := s.Overview(ctx, "NEW", 10)
	if none.Decisions.Total != 0 || none.Decisions.Items == nil {
		t.Errorf("empty section must be [] not null: %+v", none.Decisions)
	}
}

// Every view that lists issues shows the question of an issue.
func TestViewItemsCarryDecisionNeeded(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	const question = "어느 쪽으로 할지"
	dep := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "doing", Status: model.StatusDoing, DecisionNeeded: str(question), VerificationState: model.VerificationPending})
	mustIssue(t, s, ctx, trClaude, IssueInput{Title: "review", Status: model.StatusReview, DecisionNeeded: str(question)})
	mustIssue(t, s, ctx, trClaude, IssueInput{Title: "blocked", DependsOn: []string{dep.Key}, DecisionNeeded: str(question)})
	mustIssue(t, s, ctx, trClaude, IssueInput{Title: "ready", Priority: model.P0, DecisionNeeded: str(question)})

	has := func(where string, got *string) {
		t.Helper()
		if got == nil || *got != question {
			t.Errorf("%s: decision_needed = %v, want %q", where, got, question)
		}
	}
	b, err := s.Board(ctx, BoardParams{Projects: []string{"AGB"}, ClosedDays: 7, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	has("board", b.Columns[0].Issues[0].DecisionNeeded)
	q, _, err := s.Queue(ctx, QueueFilter{Statuses: model.OpenStatuses, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	has("queue", q[0].DecisionNeeded)
	o, err := s.Overview(ctx, "AGB", 10)
	if err != nil {
		t.Fatal(err)
	}
	has("overview doing", o.Doing.Items[0].DecisionNeeded)
	has("overview review", o.Review.Items[0].DecisionNeeded)
	has("overview blocked", o.Blocked.Items[0].DecisionNeeded)
	has("overview decisions", o.Decisions.Items[0].DecisionNeeded)
	has("overview next", o.Next.Items[0].DecisionNeeded)
	has("overview unverified", o.Unverified.Items[0].DecisionNeeded)
}

func TestQueueLeavesOutExternalWaits(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	wait := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "팀 합의 대기", Status: model.StatusBacklog, Priority: model.P1, Labels: []string{model.ExternalLabel}})
	blocked := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "합의 뒤 구현", Status: model.StatusBacklog, Priority: model.P1, DependsOn: []string{wait.Key}})
	ready := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "바로 할 일", Status: model.StatusBacklog, Priority: model.P2})

	items, total, err := s.Queue(ctx, QueueFilter{Statuses: []model.IssueStatus{model.StatusBacklog}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := keysOf(items, func(q model.QueueItem) string { return q.Key }); total != 2 || !reflect.DeepEqual(got, []string{blocked.Key, ready.Key}) {
		t.Errorf("queue = %v (total %d)", got, total)
	}
	o, err := s.Overview(ctx, "AGB", 10)
	if err != nil {
		t.Fatal(err)
	}
	if o.Next.Total != 1 || o.Next.Items[0].Key != ready.Key {
		t.Errorf("next = %+v", o.Next)
	}
	if o.Blocked.Total != 1 || o.Blocked.Items[0].Key != blocked.Key || !reflect.DeepEqual(o.Blocked.Items[0].BlockedBy, []string{wait.Key}) {
		t.Errorf("blocked = %+v", o.Blocked)
	}
	done := model.StatusDone
	if _, _, err := s.UpdateIssue(ctx, trOwner, wait.Key, IssuePatch{Status: &done}, nil); err != nil {
		t.Fatal(err)
	}
	o, _ = s.Overview(ctx, "AGB", 10)
	if o.Next.Total != 2 || o.Next.Items[0].Key != blocked.Key || o.Blocked.Total != 0 {
		t.Errorf("after the wait ends: next = %+v, blocked = %d", keysOf(o.Next.Items, func(q model.QueueItem) string { return q.Key }), o.Blocked.Total)
	}
}

func TestAgendaGroupsUserTurnAgentWorkAndWaiting(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "PF")
	review := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "화면 확인", Status: model.StatusReview})
	ask := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "물을 것", Status: model.StatusBacklog, DecisionNeeded: str("어느 쪽?")})
	wait := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "MR 대기", Status: model.StatusBacklog, Labels: []string{model.ExternalLabel}})
	blocked := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "MR 뒤 작업", Status: model.StatusBacklog, DependsOn: []string{wait.Key}})
	pf := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "PF", Title: "PF 할 일", Status: model.StatusBacklog})

	a, err := s.Agenda(ctx, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Projects) != 2 || a.Projects[0].Project.Key != "AGB" || a.Projects[1].Project.Key != "PF" {
		t.Fatalf("projects = %+v", a.Projects)
	}
	agb := a.Projects[0]
	if agb.UserTurn.Review.Total != 1 || agb.UserTurn.Review.Items[0].Key != review.Key || agb.UserTurn.Decisions.Total != 1 || agb.UserTurn.Decisions.Items[0].Key != ask.Key {
		t.Errorf("user turn = %+v", agb.UserTurn)
	}
	if got := keysOf(agb.Agent.Next.Items, func(q model.QueueItem) string { return q.Key }); !reflect.DeepEqual(got, []string{ask.Key}) {
		t.Errorf("agent next = %v", got)
	}
	if agb.Waiting.Blocked.Total != 1 || agb.Waiting.Blocked.Items[0].Key != blocked.Key || agb.Waiting.External.Total != 1 || agb.Waiting.External.Items[0].Key != wait.Key {
		t.Errorf("waiting = %+v", agb.Waiting)
	}
	if a.Projects[1].Agent.Next.Total != 1 || a.Projects[1].Agent.Next.Items[0].Key != pf.Key {
		t.Errorf("pf = %+v", a.Projects[1])
	}
	a, err = s.Agenda(ctx, []string{"PF"}, 10)
	if err != nil || len(a.Projects) != 1 || a.Projects[0].Project.Key != "PF" {
		t.Errorf("only PF = %+v, %v", a, err)
	}
	if _, err := s.Agenda(ctx, []string{"NOPE"}, 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown project: %v", err)
	}
}

func TestUserTasksAreTheUsersTurn(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	task := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "메일 수신 확인", Status: model.StatusBacklog, Priority: model.P1, Labels: []string{model.UserTaskLabel}})
	work := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "에이전트 일", Status: model.StatusBacklog, Priority: model.P2})
	items, _, err := s.Queue(ctx, QueueFilter{Statuses: []model.IssueStatus{model.StatusBacklog}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := keysOf(items, func(q model.QueueItem) string { return q.Key }); !reflect.DeepEqual(got, []string{work.Key}) {
		t.Errorf("queue = %v", got)
	}
	a, err := s.Agenda(ctx, []string{"AGB"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	p := a.Projects[0]
	if p.UserTurn.Tasks.Total != 1 || p.UserTurn.Tasks.Items[0].Key != task.Key || p.Agent.Next.Total != 1 {
		t.Errorf("tasks = %+v, next = %+v", p.UserTurn.Tasks, p.Agent.Next)
	}
}

func TestAgendaWaitingSessionsAttributionAndOrder(t *testing.T) {
	s, ctx := seeded(t)
	t0 := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	fixClock(s, t0)
	mustProject(t, s, ctx, "AAA")
	mustProject(t, s, ctx, "BBB")
	issueA := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AAA", Title: "a"})
	issueB := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "BBB", Title: "b"})

	// Session 1: issue in AAA, last_project in BBB -> attributed to AAA only.
	tr1 := model.Trace{Actor: "claude", SessionID: "s-issue-a", ClientHost: "desk"}
	mustTouch(t, s, tr1, t0)
	if _, err := s.AddComment(ctx, tr1, issueB.Key, "BBB 활동", nil, nil); err != nil {
		t.Fatal(err)
	}
	fixClock(s, t0.Add(2*time.Minute))
	stIn := model.SessionStateWaitingInput
	reason1 := "승인 필요"
	if _, err := s.UpdateSession(ctx, tr1, tr1.Actor, tr1.SessionID, SessionPatch{
		Issue:       Optional[string]{Set: true, Value: &issueA.Key},
		State:       Optional[string]{Set: true, Value: &stIn},
		StateReason: Optional[string]{Set: true, Value: &reason1},
	}); err != nil {
		t.Fatal(err)
	}

	// Session 2: no issue, last_project in AAA -> attributed to AAA, earlier state_at so comes first.
	tr2 := model.Trace{Actor: "claude", SessionID: "s-last-a", ClientHost: "desk"}
	mustTouch(t, s, tr2, t0)
	if _, err := s.AddComment(ctx, tr2, issueA.Key, "AAA 활동", nil, nil); err != nil {
		t.Fatal(err)
	}
	fixClock(s, t0.Add(time.Minute))
	stApp := model.SessionStateWaitingApproval
	if _, err := s.UpdateSession(ctx, tr2, tr2.Actor, tr2.SessionID, SessionPatch{
		State: Optional[string]{Set: true, Value: &stApp},
	}); err != nil {
		t.Fatal(err)
	}

	// Session 3: neither issue nor last_project -> excluded from project agendas.
	tr3 := model.Trace{Actor: "claude", SessionID: "s-orphan", ClientHost: "desk"}
	mustTouch(t, s, tr3, t0)
	fixClock(s, t0.Add(30*time.Second))
	stExt := model.SessionStateWaitingExternal
	if _, err := s.UpdateSession(ctx, tr3, tr3.Actor, tr3.SessionID, SessionPatch{
		State: Optional[string]{Set: true, Value: &stExt},
	}); err != nil {
		t.Fatal(err)
	}

	// Session 4: in AAA (via issue) but NOT waiting -> excluded.
	tr4 := model.Trace{Actor: "claude", SessionID: "s-not-waiting", ClientHost: "desk"}
	mustTouch(t, s, tr4, t0)
	if _, err := s.UpdateSession(ctx, tr4, tr4.Actor, tr4.SessionID, SessionPatch{
		Issue: Optional[string]{Set: true, Value: &issueA.Key},
	}); err != nil {
		t.Fatal(err)
	}

	a, err := s.Agenda(ctx, []string{"AAA", "BBB"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	aaa := a.Projects[0].UserTurn.WaitingSessions
	bbb := a.Projects[1].UserTurn.WaitingSessions
	if aaa.Total != 2 || len(aaa.Items) != 2 {
		t.Fatalf("AAA waiting_sessions = %+v", aaa)
	}
	if aaa.Items[0].SessionID != "s-last-a" || aaa.Items[1].SessionID != "s-issue-a" {
		t.Errorf("AAA order = [%s, %s], want [s-last-a, s-issue-a]", aaa.Items[0].SessionID, aaa.Items[1].SessionID)
	}
	if aaa.Items[0].Children != nil || aaa.Items[1].Children != nil {
		t.Errorf("agenda waiting sessions must not carry children")
	}
	if bbb.Total != 0 || bbb.Items == nil || len(bbb.Items) != 0 {
		t.Errorf("BBB waiting_sessions = %+v, want empty non-nil slice", bbb)
	}

	// Limit caps items while keeping total.
	aLimited, err := s.Agenda(ctx, []string{"AAA"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	ws := aLimited.Projects[0].UserTurn.WaitingSessions
	if ws.Total != 2 || len(ws.Items) != 1 || ws.Items[0].SessionID != "s-last-a" {
		t.Errorf("limited waiting_sessions = %+v", ws)
	}
}

func TestAgendaWaitingSessionsCutoffAndTieOrder(t *testing.T) {
	s, ctx := seeded(t)
	if err := s.EnsureActors(ctx, []model.Actor{{ID: "owner", Kind: model.ActorUser}, {ID: "claude", Kind: model.ActorAgent}, {ID: "codex", Kind: model.ActorAgent}}); err != nil {
		t.Fatal(err)
	}
	mustProject(t, s, ctx, "AAA")
	iss := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AAA", Title: "대기 이슈"})

	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	st := model.SessionStateWaitingInput

	// Session over 24h (> SessionWaitingHold) must be excluded.
	trOld := model.Trace{Actor: "claude", SessionID: "s-expired", ClientHost: "desk"}
	fixClock(s, now.Add(-24*time.Hour-time.Minute))
	mustTouch(t, s, trOld, now.Add(-24*time.Hour-time.Minute))
	if _, err := s.UpdateSession(ctx, trOld, trOld.Actor, trOld.SessionID, SessionPatch{
		Issue: Optional[string]{Set: true, Value: &iss.Key},
		State: Optional[string]{Set: true, Value: &st},
	}); err != nil {
		t.Fatal(err)
	}

	// Session at exact 24h boundary must be included and appear before tied later sessions.
	trExact := model.Trace{Actor: "owner", SessionID: "s-boundary", ClientHost: "desk"}
	fixClock(s, now.Add(-24*time.Hour))
	mustTouch(t, s, trExact, now.Add(-24*time.Hour))
	if _, err := s.UpdateSession(ctx, trExact, trExact.Actor, trExact.SessionID, SessionPatch{
		Issue: Optional[string]{Set: true, Value: &iss.Key},
		State: Optional[string]{Set: true, Value: &st},
	}); err != nil {
		t.Fatal(err)
	}

	// Three sessions sharing identical state_at tie-break by actor asc, session_id asc.
	tieAt := now.Add(-time.Hour)
	fixClock(s, tieAt)
	for _, tr := range []model.Trace{
		{Actor: "codex", SessionID: "s-a", ClientHost: "desk"},
		{Actor: "claude", SessionID: "s-b", ClientHost: "desk"},
		{Actor: "claude", SessionID: "s-a", ClientHost: "desk"},
	} {
		mustTouch(t, s, tr, tieAt)
		if _, err := s.UpdateSession(ctx, tr, tr.Actor, tr.SessionID, SessionPatch{
			Issue: Optional[string]{Set: true, Value: &iss.Key},
			State: Optional[string]{Set: true, Value: &st},
		}); err != nil {
			t.Fatal(err)
		}
	}

	fixClock(s, now)
	a, err := s.Agenda(ctx, []string{"AAA"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	ws := a.Projects[0].UserTurn.WaitingSessions
	if ws.Total != 4 || len(ws.Items) != 4 {
		t.Fatalf("waiting_sessions = %+v, want 4 items", ws)
	}
	got := make([]string, len(ws.Items))
	for i, it := range ws.Items {
		got[i] = it.Actor + "/" + it.SessionID
	}
	want := []string{"owner/s-boundary", "claude/s-a", "claude/s-b", "codex/s-a"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("waiting_sessions order = %v, want %v", got, want)
	}
}

func TestAgendaWaitingSessionsLocationConflicts(t *testing.T) {
	s, ctx := seeded(t)
	if err := s.EnsureActors(ctx, []model.Actor{{ID: "owner", Kind: model.ActorUser}, {ID: "claude", Kind: model.ActorAgent}, {ID: "codex", Kind: model.ActorAgent}}); err != nil {
		t.Fatal(err)
	}
	mustProject(t, s, ctx, "AAA")
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	fixClock(s, t0)

	ptr := func(v string) *string { return &v }
	trWait := model.Trace{Actor: "claude", SessionID: "s-wait", ClientHost: "desk"}
	trOther := model.Trace{Actor: "codex", SessionID: "s-other", ClientHost: "laptop"}
	mustTouch(t, s, trWait, t0)
	mustTouch(t, s, trOther, t0)
	i := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AAA", Title: "a"})
	stIn := model.SessionStateWaitingInput
	if _, err := s.UpdateSession(ctx, trWait, trWait.Actor, trWait.SessionID, SessionPatch{
		Issue:    Optional[string]{Set: true, Value: &i.Key},
		State:    Optional[string]{Set: true, Value: &stIn},
		Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{RepoRemote: ptr("https://example.com/acme/app.git"), Branch: ptr("feat/a")}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSession(ctx, trOther, trOther.Actor, trOther.SessionID, SessionPatch{
		Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{RepoRemote: ptr("git@example.com:acme/app.git"), Branch: ptr("feat/a")}},
	}); err != nil {
		t.Fatal(err)
	}

	a, err := s.Agenda(ctx, []string{"AAA"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	items := a.Projects[0].UserTurn.WaitingSessions.Items
	if len(items) != 1 {
		t.Fatalf("waiting items = %+v", items)
	}
	if len(items[0].LocationConflicts) != 1 || items[0].LocationConflicts[0] != (model.SessionRef{Actor: "codex", SessionID: "s-other"}) {
		t.Errorf("waiting session location_conflicts = %+v", items[0].LocationConflicts)
	}
}

func TestOverviewAndAgendaDecisionWaitingSessions(t *testing.T) {
	s, ctx := seeded(t)
	if err := s.EnsureActors(ctx, []model.Actor{
		{ID: "owner", Kind: model.ActorUser},
		{ID: "claude", Kind: model.ActorAgent},
		{ID: "codex", Kind: model.ActorAgent},
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	clockAt(s, &now)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")

	dDirect := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "direct", Priority: model.P0, DecisionNeeded: str("직접 연결 결정?")})
	dMulti := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "multi", Priority: model.P1, DecisionNeeded: str("복수 세션 결정?")})
	dClaimed := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "claimed", Priority: model.P2, DecisionNeeded: str("점유 연결 결정?")})
	dEmpty := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "empty", Priority: model.P3, DecisionNeeded: str("대기 없는 결정?")})
	nonDecision := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "plain", Priority: model.P0})
	closedDecision := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "closed", Status: model.StatusDone, DecisionNeeded: str("닫힌 결정?")})
	othDecision := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "OTH", Title: "other", DecisionNeeded: str("다른 프로젝트 결정?")})

	ptr := func(v string) *string { return &v }
	stIn := model.SessionStateWaitingInput
	stApp := model.SessionStateWaitingApproval

	// 1) Active waiting session linked via session.issue to dDirect.
	trDirect := model.Trace{Actor: "claude", SessionID: "s-direct", ClientHost: "desk"}
	mustTouch(t, s, trDirect, now.Add(-5*time.Minute))
	now = now.Add(-5 * time.Minute)
	if _, err := s.UpdateSession(ctx, trDirect, trDirect.Actor, trDirect.SessionID, SessionPatch{
		Issue: Optional[string]{Set: true, Value: &dDirect.Key},
		State: Optional[string]{Set: true, Value: &stIn},
	}); err != nil {
		t.Fatal(err)
	}

	// Non-waiting active session on dDirect must NOT be attached.
	trActiveNonWait := model.Trace{Actor: "claude", SessionID: "s-active-nowait", ClientHost: "desk"}
	mustTouch(t, s, trActiveNonWait, now)
	if _, err := s.UpdateSession(ctx, trActiveNonWait, trActiveNonWait.Actor, trActiveNonWait.SessionID, SessionPatch{
		Issue: Optional[string]{Set: true, Value: &dDirect.Key},
	}); err != nil {
		t.Fatal(err)
	}

	// Ended waiting session on dDirect must NOT be attached.
	trEnded := model.Trace{Actor: "codex", SessionID: "s-ended", ClientHost: "desk"}
	mustTouch(t, s, trEnded, now)
	if _, err := s.UpdateSession(ctx, trEnded, trEnded.Actor, trEnded.SessionID, SessionPatch{
		Issue: Optional[string]{Set: true, Value: &dDirect.Key},
		State: Optional[string]{Set: true, Value: &stIn},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSession(ctx, trEnded, trEnded.Actor, trEnded.SessionID, SessionPatch{
		State: Optional[string]{Set: true, Value: nil},
	}); err != nil {
		t.Fatal(err)
	}

	// 2) Multiple waiting sessions on dMulti: test state_at ordering, actor/session tiebreak, and location_conflicts.
	// Reset now to 2026-11-20 12:00, and register earlier + tiebreak sessions.
	base := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	now = base.Add(-20 * time.Minute)
	trMFirst := model.Trace{Actor: "codex", SessionID: "s-m1", ClientHost: "laptop"}
	mustTouch(t, s, trMFirst, now)
	if _, err := s.UpdateSession(ctx, trMFirst, trMFirst.Actor, trMFirst.SessionID, SessionPatch{
		Issue:    Optional[string]{Set: true, Value: &dMulti.Key},
		State:    Optional[string]{Set: true, Value: &stApp},
		Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{RepoRemote: ptr("git@example.com:acme/app.git"), Branch: ptr("feat/dec")}},
	}); err != nil {
		t.Fatal(err)
	}

	// Same state_at for claude/s-b, claude/s-a, codex/s-c -> ordered claude/s-a, claude/s-b, codex/s-c after trMFirst.
	now = base.Add(-10 * time.Minute)
	trMTieB := model.Trace{Actor: "claude", SessionID: "s-b", ClientHost: "desk"}
	trMTieA := model.Trace{Actor: "claude", SessionID: "s-a", ClientHost: "desk"}
	trMTieC := model.Trace{Actor: "codex", SessionID: "s-c", ClientHost: "desk"}
	for _, tr := range []model.Trace{trMTieB, trMTieA, trMTieC} {
		mustTouch(t, s, tr, now)
	}
	if _, err := s.UpdateSession(ctx, trMTieB, trMTieB.Actor, trMTieB.SessionID, SessionPatch{
		Issue:    Optional[string]{Set: true, Value: &dMulti.Key},
		State:    Optional[string]{Set: true, Value: &stIn},
		Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{RepoRemote: ptr("https://example.com/acme/app.git"), Branch: ptr("feat/dec")}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSession(ctx, trMTieA, trMTieA.Actor, trMTieA.SessionID, SessionPatch{
		Issue: Optional[string]{Set: true, Value: &dMulti.Key},
		State: Optional[string]{Set: true, Value: &stIn},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSession(ctx, trMTieC, trMTieC.Actor, trMTieC.SessionID, SessionPatch{
		Issue: Optional[string]{Set: true, Value: &dMulti.Key},
		State: Optional[string]{Set: true, Value: &stIn},
	}); err != nil {
		t.Fatal(err)
	}

	// 3) Stale session (>30m last_seen_at) with valid waiting (<24h) linked via newest open issue_claim:
	// has session.issue=dDirect AND an earlier open claim on dEmpty, then claims dClaimed -> matches ONLY dClaimed!
	now = base.Add(-2 * time.Hour)
	trStaleClaim := model.Trace{Actor: "claude", SessionID: "s-stale-claim", ClientHost: "desk"}
	mustTouch(t, s, trStaleClaim, now)
	if _, _, err := s.ClaimIssue(ctx, trStaleClaim, dEmpty.Key); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ClaimIssue(ctx, trStaleClaim, dClaimed.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSession(ctx, trStaleClaim, trStaleClaim.Actor, trStaleClaim.SessionID, SessionPatch{
		Issue: Optional[string]{Set: true, Value: &dDirect.Key},
		State: Optional[string]{Set: true, Value: &stIn},
	}); err != nil {
		t.Fatal(err)
	}

	// 4) Waiting sessions on non-decision, closed decision, and other-project decision issues must not cause inclusion.
	now = base.Add(-3 * time.Minute)
	for idx, key := range []string{nonDecision.Key, closedDecision.Key, othDecision.Key} {
		tr := model.Trace{Actor: "claude", SessionID: "s-excl-" + itoa(idx), ClientHost: "desk"}
		mustTouch(t, s, tr, now)
		if _, err := s.UpdateSession(ctx, tr, tr.Actor, tr.SessionID, SessionPatch{
			Issue: Optional[string]{Set: true, Value: &key},
			State: Optional[string]{Set: true, Value: &stIn},
		}); err != nil {
			t.Fatal(err)
		}
	}

	now = base
	o, err := s.Overview(ctx, "AGB", 10)
	if err != nil {
		t.Fatal(err)
	}
	ag, err := s.Agenda(ctx, []string{"AGB"}, 10)
	if err != nil {
		t.Fatal(err)
	}

	checkDecisions := func(name string, got Section[model.DecisionItem]) {
		t.Helper()
		wantKeys := []string{dDirect.Key, dMulti.Key, dClaimed.Key, dEmpty.Key}
		if got.Total != 4 || !reflect.DeepEqual(keysOf(got.Items, func(i model.DecisionItem) string { return i.Key }), wantKeys) {
			t.Fatalf("%s decisions keys = %v (total %d), want %v", name, keysOf(got.Items, func(i model.DecisionItem) string { return i.Key }), got.Total, wantKeys)
		}
		// dDirect: only trDirect (not non-waiting, not ended, not trStaleClaim whose open claim overrides session.issue)
		if ws := got.Items[0].WaitingSessions; len(ws) != 1 || ws[0].Actor != "claude" || ws[0].SessionID != "s-direct" {
			t.Errorf("%s dDirect waiting_sessions = %+v", name, ws)
		} else {
			if ws[0].LocationConflicts == nil || len(ws[0].LocationConflicts) != 0 {
				t.Errorf("%s dDirect location_conflicts = %#v, want empty non-nil", name, ws[0].LocationConflicts)
			}
			if ws[0].Children != nil {
				t.Errorf("%s dDirect waiting session must not carry children", name)
			}
		}
		// dMulti: deterministic order + location_conflicts
		wsMulti := got.Items[1].WaitingSessions
		wantIDs := []string{"codex/s-m1", "claude/s-a", "claude/s-b", "codex/s-c"}
		gotIDs := keysOf(wsMulti, func(sess model.Session) string { return sess.Actor + "/" + sess.SessionID })
		if !reflect.DeepEqual(gotIDs, wantIDs) {
			t.Errorf("%s dMulti order = %v, want %v", name, gotIDs, wantIDs)
		}
		if len(wsMulti) == 4 {
			if want := []model.SessionRef{{Actor: "claude", SessionID: "s-b"}}; !reflect.DeepEqual(wsMulti[0].LocationConflicts, want) {
				t.Errorf("%s dMulti[0] location_conflicts = %+v, want %+v", name, wsMulti[0].LocationConflicts, want)
			}
			if want := []model.SessionRef{{Actor: "codex", SessionID: "s-m1"}}; !reflect.DeepEqual(wsMulti[2].LocationConflicts, want) {
				t.Errorf("%s dMulti[2] location_conflicts = %+v, want %+v", name, wsMulti[2].LocationConflicts, want)
			}
			if wsMulti[1].LocationConflicts == nil || len(wsMulti[1].LocationConflicts) != 0 {
				t.Errorf("%s dMulti[1] location_conflicts = %#v, want empty non-nil", name, wsMulti[1].LocationConflicts)
			}
		}
		// dClaimed: stale session (>30m) with valid waiting (<24h) via open claim
		if ws := got.Items[2].WaitingSessions; len(ws) != 1 || ws[0].SessionID != "s-stale-claim" || !ws[0].Stale {
			t.Errorf("%s dClaimed waiting_sessions = %+v", name, ws)
		}
		// dEmpty: non-nil empty slice
		if ws := got.Items[3].WaitingSessions; ws == nil || len(ws) != 0 {
			t.Errorf("%s dEmpty waiting_sessions = %#v, want non-nil empty slice", name, ws)
		}
	}

	checkDecisions("overview", o.Decisions)
	checkDecisions("agenda", ag.Projects[0].UserTurn.Decisions)

	// UserTurn.WaitingSessions remains intact alongside Decisions.
	if ag.Projects[0].UserTurn.WaitingSessions.Total != 8 {
		t.Errorf("agenda waiting_sessions total = %d, want 8", ag.Projects[0].UserTurn.WaitingSessions.Total)
	}

	// JSON serialization of empty waiting_sessions and location_conflicts must be [] not null.
	raw, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"waiting_sessions":[]`) {
		t.Errorf("marshaled overview lacks empty waiting_sessions []: %s", raw)
	}
	if strings.Contains(string(raw), `"waiting_sessions":null`) || strings.Contains(string(raw), `"location_conflicts":null`) {
		t.Errorf("marshaled overview has null array: %s", raw)
	}
}

func TestDecisionWaitingSessionsClaimLifecycleAndExpiry(t *testing.T) {
	s, ctx := seeded(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	clockAt(s, &now)
	mustProject(t, s, ctx, "AGB")

	dClaimed := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "claimed", Priority: model.P0, DecisionNeeded: str("점유 질문?")})
	dFallback := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "fallback", Priority: model.P1, DecisionNeeded: str("기본 질문?")})

	tr := model.Trace{Actor: "claude", SessionID: "s-life", ClientHost: "desk"}
	mustTouch(t, s, tr, now)
	if _, _, err := s.ClaimIssue(ctx, tr, dClaimed.Key); err != nil {
		t.Fatal(err)
	}
	stIn := model.SessionStateWaitingInput
	if _, err := s.UpdateSession(ctx, tr, tr.Actor, tr.SessionID, SessionPatch{
		Issue: Optional[string]{Set: true, Value: &dFallback.Key},
		State: Optional[string]{Set: true, Value: &stIn},
	}); err != nil {
		t.Fatal(err)
	}

	assertWaiting := func(where string, wantClaimed, wantFallback int) {
		t.Helper()
		o, err := s.Overview(ctx, "AGB", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(o.Decisions.Items[0].WaitingSessions) != wantClaimed || len(o.Decisions.Items[1].WaitingSessions) != wantFallback {
			t.Errorf("%s: claimed=%d fallback=%d, want (%d, %d)", where,
				len(o.Decisions.Items[0].WaitingSessions), len(o.Decisions.Items[1].WaitingSessions), wantClaimed, wantFallback)
		}
		if o.Decisions.Items[0].WaitingSessions == nil || o.Decisions.Items[1].WaitingSessions == nil {
			t.Errorf("%s: nil WaitingSessions slice", where)
		}
	}

	assertWaiting("open claim overrides session.issue", 1, 0)

	// Releasing the claim falls back to session.issue (dFallback).
	if _, err := s.ReleaseClaim(ctx, tr, dClaimed.Key, model.ClaimOutcomeAbandoned, nil); err != nil {
		t.Fatal(err)
	}
	assertWaiting("after release claim", 0, 1)

	// Superseded claim (taken over by owner) also falls back to session.issue (dFallback).
	if _, _, err := s.ClaimIssue(ctx, tr, dClaimed.Key); err != nil {
		t.Fatal(err)
	}
	assertWaiting("re-claimed", 1, 0)
	doing := model.StatusDoing
	if _, _, err := s.UpdateIssue(ctx, trOwner, dClaimed.Key, IssuePatch{Status: &doing}, nil); err != nil {
		t.Fatal(err)
	}
	assertWaiting("after taken_over claim", 0, 1)

	// At exact 24h boundary on dFallback via session.issue: still attached.
	now = now.Add(model.SessionWaitingHold)
	assertWaiting("at exact 24h boundary", 0, 1)

	// Past 24h (>24h): excluded even if last_seen_at was touched recently.
	now = now.Add(time.Millisecond)
	mustTouch(t, s, tr, now)
	assertWaiting("past 24h waiting hold even with fresh last_seen_at", 0, 0)
}
