package store

import (
	"errors"
	"reflect"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func isAcknowledged(i *model.Issue) bool { return i.AcknowledgedAt != nil }

func TestInitialAcknowledgementFollowsActorKind(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	byUser := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "user"})
	byAgent := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "agent"})

	if byUser.AcknowledgedAt == nil || *byUser.AcknowledgedAt != byUser.CreatedAt || byUser.AcknowledgedBy == nil || *byUser.AcknowledgedBy != "owner" {
		t.Errorf("an issue created by the user is acknowledged at creation: %+v / %+v", byUser.AcknowledgedAt, byUser.AcknowledgedBy)
	}
	if byAgent.AcknowledgedAt != nil || byAgent.AcknowledgedBy != nil {
		t.Errorf("an issue created by an agent starts unacknowledged: %+v / %+v", byAgent.AcknowledgedAt, byAgent.AcknowledgedBy)
	}
	// The created record does not carry the acknowledgement; only later changes do.
	acts, _, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{byUser.Key}, Limit: 5})
	if _, has := acts[0].Changes["acknowledged"]; has {
		t.Errorf("created record has an acknowledged change: %+v", acts[0].Changes)
	}
	// A redeclared actor kind decides from then on.
	if err := s.EnsureActors(ctx, []model.Actor{{ID: "claude", Kind: model.ActorUser}}); err != nil {
		t.Fatal(err)
	}
	if later := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "now a user"}); !isAcknowledged(later) {
		t.Error("the actor kind in the actor table decides the initial state")
	}
}

func TestAcknowledgeAndUnacknowledge(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	i := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "agent work"})

	got, changed, err := s.SetAcknowledged(ctx, trOwner, i.Key, true, nil)
	if err != nil || !changed {
		t.Fatalf("acknowledge: changed=%v err=%v", changed, err)
	}
	if got.AcknowledgedAt == nil || got.AcknowledgedBy == nil || *got.AcknowledgedBy != "owner" || *got.AcknowledgedAt != got.UpdatedAt {
		t.Errorf("acknowledged fields = %v / %v (updated_at %v)", got.AcknowledgedAt, got.AcknowledgedBy, got.UpdatedAt)
	}
	if got.Version != i.Version+1 || got.UpdatedBy != "owner" || got.Status != i.Status || got.StatusChangedAt != i.StatusChangedAt || got.CreatedBy != "claude" {
		t.Errorf("a write bumps the version and the updater only: %+v", got)
	}
	acts, _, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{i.Key}, Limit: 1})
	ch := acts[0].Changes
	if acts[0].Kind != model.ActivityUpdated || acts[0].Actor != "owner" || len(ch) != 1 || string(ch["acknowledged"].From) != `false` || string(ch["acknowledged"].To) != `true` {
		t.Errorf("activity = %+v", acts[0])
	}

	// Acknowledging again writes nothing.
	again, changed, err := s.SetAcknowledged(ctx, trOwner, i.Key, true, nil)
	if err != nil || changed || again.Version != got.Version || !reflect.DeepEqual(again, got) {
		t.Errorf("second acknowledge: changed=%v err=%v version %d", changed, err, again.Version)
	}
	if _, total, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{i.Key}, Limit: 1}); total != 2 {
		t.Errorf("a no-op acknowledge added a record: %d records", total)
	}

	// Later changes by anyone leave the acknowledgement alone.
	doing := model.StatusDoing
	moved, _, err := s.UpdateIssue(ctx, trClaude, i.Key, IssuePatch{Status: &doing, Title: str("renamed")}, nil)
	if err != nil || !isAcknowledged(moved) || *moved.AcknowledgedBy != "owner" || *moved.AcknowledgedAt != *got.AcknowledgedAt {
		t.Errorf("update after acknowledge = %+v, %v", moved, err)
	}

	cleared, changed, err := s.SetAcknowledged(ctx, trOwner, i.Key, false, nil)
	if err != nil || !changed || cleared.AcknowledgedAt != nil || cleared.AcknowledgedBy != nil || cleared.Version != moved.Version+1 {
		t.Errorf("unacknowledge = %+v changed=%v err=%v", cleared, changed, err)
	}
	acts, _, _ = s.ListActivity(ctx, ActivityFilter{Issues: []string{i.Key}, Limit: 1})
	if ch := acts[0].Changes; len(ch) != 1 || string(ch["acknowledged"].From) != `true` || string(ch["acknowledged"].To) != `false` {
		t.Errorf("unacknowledge activity = %+v", acts[0])
	}
	if _, changed, err := s.SetAcknowledged(ctx, trOwner, i.Key, false, nil); err != nil || changed {
		t.Errorf("second unacknowledge: changed=%v err=%v", changed, err)
	}

	// The field filter finds exactly the acknowledgement records.
	_, total, err := s.ListActivity(ctx, ActivityFilter{Field: "acknowledged", Limit: 10})
	if err != nil || total != 2 {
		t.Errorf("records with an acknowledged change = %d, %v", total, err)
	}
}

func TestAcknowledgeRules(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	i := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "a"})

	// If-Match applies, also to a request that would change nothing.
	stale := i.Version + 5
	if _, _, err := s.SetAcknowledged(ctx, trOwner, i.Key, true, &stale); !errors.Is(err, ErrVersionMismatch) {
		t.Errorf("stale If-Match err = %v", err)
	}
	current := i.Version
	got, changed, err := s.SetAcknowledged(ctx, trOwner, i.Key, true, &current)
	if err != nil || !changed || got.Version != current+1 {
		t.Fatalf("matching If-Match: %+v changed=%v err=%v", got, changed, err)
	}
	if _, _, err := s.SetAcknowledged(ctx, trOwner, i.Key, true, &current); !errors.Is(err, ErrVersionMismatch) {
		t.Errorf("no-op with a stale If-Match err = %v", err)
	}
	if _, _, err := s.SetAcknowledged(ctx, trOwner, "AGB-99", true, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing issue err = %v", err)
	}

	// Closed and deleted issues can be acknowledged; a closed project cannot be written.
	del := model.StatusDeleted
	if _, _, err := s.UpdateIssue(ctx, trClaude, i.Key, IssuePatch{Status: &del, StatusReason: str("정리")}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetAcknowledged(ctx, trOwner, i.Key, false, nil); err != nil {
		t.Errorf("deleted issue: %v", err)
	}
	archived := model.ProjectArchived
	if _, err := s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &archived}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetAcknowledged(ctx, trOwner, i.Key, true, nil); !errors.Is(err, ErrProjectNotActive) {
		t.Errorf("archived project err = %v", err)
	}
	if _, changed, err := s.SetAcknowledged(ctx, trOwner, i.Key, false, nil); err != nil || changed {
		t.Errorf("a no-op in an archived project is still fine: changed=%v err=%v", changed, err)
	}
}

// The acknowledgement is a mark for the user. It must not change how issues
// move, queue or block.
func TestAcknowledgementDoesNotAffectTheWorkflow(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	dep := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "dep", Status: model.StatusDoing, Priority: model.P1})
	blocked := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "blocked", Status: model.StatusTodo, DependsOn: []string{dep.Key}})
	ready := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "ready", Status: model.StatusTodo, Priority: model.P0})
	acked := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "acked", Status: model.StatusTodo, Priority: model.P2})

	queueKeys := func() []string {
		items, _, err := s.Queue(ctx, QueueFilter{Statuses: []model.IssueStatus{model.StatusTodo}, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return keysOf(items, func(q model.QueueItem) string { return q.Key + map[bool]string{true: "+", false: "-"}[q.Ready] })
	}
	before := queueKeys()
	overviewBefore, err := s.Overview(ctx, "AGB", 10)
	if err != nil {
		t.Fatal(err)
	}
	if overviewBefore.Blocked.Total != 1 || overviewBefore.Next.Total != 2 || overviewBefore.Doing.Total != 1 {
		t.Fatalf("overview = %+v", overviewBefore)
	}
	if want := []string{ready.Key + "+", acked.Key + "+", blocked.Key + "-"}; !reflect.DeepEqual(before, want) {
		t.Fatalf("queue = %v, want %v", before, want)
	}

	// An unacknowledged issue changes status freely.
	doing, review := model.StatusDoing, model.StatusReview
	if got, _, err := s.UpdateIssue(ctx, trClaude, ready.Key, IssuePatch{Status: &doing}, nil); err != nil || got.AcknowledgedAt != nil {
		t.Fatalf("status change of an unacknowledged issue: %v", err)
	}
	if _, _, err := s.UpdateIssue(ctx, trClaude, ready.Key, IssuePatch{Status: &review}, nil); err != nil {
		t.Fatal(err)
	}
	todo := model.StatusTodo
	if _, _, err := s.UpdateIssue(ctx, trClaude, ready.Key, IssuePatch{Status: &todo}, nil); err != nil {
		t.Fatal(err)
	}

	// Acknowledging every issue leaves the queue and the blocked count as they were.
	for _, key := range []string{dep.Key, blocked.Key, ready.Key} {
		if _, _, err := s.SetAcknowledged(ctx, trOwner, key, true, nil); err != nil {
			t.Fatal(err)
		}
	}
	if after := queueKeys(); !reflect.DeepEqual(after, before) {
		t.Errorf("queue changed by acknowledging: %v -> %v", before, after)
	}
	overviewAfter, _ := s.Overview(ctx, "AGB", 10)
	if overviewAfter.Blocked.Total != 1 || overviewAfter.Next.Total != 2 || overviewAfter.Doing.Total != 1 || overviewAfter.Counts.Open != overviewBefore.Counts.Open {
		t.Errorf("overview changed by acknowledging: %+v", overviewAfter)
	}
}

func TestListIssuesAcknowledgedFilter(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	user := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "user"})
	agentA := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "agent a"})
	agentB := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "agent b"})
	if _, _, err := s.SetAcknowledged(ctx, trOwner, agentB.Key, true, nil); err != nil {
		t.Fatal(err)
	}
	list := func(ack *bool) []string {
		items, _, err := s.ListIssues(ctx, IssueFilter{Statuses: model.IssueStatuses, Acknowledged: ack, Sort: []Sort{{Field: "key"}}, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return keysOf(items, func(i model.IssueSummary) string { return i.Key })
	}
	yes, no := true, false
	if got := list(&yes); !reflect.DeepEqual(got, []string{user.Key, agentB.Key}) {
		t.Errorf("acknowledged = %v", got)
	}
	if got := list(&no); !reflect.DeepEqual(got, []string{agentA.Key}) {
		t.Errorf("unacknowledged = %v", got)
	}
	if got := list(nil); len(got) != 3 {
		t.Errorf("no filter = %v", got)
	}
	items, _, _ := s.ListIssues(ctx, IssueFilter{Statuses: model.IssueStatuses, Acknowledged: &no, Limit: 10})
	if items[0].AcknowledgedAt != nil || items[0].AcknowledgedBy != nil {
		t.Errorf("summary carries acknowledgement fields: %+v", items[0])
	}
}

func TestOverviewUnacknowledged(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	agent := func(title, project string, st model.IssueStatus) *model.Issue {
		return mustIssue(t, s, ctx, trClaude, IssueInput{Title: title, Project: project, Status: st})
	}
	first := agent("first", "AGB", model.StatusBacklog)
	done := agent("done", "AGB", model.StatusDone)
	cancelled := agent("cancelled", "AGB", model.StatusCancelled)
	gone := agent("deleted", "AGB", model.StatusDeleted)
	last := agent("last", "AGB", model.StatusDoing)
	seen := agent("seen", "AGB", model.StatusTodo)
	agent("other project", "OTH", model.StatusBacklog)
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "by the user"})
	if _, _, err := s.SetAcknowledged(ctx, trOwner, seen.Key, true, nil); err != nil {
		t.Fatal(err)
	}

	o, err := s.Overview(ctx, "AGB", 10)
	if err != nil {
		t.Fatal(err)
	}
	// Not deleted, not acknowledged, newest created first.
	want := []string{last.Key, cancelled.Key, done.Key, first.Key}
	got := keysOf(o.Unacknowledged.Items, func(i model.IssueSummary) string { return i.Key })
	if o.Unacknowledged.Total != 4 || !reflect.DeepEqual(got, want) {
		t.Errorf("unacknowledged = %v (total %d), want %v", got, o.Unacknowledged.Total, want)
	}
	_ = gone
	o, _ = s.Overview(ctx, "AGB", 2)
	if o.Unacknowledged.Total != 4 || len(o.Unacknowledged.Items) != 2 || o.Unacknowledged.Items[0].Key != last.Key {
		t.Errorf("limit 2 = %+v", o.Unacknowledged)
	}
	empty, _ := s.Overview(ctx, "OTH", 10)
	if empty.Unacknowledged.Total != 1 {
		t.Errorf("other project = %+v", empty.Unacknowledged)
	}
	mustProject(t, s, ctx, "NEW")
	none, _ := s.Overview(ctx, "NEW", 10)
	if none.Unacknowledged.Total != 0 || none.Unacknowledged.Items == nil {
		t.Errorf("empty section must be [] not null: %+v", none.Unacknowledged)
	}
}
