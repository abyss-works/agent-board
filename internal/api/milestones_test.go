package api

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func (e *env) createMilestone(t *testing.T, project, body string) model.Milestone {
	t.Helper()
	rec := e.do(t, "POST", "/v1/projects/"+project+"/milestones", body, nil)
	if rec.Code != 201 {
		t.Fatalf("create milestone %s: %d %s", body, rec.Code, rec.Body.String())
	}
	var m model.Milestone
	decodeJSON(t, rec, &m)
	return m
}

func milestonePath(id int64) string { return fmt.Sprintf("/v1/milestones/%d", id) }

func (e *env) listMilestones(t *testing.T, project, query string) []model.Milestone {
	t.Helper()
	rec := e.do(t, "GET", "/v1/projects/"+project+"/milestones"+query, "", nil)
	if rec.Code != 200 {
		t.Fatalf("list milestones %s: %d %s", query, rec.Code, rec.Body.String())
	}
	var l struct {
		Items []model.Milestone `json:"items"`
	}
	decodeJSON(t, rec, &l)
	return l.Items
}

func milestoneNames(items []model.Milestone) string {
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	return strings.Join(names, ",")
}

func TestCreateAndListMilestones(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")

	rec := e.do(t, "POST", "/v1/projects/AGB/milestones",
		`{"name":"v0.2.0","description":"운영 안정화와 릴리즈 관리","target_date":"2026-11-01"}`, nil)
	var m model.Milestone
	decodeJSON(t, rec, &m)
	if rec.Code != 201 || rec.Header().Get("Location") != milestonePath(m.ID) {
		t.Fatalf("create: %d %s %q", rec.Code, rec.Body.String(), rec.Header().Get("Location"))
	}
	if m.ID < 1 || m.Project != "AGB" || m.Name != "v0.2.0" || m.Description != "운영 안정화와 릴리즈 관리" || m.TargetDate == nil || *m.TargetDate != "2026-11-01" ||
		m.Status != model.MilestoneOpen || m.ClosedAt != nil || m.Progress != (model.MilestoneProgress{}) || m.CreatedBy != "claude" || m.UpdatedBy != "claude" {
		t.Errorf("milestone = %+v", m)
	}
	if rec.Header().Get("ETag") != "" {
		t.Errorf("a milestone has no version, so no ETag: %q", rec.Header().Get("ETag"))
	}
	for _, want := range []string{`"progress":{"total":0,"done":0}`, `"closed_at":null`, `"status":"open"`, `"target_date":"2026-11-01"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("JSON shape lacks %s: %s", want, rec.Body.String())
		}
	}

	// Only the name is required: the description is empty and the date is null.
	rec = e.do(t, "POST", "/v1/projects/AGB/milestones", `{"name":"날짜 없음"}`, map[string]string{"X-Actor": "owner"})
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"description":""`) || !strings.Contains(rec.Body.String(), `"target_date":null`) {
		t.Errorf("minimal create: %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, "POST", "/v1/projects/AGB/milestones", `{"name":"null 필드","description":null,"target_date":null}`, nil); rec.Code != 201 ||
		!strings.Contains(rec.Body.String(), `"description":""`) {
		t.Errorf("create with null fields: %d %s", rec.Code, rec.Body.String())
	}
	e.createMilestone(t, "AGB", `{"name":"v0.3.0","target_date":"2026-12-01"}`)
	e.createMilestone(t, "AGB", `{"name":"v0.1.5","target_date":"2026-10-20"}`)
	e.createMilestone(t, "OTH", `{"name":"다른 프로젝트"}`)

	// The list holds items only; open milestones by target date, the ones without a date last.
	rec = e.do(t, "GET", "/v1/projects/AGB/milestones", "", nil)
	var raw map[string]any
	decodeJSON(t, rec, &raw)
	if rec.Code != 200 || len(raw) != 1 || raw["items"] == nil {
		t.Errorf("the list response holds only items: %d %v", rec.Code, raw)
	}
	if got := milestoneNames(e.listMilestones(t, "AGB", "")); got != "v0.1.5,v0.2.0,v0.3.0,날짜 없음,null 필드" {
		t.Errorf("default list = %s", got)
	}

	// The status filter: open by default, closed, deleted, all, and commas are OR.
	e.do(t, "PATCH", milestonePath(m.ID), `{"status":"closed"}`, nil)
	deleted := e.createMilestone(t, "AGB", `{"name":"버릴 것","target_date":"2026-01-01"}`)
	e.do(t, "DELETE", milestonePath(deleted.ID), "", nil)
	for query, want := range map[string]string{
		"":                         "v0.1.5,v0.3.0,날짜 없음,null 필드",
		"?status=open":             "v0.1.5,v0.3.0,날짜 없음,null 필드",
		"?status=closed":           "v0.2.0",
		"?status=deleted":          "버릴 것",
		"?status=open,closed":      "v0.1.5,v0.2.0,v0.3.0,날짜 없음,null 필드",
		"?status=all":              "버릴 것,v0.1.5,v0.2.0,v0.3.0,날짜 없음,null 필드",
		"?status=deleted,closed":   "버릴 것,v0.2.0",
		"?status=closed,closed":    "v0.2.0",
		"?status=all,open":         "버릴 것,v0.1.5,v0.2.0,v0.3.0,날짜 없음,null 필드",
		"?status=open,bogus,":      "!400",
		"?status=bogus":            "!400",
		"?status=active":           "!400",
		"?status=open&status=done": "!400",
		"?status=,":                "!400",
		"?limit=5":                 "!400",
		"?offset=1":                "!400",
		"?sort=name":               "!400",
		"?x=1":                     "!400",
	} {
		if strings.HasPrefix(want, "!") {
			if rec := e.do(t, "GET", "/v1/projects/AGB/milestones"+query, "", nil); fmt.Sprint("!", rec.Code) != want {
				t.Errorf("%q: status %d, want %s", query, rec.Code, want[1:])
			}
			continue
		}
		if got := milestoneNames(e.listMilestones(t, "AGB", query)); got != want {
			t.Errorf("%q: %s, want %s", query, got, want)
		}
	}
	if rec := e.do(t, "GET", "/v1/projects/NOPE/milestones", "", nil); rec.Code != 404 {
		t.Errorf("missing project list: %d", rec.Code)
	}
	if items := e.listMilestones(t, "OTH", "?status=closed"); items == nil || len(items) != 0 {
		t.Errorf("an empty list must be [] not null: %#v", items)
	}
	if rec := e.do(t, "GET", "/v1/projects/OTH/milestones?status=closed", "", nil); !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("empty list JSON: %s", rec.Body.String())
	}
	for _, method := range []string{"PUT", "PATCH", "DELETE"} {
		rec := e.do(t, method, "/v1/projects/AGB/milestones", `{}`, nil)
		if rec.Code != 405 || rec.Header().Get("Allow") != "GET, POST" || decodeErr(t, rec).Error.Code != "method_not_allowed" {
			t.Errorf("%s: %d Allow=%q", method, rec.Code, rec.Header().Get("Allow"))
		}
	}
	if rec := e.do(t, "GET", "/v1/projects/AGB/milestones", "", map[string]string{"Authorization": "-"}); rec.Code != 401 {
		t.Errorf("without a token: %d", rec.Code)
	}
	if rec := e.do(t, "GET", "/v1/projects/AGB/milestones", "", map[string]string{"X-Actor": "-"}); rec.Code != 400 {
		t.Errorf("without a trace header: %d", rec.Code)
	}
}

func TestCreateMilestoneRules(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	post := func(body string) *httptest.ResponseRecorder {
		return e.do(t, "POST", "/v1/projects/AGB/milestones", body, nil)
	}

	for name, body := range map[string]string{
		"empty name":   `{"name":""}`,
		"missing name": `{}`,
		"null name":    `{"name":null}`,
		"51 runes":     `{"name":"` + strings.Repeat("가", 51) + `"}`,
	} {
		wantFieldError(t, post(body), 422, "name")
		_ = name
	}
	if rec := post(`{"name":"` + strings.Repeat("가", 50) + `"}`); rec.Code != 201 {
		t.Errorf("50 runes: %d %s", rec.Code, rec.Body.String())
	}
	wantFieldError(t, post(`{"name":"x","description":"`+strings.Repeat("가", 2001)+`"}`), 422, "description")
	if rec := post(`{"name":"긴 설명","description":"` + strings.Repeat("가", 2000) + `"}`); rec.Code != 201 {
		t.Errorf("2000 runes: %d %s", rec.Code, rec.Body.String())
	}
	for _, date := range []string{"", "2026-13-01", "2026-02-30", "26-11-01", "2026-1-1", "2026/11/01", "2026-11-01T00:00:00Z", "내일"} {
		wantFieldError(t, post(`{"name":"날짜 `+date+`","target_date":"`+date+`"}`), 422, "target_date")
	}
	if rec := post(`{"name":"윤일","target_date":"2028-02-29"}`); rec.Code != 201 {
		t.Errorf("leap day: %d %s", rec.Code, rec.Body.String())
	}
	// Every violation of one request comes back together.
	rec := post(`{"name":"","description":"` + strings.Repeat("가", 2001) + `","target_date":"soon"}`)
	if rec.Code != 422 || decodeErr(t, rec).Error.Code != "validation_failed" || len(decodeErr(t, rec).Error.Details) != 3 {
		t.Errorf("all violations: %d %s", rec.Code, rec.Body.String())
	}

	if rec := post(`{"name":"v0.3.0"}`); rec.Code != 201 {
		t.Fatalf("first v0.3.0: %d %s", rec.Code, rec.Body.String())
	}
	rec = post(`{"name":"v0.3.0"}`)
	wantReason(t, rec, 409, "duplicate")
	wantFieldError(t, rec, 409, "name")
	if b := decodeErr(t, rec); b.Error.Code != "conflict" {
		t.Errorf("duplicate code = %q", b.Error.Code)
	}

	// Fields that are not part of a create request are refused, including the server-managed ones.
	for _, field := range []string{"extra", "id", "project", "status", "progress", "closed_at", "created_at", "updated_at", "created_by", "updated_by"} {
		wantFieldError(t, post(`{"name":"x","`+field+`":1}`), 400, field)
	}
	wantFieldError(t, post(`{"name":5}`), 400, "name")
	wantFieldError(t, post(`{"name":"x","description":5}`), 400, "description")
	wantFieldError(t, post(`{"name":"x","target_date":20261101}`), 400, "target_date")
	for _, body := range []string{``, `[]`, `"x"`, `{`, `null`} {
		if rec := post(body); rec.Code != 400 {
			t.Errorf("body %q: %d", body, rec.Code)
		}
	}
	if rec := e.do(t, "POST", "/v1/projects/NOPE/milestones", `{"name":"x"}`, nil); rec.Code != 404 {
		t.Errorf("missing project: %d", rec.Code)
	}
	// A project that is archived or deleted takes no new milestone.
	for _, status := range []string{"archived", "deleted"} {
		e.do(t, "PATCH", "/v1/projects/AGB", `{"status":"`+status+`"}`, nil)
		wantReason(t, post(`{"name":"`+status+`일 때"}`), 409, "project_not_active")
	}
}

func TestGetPatchAndDeleteMilestone(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	m := e.createMilestone(t, "AGB", `{"name":"v0.2.0","target_date":"2026-11-01"}`)
	other := e.createMilestone(t, "AGB", `{"name":"v0.3.0"}`)
	path := milestonePath(m.ID)

	rec := e.do(t, "GET", path, "", nil)
	var got model.Milestone
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.ID != m.ID || got.Name != "v0.2.0" || got.Progress != (model.MilestoneProgress{}) {
		t.Fatalf("get: %d %+v", rec.Code, got)
	}
	for _, id := range []string{"9999", "abc", "0", "-1", "1.5"} {
		if rec := e.do(t, "GET", "/v1/milestones/"+id, "", nil); rec.Code != 404 || decodeErr(t, rec).Error.Code != "not_found" {
			t.Errorf("get %s: %d", id, rec.Code)
		}
		if rec := e.do(t, "PATCH", "/v1/milestones/"+id, `{"name":"x"}`, nil); rec.Code != 404 {
			t.Errorf("patch %s: %d", id, rec.Code)
		}
		if rec := e.do(t, "DELETE", "/v1/milestones/"+id, "", nil); rec.Code != 404 {
			t.Errorf("delete %s: %d", id, rec.Code)
		}
	}
	if rec := e.do(t, "GET", path+"?x=1", "", nil); rec.Code != 400 {
		t.Errorf("unknown parameter: %d", rec.Code)
	}

	// PATCH: the user edits; created_by stays with the creator.
	rec = e.do(t, "PATCH", path, `{"name":"v0.2.1","description":"설명","target_date":"2026-11-15"}`, map[string]string{"X-Actor": "owner"})
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.Name != "v0.2.1" || got.Description != "설명" || got.TargetDate == nil || *got.TargetDate != "2026-11-15" ||
		got.UpdatedBy != "owner" || got.CreatedBy != "claude" || got.CreatedAt != m.CreatedAt {
		t.Fatalf("patch: %d %+v", rec.Code, got)
	}
	if rec.Header().Get("ETag") != "" {
		t.Errorf("ETag on a milestone: %q", rec.Header().Get("ETag"))
	}
	rec = e.do(t, "PATCH", path, `{"target_date":null}`, nil)
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.TargetDate != nil || got.UpdatedBy != "claude" || !strings.Contains(rec.Body.String(), `"target_date":null`) {
		t.Errorf("clear target date: %d %s", rec.Code, rec.Body.String())
	}
	// A request that changes nothing keeps updated_at and the actor of the last real change.
	before := got.UpdatedAt
	for _, body := range []string{`{}`, `{"name":"v0.2.1"}`, `{"description":"설명","target_date":null}`, `{"status":"open"}`} {
		rec = e.do(t, "PATCH", path, body, map[string]string{"X-Actor": "owner"})
		decodeJSON(t, rec, &got)
		if rec.Code != 200 || got.UpdatedAt != before || got.UpdatedBy != "claude" {
			t.Errorf("no-op patch %s: %d updated_at %v -> %v by %s", body, rec.Code, before, got.UpdatedAt, got.UpdatedBy)
		}
	}

	// Rule violations on update.
	for body, field := range map[string]string{
		`{"name":""}`:   "name",
		`{"name":null}`: "name",
		`{"name":"` + strings.Repeat("가", 51) + `"}`:          "name",
		`{"description":null}`:                                "description",
		`{"description":"` + strings.Repeat("가", 2001) + `"}`: "description",
		`{"target_date":"2026-02-30"}`:                        "target_date",
		`{"target_date":""}`:                                  "target_date",
		`{"status":null}`:                                     "status",
		`{"status":"gone"}`:                                   "status",
		`{"status":"all"}`:                                    "status",
	} {
		wantFieldError(t, e.do(t, "PATCH", path, body, nil), 422, field)
	}
	for _, field := range []string{"id", "project", "progress", "closed_at", "created_at", "updated_at", "created_by", "updated_by", "nope"} {
		wantFieldError(t, e.do(t, "PATCH", path, `{"`+field+`":1}`, nil), 400, field)
	}
	wantFieldError(t, e.do(t, "PATCH", path, `{"name":5}`, nil), 400, "name")
	wantFieldError(t, e.do(t, "PATCH", path, `{"status":5}`, nil), 400, "status")
	wantFieldError(t, e.do(t, "PATCH", path, `{"target_date":5}`, nil), 400, "target_date")
	wantReason(t, e.do(t, "PATCH", path, `{"name":"v0.3.0"}`, nil), 409, "duplicate")
	if after := decodeMilestone(t, e.do(t, "GET", path, "", nil)); after.Name != "v0.2.1" {
		t.Errorf("a refused patch changed the milestone: %+v", after)
	}

	// Closing records closed_at and reopening clears it.
	rec = e.do(t, "PATCH", path, `{"status":"closed"}`, nil)
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.Status != model.MilestoneClosed || got.ClosedAt == nil || *got.ClosedAt != got.UpdatedAt {
		t.Fatalf("close: %d %+v", rec.Code, got)
	}
	if !strings.Contains(rec.Body.String(), `"closed_at":"`+got.ClosedAt.String()+`"`) {
		t.Errorf("closed_at shape: %s", rec.Body.String())
	}
	closedAt := *got.ClosedAt
	rec = e.do(t, "PATCH", path, `{"description":"닫은 뒤의 설명"}`, nil)
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.ClosedAt == nil || *got.ClosedAt != closedAt || got.Status != model.MilestoneClosed {
		t.Errorf("an edit keeps closed_at: %d %+v", rec.Code, got)
	}
	rec = e.do(t, "PATCH", path, `{"status":"open"}`, nil)
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.Status != model.MilestoneOpen || got.ClosedAt != nil || !strings.Contains(rec.Body.String(), `"closed_at":null`) {
		t.Errorf("reopen: %d %+v", rec.Code, got)
	}

	// DELETE is a status change to deleted, and a deleted milestone stays readable and comes back with a status patch.
	rec = e.do(t, "DELETE", path, "", nil)
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.Status != model.MilestoneDeleted || got.ID != m.ID {
		t.Fatalf("delete: %d %+v", rec.Code, got)
	}
	if rec = e.do(t, "DELETE", path, "", nil); rec.Code != 200 {
		t.Errorf("delete again: %d", rec.Code)
	}
	if rec = e.do(t, "DELETE", path+"?x=1", "", nil); rec.Code != 400 {
		t.Errorf("delete with a parameter: %d", rec.Code)
	}
	if g := decodeMilestone(t, e.do(t, "GET", path, "", nil)); g.Status != model.MilestoneDeleted {
		t.Errorf("read after delete: %+v", g)
	}
	// Its name is free while it is deleted, so restoring it into a taken name is a conflict.
	e.do(t, "PATCH", milestonePath(other.ID), `{"name":"v0.2.1"}`, nil)
	wantReason(t, e.do(t, "PATCH", path, `{"status":"open"}`, nil), 409, "duplicate")
	e.do(t, "PATCH", milestonePath(other.ID), `{"name":"v0.3.0"}`, nil)
	rec = e.do(t, "PATCH", path, `{"status":"open"}`, nil)
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.Status != model.MilestoneOpen {
		t.Errorf("restore: %d %+v", rec.Code, got)
	}

	for _, method := range []string{"POST", "PUT"} {
		rec := e.do(t, method, path, `{}`, nil)
		if rec.Code != 405 || rec.Header().Get("Allow") != "DELETE, GET, PATCH" {
			t.Errorf("%s: %d Allow=%q", method, rec.Code, rec.Header().Get("Allow"))
		}
	}

	// A project that is not active takes no change; a request that changes nothing and every read still work.
	for _, status := range []string{"archived", "deleted"} {
		e.do(t, "PATCH", "/v1/projects/AGB", `{"status":"`+status+`"}`, nil)
		for name, rec := range map[string]*httptest.ResponseRecorder{
			"rename": e.do(t, "PATCH", path, `{"name":"바꾼 이름"}`, nil),
			"close":  e.do(t, "PATCH", path, `{"status":"closed"}`, nil),
			"delete": e.do(t, "DELETE", path, "", nil),
		} {
			wantReason(t, rec, 409, "project_not_active")
			_ = name
		}
		if rec := e.do(t, "PATCH", path, `{"name":"v0.2.1"}`, nil); rec.Code != 200 {
			t.Errorf("%s project: no-op patch: %d", status, rec.Code)
		}
		if rec := e.do(t, "GET", path, "", nil); rec.Code != 200 {
			t.Errorf("%s project: get: %d", status, rec.Code)
		}
		if items := e.listMilestones(t, "AGB", "?status=all"); len(items) != 2 {
			t.Errorf("%s project: list: %d items", status, len(items))
		}
	}
}

func decodeMilestone(t *testing.T, rec *httptest.ResponseRecorder) model.Milestone {
	t.Helper()
	var m model.Milestone
	decodeJSON(t, rec, &m)
	return m
}

func TestMilestoneProgressOnTheAPI(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	m := e.createMilestone(t, "AGB", `{"name":"v0.2.0"}`)
	other := e.createMilestone(t, "AGB", `{"name":"v0.3.0"}`)
	issue := func(status string, milestone int64) model.Issue {
		return e.createIssue(t, fmt.Sprintf(`{"project":"AGB","title":"%s","status":"%s","milestone":%d}`, status, status, milestone))
	}
	progress := func(id int64) model.MilestoneProgress {
		t.Helper()
		return decodeMilestone(t, e.do(t, "GET", milestonePath(id), "", nil)).Progress
	}

	todo := issue("todo", m.ID)
	issue("doing", m.ID)
	done := issue("done", m.ID)
	cancelled := issue("backlog", m.ID)
	issue("done", other.ID)
	if p := progress(m.ID); p != (model.MilestoneProgress{Total: 4, Done: 1}) {
		t.Errorf("progress = %+v", p)
	}
	if rec := e.do(t, "PATCH", "/v1/issues/"+todo.Key, `{"status":"done"}`, nil); rec.Code != 200 {
		t.Fatalf("finish: %d %s", rec.Code, rec.Body.String())
	}
	if p := progress(m.ID); p != (model.MilestoneProgress{Total: 4, Done: 2}) {
		t.Errorf("after finishing an issue = %+v", p)
	}
	if rec := e.do(t, "PATCH", "/v1/issues/"+cancelled.Key, `{"status":"cancelled","status_reason":"정리"}`, nil); rec.Code != 200 {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "DELETE", "/v1/issues/"+done.Key+"?reason=%EC%A0%95%EB%A6%AC", "", nil); rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if p := progress(m.ID); p != (model.MilestoneProgress{Total: 2, Done: 1}) {
		t.Errorf("after cancelling and deleting = %+v", p)
	}
	if rec := e.do(t, "PATCH", "/v1/issues/"+todo.Key, fmt.Sprintf(`{"milestone":%d}`, other.ID), nil); rec.Code != 200 {
		t.Fatalf("move: %d %s", rec.Code, rec.Body.String())
	}
	if a, b := progress(m.ID), progress(other.ID); a != (model.MilestoneProgress{Total: 1, Done: 0}) || b != (model.MilestoneProgress{Total: 2, Done: 2}) {
		t.Errorf("after moving an issue: %+v and %+v", a, b)
	}

	// The list carries the progress of each milestone.
	items := e.listMilestones(t, "AGB", "")
	if len(items) != 2 || items[0].Progress != (model.MilestoneProgress{Total: 1, Done: 0}) || items[1].Progress != (model.MilestoneProgress{Total: 2, Done: 2}) {
		t.Errorf("list progress = %+v", items)
	}
	rec := e.do(t, "GET", "/v1/projects/AGB/milestones", "", nil)
	if !strings.Contains(rec.Body.String(), `"progress":{"total":1,"done":0}`) {
		t.Errorf("list JSON: %s", rec.Body.String())
	}
}

func TestIssueMilestoneOnTheAPI(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")
	v2 := e.createMilestone(t, "AGB", `{"name":"v0.2.0"}`)
	v3 := e.createMilestone(t, "AGB", `{"name":"v0.3.0"}`)
	foreign := e.createMilestone(t, "OTH", `{"name":"다른 프로젝트"}`)
	gone := e.createMilestone(t, "AGB", `{"name":"버릴 것"}`)
	e.do(t, "DELETE", milestonePath(gone.ID), "", nil)

	rec := e.postIssue(t, fmt.Sprintf(`{"project":"AGB","title":"마일스톤이 붙은 이슈","milestone":%d}`, v2.ID), nil)
	var i model.Issue
	decodeJSON(t, rec, &i)
	if rec.Code != 201 || i.Milestone == nil || *i.Milestone != v2.ID || i.MilestoneName == nil || *i.MilestoneName != "v0.2.0" {
		t.Fatalf("create with a milestone: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), fmt.Sprintf(`"milestone":%d,"milestone_name":"v0.2.0"`, v2.ID)) {
		t.Errorf("JSON shape: %s", rec.Body.String())
	}
	rec = e.postIssue(t, `{"project":"AGB","title":"마일스톤 없음","milestone":null}`, nil)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"milestone":null`) || !strings.Contains(rec.Body.String(), `"milestone_name":null`) {
		t.Errorf("create without a milestone: %d %s", rec.Code, rec.Body.String())
	}
	e.createIssue(t, fmt.Sprintf(`{"project":"AGB","title":"v0.3.0 이슈","milestone":%d}`, v3.ID))
	e.createIssue(t, fmt.Sprintf(`{"project":"AGB","title":"한 번 더","milestone":%d}`, v2.ID))

	// Refusals: another project, missing, deleted, wrong type.
	wantFieldError(t, e.postIssue(t, fmt.Sprintf(`{"project":"AGB","title":"x","milestone":%d}`, foreign.ID), nil), 422, "milestone")
	wantFieldError(t, e.postIssue(t, `{"project":"AGB","title":"x","milestone":9999}`, nil), 422, "milestone")
	wantFieldError(t, e.postIssue(t, fmt.Sprintf(`{"project":"AGB","title":"x","milestone":%d}`, gone.ID), nil), 422, "milestone")
	wantFieldError(t, e.postIssue(t, `{"project":"AGB","title":"x","milestone":"abc"}`, nil), 400, "milestone")
	// Both a wrong topic and a wrong milestone come back in one answer.
	rec = e.postIssue(t, `{"project":"AGB","title":"x","topic":9999,"milestone":9999}`, nil)
	wantFieldError(t, rec, 422, "topic")
	wantFieldError(t, rec, 422, "milestone")

	// PATCH: move, clear, refuse.
	rec = e.do(t, "PATCH", "/v1/issues/AGB-2", fmt.Sprintf(`{"milestone":%d}`, v3.ID), nil)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.MilestoneName == nil || *i.MilestoneName != "v0.3.0" || i.Version != 2 || rec.Header().Get("ETag") != `"2"` {
		t.Errorf("set: %d %+v", rec.Code, i)
	}
	rec = e.do(t, "PATCH", "/v1/issues/AGB-2", `{"milestone":null}`, nil)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.Milestone != nil || i.MilestoneName != nil || i.Version != 3 {
		t.Errorf("clear: %d %+v", rec.Code, i)
	}
	rec = e.do(t, "PATCH", "/v1/issues/AGB-2", `{"milestone":null}`, nil)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.Version != 3 {
		t.Errorf("clearing a cleared milestone changes nothing: %d %+v", rec.Code, i)
	}
	wantFieldError(t, e.do(t, "PATCH", "/v1/issues/AGB-2", fmt.Sprintf(`{"milestone":%d}`, foreign.ID), nil), 422, "milestone")
	wantFieldError(t, e.do(t, "PATCH", "/v1/issues/AGB-2", fmt.Sprintf(`{"milestone":%d}`, gone.ID), nil), 422, "milestone")
	wantFieldError(t, e.do(t, "PATCH", "/v1/issues/AGB-2", `{"milestone":"abc"}`, nil), 400, "milestone")

	// An issue that already has a milestone is not blocked when the milestone is deleted later.
	e.do(t, "DELETE", milestonePath(v3.ID), "", nil)
	rec = e.do(t, "PATCH", "/v1/issues/AGB-3", `{"title":"제목 수정"}`, nil)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.MilestoneName == nil || *i.MilestoneName != "v0.3.0" {
		t.Errorf("edit of an issue whose milestone was deleted: %d %s", rec.Code, rec.Body.String())
	}
	e.do(t, "PATCH", milestonePath(v3.ID), `{"status":"open"}`, nil)

	// Filter: ids, commas are OR, none selects the rest.
	list := func(q string) string {
		t.Helper()
		rec := e.do(t, "GET", "/v1/issues?sort=key&"+q, "", nil)
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
	if got := list(fmt.Sprintf("status=all&milestone=%d", v2.ID)); got != "AGB-1,AGB-4" {
		t.Errorf("one milestone = %s", got)
	}
	if got := list(fmt.Sprintf("status=all&milestone=%d", v3.ID)); got != "AGB-3" {
		t.Errorf("another milestone = %s", got)
	}
	if got := list(fmt.Sprintf("status=all&milestone=%d,%d", v2.ID, v3.ID)); got != "AGB-1,AGB-3,AGB-4" {
		t.Errorf("OR = %s", got)
	}
	if got := list("status=all&milestone=none"); got != "AGB-2" {
		t.Errorf("none = %s", got)
	}
	if got := list(fmt.Sprintf("status=all&milestone=none,%d", v3.ID)); got != "AGB-2,AGB-3" {
		t.Errorf("none or a milestone = %s", got)
	}
	if got := list("status=all&milestone=9999"); got != "" {
		t.Errorf("unknown milestone = %q", got)
	}
	// The default status filter (open) applies together with the milestone filter.
	e.do(t, "PATCH", "/v1/issues/AGB-4", `{"status":"done"}`, nil)
	if got := list(fmt.Sprintf("milestone=%d&project=AGB", v2.ID)); got != "AGB-1" {
		t.Errorf("milestone with the default status filter = %s", got)
	}
	if got := list(fmt.Sprintf("milestone=%d&project=AGB&status=done", v2.ID)); got != "AGB-4" {
		t.Errorf("milestone with status=done = %s", got)
	}
	for _, q := range []string{"milestone=abc", "milestone=0", "milestone=-1", "milestone=1.5", "milestone=1&milestone=2", "milestone=none,abc"} {
		if rec := e.do(t, "GET", "/v1/issues?"+q, "", nil); rec.Code != 400 {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
	// Search results, the board, the queue and the overview carry the milestone too.
	rec = e.do(t, "GET", "/v1/issues?q=%EC%88%98%EC%A0%95&status=all", "", nil) // the title AGB-3 got above: "제목 수정"
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"key":"AGB-3"`) || !strings.Contains(rec.Body.String(), `"milestone_name":"v0.3.0"`) {
		t.Errorf("search item: %d %s", rec.Code, rec.Body.String())
	}
	for _, target := range []string{"/v1/board?project=AGB", "/v1/queue?project=AGB&status=backlog", "/v1/projects/AGB/overview"} {
		rec = e.do(t, "GET", target, "", nil)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"milestone_name":"v0.2.0"`) {
			t.Errorf("%s: %d lacks milestone_name", target, rec.Code)
		}
	}

	// The activity record: created lists the milestone, an update lists the change, and the field filter finds it.
	rec = e.do(t, "GET", "/v1/issues/AGB-1/activity", "", nil)
	var acts listEnvelope[model.Activity]
	decodeJSON(t, rec, &acts)
	if rec.Code != 200 || acts.Total != 1 || string(acts.Items[0].Changes["milestone"].To) != fmt.Sprint(v2.ID) {
		t.Errorf("created record: %d %+v", rec.Code, acts)
	}
	rec = e.do(t, "GET", "/v1/activity?field=milestone&kind=updated&sort=at", "", nil)
	decodeJSON(t, rec, &acts)
	if rec.Code != 200 || acts.Total != 2 || acts.Items[0].Issue != "AGB-2" {
		t.Fatalf("activity by milestone: %d %+v", rec.Code, acts)
	}
	first, second := acts.Items[0].Changes["milestone"], acts.Items[1].Changes["milestone"]
	if string(first.From) != "null" || string(first.To) != fmt.Sprint(v3.ID) || string(second.From) != fmt.Sprint(v3.ID) || string(second.To) != "null" {
		t.Errorf("milestone changes = %+v then %+v", first, second)
	}
	if rec := e.do(t, "GET", "/v1/activity?field=milestone_name", "", nil); rec.Code != 400 {
		t.Errorf("a derived field is not a change field: %d", rec.Code)
	}

	// An archived project takes no change of an issue's milestone.
	e.do(t, "PATCH", "/v1/projects/AGB", `{"status":"archived"}`, nil)
	wantReason(t, e.do(t, "PATCH", "/v1/issues/AGB-2", fmt.Sprintf(`{"milestone":%d}`, v2.ID), nil), 409, "project_not_active")
}
