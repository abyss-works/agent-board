package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func str(s string) *string { return &s }

func mustProject(t *testing.T, s *Store, ctx context.Context, key string) {
	t.Helper()
	if _, err := s.CreateProject(ctx, trOwner, key, key); err != nil {
		t.Fatal(err)
	}
}

func mustIssue(t *testing.T, s *Store, ctx context.Context, tr model.Trace, in IssueInput) *model.Issue {
	t.Helper()
	if in.Project == "" {
		in.Project = "AGB"
	}
	if in.Title == "" {
		in.Title = "issue"
	}
	if tr == trClaude && in.Body == "" {
		// Agents write to the template of their type; fixtures that say nothing about the body do the same.
		typ := in.Type
		if typ == "" {
			typ = model.DefaultIssueType
		}
		in.Body = tplBody(typ)
	}
	i, err := s.CreateIssue(ctx, tr, in)
	if err != nil {
		t.Fatalf("CreateIssue(%+v): %v", in, err)
	}
	return i
}

func TestCreateIssueIssuesKeysAndDefaults(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	a := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "first"})
	b := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "second", Project: "OTH"})
	c := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "third"})
	if a.Key != "AGB-1" || b.Key != "OTH-1" || c.Key != "AGB-2" || c.Number != 2 || c.Project != "AGB" {
		t.Errorf("keys = %s %s %s", a.Key, b.Key, c.Key)
	}
	if a.Type != model.TypeFeature || a.Status != model.StatusBacklog || a.Priority != model.P2 || a.Body != tplBody(model.TypeFeature) || c.Body != "" || a.Version != 1 {
		t.Errorf("defaults = %+v", a)
	}
	if a.Labels == nil || a.DependsOn == nil || a.Refs == nil || len(a.Labels) != 0 {
		t.Errorf("arrays must be empty, not nil: %+v", a)
	}
	if a.Parent != nil || a.RecheckCondition != nil || a.ClosedAt != nil {
		t.Errorf("nullable defaults = %+v", a)
	}
	if a.CreatedBy != "claude" || a.UpdatedBy != "claude" || a.StatusChangedBy != "claude" || a.StatusChangedAt != a.CreatedAt || a.UpdatedAt != a.CreatedAt {
		t.Errorf("actor stamps = %+v", a)
	}
	got, err := s.GetIssue(ctx, "AGB-2")
	if err != nil || !reflect.DeepEqual(got, c) {
		t.Errorf("GetIssue = %+v, %v; want %+v", got, err, c)
	}
	if _, err := s.GetIssue(ctx, "AGB-99"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing issue err = %v", err)
	}
	if _, err := s.CreateIssue(ctx, trOwner, IssueInput{Project: "NOPE", Title: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing project err = %v", err)
	}
}

func TestCreateIssueFullFieldsAndActivity(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	parent := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "epic"})
	dep := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "dep"})
	in := IssueInput{
		Title: "full", Body: tplBody(model.TypeObservation), Type: model.TypeObservation, Status: model.StatusDoing, Priority: model.P0,
		Labels: []string{"cli", "api", "cli"}, Parent: str(parent.Key), DependsOn: []string{dep.Key},
		Refs: []string{"repo:wsk0715/agent-board", "doc:api.md", "repo:wsk0715/agent-board"}, RecheckCondition: str("when R2 is ready"),
		DecisionNeeded: str("whether to wait for R2"),
	}
	i := mustIssue(t, s, ctx, trClaude, in)
	if i.Key != "AGB-3" || i.Body != tplBody(model.TypeObservation) || i.Type != model.TypeObservation || i.Status != model.StatusDoing || i.Priority != model.P0 {
		t.Errorf("fields = %+v", i)
	}
	if !reflect.DeepEqual(i.Labels, []string{"api", "cli"}) {
		t.Errorf("labels not deduped and sorted: %v", i.Labels)
	}
	if !reflect.DeepEqual(i.Refs, []string{"doc:api.md", "repo:wsk0715/agent-board"}) {
		t.Errorf("refs = %v", i.Refs)
	}
	if i.Parent == nil || *i.Parent != "AGB-1" || !reflect.DeepEqual(i.DependsOn, []string{"AGB-2"}) || *i.RecheckCondition != "when R2 is ready" ||
		i.DecisionNeeded == nil || *i.DecisionNeeded != "whether to wait for R2" {
		t.Errorf("links = %+v", i)
	}

	acts, total, err := s.ListActivity(ctx, ActivityFilter{Issues: []string{"AGB-3"}, Limit: 10})
	if err != nil || total != 1 || len(acts) != 1 {
		t.Fatalf("activity = %+v %d %v", acts, total, err)
	}
	a := acts[0]
	if a.Kind != model.ActivityCreated || a.Actor != "claude" || a.SessionID != "s-claude" || a.ClientHost != "laptop" || a.Issue != "AGB-3" || a.Project != "AGB" || a.Body != nil {
		t.Errorf("created record = %+v", a)
	}
	if a.At != i.CreatedAt {
		t.Errorf("activity at %v != created_at %v", a.At, i.CreatedAt)
	}
	for _, f := range []string{"title", "body", "type", "status", "priority", "labels", "parent", "depends_on", "refs", "recheck_condition", "decision_needed"} {
		ch, ok := a.Changes[f]
		if !ok {
			t.Errorf("created changes missing %s", f)
			continue
		}
		if ch.From != nil {
			t.Errorf("created change %s has from", f)
		}
	}
	if string(a.Changes["labels"].To) != `["api","cli"]` || string(a.Changes["status"].To) != `"doing"` || string(a.Changes["decision_needed"].To) != `"whether to wait for R2"` {
		t.Errorf("changes = %s %s %s", a.Changes["labels"].To, a.Changes["status"].To, a.Changes["decision_needed"].To)
	}
}

func TestCreateIssueValidationAndRefs(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "one"})

	var verr *model.ValidationError
	_, err := s.CreateIssue(ctx, trOwner, IssueInput{Project: "AGB", Title: "", Priority: "P9", Labels: []string{"Bad Label"}, Type: model.TypeObservation})
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	fields := map[string]bool{}
	for _, f := range verr.Fields {
		fields[f.Field] = true
	}
	for _, want := range []string{"title", "priority", "labels[0]", "recheck_condition"} {
		if !fields[want] {
			t.Errorf("validation missing field %s: %+v", want, verr.Fields)
		}
	}

	var rerr *RefError
	_, err = s.CreateIssue(ctx, trOwner, IssueInput{Project: "AGB", Title: "x", Parent: str("AGB-77")})
	if !errors.As(err, &rerr) || rerr.Field != "parent" || rerr.Key != "AGB-77" {
		t.Errorf("missing parent err = %v", err)
	}
	_, err = s.CreateIssue(ctx, trOwner, IssueInput{Project: "AGB", Title: "x", DependsOn: []string{"AGB-1", "AGB-78"}})
	if !errors.As(err, &rerr) || rerr.Field != "depends_on[1]" || rerr.Key != "AGB-78" {
		t.Errorf("missing dep err = %v", err)
	}

	archived := model.ProjectArchived
	if _, err := s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &archived}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateIssue(ctx, trOwner, IssueInput{Project: "AGB", Title: "x"}); !errors.Is(err, ErrProjectNotActive) {
		t.Errorf("archived project err = %v", err)
	}
	// A failed creation must not consume a number.
	active := model.ProjectActive
	s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &active})
	i := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "two"})
	if i.Key != "AGB-2" {
		t.Errorf("key after failures = %s, want AGB-2", i.Key)
	}
}

func TestUpdateIssue(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	i := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "t", Labels: []string{"api"}})

	doing := model.StatusDoing
	got, changed, err := s.UpdateIssue(ctx, trClaude, "AGB-1", IssuePatch{Status: &doing, Labels: &[]string{"cli", "api"}, Title: str("t2")}, nil)
	if err != nil || !changed {
		t.Fatalf("update: %v changed=%v", err, changed)
	}
	if got.Status != model.StatusDoing || got.Title != "t2" || !reflect.DeepEqual(got.Labels, []string{"api", "cli"}) || got.Version != 2 {
		t.Errorf("updated = %+v", got)
	}
	if got.UpdatedBy != "claude" || got.StatusChangedBy != "claude" || got.StatusChangedAt != got.UpdatedAt || !i.UpdatedAt.Before(got.UpdatedAt) || got.CreatedBy != "owner" {
		t.Errorf("stamps = %+v", got)
	}
	acts, _, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{"AGB-1"}, Limit: 10})
	if len(acts) != 2 || acts[0].Kind != model.ActivityUpdated {
		t.Fatalf("activity = %+v", acts)
	}
	// The user created the issue, so it starts acknowledged, and the new title clears that.
	ch := acts[0].Changes
	if len(ch) != 5 || string(ch["status"].From) != `"backlog"` || string(ch["status"].To) != `"doing"` || string(ch["labels"].From) != `["api"]` || string(ch["title"].To) != `"t2"` ||
		string(ch["status_reason"].From) != `null` || string(ch["status_reason"].To) != `"백로그 → 진행"` ||
		string(ch["acknowledged"].From) != `true` || string(ch["acknowledged"].To) != `false` {
		t.Errorf("changes = %+v", ch)
	}

	// Same values: no change, no record, version unchanged.
	got2, changed, err := s.UpdateIssue(ctx, trOwner, "AGB-1", IssuePatch{Status: &doing, Labels: &[]string{"api", "cli"}}, nil)
	if err != nil || changed || got2.Version != 2 || got2.UpdatedBy != "claude" {
		t.Errorf("no-op = %+v changed=%v err=%v", got2, changed, err)
	}
	if _, total, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{"AGB-1"}, Limit: 10}); total != 2 {
		t.Errorf("no-op added activity: %d", total)
	}

	// If-Match.
	v1 := int64(1)
	if _, _, err := s.UpdateIssue(ctx, trOwner, "AGB-1", IssuePatch{Title: str("x")}, &v1); !errors.Is(err, ErrVersionMismatch) {
		t.Errorf("stale If-Match err = %v", err)
	}
	v2 := int64(2)
	if got, _, err = s.UpdateIssue(ctx, trOwner, "AGB-1", IssuePatch{Title: str("x")}, &v2); err != nil || got.Version != 3 {
		t.Errorf("matching If-Match = %+v %v", got, err)
	}
	if _, _, err := s.UpdateIssue(ctx, trOwner, "AGB-9", IssuePatch{Title: str("x")}, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing err = %v", err)
	}
}

func TestUpdateIssueStatusLifecycle(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "t"})
	set := func(st model.IssueStatus) *model.Issue {
		t.Helper()
		patch := IssuePatch{Status: &st}
		if st.ReasonRequired() {
			patch.StatusReason = str("사유")
		}
		got, _, err := s.UpdateIssue(ctx, trClaude, "AGB-1", patch, nil)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	done := set(model.StatusDone)
	if done.ClosedAt == nil || *done.ClosedAt != done.StatusChangedAt {
		t.Errorf("closed_at not set on done: %+v", done)
	}
	deleted := set(model.StatusDeleted)
	if deleted.ClosedAt == nil || *deleted.ClosedAt != *done.ClosedAt {
		t.Errorf("closed_at must be kept between closed statuses: %+v", deleted)
	}
	// Soft deleted issues remain readable.
	if got, err := s.GetIssue(ctx, "AGB-1"); err != nil || got.Status != model.StatusDeleted {
		t.Errorf("deleted issue read = %+v, %v", got, err)
	}
	reopened := set(model.StatusBacklog)
	if reopened.ClosedAt != nil || reopened.StatusChangedBy != "claude" {
		t.Errorf("reopen = %+v", reopened)
	}
	// Writes to issues of a non-active project are rejected, reads are not.
	archived := model.ProjectArchived
	s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &archived})
	st := model.StatusDoing
	if _, _, err := s.UpdateIssue(ctx, trOwner, "AGB-1", IssuePatch{Status: &st}, nil); !errors.Is(err, ErrProjectNotActive) {
		t.Errorf("archived project update err = %v", err)
	}
	if _, err := s.GetIssue(ctx, "AGB-1"); err != nil {
		t.Errorf("archived project read err = %v", err)
	}
}

func TestUpdateIssueNullableAndValidation(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	p := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "parent"})
	i := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "child", Parent: str(p.Key)})

	got, changed, err := s.UpdateIssue(ctx, trOwner, i.Key, IssuePatch{Parent: Optional[string]{Set: true}}, nil)
	if err != nil || !changed || got.Parent != nil {
		t.Errorf("clear parent = %+v %v", got, err)
	}
	acts, _, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{i.Key}, Kinds: []model.ActivityKind{model.ActivityUpdated}, Limit: 1})
	if string(acts[0].Changes["parent"].From) != `"AGB-1"` || string(acts[0].Changes["parent"].To) != `null` {
		t.Errorf("parent change = %+v", acts[0].Changes["parent"])
	}

	obs := model.TypeObservation
	var verr *model.ValidationError
	if _, _, err := s.UpdateIssue(ctx, trOwner, i.Key, IssuePatch{Type: &obs}, nil); !errors.As(err, &verr) || verr.Fields[0].Field != "recheck_condition" {
		t.Errorf("observation without recheck err = %v", err)
	}
	if _, _, err := s.UpdateIssue(ctx, trOwner, i.Key, IssuePatch{Type: &obs, RecheckCondition: Optional[string]{Set: true, Value: str("later")}}, nil); err != nil {
		t.Errorf("observation with recheck err = %v", err)
	}
	feat := model.TypeFeature
	if _, _, err := s.UpdateIssue(ctx, trOwner, i.Key, IssuePatch{Type: &feat}, nil); !errors.As(err, &verr) {
		t.Errorf("feature keeping recheck must fail: %v", err)
	}
	if _, _, err := s.UpdateIssue(ctx, trOwner, i.Key, IssuePatch{Type: &feat, RecheckCondition: Optional[string]{Set: true}}, nil); err != nil {
		t.Errorf("feature clearing recheck err = %v", err)
	}
	var rerr *RefError
	if _, _, err := s.UpdateIssue(ctx, trOwner, i.Key, IssuePatch{DependsOn: &[]string{"AGB-50"}}, nil); !errors.As(err, &rerr) {
		t.Errorf("missing dep err = %v", err)
	}
}

// The todo status and the decision type are gone: no write can set them.
func TestRemovedStatusAndTypeAreRejected(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	i := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "t"})
	todo, decision := model.IssueStatus("todo"), model.IssueType("decision")

	for name, c := range map[string]struct {
		in    IssueInput
		field string
	}{
		"create in todo":       {IssueInput{Project: "AGB", Title: "x", Status: todo}, "status"},
		"create as a decision": {IssueInput{Project: "AGB", Title: "x", Type: decision}, "type"},
	} {
		if _, err := s.CreateIssue(ctx, trOwner, c.in); !hasViolation(err, c.field) {
			t.Errorf("%s: err = %v, want a %s violation", name, err, c.field)
		}
	}
	for name, c := range map[string]struct {
		patch IssuePatch
		field string
	}{
		"update to todo":       {IssuePatch{Status: &todo}, "status"},
		"update to a decision": {IssuePatch{Type: &decision}, "type"},
	} {
		if _, _, err := s.UpdateIssue(ctx, trOwner, i.Key, c.patch, nil); !hasViolation(err, c.field) {
			t.Errorf("%s: err = %v, want a %s violation", name, err, c.field)
		}
	}

	// Refused writes left nothing behind: no issue, no record, no new version.
	cur, err := s.GetIssue(ctx, i.Key)
	if err != nil || cur.Version != 1 || cur.Status != model.StatusBacklog || cur.Type != model.TypeFeature {
		t.Errorf("issue after the refusals = %+v, %v", cur, err)
	}
	if _, total, _ := s.ListActivity(ctx, ActivityFilter{Limit: 10}); total != 1 {
		t.Errorf("records = %d, want only the creation", total)
	}
	if next := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "next"}); next.Key != "AGB-2" {
		t.Errorf("key after the refusals = %s, want AGB-2", next.Key)
	}
}

func hasViolation(err error, field string) bool {
	var verr *model.ValidationError
	if !errors.As(err, &verr) {
		return false
	}
	for _, f := range verr.Fields {
		if f.Field == field {
			return true
		}
	}
	return false
}

func TestDecisionNeeded(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	set := func(q string) IssuePatch { return IssuePatch{DecisionNeeded: Optional[string]{Set: true, Value: &q}} }
	clear := IssuePatch{DecisionNeeded: Optional[string]{Set: true}}
	record := func(key string) model.Change {
		t.Helper()
		acts, _, err := s.ListActivity(ctx, ActivityFilter{Issues: []string{key}, Limit: 1})
		if err != nil || len(acts) != 1 {
			t.Fatalf("activity of %s: %v, %v", key, acts, err)
		}
		return acts[0].Changes["decision_needed"]
	}

	// A question can be given when the issue is created, and the created record holds it, null or not.
	asked := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "asks", DecisionNeeded: str("SQLite로 둘지")})
	plain := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "plain"})
	if asked.DecisionNeeded == nil || *asked.DecisionNeeded != "SQLite로 둘지" || plain.DecisionNeeded != nil {
		t.Fatalf("created: %v and %v", asked.DecisionNeeded, plain.DecisionNeeded)
	}
	if c := record(asked.Key); c.From != nil || string(c.To) != `"SQLite로 둘지"` {
		t.Errorf("created record of the question = %+v", c)
	}
	if c := record(plain.Key); c.From != nil || string(c.To) != `null` {
		t.Errorf("created record without a question = %+v", c)
	}

	// Set, change and clear are each one change with the value before and after.
	for _, step := range []struct {
		name     string
		patch    IssuePatch
		from, to string
		want     *string
	}{
		{"set", set("두 안 중 어느 쪽으로 할지"), `null`, `"두 안 중 어느 쪽으로 할지"`, str("두 안 중 어느 쪽으로 할지")},
		{"change", set("다른 질문"), `"두 안 중 어느 쪽으로 할지"`, `"다른 질문"`, str("다른 질문")},
		{"clear", clear, `"다른 질문"`, `null`, nil},
	} {
		got, changed, err := s.UpdateIssue(ctx, trClaude, plain.Key, step.patch, nil)
		if err != nil || !changed || !reflect.DeepEqual(got.DecisionNeeded, step.want) {
			t.Fatalf("%s: %v changed=%v err=%v", step.name, got.DecisionNeeded, changed, err)
		}
		if c := record(plain.Key); string(c.From) != step.from || string(c.To) != step.to {
			t.Errorf("%s: record = %s -> %s, want %s -> %s", step.name, c.From, c.To, step.from, step.to)
		}
		acts, _, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{plain.Key}, Limit: 1})
		if len(acts[0].Changes) != 1 {
			t.Errorf("%s: the record holds %d changes, want only decision_needed", step.name, len(acts[0].Changes))
		}
		if reread, err := s.GetIssue(ctx, plain.Key); err != nil || !reflect.DeepEqual(reread, got) {
			t.Errorf("%s: stored %+v, %v", step.name, reread, err)
		}
	}
	// Writing the same value, or clearing what is not there, is no change.
	if _, changed, err := s.UpdateIssue(ctx, trClaude, plain.Key, clear, nil); err != nil || changed {
		t.Errorf("clear again: changed=%v err=%v", changed, err)
	}
	if _, changed, err := s.UpdateIssue(ctx, trClaude, asked.Key, set("SQLite로 둘지"), nil); err != nil || changed {
		t.Errorf("same question: changed=%v err=%v", changed, err)
	}

	// The question is a summary field: lists carry it.
	items, _, err := s.ListIssues(ctx, IssueFilter{Statuses: model.OpenStatuses, Sort: []Sort{{Field: "key"}}, Limit: 10})
	if err != nil || len(items) != 2 || items[0].DecisionNeeded == nil || *items[0].DecisionNeeded != "SQLite로 둘지" || items[1].DecisionNeeded != nil {
		t.Errorf("list items = %+v, %v", items, err)
	}

	// One to 500 characters; the limit counts characters, not bytes.
	for name, q := range map[string]string{"empty": "", "501 characters": strings.Repeat("가", 501)} {
		if _, _, err := s.UpdateIssue(ctx, trClaude, plain.Key, set(q), nil); !hasViolation(err, "decision_needed") {
			t.Errorf("update with %s: err = %v", name, err)
		}
		if _, err := s.CreateIssue(ctx, trClaude, IssueInput{Project: "AGB", Title: "x", Body: tplBody(model.TypeFeature), DecisionNeeded: str(q)}); !hasViolation(err, "decision_needed") {
			t.Errorf("create with %s: err = %v", name, err)
		}
	}
	long := strings.Repeat("가", 500)
	if got, _, err := s.UpdateIssue(ctx, trClaude, plain.Key, set(long), nil); err != nil || *got.DecisionNeeded != long {
		t.Errorf("500 characters: %v", err)
	}
	// Only the creation, the three steps and the 500 character write were recorded.
	if _, total, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{plain.Key}, Limit: 20}); total != 5 {
		t.Errorf("records of the plain issue = %d, want 5", total)
	}
}

func TestCycles(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	a := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "a"})
	b := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "b", Parent: str(a.Key)})
	c := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "c", Parent: str(b.Key)})

	// a -> b -> c is the parent chain; making a's parent c closes the loop.
	if _, _, err := s.UpdateIssue(ctx, trOwner, a.Key, IssuePatch{Parent: Optional[string]{Set: true, Value: str(c.Key)}}, nil); !errors.Is(err, ErrCycle) {
		t.Errorf("parent cycle err = %v", err)
	}
	if _, _, err := s.UpdateIssue(ctx, trOwner, a.Key, IssuePatch{Parent: Optional[string]{Set: true, Value: str(a.Key)}}, nil); !errors.Is(err, ErrCycle) {
		t.Errorf("self parent err = %v", err)
	}

	// Dependencies: c depends on b, b depends on a; a depending on c is a cycle.
	if _, _, err := s.UpdateIssue(ctx, trOwner, c.Key, IssuePatch{DependsOn: &[]string{b.Key}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpdateIssue(ctx, trOwner, b.Key, IssuePatch{DependsOn: &[]string{a.Key}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpdateIssue(ctx, trOwner, a.Key, IssuePatch{DependsOn: &[]string{c.Key}}, nil); !errors.Is(err, ErrCycle) {
		t.Errorf("dependency cycle err = %v", err)
	}
	if _, _, err := s.UpdateIssue(ctx, trOwner, a.Key, IssuePatch{DependsOn: &[]string{a.Key}}, nil); !errors.Is(err, ErrCycle) {
		t.Errorf("self dependency err = %v", err)
	}
	// Cross-project dependency without cycle is fine.
	mustProject(t, s, ctx, "OTH")
	o := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "OTH", Title: "o", DependsOn: []string{a.Key}})
	if !reflect.DeepEqual(o.DependsOn, []string{"AGB-1"}) {
		t.Errorf("cross project dep = %v", o.DependsOn)
	}
}

func TestListIssuesFilters(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	i1 := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "alpha", Priority: model.P1, Labels: []string{"api"}, Status: model.StatusDoing})
	i2 := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "beta", Priority: model.P0, Labels: []string{"api", "cli"}, Type: model.TypeBug})
	i3 := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "gamma", Project: "OTH", Status: model.StatusDone, Parent: nil})
	i4 := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "delta", Parent: str(i1.Key), Status: model.StatusDeleted})

	list := func(f IssueFilter) []string {
		t.Helper()
		if f.Limit == 0 {
			f.Limit = 50
		}
		items, _, err := s.ListIssues(ctx, f)
		if err != nil {
			t.Fatalf("ListIssues(%+v): %v", f, err)
		}
		keys := make([]string, len(items))
		for n, it := range items {
			keys[n] = it.Key
		}
		return keys
	}
	open := model.OpenStatuses
	all := model.IssueStatuses
	if got := list(IssueFilter{Statuses: open}); !reflect.DeepEqual(got, []string{i2.Key, i1.Key}) {
		t.Errorf("open default sort priority,created = %v", got)
	}
	if got := list(IssueFilter{Statuses: all}); len(got) != 4 {
		t.Errorf("all = %v", got)
	}
	if got := list(IssueFilter{Statuses: all, Projects: []string{"OTH"}}); !reflect.DeepEqual(got, []string{i3.Key}) {
		t.Errorf("project = %v", got)
	}
	if got := list(IssueFilter{Statuses: all, Labels: []string{"api"}}); len(got) != 2 {
		t.Errorf("label api = %v", got)
	}
	if got := list(IssueFilter{Statuses: all, Labels: []string{"api", "cli"}}); !reflect.DeepEqual(got, []string{i2.Key}) {
		t.Errorf("label api AND cli = %v", got)
	}
	if got := list(IssueFilter{Statuses: all, Types: []model.IssueType{model.TypeBug}}); !reflect.DeepEqual(got, []string{i2.Key}) {
		t.Errorf("type = %v", got)
	}
	if got := list(IssueFilter{Statuses: all, Priorities: []model.Priority{model.P1, model.P2}}); len(got) != 3 {
		t.Errorf("priority = %v", got)
	}
	if got := list(IssueFilter{Statuses: all, Parent: str(i1.Key)}); !reflect.DeepEqual(got, []string{i4.Key}) {
		t.Errorf("parent = %v", got)
	}
	if got := list(IssueFilter{Statuses: all, Parent: str("none")}); len(got) != 3 {
		t.Errorf("parent none = %v", got)
	}
	if got := list(IssueFilter{Statuses: all, CreatedBy: []string{"claude"}}); !reflect.DeepEqual(got, []string{i2.Key}) {
		t.Errorf("created_by = %v", got)
	}
	if got := list(IssueFilter{Statuses: []model.IssueStatus{model.StatusDeleted}}); !reflect.DeepEqual(got, []string{i4.Key}) {
		t.Errorf("deleted visible via status filter = %v", got)
	}
	if got := list(IssueFilter{Statuses: all, Sort: []Sort{{Field: "created_at", Desc: true}}, Limit: 2}); !reflect.DeepEqual(got, []string{i4.Key, i3.Key}) {
		t.Errorf("-created_at limit 2 = %v", got)
	}
	if got := list(IssueFilter{Statuses: all, Sort: []Sort{{Field: "key"}}}); !reflect.DeepEqual(got, []string{"AGB-1", "AGB-2", "AGB-3", "OTH-1"}) {
		t.Errorf("key sort = %v", got)
	}
	items, total, _ := s.ListIssues(ctx, IssueFilter{Statuses: all, Limit: 1, Offset: 3, Sort: []Sort{{Field: "key"}}})
	if total != 4 || len(items) != 1 || items[0].Key != "OTH-1" {
		t.Errorf("paging = %+v total %d", items, total)
	}
	if items[0].Snippet != nil {
		t.Error("snippet must be absent without a query")
	}
	// Summary items carry arrays and parent.
	items, _, _ = s.ListIssues(ctx, IssueFilter{Statuses: all, Parent: str(i1.Key), Limit: 10})
	if items[0].Parent == nil || *items[0].Parent != i1.Key || items[0].Labels == nil {
		t.Errorf("summary = %+v", items[0])
	}
}

func TestSearch(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "REST API 설계안 작성", Body: "리소스 모델과 경로를 정한다."})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "SQLite 드라이버 검증", Body: "cgo 없는 순수 Go 드라이버에서 FTS5와 WAL 동작을 확인한다."})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "Backup policy", Body: "Decide the backup interval."})

	search := func(q string) []string {
		t.Helper()
		items, _, err := s.ListIssues(ctx, IssueFilter{Statuses: model.IssueStatuses, Query: q, Limit: 10})
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		keys := make([]string, len(items))
		for n, it := range items {
			keys[n] = it.Key
			if it.Snippet == nil {
				t.Errorf("search %q: item %s has no snippet", q, it.Key)
			}
		}
		return keys
	}
	if got := search("설계"); !reflect.DeepEqual(got, []string{"AGB-1"}) {
		t.Errorf("prefix 설계 = %v", got)
	}
	if got := search("드라이버에서"); !reflect.DeepEqual(got, []string{"AGB-2"}) {
		t.Errorf("드라이버에서 = %v", got)
	}
	if got := search("api 설계"); !reflect.DeepEqual(got, []string{"AGB-1"}) {
		t.Errorf("AND terms = %v", got)
	}
	if got := search("api 없음"); len(got) != 0 {
		t.Errorf("AND with absent term = %v", got)
	}
	if got := search(`"backup interval"`); !reflect.DeepEqual(got, []string{"AGB-3"}) {
		t.Errorf("phrase = %v", got)
	}
	if got := search(`agent-board`); len(got) != 0 {
		t.Errorf("hyphen must not error: %v", got)
	}
	if got := search(`AGB-2`); !reflect.DeepEqual(got, []string{"AGB-2"}) {
		t.Errorf("key search = %v", got)
	}
	if got := search(`say "hi"`); len(got) != 0 {
		t.Errorf("inner quotes must be escaped: %v", got)
	}
	items, _, _ := s.ListIssues(ctx, IssueFilter{Statuses: model.IssueStatuses, Query: "backup", Limit: 10})
	if len(items) != 1 || !strings.Contains(*items[0].Snippet, "[") {
		t.Errorf("snippet = %v", items[0].Snippet)
	}
	// Edits are reflected in the index.
	s.UpdateIssue(ctx, trOwner, "AGB-3", IssuePatch{Title: str("Restore drill")}, nil)
	if got := search("restore"); !reflect.DeepEqual(got, []string{"AGB-3"}) {
		t.Errorf("after update = %v", got)
	}
	if _, err := BuildMatch(strings.Repeat("a ", 11)); err == nil {
		t.Error("more than 10 terms must be rejected")
	}
	if m, _ := BuildMatch("   "); m != "" {
		t.Errorf("blank query = %q", m)
	}
}
