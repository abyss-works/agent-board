package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func TestTemplatesEndpoint(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, "GET", "/v1/templates", "", map[string]string{"X-Actor": "owner"})
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Items []model.Template `json:"items"`
	}
	decodeJSON(t, rec, &got)
	if len(got.Items) != 5 {
		t.Fatalf("templates = %d, want 5", len(got.Items))
	}
	for i, typ := range model.IssueTypes {
		want := model.Templates[i]
		if got.Items[i].Type != typ || got.Items[i].TitleRule != want.TitleRule || got.Items[i].Body != want.Body || strings.Join(got.Items[i].Headings, "|") != strings.Join(want.Headings, "|") {
			t.Errorf("template %d = %+v, want %+v", i, got.Items[i], want)
		}
	}
	var raw struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	decodeJSON(t, rec, &raw)
	for _, k := range []string{"type", "title_rule", "headings", "body"} {
		if _, has := raw.Items[0][k]; !has {
			t.Errorf("template JSON lacks %s: %s", k, rec.Body.String())
		}
	}
	if len(raw.Items[0]) != 4 {
		t.Errorf("template JSON has %d keys, want 4", len(raw.Items[0]))
	}
	// The same list for an agent.
	if rec := e.do(t, "GET", "/v1/templates", "", nil); rec.Code != 200 {
		t.Errorf("agent: %d", rec.Code)
	}
	if rec := e.do(t, "POST", "/v1/templates", `{}`, nil); rec.Code != 405 || rec.Header().Get("Allow") != "GET" {
		t.Errorf("POST: %d Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
	if rec := e.do(t, "GET", "/v1/templates?x=1", "", nil); rec.Code != 400 {
		t.Errorf("unknown parameter: %d", rec.Code)
	}
	if rec := e.do(t, "GET", "/v1/templates", "", map[string]string{"Authorization": "-"}); rec.Code != 401 {
		t.Errorf("without a token: %d", rec.Code)
	}
}
