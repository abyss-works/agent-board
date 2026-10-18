package api

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func (e *env) createTopic(t *testing.T, project, body string) model.Topic {
	t.Helper()
	rec := e.do(t, "POST", "/v1/projects/"+project+"/topics", body, nil)
	if rec.Code != 201 {
		t.Fatalf("create topic %s: %d %s", body, rec.Code, rec.Body.String())
	}
	var tp model.Topic
	decodeJSON(t, rec, &tp)
	return tp
}

func wantReason(t *testing.T, rec *httptest.ResponseRecorder, status int, reason string) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("status = %d, want %d: %s", rec.Code, status, rec.Body.String())
		return
	}
	for _, d := range decodeErr(t, rec).Error.Details {
		if d.Reason == reason {
			return
		}
	}
	t.Errorf("details lack reason %s: %s", reason, rec.Body.String())
}

func TestCreateAndListTopics(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")

	rec := e.do(t, "POST", "/v1/projects/AGB/topics", `{"name":"이슈 흐름"}`, nil)
	var flow model.Topic
	decodeJSON(t, rec, &flow)
	if rec.Code != 201 || rec.Header().Get("Location") != fmt.Sprintf("/v1/topics/%d", flow.ID) {
		t.Fatalf("create: %d %s %q", rec.Code, rec.Body.String(), rec.Header().Get("Location"))
	}
	if flow.Project != "AGB" || flow.Name != "이슈 흐름" || flow.Parent != nil || flow.Path != "이슈 흐름" || flow.Status != model.TopicActive || flow.CreatedBy != "claude" {
		t.Errorf("topic = %+v", flow)
	}
	if !strings.Contains(rec.Body.String(), `"parent":null`) || !strings.Contains(rec.Body.String(), `"path":"이슈 흐름"`) {
		t.Errorf("JSON shape: %s", rec.Body.String())
	}
	triage := e.createTopic(t, "AGB", fmt.Sprintf(`{"name":"트리아지","parent":%d}`, flow.ID))
	if triage.Parent == nil || *triage.Parent != flow.ID || triage.Path != "이슈 흐름/트리아지" {
		t.Errorf("child = %+v", triage)
	}
	e.createTopic(t, "AGB", fmt.Sprintf(`{"name":"상태 사유","parent":%d}`, flow.ID))
	infra := e.createTopic(t, "AGB", `{"name":"인프라","parent":null}`)
	e.createTopic(t, "OTH", `{"name":"이슈 흐름"}`)

	// The list is an envelope with items only, parents by name then their topics by name.
	rec = e.do(t, "GET", "/v1/projects/AGB/topics", "", nil)
	var list struct {
		Items []model.Topic `json:"items"`
	}
	decodeJSON(t, rec, &list)
	var paths []string
	for _, it := range list.Items {
		paths = append(paths, it.Path)
	}
	if rec.Code != 200 || strings.Join(paths, ",") != "이슈 흐름,이슈 흐름/상태 사유,이슈 흐름/트리아지,인프라" {
		t.Errorf("list: %d %v", rec.Code, paths)
	}
	var raw map[string]any
	decodeJSON(t, rec, &raw)
	if len(raw) != 1 {
		t.Errorf("the list response holds only items: %v", raw)
	}

	// Status filter.
	e.do(t, "DELETE", fmt.Sprintf("/v1/topics/%d", infra.ID), "", nil)
	count := func(q string) int {
		t.Helper()
		rec := e.do(t, "GET", "/v1/projects/AGB/topics"+q, "", nil)
		if rec.Code != 200 {
			t.Fatalf("list %s: %d %s", q, rec.Code, rec.Body.String())
		}
		var l struct {
			Items []model.Topic `json:"items"`
		}
		decodeJSON(t, rec, &l)
		return len(l.Items)
	}
	if n := count(""); n != 3 {
		t.Errorf("default active = %d", n)
	}
	if n := count("?status=active"); n != 3 {
		t.Errorf("status=active = %d", n)
	}
	if n := count("?status=deleted"); n != 1 {
		t.Errorf("status=deleted = %d", n)
	}
	if n := count("?status=all"); n != 4 {
		t.Errorf("status=all = %d", n)
	}
	for _, q := range []string{"status=bogus", "status=active&status=deleted", "limit=5", "x=1"} {
		if rec := e.do(t, "GET", "/v1/projects/AGB/topics?"+q, "", nil); rec.Code != 400 {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
	if rec := e.do(t, "GET", "/v1/projects/NOPE/topics", "", nil); rec.Code != 404 {
		t.Errorf("missing project list: %d", rec.Code)
	}
}

func TestCreateTopicRules(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")
	flow := e.createTopic(t, "AGB", `{"name":"이슈 흐름"}`)
	triage := e.createTopic(t, "AGB", fmt.Sprintf(`{"name":"트리아지","parent":%d}`, flow.ID))
	foreign := e.createTopic(t, "OTH", `{"name":"다른 프로젝트"}`)
	post := func(body string) *httptest.ResponseRecorder {
		return e.do(t, "POST", "/v1/projects/AGB/topics", body, nil)
	}

	for name, body := range map[string]string{
		"empty name":   `{"name":""}`,
		"missing name": `{}`,
		"null name":    `{"name":null}`,
		"51 runes":     `{"name":"` + strings.Repeat("가", 51) + `"}`,
		"slash":        `{"name":"a/b"}`,
	} {
		wantFieldError(t, post(body), 422, "name")
		_ = name
	}
	if rec := post(`{"name":"` + strings.Repeat("가", 50) + `"}`); rec.Code != 201 {
		t.Errorf("50 runes: %d %s", rec.Code, rec.Body.String())
	}
	for name, body := range map[string]string{
		"under a child topic": fmt.Sprintf(`{"name":"셋째 단계","parent":%d}`, triage.ID),
		"other project":       fmt.Sprintf(`{"name":"x","parent":%d}`, foreign.ID),
		"missing parent":      `{"name":"x","parent":9999}`,
	} {
		wantFieldError(t, post(body), 422, "parent")
		_ = name
	}
	wantReason(t, post(`{"name":"트리아지","parent":`+fmt.Sprint(flow.ID)+`}`), 409, "duplicate")
	wantReason(t, post(`{"name":"이슈 흐름"}`), 409, "duplicate")
	wantFieldError(t, post(`{"name":"x","extra":1}`), 400, "extra")
	wantFieldError(t, post(`{"name":5}`), 400, "name")
	wantFieldError(t, post(`{"name":"x","parent":"1"}`), 400, "parent")
	wantFieldError(t, post(`{"name":"x","path":"a"}`), 400, "path")
	wantFieldError(t, post(`{"name":"x","status":"active"}`), 400, "status")
	if rec := e.do(t, "POST", "/v1/projects/NOPE/topics", `{"name":"x"}`, nil); rec.Code != 404 {
		t.Errorf("missing project: %d", rec.Code)
	}
	e.do(t, "PATCH", "/v1/projects/AGB", `{"status":"archived"}`, nil)
	wantReason(t, post(`{"name":"보관 중"}`), 409, "project_not_active")
}

func TestPatchAndDeleteTopic(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	flow := e.createTopic(t, "AGB", `{"name":"이슈 흐름"}`)
	infra := e.createTopic(t, "AGB", `{"name":"인프라"}`)
	triage := e.createTopic(t, "AGB", fmt.Sprintf(`{"name":"트리아지","parent":%d}`, flow.ID))
	path := func(id int64) string { return fmt.Sprintf("/v1/topics/%d", id) }

	rec := e.do(t, "PATCH", path(triage.ID), `{"name":"분류"}`, map[string]string{"X-Actor": "owner"})
	var got model.Topic
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.Name != "분류" || got.Path != "이슈 흐름/분류" || got.UpdatedBy != "owner" || got.CreatedBy != "claude" {
		t.Fatalf("rename: %d %+v", rec.Code, got)
	}
	rec = e.do(t, "PATCH", path(triage.ID), fmt.Sprintf(`{"parent":%d}`, infra.ID), nil)
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.Path != "인프라/분류" {
		t.Errorf("move: %d %+v", rec.Code, got)
	}
	rec = e.do(t, "PATCH", path(triage.ID), `{"parent":null}`, nil)
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.Parent != nil || got.Path != "분류" {
		t.Errorf("promote: %d %+v", rec.Code, got)
	}
	before := got.UpdatedAt
	rec = e.do(t, "PATCH", path(triage.ID), `{"name":"분류"}`, nil)
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.UpdatedAt != before {
		t.Errorf("no-op patch: %d updated_at %v -> %v", rec.Code, before, got.UpdatedAt)
	}
	if rec := e.do(t, "PATCH", path(triage.ID), `{}`, nil); rec.Code != 200 {
		t.Errorf("empty patch: %d", rec.Code)
	}
	// Rule violations on update. leaf is a topic under flow; flow holds it, so flow cannot move under infra.
	leaf := e.createTopic(t, "AGB", fmt.Sprintf(`{"name":"잎","parent":%d}`, flow.ID))
	for body, field := range map[string]string{
		`{"name":""}`:                         "name",
		`{"name":"a/b"}`:                      "name",
		`{"name":null}`:                       "name",
		`{"status":null}`:                     "status",
		`{"status":"gone"}`:                   "status",
		`{"parent":9999}`:                     "parent",
		fmt.Sprintf(`{"parent":%d}`, leaf.ID): "parent",
	} {
		wantFieldError(t, e.do(t, "PATCH", path(infra.ID), body, nil), 422, field)
	}
	wantFieldError(t, e.do(t, "PATCH", path(flow.ID), fmt.Sprintf(`{"parent":%d}`, infra.ID), nil), 422, "parent")
	wantFieldError(t, e.do(t, "PATCH", path(infra.ID), fmt.Sprintf(`{"parent":%d}`, infra.ID), nil), 422, "parent")
	wantReason(t, e.do(t, "PATCH", path(infra.ID), `{"name":"분류"}`, nil), 409, "duplicate")
	for _, body := range []string{`{"project":"X"}`, `{"path":"x"}`, `{"id":1}`, `{"created_by":"owner"}`, `{"nope":1}`} {
		if rec := e.do(t, "PATCH", path(infra.ID), body, nil); rec.Code != 400 {
			t.Errorf("patch %s: %d", body, rec.Code)
		}
	}
	if rec := e.do(t, "PATCH", path(infra.ID), `{"name":5}`, nil); rec.Code != 400 {
		t.Errorf("type mismatch: %d", rec.Code)
	}
	for _, id := range []string{"9999", "abc", "0", "-1"} {
		if rec := e.do(t, "PATCH", "/v1/topics/"+id, `{"name":"x"}`, nil); rec.Code != 404 {
			t.Errorf("topic %s: %d", id, rec.Code)
		}
	}
	rec = e.do(t, "GET", path(infra.ID), "", nil)
	if rec.Code != 405 || rec.Header().Get("Allow") != "DELETE, PATCH" {
		t.Errorf("GET: %d Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}

	// DELETE: a top-level topic with an active child is refused; otherwise it is a status change.
	child := e.createTopic(t, "AGB", fmt.Sprintf(`{"name":"하위","parent":%d}`, infra.ID))
	wantReason(t, e.do(t, "DELETE", path(infra.ID), "", nil), 409, "topic_has_children")
	rec = e.do(t, "DELETE", path(child.ID), "", nil)
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.Status != model.TopicDeleted {
		t.Errorf("delete child: %d %+v", rec.Code, got)
	}
	if rec = e.do(t, "DELETE", path(child.ID), "", nil); rec.Code != 200 {
		t.Errorf("delete again: %d", rec.Code)
	}
	if rec = e.do(t, "DELETE", path(infra.ID), "", nil); rec.Code != 200 {
		t.Errorf("delete parent after the child: %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, "DELETE", path(infra.ID)+"?x=1", "", nil); rec.Code != 400 {
		t.Errorf("unknown parameter: %d", rec.Code)
	}
	// A deleted topic comes back with a status patch.
	rec = e.do(t, "PATCH", path(infra.ID), `{"status":"active"}`, nil)
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.Status != model.TopicActive {
		t.Errorf("restore: %d %+v", rec.Code, got)
	}
}

func TestIssueTopicOnTheAPI(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")
	flow := e.createTopic(t, "AGB", `{"name":"이슈 흐름"}`)
	triage := e.createTopic(t, "AGB", fmt.Sprintf(`{"name":"트리아지","parent":%d}`, flow.ID))
	infra := e.createTopic(t, "AGB", `{"name":"인프라"}`)
	foreign := e.createTopic(t, "OTH", `{"name":"다른 프로젝트"}`)

	rec := e.postIssue(t, fmt.Sprintf(`{"project":"AGB","title":"분류된 이슈","topic":%d}`, triage.ID), nil)
	var i model.Issue
	decodeJSON(t, rec, &i)
	if rec.Code != 201 || i.Topic == nil || *i.Topic != triage.ID || i.TopicPath == nil || *i.TopicPath != "이슈 흐름/트리아지" {
		t.Fatalf("create with topic: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.postIssue(t, `{"project":"AGB","title":"주제 없음","topic":null}`, nil)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"topic":null`) || !strings.Contains(rec.Body.String(), `"topic_path":null`) {
		t.Errorf("create without topic: %d %s", rec.Code, rec.Body.String())
	}
	e.createIssue(t, fmt.Sprintf(`{"project":"AGB","title":"대주제에 붙임","topic":%d}`, flow.ID))
	e.createIssue(t, fmt.Sprintf(`{"project":"AGB","title":"인프라","topic":%d}`, infra.ID))

	// Refusals: another project, missing, wrong type.
	wantFieldError(t, e.postIssue(t, fmt.Sprintf(`{"project":"AGB","title":"x","topic":%d}`, foreign.ID), nil), 422, "topic")
	wantFieldError(t, e.postIssue(t, `{"project":"AGB","title":"x","topic":9999}`, nil), 422, "topic")
	wantFieldError(t, e.postIssue(t, `{"project":"AGB","title":"x","topic":"abc"}`, nil), 400, "topic")

	// PATCH: move, clear, refuse.
	rec = e.do(t, "PATCH", "/v1/issues/AGB-2", fmt.Sprintf(`{"topic":%d}`, infra.ID), nil)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || *i.TopicPath != "인프라" || i.Version != 2 {
		t.Errorf("set: %d %+v", rec.Code, i)
	}
	rec = e.do(t, "PATCH", "/v1/issues/AGB-2", `{"topic":null}`, nil)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.Topic != nil || i.TopicPath != nil {
		t.Errorf("clear: %d %+v", rec.Code, i)
	}
	wantFieldError(t, e.do(t, "PATCH", "/v1/issues/AGB-2", fmt.Sprintf(`{"topic":%d}`, foreign.ID), nil), 422, "topic")
	e.do(t, "DELETE", fmt.Sprintf("/v1/topics/%d", infra.ID), "", nil)
	wantFieldError(t, e.do(t, "PATCH", "/v1/issues/AGB-2", fmt.Sprintf(`{"topic":%d}`, infra.ID), nil), 422, "topic")
	// The issue that already had the topic is not blocked by its deletion.
	if rec := e.do(t, "PATCH", "/v1/issues/AGB-4", `{"title":"제목 수정"}`, nil); rec.Code != 200 {
		t.Errorf("edit of an issue whose topic was deleted: %d %s", rec.Code, rec.Body.String())
	}

	// Filter: a top-level topic includes the topics under it, none selects the rest, commas are OR.
	list := func(q string) string {
		t.Helper()
		rec := e.do(t, "GET", "/v1/issues?status=all&sort=key&"+q, "", nil)
		if rec.Code != 200 {
			t.Fatalf("list %s: %d %s", q, rec.Code, rec.Body.String())
		}
		var l listEnvelope[model.IssueSummary]
		decodeJSON(t, rec, &l)
		var ks []string
		for _, it := range l.Items {
			ks = append(ks, it.Key)
		}
		return strings.Join(ks, ",")
	}
	if got := list(fmt.Sprintf("topic=%d", triage.ID)); got != "AGB-1" {
		t.Errorf("child topic = %s", got)
	}
	if got := list(fmt.Sprintf("topic=%d", flow.ID)); got != "AGB-1,AGB-3" {
		t.Errorf("top-level topic = %s", got)
	}
	if got := list(fmt.Sprintf("topic=%d,%d", triage.ID, infra.ID)); got != "AGB-1,AGB-4" {
		t.Errorf("OR = %s", got)
	}
	if got := list("topic=none"); got != "AGB-2" {
		t.Errorf("none = %s", got)
	}
	if got := list(fmt.Sprintf("topic=none,%d", infra.ID)); got != "AGB-2,AGB-4" {
		t.Errorf("none or a topic = %s", got)
	}
	for _, q := range []string{"topic=abc", "topic=0", "topic=-1", "topic=1.5", "topic=1&topic=2"} {
		if rec := e.do(t, "GET", "/v1/issues?"+q, "", nil); rec.Code != 400 {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
	// The activity record and its field filter.
	rec = e.do(t, "GET", "/v1/activity?field=topic&kind=updated", "", nil)
	var acts listEnvelope[model.Activity]
	decodeJSON(t, rec, &acts)
	if rec.Code != 200 || acts.Total != 2 || acts.Items[0].Issue != "AGB-2" {
		t.Errorf("activity by topic: %d %+v", rec.Code, acts)
	}
}
