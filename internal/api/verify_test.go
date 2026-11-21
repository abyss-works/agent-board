package api

import (
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func TestVerifyEndpoints(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "APP", "Example App")
	owner := map[string]string{"X-Actor": "owner"}

	// Default verification_state is none.
	plain := e.createIssue(t, `{"project":"APP","title":"plain work"}`)
	if plain.VerificationState != model.VerificationNone || plain.VerificationNote != nil || plain.VerifiedAt != nil || plain.VerifiedBy != nil {
		t.Fatalf("default issue verification = %+v", plain)
	}
	// Verifying or unverifying a "none" issue fails with 422.
	if rec := e.do(t, "POST", "/v1/issues/APP-1/verify", "", owner); rec.Code != 422 {
		t.Fatalf("verify none issue: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "DELETE", "/v1/issues/APP-1/verify", "", owner); rec.Code != 422 {
		t.Fatalf("unverify none issue: %d %s", rec.Code, rec.Body.String())
	}

	// Create pending issue with verification_note.
	rec := e.postIssue(t, `{"project":"APP","title":"ui polish","verification_state":"pending","verification_note":"Check mobile layout"}`, nil)
	var pending model.Issue
	decodeJSON(t, rec, &pending)
	if rec.Code != 201 || pending.VerificationState != model.VerificationPending || pending.VerificationNote == nil || *pending.VerificationNote != "Check mobile layout" || pending.VerifiedAt != nil || pending.VerifiedBy != nil {
		t.Fatalf("pending issue: %d %s", rec.Code, rec.Body.String())
	}

	// Verify the pending issue.
	rec = e.do(t, "POST", "/v1/issues/APP-2/verify", "", owner)
	var i model.Issue
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.VerificationState != model.VerificationVerified || i.VerifiedAt == nil || i.VerifiedBy == nil || *i.VerifiedBy != "owner" || i.Version != 2 || rec.Header().Get("ETag") != `"2"` {
		t.Fatalf("verify: %d %s etag=%q", rec.Code, rec.Body.String(), rec.Header().Get("ETag"))
	}
	// Idempotent verify: 200, version unchanged.
	rec = e.do(t, "POST", "/v1/issues/APP-2/verify", "", owner)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.Version != 2 || rec.Header().Get("ETag") != `"2"` {
		t.Errorf("second verify: %d version %d", rec.Code, i.Version)
	}
	rec = e.do(t, "GET", "/v1/issues/APP-2/activity", "", nil)
	var acts listEnvelope[model.Activity]
	decodeJSON(t, rec, &acts)
	if acts.Total != 2 || acts.Items[0].Actor != "owner" || string(acts.Items[0].Changes["verification_state"].To) != `"verified"` || len(acts.Items[0].Changes) != 1 {
		t.Errorf("activity = %+v", acts)
	}

	// Unverify: reverts to pending, preserves verification_note, clears verified_at/by.
	rec = e.do(t, "DELETE", "/v1/issues/APP-2/verify", "", owner)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.VerificationState != model.VerificationPending || i.VerifiedAt != nil || i.VerifiedBy != nil || i.VerificationNote == nil || *i.VerificationNote != "Check mobile layout" || i.Version != 3 {
		t.Errorf("unverify: %d %+v", rec.Code, i)
	}
	if rec = e.do(t, "DELETE", "/v1/issues/APP-2/verify", "", owner); rec.Code != 200 || rec.Header().Get("ETag") != `"3"` {
		t.Errorf("second unverify: %d etag=%q", rec.Code, rec.Header().Get("ETag"))
	}

	// If-Match handling.
	if rec = e.do(t, "POST", "/v1/issues/APP-2/verify", "", map[string]string{"X-Actor": "owner", "If-Match": `"1"`}); rec.Code != 412 {
		t.Errorf("stale If-Match: %d", rec.Code)
	}
	if rec = e.do(t, "POST", "/v1/issues/APP-2/verify", "", map[string]string{"X-Actor": "owner", "If-Match": `abc`}); rec.Code != 400 {
		t.Errorf("malformed If-Match: %d", rec.Code)
	}
	if rec = e.do(t, "POST", "/v1/issues/APP-2/verify", "", map[string]string{"X-Actor": "owner", "If-Match": `"3"`}); rec.Code != 200 {
		t.Errorf("matching If-Match: %d %s", rec.Code, rec.Body.String())
	}

	// Patch verification_state and verification_note.
	rec = e.do(t, "PATCH", "/v1/issues/APP-1", `{"verification_state":"pending","verification_note":"Check dark mode"}`, nil)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.VerificationState != model.VerificationPending || i.VerificationNote == nil || *i.VerificationNote != "Check dark mode" {
		t.Errorf("patch verification: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "PATCH", "/v1/issues/APP-1", `{"verification_note":null}`, nil)
	decodeJSON(t, rec, &i)
	if rec.Code != 200 || i.VerificationNote != nil {
		t.Errorf("clear verification_note: %d %s", rec.Code, rec.Body.String())
	}
	// Direct PATCH to verified is rejected by validation (must use /verify endpoint).
	if rec = e.do(t, "PATCH", "/v1/issues/APP-1", `{"verification_state":"verified"}`, nil); rec.Code != 422 {
		t.Errorf("patch to verified: %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, "PATCH", "/v1/issues/APP-1", `{"verification_state":null}`, nil); rec.Code != 422 {
		t.Errorf("patch verification_state null: %d %s", rec.Code, rec.Body.String())
	}

	// Error cases.
	if rec = e.do(t, "POST", "/v1/issues/APP-9/verify", "", owner); rec.Code != 404 {
		t.Errorf("missing issue: %d", rec.Code)
	}
	if rec = e.do(t, "POST", "/v1/issues/APP-2/verify?x=1", "", owner); rec.Code != 400 {
		t.Errorf("unknown query parameter: %d", rec.Code)
	}
	rec = e.do(t, "GET", "/v1/issues/APP-2/verify", "", owner)
	if rec.Code != 405 || rec.Header().Get("Allow") != "DELETE, POST" {
		t.Errorf("GET: %d Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
	e.do(t, "PATCH", "/v1/projects/APP", `{"status":"archived"}`, nil)
	rec = e.do(t, "DELETE", "/v1/issues/APP-2/verify", "", owner)
	if rec.Code != 409 || decodeErr(t, rec).Error.Details[0].Reason != "project_not_active" {
		t.Errorf("archived project: %d %s", rec.Code, rec.Body.String())
	}
}

func TestVerificationFilterAndOverview(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "APP", "Example App")
	owner := map[string]string{"X-Actor": "owner"}
	e.createIssue(t, `{"project":"APP","title":"a"}`)
	e.createIssue(t, `{"project":"APP","title":"b","verification_state":"pending","verification_note":"Check dialog"}`)
	e.createIssue(t, `{"project":"APP","title":"c","status":"done","verification_state":"pending"}`)
	e.createIssue(t, `{"project":"APP","title":"d","verification_state":"pending"}`)
	e.do(t, "POST", "/v1/issues/APP-4/verify", "", owner)
	e.do(t, "DELETE", "/v1/issues/APP-3?reason=x", "", nil)

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
	if l := list("verification=none"); keys(l) != "APP-1" {
		t.Errorf("verification=none: %s", keys(l))
	}
	if l := list("verification=pending"); keys(l) != "APP-2,APP-3" {
		t.Errorf("verification=pending: %s", keys(l))
	}
	if l := list("verification=verified"); keys(l) != "APP-4" || l.Items[0].VerifiedAt == nil {
		t.Errorf("verification=verified: %s", keys(l))
	}
	if l := list("verification=pending,verified"); keys(l) != "APP-2,APP-3,APP-4" {
		t.Errorf("verification=pending,verified: %s", keys(l))
	}
	if l := list(""); l.Total != 4 {
		t.Errorf("no filter: %d", l.Total)
	}
	for _, q := range []string{"verification=yes", "verification=pending&verification=verified", "verification=PENDING", "acknowledged=true"} {
		if rec := e.do(t, "GET", "/v1/issues?"+q, "", nil); rec.Code != 400 {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}

	// Overview lists pending non-cancelled/non-deleted issues under "unverified".
	rec := e.do(t, "GET", "/v1/projects/APP/overview", "", nil)
	var o store.Overview
	decodeJSON(t, rec, &o)
	if rec.Code != 200 || o.Unverified.Total != 1 || o.Unverified.Items[0].Key != "APP-2" {
		t.Errorf("unverified section = %+v", o.Unverified)
	}
	if !strings.Contains(rec.Body.String(), `"unverified":{"total":1,"items":[`) {
		t.Errorf("overview JSON lacks unverified section: %s", rec.Body.String())
	}

	// Activity field filter accepts verification_state and verification_note.
	rec = e.do(t, "GET", "/v1/activity?field=verification_state", "", nil)
	var acts listEnvelope[model.Activity]
	decodeJSON(t, rec, &acts)
	if rec.Code != 200 || acts.Total < 1 {
		t.Errorf("activity by verification_state: %d %+v", rec.Code, acts)
	}
	rec = e.do(t, "GET", "/v1/activity?field=verification_note", "", nil)
	if rec.Code != 200 {
		t.Errorf("activity by verification_note: %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, "GET", "/v1/activity?field=acknowledged", "", nil); rec.Code != 400 {
		t.Errorf("activity by removed acknowledged field: %d", rec.Code)
	}
}
