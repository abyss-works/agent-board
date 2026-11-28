package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/embed"
	"github.com/wsk0715/agent-board/internal/graph"
	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store/pg"
)

// DefaultSweepInterval is how often the worker sweeps for delayed retries or
// missed notifications.
const DefaultSweepInterval = 1 * time.Second

// WorkerConfig configures an asynchronous ingest Worker.
type WorkerConfig struct {
	Pool          *pgxpool.Pool
	Queue         *Queue
	Normalizer    pg.Normalizer
	Chunker       *SemanticChunker
	GraphStore    *graph.Store
	Extractor     *graph.Extractor
	Embedder      pg.Embedder
	Generations   *GenerationManager
	WorkerID      string
	SweepInterval time.Duration
	Logger        *slog.Logger
}

// Worker consumes ingest_job rows via LISTEN/NOTIFY and periodic sweeps,
// running the four independent stages (Normalize -> Semantic Chunk -> Graph
// Extract -> Embed) per job without ever mutating source domain tables.
type Worker struct {
	pool          *pgxpool.Pool
	queue         *Queue
	normalizer    pg.Normalizer
	chunker       *SemanticChunker
	graphStore    *graph.Store
	extractor     *graph.Extractor
	embedder      pg.Embedder
	generations   *GenerationManager
	workerID      string
	sweepInterval time.Duration
	logger        *slog.Logger
}

// NewWorker builds a Worker with sensible defaults for omitted optional
// components.
func NewWorker(cfg WorkerConfig) *Worker {
	q := cfg.Queue
	if q == nil {
		q = NewQueue(cfg.Pool, QueueConfig{})
	}
	norm := cfg.Normalizer
	if norm == nil {
		norm = NewDefaultNormalizer(DefaultPipelineVersion)
	}
	chunker := cfg.Chunker
	if chunker == nil {
		chunker = NewSemanticChunker(ChunkerConfig{})
	}
	gs := cfg.GraphStore
	if gs == nil {
		gs = graph.NewStore(cfg.Pool)
	}
	ext := cfg.Extractor
	if ext == nil {
		ext = graph.NewExtractor(cfg.Pool, gs)
	}
	gens := cfg.Generations
	if gens == nil {
		gens = NewGenerationManager(cfg.Pool)
	}
	workerID := strings.TrimSpace(cfg.WorkerID)
	if workerID == "" {
		workerID = "ingest-worker"
	}
	sweep := cfg.SweepInterval
	if sweep <= 0 {
		sweep = DefaultSweepInterval
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		pool:          cfg.Pool,
		queue:         q,
		normalizer:    norm,
		chunker:       chunker,
		graphStore:    gs,
		extractor:     ext,
		embedder:      cfg.Embedder,
		generations:   gens,
		workerID:      workerID,
		sweepInterval: sweep,
		logger:        logger,
	}
}

// Run listens on IngestNotifyChannel and periodically sweeps pending jobs until
// ctx is canceled.
func (w *Worker) Run(ctx context.Context) error {
	// Drain any already-queued jobs on startup.
	w.drainPending(ctx)

	ticker := time.NewTicker(w.sweepInterval)
	defer ticker.Stop()

	notifyCh := make(chan struct{}, 1)
	go w.listenLoop(ctx, notifyCh)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-notifyCh:
			w.drainPending(ctx)
		case <-ticker.C:
			w.drainPending(ctx)
		}
	}
}

func (w *Worker) listenLoop(ctx context.Context, notifyCh chan<- struct{}) {
	for {
		if ctx.Err() != nil {
			return
		}
		conn, err := w.pool.Acquire(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(min(w.sweepInterval, 500*time.Millisecond)):
			}
			continue
		}
		if _, err := conn.Exec(ctx, `LISTEN `+IngestNotifyChannel); err != nil {
			conn.Release()
			select {
			case <-ctx.Done():
				return
			case <-time.After(min(w.sweepInterval, 500*time.Millisecond)):
			}
			continue
		}

		for {
			if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
				break
			}
			select {
			case notifyCh <- struct{}{}:
			default:
			}
		}
		conn.Release()
	}
}

func (w *Worker) drainPending(ctx context.Context) {
	for ctx.Err() == nil {
		processed, err := w.ProcessOnce(ctx)
		if err != nil {
			w.logger.Warn("ingest worker claim error", "worker", w.workerID, "err", err)
			return
		}
		if !processed {
			return
		}
	}
}

// ProcessOnce claims and processes at most one due pending job. It returns
// processed=true when a job was claimed (whether it succeeded, retried, or was
// dead-lettered) so callers can drain queues deterministically.
func (w *Worker) ProcessOnce(ctx context.Context) (bool, error) {
	job, err := w.queue.ClaimNext(ctx, w.workerID)
	if err != nil {
		return false, err
	}
	if job == nil {
		return false, nil
	}

	if err := w.ProcessJob(ctx, job); err != nil {
		deadLettered, failErr := w.queue.Fail(ctx, job.ID, err)
		if failErr != nil {
			w.logger.Error("ingest fail transition error", "job_id", job.ID, "err", failErr)
		} else if deadLettered {
			w.logger.Warn("ingest job dead-lettered", "job_id", job.ID, "source_type", job.SourceType, "source_id", job.SourceID, "err", err)
		}
		return true, nil
	}

	if err := w.queue.MarkDone(ctx, job.ID); err != nil {
		w.logger.Error("ingest mark done error", "job_id", job.ID, "err", err)
		return true, err
	}
	return true, nil
}

type loadedSource struct {
	projectID  int64
	projectKey string
	sourceType model.IngestSourceType
	sourceID   int64
	keyOrName  string
	kind       string
	title      string
	content    string
	tags       []string
	refs       []string
	archived   bool
}

// ProcessJob executes the 4-stage pipeline (Normalize -> Semantic Chunk ->
// Graph Extract -> Embed) for a single claimed ingest job.
func (w *Worker) ProcessJob(ctx context.Context, job *model.IngestJob) error {
	src, found, err := w.loadSource(ctx, job.SourceType, job.SourceID)
	if err != nil {
		return fmt.Errorf("load source %s/%d: %w", job.SourceType, job.SourceID, err)
	}
	if !found {
		// Source row no longer exists; clean any leftover projection chunks.
		_, err := w.pool.Exec(ctx,
			`DELETE FROM rag_chunk WHERE source_type = $1 AND source_id = $2`,
			string(job.SourceType), job.SourceID,
		)
		return err
	}

	// Stage 1: Normalize raw content into normalized_artifact (never mutating source tables).
	normOut, err := w.normalizer.Normalize(ctx, pg.NormalizeInput{
		SourceType: src.sourceType,
		ProjectKey: src.projectKey,
		KeyOrName:  src.keyOrName,
		Kind:       src.kind,
		Title:      src.title,
		Content:    src.content,
	})
	if err != nil {
		return fmt.Errorf("normalize %s/%d: %w", src.sourceType, src.sourceID, err)
	}

	artifactID, err := UpsertNormalizedArtifact(
		ctx, w.pool,
		src.sourceType, src.sourceID, job.SourceSHA256,
		w.normalizer.PipelineVersion(), normOut,
	)
	if err != nil {
		return fmt.Errorf("persist normalized_artifact: %w", err)
	}
	if job.Stage == model.IngestStageNormalize {
		return nil
	}

	// Stage 2: Heading-aware semantic chunking with breadcrumb headers.
	chunks := w.chunker.Chunk(ChunkInput{
		Title:      normOut.NormalizedTitle,
		ProjectKey: src.projectKey,
		Kind:       src.kind,
		Body:       normOut.NormalizedBody,
		Summary:    normOut.Summary,
	})

	// Stage 3: Graph edge extraction (explicit refs + inferred about/related + candidate supersedes).
	if job.Stage == model.IngestStageAll || job.Stage == model.IngestStageIndex {
		if nodeType, ok := ingestSourceToGraphNode(src.sourceType); ok {
			if _, err := w.extractor.ExtractAndStore(ctx, graph.ExtractSource{
				ProjectID:  src.projectID,
				ProjectKey: src.projectKey,
				SourceType: nodeType,
				SourceID:   src.sourceID,
				KeyOrName:  src.keyOrName,
				Kind:       src.kind,
				Title:      normOut.NormalizedTitle,
				Body:       src.content,
				Tags:       src.tags,
				Refs:       src.refs,
			}); err != nil {
				return fmt.Errorf("extract graph edges: %w", err)
			}
		}
	}

	// Stage 4: External embedding generation OUTSIDE the DB write transaction,
	// followed by atomic chunk replacement + embedding write inside one tx.
	var (
		activeGen  Generation
		hasVectors bool
		vectorLits []string
	)
	if w.embedder != nil && len(chunks) > 0 && job.Stage != model.IngestStageChunk {
		activeGen, err = w.generations.EnsureActiveGeneration(
			ctx, w.embedder.Provider(), w.embedder.Model(), w.embedder.Dimensions(),
		)
		if err != nil {
			return fmt.Errorf("ensure active generation: %w", err)
		}
		if w.embedder.Dimensions() != activeGen.Dimensions {
			return fmt.Errorf("%w: embedder returns %d, active generation %d requires %d",
				embed.ErrDimensionMismatch, w.embedder.Dimensions(), activeGen.ID, activeGen.Dimensions)
		}

		texts := make([]string, len(chunks))
		for i, ch := range chunks {
			texts[i] = ch.SearchText
		}
		vecs, _, err := w.embedder.EmbedDocuments(ctx, texts)
		if err != nil {
			return fmt.Errorf("embed chunks: %w", err)
		}
		if len(vecs) != len(chunks) {
			return fmt.Errorf("embedder returned %d vectors for %d chunks", len(vecs), len(chunks))
		}
		vectorLits = make([]string, len(vecs))
		for i, v := range vecs {
			lit, err := embed.FormatVectorChecked(v, activeGen.Dimensions)
			if err != nil {
				return fmt.Errorf("format chunk vector %d: %w", i, err)
			}
			vectorLits[i] = lit
		}
		hasVectors = true
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	stored, err := ReplaceChunksTx(ctx, tx, ChunkTarget{
		ProjectID:  src.projectID,
		SourceType: src.sourceType,
		SourceID:   src.sourceID,
		ArtifactID: &artifactID,
		Archived:   src.archived,
	}, chunks)
	if err != nil {
		return fmt.Errorf("replace rag_chunk: %w", err)
	}

	if hasVectors {
		for i, sc := range stored {
			if _, err := tx.Exec(ctx, `
				INSERT INTO rag_embedding (chunk_id, generation_id, embedding)
				VALUES ($1, $2, $3::vector)
				ON CONFLICT (chunk_id, generation_id)
				DO UPDATE SET embedding = EXCLUDED.embedding`,
				sc.ID, activeGen.ID, vectorLits[i],
			); err != nil {
				return fmt.Errorf("insert rag_embedding for chunk %d: %w", sc.ID, err)
			}
		}
	}

	return tx.Commit(ctx)
}

func (w *Worker) loadSource(ctx context.Context, sourceType model.IngestSourceType, sourceID int64) (loadedSource, bool, error) {
	switch sourceType {
	case model.IngestSourceIssue:
		var (
			src    loadedSource
			status string
		)
		src.sourceType = sourceType
		src.sourceID = sourceID
		err := w.pool.QueryRow(ctx, `
			SELECT i.project_id, p.key, i.key, i.type, i.title, i.body, i.status
			FROM issue i
			JOIN project p ON p.id = i.project_id
			WHERE i.id = $1`,
			sourceID,
		).Scan(&src.projectID, &src.projectKey, &src.keyOrName, &src.kind, &src.title, &src.content, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return loadedSource{}, false, nil
		}
		if err != nil {
			return loadedSource{}, false, err
		}
		src.archived = status == string(model.StatusDeleted)

		lRows, err := w.pool.Query(ctx, `SELECT label FROM issue_label WHERE issue_id = $1 ORDER BY label`, sourceID)
		if err != nil {
			return loadedSource{}, false, err
		}
		for lRows.Next() {
			var l string
			if err := lRows.Scan(&l); err != nil {
				lRows.Close()
				return loadedSource{}, false, err
			}
			src.tags = append(src.tags, l)
		}
		lRows.Close()
		if err := lRows.Err(); err != nil {
			return loadedSource{}, false, err
		}

		rRows, err := w.pool.Query(ctx, `SELECT ref FROM issue_ref WHERE issue_id = $1 ORDER BY ref`, sourceID)
		if err != nil {
			return loadedSource{}, false, err
		}
		for rRows.Next() {
			var r string
			if err := rRows.Scan(&r); err != nil {
				rRows.Close()
				return loadedSource{}, false, err
			}
			src.refs = append(src.refs, r)
		}
		rRows.Close()
		if err := rRows.Err(); err != nil {
			return loadedSource{}, false, err
		}
		return src, true, nil

	case model.IngestSourceActivity:
		var (
			src         loadedSource
			body        *string
			summary     *string
			commentKind *string
			issueStatus string
		)
		src.sourceType = sourceType
		src.sourceID = sourceID
		err := w.pool.QueryRow(ctx, `
			SELECT i.project_id, p.key, i.key, i.title, i.status, a.body, a.summary, a.comment_kind
			FROM activity a
			JOIN issue i ON i.id = a.issue_id
			JOIN project p ON p.id = i.project_id
			WHERE a.id = $1`,
			sourceID,
		).Scan(&src.projectID, &src.projectKey, &src.keyOrName, &src.title, &issueStatus, &body, &summary, &commentKind)
		if errors.Is(err, pgx.ErrNoRows) {
			return loadedSource{}, false, nil
		}
		if err != nil {
			return loadedSource{}, false, err
		}
		src.kind = "comment"
		if commentKind != nil && *commentKind != "" {
			src.kind = *commentKind
		}
		if body != nil {
			src.content = *body
		}
		if summary != nil && strings.TrimSpace(*summary) != "" && src.content == "" {
			src.content = *summary
		}
		src.archived = issueStatus == string(model.StatusDeleted)
		return src, true, nil

	case model.IngestSourceSpec:
		var (
			src     loadedSource
			tagsRaw []byte
		)
		src.sourceType = sourceType
		src.sourceID = sourceID
		err := w.pool.QueryRow(ctx, `
			SELECT sd.project_id, p.key, sd.slug, sd.kind, sd.title, sd.tags, sd.archived,
			       COALESCE(sr.content, '')
			FROM spec_document sd
			JOIN project p ON p.id = sd.project_id
			LEFT JOIN spec_revision sr ON sr.id = sd.current_revision_id
			WHERE sd.id = $1`,
			sourceID,
		).Scan(&src.projectID, &src.projectKey, &src.keyOrName, &src.kind, &src.title, &tagsRaw, &src.archived, &src.content)
		if errors.Is(err, pgx.ErrNoRows) {
			return loadedSource{}, false, nil
		}
		if err != nil {
			return loadedSource{}, false, err
		}
		src.tags = parseJSONStringArray(tagsRaw)
		return src, true, nil

	case model.IngestSourceRecord:
		var (
			src     loadedSource
			tagsRaw []byte
		)
		src.sourceType = sourceType
		src.sourceID = sourceID
		err := w.pool.QueryRow(ctx, `
			SELECT rd.project_id, p.key, rd.name, rd.kind, rd.title, rd.tags, rd.archived, rd.content
			FROM record_document rd
			JOIN project p ON p.id = rd.project_id
			WHERE rd.id = $1`,
			sourceID,
		).Scan(&src.projectID, &src.projectKey, &src.keyOrName, &src.kind, &src.title, &tagsRaw, &src.archived, &src.content)
		if errors.Is(err, pgx.ErrNoRows) {
			return loadedSource{}, false, nil
		}
		if err != nil {
			return loadedSource{}, false, err
		}
		src.tags = parseJSONStringArray(tagsRaw)
		return src, true, nil

	default:
		return loadedSource{}, false, fmt.Errorf("unsupported ingest source type %q", sourceType)
	}
}

func parseJSONStringArray(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
