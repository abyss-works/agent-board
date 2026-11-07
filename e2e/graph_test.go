//go:build e2e

package e2e

import (
	"net/url"
	"testing"
)

type graphNode struct {
	ID   string   `json:"id"`
	Refs []string `json:"refs"`
}

type graphEdge struct {
	From, To, Type string
}

type graphResult struct {
	Nodes []graphNode `json:"nodes"`
	Edges []graphEdge `json:"edges"`
}

// TestGraph links an issue, its parent and a record document that names the
// issue and supersedes another record.
func TestGraph(t *testing.T) {
	p := createProject(t, "GA")
	parent := newIssue(t, p.Key, "상위", nil)
	child := newIssue(t, p.Key, "하위", map[string]any{"parent": parent.Key, "refs": []string{"commit:abc1234"}})
	mustPutDocument(t, owner, p.Key, "2026-11-06-1500-decision-old.md", "history", documentText("decision", "2026-11-06 15:00", "옛 결정", "", "본문"))
	mustPutDocument(t, owner, p.Key, "2026-11-06-1501-decision-new.md", "history",
		"---\nkind: decision\ntitle: 새 결정\nissue: "+child.Key+"\nsupersedes: 2026-11-06-1500-decision-old.md\n---\n본문\n")

	g := decodeAs[graphResult](t, owner.do(t, "GET", "/v1/graph?node="+url.QueryEscape("issue:"+child.Key), nil), 200)
	want := map[graphEdge]bool{
		{From: "issue:" + child.Key, To: "issue:" + parent.Key, Type: "parent"}:                              false,
		{From: "doc:" + p.Key + "/2026-11-06-1501-decision-new.md", To: "issue:" + child.Key, Type: "about"}: false,
	}
	for _, e := range g.Edges {
		if _, ok := want[e]; ok {
			want[e] = true
		}
	}
	for e, seen := range want {
		if !seen {
			t.Errorf("edge %+v missing: %+v", e, g.Edges)
		}
	}
	if len(g.Nodes) == 0 || g.Nodes[0].ID != "issue:"+child.Key || len(g.Nodes[0].Refs) != 1 {
		t.Errorf("nodes = %+v", g.Nodes)
	}
	deep := decodeAs[graphResult](t, owner.do(t, "GET", "/v1/graph?depth=2&state=all&node="+url.QueryEscape("issue:"+child.Key), nil), 200)
	found := false
	for _, e := range deep.Edges {
		found = found || e.Type == "supersedes"
	}
	if !found {
		t.Errorf("depth 2 lacks the supersedes edge: %+v", deep.Edges)
	}
	wantError(t, owner.do(t, "GET", "/v1/graph?node=issue:"+p.Key+"-999", nil), 404, "not_found")
}
