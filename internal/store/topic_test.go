package store

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func mustTopic(t *testing.T, s *Store, project, name string, parent *int64) *model.Topic {
	t.Helper()
	tp, err := s.CreateTopic(t.Context(), trOwner, project, TopicInput{Name: name, Parent: parent})
	if err != nil {
		t.Fatalf("CreateTopic(%s, %s): %v", project, name, err)
	}
	return tp
}

func idp(id int64) *int64 { return &id }

func violationFields(err error) []string {
	var verr *model.ValidationError
	if !errors.As(err, &verr) {
		return nil
	}
	out := make([]string, len(verr.Fields))
	for i, f := range verr.Fields {
		out[i] = f.Field
	}
	return out
}

func TestCreateAndListTopics(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	flow := mustTopic(t, s, "AGB", "이슈 흐름", nil)
	triage := mustTopic(t, s, "AGB", "트리아지", &flow.ID)
	reason := mustTopic(t, s, "AGB", "상태 사유", &flow.ID)
	infra := mustTopic(t, s, "AGB", "인프라", nil)
	ci := mustTopic(t, s, "AGB", "배포", &infra.ID)
	mustTopic(t, s, "OTH", "이슈 흐름", nil) // the same name in another project is fine

	if flow.Project != "AGB" || flow.Name != "이슈 흐름" || flow.Parent != nil || flow.Path != "이슈 흐름" || flow.Status != model.TopicActive {
		t.Errorf("top-level topic = %+v", flow)
	}
	if triage.Parent == nil || *triage.Parent != flow.ID || triage.Path != "이슈 흐름/트리아지" || triage.CreatedBy != "owner" || triage.UpdatedBy != "owner" {
		t.Errorf("child topic = %+v", triage)
	}
	if triage.CreatedAt.IsZero() || triage.CreatedAt != triage.UpdatedAt {
		t.Errorf("timestamps = %v %v", triage.CreatedAt, triage.UpdatedAt)
	}
	got, err := s.GetTopic(ctx, triage.ID)
	if err != nil || !reflect.DeepEqual(got, triage) {
		t.Errorf("GetTopic = %+v, %v", got, err)
	}
	if _, err := s.GetTopic(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing topic err = %v", err)
	}

	// Parents by name, each followed by its children by name.
	items, err := s.ListTopics(ctx, "AGB", []model.TopicStatus{model.TopicActive})
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(items))
	for i, it := range items {
		paths[i] = it.Path
	}
	want := []string{"이슈 흐름", "이슈 흐름/상태 사유", "이슈 흐름/트리아지", "인프라", "인프라/배포"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("list order = %v, want %v", paths, want)
	}
	_ = reason
	_ = ci
	if _, err := s.ListTopics(ctx, "NOPE", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing project err = %v", err)
	}
	empty, err := s.ListTopics(ctx, "OTH", []model.TopicStatus{model.TopicDeleted})
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("empty list must be [] not nil: %#v %v", empty, err)
	}
}

func TestTopicRules(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	flow := mustTopic(t, s, "AGB", "이슈 흐름", nil)
	triage := mustTopic(t, s, "AGB", "트리아지", &flow.ID)
	other := mustTopic(t, s, "OTH", "다른 프로젝트", nil)

	create := func(name string, parent *int64) error {
		_, err := s.CreateTopic(ctx, trOwner, "AGB", TopicInput{Name: name, Parent: parent})
		return err
	}
	// Names: 1 to 50 characters, no slash.
	for name, ok := range map[string]bool{"가": true, strings.Repeat("가", 50): true, "": false, strings.Repeat("가", 51): false, "a/b": false, "/": false} {
		err := create(name, nil)
		if ok && err != nil || !ok && !reflect.DeepEqual(violationFields(err), []string{"name"}) {
			t.Errorf("name %q: err = %v", name, err)
		}
	}
	// Two levels only, in this project, under an active top-level topic.
	for name, parent := range map[string]*int64{"주제 아래": &triage.ID, "다른 프로젝트 아래": &other.ID, "없는 부모": idp(9999)} {
		if err := create(name, parent); !reflect.DeepEqual(violationFields(err), []string{"parent"}) {
			t.Errorf("parent for %q: err = %v", name, err)
		}
	}
	// Sibling names must differ while both are active; other parents and top level are separate.
	if err := create("트리아지", &flow.ID); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("duplicate child err = %v", err)
	}
	if err := create("이슈 흐름", nil); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("duplicate top-level err = %v", err)
	}
	if err := create("트리아지", nil); err != nil {
		t.Errorf("the same name at another level: %v", err)
	}
	if err := create("이슈 흐름", &flow.ID); err != nil {
		t.Errorf("a child may share its parent's name: %v", err)
	}
	// A deleted sibling frees the name.
	deleted := model.TopicDeleted
	if _, err := s.UpdateTopic(ctx, trOwner, triage.ID, TopicPatch{Status: &deleted}); err != nil {
		t.Fatal(err)
	}
	if err := create("트리아지", &flow.ID); err != nil {
		t.Errorf("name of a deleted sibling: %v", err)
	}
	// Writes to a project that is not active are refused.
	archived := model.ProjectArchived
	if _, err := s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &archived}); err != nil {
		t.Fatal(err)
	}
	if err := create("보관 중", nil); !errors.Is(err, ErrProjectNotActive) {
		t.Errorf("archived project create err = %v", err)
	}
	if _, err := s.UpdateTopic(ctx, trOwner, flow.ID, TopicPatch{Name: str("바꾼 이름")}); !errors.Is(err, ErrProjectNotActive) {
		t.Errorf("archived project update err = %v", err)
	}
	if _, err := s.CreateTopic(ctx, trOwner, "NOPE", TopicInput{Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing project err = %v", err)
	}
}

func TestUpdateTopic(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	flow := mustTopic(t, s, "AGB", "이슈 흐름", nil)
	infra := mustTopic(t, s, "AGB", "인프라", nil)
	triage := mustTopic(t, s, "AGB", "트리아지", &flow.ID)

	// Rename.
	got, err := s.UpdateTopic(ctx, trClaude, triage.ID, TopicPatch{Name: str("분류")})
	if err != nil || got.Name != "분류" || got.Path != "이슈 흐름/분류" || got.UpdatedBy != "claude" || !triage.UpdatedAt.Before(got.UpdatedAt) || got.CreatedBy != "owner" {
		t.Fatalf("rename = %+v, %v", got, err)
	}
	// A patch that changes nothing leaves updated_at alone.
	same, err := s.UpdateTopic(ctx, trOwner, triage.ID, TopicPatch{Name: str("분류"), Parent: Optional[int64]{Set: true, Value: &flow.ID}})
	if err != nil || same.UpdatedAt != got.UpdatedAt || same.UpdatedBy != "claude" {
		t.Errorf("no-op = %+v, %v", same, err)
	}
	// Move under another top-level topic; a duplicate there is refused.
	moved, err := s.UpdateTopic(ctx, trOwner, triage.ID, TopicPatch{Parent: Optional[int64]{Set: true, Value: &infra.ID}})
	if err != nil || moved.Path != "인프라/분류" || *moved.Parent != infra.ID {
		t.Fatalf("move = %+v, %v", moved, err)
	}
	mustTopic(t, s, "AGB", "분류", &flow.ID)
	if _, err := s.UpdateTopic(ctx, trOwner, triage.ID, TopicPatch{Parent: Optional[int64]{Set: true, Value: &flow.ID}}); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("move into a duplicate err = %v", err)
	}
	// Promote to a top-level topic.
	promoted, err := s.UpdateTopic(ctx, trOwner, triage.ID, TopicPatch{Parent: Optional[int64]{Set: true}})
	if err != nil || promoted.Parent != nil || promoted.Path != "분류" {
		t.Fatalf("promote = %+v, %v", promoted, err)
	}
	// A top-level topic with children cannot get a parent; nothing can be its own parent or sit under a child.
	child := mustTopic(t, s, "AGB", "하위", &flow.ID)
	for name, patch := range map[string]TopicPatch{
		"has children": {Parent: Optional[int64]{Set: true, Value: &infra.ID}},
	} {
		if _, err := s.UpdateTopic(ctx, trOwner, flow.ID, patch); !reflect.DeepEqual(violationFields(err), []string{"parent"}) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := s.UpdateTopic(ctx, trOwner, infra.ID, TopicPatch{Parent: Optional[int64]{Set: true, Value: &infra.ID}}); !reflect.DeepEqual(violationFields(err), []string{"parent"}) {
		t.Errorf("own parent err = %v", err)
	}
	if _, err := s.UpdateTopic(ctx, trOwner, infra.ID, TopicPatch{Parent: Optional[int64]{Set: true, Value: &child.ID}}); !reflect.DeepEqual(violationFields(err), []string{"parent"}) {
		t.Errorf("under a child err = %v", err)
	}
	// Rules for the name hold on update too.
	if _, err := s.UpdateTopic(ctx, trOwner, infra.ID, TopicPatch{Name: str("a/b")}); !reflect.DeepEqual(violationFields(err), []string{"name"}) {
		t.Errorf("slash err = %v", err)
	}
	if _, err := s.UpdateTopic(ctx, trOwner, infra.ID, TopicPatch{Name: str("이슈 흐름")}); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("rename into a duplicate err = %v", err)
	}
	if _, err := s.UpdateTopic(ctx, trOwner, 9999, TopicPatch{Name: str("x")}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing topic err = %v", err)
	}
	bad := model.TopicStatus("gone")
	if _, err := s.UpdateTopic(ctx, trOwner, infra.ID, TopicPatch{Status: &bad}); !reflect.DeepEqual(violationFields(err), []string{"status"}) {
		t.Errorf("unknown status err = %v", err)
	}
}

func TestDeleteAndRestoreTopic(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	flow := mustTopic(t, s, "AGB", "이슈 흐름", nil)
	triage := mustTopic(t, s, "AGB", "트리아지", &flow.ID)
	deleted, active := model.TopicDeleted, model.TopicActive

	// A top-level topic with an active child cannot be deleted.
	if _, err := s.UpdateTopic(ctx, trOwner, flow.ID, TopicPatch{Status: &deleted}); !errors.Is(err, ErrTopicHasChildren) {
		t.Errorf("delete with a child err = %v", err)
	}
	gone, err := s.UpdateTopic(ctx, trOwner, triage.ID, TopicPatch{Status: &deleted})
	if err != nil || gone.Status != model.TopicDeleted {
		t.Fatalf("delete child = %+v, %v", gone, err)
	}
	if _, err := s.UpdateTopic(ctx, trOwner, flow.ID, TopicPatch{Status: &deleted}); err != nil {
		t.Errorf("delete after the child is gone: %v", err)
	}
	// A child cannot be restored under a deleted parent; restore the parent first.
	if _, err := s.UpdateTopic(ctx, trOwner, triage.ID, TopicPatch{Status: &active}); !reflect.DeepEqual(violationFields(err), []string{"status"}) {
		t.Errorf("restore under a deleted parent err = %v", err)
	}
	if _, err := s.CreateTopic(ctx, trOwner, "AGB", TopicInput{Name: "새 하위", Parent: &flow.ID}); !reflect.DeepEqual(violationFields(err), []string{"parent"}) {
		t.Errorf("create under a deleted parent err = %v", err)
	}
	if back, err := s.UpdateTopic(ctx, trOwner, flow.ID, TopicPatch{Status: &active}); err != nil || back.Status != model.TopicActive {
		t.Fatalf("restore parent = %+v, %v", back, err)
	}
	if back, err := s.UpdateTopic(ctx, trOwner, triage.ID, TopicPatch{Status: &active}); err != nil || back.Status != model.TopicActive {
		t.Fatalf("restore child = %+v, %v", back, err)
	}
	// Restoring into a taken name is a duplicate.
	if _, err := s.UpdateTopic(ctx, trOwner, triage.ID, TopicPatch{Status: &deleted}); err != nil {
		t.Fatal(err)
	}
	mustTopic(t, s, "AGB", "트리아지", &flow.ID)
	if _, err := s.UpdateTopic(ctx, trOwner, triage.ID, TopicPatch{Status: &active}); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("restore into a taken name err = %v", err)
	}
	// The list filter separates the two.
	act, _ := s.ListTopics(ctx, "AGB", []model.TopicStatus{model.TopicActive})
	del, _ := s.ListTopics(ctx, "AGB", []model.TopicStatus{model.TopicDeleted})
	all, _ := s.ListTopics(ctx, "AGB", model.TopicStatuses)
	if len(act) != 2 || len(del) != 1 || del[0].ID != triage.ID || len(all) != 3 {
		t.Errorf("active %d deleted %d all %d", len(act), len(del), len(all))
	}
}

func TestIssueTopic(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	flow := mustTopic(t, s, "AGB", "이슈 흐름", nil)
	triage := mustTopic(t, s, "AGB", "트리아지", &flow.ID)
	infra := mustTopic(t, s, "AGB", "인프라", nil)
	foreign := mustTopic(t, s, "OTH", "다른 프로젝트", nil)

	i := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "분류된 이슈", Topic: &triage.ID})
	if i.Topic == nil || *i.Topic != triage.ID || i.TopicPath == nil || *i.TopicPath != "이슈 흐름/트리아지" {
		t.Errorf("created with a topic = %v %v", i.Topic, i.TopicPath)
	}
	plain := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "주제 없음"})
	if plain.Topic != nil || plain.TopicPath != nil {
		t.Errorf("no topic = %v %v", plain.Topic, plain.TopicPath)
	}
	onParent := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "대주제에 붙임", Topic: &flow.ID})
	if onParent.TopicPath == nil || *onParent.TopicPath != "이슈 흐름" {
		t.Errorf("an issue may carry a top-level topic: %v", onParent.TopicPath)
	}
	// The created record lists the topic.
	acts, _, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{i.Key}, Limit: 1})
	if c, ok := acts[0].Changes["topic"]; !ok || string(c.To) != strconv.FormatInt(triage.ID, 10) || c.From != nil {
		t.Errorf("created topic change = %+v (%v)", c, ok)
	}

	// Refusals: another project, missing, deleted.
	for name, id := range map[string]int64{"other project": foreign.ID, "missing": 9999} {
		_, err := s.CreateIssue(ctx, trOwner, IssueInput{Project: "AGB", Title: "x", Topic: idp(id)})
		if !reflect.DeepEqual(violationFields(err), []string{"topic"}) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	deleted := model.TopicDeleted
	if _, err := s.UpdateTopic(ctx, trOwner, infra.ID, TopicPatch{Status: &deleted}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateIssue(ctx, trOwner, IssueInput{Project: "AGB", Title: "x", Topic: &infra.ID}); !reflect.DeepEqual(violationFields(err), []string{"topic"}) {
		t.Errorf("deleted topic err = %v", err)
	}

	// Update: move, clear, and the activity record.
	moved, changed, err := s.UpdateIssue(ctx, trClaude, plain.Key, IssuePatch{Topic: Optional[int64]{Set: true, Value: &triage.ID}}, nil)
	if err != nil || !changed || moved.TopicPath == nil || *moved.TopicPath != "이슈 흐름/트리아지" || moved.Version != 2 {
		t.Fatalf("set topic = %+v changed=%v err=%v", moved, changed, err)
	}
	acts, _, _ = s.ListActivity(ctx, ActivityFilter{Issues: []string{plain.Key}, Limit: 1})
	if c := acts[0].Changes["topic"]; string(c.From) != "null" || string(c.To) != strconv.FormatInt(triage.ID, 10) || len(acts[0].Changes) != 1 {
		t.Errorf("topic change = %+v", acts[0].Changes)
	}
	if _, changed, err := s.UpdateIssue(ctx, trClaude, plain.Key, IssuePatch{Topic: Optional[int64]{Set: true, Value: &triage.ID}}, nil); err != nil || changed {
		t.Errorf("the same topic: changed=%v err=%v", changed, err)
	}
	if _, _, err := s.UpdateIssue(ctx, trClaude, plain.Key, IssuePatch{Topic: Optional[int64]{Set: true, Value: &foreign.ID}}, nil); !reflect.DeepEqual(violationFields(err), []string{"topic"}) {
		t.Errorf("other project topic err = %v", err)
	}
	cleared, changed, err := s.UpdateIssue(ctx, trClaude, plain.Key, IssuePatch{Topic: Optional[int64]{Set: true}}, nil)
	if err != nil || !changed || cleared.Topic != nil || cleared.TopicPath != nil {
		t.Errorf("clear topic = %+v changed=%v err=%v", cleared, changed, err)
	}

	// An issue keeps pointing at a topic that is deleted later, and other edits still work.
	if _, err := s.UpdateTopic(ctx, trOwner, triage.ID, TopicPatch{Status: &deleted}); err != nil {
		t.Fatal(err)
	}
	kept, _, err := s.UpdateIssue(ctx, trClaude, i.Key, IssuePatch{Title: str("제목만 바꿈")}, nil)
	if err != nil || kept.Topic == nil || *kept.Topic != triage.ID || kept.TopicPath == nil || *kept.TopicPath != "이슈 흐름/트리아지" {
		t.Errorf("issue of a deleted topic = %+v err=%v", kept, err)
	}
	// Renaming a topic shows in the path of its issues.
	if _, err := s.UpdateTopic(ctx, trOwner, flow.ID, TopicPatch{Name: str("흐름")}); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.GetIssue(ctx, i.Key); *again.TopicPath != "흐름/트리아지" {
		t.Errorf("path after rename = %q", *again.TopicPath)
	}
	// Summaries carry the same fields.
	items, _, err := s.ListIssues(ctx, IssueFilter{Statuses: model.IssueStatuses, Sort: []Sort{{Field: "key"}}, Limit: 10})
	if err != nil || items[0].Topic == nil || items[0].TopicPath == nil || *items[0].TopicPath != "흐름/트리아지" || items[1].Topic != nil {
		t.Errorf("summary topic fields = %+v err=%v", items, err)
	}
}

func TestListIssuesTopicFilter(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	flow := mustTopic(t, s, "AGB", "이슈 흐름", nil)
	triage := mustTopic(t, s, "AGB", "트리아지", &flow.ID)
	reason := mustTopic(t, s, "AGB", "상태 사유", &flow.ID)
	infra := mustTopic(t, s, "AGB", "인프라", nil)
	onTriage := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "a", Topic: &triage.ID})
	onReason := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "b", Topic: &reason.ID})
	onFlow := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "c", Topic: &flow.ID})
	onInfra := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "d", Topic: &infra.ID})
	none := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "e"})

	list := func(f IssueFilter) []string {
		t.Helper()
		f.Statuses, f.Sort, f.Limit = model.IssueStatuses, []Sort{{Field: "key"}}, 20
		items, _, err := s.ListIssues(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return keysOf(items, func(i model.IssueSummary) string { return i.Key })
	}
	cases := []struct {
		name string
		f    IssueFilter
		want []*model.Issue
	}{
		{"a child topic", IssueFilter{Topics: []int64{triage.ID}}, []*model.Issue{onTriage}},
		{"a top-level topic includes its children", IssueFilter{Topics: []int64{flow.ID}}, []*model.Issue{onTriage, onReason, onFlow}},
		{"several topics are OR", IssueFilter{Topics: []int64{triage.ID, infra.ID}}, []*model.Issue{onTriage, onInfra}},
		{"none", IssueFilter{TopicNone: true}, []*model.Issue{none}},
		{"none and a topic", IssueFilter{TopicNone: true, Topics: []int64{infra.ID}}, []*model.Issue{onInfra, none}},
		{"unknown topic", IssueFilter{Topics: []int64{9999}}, nil},
		{"no filter", IssueFilter{}, []*model.Issue{onTriage, onReason, onFlow, onInfra, none}},
	}
	for _, c := range cases {
		var want []string
		for _, i := range c.want {
			want = append(want, i.Key)
		}
		if got := list(c.f); !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("%s: %v, want %v", c.name, got, want)
		}
	}
	// A deleted child still counts under its top-level topic.
	deleted := model.TopicDeleted
	s.UpdateTopic(ctx, trOwner, triage.ID, TopicPatch{Status: &deleted})
	if got := list(IssueFilter{Topics: []int64{flow.ID}}); len(got) != 3 {
		t.Errorf("deleted child topic: %v", got)
	}
}

func TestTopicWritesChangeTheFingerprint(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	before, _ := s.FingerprintNow(ctx)
	tp := mustTopic(t, s, "AGB", "이슈 흐름", nil)
	afterCreate, _ := s.FingerprintNow(ctx)
	if afterCreate == before {
		t.Error("fingerprint unchanged after a topic was created: a backup would be skipped")
	}
	s.UpdateTopic(ctx, trOwner, tp.ID, TopicPatch{Name: str("흐름")})
	afterRename, _ := s.FingerprintNow(ctx)
	if afterRename == afterCreate {
		t.Error("fingerprint unchanged after a topic was renamed")
	}
}
