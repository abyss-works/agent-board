package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func (e *env) createProject(t *testing.T, key, name string) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(t, "POST", "/v1/projects", `{"key":"`+key+`","name":"`+name+`"}`, nil)
}

type listEnvelope[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func TestCreateProject(t *testing.T) {
	e := newEnv(t)
	rec := e.createProject(t, "AGB", "agent-board")
	if rec.Code != 201 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/v1/projects/AGB" {
		t.Errorf("Location = %q", loc)
	}
	var p model.Project
	decodeJSON(t, rec, &p)
	if p.Key != "AGB" || p.Name != "agent-board" || p.Status != model.ProjectActive || p.CreatedBy != "claude" {
		t.Errorf("project = %+v", p)
	}

	rec = e.createProject(t, "AGB", "again")
	if rec.Code != 409 || decodeErr(t, rec).Error.Details[0].Reason != "duplicate_key" {
		t.Errorf("duplicate: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.createProject(t, "agb", "")
	b := decodeErr(t, rec)
	if rec.Code != 422 || b.Error.Code != "validation_failed" || len(b.Error.Details) != 2 || b.Error.Details[0].Field != "key" || b.Error.Details[1].Field != "name" {
		t.Errorf("validation: %d %+v", rec.Code, b.Error)
	}
	rec = e.do(t, "POST", "/v1/projects", `{"key":"X1","name":"x","archived":true}`, nil)
	if rec.Code != 400 || decodeErr(t, rec).Error.Details[0].Field != "archived" {
		t.Errorf("unknown field: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "POST", "/v1/projects", `{"key":"X1","name":`, nil)
	if rec.Code != 400 || decodeErr(t, rec).Error.Code != "bad_request" {
		t.Errorf("bad json: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "POST", "/v1/projects", `{"key":"X1","name":5}`, nil)
	if rec.Code != 400 || decodeErr(t, rec).Error.Details[0].Field != "name" {
		t.Errorf("type mismatch: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "POST", "/v1/projects", `{"key":"X1"}`, nil)
	if rec.Code != 422 {
		t.Errorf("missing name: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "POST", "/v1/projects", `{"key":"X1","name":"`+strings.Repeat("a", 1<<20)+`"}`, nil)
	if rec.Code != 413 || decodeErr(t, rec).Error.Code != "payload_too_large" {
		t.Errorf("too large: %d %s", rec.Code, rec.Body.String()[:min(200, rec.Body.Len())])
	}
}

func TestGetListPatchDeleteProject(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "BB", "b")
	e.createProject(t, "AA", "a")

	rec := e.do(t, "GET", "/v1/projects/AA", "", nil)
	var p model.Project
	decodeJSON(t, rec, &p)
	if rec.Code != 200 || p.Key != "AA" {
		t.Errorf("get: %d %+v", rec.Code, p)
	}
	if rec = e.do(t, "GET", "/v1/projects/ZZ", "", nil); rec.Code != 404 || decodeErr(t, rec).Error.Code != "not_found" {
		t.Errorf("missing: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, "PATCH", "/v1/projects/BB", `{"name":"bee","status":"archived"}`, nil)
	decodeJSON(t, rec, &p)
	if rec.Code != 200 || p.Name != "bee" || p.Status != model.ProjectArchived || p.UpdatedBy != "claude" {
		t.Errorf("patch: %d %+v", rec.Code, p)
	}
	if rec = e.do(t, "PATCH", "/v1/projects/BB", `{"key":"CC"}`, nil); rec.Code != 400 || decodeErr(t, rec).Error.Details[0].Field != "key" {
		t.Errorf("patch read-only: %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, "PATCH", "/v1/projects/BB", `{"name":null}`, nil); rec.Code != 422 {
		t.Errorf("patch null name: %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, "PATCH", "/v1/projects/BB", `{"status":"gone"}`, nil); rec.Code != 422 || decodeErr(t, rec).Error.Details[0].Field != "status" {
		t.Errorf("patch bad status: %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, "PATCH", "/v1/projects/BB", `{}`, nil); rec.Code != 200 {
		t.Errorf("empty patch: %d %s", rec.Code, rec.Body.String())
	}

	var list listEnvelope[model.Project]
	rec = e.do(t, "GET", "/v1/projects", "", nil)
	decodeJSON(t, rec, &list)
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].Key != "AA" || list.Limit != 50 || list.Offset != 0 {
		t.Errorf("default list = %+v", list)
	}
	rec = e.do(t, "GET", "/v1/projects?status=all&sort=-created_at", "", nil)
	decodeJSON(t, rec, &list)
	if list.Total != 2 || list.Items[0].Key != "AA" {
		t.Errorf("all = %+v", list)
	}
	rec = e.do(t, "GET", "/v1/projects?status=archived,deleted", "", nil)
	decodeJSON(t, rec, &list)
	if list.Total != 1 || list.Items[0].Key != "BB" {
		t.Errorf("archived,deleted = %+v", list)
	}
	for _, q := range []string{"status=bogus", "sort=name", "limit=0", "limit=201", "limit=x", "offset=-1", "status=all&status=active", "foo=1"} {
		if rec = e.do(t, "GET", "/v1/projects?"+q, "", nil); rec.Code != 400 {
			t.Errorf("%s: status = %d", q, rec.Code)
		}
	}

	rec = e.do(t, "DELETE", "/v1/projects/AA", "", nil)
	decodeJSON(t, rec, &p)
	if rec.Code != 200 || p.Status != model.ProjectDeleted {
		t.Errorf("delete: %d %+v", rec.Code, p)
	}
	// Deleted projects are not hidden from an explicit status filter nor from GET.
	rec = e.do(t, "GET", "/v1/projects?status=deleted", "", nil)
	decodeJSON(t, rec, &list)
	if list.Total != 1 {
		t.Errorf("deleted list = %+v", list)
	}
	if rec = e.do(t, "GET", "/v1/projects/AA", "", nil); rec.Code != 200 {
		t.Errorf("deleted get = %d", rec.Code)
	}
	if rec = e.do(t, "DELETE", "/v1/projects/ZZ", "", nil); rec.Code != 404 {
		t.Errorf("delete missing = %d", rec.Code)
	}
}
