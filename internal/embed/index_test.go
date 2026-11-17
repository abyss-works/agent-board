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

// withQueryTimeouts shortens the limits the attempts share for one test.
func withQueryTimeouts(t *testing.T, first, total time.Duration) {
	t.Helper()
	prevFirst, prevTotal := queryFirstTimeout, queryTotalTimeout
	queryFirstTimeout, queryTotalTimeout = first, total
	t.Cleanup(func() {
		queryFirstTimeout, queryTotalTimeout = prevFirst, prevTotal
	})
}

// logged runs one search and returns the log lines it wrote.
func logged(e Embedder, retry time.Duration, query string) []map[string]any {
	var buf bytes.Buffer
	s := &Searcher{Embedder: e, Model: "m1", Index: oneSourceIndex(), Timeout: retry, Logger: slog.New(slog.NewJSONHandler(&buf, nil))}
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

// A query embedding that succeeds writes one line; a failed first attempt
// writes a line for each of its two attempts.
func TestSearcherLogsOneLinePerEmbeddingAttempt(t *testing.T) {
	withQueryTimeouts(t, 20*time.Millisecond, 60*time.Millisecond)
	for name, tc := range map[string]struct {
		e      Embedder
		result string
		lines  int
	}{
		"ok":      {&scripted{}, "ok", 1},
		"error":   {failing{}, "error", 2},
		"timeout": {&hanging{}, "timeout", 2},
	} {
		lines := logged(tc.e, 20*time.Millisecond, "secret-query-text")
		if len(lines) != tc.lines {
			t.Fatalf("%s: %d log lines, want %d", name, len(lines), tc.lines)
		}
		for i, l := range lines {
			if l["level"] != "INFO" || l["purpose"] != "query" || l["model"] != "m1" || l["result"] != tc.result || l["attempt"] != float64(i+1) {
				t.Errorf("%s: line %d = %v", name, i, l)
			}
			if _, ok := l["duration_ms"]; !ok {
				t.Errorf("%s: no duration_ms in %v", name, l)
			}
			p, _ := l["response_id"].(string)
			if (tc.result == "ok") != (p == "p") {
				t.Errorf("%s: response_id = %q", name, p)
			}
			raw, _ := json.Marshal(l)
			if strings.Contains(string(raw), "secret-query-text") {
				t.Errorf("%s: log holds the query: %s", name, raw)
			}
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
	errs   []error
	calls  int
	inputs [][]string
}

func (s *scripted) Embed(ctx context.Context, in []string) ([][]float32, string, error) {
	i := s.calls
	s.calls++
	s.inputs = append(s.inputs, in)
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

func TestSearcherReportsTheLastFailureAfterARetry(t *testing.T) {
	e := &scripted{errs: []error{errors.New("a"), errors.New("b")}}
	withQueryTimeouts(t, 20*time.Millisecond, time.Second)
	_, err := (&Searcher{Embedder: e, Index: oneSourceIndex(), Timeout: 20 * time.Millisecond}).Search(context.Background(), "q", 5, nil)
	if err == nil || err.Error() != "b" || e.calls != 2 {
		t.Errorf("err %v, calls %d", err, e.calls)
	}
}

func TestSearcherRetriesOnceWithTheSameQuery(t *testing.T) {
	e := &scripted{errs: []error{errors.New("first")}}
	withQueryTimeouts(t, 20*time.Millisecond, time.Second)
	s := &Searcher{Embedder: e, Prefix: "aQuery: ", Index: oneSourceIndex(), Timeout: 20 * time.Millisecond}
	got, err := s.Search(context.Background(), "b", 5, nil)
	if err != nil || len(got) != 1 || e.calls != 2 {
		t.Fatalf("got %+v, %v, calls %d", got, err, e.calls)
	}
	for i, in := range e.inputs {
		if len(in) != 1 || in[0] != "aQuery: b" {
			t.Errorf("attempt %d embedded %v", i+1, in)
		}
	}
}

// hanging blocks until its context ends.
type hanging struct{ calls int }

func (h *hanging) Embed(ctx context.Context, _ []string) ([][]float32, string, error) {
	h.calls++
	<-ctx.Done()
	return nil, "", ctx.Err()
}

// hangAfterFirst fails the first attempt at once and blocks on the second
// until its context ends.
type hangAfterFirst struct{ calls int }

func (h *hangAfterFirst) Embed(ctx context.Context, _ []string) ([][]float32, string, error) {
	h.calls++
	if h.calls == 1 {
		return nil, "", errors.New("first")
	}
	<-ctx.Done()
	return nil, "", ctx.Err()
}

// A failed first attempt is retried once, and both attempts stay inside the
// total limit even when the retry timeout alone is longer.
func TestSearcherNeverWaitsBeyondTheTotalLimit(t *testing.T) {
	withQueryTimeouts(t, 20*time.Millisecond, 60*time.Millisecond)
	h := &hanging{}
	s := &Searcher{Embedder: h, Index: oneSourceIndex(), Timeout: 5 * time.Second}
	start := time.Now()
	_, err := s.Search(context.Background(), "q", 5, nil)
	if !errors.Is(err, context.DeadlineExceeded) || h.calls != 2 {
		t.Errorf("err %v, calls %d", err, h.calls)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("search took %v, beyond the total limit", d)
	}
}

// The total limit ends the first attempt, so there is no time left to retry.
func TestSearcherDoesNotRetryAfterTheTotalLimit(t *testing.T) {
	withQueryTimeouts(t, time.Second, 20*time.Millisecond)
	h := &hanging{}
	s := &Searcher{Embedder: h, Index: oneSourceIndex(), Timeout: 20 * time.Millisecond}
	_, err := s.Search(context.Background(), "q", 5, nil)
	if !errors.Is(err, context.DeadlineExceeded) || h.calls != 1 {
		t.Errorf("err %v, calls %d", err, h.calls)
	}
}

// The retry runs under Searcher.Timeout, not under the first attempt's limit.
func TestSearcherBoundsTheRetryByItsTimeout(t *testing.T) {
	withQueryTimeouts(t, time.Second, time.Second)
	h := &hangAfterFirst{}
	s := &Searcher{Embedder: h, Index: oneSourceIndex(), Timeout: 20 * time.Millisecond}
	start := time.Now()
	_, err := s.Search(context.Background(), "q", 5, nil)
	if !errors.Is(err, context.DeadlineExceeded) || h.calls != 2 {
		t.Errorf("err %v, calls %d", err, h.calls)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("search took %v, beyond the retry timeout", d)
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
