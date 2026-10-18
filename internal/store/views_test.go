package store

import (
	"reflect"
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
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "t1", Status: model.StatusTodo})
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
	if len(b.Columns) != 7 {
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
	if b.Columns[1].Total != 1 || b.Columns[2].Total != 0 || b.Columns[4].Total != 1 || b.Columns[6].Total != 1 {
		t.Errorf("totals = %v", keysOf(b.Columns, func(c BoardColumn) string { return string(c.Status) + ":" + itoa(c.Total) }))
	}
	if b.Columns[2].Issues == nil {
		t.Error("empty column must have [] not null")
	}
	b, _ = s.Board(ctx, BoardParams{Projects: []string{"AGB"}, ClosedDays: 0, Limit: 100})
	if b.Columns[4].Total != 2 {
		t.Errorf("closed_days=0 must include old closed issues: %d", b.Columns[4].Total)
	}
	b, _ = s.Board(ctx, BoardParams{Projects: []string{"AGB"}, ClosedDays: 7, Limit: 1})
	if b.Columns[0].Total != 2 || len(b.Columns[0].Issues) != 1 {
		t.Errorf("limit per column: total %d items %d", b.Columns[0].Total, len(b.Columns[0].Issues))
	}
	b, _ = s.Board(ctx, BoardParams{ClosedDays: 7, Limit: 100})
	if b.Columns[2].Total != 1 || b.Columns[2].Issues[0].Key != "OTH-1" {
		t.Errorf("all projects doing = %+v", b.Columns[2])
	}
}

func itoa(n int) string { return string(rune('0' + n)) }

func TestQueue(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	dep := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "dep", Status: model.StatusDoing})
	doneDep := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "done dep", Status: model.StatusDone})
	p1blocked := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "p1 blocked", Status: model.StatusTodo, Priority: model.P1, DependsOn: []string{dep.Key, doneDep.Key}})
	p1ready := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "p1 ready", Status: model.StatusTodo, Priority: model.P1, DependsOn: []string{doneDep.Key}})
	p0 := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "p0", Status: model.StatusTodo, Priority: model.P0})
	p2 := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "p2", Status: model.StatusTodo, Priority: model.P2})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "backlog", Status: model.StatusBacklog, Priority: model.P0})

	items, total, err := s.Queue(ctx, QueueFilter{Statuses: []model.IssueStatus{model.StatusTodo}, Limit: 10})
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
	items, _, _ = s.Queue(ctx, QueueFilter{Statuses: []model.IssueStatus{model.StatusTodo}, Limit: 1})
	if len(items) != 1 || items[0].Key != p0.Key {
		t.Errorf("next = %+v", items)
	}
	items, total, _ = s.Queue(ctx, QueueFilter{Statuses: []model.IssueStatus{model.StatusTodo, model.StatusBacklog}, Limit: 10})
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
	items, _, _ = s.Queue(ctx, QueueFilter{Statuses: []model.IssueStatus{model.StatusTodo}, Limit: 10})
	if items[1].Key != p1blocked.Key && items[2].Key != p1blocked.Key || !items[2].Ready {
		t.Errorf("after dep closed = %+v", items)
	}
	v, _ := s.GetIssue(ctx, p1blocked.Key)
	if v.Version != 1 {
		t.Errorf("dependent issue was written: version %d", v.Version)
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
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "blocked", Status: model.StatusTodo, DependsOn: []string{dep.Key}})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "ready", Status: model.StatusTodo, Priority: model.P0})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "decide", Type: model.TypeDecision, Status: model.StatusBacklog})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "decided", Type: model.TypeDecision, Status: model.StatusDone})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "review", Status: model.StatusReview, Type: model.TypeBug})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "gone", Status: model.StatusDeleted})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "other", Project: "OTH", Status: model.StatusDoing})
	s.AddComment(ctx, trClaude, dep.Key, "hi", nil)

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
	if c.ByStatus[model.StatusDoing] != 1 || c.ByStatus[model.StatusTodo] != 2 || c.ByStatus[model.StatusDeleted] != 1 || c.ByStatus[model.StatusCancelled] != 0 || len(c.ByStatus) != 7 {
		t.Errorf("by_status = %v", c.ByStatus)
	}
	if c.ByType[model.TypeDecision] != 1 || c.ByType[model.TypeFeature] != 3 || c.ByType[model.TypeBug] != 1 || len(c.ByType) != 6 {
		t.Errorf("by_type (open only) = %v", c.ByType)
	}
	if c.ByPriority[model.P0] != 1 || c.ByPriority[model.P1] != 1 || c.ByPriority[model.P2] != 3 || len(c.ByPriority) != 4 {
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
	if o.Next.Total != 1 || o.Next.Items[0].Title != "ready" || !o.Next.Items[0].Ready {
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
