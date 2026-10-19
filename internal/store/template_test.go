package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

// tplBody is the template body of an issue type: what an agent writes when it
// has nothing more to say.
func tplBody(typ model.IssueType) string {
	tpl, ok := model.TemplateFor(typ)
	if !ok {
		panic("no template for " + string(typ))
	}
	return tpl.Body
}

func bodyViolation(t *testing.T, err error) string {
	t.Helper()
	var verr *model.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want a validation error", err)
	}
	for _, f := range verr.Fields {
		if f.Field == "body" {
			return f.Message
		}
	}
	t.Fatalf("no body violation in %+v", verr.Fields)
	return ""
}

func TestTemplateCheckOnCreateAppliesToAgents(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")

	// An agent's body must carry the headings of its type.
	for name, in := range map[string]IssueInput{
		"no body":          {Project: "AGB", Title: "x"},
		"free text":        {Project: "AGB", Title: "x", Body: "메모입니다."},
		"another type":     {Project: "AGB", Title: "x", Type: model.TypeBug, Body: tplBody(model.TypeFeature)},
		"missing a middle": {Project: "AGB", Title: "x", Body: "## 배경\n본문\n## 완료 조건\n조건\n"},
	} {
		_, err := s.CreateIssue(ctx, trClaude, in)
		if msg := bodyViolation(t, err); !strings.Contains(msg, "## ") {
			t.Errorf("%s: message %q names no heading", name, msg)
		}
	}
	for _, typ := range model.IssueTypes {
		in := IssueInput{Project: "AGB", Title: "ok " + string(typ), Type: typ, Body: tplBody(typ)}
		if typ == model.TypeObservation {
			in.RecheckCondition = str("다음 릴리즈")
		}
		if _, err := s.CreateIssue(ctx, trClaude, in); err != nil {
			t.Errorf("%s template body: %v", typ, err)
		}
	}
	// The user is not checked.
	for _, in := range []IssueInput{
		{Project: "AGB", Title: "빠른 메모"},
		{Project: "AGB", Title: "자유 본문", Body: "그냥 적는다."},
		{Project: "AGB", Title: "다른 종류의 본문", Type: model.TypeIdea, Body: tplBody(model.TypeBug)},
	} {
		if _, err := s.CreateIssue(ctx, trOwner, in); err != nil {
			t.Errorf("user %q: %v", in.Title, err)
		}
	}
	// A refused creation consumes no number: 5 agent + 3 user issues so far.
	if i := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "next"}); i.Key != "AGB-9" {
		t.Errorf("key after refusals = %s, want AGB-9", i.Key)
	}

	// The template violation comes with the other field violations.
	_, err := s.CreateIssue(ctx, trClaude, IssueInput{Project: "AGB", Title: "", Priority: "P9", Body: "메모"})
	var verr *model.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v", err)
	}
	var fields []string
	for _, f := range verr.Fields {
		fields = append(fields, f.Field)
	}
	if !reflect.DeepEqual(fields, []string{"title", "priority", "body"}) {
		t.Errorf("violations = %v, want title, priority, body", fields)
	}
}

func TestTemplateCheckOnUpdate(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	// The user wrote this one freely; agents may keep working on it.
	legacy := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "legacy", Body: "자유롭게 쓴 본문"})
	update := func(tr model.Trace, patch IssuePatch) (*model.Issue, bool, error) {
		return s.UpdateIssue(ctx, tr, legacy.Key, patch, nil)
	}
	doing, bug, feature := model.StatusDoing, model.TypeBug, model.TypeFeature

	// Changes that touch neither the body nor the type are not checked.
	if _, changed, err := update(trClaude, IssuePatch{Title: str("renamed"), Status: &doing}); err != nil || !changed {
		t.Errorf("title and status by an agent: changed=%v err=%v", changed, err)
	}
	// Writing the same body again changes nothing and is not checked.
	if _, changed, err := update(trClaude, IssuePatch{Body: str("자유롭게 쓴 본문"), Type: &feature}); err != nil || changed {
		t.Errorf("same body and type: changed=%v err=%v", changed, err)
	}
	// A new body must follow the template.
	if _, _, err := update(trClaude, IssuePatch{Body: str("새 메모")}); !strings.Contains(bodyViolation(t, err), "## 배경") {
		t.Errorf("free text by an agent: %v", err)
	}
	if got, changed, err := update(trClaude, IssuePatch{Body: str(tplBody(model.TypeFeature))}); err != nil || !changed || got.Body != tplBody(model.TypeFeature) {
		t.Errorf("template body by an agent: %v changed=%v", err, changed)
	}
	// Changing the type checks the body against the new type.
	if _, _, err := update(trClaude, IssuePatch{Type: &bug}); !strings.Contains(bodyViolation(t, err), "## 현상") {
		t.Errorf("type change keeping a feature body: %v", err)
	}
	if got, _, err := update(trClaude, IssuePatch{Type: &bug, Body: str(tplBody(model.TypeBug))}); err != nil || got.Type != model.TypeBug {
		t.Errorf("type and body together: %v", err)
	}
	// Several violations are reported together.
	var verr *model.ValidationError
	_, _, err := update(trClaude, IssuePatch{Title: str(""), Body: str("메모")})
	if !errors.As(err, &verr) || len(verr.Fields) != 2 || verr.Fields[0].Field != "title" || verr.Fields[1].Field != "body" {
		t.Errorf("combined violations = %v", err)
	}
	// The user is never checked.
	if got, _, err := update(trOwner, IssuePatch{Body: str("다시 자유롭게")}); err != nil || got.Body != "다시 자유롭게" {
		t.Errorf("free text by the user: %v", err)
	}
	if _, _, err := update(trOwner, IssuePatch{Type: &feature}); err != nil {
		t.Errorf("type change by the user: %v", err)
	}
	// A refused update wrote nothing.
	cur, _ := s.GetIssue(ctx, legacy.Key)
	if _, total, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{legacy.Key}, Limit: 1}); cur.Body != "다시 자유롭게" || total != 6 {
		t.Errorf("issue %+v with %d records", cur, total)
	}
}

func TestTemplateCheckFollowsTheActorKind(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	create := func(tr model.Trace) error {
		_, err := s.CreateIssue(ctx, tr, IssueInput{Project: "AGB", Title: "x", Body: "메모"})
		return err
	}
	if err := create(trClaude); err == nil {
		t.Fatal("an agent without the template must be refused")
	}
	if err := s.EnsureActors(ctx, []model.Actor{{ID: "claude", Kind: model.ActorUser}}); err != nil {
		t.Fatal(err)
	}
	if err := create(trClaude); err != nil {
		t.Errorf("claude declared as a user is not checked: %v", err)
	}
	if err := s.EnsureActors(ctx, []model.Actor{{ID: "owner", Kind: model.ActorAgent}}); err != nil {
		t.Fatal(err)
	}
	if err := create(trOwner); err == nil {
		t.Error("owner declared as an agent must be checked")
	}
}
