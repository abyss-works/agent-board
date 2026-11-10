package api

import (
	"net/url"
	"strings"
	"testing"

	"github.com/abyss-works/agent-board/internal/model"
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

func TestGraphEndpointFoldsByState(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	issue := e.createIssue(t, `{"project":"AGB","title":"근거 이슈"}`)
	e.putFields(t, "AGB", stOld, map[string]any{"content": apiDocText("decision", "옛"), "issues": []string{issue.Key}}, nil)
	e.putFields(t, "AGB", stNew, map[string]any{"content": apiDocText("decision", "새"), "issues": []string{issue.Key}, "supersedes": []string{stOld}}, nil)
	e.putFields(t, "AGB", stRep, map[string]any{"content": apiDocText("report", "보관"), "issues": []string{issue.Key}, "archived": true}, nil)

	node := "/v1/graph?node=" + url.QueryEscape("issue:"+issue.Key)
	rec := e.do(t, "GET", node, "", nil)
	var g model.Graph
	decodeJSON(t, rec, &g)
	if rec.Code != 200 || len(g.Nodes) != 2 || g.Nodes[1].ID != "doc:AGB/"+stNew || len(g.Edges) != 1 {
		t.Fatalf("folded: %d %s", rec.Code, rec.Body.String())
	}
	if f := g.Nodes[0].Folded; f == nil || f.Superseded != 1 || f.Archived != 1 {
		t.Errorf("issue folded = %+v", f)
	}
	for _, want := range []string{`"status":"backlog","state":null,"refs":[],"folded":{"superseded":1,"archived":1}`, `"status":null,"state":"current","refs":[],"folded":{"superseded":1,"archived":0}`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("JSON lacks %s: %s", want, rec.Body.String())
		}
	}
	for _, target := range []string{node + "&state=current", node} {
		if rec := e.do(t, "GET", target, "", nil); rec.Code != 200 {
			t.Errorf("%s: %d", target, rec.Code)
		}
	}
	rec = e.do(t, "GET", node+"&state=all", "", nil)
	decodeJSON(t, rec, &g)
	if rec.Code != 200 || len(g.Nodes) != 4 || len(g.Edges) != 4 {
		t.Fatalf("all: %d %s", rec.Code, rec.Body.String())
	}
	for _, n := range g.Nodes {
		if n.Folded != nil {
			t.Errorf("state=all must not fold: %+v", n)
		}
	}
	// The node asked for is shown in any state.
	rec = e.do(t, "GET", "/v1/graph?node="+url.QueryEscape("doc:AGB/"+stOld), "", nil)
	decodeJSON(t, rec, &g)
	if rec.Code != 200 || g.Nodes[0].ID != "doc:AGB/"+stOld || *g.Nodes[0].State != "superseded" {
		t.Errorf("superseded node: %d %s", rec.Code, rec.Body.String())
	}
	for _, target := range []string{node + "&state=superseded", node + "&state=", node + "&state=all&state=current"} {
		if target == node+"&state=" {
			continue
		}
		if rec := e.do(t, "GET", target, "", nil); rec.Code != 400 {
			t.Errorf("%s: %d %s", target, rec.Code, rec.Body.String())
		}
	}
}
