package embed

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/store"
)

func TestIndexSearchReturnsTheBestChunkPerSource(t *testing.T) {
	var x Index
	x.Load([]store.EmbeddingRow{
		{Source: "issue", SourceID: 1, Chunk: 0, Text: "far", Vector: []float32{0, 1}},
		{Source: "issue", SourceID: 1, Chunk: 1, Text: "near", Vector: []float32{1, 0}},
		{Source: "comment", SourceID: 1, Chunk: 0, Text: "middle", Vector: []float32{0.6, 0.8}},
		{Source: "document", SourceID: 1, Chunk: 0, Text: "other size", Vector: []float32{1}},
	})
	got := x.Search([]float32{3, 0}, 5, nil)
	if len(got) != 2 || got[0] != (store.SourceText{Source: "issue", ID: 1, Text: "near", Score: 1}) || got[1].Text != "middle" {
		t.Errorf("hits = %+v", got)
	}
	if got := x.Search([]float32{3, 0}, 1, nil); len(got) != 1 || got[0].Text != "near" {
		t.Errorf("top 1 = %+v", got)
	}
	var empty Index
	if got := empty.Search([]float32{1}, 5, nil); len(got) != 0 {
		t.Errorf("empty index = %+v", got)
	}
}

func TestSearcherEmbedsTheQueryWithThePrefix(t *testing.T) {
	var x Index
	x.Load([]store.EmbeddingRow{{Source: "issue", SourceID: 1, Text: "t", Vector: []float32{1, 0, 0}}})
	f := newFake()
	s := &Searcher{Embedder: f, Prefix: "aQuery: ", Index: &x}
	got, err := s.Search(context.Background(), "b", 5, nil)
	if err != nil || len(got) != 1 || f.calls[0][0] != "aQuery: b" {
		t.Errorf("got %+v, %v, calls %v", got, err, f.calls)
	}
	if _, err := (&Searcher{Embedder: failing{}, Index: &x}).Search(context.Background(), "b", 5, nil); err == nil {
		t.Error("an embedder failure was not reported")
	}
}

type failing struct{}

func (failing) Embed(context.Context, []string) ([][]float32, error) { return nil, errors.New("x") }

// scripted answers each call with the next entry of errs; nil succeeds with a
// vector matching the index.
type scripted struct {
	errs  []error
	calls int
}

func (s *scripted) Embed(ctx context.Context, _ []string) ([][]float32, error) {
	i := s.calls
	s.calls++
	if i < len(s.errs) && s.errs[i] != nil {
		return nil, s.errs[i]
	}
	return [][]float32{{1, 0, 0}}, nil
}

func oneSourceIndex() *Index {
	var x Index
	x.Load([]store.EmbeddingRow{{Source: "issue", SourceID: 1, Text: "t", Vector: []float32{1, 0, 0}}})
	return &x
}

func TestSearcherRetriesTheQueryOnceAfterAFailure(t *testing.T) {
	e := &scripted{errs: []error{errors.New("deadline")}}
	got, err := (&Searcher{Embedder: e, Index: oneSourceIndex()}).Search(context.Background(), "q", 5, nil)
	if err != nil || len(got) != 1 || e.calls != 2 {
		t.Errorf("got %+v, %v, calls %d", got, err, e.calls)
	}
}

func TestSearcherReportsTheErrorAfterTwoFailures(t *testing.T) {
	e := &scripted{errs: []error{errors.New("a"), errors.New("b")}}
	_, err := (&Searcher{Embedder: e, Index: oneSourceIndex()}).Search(context.Background(), "q", 5, nil)
	if err == nil || err.Error() != "b" || e.calls != 2 {
		t.Errorf("err %v, calls %d", err, e.calls)
	}
}

func TestSearcherDoesNotRetryWhenTheRequestIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := &scripted{errs: []error{context.Canceled}}
	_, err := (&Searcher{Embedder: e, Index: oneSourceIndex()}).Search(ctx, "q", 5, nil)
	if err == nil || e.calls != 1 {
		t.Errorf("err %v, calls %d", err, e.calls)
	}
}

// hanging blocks until its context ends on the first call and succeeds after.
type hanging struct{ calls int }

func (h *hanging) Embed(ctx context.Context, _ []string) ([][]float32, error) {
	h.calls++
	if h.calls == 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return [][]float32{{1, 0, 0}}, nil
}

func TestSearcherGivesEachAttemptItsOwnTimeout(t *testing.T) {
	h := &hanging{}
	s := &Searcher{Embedder: h, Index: oneSourceIndex(), Timeout: 20 * time.Millisecond}
	got, err := s.Search(context.Background(), "q", 5, nil)
	if err != nil || len(got) != 1 || h.calls != 2 {
		t.Errorf("got %+v, %v, calls %d", got, err, h.calls)
	}
}

// TestIndexSearchRanksAmongAllowedSourcesOnly keeps a nearer source that is not
// allowed from taking a place, so the limit counts allowed sources alone.
func TestIndexSearchRanksAmongAllowedSourcesOnly(t *testing.T) {
	var x Index
	x.Load([]store.EmbeddingRow{
		{Source: "issue", SourceID: 1, Text: "nearest", Vector: []float32{1, 0}},
		{Source: "issue", SourceID: 2, Text: "next", Vector: []float32{0.8, 0.6}},
		{Source: "comment", SourceID: 1, Text: "far", Vector: []float32{0, 1}},
	})
	allowed := map[store.SourceRef]struct{}{{Source: "issue", ID: 2}: {}, {Source: "comment", ID: 1}: {}}
	got := x.Search([]float32{1, 0}, 1, allowed)
	if len(got) != 1 || got[0].ID != 2 || got[0].Source != "issue" {
		t.Errorf("top 1 among allowed = %+v", got)
	}
	if got := x.Search([]float32{1, 0}, 5, map[store.SourceRef]struct{}{}); len(got) != 0 {
		t.Errorf("nothing allowed = %+v", got)
	}
	if got := x.Search([]float32{1, 0}, 5, nil); len(got) != 3 {
		t.Errorf("no restriction = %+v", got)
	}
}
