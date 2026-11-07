package embed

import (
	"context"
	"errors"
	"testing"

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
	got := x.Search([]float32{3, 0}, 5)
	if len(got) != 2 || got[0] != (store.SourceText{Source: "issue", ID: 1, Text: "near"}) || got[1].Text != "middle" {
		t.Errorf("hits = %+v", got)
	}
	if got := x.Search([]float32{3, 0}, 1); len(got) != 1 || got[0].Text != "near" {
		t.Errorf("top 1 = %+v", got)
	}
	var empty Index
	if got := empty.Search([]float32{1}, 5); len(got) != 0 {
		t.Errorf("empty index = %+v", got)
	}
}

func TestSearcherEmbedsTheQueryWithThePrefix(t *testing.T) {
	var x Index
	x.Load([]store.EmbeddingRow{{Source: "issue", SourceID: 1, Text: "t", Vector: []float32{1, 0, 0}}})
	f := newFake()
	s := &Searcher{Embedder: f, Prefix: "aQuery: ", Index: &x}
	got, err := s.Search(context.Background(), "b", 5)
	if err != nil || len(got) != 1 || f.calls[0][0] != "aQuery: b" {
		t.Errorf("got %+v, %v, calls %v", got, err, f.calls)
	}
	if _, err := (&Searcher{Embedder: failing{}, Index: &x}).Search(context.Background(), "b", 5); err == nil {
		t.Error("an embedder failure was not reported")
	}
}

type failing struct{}

func (failing) Embed(context.Context, []string) ([][]float32, error) { return nil, errors.New("x") }
