package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func TestTemplateCheckOnTheAPI(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	owner := map[string]string{"X-Actor": "owner"}
	bug, _ := model.TemplateFor(model.TypeBug)
	feature, _ := model.TemplateFor(model.TypeFeature)
	post := func(headers map[string]string, fields map[string]any) (statusCode int, body string) {
		t.Helper()
		fields["project"] = "AGB"
		b, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		rec := e.do(t, "POST", "/v1/issues", string(b), headers)
		return rec.Code, rec.Body.String()
	}
	messageOf := func(body string) string {
		t.Helper()
		var eb errBody
		if err := json.Unmarshal([]byte(body), &eb); err != nil || eb.Error.Code != "validation_failed" || len(eb.Error.Details) != 1 || eb.Error.Details[0].Field != "body" {
			t.Fatalf("not a single body violation: %s", body)
		}
		return eb.Error.Details[0].Message
	}

	// An agent: every body needs the headings of its type, in order.
	if code, body := post(nil, map[string]any{"title": "x"}); code != 422 || !strings.Contains(messageOf(body), "## 배경, ## 할 일, ## 완료 조건") {
		t.Errorf("no body: %d %s", code, body)
	}
	if code, body := post(nil, map[string]any{"title": "x", "type": "bug", "body": "## 현상\n문제"}); code != 422 || !strings.Contains(messageOf(body), "빠진 제목: ## 재현, ## 원인, ## 완료 조건") {
		t.Errorf("partial bug body: %d %s", code, body)
	}
	if code, body := post(nil, map[string]any{"title": "x", "body": "## 완료 조건\n## 배경\n## 할 일\n"}); code != 422 || !strings.Contains(messageOf(body), "순서가 맞지 않는 제목: ## 완료 조건") {
		t.Errorf("wrong order: %d %s", code, body)
	}
	if code, body := post(nil, map[string]any{"title": "x", "type": "bug", "body": feature.Body}); code != 422 {
		t.Errorf("another type's template: %d %s", code, body)
	}
	// Every type that remains has its template checked: the template itself passes,
	// and the same body without its last heading does not.
	for _, tpl := range model.Templates {
		fields := map[string]any{"title": "ok " + string(tpl.Type), "type": string(tpl.Type), "body": tpl.Body}
		if tpl.Type == model.TypeObservation {
			fields["recheck_condition"] = "다음 릴리즈"
		}
		if code, body := post(nil, fields); code != 201 {
			t.Errorf("%s template body: %d %s", tpl.Type, code, body)
		}
		last := tpl.Headings[len(tpl.Headings)-1]
		fields["body"] = tpl.Body[:strings.LastIndex(tpl.Body, "## "+last)]
		if code, body := post(nil, fields); code != 422 || !strings.Contains(messageOf(body), "빠진 제목: ## "+last) {
			t.Errorf("%s without its last heading: %d %s", tpl.Type, code, body)
		}
	}
	// Other violations come along with it.
	rec := e.do(t, "POST", "/v1/issues", `{"project":"AGB","title":"","priority":"P9","body":"메모"}`, nil)
	if b := decodeErr(t, rec); rec.Code != 422 || len(b.Error.Details) != 3 || b.Error.Details[2].Field != "body" {
		t.Errorf("combined: %d %s", rec.Code, rec.Body.String())
	}
	if code, body := post(nil, map[string]any{"title": "ok", "type": "bug", "body": "  " + bug.Body + "\r\n"}); code != 201 {
		t.Errorf("template body: %d %s", code, body)
	}
	if code, body := post(nil, map[string]any{"title": "ok", "body": "## 배경\r\n오늘\r\n## 참고\r\n## 할 일\r\n- x\r\n## 완료 조건\r\n- y\r\n"}); code != 201 {
		t.Errorf("windows line ends and an extra heading: %d %s", code, body)
	}
	// The user is not held to it.
	if code, body := post(owner, map[string]any{"title": "빠른 메모", "body": "그냥 적는다."}); code != 201 {
		t.Errorf("user free text: %d %s", code, body)
	}
	if code, body := post(owner, map[string]any{"title": "본문 없는 메모"}); code != 201 {
		t.Errorf("user without a body: %d %s", code, body)
	}

	// Updates: only a changed body or type is checked, for an agent only.
	code, body := post(owner, map[string]any{"title": "legacy", "body": "자유 본문"})
	if code != 201 {
		t.Fatalf("legacy: %d %s", code, body)
	}
	var legacy model.Issue
	if err := json.Unmarshal([]byte(body), &legacy); err != nil {
		t.Fatal(err)
	}
	path := "/v1/issues/" + legacy.Key
	patch := func(headers map[string]string, p string) int {
		return e.do(t, "PATCH", path, p, headers).Code
	}
	if c := patch(nil, `{"title":"새 제목","status":"doing"}`); c != 200 {
		t.Errorf("title and status by an agent: %d", c)
	}
	if c := patch(nil, `{"body":"자유 본문"}`); c != 200 {
		t.Errorf("the same body by an agent: %d", c)
	}
	rec = e.do(t, "PATCH", path, `{"body":"새 메모"}`, nil)
	if rec.Code != 422 || !strings.Contains(decodeErr(t, rec).Error.Details[0].Message, "## 배경") {
		t.Errorf("free text by an agent: %d %s", rec.Code, rec.Body.String())
	}
	if c := patch(nil, `{"type":"bug"}`); c != 422 {
		t.Errorf("type change keeping a free body: %d", c)
	}
	b, _ := json.Marshal(map[string]any{"type": "bug", "body": bug.Body})
	if c := patch(nil, string(b)); c != 200 {
		t.Errorf("type and body together: %d", c)
	}
	if c := patch(owner, `{"body":"다시 자유롭게","type":"idea"}`); c != 200 {
		t.Errorf("free text by the user: %d", c)
	}
}
