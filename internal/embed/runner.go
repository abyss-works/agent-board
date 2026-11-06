package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
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
// the rows of sources that are gone, and reloads the index if anything
// changed. An error of the embeddings API ends the round; what was stored
// before it stays.
func (r *Runner) RunOnce(ctx context.Context) (res Result, err error) {
	defer func() {
		if res.Sources+res.Removed == 0 {
			return
		}
		rows, lerr := r.Store.LoadEmbeddings(ctx, r.Model)
		if lerr != nil {
			if err == nil {
				err = lerr
			}
			return
		}
		r.Index.Load(rows)
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

	for _, j := range jobs {
		vecs := make([][]float32, 0, len(j.chunks))
		for start := 0; start < len(j.chunks); start += batchSize {
			batch, err := r.Embedder.Embed(ctx, j.chunks[start:min(start+batchSize, len(j.chunks))])
			if err != nil {
				return res, fmt.Errorf("embed %s %d: %w", j.st.Source, j.st.ID, err)
			}
			vecs = append(vecs, batch...)
		}
		rows := make([]store.EmbeddingRow, len(j.chunks))
		for i, c := range j.chunks {
			rows[i] = store.EmbeddingRow{Source: j.st.Source, SourceID: j.st.ID, Chunk: i, Model: r.Model, SHA256: j.shas[i], Text: c, Vector: normalize(vecs[i])}
		}
		if err := r.Store.ReplaceEmbeddings(ctx, j.st.Source, j.st.ID, rows); err != nil {
			return res, err
		}
		res.Sources++
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
