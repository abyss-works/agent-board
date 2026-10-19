package store

import (
	"context"
	"errors"
	"reflect"
	"sort"
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

	// Later changes that leave the spec alone keep the acknowledgement.
	doing := model.StatusDoing
	moved, _, err := s.UpdateIssue(ctx, trClaude, i.Key, IssuePatch{Status: &doing, Labels: &[]string{"api"}}, nil)
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
	blocked := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "blocked", Status: model.StatusBacklog, DependsOn: []string{dep.Key}})
	ready := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "ready", Status: model.StatusBacklog, Priority: model.P0})
	acked := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "acked", Status: model.StatusBacklog, Priority: model.P2})

	queueKeys := func() []string {
		items, _, err := s.Queue(ctx, QueueFilter{Statuses: []model.IssueStatus{model.StatusBacklog}, Limit: 10})
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
	backlog := model.StatusBacklog
	if _, _, err := s.UpdateIssue(ctx, trClaude, ready.Key, IssuePatch{Status: &backlog}, nil); err != nil {
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
	seen := agent("seen", "AGB", model.StatusReview)
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

// acknowledgedIssue has an agent create an issue and the user acknowledge it.
func acknowledgedIssue(t *testing.T, s *Store, ctx context.Context, in IssueInput) *model.Issue {
	t.Helper()
	i := mustIssue(t, s, ctx, trClaude, in)
	got, _, err := s.SetAcknowledged(ctx, trOwner, i.Key, true, nil)
	if err != nil || !isAcknowledged(got) {
		t.Fatalf("acknowledge %s: %+v, %v", i.Key, got, err)
	}
	return got
}

// ackState describes the acknowledgement of an issue for failure messages.
func ackState(i *model.Issue) string {
	if i.AcknowledgedAt == nil && i.AcknowledgedBy == nil {
		return "unacknowledged"
	}
	by, at := "nobody", "no time"
	if i.AcknowledgedBy != nil {
		by = *i.AcknowledgedBy
	}
	if i.AcknowledgedAt != nil {
		at = i.AcknowledgedAt.String()
	}
	return "acknowledged by " + by + " at " + at
}

// newestChange returns the sorted fields of the newest activity record of an
// issue, and the record.
func newestChange(t *testing.T, s *Store, ctx context.Context, key string) ([]string, model.Activity) {
	t.Helper()
	acts, _, err := s.ListActivity(ctx, ActivityFilter{Issues: []string{key}, Limit: 1})
	if err != nil || len(acts) != 1 {
		t.Fatalf("activity of %s: %v, %v", key, acts, err)
	}
	fields := make([]string, 0, len(acts[0].Changes))
	for f := range acts[0].Changes {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	return fields, acts[0]
}

func hasField(fields []string, want string) bool {
	for _, f := range fields {
		if f == want {
			return true
		}
	}
	return false
}

// A change to what the issue says makes the user's acknowledgement stale: the
// server clears it in the same write and the record of that write says so.
func TestSpecChangeResetsTheAcknowledgement(t *testing.T) {
	doing, design := model.StatusDoing, model.TypeDesign
	cases := []struct {
		name   string
		start  IssueInput
		actor  model.Trace
		patch  IssuePatch
		fields []string // sorted fields of the record
	}{
		{"title", IssueInput{}, trClaude, IssuePatch{Title: str("renamed")}, []string{"acknowledged", "title"}},
		{"body", IssueInput{}, trClaude, IssuePatch{Body: str(tplBody(model.TypeFeature) + "\n보충")}, []string{"acknowledged", "body"}},
		{"type", IssueInput{}, trOwner, IssuePatch{Type: &design}, []string{"acknowledged", "type"}},
		{"review to doing", IssueInput{Status: model.StatusReview}, trClaude, IssuePatch{Status: &doing}, []string{"acknowledged", "status", "status_reason"}},
		{"title together with review to doing", IssueInput{Status: model.StatusReview}, trClaude, IssuePatch{Title: str("renamed"), Status: &doing},
			[]string{"acknowledged", "status", "status_reason", "title"}},
		{"title by the user", IssueInput{}, trOwner, IssuePatch{Title: str("renamed")}, []string{"acknowledged", "title"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, ctx := seeded(t)
			mustProject(t, s, ctx, "AGB")
			before := acknowledgedIssue(t, s, ctx, c.start)

			got, changed, err := s.UpdateIssue(ctx, c.actor, before.Key, c.patch, nil)
			if err != nil || !changed {
				t.Fatalf("update: changed=%v err=%v", changed, err)
			}
			if got.AcknowledgedAt != nil || got.AcknowledgedBy != nil || got.Version != before.Version+1 {
				t.Errorf("after the update: %s, version %d (was %d)", ackState(got), got.Version, before.Version)
			}
			stored, err := s.GetIssue(ctx, before.Key)
			if err != nil {
				t.Fatal(err)
			}
			if stored.AcknowledgedAt != nil || stored.AcknowledgedBy != nil {
				t.Errorf("stored issue: %s", ackState(stored))
			}
			fields, rec := newestChange(t, s, ctx, before.Key)
			ack := rec.Changes["acknowledged"]
			if rec.Kind != model.ActivityUpdated || !reflect.DeepEqual(fields, c.fields) || string(ack.From) != "true" || string(ack.To) != "false" {
				t.Errorf("record = %v, acknowledged %s -> %s; want fields %v and true -> false", fields, ack.From, ack.To, c.fields)
			}

			// The user can acknowledge the new spec.
			again, changed, err := s.SetAcknowledged(ctx, trOwner, before.Key, true, nil)
			if err != nil || !changed || !isAcknowledged(again) {
				t.Errorf("acknowledge again: %s changed=%v err=%v", ackState(again), changed, err)
			}
		})
	}
}

// Every other change leaves the acknowledgement as it was.
func TestOtherChangesKeepTheAcknowledgement(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	other := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "other"})
	topic := mustTopic(t, s, "AGB", "이슈 흐름", nil)
	milestone, err := s.CreateMilestone(ctx, trOwner, "AGB", MilestoneInput{Name: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	doing, review, done, backlog, p0 := model.StatusDoing, model.StatusReview, model.StatusDone, model.StatusBacklog, model.P0
	cases := []struct {
		name  string
		start IssueInput
		patch IssuePatch
	}{
		{"labels", IssueInput{}, IssuePatch{Labels: &[]string{"api"}}},
		{"priority", IssueInput{}, IssuePatch{Priority: &p0}},
		{"parent", IssueInput{}, IssuePatch{Parent: Optional[string]{Set: true, Value: &other.Key}}},
		{"depends_on", IssueInput{}, IssuePatch{DependsOn: &[]string{other.Key}}},
		{"refs", IssueInput{}, IssuePatch{Refs: &[]string{"doc:api.md"}}},
		{"topic", IssueInput{}, IssuePatch{Topic: Optional[int64]{Set: true, Value: &topic.ID}}},
		{"milestone", IssueInput{}, IssuePatch{Milestone: Optional[int64]{Set: true, Value: &milestone.ID}}},
		{"recheck_condition", IssueInput{Type: model.TypeObservation, RecheckCondition: str("다음 릴리즈")},
			IssuePatch{RecheckCondition: Optional[string]{Set: true, Value: str("다음 마일스톤")}}},
		{"decision_needed", IssueInput{}, IssuePatch{DecisionNeeded: Optional[string]{Set: true, Value: str("어느 쪽으로 할지")}}},
		{"decision_needed cleared", IssueInput{DecisionNeeded: str("어느 쪽으로 할지")}, IssuePatch{DecisionNeeded: Optional[string]{Set: true}}},
		{"backlog to doing", IssueInput{}, IssuePatch{Status: &doing}},
		{"doing to review", IssueInput{Status: model.StatusDoing}, IssuePatch{Status: &review}},
		{"review to done", IssueInput{Status: model.StatusReview}, IssuePatch{Status: &done}},
		{"review to backlog", IssueInput{Status: model.StatusReview}, IssuePatch{Status: &backlog}},
	}
	for _, c := range cases {
		before := acknowledgedIssue(t, s, ctx, c.start)
		got, changed, err := s.UpdateIssue(ctx, trClaude, before.Key, c.patch, nil)
		if err != nil || !changed {
			t.Errorf("%s: changed=%v err=%v", c.name, changed, err)
			continue
		}
		if !isAcknowledged(got) || *got.AcknowledgedBy != "owner" || *got.AcknowledgedAt != *before.AcknowledgedAt || got.Version != before.Version+1 {
			t.Errorf("%s: %s, version %d (was %d)", c.name, ackState(got), got.Version, before.Version)
		}
		if fields, _ := newestChange(t, s, ctx, before.Key); hasField(fields, "acknowledged") {
			t.Errorf("%s: the record has an acknowledged change: %v", c.name, fields)
		}
	}

	// A comment does not touch the issue.
	commented := acknowledgedIssue(t, s, ctx, IssueInput{})
	if _, err := s.AddComment(ctx, trClaude, commented.Key, "착수한다.", nil); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetIssue(ctx, commented.Key); err != nil || !reflect.DeepEqual(got, commented) {
		t.Errorf("after a comment: %+v, %v; want %+v", got, err, commented)
	}
}

// Sending the value an issue already has is not a change, so it neither writes
// nor clears the acknowledgement; only the fields that differ count.
func TestUnchangedValuesKeepTheAcknowledgement(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	before := acknowledgedIssue(t, s, ctx, IssueInput{Status: model.StatusReview})

	got, changed, err := s.UpdateIssue(ctx, trClaude, before.Key, IssuePatch{Title: &before.Title, Body: &before.Body, Type: &before.Type, Status: &before.Status}, nil)
	if err != nil || changed || !reflect.DeepEqual(got, before) {
		t.Errorf("repeating every value: changed=%v err=%v issue %+v", changed, err, got)
	}
	if _, total, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{before.Key}, Limit: 10}); total != 2 {
		t.Errorf("records = %d, want 2 (created and acknowledged)", total)
	}

	// The title is repeated but the labels change: a write that keeps the mark.
	got, changed, err = s.UpdateIssue(ctx, trClaude, before.Key, IssuePatch{Title: &before.Title, Labels: &[]string{"api"}}, nil)
	if err != nil || !changed || !isAcknowledged(got) {
		t.Errorf("repeated title with a new label: %+v changed=%v err=%v", got, changed, err)
	}
	if fields, _ := newestChange(t, s, ctx, before.Key); !reflect.DeepEqual(fields, []string{"labels"}) {
		t.Errorf("record = %v, want labels only", fields)
	}
}

// An issue the user has not acknowledged has nothing to clear: the update
// records only its own changes.
func TestSpecChangeOfAnUnacknowledgedIssue(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	i := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "agent work"})

	got, changed, err := s.UpdateIssue(ctx, trClaude, i.Key, IssuePatch{Title: str("renamed")}, nil)
	if err != nil || !changed || got.AcknowledgedAt != nil || got.AcknowledgedBy != nil {
		t.Fatalf("update: %+v changed=%v err=%v", got, changed, err)
	}
	if fields, _ := newestChange(t, s, ctx, i.Key); !reflect.DeepEqual(fields, []string{"title"}) {
		t.Errorf("record = %v, want title only", fields)
	}
}
