package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

const (
	apiBatchA = "2026-10-10-1000-decision-alpha.md"
	apiBatchB = "2026-10-10-1001-decision-beta.md"
	apiBatchC = "2026-10-10-1002-decision-gamma.md"
)

func TestPutDocumentsBatchEndpoint(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "PROJ", "sample-project")

	body := fmt.Sprintf(`{"documents":[`+
		`{"name":%q,"content":%q},`+
		`{"name":%q,"content":%q}`+
		`]}`,
		apiBatchA, apiDocText("decision", "alpha", "related: "+apiBatchB),
		apiBatchB, apiDocText("decision", "beta", "related: "+apiBatchA),
	)
	rec := e.do(t, "POST", "/v1/projects/PROJ/documents:batch", body, nil)
	if rec.Code != 200 {
		t.Fatalf("POST batch = %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Documents []model.DocumentResult `json:"documents"`
	}
	decodeJSON(t, rec, &got)
	if len(got.Documents) != 2 ||
		!reflect.DeepEqual(got.Documents[0].Related, []string{apiBatchB}) ||
		!reflect.DeepEqual(got.Documents[1].Related, []string{apiBatchA}) ||
		got.Documents[0].State != model.StateCurrent || got.Documents[1].State != model.StateCurrent {
		t.Errorf("batch response = %+v", got.Documents)
	}

	// Method check on :batch.
	if m := e.do(t, "GET", "/v1/projects/PROJ/documents:batch", "", nil); m.Code != 405 {
		t.Errorf("GET :batch = %d %s, want 405", m.Code, m.Body.String())
	}

	// Missing project -> 404.
	if m := e.do(t, "POST", "/v1/projects/NOPE/documents:batch", body, nil); m.Code != 404 {
		t.Errorf("missing project = %d %s, want 404", m.Code, m.Body.String())
	}

	// Inactive project -> 409 conflict with reason project_not_active.
	if p := e.do(t, "PATCH", "/v1/projects/PROJ", `{"status":"archived"}`, nil); p.Code != 200 {
		t.Fatalf("archive project = %d %s", p.Code, p.Body.String())
	}
	m := e.do(t, "POST", "/v1/projects/PROJ/documents:batch", body, nil)
	mb := decodeErr(t, m)
	if m.Code != 409 || mb.Error.Code != "conflict" || len(mb.Error.Details) != 1 || mb.Error.Details[0].Reason != "project_not_active" {
		t.Errorf("archived project = %d %s, want 409 project_not_active", m.Code, m.Body.String())
	}
}

func TestPutDocumentsBatchValidationAndConflicts(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "PROJ", "sample-project")

	// Empty array -> 422 on documents.
	rec := e.do(t, "POST", "/v1/projects/PROJ/documents:batch", `{"documents":[]}`, nil)
	if rec.Code != 422 || len(decodeErr(t, rec).Error.Details) != 1 || decodeErr(t, rec).Error.Details[0].Field != "documents" {
		t.Errorf("empty documents = %d %s", rec.Code, rec.Body.String())
	}
	// Missing documents field -> 422 on documents.
	rec = e.do(t, "POST", "/v1/projects/PROJ/documents:batch", `{}`, nil)
	if rec.Code != 422 || len(decodeErr(t, rec).Error.Details) != 1 || decodeErr(t, rec).Error.Details[0].Field != "documents" {
		t.Errorf("missing documents = %d %s", rec.Code, rec.Body.String())
	}

	// >100 items -> 422 on documents.
	items := make([]map[string]any, 101)
	for i := range items {
		items[i] = map[string]any{
			"name":    fmt.Sprintf("2026-10-10-%04d-decision-d-%d.md", 1000+i, i),
			"content": apiDocText("decision", "body"),
		}
	}
	overBytes, _ := json.Marshal(map[string]any{"documents": items})
	rec = e.do(t, "POST", "/v1/projects/PROJ/documents:batch", string(overBytes), nil)
	if rec.Code != 422 || decodeErr(t, rec).Error.Details[0].Field != "documents" {
		t.Errorf("101 documents = %d %s", rec.Code, rec.Body.String())
	}

	// Duplicate name -> 422 with documents[1].name.
	dupBody, _ := json.Marshal(map[string]any{"documents": []map[string]any{
		{"name": apiBatchA, "content": apiDocText("decision", "a")},
		{"name": apiBatchA, "content": apiDocText("decision", "a")},
	}})
	rec = e.do(t, "POST", "/v1/projects/PROJ/documents:batch", string(dupBody), nil)
	if rec.Code != 422 || decodeErr(t, rec).Error.Details[0].Field != "documents[1].name" {
		t.Errorf("duplicate name = %d %s", rec.Code, rec.Body.String())
	}

	// Unknown field inside item -> 400 with documents[0].extra.
	rec = e.do(t, "POST", "/v1/projects/PROJ/documents:batch", `{"documents":[{"name":"`+apiBatchA+`","content":"x","extra":1}]}`, nil)
	if rec.Code != 400 || decodeErr(t, rec).Error.Details[0].Field != "documents[0].extra" {
		t.Errorf("unknown item field = %d %s", rec.Code, rec.Body.String())
	}

	// Immutable conflict reports SHA256 and document name, and rolls back peer.
	contentA := apiDocText("decision", "original")
	e.mustPutDocument(t, "PROJ", apiBatchA, "history", contentA)

	immBody, _ := json.Marshal(map[string]any{"documents": []map[string]any{
		{"name": apiBatchC, "content": apiDocText("decision", "peer")},
		{"name": apiBatchA, "content": apiDocText("decision", "changed")},
	}})
	rec = e.do(t, "POST", "/v1/projects/PROJ/documents:batch", string(immBody), nil)
	var immErr struct {
		Error struct {
			Code    string   `json:"code"`
			Details []Detail `json:"details"`
		} `json:"error"`
	}
	decodeJSON(t, rec, &immErr)
	if rec.Code != 409 || len(immErr.Error.Details) != 1 || immErr.Error.Details[0].Reason != "immutable" ||
		immErr.Error.Details[0].SHA256 != apiDocSHA(contentA) || !strings.Contains(immErr.Error.Details[0].Field, "documents[1]") {
		t.Errorf("immutable batch = %d %s", rec.Code, rec.Body.String())
	}
	if g := e.do(t, "GET", documentPath("PROJ", apiBatchC), "", nil); g.Code != 404 {
		t.Errorf("peer stored despite conflict: %d %s", g.Code, g.Body.String())
	}
}
