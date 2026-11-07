package embed

import (
	"cmp"
	"slices"

	"github.com/wsk0715/agent-board/internal/store"
)

const (
	// similarPerDocument is how many nearest documents each document links to.
	similarPerDocument = 3
	// similarFloor is the lowest cosine similarity that still links two
	// documents. Chosen on the records of four management repositories with
	// qwen/qwen3-embedding-8b; another model may need another floor.
	similarFloor = 0.70
)

// similarPairs finds, for each document among rows, the nearest other
// documents whose score is at least floor, at most perDocument each. A
// document's vector is the normalized mean of its chunk vectors.
//
// ponytail: compares every pair of documents, O(n²·d). Fine for a few
// thousand documents; use the in-memory index per document beyond that.
func similarPairs(rows []store.EmbeddingRow, perDocument int, floor float64) []store.SimilarPair {
	type doc struct {
		id  int64
		vec []float32
		n   int
	}
	var docs []*doc
	byID := map[int64]*doc{}
	for _, r := range rows {
		if r.Source != "document" {
			continue
		}
		d := byID[r.SourceID]
		if d == nil {
			d = &doc{id: r.SourceID, vec: make([]float32, len(r.Vector))}
			byID[r.SourceID] = d
			docs = append(docs, d)
		}
		if len(r.Vector) != len(d.vec) {
			continue
		}
		for i, v := range r.Vector {
			d.vec[i] += v
		}
		d.n++
	}
	for _, d := range docs {
		d.vec = normalize(d.vec)
	}
	var pairs []store.SimilarPair
	for _, a := range docs {
		var near []store.SimilarPair
		for _, b := range docs {
			if a == b || len(a.vec) != len(b.vec) {
				continue
			}
			var dot float64
			for i, v := range a.vec {
				dot += float64(v) * float64(b.vec[i])
			}
			if dot >= floor {
				near = append(near, store.SimilarPair{Document: a.id, Similar: b.id, Score: dot})
			}
		}
		slices.SortFunc(near, func(x, y store.SimilarPair) int {
			return cmp.Or(cmp.Compare(y.Score, x.Score), cmp.Compare(x.Similar, y.Similar))
		})
		pairs = append(pairs, near[:min(perDocument, len(near))]...)
	}
	return pairs
}
