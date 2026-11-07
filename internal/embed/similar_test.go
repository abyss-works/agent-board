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
	pairs := similarPairs(rows, 2, 0.5, map[int64]bool{1: true, 2: true, 3: true, 4: true, 9: true})
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
		if _, _, err := st.PutDocument(ctx, claude, "AGB", d.name, store.DocumentInput{Content: "---\nkind: report\ntitle: t\n---\n\n" + d.body}); err != nil {
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
	g, err := st.Graph(ctx, model.DocumentNodeID("AGB", "2026-11-01-0900-report-a.md"), 1, "m", false)
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

// Only the documents that are current are candidates, on either side of a pair.
func TestSimilarPairsLeaveOutDocumentsThatAreNotCurrent(t *testing.T) {
	row := func(id int64, v ...float32) store.EmbeddingRow {
		return store.EmbeddingRow{Source: "document", SourceID: id, Vector: normalize(v)}
	}
	rows := []store.EmbeddingRow{row(1, 1, 0), row(2, 1, 0.1), row(3, 1, 0.2)}
	pairs := similarPairs(rows, 3, 0.5, map[int64]bool{1: true, 3: true})
	if len(pairs) != 2 {
		t.Fatalf("pairs = %+v, want 1-3 and 3-1", pairs)
	}
	for _, p := range pairs {
		if p.Document == 2 || p.Similar == 2 {
			t.Errorf("a document that is not current took part: %+v", p)
		}
	}
	if pairs := similarPairs(rows, 3, 0.5, nil); len(pairs) != 0 {
		t.Errorf("no current document, pairs = %+v", pairs)
	}
}

func similarCount(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`select count(*) from similar_document`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The similar documents are computed again when the state of a document changes,
// not otherwise, and the documents that are not current are not among them.
func TestRunOnceRecomputesSimilarDocumentsWhenAStateChanges(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	r, st := newRunner(t, f)
	names := []string{"2026-11-01-0900-report-a.md", "2026-11-01-0901-report-b.md", "2026-11-01-0902-report-c.md"}
	for i, name := range names {
		body := []string{"aaaa", "aaab", "aaac"}[i]
		if _, _, err := st.PutDocument(ctx, claude, "AGB", name, store.DocumentInput{Content: "---\nkind: report\ntitle: t\n---\n\n" + body}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	full := similarCount(t, st)
	if full == 0 {
		t.Fatal("no similar documents were computed")
	}

	// Nothing changed: the table is left alone, even when it was emptied by hand.
	if _, err := st.DB().Exec(`delete from similar_document`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunOnce(ctx); err != nil || similarCount(t, st) != 0 {
		t.Fatalf("a round without a change recomputed: %d rows, %v", similarCount(t, st), err)
	}

	// Archiving a document computes them again, without that document.
	if _, err := st.SetDocumentArchived(ctx, claude, "AGB", names[1], true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var withB int
	if err := st.DB().QueryRow(`select count(*) from similar_document sd join document d on d.id in (sd.document_id, sd.similar_id) where d.name = ?`, names[1]).Scan(&withB); err != nil {
		t.Fatal(err)
	}
	if n := similarCount(t, st); n == 0 || n >= full || withB != 0 {
		t.Errorf("after archiving: %d rows (was %d), %d of them with the archived document", n, full, withB)
	}

	// So does a supersedes relation, and taking the mark off.
	if _, err := st.DB().Exec(`delete from similar_document`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ChangeDocumentRelation(ctx, claude, "AGB", names[2], model.RelSupersedes, names[0], true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// a is superseded and b is archived: only c is current, so no pair is left.
	if n := similarCount(t, st); n != 0 {
		t.Errorf("after a supersedes: %d rows", n)
	}
	if _, err := st.SetDocumentArchived(ctx, claude, "AGB", names[1], false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunOnce(ctx); err != nil || similarCount(t, st) == 0 {
		t.Errorf("after the mark was taken off: %d rows, %v", similarCount(t, st), err)
	}
}
