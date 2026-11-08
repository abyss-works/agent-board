package embed

import (
	"cmp"
	"context"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wsk0715/agent-board/internal/store"
)

// queryTimeout bounds each attempt to embed a search query. The API usually
// answers in under a second but sometimes takes several; a tighter bound drops
// the search by meaning for those queries. Search retries a failed attempt
// once, so a single slow call does not lose the search by meaning.
const queryTimeout = 10 * time.Second

// Index holds every stored chunk in memory and finds the sources nearest to a
// query vector.
//
// ponytail: brute force over all chunks, and Load replaces the whole index.
// This holds up to tens of thousands of chunks; beyond that use an ANN index
// or update the index per source instead of reloading it.
type Index struct {
	mu      sync.RWMutex
	entries []store.EmbeddingRow
}

// Load replaces the content of the index with rows, whose vectors must be
// normalized.
func (x *Index) Load(rows []store.EmbeddingRow) {
	x.mu.Lock()
	x.entries = rows
	x.mu.Unlock()
}

// Search returns the n sources whose best chunk is nearest to q, nearest
// first. Text of a result is that best chunk and Score its cosine similarity
// to q. Only sources in allowed take part; nil allows every source. Chunks
// whose vector has another size than q are skipped.
func (x *Index) Search(q []float32, n int, allowed map[store.SourceRef]struct{}) []store.SourceText {
	q = normalize(q)
	type key struct {
		source string
		id     int64
	}
	type scored struct {
		store.SourceText
		score float32
	}
	x.mu.RLock()
	best := map[key]scored{}
	for i := range x.entries {
		e := &x.entries[i]
		if len(e.Vector) != len(q) {
			continue
		}
		if allowed != nil {
			if _, ok := allowed[store.SourceRef{Source: e.Source, ID: e.SourceID}]; !ok {
				continue
			}
		}
		var dot float32
		for j, v := range e.Vector {
			dot += v * q[j]
		}
		k := key{e.Source, e.SourceID}
		if b, ok := best[k]; !ok || dot > b.score {
			best[k] = scored{store.SourceText{Source: e.Source, ID: e.SourceID, Text: e.Text, Score: float64(dot)}, dot}
		}
	}
	x.mu.RUnlock()
	all := make([]scored, 0, len(best))
	for _, b := range best {
		all = append(all, b)
	}
	slices.SortFunc(all, func(a, b scored) int {
		return cmp.Or(cmp.Compare(b.score, a.score), strings.Compare(a.Source, b.Source), cmp.Compare(a.ID, b.ID))
	})
	if len(all) > n {
		all = all[:n]
	}
	out := make([]store.SourceText, len(all))
	for i, a := range all {
		out[i] = a.SourceText
	}
	return out
}

// normalize returns v scaled to unit length. A zero vector stays as it is.
func normalize(v []float32) []float32 {
	var sum float64
	for _, f := range v {
		sum += float64(f) * float64(f)
	}
	out := make([]float32, len(v))
	if sum == 0 {
		return out
	}
	n := float32(math.Sqrt(sum))
	for i, f := range v {
		out[i] = f / n
	}
	return out
}

// Searcher finds the sources nearest in meaning to a query text.
type Searcher struct {
	Embedder Embedder
	Prefix   string // prepended to the query text before it is embedded
	Index    *Index
	Timeout  time.Duration // bound of one embedding attempt; zero means queryTimeout
}

// Search embeds the query and returns the n nearest sources among allowed, as
// Index.Search does. A failed embedding is retried once unless ctx is already
// done.
func (s *Searcher) Search(ctx context.Context, query string, n int, allowed map[store.SourceRef]struct{}) ([]store.SourceText, error) {
	vecs, err := s.embed(ctx, query)
	if err != nil && ctx.Err() == nil {
		vecs, err = s.embed(ctx, query)
	}
	if err != nil {
		return nil, err
	}
	return s.Index.Search(vecs[0], n, allowed), nil
}

func (s *Searcher) embed(ctx context.Context, query string) ([][]float32, error) {
	timeout := cmp.Or(s.Timeout, queryTimeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return s.Embedder.Embed(ctx, []string{s.Prefix + query})
}
