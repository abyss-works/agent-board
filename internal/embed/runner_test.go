package embed

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abyss-works/agent-board/internal/model"
	"github.com/abyss-works/agent-board/internal/store"
)

var (
	owner  = model.Trace{Actor: "owner", SessionID: "s", ClientHost: "pc"}
	claude = model.Trace{Actor: "claude", SessionID: "s", ClientHost: "pc"}
)

// fakeEmbedder maps a text to a vector without a network. Equal texts have
// equal vectors. From failAfter calls on it fails.
type fakeEmbedder struct {
	calls     [][]string
	failAfter int // -1 never fails
}

func newFake() *fakeEmbedder { return &fakeEmbedder{failAfter: -1} }

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, string, error) {
	if f.failAfter >= 0 && len(f.calls) >= f.failAfter {
		return nil, "", errors.New("embeddings API returned 503")
	}
	f.calls = append(f.calls, texts)
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = fakeVector(t)
	}
	return out, "", nil
}

// fakeVector counts the letters a, b and c in the text.
func fakeVector(text string) []float32 {
	return []float32{float32(strings.Count(text, "a")), float32(strings.Count(text, "b")), float32(strings.Count(text, "c")) + 0.01}
}

func (f *fakeEmbedder) texts() int {
	n := 0
	for _, c := range f.calls {
		n += len(c)
	}
	return n
}

func newRunner(t *testing.T, f Embedder) (*Runner, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "agb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.EnsureActors(ctx, []model.Actor{{ID: "owner", Kind: model.ActorUser}, {ID: "claude", Kind: model.ActorAgent}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProject(ctx, owner, "AGB", "agent-board"); err != nil {
		t.Fatal(err)
	}
	return &Runner{Store: st, Embedder: f, Model: "m", Index: &Index{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, st
}

func issue(t *testing.T, st *store.Store, title, body string) *model.Issue {
	t.Helper()
	i, err := st.CreateIssue(context.Background(), owner, store.IssueInput{Project: "AGB", Title: title, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func stored(t *testing.T, st *store.Store) int {
	t.Helper()
	meta, err := st.EmbeddingMeta(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(meta)
}

func TestRunOnceIndexesAndSkipsWhatIsCurrent(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	r, st := newRunner(t, f)
	i := issue(t, st, "aaa", "bb")
	if _, err := st.AddComment(ctx, claude, i.Key, "ccc", str("요약"), nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.PutDocument(ctx, claude, "AGB", "2026-11-01-0900-report-x.md", store.DocumentInput{Content: "---\nkind: report\ntitle: t\n---\n\nbbbb"}); err != nil {
		t.Fatal(err)
	}

	res, err := r.RunOnce(ctx)
	if err != nil || res.Sources != 3 || res.Removed != 0 {
		t.Fatalf("first round = %+v, %v", res, err)
	}
	if n := stored(t, st); n != 3 || f.texts() != 3 {
		t.Fatalf("stored %d rows with %d embedded texts", n, f.texts())
	}
	// The index holds what was stored: the issue is the nearest to its own text.
	if got := r.Index.Search(fakeVector("aaa\nbb"), 1, nil); len(got) != 1 || got[0].Source != "issue" || got[0].Text != "aaa\nbb" {
		t.Errorf("index search = %+v", got)
	}

	before := len(f.calls)
	if res, err := r.RunOnce(ctx); err != nil || res.Sources != 0 || len(f.calls) != before {
		t.Errorf("second round = %+v, %v, %d new calls", res, err, len(f.calls)-before)
	}
}

func TestRunOnceReindexesAnEditedIssueAndAChangedModel(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	r, st := newRunner(t, f)
	a := issue(t, st, "aaa", "")
	issue(t, st, "bbb", "")
	if _, err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}

	if _, _, err := st.UpdateIssue(ctx, owner, a.Key, store.IssuePatch{Title: str("ccc")}, nil); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	res, err := r.RunOnce(ctx)
	if err != nil || res.Sources != 1 || f.texts() != 1 || f.calls[0][0] != "ccc" {
		t.Fatalf("round after an edit = %+v, %v, calls %v", res, err, f.calls)
	}
	if got := r.Index.Search(fakeVector("ccc"), 1, nil); len(got) != 1 || got[0].Text != "ccc" {
		t.Errorf("index after an edit = %+v", got)
	}

	f.calls = nil
	r.Model = "m2"
	if res, err := r.RunOnce(ctx); err != nil || res.Sources != 2 || f.texts() != 2 {
		t.Errorf("round after a model change = %+v, %v, %d texts", res, err, f.texts())
	}
	if rows, _ := st.LoadEmbeddings(ctx, "m"); len(rows) != 0 {
		t.Errorf("rows of the old model = %d", len(rows))
	}
}

func TestRunOnceRemovesWhatIsNoLongerSearchable(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	r, st := newRunner(t, f)
	a := issue(t, st, "aaa", "")
	b := issue(t, st, "bbb", "")
	if _, err := st.AddComment(ctx, claude, b.Key, "ccc", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunOnce(ctx); err != nil || stored(t, st) != 3 {
		t.Fatalf("first round: %d rows, %v", stored(t, st), err)
	}

	del := model.StatusDeleted
	if _, _, err := st.UpdateIssue(ctx, owner, b.Key, store.IssuePatch{Status: &del, StatusReason: str("삭제")}, nil); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	res, err := r.RunOnce(ctx)
	if err != nil || res.Removed != 2 || stored(t, st) != 1 || len(f.calls) != 0 {
		t.Fatalf("round after a delete = %+v, %v, %d rows, %d calls", res, err, stored(t, st), len(f.calls))
	}
	if got := r.Index.Search(fakeVector("bbb"), 5, nil); len(got) != 1 || got[0].Text != a.Title {
		t.Errorf("index after a delete = %+v", got)
	}
}

func TestRunOnceEndsTheRoundOnAnAPIError(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	f.failAfter = 1
	r, st := newRunner(t, f)
	// The first issue fills a request by itself, so the other two need a
	// second request, which fails.
	issue(t, st, "aaa", strings.Repeat("x", 900*31))
	issue(t, st, "bbb", "")
	issue(t, st, "ccc", "")

	res, err := r.RunOnce(ctx)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v", err)
	}
	// The source embedded before the failure stays, and the index knows it.
	if n := stored(t, st); n != batchSize || res.Sources != 1 || len(f.calls) != 1 {
		t.Errorf("after the error: %d rows, %+v, %d calls", n, res, len(f.calls))
	}
	if got := r.Index.Search(fakeVector("aaa"), 5, nil); len(got) != 1 {
		t.Errorf("index after the error = %+v", got)
	}
	// The next round retries the rest.
	f.failAfter = -1
	if res, err := r.RunOnce(ctx); err != nil || res.Sources != 2 || stored(t, st) != batchSize+2 {
		t.Errorf("retry = %+v, %v, %d rows", res, err, stored(t, st))
	}
}

func TestRunOnceSendsChunksInBatchesOf32(t *testing.T) {
	f := newFake()
	r, st := newRunner(t, f)
	issue(t, st, "t", strings.Repeat("a", 70*MaxChunkRunes-2)) // title, newline and body make 70 chunks
	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 3 || len(f.calls[0]) != 32 || len(f.calls[1]) != 32 || len(f.calls[2]) != 6 || stored(t, st) != 70 {
		t.Errorf("batches = %d, rows %d", len(f.calls), stored(t, st))
	}
}

func str(s string) *string { return &s }

// Short sources share a request instead of sending one each.
func TestRunOnceBatchesAcrossSources(t *testing.T) {
	f := newFake()
	r, st := newRunner(t, f)
	for _, title := range []string{"a", "b", "c", "d", "e"} {
		issue(t, st, title, "")
	}
	if res, err := r.RunOnce(context.Background()); err != nil || res.Sources != 5 || len(f.calls) != 1 || len(f.calls[0]) != 5 {
		t.Errorf("round = %+v, %v, calls %v", res, err, f.calls)
	}
}
