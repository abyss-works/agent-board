package embed

import (
	"context"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func TestSimilarPairs(t *testing.T) {
	row := func(source string, id int64, chunk int, v ...float32) store.EmbeddingRow {
		return store.EmbeddingRow{Source: source, SourceID: id, Chunk: chunk, Vector: normalize(v)}
	}
	rows := []store.EmbeddingRow{
		row("document", 1, 0, 1, 0, 0),
		row("document", 1, 1, 1, 0.2, 0), // the mean of two chunks
		row("document", 2, 0, 1, 0.1, 0),
		row("document", 3, 0, 0, 1, 0),
		row("document", 4, 0, 0.9, 0.1, 0.1),
		row("issue", 9, 0, 1, 0, 0), // not a document
	}
	pairs := similarPairs(rows, 2, 0.5)
	near := map[int64][]int64{}
	for _, p := range pairs {
		if p.Score < 0.5 || p.Document == p.Similar {
			t.Errorf("pair %+v", p)
		}
		near[p.Document] = append(near[p.Document], p.Similar)
	}
	if got := near[1]; len(got) != 2 || got[0] != 2 {
		t.Errorf("near 1 = %v, want 2 first and two at most", got)
	}
	if len(near[3]) != 0 {
		t.Errorf("document 3 is close to none above the floor, got %v", near[3])
	}
	for _, p := range pairs {
		if p.Document == 9 || p.Similar == 9 {
			t.Errorf("an issue took part: %+v", p)
		}
	}
}

// The first round computes the similar documents from stored embeddings even
// when nothing needs embedding, and the graph shows them.
func TestRunOnceComputesSimilarDocuments(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	r, st := newRunner(t, f)
	for _, d := range []struct{ name, body string }{
		{"2026-11-01-0900-report-a.md", "aaaa"},
		{"2026-11-01-0901-report-b.md", "aaab"},
		{"2026-11-01-0902-report-c.md", "cccc"},
	} {
		if _, _, err := st.PutDocument(ctx, claude, "AGB", d.name, store.DocumentInput{Location: "history", Content: "---\nkind: report\n---\n\n" + d.body}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	r2 := &Runner{Store: st, Embedder: f, Model: "m", Index: &Index{}, Logger: r.Logger}
	if _, err := r2.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	g, err := st.Graph(ctx, model.DocumentNodeID("AGB", "2026-11-01-0900-report-a.md"), 1, "m")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range g.Edges {
		if e.Type == model.EdgeSimilar && e.Score != nil && (e.To == model.DocumentNodeID("AGB", "2026-11-01-0901-report-b.md") || e.From == model.DocumentNodeID("AGB", "2026-11-01-0901-report-b.md")) {
			found = true
		}
	}
	if !found {
		t.Errorf("no similar edge from a to b: %+v", g.Edges)
	}
}
