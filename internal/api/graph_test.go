package api

import (
	"net/url"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func TestGraphEndpoint(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	issue := e.createIssue(t, `{"project":"AGB","title":"근거 이슈"}`)
	e.mustPutDocument(t, "AGB", apiDocPriority, "history", apiDocText("decision", "본문", "issue: "+issue.Key))

	rec := e.do(t, "GET", "/v1/graph?node="+url.QueryEscape("issue:"+issue.Key), "", nil)
	var g model.Graph
	decodeJSON(t, rec, &g)
	if rec.Code != 200 || len(g.Nodes) != 2 || g.Nodes[0].ID != "issue:"+issue.Key || len(g.Edges) != 1 ||
		g.Edges[0] != (model.GraphEdge{From: "doc:AGB/" + apiDocPriority, To: "issue:" + issue.Key, Type: model.EdgeAbout}) {
		t.Fatalf("graph: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "GET", "/v1/graph?node="+url.QueryEscape("doc:AGB/"+apiDocPriority)+"&depth=2", "", nil); rec.Code != 200 {
		t.Errorf("document node: %d %s", rec.Code, rec.Body.String())
	}
	for label, c := range map[string]struct {
		target string
		code   int
	}{
		"no node":       {"/v1/graph", 400},
		"bad node":      {"/v1/graph?node=AGB-1", 400},
		"depth 3":       {"/v1/graph?node=issue:AGB-1&depth=3", 400},
		"unknown param": {"/v1/graph?node=issue:AGB-1&x=1", 400},
		"missing issue": {"/v1/graph?node=issue:AGB-99", 404},
		"missing doc":   {"/v1/graph?node=" + url.QueryEscape("doc:AGB/x.md"), 404},
	} {
		if rec := e.do(t, "GET", c.target, "", nil); rec.Code != c.code {
			t.Errorf("%s: %d %s", label, rec.Code, rec.Body.String())
		}
	}
}
