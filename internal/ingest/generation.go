package ingest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/embed"
	"github.com/wsk0715/agent-board/internal/store/pg"
)

// Embedding generation lifecycle states matching the embedding_generation DDL.
const (
	GenerationBuilding = "building"
	GenerationActive   = "active"
	GenerationRetiring = "retiring"
	GenerationRetired  = "retired"

	DefaultBackfillBatchSize = 32
)

// Generation represents one row of embedding_generation.
type Generation struct {
	ID          int64
	Provider    string
	Model       string
	Dimensions  int
	State       string
	ActivatedAt *time.Time
	CreatedAt   time.Time
}

// BackfillStats reports progress of a generation backfill run.
type BackfillStats struct {
	GenerationID   int64
	ChunksEmbedded int
	Batches        int
	TotalTokens    int
}

// GenerationManager orchestrates zero-downtime blue-green embedding generation
// creation, backfill, atomic activation, and retired cleanup.
type GenerationManager struct {
	pool *pgxpool.Pool
}

// NewGenerationManager constructs a GenerationManager backed by pool.
func NewGenerationManager(pool *pgxpool.Pool) *GenerationManager {
	return &GenerationManager{pool: pool}
}

// GetActiveGeneration returns the single active embedding generation, or
// pg.ErrNotFound if none has been activated yet.
func (m *GenerationManager) GetActiveGeneration(ctx context.Context) (Generation, error) {
	row := m.pool.QueryRow(ctx, `
		SELECT id, provider, model, dimensions, state, activated_at, created_at
		FROM embedding_generation
		WHERE state = 'active'
		LIMIT 1`)
	gen, err := scanGeneration(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Generation{}, pg.ErrNotFound
	}
	return gen, err
}

// EnsureActiveGeneration returns the current active generation if one exists,
// or creates and activates an initial generation with (provider, modelName, dimensions).
func (m *GenerationManager) EnsureActiveGeneration(ctx context.Context, provider, modelName string, dimensions int) (Generation, error) {
	if gen, err := m.GetActiveGeneration(ctx); err == nil {
		return gen, nil
	} else if !errors.Is(err, pg.ErrNotFound) {
		return Generation{}, err
	}

	if err := validateGenerationParams(provider, modelName, dimensions); err != nil {
		return Generation{}, err
	}

	row := m.pool.QueryRow(ctx, `
		INSERT INTO embedding_generation (provider, model, dimensions, state, activated_at)
		VALUES ($1, $2, $3, 'active', now())
		RETURNING id, provider, model, dimensions, state, activated_at, created_at`,
		strings.TrimSpace(provider), strings.TrimSpace(modelName), dimensions,
	)
	gen, err := scanGeneration(row)
	if err == nil {
		return gen, nil
	}
	// Concurrent startup race on idx_embedding_generation_single_active.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return m.GetActiveGeneration(ctx)
	}
	return Generation{}, err
}

// StartBuildingGeneration inserts a new generation in state='building' without
// affecting the currently active serving generation.
func (m *GenerationManager) StartBuildingGeneration(ctx context.Context, provider, modelName string, dimensions int) (Generation, error) {
	if err := validateGenerationParams(provider, modelName, dimensions); err != nil {
		return Generation{}, err
	}
	row := m.pool.QueryRow(ctx, `
		INSERT INTO embedding_generation (provider, model, dimensions, state)
		VALUES ($1, $2, $3, 'building')
		RETURNING id, provider, model, dimensions, state, activated_at, created_at`,
		strings.TrimSpace(provider), strings.TrimSpace(modelName), dimensions,
	)
	return scanGeneration(row)
}

// BackfillGeneration embeds every rag_chunk missing an embedding for
// generationID using embedder, writing batches into rag_embedding while the
// existing active generation remains online and untouched.
func (m *GenerationManager) BackfillGeneration(ctx context.Context, generationID int64, embedder pg.Embedder, batchSize int) (BackfillStats, error) {
	if embedder == nil {
		return BackfillStats{}, errors.New("embedder is required for backfill")
	}
	if batchSize <= 0 {
		batchSize = DefaultBackfillBatchSize
	}

	gen, err := m.getGenerationByID(ctx, generationID)
	if err != nil {
		return BackfillStats{}, err
	}
	if gen.State != GenerationBuilding && gen.State != GenerationActive {
		return BackfillStats{}, fmt.Errorf("generation %d is in state %q and cannot be backfilled", generationID, gen.State)
	}
	if embedder.Dimensions() != gen.Dimensions {
		return BackfillStats{}, fmt.Errorf("%w: embedder returns %d, generation %d requires %d",
			embed.ErrDimensionMismatch, embedder.Dimensions(), generationID, gen.Dimensions)
	}

	stats := BackfillStats{GenerationID: generationID}
	var lastChunkID int64

	for {
		rows, err := m.pool.Query(ctx, `
			SELECT c.id, c.search_text
			FROM rag_chunk c
			LEFT JOIN rag_embedding e
			       ON e.chunk_id = c.id AND e.generation_id = $1
			WHERE e.chunk_id IS NULL
			  AND c.id > $2
			ORDER BY c.id ASC
			LIMIT $3`,
			generationID, lastChunkID, batchSize,
		)
		if err != nil {
			return stats, err
		}

		type pendingChunk struct {
			id   int64
			text string
		}
		var batch []pendingChunk
		for rows.Next() {
			var pc pendingChunk
			if err := rows.Scan(&pc.id, &pc.text); err != nil {
				rows.Close()
				return stats, err
			}
			batch = append(batch, pc)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return stats, err
		}
		if len(batch) == 0 {
			break
		}

		texts := make([]string, len(batch))
		for i, pc := range batch {
			texts[i] = pc.text
			lastChunkID = pc.id
		}

		vecs, usage, err := embedder.EmbedDocuments(ctx, texts)
		if err != nil {
			return stats, err
		}
		if len(vecs) != len(batch) {
			return stats, fmt.Errorf("embedder returned %d vectors for %d chunks", len(vecs), len(batch))
		}

		tx, err := m.pool.Begin(ctx)
		if err != nil {
			return stats, err
		}
		batchEmbedded := 0
		for i, pc := range batch {
			lit, err := embed.FormatVectorChecked(vecs[i], gen.Dimensions)
			if err != nil {
				_ = tx.Rollback(ctx)
				return stats, err
			}
			tag, err := tx.Exec(ctx, `
				INSERT INTO rag_embedding (chunk_id, generation_id, embedding)
				SELECT $1, $2, $3::vector
				WHERE EXISTS (SELECT 1 FROM rag_chunk WHERE id = $1)
				ON CONFLICT (chunk_id, generation_id)
				DO UPDATE SET embedding = EXCLUDED.embedding`,
				pc.id, generationID, lit,
			)
			if err != nil {
				_ = tx.Rollback(ctx)
				return stats, err
			}
			batchEmbedded += int(tag.RowsAffected())
		}
		if err := tx.Commit(ctx); err != nil {
			return stats, err
		}

		stats.ChunksEmbedded += batchEmbedded
		stats.Batches++
		stats.TotalTokens += usage.TotalTokens
	}

	return stats, nil
}

// ActivateGeneration atomically retires the current active generation (if any)
// and promotes generationID from building to active inside a single transaction.
func (m *GenerationManager) ActivateGeneration(ctx context.Context, generationID int64) (Generation, error) {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return Generation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the target generation row first.
	target, err := scanGeneration(tx.QueryRow(ctx, `
		SELECT id, provider, model, dimensions, state, activated_at, created_at
		FROM embedding_generation
		WHERE id = $1
		FOR UPDATE`,
		generationID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Generation{}, pg.ErrNotFound
	}
	if err != nil {
		return Generation{}, err
	}
	if target.State == GenerationActive {
		if err := tx.Commit(ctx); err != nil {
			return Generation{}, err
		}
		return target, nil
	}
	if target.State != GenerationBuilding {
		return Generation{}, fmt.Errorf("generation %d cannot be activated from state %q", generationID, target.State)
	}

	// Retire the currently active generation inside the same transaction before
	// activating the new generation to satisfy idx_embedding_generation_single_active.
	if _, err := tx.Exec(ctx, `
		UPDATE embedding_generation
		SET state = 'retired'
		WHERE state = 'active' AND id <> $1`,
		generationID,
	); err != nil {
		return Generation{}, err
	}

	activated, err := scanGeneration(tx.QueryRow(ctx, `
		UPDATE embedding_generation
		SET state = 'active',
		    activated_at = now()
		WHERE id = $1
		RETURNING id, provider, model, dimensions, state, activated_at, created_at`,
		generationID,
	))
	if err != nil {
		return Generation{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Generation{}, err
	}
	return activated, nil
}

// PruneRetired deletes rag_embedding and query_embedding_cache rows belonging to
// retired generations and returns the number of pruned embedding rows.
func (m *GenerationManager) PruneRetired(ctx context.Context) (int64, error) {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		DELETE FROM query_embedding_cache
		WHERE generation_id IN (SELECT id FROM embedding_generation WHERE state = 'retired')`); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `
		DELETE FROM rag_embedding
		WHERE generation_id IN (SELECT id FROM embedding_generation WHERE state = 'retired')`)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (m *GenerationManager) getGenerationByID(ctx context.Context, id int64) (Generation, error) {
	row := m.pool.QueryRow(ctx, `
		SELECT id, provider, model, dimensions, state, activated_at, created_at
		FROM embedding_generation
		WHERE id = $1`,
		id,
	)
	gen, err := scanGeneration(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Generation{}, pg.ErrNotFound
	}
	return gen, err
}

func validateGenerationParams(provider, modelName string, dimensions int) error {
	if strings.TrimSpace(provider) == "" {
		return errors.New("embedding provider is required")
	}
	if strings.TrimSpace(modelName) == "" {
		return errors.New("embedding model is required")
	}
	if dimensions <= 0 {
		return fmt.Errorf("%w: dimensions must be positive, got %d", embed.ErrDimensionMismatch, dimensions)
	}
	return nil
}

type generationScanner interface {
	Scan(dest ...any) error
}

func scanGeneration(row generationScanner) (Generation, error) {
	var g Generation
	err := row.Scan(&g.ID, &g.Provider, &g.Model, &g.Dimensions, &g.State, &g.ActivatedAt, &g.CreatedAt)
	return g, err
}
