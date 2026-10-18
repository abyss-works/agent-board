package api

import (
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func TestAcknowledgeEndpoints(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	owner := map[string]string{"X-Actor": "owner"}
	e.createIssue(t, `{"project":"AGB","title":"agent made"}`) // claude is an agent
	rec := e.do(t, "POST", "/v1/issues", `{"project":"AGB","title":"user made"}`, owner)
	var made model.Issue
	decodeJSON(t, rec, &made)
	if rec.Code != 201 || made.AcknowledgedAt == nil || made.AcknowledgedBy == nil || *made.AcknowledgedBy != "owner" || *made.AcknowledgedAt != made.CreatedAt {
		t.Fatalf("an issue created by the user is acknowledged: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "GET", "/v1/issues/AGB-1", "", nil)
	if !strings.Contains(rec.Body.String(), `"acknowledged_at":null`) || !strings.Contains(rec.Body.String(), `"acknowledged_by":null`) {
		t.Fatalf("an issue created by an agent is unacknowledged: %s", rec.Body.String())
	}

	rec = e.do(t, "POST", "/v1/issues/AGB-1/acknowledge", "", owner)
	var i model.Issue
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.AcknowledgedAt == nil || *i.AcknowledgedBy != "owner" || i.Version != 2 || rec.Header().Get("ETag") != `"2"` {
		t.Fatalf("acknowledge: %d %s etag=%q", rec.Code, rec.Body.String(), rec.Header().Get("ETag"))
	}
	// Again: 200, nothing written.
	rec = e.do(t, "POST", "/v1/issues/AGB-1/acknowledge", "", owner)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.Version != 2 || rec.Header().Get("ETag") != `"2"` {
		t.Errorf("second acknowledge: %d version %d", rec.Code, i.Version)
	}
	rec = e.do(t, "GET", "/v1/issues/AGB-1/activity", "", nil)
	var acts listEnvelope[model.Activity]
	decodeJSON(t, rec, &acts)
	if acts.Total != 2 || acts.Items[0].Actor != "owner" || string(acts.Items[0].Changes["acknowledged"].To) != `true` || len(acts.Items[0].Changes) != 1 {
		t.Errorf("activity = %+v", acts)
	}

	// Unacknowledge. The mark is the only thing that changes.
	rec = e.do(t, "DELETE", "/v1/issues/AGB-1/acknowledge", "", owner)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.AcknowledgedAt != nil || i.AcknowledgedBy != nil || i.Version != 3 || i.Status != model.StatusBacklog {
		t.Errorf("unacknowledge: %d %+v", rec.Code, i)
	}
	if rec = e.do(t, "DELETE", "/v1/issues/AGB-1/acknowledge", "", owner); rec.Code != 200 || rec.Header().Get("ETag") != `"3"` {
		t.Errorf("second unacknowledge: %d etag=%q", rec.Code, rec.Header().Get("ETag"))
	}

	// If-Match, also on a request that would change nothing.
	if rec = e.do(t, "POST", "/v1/issues/AGB-1/acknowledge", "", map[string]string{"X-Actor": "owner", "If-Match": `"1"`}); rec.Code != 412 {
		t.Errorf("stale If-Match: %d", rec.Code)
	}
	if rec = e.do(t, "POST", "/v1/issues/AGB-1/acknowledge", "", map[string]string{"X-Actor": "owner", "If-Match": `abc`}); rec.Code != 400 {
		t.Errorf("malformed If-Match: %d", rec.Code)
	}
	if rec = e.do(t, "POST", "/v1/issues/AGB-1/acknowledge", "", map[string]string{"X-Actor": "owner", "If-Match": `"3"`}); rec.Code != 200 {
		t.Errorf("matching If-Match: %d %s", rec.Code, rec.Body.String())
	}

	if rec = e.do(t, "POST", "/v1/issues/AGB-9/acknowledge", "", owner); rec.Code != 404 {
		t.Errorf("missing issue: %d", rec.Code)
	}
	if rec = e.do(t, "POST", "/v1/issues/AGB-1/acknowledge?x=1", "", owner); rec.Code != 400 {
		t.Errorf("unknown query parameter: %d", rec.Code)
	}
	rec = e.do(t, "GET", "/v1/issues/AGB-1/acknowledge", "", owner)
	if rec.Code != 405 || rec.Header().Get("Allow") != "DELETE, POST" {
		t.Errorf("GET: %d Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
	e.do(t, "PATCH", "/v1/projects/AGB", `{"status":"archived"}`, nil)
	rec = e.do(t, "DELETE", "/v1/issues/AGB-1/acknowledge", "", owner)
	if rec.Code != 409 || decodeErr(t, rec).Error.Details[0].Reason != "project_not_active" {
		t.Errorf("archived project: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAcknowledgedFilterAndOverview(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	owner := map[string]string{"X-Actor": "owner"}
	e.createIssue(t, `{"project":"AGB","title":"a"}`)
	e.createIssue(t, `{"project":"AGB","title":"b"}`)
	e.createIssue(t, `{"project":"AGB","title":"c","status":"done"}`)
	e.do(t, "POST", "/v1/issues", `{"project":"AGB","title":"by the user"}`, owner)
	e.do(t, "POST", "/v1/issues/AGB-2/acknowledge", "", owner)
	e.do(t, "DELETE", "/v1/issues/AGB-3?reason=x", "", nil)

	list := func(q string) listEnvelope[model.IssueSummary] {
		t.Helper()
		rec := e.do(t, "GET", "/v1/issues?status=all&sort=key&"+q, "", nil)
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
	if l := list("acknowledged=true"); keys(l) != "AGB-2,AGB-4" || l.Items[0].AcknowledgedAt == nil {
		t.Errorf("acknowledged=true: %s", keys(l))
	}
	if l := list("acknowledged=false"); keys(l) != "AGB-1,AGB-3" || l.Items[0].AcknowledgedBy != nil {
		t.Errorf("acknowledged=false: %s", keys(l))
	}
	if l := list(""); l.Total != 4 {
		t.Errorf("no filter: %d", l.Total)
	}
	for _, q := range []string{"acknowledged=yes", "acknowledged=true&acknowledged=false", "acknowledged=TRUE", "acknowledged=1"} {
		if rec := e.do(t, "GET", "/v1/issues?"+q, "", nil); rec.Code != 400 {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}

	// The status view lists unacknowledged issues that are not deleted, newest first.
	rec := e.do(t, "GET", "/v1/projects/AGB/overview", "", nil)
	var o store.Overview
	decodeJSON(t, rec, &o)
	if rec.Code != 200 || o.Unacknowledged.Total != 1 || o.Unacknowledged.Items[0].Key != "AGB-1" {
		t.Errorf("unacknowledged section = %+v", o.Unacknowledged)
	}
	if !strings.Contains(rec.Body.String(), `"unacknowledged":{"total":1,"items":[`) {
		t.Errorf("overview JSON lacks the section: %s", rec.Body.String())
	}
	// The field filter accepts acknowledged.
	rec = e.do(t, "GET", "/v1/activity?field=acknowledged", "", nil)
	var acts listEnvelope[model.Activity]
	decodeJSON(t, rec, &acts)
	if rec.Code != 200 || acts.Total != 1 || acts.Items[0].Issue != "AGB-2" {
		t.Errorf("activity by acknowledged: %d %+v", rec.Code, acts)
	}
}
