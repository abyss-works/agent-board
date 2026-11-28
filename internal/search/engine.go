package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/embed"
	"github.com/wsk0715/agent-board/internal/graph"
	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store/pg"
)

// Default retrieval, fusion, reranking, and packet assembly limits.
const (
	DefaultMaxResults         = 3
	MaxQueryResults           = 10
	MaxQueryRunes             = 4096
	MaxExpandHops             = 2
	DefaultCutoffThreshold    = 0.15
	DefaultRerankTimeout      = 1500 * time.Millisecond
	DefaultEmbedTimeout       = 5 * time.Second
	DefaultRRFK               = 60.0
	channelCandidateLimit     = 40
	rrfCandidatePoolLimit     = 25
	defaultNeighborContentCap = 4096
	defaultItemContentCap     = 32768
	defaultItemSummaryCap     = 320
)

// Compile-time check that Engine implements pg.ContextEngine.
var _ pg.ContextEngine = (*Engine)(nil)

// EngineConfig configures the single-call context query engine.
type EngineConfig struct {
	Pool            *pgxpool.Pool
	GraphStore      *graph.Store
	Embedder        pg.Embedder
	Reranker        pg.Reranker
	CutoffThreshold float64
	RerankTimeout   time.Duration
	EmbedTimeout    time.Duration
	Logger          *slog.Logger
}

// Engine executes hybrid PGroonga + pgvector HNSW retrieval, RRF fusion,
// reranking with timeout fallback, confidence cutoff, and bidirectional graph
// packet assembly in a single call.
type Engine struct {
	pool            *pgxpool.Pool
	graphStore      *graph.Store
	embedder        pg.Embedder
	reranker        pg.Reranker
	cutoffThreshold float64
	rerankTimeout   time.Duration
	embedTimeout    time.Duration
	logger          *slog.Logger
}

// NewEngine constructs an Engine with safe defaults.
func NewEngine(cfg EngineConfig) *Engine {
	gs := cfg.GraphStore
	if gs == nil && cfg.Pool != nil {
		gs = graph.NewStore(cfg.Pool)
	}
	cutoff := cfg.CutoffThreshold
	if cutoff <= 0 || cutoff > 1 || math.IsNaN(cutoff) {
		cutoff = DefaultCutoffThreshold
	}
	rerankTimeout := cfg.RerankTimeout
	if rerankTimeout <= 0 {
		rerankTimeout = DefaultRerankTimeout
	}
	embedTimeout := cfg.EmbedTimeout
	if embedTimeout <= 0 {
		embedTimeout = DefaultEmbedTimeout
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		pool:            cfg.Pool,
		graphStore:      gs,
		embedder:        cfg.Embedder,
		reranker:        cfg.Reranker,
		cutoffThreshold: cutoff,
		rerankTimeout:   rerankTimeout,
		embedTimeout:    embedTimeout,
		logger:          logger,
	}
}

type sourceKey struct {
	sourceType string
	sourceID   int64
}

type chunkHit struct {
	chunkID     int64
	projectID   int64
	sourceType  string
	sourceID    int64
	chunkIndex  int
	sectionPath string
	chunkText   string
	searchText  string
	rawScore    float64
}

type fusedCandidate struct {
	key         sourceKey
	projectID   int64
	chunkText   string
	searchText  string
	sectionPath string
	ftsRank     int
	vecRank     int
	score       float64
	rrfScore    float64
}

// Query executes the end-to-end context retrieval pipeline.
func (e *Engine) Query(ctx context.Context, req pg.ContextQueryRequest) (pg.ContextQueryResponse, error) {
	totalStart := time.Now()

	validated, err := e.validateRequest(req)
	if err != nil {
		return pg.ContextQueryResponse{}, err
	}

	projectIDs, err := e.resolveProjectScope(ctx, validated.project, validated.includeGlobal)
	if err != nil {
		return pg.ContextQueryResponse{}, err
	}

	resp := pg.ContextQueryResponse{
		Query:           req.Query,
		CutoffThreshold: validated.cutoffThreshold,
		FallbackMode:    "none",
		Items:           []pg.ContextItem{},
	}
	if len(projectIDs) == 0 {
		resp.TimingMS.Total = msSince(totalStart)
		return resp, nil
	}

	// Stage 1: Query embedding cache lookup (or embed + upsert on miss).
	embedStart := time.Now()
	normQuery := NormalizeQuery(req.Query)
	activeGenID, queryVecLit, cacheHit, embedFailed := e.resolveQueryEmbedding(ctx, normQuery)
	resp.CacheHit = cacheHit
	resp.TimingMS.Embed = msSince(embedStart)
	if embedFailed {
		resp.FallbackMode = "fts_only"
		resp.FallbackUsed = true
	}

	// Stage 2: Hybrid candidate retrieval (PGroonga + pgvector HNSW) and RRF fusion.
	dbStart := time.Now()
	ftsHits, err := e.searchPGroonga(ctx, req.Query, projectIDs, validated.sourceTypes, validated.includeArchived, validated.includeSuperseded)
	if err != nil {
		return pg.ContextQueryResponse{}, fmt.Errorf("pgroonga search: %w", err)
	}

	var vecHits []chunkHit
	if activeGenID > 0 && queryVecLit != "" {
		vecHits, err = e.searchVectorHNSW(ctx, activeGenID, queryVecLit, projectIDs, validated.sourceTypes, validated.includeArchived, validated.includeSuperseded)
		if err != nil {
			// Vector search failure degrades cleanly to FTS-only rather than failing the request.
			e.logger.Warn("vector search fallback to fts_only", "err", err)
			resp.FallbackMode = "fts_only"
			resp.FallbackUsed = true
		}
	}

	candidates := fuseRRF(ftsHits, vecHits, rrfCandidatePoolLimit)
	dbElapsed := msSince(dbStart)

	if len(candidates) == 0 {
		resp.TimingMS.DB = dbElapsed
		resp.TimingMS.Total = msSince(totalStart)
		return resp, nil
	}

	// Stage 3: Dedicated cross-encoder reranker with bounded timeout and RRF fallback.
	rerankStart := time.Now()
	if e.reranker != nil {
		rerankedCandidates, rErr := e.rerankWithTimeout(ctx, req.Query, candidates)
		if rErr != nil {
			if ctx.Err() != nil {
				return pg.ContextQueryResponse{}, ctx.Err()
			}
			e.logger.Warn("reranker fallback to rrf", "err", rErr)
			resp.Reranked = false
			resp.FallbackUsed = true
			resp.FallbackMode = "rrf"
		} else if len(rerankedCandidates) > 0 {
			candidates = rerankedCandidates
			resp.Reranked = true
		}
	}
	resp.TimingMS.Rerank = msSince(rerankStart)

	// Stage 4: Confidence cutoff (gap between top-1 and top-2 >= cutoff threshold).
	if len(candidates) >= 2 {
		gap := candidates[0].score - candidates[1].score
		if gap < 0 {
			gap = 0
		}
		gap = round6(gap)
		resp.ScoreGap12 = gap
		resp.ConfidenceGap = gap
	}
	selected := candidates
	if resp.Reranked && len(candidates) >= 2 && resp.ScoreGap12 >= validated.cutoffThreshold {
		resp.CutoffApplied = true
		selected = candidates[:1]
	} else if len(selected) > validated.limit {
		selected = selected[:validated.limit]
	}

	// Stage 5: Hydrate item metadata/content, bidirectional neighbors, and supersedes chains.
	hydrateStart := time.Now()
	items, err := e.assemblePacket(ctx, selected, validated.expandHops, validated.includeSuperseded)
	if err != nil {
		return pg.ContextQueryResponse{}, fmt.Errorf("assemble context packet: %w", err)
	}
	resp.TimingMS.DB = round3(dbElapsed + msSince(hydrateStart))
	resp.Items = items
	resp.TimingMS.Total = msSince(totalStart)
	return resp, nil
}

type validatedQuery struct {
	project           string
	includeGlobal     bool
	sourceTypes       []string
	limit             int
	cutoffThreshold   float64
	expandHops        int
	includeArchived   bool
	includeSuperseded bool
}

func (e *Engine) validateRequest(req pg.ContextQueryRequest) (validatedQuery, error) {
	var verr model.ValidationError

	if !utf8.ValidString(req.Query) {
		verr.Fields = append(verr.Fields, model.FieldError{
			Field:   "query",
			Reason:  "invalid_utf8",
			Message: "query는 유효한 UTF-8 문자열이어야 합니다.",
		})
	} else if strings.IndexByte(req.Query, 0) >= 0 {
		verr.Fields = append(verr.Fields, model.FieldError{
			Field:   "query",
			Reason:  "invalid_value",
			Message: "query에는 NUL 바이트를 포함할 수 없습니다.",
		})
	} else if strings.TrimSpace(req.Query) == "" {
		verr.Fields = append(verr.Fields, model.FieldError{
			Field:   "query",
			Reason:  "required",
			Message: "query는 비어 있을 수 없습니다.",
		})
	} else if utf8.RuneCountInString(req.Query) > MaxQueryRunes {
		verr.Fields = append(verr.Fields, model.FieldError{
			Field:   "query",
			Reason:  "too_long",
			Message: fmt.Sprintf("query는 %d자를 넘을 수 없습니다.", MaxQueryRunes),
		})
	}

	project := strings.TrimSpace(req.Project)
	if project != "" {
		project = model.NormalizeProjectKey(project)
		if !model.ValidProjectKey(project) {
			verr.Fields = append(verr.Fields, model.FieldError{
				Field:   "project",
				Reason:  "invalid_value",
				Message: "프로젝트 키 형식이 아닙니다.",
			})
		}
	}

	limit := req.MaxResults
	if limit == 0 {
		limit = DefaultMaxResults
	} else if limit < 0 || limit > MaxQueryResults {
		verr.Fields = append(verr.Fields, model.FieldError{
			Field:   "max_results",
			Reason:  "invalid_value",
			Message: fmt.Sprintf("1~%d 사이의 정수여야 합니다.", MaxQueryResults),
		})
	}

	expandHops := int(req.ExpandHops)
	if expandHops < 0 || expandHops > MaxExpandHops {
		verr.Fields = append(verr.Fields, model.FieldError{
			Field:   "expand_hops",
			Reason:  "invalid_value",
			Message: fmt.Sprintf("0~%d 사이의 정수여야 합니다.", MaxExpandHops),
		})
	}

	cutoff := e.cutoffThreshold
	if req.ConfidenceCutoff != nil {
		c := *req.ConfidenceCutoff
		if math.IsNaN(c) || math.IsInf(c, 0) || c < 0 || c > 1 {
			verr.Fields = append(verr.Fields, model.FieldError{
				Field:   "confidence_cutoff",
				Reason:  "invalid_value",
				Message: "0.0~1.0 사이의 실수여야 합니다.",
			})
		} else {
			cutoff = c
		}
	}

	var sourceTypes []string
	if len(req.SourceTypes) > 0 {
		seen := make(map[string]bool, len(req.SourceTypes))
		for _, st := range req.SourceTypes {
			st = strings.TrimSpace(st)
			switch model.IngestSourceType(st) {
			case model.IngestSourceIssue, model.IngestSourceActivity, model.IngestSourceSpec, model.IngestSourceRecord:
				if !seen[st] {
					seen[st] = true
					sourceTypes = append(sourceTypes, st)
				}
			default:
				verr.Fields = append(verr.Fields, model.FieldError{
					Field:   "source_types",
					Reason:  "invalid_value",
					Message: fmt.Sprintf("%q는 허용되지 않는 source_type입니다.", st),
				})
			}
		}
	}

	if len(verr.Fields) > 0 {
		return validatedQuery{}, &verr
	}

	includeGlobal := true
	if req.IncludeGlobal != nil {
		includeGlobal = *req.IncludeGlobal
	}

	return validatedQuery{
		project:           project,
		includeGlobal:     includeGlobal,
		sourceTypes:       sourceTypes,
		limit:             limit,
		cutoffThreshold:   cutoff,
		expandHops:        expandHops,
		includeArchived:   req.IncludeArchived,
		includeSuperseded: req.IncludeSuperseded,
	}, nil
}

func (e *Engine) resolveProjectScope(ctx context.Context, projectKey string, includeGlobal bool) ([]int64, error) {
	if projectKey != "" {
		var projID int64
		err := e.pool.QueryRow(ctx, `SELECT id FROM project WHERE key = $1 AND status <> 'deleted'`, projectKey).Scan(&projID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pg.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		ids := []int64{projID}
		if includeGlobal && projID != model.GlobalProjectID {
			ids = append(ids, model.GlobalProjectID)
		}
		return ids, nil
	}

	query := `SELECT id FROM project WHERE status = 'active'`
	if !includeGlobal {
		query += ` AND id <> 0`
	}
	query += ` ORDER BY id ASC`
	rows, err := e.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (e *Engine) resolveQueryEmbedding(ctx context.Context, normQuery string) (genID int64, vecLit string, cacheHit bool, embedFailed bool) {
	var dims int
	err := e.pool.QueryRow(ctx, `
		SELECT id, dimensions
		FROM embedding_generation
		WHERE state = 'active'
		LIMIT 1`,
	).Scan(&genID, &dims)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", false, false
	}
	if err != nil {
		e.logger.Warn("load active embedding generation failed", "err", err)
		return 0, "", false, true
	}

	sum := sha256.Sum256([]byte(normQuery))
	queryHash := hex.EncodeToString(sum[:])

	var cached string
	err = e.pool.QueryRow(ctx, `
		UPDATE query_embedding_cache
		SET hit_count = hit_count + 1,
		    last_used_at = now()
		WHERE query_hash = $1 AND generation_id = $2
		RETURNING embedding::text`,
		queryHash, genID,
	).Scan(&cached)
	if err == nil && cached != "" {
		return genID, cached, true, false
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		e.logger.Warn("query_embedding_cache lookup failed", "err", err)
	}

	if e.embedder == nil {
		return genID, "", false, false
	}

	embedCtx, cancel := context.WithTimeout(ctx, e.embedTimeout)
	defer cancel()

	vec, _, err := e.embedder.EmbedQuery(embedCtx, normQuery)
	if err != nil {
		e.logger.Warn("query embedding failed; falling back to fts", "err", err)
		return genID, "", false, true
	}
	lit, err := embed.FormatVectorChecked(vec, dims)
	if err != nil {
		e.logger.Warn("query embedding vector invalid; falling back to fts", "err", err)
		return genID, "", false, true
	}

	if _, uErr := e.pool.Exec(ctx, `
		INSERT INTO query_embedding_cache (query_hash, generation_id, normalized_query, embedding, hit_count, created_at, last_used_at)
		VALUES ($1, $2, $3, $4::vector, 1, now(), now())
		ON CONFLICT (query_hash, generation_id)
		DO UPDATE SET hit_count = query_embedding_cache.hit_count + 1,
		              last_used_at = now()`,
		queryHash, genID, normQuery, lit,
	); uErr != nil {
		e.logger.Warn("query_embedding_cache upsert failed", "err", uErr)
	}
	return genID, lit, false, false
}

func (e *Engine) searchPGroonga(
	ctx context.Context,
	rawQuery string,
	projectIDs []int64,
	sourceTypes []string,
	includeArchived bool,
	includeSuperseded bool,
) ([]chunkHit, error) {
	strictQ := BuildPGroongaQuery(rawQuery)
	if strictQ == "" {
		return nil, nil
	}

	hits, err := e.runPGroongaQuery(ctx, strictQ, projectIDs, sourceTypes, includeArchived, includeSuperseded, channelCandidateLimit)
	if err != nil {
		return nil, err
	}

	if len(hits) < rrfCandidatePoolLimit {
		relaxedQ := BuildPGroongaRelaxedQuery(rawQuery)
		if relaxedQ != "" && relaxedQ != strictQ {
			extra, err := e.runPGroongaQuery(ctx, relaxedQ, projectIDs, sourceTypes, includeArchived, includeSuperseded, channelCandidateLimit)
			if err != nil {
				return nil, err
			}
			seen := make(map[int64]bool, len(hits))
			for _, h := range hits {
				seen[h.chunkID] = true
			}
			for _, h := range extra {
				if !seen[h.chunkID] {
					seen[h.chunkID] = true
					hits = append(hits, h)
					if len(hits) >= channelCandidateLimit {
						break
					}
				}
			}
		}
	}
	return hits, nil
}

func (e *Engine) runPGroongaQuery(
	ctx context.Context,
	pgroongaQuery string,
	projectIDs []int64,
	sourceTypes []string,
	includeArchived bool,
	includeSuperseded bool,
	limit int,
) ([]chunkHit, error) {
	args := make([]any, 0, len(projectIDs)+4)
	inPlaceholders := make([]string, len(projectIDs))
	for i, pid := range projectIDs {
		args = append(args, pid)
		inPlaceholders[i] = fmt.Sprintf("$%d", len(args))
	}

	conds := make([]string, 0, 5)
	if !includeArchived {
		conds = append(conds, "NOT c.archived")
	}
	conds = append(conds, "c.project_id IN ("+strings.Join(inPlaceholders, ", ")+")")

	args = append(args, pgroongaQuery)
	conds = append(conds, fmt.Sprintf("c.search_text &@~ $%d", len(args)))

	if len(sourceTypes) > 0 {
		args = append(args, sourceTypes)
		conds = append(conds, fmt.Sprintf("c.source_type = ANY($%d)", len(args)))
	}
	if !includeSuperseded {
		conds = append(conds, notSupersededSQL)
	}

	args = append(args, limit)
	limitArg := fmt.Sprintf("$%d", len(args))

	sql := `
		SELECT c.id, c.project_id, c.source_type, c.source_id, c.chunk_index,
		       c.section_path, c.chunk_text, c.search_text,
		       pgroonga_score(c.tableoid, c.ctid) AS score
		FROM rag_chunk c
		WHERE ` + strings.Join(conds, " AND ") + `
		ORDER BY score DESC, c.id ASC
		LIMIT ` + limitArg

	rows, err := e.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []chunkHit
	for rows.Next() {
		var h chunkHit
		if err := rows.Scan(
			&h.chunkID, &h.projectID, &h.sourceType, &h.sourceID, &h.chunkIndex,
			&h.sectionPath, &h.chunkText, &h.searchText, &h.rawScore,
		); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (e *Engine) searchVectorHNSW(
	ctx context.Context,
	genID int64,
	queryVecLit string,
	projectIDs []int64,
	sourceTypes []string,
	includeArchived bool,
	includeSuperseded bool,
) ([]chunkHit, error) {
	tx, err := e.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SET LOCAL hnsw.ef_search = 100`); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `SET LOCAL hnsw.iterative_scan = 'relaxed_order'`); err != nil {
		return nil, err
	}

	args := make([]any, 0, len(projectIDs)+4)
	args = append(args, queryVecLit, genID)

	inPlaceholders := make([]string, len(projectIDs))
	for i, pid := range projectIDs {
		args = append(args, pid)
		inPlaceholders[i] = fmt.Sprintf("$%d", len(args))
	}

	conds := []string{
		"re.generation_id = $2",
		"c.project_id IN (" + strings.Join(inPlaceholders, ", ") + ")",
	}
	if !includeArchived {
		conds = append(conds, "NOT c.archived")
	}
	if len(sourceTypes) > 0 {
		args = append(args, sourceTypes)
		conds = append(conds, fmt.Sprintf("c.source_type = ANY($%d)", len(args)))
	}
	if !includeSuperseded {
		conds = append(conds, notSupersededSQL)
	}

	args = append(args, channelCandidateLimit)
	limitArg := fmt.Sprintf("$%d", len(args))

	sql := `
		SELECT c.id, c.project_id, c.source_type, c.source_id, c.chunk_index,
		       c.section_path, c.chunk_text, c.search_text,
		       (1.0 - (re.embedding <=> $1::vector)) AS similarity
		FROM rag_embedding re
		JOIN rag_chunk c ON c.id = re.chunk_id
		WHERE ` + strings.Join(conds, " AND ") + `
		ORDER BY re.embedding <=> $1::vector ASC, c.id ASC
		LIMIT ` + limitArg

	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []chunkHit
	for rows.Next() {
		var h chunkHit
		if err := rows.Scan(
			&h.chunkID, &h.projectID, &h.sourceType, &h.sourceID, &h.chunkIndex,
			&h.sectionPath, &h.chunkText, &h.searchText, &h.rawScore,
		); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

const notSupersededSQL = `NOT EXISTS (
	SELECT 1
	FROM graph_edge se
	WHERE se.kind = 'supersedes'
	  AND se.status = 'confirmed'
	  AND se.target_type = c.source_type
	  AND se.target_id = c.source_id
)`

// fuseRRF deduplicates FTS and vector chunk hits by (source_type, source_id),
// computes reciprocal rank fusion (k=60), and returns at most poolLimit items.
func fuseRRF(ftsHits, vecHits []chunkHit, poolLimit int) []fusedCandidate {
	bySource := make(map[sourceKey]*fusedCandidate)

	addChannel := func(hits []chunkHit, isVec bool) {
		seen := make(map[sourceKey]bool, len(hits))
		rank := 0
		for _, h := range hits {
			sk := sourceKey{sourceType: h.sourceType, sourceID: h.sourceID}
			if seen[sk] {
				continue
			}
			seen[sk] = true
			rank++

			fc, exists := bySource[sk]
			if !exists {
				fc = &fusedCandidate{
					key:         sk,
					projectID:   h.projectID,
					chunkText:   h.chunkText,
					searchText:  h.searchText,
					sectionPath: h.sectionPath,
				}
				bySource[sk] = fc
			}
			if isVec {
				fc.vecRank = rank
			} else {
				fc.ftsRank = rank
			}
			fc.rrfScore += 1.0 / (DefaultRRFK + float64(rank))
		}
	}

	addChannel(ftsHits, false)
	addChannel(vecHits, true)

	out := make([]fusedCandidate, 0, len(bySource))
	for _, fc := range bySource {
		fc.score = round6(fc.rrfScore)
		out = append(out, *fc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].rrfScore != out[j].rrfScore {
			return out[i].rrfScore > out[j].rrfScore
		}
		if out[i].key.sourceType != out[j].key.sourceType {
			return out[i].key.sourceType < out[j].key.sourceType
		}
		return out[i].key.sourceID < out[j].key.sourceID
	})
	if poolLimit > 0 && len(out) > poolLimit {
		out = out[:poolLimit]
	}
	return out
}

func (e *Engine) rerankWithTimeout(ctx context.Context, query string, candidates []fusedCandidate) ([]fusedCandidate, error) {
	rerankCtx, cancel := context.WithTimeout(ctx, e.rerankTimeout)
	defer cancel()

	docs := make([]pg.RerankDocument, len(candidates))
	for i, c := range candidates {
		docs[i] = pg.RerankDocument{
			ID:   fmt.Sprintf("%s:%d", c.key.sourceType, c.key.sourceID),
			Text: c.searchText,
		}
	}

	results, _, err := e.reranker.Rerank(rerankCtx, query, docs, len(docs))
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, errors.New("reranker returned empty results")
	}

	reordered := make([]fusedCandidate, 0, len(results))
	seen := make(map[int]bool, len(results))
	for _, r := range results {
		if r.Index < 0 || r.Index >= len(candidates) || seen[r.Index] {
			continue
		}
		seen[r.Index] = true
		c := candidates[r.Index]
		c.score = round6(r.Score)
		reordered = append(reordered, c)
	}
	if len(reordered) == 0 {
		return nil, errors.New("reranker returned no valid indices")
	}
	sort.SliceStable(reordered, func(i, j int) bool {
		return reordered[i].score > reordered[j].score
	})
	return reordered, nil
}

func (e *Engine) assemblePacket(
	ctx context.Context,
	selected []fusedCandidate,
	expandHops int,
	includeSuperseded bool,
) ([]pg.ContextItem, error) {
	items := make([]pg.ContextItem, 0, len(selected))
	var seeds []pg.NodeRef

	for _, c := range selected {
		item, err := e.hydrateSource(ctx, c)
		if err != nil {
			return nil, err
		}
		item.Score = c.score
		item.Neighbors = []pg.NeighborSummary{}
		items = append(items, item)

		if _, ok := model.ParseGraphNodeType(string(item.NodeType)); ok {
			seeds = append(seeds, pg.NodeRef{Type: item.NodeType, ID: item.NodeID})
		}
	}

	if e.graphStore != nil && len(seeds) > 0 {
		maxHops := 1
		includeContent := false
		if expandHops >= 1 {
			includeContent = true
			maxHops = min(expandHops, MaxExpandHops)
		}
		neighborMap, err := e.graphStore.Expand1Hop(ctx, seeds, pg.ExpandOptions{
			IncludeContent:  includeContent,
			MaxHops:         maxHops,
			MaxPerSeed:      64,
			MaxContentRunes: defaultNeighborContentCap,
		})
		if err != nil {
			return nil, err
		}
		for i := range items {
			ref := pg.NodeRef{Type: items[i].NodeType, ID: items[i].NodeID}
			if ns, ok := neighborMap[ref]; ok && ns != nil {
				items[i].Neighbors = ns
			}
			if includeSuperseded {
				if _, ok := model.ParseGraphNodeType(string(items[i].NodeType)); ok {
					sup, err := e.graphStore.LatestSuperseding(ctx, items[i].NodeType, items[i].NodeID)
					if err != nil {
						return nil, err
					}
					items[i].SupersededBy = sup
				}
			}
		}
	}

	return items, nil
}

func (e *Engine) hydrateSource(ctx context.Context, c fusedCandidate) (pg.ContextItem, error) {
	item := pg.ContextItem{
		NodeType: model.GraphNodeType(c.key.sourceType),
		NodeID:   c.key.sourceID,
	}

	var normSummary string
	_ = e.pool.QueryRow(ctx, `
		SELECT summary
		FROM normalized_artifact
		WHERE source_type = $1 AND source_id = $2
		ORDER BY id DESC
		LIMIT 1`,
		c.key.sourceType, c.key.sourceID,
	).Scan(&normSummary)

	var fullBody string
	switch model.IngestSourceType(c.key.sourceType) {
	case model.IngestSourceIssue:
		err := e.pool.QueryRow(ctx, `
			SELECT p.key, i.key, i.type, i.title, i.body
			FROM issue i
			JOIN project p ON p.id = i.project_id
			WHERE i.id = $1`,
			c.key.sourceID,
		).Scan(&item.Project, &item.KeyOrName, &item.Kind, &item.Title, &fullBody)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return pg.ContextItem{}, err
		}
	case model.IngestSourceSpec:
		err := e.pool.QueryRow(ctx, `
			SELECT p.key, sd.slug, sd.kind, sd.title, COALESCE(sr.content, '')
			FROM spec_document sd
			JOIN project p ON p.id = sd.project_id
			LEFT JOIN spec_revision sr ON sr.id = sd.current_revision_id
			WHERE sd.id = $1`,
			c.key.sourceID,
		).Scan(&item.Project, &item.KeyOrName, &item.Kind, &item.Title, &fullBody)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return pg.ContextItem{}, err
		}
	case model.IngestSourceRecord:
		err := e.pool.QueryRow(ctx, `
			SELECT p.key, rd.name, rd.kind, CASE WHEN rd.title <> '' THEN rd.title ELSE rd.name END, rd.content
			FROM record_document rd
			JOIN project p ON p.id = rd.project_id
			WHERE rd.id = $1`,
			c.key.sourceID,
		).Scan(&item.Project, &item.KeyOrName, &item.Kind, &item.Title, &fullBody)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return pg.ContextItem{}, err
		}
	case model.IngestSourceActivity:
		var commentKind *string
		var body, summary *string
		err := e.pool.QueryRow(ctx, `
			SELECT p.key, i.key, i.title, a.comment_kind, a.body, a.summary
			FROM activity a
			JOIN issue i ON i.id = a.issue_id
			JOIN project p ON p.id = i.project_id
			WHERE a.id = $1`,
			c.key.sourceID,
		).Scan(&item.Project, &item.KeyOrName, &item.Title, &commentKind, &body, &summary)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return pg.ContextItem{}, err
		}
		item.Kind = "comment"
		if commentKind != nil && *commentKind != "" {
			item.Kind = *commentKind
		}
		if body != nil {
			fullBody = *body
		} else if summary != nil {
			fullBody = *summary
		}
	}

	if strings.TrimSpace(fullBody) != "" {
		if utf8.RuneCountInString(fullBody) <= defaultItemContentCap {
			item.Content = fullBody
		} else {
			item.Content = c.chunkText
		}
	} else {
		item.Content = c.chunkText
	}

	item.Summary = strings.TrimSpace(normSummary)
	if item.Summary == "" {
		item.Summary = truncateItemRunes(strings.Join(strings.Fields(item.Content), " "), defaultItemSummaryCap)
	}
	if item.Summary == "" {
		item.Summary = item.Title
	}
	return item, nil
}

func truncateItemRunes(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return strings.TrimSpace(string(r[:maxRunes])) + "..."
}

func msSince(start time.Time) float64 {
	return round3(float64(time.Since(start).Microseconds()) / 1000.0)
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
func round6(v float64) float64 { return math.Round(v*1000000) / 1000000 }
