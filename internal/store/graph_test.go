package store

import (
	"errors"
	"slices"
	"sort"
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
	parent := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "상위"})
	putDoc(t, s, trClaude, "AGB", "decisions/legacy.md", "legacy", "# 이전 결정\n")
	putDoc(t, s, trClaude, "AGB", dOld, "history", docText("decision", "옛 결정"))
	putDoc(t, s, trClaude, "AGB", dNew, "history", docText("decision", "새 결정", "issue: AGB-1", "supersedes: "+dOld, "related: [decisions/legacy.md]"))
	putDoc(t, s, trClaude, "OTH", dRel, "history", docText("report", "다른 프로젝트", "related: [AGB/"+dNew+"]"))
	dep := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "선행"})
	gone := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "지운 이슈", Status: model.StatusDeleted})
	child := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "하위", Parent: str(parent.Key), DependsOn: []string{dep.Key},
		Refs: []string{"doc:" + dNew, "issue:" + gone.Key, "commit:abc1234"}})
	if parent.Key != "AGB-1" {
		t.Fatalf("parent key = %s", parent.Key)
	}

	g, err := s.Graph(ctx, model.IssueNodeID(child.Key), 1, "", false)
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

	g, err = s.Graph(ctx, model.DocumentNodeID("AGB", dNew), 1, "", false)
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
	if g2, err := s.Graph(ctx, model.DocumentNodeID("AGB", dOld), 2, "", false); err != nil || len(g2.Nodes) < 5 {
		t.Errorf("depth 2 from the old decision = %+v, %v", g2, err)
	}
	for _, id := range []string{model.IssueNodeID(gone.Key), model.IssueNodeID("AGB-99"), model.DocumentNodeID("AGB", "x.md")} {
		if _, err := s.Graph(ctx, id, 1, "", false); !errors.Is(err, ErrNotFound) {
			t.Errorf("Graph(%s) = %v, want not found", id, err)
		}
	}
}

// Similar edges come from the stored pairs, once per pair of documents, and
// not where a written link joins the two already.
func TestGraphSimilarEdges(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	const (
		a = "2026-10-20-1544-decision-a.md"
		b = "2026-10-21-1544-decision-b.md"
		c = "2026-10-22-1544-decision-c.md"
	)
	putDoc(t, s, trClaude, "AGB", a, "history", docText("decision", "가"))
	putDoc(t, s, trClaude, "AGB", b, "history", docText("decision", "나", "related: ["+a+"]"))
	putDoc(t, s, trClaude, "AGB", c, "history", docText("decision", "다"))
	id := func(name string) int64 {
		var v int64
		if err := s.db.QueryRow(`select id from document where name = ?`, name).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	pairs := []SimilarPair{
		{Document: id(a), Similar: id(b), Score: 0.9}, // a written related link exists
		{Document: id(a), Similar: id(c), Score: 0.8},
		{Document: id(c), Similar: id(a), Score: 0.8}, // the same pair the other way
	}
	if err := s.ReplaceSimilarDocuments(ctx, "m", pairs); err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph(ctx, model.DocumentNodeID("AGB", a), 1, "m", false)
	if err != nil {
		t.Fatal(err)
	}
	var similar []model.GraphEdge
	for _, e := range g.Edges {
		if e.Type == model.EdgeSimilar {
			similar = append(similar, e)
		}
	}
	if len(similar) != 1 || similar[0].Score == nil || *similar[0].Score != 0.8 {
		t.Errorf("similar edges = %+v", similar)
	}
	// Pairs found with another model, or with search by meaning off, are stale.
	for _, m := range []string{"", "other"} {
		g, err := s.Graph(ctx, model.DocumentNodeID("AGB", a), 1, m, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range g.Edges {
			if e.Type == model.EdgeSimilar {
				t.Errorf("model %q: stale similar edge %+v", m, e)
			}
		}
	}
	if err := s.ReplaceSimilarDocuments(ctx, "m", nil); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.Graph(ctx, model.DocumentNodeID("AGB", a), 1, "m", false); len(g.Edges) != 1 {
		t.Errorf("after clearing, edges = %+v", g.Edges)
	}
}

// The graph reads its document edges from the relation tables: an unresolved
// relation, and a relation to a deleted issue, make no edge.
func TestGraphEdgesFromRelationTables(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	live := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "살아 있는 이슈"})
	gone := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "지울 이슈"})
	putDoc(t, s, trClaude, "OTH", relOld, "history", docText("decision", "다른 프로젝트"))
	putDocArgs(t, s, trClaude, "AGB", "notes/a.md", "# a", imported(model.DocumentArgs{
		Related: []string{"OTH/" + relOld, "later.md"}, Issues: []string{live.Key, gone.Key, "AGB-99"}}))
	deleted := model.StatusDeleted
	if _, _, err := s.UpdateIssue(ctx, trOwner, gone.Key, IssuePatch{Status: &deleted, StatusReason: str("지움")}, nil); err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph(ctx, model.DocumentNodeID("AGB", "notes/a.md"), 1, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := edgeSet(g)
	want := []model.GraphEdge{
		{From: "doc:AGB/notes/a.md", To: "doc:OTH/" + relOld, Type: model.EdgeRelated},
		{From: "doc:AGB/notes/a.md", To: "issue:" + live.Key, Type: model.EdgeAbout},
	}
	if len(got) != len(want) {
		t.Errorf("edges = %+v, want %+v", g.Edges, want)
	}
	for _, e := range want {
		if !got[e] {
			t.Errorf("lacks %+v; edges %+v", e, g.Edges)
		}
	}
	// Node fields: status for an issue, state for a document, and no folded.
	for _, n := range g.Nodes {
		switch n.Type {
		case "issue":
			if n.Status == nil || n.State != nil || n.Folded != nil {
				t.Errorf("issue node = %+v", n)
			}
		case "document":
			if n.Status != nil || n.State == nil || n.Folded != nil {
				t.Errorf("document node = %+v", n)
			}
		}
	}
}

// With fold a superseded or archived document is no neighbor and no way
// through; the nodes next to it count it. Without fold nothing is left out.
func TestGraphFoldsSupersededAndArchivedDocuments(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	issue := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "근거"})
	const (
		cur   = "2026-10-03-1000-decision-cur.md"
		old   = "2026-10-01-1000-decision-old.md"
		arch  = "2026-10-02-1000-decision-arch.md"
		far   = "2026-10-04-1000-report-far.md"
		other = "2026-10-05-1000-report-other.md"
	)
	putDoc(t, s, trClaude, "AGB", far, "history", docText("report", "멀리 있는 문서"))
	putDoc(t, s, trClaude, "AGB", old, "history", docText("decision", "옛", "issue: "+issue.Key, "related: "+far))
	putDoc(t, s, trClaude, "AGB", arch, "archive", docText("decision", "보관", "issue: "+issue.Key))
	putDoc(t, s, trClaude, "AGB", cur, "history", docText("decision", "새", "issue: "+issue.Key, "supersedes: "+old, "related: "+arch))
	putDoc(t, s, trClaude, "AGB", other, "history", docText("report", "다른 문서", "related: "+far))
	ids := func(g *model.Graph) []string {
		var out []string
		for _, n := range g.Nodes {
			out = append(out, n.ID)
		}
		sort.Strings(out)
		return out
	}
	doc := func(name string) string { return "doc:AGB/" + name }

	// From the issue: the current document only; the others are counted on both.
	g, err := s.Graph(ctx, model.IssueNodeID(issue.Key), 2, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{doc(cur), "issue:" + issue.Key}; !slices.Equal(ids(g), want) {
		t.Errorf("folded nodes = %v, want %v", ids(g), want)
	}
	if g.Nodes[0].ID != "issue:"+issue.Key || g.Nodes[0].Folded == nil || *g.Nodes[0].Folded != (model.GraphFolded{Superseded: 1, Archived: 1}) {
		t.Errorf("issue node = %+v", g.Nodes[0])
	}
	if f := g.Nodes[1].Folded; f == nil || *f != (model.GraphFolded{Superseded: 1, Archived: 1}) {
		t.Errorf("current document folded = %+v", f)
	}
	if len(g.Edges) != 1 || g.Edges[0].Type != model.EdgeAbout {
		t.Errorf("folded edges = %+v", g.Edges)
	}
	// "far" is only reachable through the superseded document, so it is not there at depth 2.
	if slices.Contains(ids(g), doc(far)) {
		t.Errorf("a folded document was a way through: %v", ids(g))
	}

	// Not folded: everything within two steps, and no folded counts.
	g, err = s.Graph(ctx, model.IssueNodeID(issue.Key), 2, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{doc(old), doc(arch), doc(cur), doc(far), "issue:" + issue.Key}; !slices.Equal(ids(g), want) {
		t.Errorf("all nodes = %v, want %v", ids(g), want)
	}
	for _, n := range g.Nodes {
		if n.Folded != nil {
			t.Errorf("unfolded node has folded: %+v", n)
		}
	}

	// A superseded document asked for is shown, with the current ones next to it.
	g, err = s.Graph(ctx, doc(old), 1, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{doc(old), doc(cur), doc(far), "issue:" + issue.Key}; !slices.Equal(ids(g), want) || g.Nodes[0].ID != doc(old) || *g.Nodes[0].State != "superseded" {
		t.Errorf("from the superseded document = %v", ids(g))
	}
	// An archived document asked for: its neighbor "cur" is current, so nothing folds.
	g, err = s.Graph(ctx, doc(arch), 1, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{doc(arch), doc(cur), "issue:" + issue.Key}; !slices.Equal(ids(g), want) {
		t.Errorf("from the archived document = %v", ids(g))
	}
	// A node with no folded neighbor has none counted; "far" has the superseded document as one.
	g, _ = s.Graph(ctx, doc(other), 1, "", true)
	if g.Nodes[0].Folded != nil || g.Nodes[1].Folded == nil || *g.Nodes[1].Folded != (model.GraphFolded{Superseded: 1}) {
		t.Errorf("folded around the other document = %+v, %+v", g.Nodes[0].Folded, g.Nodes[1].Folded)
	}
}
