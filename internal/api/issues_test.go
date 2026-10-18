package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func (e *env) createIssue(t *testing.T, body string) model.Issue {
	t.Helper()
	rec := e.do(t, "POST", "/v1/issues", body, nil)
	if rec.Code != 201 {
		t.Fatalf("create issue %s: %d %s", body, rec.Code, rec.Body.String())
	}
	var i model.Issue
	decodeJSON(t, rec, &i)
	return i
}

func TestCreateIssue(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	rec := e.do(t, "POST", "/v1/issues", `{"project":"AGB","title":"첫 이슈"}`, nil)
	if rec.Code != 201 || rec.Header().Get("Location") != "/v1/issues/AGB-1" {
		t.Fatalf("create: %d %s %q", rec.Code, rec.Body.String(), rec.Header().Get("Location"))
	}
	var i model.Issue
	decodeJSON(t, rec, &i)
	if i.Key != "AGB-1" || i.Title != "첫 이슈" || i.Status != model.StatusBacklog || i.Priority != model.P2 || i.Type != model.TypeFeature || i.CreatedBy != "claude" || i.Version != 1 {
		t.Errorf("issue = %+v", i)
	}
	if !strings.Contains(rec.Body.String(), `"labels":[]`) || !strings.Contains(rec.Body.String(), `"parent":null`) || !strings.Contains(rec.Body.String(), `"closed_at":null`) {
		t.Errorf("json shape: %s", rec.Body.String())
	}

	full := e.createIssue(t, `{"project":"AGB","title":"full","body":"# b","type":"observation","status":"todo","priority":"P0",
		"labels":["cli","api"],"parent":"AGB-1","depends_on":["AGB-1"],"refs":["repo:wsk0715/agent-board"],"recheck_condition":"later"}`)
	if full.Key != "AGB-2" || full.Body != "# b" || *full.Parent != "AGB-1" || full.Labels[0] != "api" || *full.RecheckCondition != "later" || full.Priority != model.P0 {
		t.Errorf("full = %+v", full)
	}

	cases := []struct {
		name  string
		body  string
		code  int
		field string
	}{
		{"missing project", `{"project":"NOPE","title":"x"}`, 404, ""},
		{"bad priority", `{"project":"AGB","title":"x","priority":"P9"}`, 422, "priority"},
		{"empty title", `{"project":"AGB","title":""}`, 422, "title"},
		{"no title", `{"project":"AGB"}`, 422, "title"},
		{"bad label", `{"project":"AGB","title":"x","labels":["Bad"]}`, 422, "labels[0]"},
		{"bad ref", `{"project":"AGB","title":"x","refs":["git:x"]}`, 422, "refs[0]"},
		{"observation without recheck", `{"project":"AGB","title":"x","type":"observation"}`, 422, "recheck_condition"},
		{"recheck on feature", `{"project":"AGB","title":"x","recheck_condition":"c"}`, 422, "recheck_condition"},
		{"missing parent", `{"project":"AGB","title":"x","parent":"AGB-99"}`, 422, "parent"},
		{"missing dep", `{"project":"AGB","title":"x","depends_on":["AGB-1","AGB-99"]}`, 422, "depends_on[1]"},
		{"unknown field", `{"project":"AGB","title":"x","prio":"P1"}`, 400, "prio"},
		{"server field", `{"project":"AGB","title":"x","key":"AGB-9"}`, 400, "key"},
		{"type mismatch", `{"project":"AGB","title":"x","priority":5}`, 400, "priority"},
		{"labels not array", `{"project":"AGB","title":"x","labels":"api"}`, 400, "labels"},
		{"null title", `{"project":"AGB","title":null}`, 422, "title"},
		{"no project", `{"title":"x"}`, 422, "project"},
		{"not an object", `[1]`, 400, ""},
	}
	for _, c := range cases {
		rec := e.do(t, "POST", "/v1/issues", c.body, nil)
		if rec.Code != c.code {
			t.Errorf("%s: status = %d, want %d: %s", c.name, rec.Code, c.code, rec.Body.String())
			continue
		}
		if c.field != "" {
			b := decodeErr(t, rec)
			found := false
			for _, d := range b.Error.Details {
				if d.Field == c.field {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: details %+v lack field %s", c.name, b.Error.Details, c.field)
			}
		}
	}

	e.do(t, "PATCH", "/v1/projects/AGB", `{"status":"archived"}`, nil)
	rec = e.do(t, "POST", "/v1/issues", `{"project":"AGB","title":"x"}`, nil)
	if rec.Code != 409 || decodeErr(t, rec).Error.Details[0].Reason != "project_not_active" {
		t.Errorf("archived project: %d %s", rec.Code, rec.Body.String())
	}
}

func TestGetPatchDeleteIssue(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createIssue(t, `{"project":"AGB","title":"a","status":"todo","labels":["api"]}`)
	e.createIssue(t, `{"project":"AGB","title":"b","parent":"AGB-1"}`)

	rec := e.do(t, "GET", "/v1/issues/AGB-1", "", nil)
	if rec.Code != 200 || rec.Header().Get("ETag") != `"1"` {
		t.Fatalf("get: %d etag=%q", rec.Code, rec.Header().Get("ETag"))
	}
	var i model.Issue
	decodeJSON(t, rec, &i)
	if i.Key != "AGB-1" || !strings.Contains(rec.Body.String(), `"body":""`) {
		t.Errorf("get body = %s", rec.Body.String())
	}
	if rec = e.do(t, "GET", "/v1/issues/AGB-9", "", nil); rec.Code != 404 {
		t.Errorf("missing = %d", rec.Code)
	}
	if rec = e.do(t, "GET", "/v1/issues/not-a-key", "", nil); rec.Code != 404 {
		t.Errorf("bad key = %d", rec.Code)
	}

	rec = e.do(t, "PATCH", "/v1/issues/AGB-1", `{"status":"doing","labels":["api","cli"],"body":"work"}`, map[string]string{"X-Actor": "owner"})
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.Status != model.StatusDoing || i.Version != 2 || rec.Header().Get("ETag") != `"2"` || i.UpdatedBy != "owner" || i.StatusChangedBy != "owner" || i.Body != "work" {
		t.Errorf("patch: %d %+v etag=%q", rec.Code, i, rec.Header().Get("ETag"))
	}
	rec = e.do(t, "PATCH", "/v1/issues/AGB-1", `{"title":"stale"}`, map[string]string{"If-Match": `"1"`})
	if rec.Code != 412 || decodeErr(t, rec).Error.Code != "precondition_failed" {
		t.Errorf("stale if-match: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "PATCH", "/v1/issues/AGB-1", `{"title":"fresh"}`, map[string]string{"If-Match": `"2"`})
	if rec.Code != 200 || rec.Header().Get("ETag") != `"3"` {
		t.Errorf("good if-match: %d etag=%q", rec.Code, rec.Header().Get("ETag"))
	}
	if rec = e.do(t, "PATCH", "/v1/issues/AGB-1", `{"title":"x"}`, map[string]string{"If-Match": `abc`}); rec.Code != 400 {
		t.Errorf("malformed if-match: %d", rec.Code)
	}
	rec = e.do(t, "PATCH", "/v1/issues/AGB-1", `{"title":"fresh"}`, nil)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.Version != 3 {
		t.Errorf("no-op patch: %d version=%d", rec.Code, i.Version)
	}
	rec = e.do(t, "PATCH", "/v1/issues/AGB-2", `{"parent":null}`, nil)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.Parent != nil {
		t.Errorf("clear parent: %d %+v", rec.Code, i)
	}
	for _, body := range []string{`{"key":"AGB-5"}`, `{"project":"X"}`, `{"version":9}`, `{"created_by":"owner"}`, `{"number":3}`, `{"nope":1}`} {
		if rec = e.do(t, "PATCH", "/v1/issues/AGB-1", body, nil); rec.Code != 400 {
			t.Errorf("patch %s: %d", body, rec.Code)
		}
	}
	for _, body := range []string{`{"title":null}`, `{"labels":null}`, `{"status":"gone"}`, `{"depends_on":["AGB-99"]}`} {
		if rec = e.do(t, "PATCH", "/v1/issues/AGB-1", body, nil); rec.Code != 422 {
			t.Errorf("patch %s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	e.do(t, "PATCH", "/v1/issues/AGB-2", `{"parent":"AGB-1"}`, nil)
	rec = e.do(t, "PATCH", "/v1/issues/AGB-1", `{"parent":"AGB-2"}`, nil)
	if rec.Code != 409 || decodeErr(t, rec).Error.Details[0].Reason != "cycle" {
		t.Errorf("cycle: %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, "PATCH", "/v1/issues/AGB-9", `{"title":"x"}`, nil); rec.Code != 404 {
		t.Errorf("patch missing = %d", rec.Code)
	}

	rec = e.do(t, "DELETE", "/v1/issues/AGB-2?reason=시험", "", map[string]string{"If-Match": `"1"`})
	if rec.Code != 412 {
		t.Errorf("delete stale if-match: %d", rec.Code)
	}
	rec = e.do(t, "DELETE", "/v1/issues/AGB-2?reason=시험", "", nil)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.Status != model.StatusDeleted || i.ClosedAt == nil {
		t.Errorf("delete: %d %+v", rec.Code, i)
	}
	if rec = e.do(t, "GET", "/v1/issues/AGB-2", "", nil); rec.Code != 200 {
		t.Errorf("deleted issue still readable: %d", rec.Code)
	}
	rec = e.do(t, "GET", "/v1/issues/AGB-2/activity", "", nil)
	var acts listEnvelope[model.Activity]
	decodeJSON(t, rec, &acts)
	if acts.Total != 4 || acts.Items[0].Kind != model.ActivityUpdated || string(acts.Items[0].Changes["status"].To) != `"deleted"` {
		t.Errorf("delete activity = %+v", acts)
	}
	if rec = e.do(t, "DELETE", "/v1/issues/AGB-9?reason=x", "", nil); rec.Code != 404 {
		t.Errorf("delete missing = %d", rec.Code)
	}
}

func TestListIssues(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")
	e.createIssue(t, `{"project":"AGB","title":"REST API 설계","status":"todo","priority":"P1","labels":["api"]}`)
	e.createIssue(t, `{"project":"AGB","title":"드라이버 검증","priority":"P0","labels":["api","cli"],"type":"bug"}`)
	e.createIssue(t, `{"project":"OTH","title":"done thing","status":"done"}`)
	e.createIssue(t, `{"project":"AGB","title":"gone","status":"deleted","parent":"AGB-1"}`)

	list := func(q string) listEnvelope[model.IssueSummary] {
		t.Helper()
		rec := e.do(t, "GET", "/v1/issues?"+q, "", nil)
		if rec.Code != 200 {
			t.Fatalf("list %s: %d %s", q, rec.Code, rec.Body.String())
		}
		var l listEnvelope[model.IssueSummary]
		decodeJSON(t, rec, &l)
		return l
	}
	keys := func(l listEnvelope[model.IssueSummary]) string {
		var ks []string
		for _, it := range l.Items {
			ks = append(ks, it.Key)
		}
		return strings.Join(ks, ",")
	}
	if l := list(""); keys(l) != "AGB-2,AGB-1" || l.Total != 2 {
		t.Errorf("default open = %s total %d", keys(l), l.Total)
	}
	if l := list("status=all"); l.Total != 4 {
		t.Errorf("all = %d", l.Total)
	}
	if l := list("status=closed"); keys(l) != "OTH-1,AGB-3" && keys(l) != "AGB-3,OTH-1" {
		t.Errorf("closed = %s", keys(l))
	}
	if l := list("status=deleted"); keys(l) != "AGB-3" {
		t.Errorf("deleted = %s", keys(l))
	}
	if l := list("project=OTH&status=all"); keys(l) != "OTH-1" {
		t.Errorf("project = %s", keys(l))
	}
	if l := list("label=api&label=cli&status=all"); keys(l) != "AGB-2" {
		t.Errorf("labels AND = %s", keys(l))
	}
	if l := list("type=bug,design&status=all"); keys(l) != "AGB-2" {
		t.Errorf("type = %s", keys(l))
	}
	if l := list("priority=P1&status=all"); keys(l) != "AGB-1" {
		t.Errorf("priority = %s", keys(l))
	}
	if l := list("parent=AGB-1&status=all"); keys(l) != "AGB-3" {
		t.Errorf("parent = %s", keys(l))
	}
	if l := list("parent=none&status=all"); l.Total != 3 {
		t.Errorf("parent none = %d", l.Total)
	}
	if l := list("created_by=claude&status=all"); l.Total != 4 {
		t.Errorf("created_by = %d", l.Total)
	}
	if l := list("sort=-created_at&limit=1&offset=1&status=all"); keys(l) != "OTH-1" || l.Limit != 1 || l.Offset != 1 || l.Total != 4 {
		t.Errorf("paging = %+v", l)
	}
	if l := list("sort=key&status=all"); keys(l) != "AGB-1,AGB-2,AGB-3,OTH-1" {
		t.Errorf("sort key = %s", keys(l))
	}
	l := list("q=설계&status=all")
	if keys(l) != "AGB-1" || l.Items[0].Snippet == nil || !strings.Contains(*l.Items[0].Snippet, "[") {
		t.Errorf("search = %+v", l)
	}
	if l := list("q=api+드라이&status=all"); l.Total != 0 {
		t.Errorf("search AND = %d", l.Total)
	}
	if l := list("q=agent-board"); l.Total != 0 {
		t.Errorf("hyphen search = %d", l.Total)
	}
	if l := list("q=api&sort=-created_at&status=all"); l.Total != 1 {
		t.Errorf("search with sort = %d", l.Total)
	}
	rec := e.do(t, "GET", "/v1/issues?status=all", "", nil)
	if strings.Contains(rec.Body.String(), `"body"`) || strings.Contains(rec.Body.String(), `"snippet"`) {
		t.Errorf("list must omit body and snippet: %s", rec.Body.String())
	}
	for _, q := range []string{"status=bogus", "type=x", "priority=P7", "sort=relevance", "sort=title", "limit=0", "limit=201", "offset=-1",
		"project=A&project=B", "parent=bad-key", "q=" + strings.Repeat("a+", 11), "nope=1", "label=Bad"} {
		if rec := e.do(t, "GET", "/v1/issues?"+q, "", nil); rec.Code != 400 {
			t.Errorf("%s: status = %d: %s", q, rec.Code, rec.Body.String())
		}
	}
}

func TestCommentsAndActivity(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createIssue(t, `{"project":"AGB","title":"a"}`)
	e.createIssue(t, `{"project":"AGB","title":"b"}`)

	rec := e.do(t, "POST", "/v1/issues/AGB-1/comments", `{"body":"착수한다."}`, map[string]string{"X-Session-Id": "sess-9", "X-Client-Host": "ab"})
	if rec.Code != 201 {
		t.Fatalf("comment: %d %s", rec.Code, rec.Body.String())
	}
	var a model.Activity
	decodeJSON(t, rec, &a)
	if a.Kind != model.ActivityCommented || *a.Body != "착수한다." || a.SessionID != "sess-9" || a.ClientHost != "ab" || a.Actor != "claude" || a.Issue != "AGB-1" || a.Changes != nil {
		t.Errorf("comment = %+v", a)
	}
	if !strings.Contains(rec.Body.String(), `"changes":null`) {
		t.Errorf("changes must be null: %s", rec.Body.String())
	}
	for body, code := range map[string]int{`{"body":""}`: 422, `{}`: 422, `{"body":"x","extra":1}`: 400, `{"body":5}`: 400} {
		if rec = e.do(t, "POST", "/v1/issues/AGB-1/comments", body, nil); rec.Code != code {
			t.Errorf("comment %s: %d, want %d", body, rec.Code, code)
		}
	}
	if rec = e.do(t, "POST", "/v1/issues/AGB-9/comments", `{"body":"x"}`, nil); rec.Code != 404 {
		t.Errorf("comment missing issue: %d", rec.Code)
	}
	e.do(t, "PATCH", "/v1/issues/AGB-2", `{"status":"doing"}`, map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-sess"})

	rec = e.do(t, "GET", "/v1/issues/AGB-1/activity", "", nil)
	var acts listEnvelope[model.Activity]
	decodeJSON(t, rec, &acts)
	if rec.Code != 200 || acts.Total != 2 || acts.Items[0].Kind != model.ActivityCommented || acts.Items[1].Kind != model.ActivityCreated {
		t.Errorf("issue activity = %+v", acts)
	}
	if rec = e.do(t, "GET", "/v1/issues/AGB-9/activity", "", nil); rec.Code != 404 {
		t.Errorf("activity of missing issue: %d", rec.Code)
	}

	get := func(q string) listEnvelope[model.Activity] {
		t.Helper()
		rec := e.do(t, "GET", "/v1/activity?"+q, "", nil)
		if rec.Code != 200 {
			t.Fatalf("activity %s: %d %s", q, rec.Code, rec.Body.String())
		}
		var l listEnvelope[model.Activity]
		decodeJSON(t, rec, &l)
		return l
	}
	if l := get(""); l.Total != 4 || l.Items[0].Kind != model.ActivityUpdated {
		t.Errorf("all = %+v", l)
	}
	if l := get("session=owner-sess"); l.Total != 1 || l.Items[0].Actor != "owner" {
		t.Errorf("session = %+v", l)
	}
	if l := get("actor=claude&kind=commented"); l.Total != 1 {
		t.Errorf("actor+kind = %d", l.Total)
	}
	if l := get("field=status&kind=updated"); l.Total != 1 || l.Items[0].Issue != "AGB-2" {
		t.Errorf("field = %+v", l)
	}
	if l := get("issue=AGB-1,AGB-2&sort=at&limit=2"); len(l.Items) != 2 || l.Items[0].Kind != model.ActivityCreated || l.Total != 4 {
		t.Errorf("sort at = %+v", l)
	}
	if l := get("client_host=ab"); l.Total != 1 {
		t.Errorf("client_host = %d", l.Total)
	}
	since := acts.Items[0].At.String()
	if l := get("since=" + since); l.Total != 2 {
		t.Errorf("since = %d", l.Total)
	}
	if l := get("until=" + since); l.Total != 2 {
		t.Errorf("until = %d", l.Total)
	}
	for _, q := range []string{"kind=bogus", "field=nope", "since=yesterday", "sort=-id", "project=AGB&project=X"} {
		if rec := e.do(t, "GET", "/v1/activity?"+q, "", nil); rec.Code != 400 {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
	e.createProject(t, "ARC", "archived")
	e.createIssue(t, `{"project":"ARC","title":"c"}`)
	e.do(t, "PATCH", "/v1/projects/ARC", `{"status":"archived"}`, nil)
	if rec := e.do(t, "POST", "/v1/issues/ARC-1/comments", `{"body":"x"}`, nil); rec.Code != 409 || decodeErr(t, rec).Error.Details[0].Reason != "project_not_active" {
		t.Errorf("comment on archived project: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCommentSummary(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createIssue(t, `{"project":"AGB","title":"a"}`)
	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		return e.do(t, "POST", "/v1/issues/AGB-1/comments", body, nil)
	}

	rec := post(`{"summary":"선행 이슈 완료로 착수","body":"선행 이슈 AGB-10이 끝나 착수한다."}`)
	if rec.Code != 201 {
		t.Fatalf("comment with summary: %d %s", rec.Code, rec.Body.String())
	}
	var a model.Activity
	decodeJSON(t, rec, &a)
	if a.Kind != model.ActivityCommented || a.Summary == nil || *a.Summary != "선행 이슈 완료로 착수" || a.Body == nil || *a.Body != "선행 이슈 AGB-10이 끝나 착수한다." {
		t.Errorf("comment = %+v", a)
	}
	if !strings.Contains(rec.Body.String(), `"summary":"선행 이슈 완료로 착수"`) {
		t.Errorf("response lacks the summary: %s", rec.Body.String())
	}

	// A missing key and an explicit null both store no summary, and the key
	// is still present in the response.
	for _, body := range []string{`{"body":"x"}`, `{"summary":null,"body":"x"}`} {
		rec = post(body)
		if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"summary":null`) {
			t.Errorf("comment %s: %d %s", body, rec.Code, rec.Body.String())
		}
	}

	// 30 runes pass even though they take 90 bytes.
	if rec = post(`{"summary":"` + strings.Repeat("가", 30) + `","body":"x"}`); rec.Code != 201 {
		t.Errorf("30 runes: %d %s", rec.Code, rec.Body.String())
	}

	rejected := map[string]string{
		"empty":     `{"summary":"","body":"x"}`,
		"31 runes":  `{"summary":"` + strings.Repeat("가", 31) + `","body":"x"}`,
		"line feed": `{"summary":"서버\n랭킹","body":"x"}`,
		"carriage":  `{"summary":"서버\r랭킹","body":"x"}`,
	}
	for name, body := range rejected {
		rec = post(body)
		got := decodeErr(t, rec)
		if rec.Code != 422 || got.Error.Code != "validation_failed" || len(got.Error.Details) != 1 || got.Error.Details[0].Field != "summary" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// Every violation is reported at once.
	rec = post(`{"summary":"","body":""}`)
	if got := decodeErr(t, rec); rec.Code != 422 || len(got.Error.Details) != 2 || got.Error.Details[0].Field != "body" || got.Error.Details[1].Field != "summary" {
		t.Errorf("body and summary: %d %s", rec.Code, rec.Body.String())
	}
	if rec = post(`{"summary":"요약만"}`); rec.Code != 422 || decodeErr(t, rec).Error.Details[0].Field != "body" {
		t.Errorf("summary without body: %d %s", rec.Code, rec.Body.String())
	}

	for _, body := range []string{`{"summary":5,"body":"x"}`, `{"summary":["a"],"body":"x"}`, `{"summary":true,"body":"x"}`} {
		rec = post(body)
		got := decodeErr(t, rec)
		if rec.Code != 400 || got.Error.Code != "bad_request" || len(got.Error.Details) != 1 || got.Error.Details[0].Field != "summary" || got.Error.Details[0].Reason != "type_mismatch" {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec = post(`{"summary":"a","body":"x","extra":1}`); rec.Code != 400 {
		t.Errorf("unknown field next to summary: %d", rec.Code)
	}
}

// Every activity object carries the summary key: a string for a comment that
// has one, null for everything else.
func TestActivityResponsesAlwaysCarrySummary(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createIssue(t, `{"project":"AGB","title":"a"}`)
	e.do(t, "PATCH", "/v1/issues/AGB-1", `{"status":"doing"}`, nil)
	e.do(t, "POST", "/v1/issues/AGB-1/comments", `{"body":"요약 없는 댓글"}`, nil)
	e.do(t, "POST", "/v1/issues/AGB-1/comments", `{"summary":"서버 랭킹 함수 구현","body":"랭킹 함수를 추가했다."}`, nil)

	// Newest first: the summarized comment, the plain comment, the update, the creation.
	want := []string{`"서버 랭킹 함수 구현"`, `null`, `null`, `null`}
	type items struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	check := func(label string, got []map[string]json.RawMessage) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: %d items, want %d", label, len(got), len(want))
		}
		for n, it := range got {
			raw, ok := it["summary"]
			if !ok {
				t.Errorf("%s: item %d has no summary key: %v", label, n, it)
			} else if string(raw) != want[n] {
				t.Errorf("%s: item %d summary = %s, want %s", label, n, raw, want[n])
			}
		}
	}
	for _, target := range []string{"/v1/issues/AGB-1/activity", "/v1/activity"} {
		rec := e.do(t, "GET", target, "", nil)
		var l items
		decodeJSON(t, rec, &l)
		check(target, l.Items)
	}
	rec := e.do(t, "GET", "/v1/projects/AGB/overview", "", nil)
	var o struct {
		RecentActivity items `json:"recent_activity"`
	}
	decodeJSON(t, rec, &o)
	check("overview recent_activity", o.RecentActivity.Items)
}
