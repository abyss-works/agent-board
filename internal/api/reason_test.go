package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func wantFieldError(t *testing.T, rec *httptest.ResponseRecorder, status int, field string) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("status = %d, want %d: %s", rec.Code, status, rec.Body.String())
		return
	}
	for _, d := range decodeErr(t, rec).Error.Details {
		if d.Field == field {
			return
		}
	}
	t.Errorf("details lack field %s: %s", field, rec.Body.String())
}

func TestStatusReasonOnPatch(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createIssue(t, `{"project":"AGB","title":"a","status":"todo"}`)

	// An ordinary change without a reason gets the default message.
	rec := e.do(t, "PATCH", "/v1/issues/AGB-1", `{"status":"doing"}`, nil)
	var i model.Issue
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.StatusReason == nil || *i.StatusReason != "할 일 → 진행" {
		t.Fatalf("default reason: %d %v", rec.Code, i.StatusReason)
	}
	if !strings.Contains(rec.Body.String(), `"status_reason":"할 일 → 진행"`) {
		t.Errorf("response lacks status_reason: %s", rec.Body.String())
	}
	rec = e.do(t, "PATCH", "/v1/issues/AGB-1", `{"status":"review","status_reason":"구현을 마쳐 검토를 요청한다"}`, nil)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || *i.StatusReason != "구현을 마쳐 검토를 요청한다" {
		t.Fatalf("given reason: %d %v", rec.Code, i.StatusReason)
	}

	// Cancelling needs one.
	wantFieldError(t, e.do(t, "PATCH", "/v1/issues/AGB-1", `{"status":"cancelled"}`, nil), 422, "status_reason")
	wantFieldError(t, e.do(t, "PATCH", "/v1/issues/AGB-1", `{"status":"deleted"}`, nil), 422, "status_reason")
	rec = e.do(t, "PATCH", "/v1/issues/AGB-1", `{"status":"cancelled","status_reason":"AGB-20과 중복"}`, nil)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.Status != model.StatusCancelled || *i.StatusReason != "AGB-20과 중복" || i.ClosedAt == nil {
		t.Errorf("cancel: %d %+v", rec.Code, i)
	}

	// A reason without a status change, with the same status, empty, or too long.
	for name, body := range map[string]string{
		"reason alone":        `{"status_reason":"이유"}`,
		"same status":         `{"status":"cancelled","status_reason":"이유"}`,
		"empty":               `{"status":"todo","status_reason":""}`,
		"501 characters":      `{"status":"todo","status_reason":"` + strings.Repeat("가", 501) + `"}`,
		"reason with a title": `{"title":"x","status_reason":"이유"}`,
	} {
		wantFieldError(t, e.do(t, "PATCH", "/v1/issues/AGB-1", body, nil), 422, "status_reason")
		_ = name
	}
	wantFieldError(t, e.do(t, "PATCH", "/v1/issues/AGB-1", `{"status":"todo","status_reason":null}`, nil), 422, "status_reason")
	wantFieldError(t, e.do(t, "PATCH", "/v1/issues/AGB-1", `{"status":"todo","status_reason":5}`, nil), 400, "status_reason")
	rec = e.do(t, "PATCH", "/v1/issues/AGB-1", `{"status":"todo","status_reason":"`+strings.Repeat("가", 500)+`"}`, nil)
	if rec.Code != 200 {
		t.Errorf("500 characters: %d %s", rec.Code, rec.Body.String())
	}

	// Violations come back together with the other field problems.
	rec = e.do(t, "PATCH", "/v1/issues/AGB-1", `{"title":"","status":"deleted"}`, nil)
	b := decodeErr(t, rec)
	if rec.Code != 422 || len(b.Error.Details) != 2 || b.Error.Details[0].Field != "title" || b.Error.Details[1].Field != "status_reason" {
		t.Errorf("combined: %d %s", rec.Code, rec.Body.String())
	}

	// status_reason is server managed on creation.
	wantFieldError(t, e.do(t, "POST", "/v1/issues", `{"project":"AGB","title":"x","status_reason":"이유"}`, nil), 400, "status_reason")
	rec = e.do(t, "POST", "/v1/issues", `{"project":"AGB","title":"born cancelled","status":"cancelled"}`, nil)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"status_reason":null`) {
		t.Errorf("created cancelled: %d %s", rec.Code, rec.Body.String())
	}

	// The record carries the reason and the field filter accepts it.
	rec = e.do(t, "GET", "/v1/activity?issue=AGB-1&field=status_reason&kind=updated&sort=at", "", nil)
	var acts listEnvelope[model.Activity]
	decodeJSON(t, rec, &acts)
	if rec.Code != 200 || acts.Total < 3 || string(acts.Items[0].Changes["status_reason"].To) != `"할 일 → 진행"` {
		t.Errorf("activity by status_reason: %d %+v", rec.Code, acts)
	}
	// Lists and the single read return it.
	rec = e.do(t, "GET", "/v1/issues?status=all&project=AGB&sort=key", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status_reason":"`+strings.Repeat("가", 500)+`"`) {
		t.Errorf("list lacks status_reason: %d", rec.Code)
	}
}

func TestDeleteIssueNeedsReason(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createIssue(t, `{"project":"AGB","title":"a"}`)
	e.createIssue(t, `{"project":"AGB","title":"b"}`)

	wantFieldError(t, e.do(t, "DELETE", "/v1/issues/AGB-1", "", nil), 422, "status_reason")
	wantFieldError(t, e.do(t, "DELETE", "/v1/issues/AGB-1?reason=", "", nil), 422, "status_reason")
	wantFieldError(t, e.do(t, "DELETE", "/v1/issues/AGB-1?reason="+strings.Repeat("a", 501), "", nil), 422, "status_reason")
	if rec := e.do(t, "DELETE", "/v1/issues/AGB-1?reason=x&other=1", "", nil); rec.Code != 400 {
		t.Errorf("unknown parameter: %d", rec.Code)
	}
	if rec := e.do(t, "DELETE", "/v1/issues/AGB-1?reason=a&reason=b", "", nil); rec.Code != 400 {
		t.Errorf("repeated reason: %d", rec.Code)
	}
	if rec := e.do(t, "GET", "/v1/issues/AGB-1", "", nil); !strings.Contains(rec.Body.String(), `"status":"backlog"`) {
		t.Errorf("a refused delete changed the issue: %s", rec.Body.String())
	}

	rec := e.do(t, "DELETE", "/v1/issues/AGB-1?reason=%EC%9E%98%EB%AA%BB+%EB%A7%8C%EB%93%A0+%EC%9D%B4%EC%8A%88", "", nil)
	var i model.Issue
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.Status != model.StatusDeleted || i.StatusReason == nil || *i.StatusReason != "잘못 만든 이슈" || rec.Header().Get("ETag") != `"2"` {
		t.Fatalf("delete: %d %+v", rec.Code, i)
	}
	rec = e.do(t, "GET", "/v1/issues/AGB-1/activity", "", nil)
	var acts listEnvelope[model.Activity]
	decodeJSON(t, rec, &acts)
	ch := acts.Items[0].Changes
	if acts.Items[0].Kind != model.ActivityUpdated || string(ch["status"].To) != `"deleted"` || string(ch["status_reason"].To) != `"잘못 만든 이슈"` {
		t.Errorf("delete record = %+v", acts.Items[0])
	}
	// The status did not change, so a second delete is a refused reason.
	wantFieldError(t, e.do(t, "DELETE", "/v1/issues/AGB-1?reason=again", "", nil), 422, "status_reason")
	// If-Match applies.
	if rec := e.do(t, "DELETE", "/v1/issues/AGB-2?reason=x", "", map[string]string{"If-Match": `"9"`}); rec.Code != 412 {
		t.Errorf("stale If-Match: %d", rec.Code)
	}
	if rec := e.do(t, "DELETE", "/v1/issues/AGB-2?reason=x", "", map[string]string{"If-Match": `"1"`}); rec.Code != 200 {
		t.Errorf("matching If-Match: %d %s", rec.Code, rec.Body.String())
	}
	// Reviving falls back to the default message.
	rec = e.do(t, "PATCH", "/v1/issues/AGB-1", `{"status":"backlog"}`, nil)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || *i.StatusReason != "삭제 → 백로그" {
		t.Errorf("revive: %d %v", rec.Code, i.StatusReason)
	}
}
