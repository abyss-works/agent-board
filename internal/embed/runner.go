package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/wsk0715/agent-board/internal/store"
)

// batchSize is the number of chunks sent to the embeddings API in one request.
const batchSize = 32

// Runner keeps the stored embeddings current and reloads the index when they
// change.
type Runner struct {
	Store    *store.Store
	Embedder Embedder
	Model    string
	Interval time.Duration
	Index    *Index
	Logger   *slog.Logger

	// similarDone records that the similar documents were computed once, so
	// that a server started on stored embeddings computes them without waiting
	// for a change.
	similarDone bool
	// current holds the documents that were current when the similar documents
	// were computed; a document that gains or loses that state, by being
	// archived or superseded, calls for another computation.
	current []int64
}

// Result counts what one round changed: the sources embedded again and the
// sources whose rows were removed.
type Result struct {
	Sources int
	Removed int
}

// Run indexes at once and then every Interval until ctx is done.
func (r *Runner) Run(ctx context.Context) {
	r.round(ctx)
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.round(ctx)
		}
	}
}

func (r *Runner) round(ctx context.Context) {
	res, err := r.RunOnce(ctx)
	if res.Sources+res.Removed > 0 {
		r.Logger.Info("embeddings updated", "embedded", res.Sources, "removed", res.Removed)
	}
	if err != nil && ctx.Err() == nil {
		r.Logger.Warn("embedding round ended early, retrying next round", "err", err.Error())
	}
}

// RunOnce embeds every source whose chunks differ from the stored ones, removes
// the rows of sources that are gone, and reloads the index and the similar
// documents if anything changed, if a document became or stopped being current,
// and once on the first round. An error of the embeddings API ends the round;
// what was stored before it stays.
func (r *Runner) RunOnce(ctx context.Context) (res Result, err error) {
	defer func() {
		current, lerr := r.Store.CurrentDocumentIDs(ctx)
		if lerr != nil {
			if err == nil {
				err = lerr
			}
			return
		}
		if res.Sources+res.Removed == 0 && r.similarDone && slices.Equal(current, r.current) {
			return
		}
		rows, lerr := r.Store.LoadEmbeddings(ctx, r.Model)
		if lerr == nil {
			r.Index.Load(rows)
			set := make(map[int64]bool, len(current))
			for _, id := range current {
				set[id] = true
			}
			lerr = r.Store.ReplaceSimilarDocuments(ctx, r.Model, similarPairs(rows, similarPerDocument, similarFloor, set))
		}
		if lerr != nil {
			if err == nil {
				err = lerr
			}
			return
		}
		r.similarDone, r.current = true, current
	}()

	texts, err := r.Store.SourceTexts(ctx)
	if err != nil {
		return res, err
	}
	meta, err := r.Store.EmbeddingMeta(ctx)
	if err != nil {
		return res, err
	}
	type key struct {
		source string
		id     int64
	}
	have := map[key][]store.EmbeddingRow{}
	for _, m := range meta {
		k := key{m.Source, m.SourceID}
		have[k] = append(have[k], m)
	}

	type job struct {
		st     store.SourceText
		chunks []string
		shas   []string
	}
	var jobs []job
	for _, st := range texts {
		chunks := Chunk(st.Text)
		if len(chunks) == 0 {
			continue
		}
		k := key{st.Source, st.ID}
		shas := make([]string, len(chunks))
		for i, c := range chunks {
			sum := sha256.Sum256([]byte(c))
			shas[i] = hex.EncodeToString(sum[:])
		}
		stored := have[k]
		delete(have, k)
		if !current(stored, shas, r.Model) {
			jobs = append(jobs, job{st, chunks, shas})
		}
	}
	// Whatever is left in have belongs to a source that is gone or has no text.
	for k := range have {
		if err := r.Store.ReplaceEmbeddings(ctx, k.source, k.id, nil); err != nil {
			return res, err
		}
		res.Removed++
	}

	// Most sources have one or two chunks, so the chunks of several sources
	// share a request; a group is stored as soon as its vectors are back.
	for start := 0; start < len(jobs); {
		end, n := start, 0
		for end < len(jobs) && (n == 0 || n+len(jobs[end].chunks) <= batchSize) {
			n += len(jobs[end].chunks)
			end++
		}
		group := jobs[start:end]
		var texts []string
		for _, j := range group {
			texts = append(texts, j.chunks...)
		}
		vecs := make([][]float32, 0, len(texts))
		for b := 0; b < len(texts); b += batchSize {
			batch, err := r.Embedder.Embed(ctx, texts[b:min(b+batchSize, len(texts))])
			if err != nil {
				return res, fmt.Errorf("embed %s %d: %w", group[0].st.Source, group[0].st.ID, err)
			}
			vecs = append(vecs, batch...)
		}
		off := 0
		for _, j := range group {
			rows := make([]store.EmbeddingRow, len(j.chunks))
			for i, c := range j.chunks {
				rows[i] = store.EmbeddingRow{Source: j.st.Source, SourceID: j.st.ID, Chunk: i, Model: r.Model, SHA256: j.shas[i], Text: c, Vector: normalize(vecs[off+i])}
			}
			off += len(j.chunks)
			if err := r.Store.ReplaceEmbeddings(ctx, j.st.Source, j.st.ID, rows); err != nil {
				return res, err
			}
			res.Sources++
		}
		start = end
	}
	return res, nil
}

// current reports whether the stored rows are the embeddings of the chunks
// with the hashes shas, made by model.
func current(stored []store.EmbeddingRow, shas []string, model string) bool {
	if len(stored) != len(shas) {
		return false
	}
	for i, s := range stored {
		if s.Chunk != i || s.SHA256 != shas[i] || s.Model != model {
			return false
		}
	}
	return true
}
