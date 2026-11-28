package search

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/embed"
	"github.com/wsk0715/agent-board/internal/graph"
	"github.com/wsk0715/agent-board/internal/ingest"
	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store/pg"
)

func TestKoreanParticleStrippingAndPGroongaQueryBuilder(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"비밀번호를", "비밀번호"},
		{"훅과", "훅"},
		{"정책의", "정책"},
		{"서버에서", "서버"},
		{"토큰으로", "토큰"},
		{"키만", "키"},
		{"권한부터", "권한"},
		{"규칙까지", "규칙"},
		{"API를", "API"},
		{"PGroonga에서", "PGroonga"},
		// Batchim guard prevents false stripping of nouns.
		{"사과", "사과"},
		{"아이", "아이"},
		// Pure ASCII identifiers must stay untouched.
		{"voyage-4", "voyage-4"},
		{"hnsw.iterative_scan", "hnsw.iterative_scan"},
		{"DEMO-42", "DEMO-42"},
		{"foo_bar", "foo_bar"},
	}
	for _, tc := range cases {
		if got := StripKoreanParticle(tc.in); got != tc.want {
			t.Errorf("StripKoreanParticle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	gotQ := BuildPGroongaQuery("비밀번호를 훅과 hnsw.iterative_scan")
	wantQ := `("비밀번호를" OR "비밀번호") ("훅과" OR "훅") "hnsw.iterative_scan"`
	if gotQ != wantQ {
		t.Fatalf("BuildPGroongaQuery = %q, want %q", gotQ, wantQ)
	}

	// Special Groonga syntax characters must be safely quoted.
	escaped := BuildPGroongaQuery(`OR -drop "quote" a:b`)
	if !strings.Contains(escaped, `"OR"`) || !strings.Contains(escaped, `"-drop"`) || !strings.Contains(escaped, `"a:b"`) {
		t.Fatalf("unexpected escaped query: %q", escaped)
	}
}

func TestValidateRequestUnit(t *testing.T) {
	eng := NewEngine(EngineConfig{})

	badReqs := []struct {
		name string
		req  pg.ContextQueryRequest
	}{
		{"empty", pg.ContextQueryRequest{Query: "   "}},
		{"nul byte", pg.ContextQueryRequest{Query: "hello\x00world"}},
		{"invalid utf8", pg.ContextQueryRequest{Query: string([]byte{0xff, 0xfe})}},
		{"too long", pg.ContextQueryRequest{Query: strings.Repeat("가", MaxQueryRunes+1)}},
		{"limit too high", pg.ContextQueryRequest{Query: "ok", MaxResults: 11}},
		{"limit negative", pg.ContextQueryRequest{Query: "ok", MaxResults: -1}},
		{"expand_hops too high", pg.ContextQueryRequest{Query: "ok", ExpandHops: 3}},
		{"expand_hops negative", pg.ContextQueryRequest{Query: "ok", ExpandHops: -1}},
		{"bad source type", pg.ContextQueryRequest{Query: "ok", SourceTypes: []string{"unknown"}}},
	}
	for _, tc := range badReqs {
		if _, err := eng.validateRequest(tc.req); err == nil {
			t.Errorf("%s: expected validation error", tc.name)
		}
	}
}

var searchTestDBSeq atomic.Int64

func openSearchTestDB(t *testing.T) *pg.Store {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("AGB_TEST_PG_DSN"))
	if baseDSN == "" {
		t.Skip("AGB_TEST_PG_DSN not set")
	}
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse AGB_TEST_PG_DSN: %v", err)
	}
	dbName := fmt.Sprintf("agb_t_search_%d_%d", os.Getpid(), searchTestDBSeq.Add(1))
	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, baseDSN)
	if err != nil {
		t.Fatalf("connect admin pool: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `CREATE DATABASE `+dbName); err != nil {
		adminPool.Close()
		t.Fatalf("CREATE DATABASE %s: %v", dbName, err)
	}
	adminPool.Close()

	t.Cleanup(func() {
		p, err := pgxpool.New(context.Background(), baseDSN)
		if err != nil {
			return
		}
		defer p.Close()
		_, _ = p.Exec(context.Background(), `DROP DATABASE IF EXISTS `+dbName+` WITH (FORCE)`)
	})

	u.Path = "/" + dbName
	st, err := pg.Open(ctx, u.String(), 8)
	if err != nil {
		t.Fatalf("pg.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.EnsureActors(ctx, []model.Actor{
		{ID: "owner", Kind: model.ActorUser},
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

// deterministicEmbedder returns 1024-d unit basis vectors keyed by topic tokens
// so hybrid PGroonga + pgvector HNSW retrieval and query_embedding_cache hits
// can be verified deterministically without network access.
type deterministicEmbedder struct {
	queryCalls atomic.Int64
	docCalls   atomic.Int64
	failQuery  atomic.Bool
}

func (d *deterministicEmbedder) Provider() string { return "mock" }
func (d *deterministicEmbedder) Model() string    { return "mock-1024" }
func (d *deterministicEmbedder) Dimensions() int  { return 1024 }

func (d *deterministicEmbedder) vecFor(text string) []float32 {
	v := make([]float32, 1024)
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "비밀번호") || strings.Contains(lower, "password") || strings.Contains(lower, "credential"):
		v[0] = 1.0
	case strings.Contains(lower, "훅") || strings.Contains(lower, "hook"):
		v[1] = 1.0
	case strings.Contains(lower, "벡터") || strings.Contains(lower, "vector") || strings.Contains(lower, "hnsw"):
		v[2] = 1.0
	default:
		v[3] = 1.0
	}
	return v
}

func (d *deterministicEmbedder) EmbedDocuments(_ context.Context, texts []string) ([][]float32, pg.Usage, error) {
	d.docCalls.Add(1)
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = d.vecFor(text)
	}
	return out, pg.Usage{}, nil
}

func (d *deterministicEmbedder) EmbedQuery(_ context.Context, query string) ([]float32, pg.Usage, error) {
	d.queryCalls.Add(1)
	if d.failQuery.Load() {
		return nil, pg.Usage{}, embed.ErrUpstream
	}
	return d.vecFor(query), pg.Usage{}, nil
}

type mockReranker struct {
	fn func(ctx context.Context, query string, docs []pg.RerankDocument, topK int) ([]pg.RerankResult, pg.Usage, error)
}

func (m *mockReranker) Provider() string { return "mock" }
func (m *mockReranker) Model() string    { return "mock-rerank" }
func (m *mockReranker) Rerank(ctx context.Context, query string, docs []pg.RerankDocument, topK int) ([]pg.RerankResult, pg.Usage, error) {
	return m.fn(ctx, query, docs, topK)
}

var trSearchOwner = pg.Trace{Actor: "owner", SessionID: "sess-search", ClientHost: "devbox"}

func drainWorker(t *testing.T, ctx context.Context, w *ingest.Worker) {
	t.Helper()
	for i := 0; i < 64; i++ {
		processed, err := w.ProcessOnce(ctx)
		if err != nil {
			t.Fatalf("worker.ProcessOnce: %v", err)
		}
		if !processed {
			return
		}
	}
	t.Fatal("worker did not drain within 64 steps")
}

func TestContextEngineKoreanParticlesHybridCacheCutoffFallbackAndGraph(t *testing.T) {
	st := openSearchTestDB(t)
	ctx := context.Background()
	pool := st.Pool()
	gs := graph.NewStore(pool)
	q := ingest.NewQueue(pool, ingest.QueueConfig{})
	defer q.Close()
	emb := &deterministicEmbedder{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	worker := ingest.NewWorker(ingest.WorkerConfig{
		Pool:       pool,
		Queue:      q,
		GraphStore: gs,
		Embedder:   emb,
		Logger:     logger,
	})

	proj, err := st.CreateProject(ctx, trSearchOwner, pg.CreateProjectInput{Key: "DEMO", Name: "Demo Project"})
	if err != nil {
		t.Fatal(err)
	}

	// Seed documents:
	// 1. Password policy spec (base form "비밀번호", no particle).
	specPwd, err := st.UpsertSpec(ctx, trSearchOwner, proj.Key, "password-policy", pg.UpsertSpecInput{
		Title:   "비밀번호 저장 규칙",
		Kind:    "spec",
		Content: "# 비밀번호 저장 규칙\n비밀번호 원문은 저장하지 않고 Argon2id 해시로만 보관한다.",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 2. Hook guideline record (base form "훅", no particle).
	recHook, err := st.CreateRecord(ctx, trSearchOwner, proj.Key, pg.CreateRecordInput{
		Name:    "2026-10-01-1000-decision-hook-policy.md",
		Kind:    "decision",
		Title:   "훅 검증 규칙",
		Content: "# 훅 검증 규칙\n사전 커밋 훅 실행 시 정적 검사를 강제한다.",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 3. Vector-only semantic doc (mentions "credential", no Korean "비밀번호" text, shares vector axis 0).
	recVecOnly, err := st.CreateRecord(ctx, trSearchOwner, proj.Key, pg.CreateRecordInput{
		Name:    "2026-10-02-1000-note-credential-rotation.md",
		Kind:    "note",
		Title:   "Credential Rotation Guide",
		Content: "# Credential Rotation\nRotate secret credential hashes quarterly.",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 4. Third password-related doc for Top-3 vs cutoff verification.
	recPwdExtra, err := st.CreateRecord(ctx, trSearchOwner, proj.Key, pg.CreateRecordInput{
		Name:    "2026-10-03-1000-note-password-audit.md",
		Kind:    "note",
		Title:   "비밀번호 감사 체크리스트",
		Content: "# 비밀번호 감사\n비밀번호 복잡도와 재사용 금지 규칙을 점검한다.",
	})
	if err != nil {
		t.Fatal(err)
	}

	drainWorker(t, ctx, worker)

	// Connect specPwd <-> recHook explicitly (and cyclic related) to test bidirectional 1-hop packet.
	refPwd := pg.NodeRef{Type: model.GraphNodeSpec, ID: specPwd.Spec.ID}
	refHook := pg.NodeRef{Type: model.GraphNodeRecord, ID: recHook.Record.ID}
	if _, err := gs.UpsertExplicitEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: refPwd, Target: &refHook, Kind: model.EdgeKindRelated,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.UpsertExplicitEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: refHook, Target: &refPwd, Kind: model.EdgeKindRelated,
	}); err != nil {
		t.Fatal(err)
	}

	// ① Korean particle queries ("비밀번호를", "훅과") retrieve base-form documents via PGroonga + HNSW.
	engNoRerank := NewEngine(EngineConfig{
		Pool:       pool,
		GraphStore: gs,
		Embedder:   emb,
		Logger:     logger,
	})

	resPwd, err := engNoRerank.Query(ctx, pg.ContextQueryRequest{
		Query:   "비밀번호를",
		Project: proj.Key,
	})
	if err != nil {
		t.Fatalf("Query(비밀번호를): %v", err)
	}
	if len(resPwd.Items) < 2 {
		t.Fatalf("expected >= 2 items (FTS + vector), got %+v", resPwd.Items)
	}
	var sawSpecPwd, sawVecOnly bool
	for _, it := range resPwd.Items {
		if it.NodeType == model.GraphNodeSpec && it.NodeID == specPwd.Spec.ID {
			sawSpecPwd = true
			// Verify 1-hop summary without content by default (expand_hops=0).
			if len(it.Neighbors) == 0 {
				t.Fatalf("expected 1-hop neighbors on specPwd, got none")
			}
			for _, n := range it.Neighbors {
				if n.Content != nil {
					t.Fatalf("expected nil neighbor content when expand_hops=0: %+v", n)
				}
			}
		}
		if it.NodeType == model.GraphNodeRecord && it.NodeID == recVecOnly.Record.ID {
			sawVecOnly = true
		}
	}
	if !sawSpecPwd {
		t.Fatalf("expected password-policy spec for query '비밀번호를', got %+v", resPwd.Items)
	}
	if !sawVecOnly {
		t.Fatalf("expected vector-only credential doc via HNSW hybrid retrieval, got %+v", resPwd.Items)
	}
	if resPwd.CacheHit {
		t.Fatal("first call must be cache_hit=false")
	}

	// ② Second call with same normalized query hits query_embedding_cache (0 external EmbedQuery calls).
	callsBefore := emb.queryCalls.Load()
	resPwd2, err := engNoRerank.Query(ctx, pg.ContextQueryRequest{
		Query:   "  비밀번호를  ",
		Project: proj.Key,
	})
	if err != nil {
		t.Fatalf("second Query: %v", err)
	}
	if !resPwd2.CacheHit {
		t.Fatal("expected cache_hit=true on second normalized query")
	}
	if emb.queryCalls.Load() != callsBefore {
		t.Fatalf("EmbedQuery called %d times on cache hit, want 0", emb.queryCalls.Load()-callsBefore)
	}

	// Query "훅과" retrieves base-form "훅" record.
	resHook, err := engNoRerank.Query(ctx, pg.ContextQueryRequest{
		Query:      "훅과",
		Project:    proj.Key,
		ExpandHops: 1,
	})
	if err != nil {
		t.Fatalf("Query(훅과): %v", err)
	}
	if len(resHook.Items) == 0 || resHook.Items[0].NodeID != recHook.Record.ID {
		t.Fatalf("expected hook record for '훅과', got %+v", resHook.Items)
	}
	// ⑥ expand_hops=1 expands neighbor content safely even on cyclic edges.
	var sawExpandedNeighborContent bool
	for _, n := range resHook.Items[0].Neighbors {
		if n.NodeID == specPwd.Spec.ID && n.Content != nil && strings.Contains(*n.Content, "Argon2id") {
			sawExpandedNeighborContent = true
		}
	}
	if !sawExpandedNeighborContent {
		t.Fatalf("expected expanded neighbor content when expand_hops=1, got %+v", resHook.Items[0].Neighbors)
	}

	// ③ Mock reranker: score gap >= 0.15 -> single-item cutoff (cutoff_applied=true).
	highGapReranker := &mockReranker{
		fn: func(_ context.Context, _ string, docs []pg.RerankDocument, _ int) ([]pg.RerankResult, pg.Usage, error) {
			out := make([]pg.RerankResult, len(docs))
			for i, d := range docs {
				score := 0.40 - float64(i)*0.05
				if i == 0 {
					score = 0.92
				}
				out[i] = pg.RerankResult{Index: i, ID: d.ID, Score: score}
			}
			return out, pg.Usage{}, nil
		},
	}
	engCutoff := NewEngine(EngineConfig{
		Pool:            pool,
		GraphStore:      gs,
		Embedder:        emb,
		Reranker:        highGapReranker,
		CutoffThreshold: 0.15,
		Logger:          logger,
	})
	resCut, err := engCutoff.Query(ctx, pg.ContextQueryRequest{Query: "비밀번호를", Project: proj.Key})
	if err != nil {
		t.Fatalf("high-gap rerank Query: %v", err)
	}
	if !resCut.Reranked || !resCut.CutoffApplied || len(resCut.Items) != 1 || resCut.ConfidenceGap < 0.15 {
		t.Fatalf("expected single-item cutoff, got reranked=%v cutoff=%v len=%d gap=%v",
			resCut.Reranked, resCut.CutoffApplied, len(resCut.Items), resCut.ConfidenceGap)
	}

	// ③b Mock reranker: score gap < 0.15 -> Top-3 returned (cutoff_applied=false).
	lowGapReranker := &mockReranker{
		fn: func(_ context.Context, _ string, docs []pg.RerankDocument, _ int) ([]pg.RerankResult, pg.Usage, error) {
			out := make([]pg.RerankResult, len(docs))
			for i, d := range docs {
				out[i] = pg.RerankResult{Index: i, ID: d.ID, Score: 0.80 - float64(i)*0.05}
			}
			return out, pg.Usage{}, nil
		},
	}
	engTop3 := NewEngine(EngineConfig{
		Pool:            pool,
		GraphStore:      gs,
		Embedder:        emb,
		Reranker:        lowGapReranker,
		CutoffThreshold: 0.15,
		Logger:          logger,
	})
	resTop3, err := engTop3.Query(ctx, pg.ContextQueryRequest{Query: "비밀번호를", Project: proj.Key})
	if err != nil {
		t.Fatalf("low-gap rerank Query: %v", err)
	}
	if !resTop3.Reranked || resTop3.CutoffApplied || len(resTop3.Items) != 3 {
		t.Fatalf("expected Top-3 without cutoff, got reranked=%v cutoff=%v len=%d (extra rec=%d)",
			resTop3.Reranked, resTop3.CutoffApplied, len(resTop3.Items), recPwdExtra.Record.ID)
	}

	// ④ Mock reranker timeout -> immediate RRF fallback without error (fallback_used=true, reranked=false).
	slowReranker := &mockReranker{
		fn: func(rctx context.Context, _ string, _ []pg.RerankDocument, _ int) ([]pg.RerankResult, pg.Usage, error) {
			select {
			case <-rctx.Done():
				return nil, pg.Usage{}, rctx.Err()
			case <-time.After(2 * time.Second):
				return nil, pg.Usage{}, errors.New("too slow")
			}
		},
	}
	engTimeout := NewEngine(EngineConfig{
		Pool:          pool,
		GraphStore:    gs,
		Embedder:      emb,
		Reranker:      slowReranker,
		RerankTimeout: 60 * time.Millisecond,
		Logger:        logger,
	})
	resFB, err := engTimeout.Query(ctx, pg.ContextQueryRequest{Query: "비밀번호를", Project: proj.Key})
	if err != nil {
		t.Fatalf("timeout fallback Query returned error: %v", err)
	}
	if !resFB.FallbackUsed || resFB.Reranked || resFB.FallbackMode != "rrf" || len(resFB.Items) == 0 {
		t.Fatalf("expected RRF fallback, got fallback_used=%v reranked=%v mode=%q len=%d",
			resFB.FallbackUsed, resFB.Reranked, resFB.FallbackMode, len(resFB.Items))
	}

	// Embedder error fallback -> PGroonga still succeeds cleanly.
	emb.failQuery.Store(true)
	resFTSOnly, err := engNoRerank.Query(ctx, pg.ContextQueryRequest{Query: "비밀번호 감사", Project: proj.Key})
	if err != nil {
		t.Fatalf("embedder failure fallback returned error: %v", err)
	}
	if len(resFTSOnly.Items) == 0 || resFTSOnly.FallbackMode != "fts_only" {
		t.Fatalf("expected PGroonga fts_only fallback results, got mode=%q len=%d", resFTSOnly.FallbackMode, len(resFTSOnly.Items))
	}
}
