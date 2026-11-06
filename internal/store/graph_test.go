package store

import (
	"errors"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func edgeSet(g *model.Graph) map[model.GraphEdge]bool {
	out := map[model.GraphEdge]bool{}
	for _, e := range g.Edges {
		out[e] = true
	}
	return out
}

func TestGraph(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	const (
		dOld = "2026-10-20-1544-decision-old.md"
		dNew = "2026-10-21-1544-decision-new.md"
		dRel = "2026-10-22-1544-report-rel.md"
	)
	putDoc(t, s, trClaude, "AGB", dOld, "history", docText("decision", "옛 결정"))
	putDoc(t, s, trClaude, "AGB", dNew, "history", docText("decision", "새 결정", "issue: AGB-1", "supersedes: "+dOld+"의 결론", "related: [decisions/legacy.md]"))
	putDoc(t, s, trClaude, "OTH", dRel, "history", docText("report", "다른 프로젝트", "related: ["+dNew+"]"))
	putDoc(t, s, trClaude, "AGB", "decisions/legacy.md", "legacy", "# 이전 결정\n")
	parent := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "상위"})
	dep := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "선행"})
	gone := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "지운 이슈", Status: model.StatusDeleted})
	child := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "하위", Parent: str(parent.Key), DependsOn: []string{dep.Key},
		Refs: []string{"doc:" + dNew, "issue:" + gone.Key, "commit:abc1234", "doc:missing.md"}})
	if parent.Key != "AGB-1" {
		t.Fatalf("parent key = %s", parent.Key)
	}

	g, err := s.Graph(ctx, model.IssueNodeID(child.Key), 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.GraphEdge{
		{From: "issue:" + child.Key, To: "issue:AGB-1", Type: model.EdgeParent},
		{From: "issue:" + child.Key, To: "issue:" + dep.Key, Type: model.EdgeDependsOn},
		{From: "issue:" + child.Key, To: "doc:AGB/" + dNew, Type: model.EdgeRef},
	}
	got := edgeSet(g)
	for _, e := range want {
		if !got[e] {
			t.Errorf("depth 1 lacks %+v; edges %+v", e, g.Edges)
		}
	}
	if g.Nodes[0].ID != "issue:"+child.Key || len(g.Nodes) != 4 || len(g.Nodes[0].Refs) != 1 || g.Nodes[0].Refs[0] != "commit:abc1234" {
		t.Errorf("depth 1 nodes = %+v", g.Nodes)
	}

	g, err = s.Graph(ctx, model.DocumentNodeID("AGB", dNew), 1)
	if err != nil {
		t.Fatal(err)
	}
	got = edgeSet(g)
	for _, e := range []model.GraphEdge{
		{From: "doc:AGB/" + dNew, To: "issue:AGB-1", Type: model.EdgeAbout},
		{From: "doc:AGB/" + dNew, To: "doc:AGB/" + dOld, Type: model.EdgeSupersedes},
		{From: "doc:AGB/" + dNew, To: "doc:AGB/decisions/legacy.md", Type: model.EdgeRelated},
		{From: "doc:OTH/" + dRel, To: "doc:AGB/" + dNew, Type: model.EdgeRelated},
		{From: "issue:" + child.Key, To: "doc:AGB/" + dNew, Type: model.EdgeRef},
	} {
		if !got[e] {
			t.Errorf("document graph lacks %+v; edges %+v", e, g.Edges)
		}
	}
	if g2, err := s.Graph(ctx, model.DocumentNodeID("AGB", dOld), 2); err != nil || len(g2.Nodes) < 5 {
		t.Errorf("depth 2 from the old decision = %+v, %v", g2, err)
	}
	for _, id := range []string{model.IssueNodeID(gone.Key), model.IssueNodeID("AGB-99"), model.DocumentNodeID("AGB", "x.md")} {
		if _, err := s.Graph(ctx, id, 1); !errors.Is(err, ErrNotFound) {
			t.Errorf("Graph(%s) = %v, want not found", id, err)
		}
	}
}
