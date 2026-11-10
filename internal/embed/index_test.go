package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/abyss-works/agent-board/internal/store"
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

// logged runs one search and returns the log lines it wrote.
func logged(e Embedder, timeout time.Duration, query string) []map[string]any {
	var buf bytes.Buffer
	s := &Searcher{Embedder: e, Model: "m1", Index: oneSourceIndex(), Timeout: timeout, Logger: slog.New(slog.NewJSONHandler(&buf, nil))}
	s.Search(context.Background(), query, 5, nil)
	var lines []map[string]any
	for _, l := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte{10}) {
		var m map[string]any
		if json.Unmarshal(l, &m) == nil {
			lines = append(lines, m)
		}
	}
	return lines
}

func TestSearcherLogsOneLinePerQueryEmbedding(t *testing.T) {
	for name, tc := range map[string]struct {
		e      Embedder
		result string
	}{
		"ok":      {&scripted{}, "ok"},
		"error":   {failing{}, "error"},
		"timeout": {&hanging{}, "timeout"},
	} {
		lines := logged(tc.e, 20*time.Millisecond, "secret-query-text")
		if len(lines) != 1 {
			t.Fatalf("%s: %d log lines", name, len(lines))
		}
		l := lines[0]
		if l["level"] != "INFO" || l["purpose"] != "query" || l["model"] != "m1" || l["result"] != tc.result {
			t.Errorf("%s: line = %v", name, l)
		}
		if _, ok := l["duration_ms"]; !ok {
			t.Errorf("%s: no duration_ms in %v", name, l)
		}
		if p, _ := l["response_id"].(string); (name == "ok") != (p == "p") {
			t.Errorf("%s: response_id = %q", name, p)
		}
		raw, _ := json.Marshal(l)
		if strings.Contains(string(raw), "secret-query-text") {
			t.Errorf("%s: log holds the query: %s", name, raw)
		}
	}
}

type failing struct{}

func (failing) Embed(context.Context, []string) ([][]float32, string, error) {
	return nil, "", errors.New("x")
}

// scripted answers each call with the next entry of errs; nil succeeds with a
// vector matching the index.
type scripted struct {
	errs  []error
	calls int
}

func (s *scripted) Embed(ctx context.Context, _ []string) ([][]float32, string, error) {
	i := s.calls
	s.calls++
	if i < len(s.errs) && s.errs[i] != nil {
		return nil, "", s.errs[i]
	}
	return [][]float32{{1, 0, 0}}, "p", nil
}

func oneSourceIndex() *Index {
	var x Index
	x.Load([]store.EmbeddingRow{{Source: "issue", SourceID: 1, Text: "t", Vector: []float32{1, 0, 0}}})
	return &x
}

func TestSearcherEmbedsTheQueryOnceAndReportsAFailure(t *testing.T) {
	e := &scripted{errs: []error{errors.New("a"), errors.New("b")}}
	_, err := (&Searcher{Embedder: e, Index: oneSourceIndex()}).Search(context.Background(), "q", 5, nil)
	if err == nil || err.Error() != "a" || e.calls != 1 {
		t.Errorf("err %v, calls %d", err, e.calls)
	}
}

// hanging blocks until its context ends.
type hanging struct{ calls int }

func (h *hanging) Embed(ctx context.Context, _ []string) ([][]float32, string, error) {
	h.calls++
	<-ctx.Done()
	return nil, "", ctx.Err()
}

func TestSearcherStopsWaitingAfterTheTimeoutWithoutRetrying(t *testing.T) {
	h := &hanging{}
	s := &Searcher{Embedder: h, Index: oneSourceIndex(), Timeout: 20 * time.Millisecond}
	_, err := s.Search(context.Background(), "q", 5, nil)
	if !errors.Is(err, context.DeadlineExceeded) || h.calls != 1 {
		t.Errorf("err %v, calls %d", err, h.calls)
	}
}

func TestSearcherAnswersWithinTheTimeout(t *testing.T) {
	e := &scripted{}
	s := &Searcher{Embedder: e, Index: oneSourceIndex(), Timeout: time.Second}
	got, err := s.Search(context.Background(), "q", 5, nil)
	if err != nil || len(got) != 1 || e.calls != 1 {
		t.Errorf("got %+v, %v, calls %d", got, err, e.calls)
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
