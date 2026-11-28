package ingest

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/embed"
	"github.com/wsk0715/agent-board/internal/graph"
	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store/pg"
)

var ingestTestDBSeq atomic.Int64

func openTestPGStore(t *testing.T) *pg.Store {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("AGB_TEST_PG_DSN"))
	if baseDSN == "" {
		t.Skip("AGB_TEST_PG_DSN not set")
	}
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse AGB_TEST_PG_DSN: %v", err)
	}
	dbName := fmt.Sprintf("agb_t_ingest_%d_%d", os.Getpid(), ingestTestDBSeq.Add(1))
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
	st, err := pg.Open(ctx, u.String(), 12)
	if err != nil {
		t.Fatalf("pg.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.EnsureActors(ctx, []model.Actor{
		{ID: "owner", Kind: model.ActorUser},
		{ID: "worker", Kind: model.ActorAgent},
	}); err != nil {
		t.Fatalf("EnsureActors: %v", err)
	}
	return st
}

var trOwner = pg.Trace{Actor: "owner", SessionID: "sess-ingest", ClientHost: "devbox"}

// deterministicEmbedder produces normalized 1024-d vectors without calling any
// external network service. Optional hooks support poison-job failure injection
// and mid-backfill concurrency assertions.
type deterministicEmbedder struct {
	provider   string
	modelName  string
	dims       int
	seedOffset uint32
	onEmbed    func(texts []string) error
}

func newDeterministicEmbedder(modelName string, seedOffset uint32) *deterministicEmbedder {
	return &deterministicEmbedder{
		provider:   "mock",
		modelName:  modelName,
		dims:       embed.DefaultVoyageEmbedDimensions,
		seedOffset: seedOffset,
	}
}

func (e *deterministicEmbedder) Provider() string { return e.provider }
func (e *deterministicEmbedder) Model() string    { return e.modelName }
func (e *deterministicEmbedder) Dimensions() int  { return e.dims }

func (e *deterministicEmbedder) EmbedDocuments(_ context.Context, texts []string) ([][]float32, pg.Usage, error) {
	if e.onEmbed != nil {
		if err := e.onEmbed(texts); err != nil {
			return nil, pg.Usage{}, err
		}
	}
	vecs := make([][]float32, len(texts))
	for i, text := range texts {
		vecs[i] = e.vectorFor(text)
	}
	return vecs, pg.Usage{PromptTokens: len(texts) * 10, TotalTokens: len(texts) * 10}, nil
}

func (e *deterministicEmbedder) EmbedQuery(_ context.Context, query string) ([]float32, pg.Usage, error) {
	return e.vectorFor(query), pg.Usage{PromptTokens: 8, TotalTokens: 8}, nil
}

func (e *deterministicEmbedder) vectorFor(text string) []float32 {
	vec := make([]float32, e.dims)
	tokens := strings.Fields(strings.ToLower(text))
	if len(tokens) == 0 {
		vec[0] = 1
		return vec
	}
	for _, tok := range tokens {
		h := fnv.New32a()
		_, _ = h.Write([]byte(tok))
		bucket := int((h.Sum32() + e.seedOffset) % uint32(e.dims))
		vec[bucket] += 1.0
	}
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		vec[0] = 1
		return vec
	}
	inv := float32(1.0 / math.Sqrt(norm))
	for i := range vec {
		vec[i] *= inv
	}
	return vec
}

// --- Pure Unit Tests (run without PostgreSQL) ---

func TestDefaultNormalizerFrontmatterSummaryAndMetadata(t *testing.T) {
	norm := NewDefaultNormalizer("")
	if norm.PipelineVersion() != DefaultPipelineVersion {
		t.Fatalf("PipelineVersion = %q, want %q", norm.PipelineVersion(), DefaultPipelineVersion)
	}

	raw := "\xef\xbb\xbf---\r\nkind: decision\r\ntitle: Hybrid Search Storage\r\ntags: [postgres, pgroonga, pgvector]\r\n---\r\n\r\n# Hybrid Search Storage\r\n\r\nUse PostgreSQL with PGroonga and pgvector for unified search indexing.\r\n\r\n## Recovery\r\n\r\nEnable crash safe mode.\r\n"
	out, err := norm.Normalize(context.Background(), pg.NormalizeInput{
		SourceType: model.IngestSourceRecord,
		ProjectKey: "DEMO",
		KeyOrName:  "2026-10-01-1200-decision-hybrid-search.md",
		Kind:       "decision",
		Content:    raw,
	})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if out.NormalizedTitle != "Hybrid Search Storage" {
		t.Fatalf("NormalizedTitle = %q", out.NormalizedTitle)
	}
	if strings.Contains(out.NormalizedBody, "tags: [postgres") {
		t.Fatalf("frontmatter was not stripped from NormalizedBody: %q", out.NormalizedBody)
	}
	if !strings.Contains(out.Summary, "PGroonga") {
		t.Fatalf("summary missing prose content: %q", out.Summary)
	}
	headings, _ := out.Metadata["headings"].([]string)
	if len(headings) != 2 || headings[0] != "Hybrid Search Storage" || headings[1] != "Recovery" {
		t.Fatalf("headings = %#v", headings)
	}
	keywords, _ := out.Metadata["keywords"].([]string)
	if len(keywords) == 0 {
		t.Fatal("expected extracted keywords")
	}
}

func TestSemanticChunkerHeadingsCodeBlocksAndTables(t *testing.T) {
	chunker := NewSemanticChunker(ChunkerConfig{MaxChunkRunes: 360})
	body := `# Architecture

Overview paragraph introducing the indexing service.

## Code Example

` + "```go\n// # Not a markdown heading\nfunc buildIndex() string {\n\treturn \"ok\"\n}\n```" + `

## Config Table

| Key | Value |
| --- | --- |
| tokenizer | TokenBigram |
| dimensions | 1024 |
| metric | cosine |
`

	chunks := chunker.Chunk(ChunkInput{
		Title:      "Indexing Spec",
		ProjectKey: "DEMO",
		Kind:       "spec",
		Body:       body,
	})
	if len(chunks) != 3 {
		t.Fatalf("chunk count = %d, want 3: %+v", len(chunks), chunks)
	}

	if chunks[0].SectionPath != "Architecture" {
		t.Fatalf("chunk[0].SectionPath = %q", chunks[0].SectionPath)
	}
	wantPrefix := "Indexing Spec · DEMO · spec · Architecture > Code Example"
	if chunks[1].HeaderPrefix != wantPrefix {
		t.Fatalf("chunk[1].HeaderPrefix = %q, want %q", chunks[1].HeaderPrefix, wantPrefix)
	}
	if !strings.Contains(chunks[1].ChunkText, "// # Not a markdown heading") || !strings.Contains(chunks[1].ChunkText, "```go") {
		t.Fatalf("code fence was corrupted: %q", chunks[1].ChunkText)
	}
	if !strings.HasPrefix(chunks[1].SearchText, wantPrefix+"\n\n") {
		t.Fatalf("SearchText missing breadcrumb prefix: %q", chunks[1].SearchText)
	}
	if len(chunks[1].ContentSHA256) != 64 {
		t.Fatalf("unexpected sha256 length: %q", chunks[1].ContentSHA256)
	}
	if chunks[2].SectionPath != "Architecture > Config Table" || !strings.Contains(chunks[2].ChunkText, "| tokenizer | TokenBigram |") {
		t.Fatalf("table chunk corrupted: %+v", chunks[2])
	}
}

// --- Real PostgreSQL 17 + PGroonga + pgvector Integration Tests ---

// Test 1: Poison job dead-letter isolation with zero Head-of-Line Blocking.
func TestPoisonJobDeadLetterNoHeadOfLineBlocking(t *testing.T) {
	st := openTestPGStore(t)
	ctx := context.Background()

	proj, err := st.CreateProject(ctx, trOwner, pg.CreateProjectInput{Key: "DEMO", Name: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	poisonRec, err := st.CreateRecord(ctx, trOwner, proj.Key, pg.CreateRecordInput{
		Name:    "2026-10-01-1000-note-poison.md",
		Kind:    "note",
		Title:   "Poison Record",
		Content: "# Poison\nPOISON_TRIGGER_TOKEN causes upstream failure",
	})
	if err != nil {
		t.Fatal(err)
	}
	goodRec, err := st.CreateRecord(ctx, trOwner, proj.Key, pg.CreateRecordInput{
		Name:    "2026-10-01-1005-note-healthy.md",
		Kind:    "note",
		Title:   "Healthy Record",
		Content: "# Healthy\nNormal architecture document that must complete promptly.",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Lower max_attempts on the poison job to 2 so it dead-letters deterministically.
	if _, err := st.Pool().Exec(ctx, `UPDATE ingest_job SET max_attempts = 2 WHERE id = $1`, poisonRec.IngestJobID); err != nil {
		t.Fatal(err)
	}

	var poisonFails atomic.Int32
	emb := newDeterministicEmbedder("voyage-4", 1)
	emb.onEmbed = func(texts []string) error {
		for _, txt := range texts {
			if strings.Contains(txt, "POISON_TRIGGER_TOKEN") {
				poisonFails.Add(1)
				return errors.New("simulated poison payload failure")
			}
		}
		return nil
	}

	q := NewQueue(st.Pool(), QueueConfig{
		DefaultMaxAttempts: 2,
		BaseBackoff:        10 * time.Millisecond,
		MaxBackoff:         20 * time.Millisecond,
		WaitPollInterval:   15 * time.Millisecond,
	})
	w := NewWorker(WorkerConfig{
		Pool:          st.Pool(),
		Queue:         q,
		Embedder:      emb,
		WorkerID:      "w-hol-test",
		SweepInterval: 20 * time.Millisecond,
	})

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = w.Run(runCtx) }()

	// Healthy job MUST reach done even though the earlier job is poison.
	goodWait, err := q.Wait(ctx, goodRec.IngestJobID, 5*time.Second)
	if err != nil {
		t.Fatalf("Wait(good): %v", err)
	}
	if goodWait.TimedOut || goodWait.Status != model.IngestStatusDone {
		t.Fatalf("good job result = %+v, want done without timeout", goodWait)
	}

	// Poison job MUST isolate into dead_letter after 2 attempts.
	poisonWait, err := q.Wait(ctx, poisonRec.IngestJobID, 5*time.Second)
	if err != nil {
		t.Fatalf("Wait(poison): %v", err)
	}
	if poisonWait.TimedOut || poisonWait.Status != model.IngestStatusDeadLetter {
		t.Fatalf("poison job result = %+v, want dead_letter", poisonWait)
	}
	if !strings.Contains(poisonWait.LastError, "simulated poison payload failure") {
		t.Fatalf("poison last_error = %q", poisonWait.LastError)
	}
	if poisonFails.Load() != 2 {
		t.Fatalf("poison attempts = %d, want 2", poisonFails.Load())
	}

	dlq, err := q.ListDeadLetters(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(dlq) != 1 || dlq[0].ID != poisonRec.IngestJobID {
		t.Fatalf("DLQ = %+v, want [%d]", dlq, poisonRec.IngestJobID)
	}

	// Clear fault injection, requeue dead letter, and verify recovery.
	emb.onEmbed = nil
	requeued, err := q.RequeueDeadLetter(ctx, poisonRec.IngestJobID)
	if err != nil {
		t.Fatalf("RequeueDeadLetter: %v", err)
	}
	if requeued.Status != model.IngestStatusPending || requeued.Attempts != 0 {
		t.Fatalf("requeued = %+v", requeued)
	}
	recovered, err := q.Wait(ctx, poisonRec.IngestJobID, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != model.IngestStatusDone {
		t.Fatalf("recovered status = %s, want done", recovered.Status)
	}
}

// Test 2 & 3: Wait() notification + supersedes_candidates delivery + rejected protection.
func TestWaitReturnsSupersedesCandidatesAndRespectsRejection(t *testing.T) {
	st := openTestPGStore(t)
	ctx := context.Background()

	proj, err := st.CreateProject(ctx, trOwner, pg.CreateProjectInput{Key: "CORE", Name: "Core"})
	if err != nil {
		t.Fatal(err)
	}
	iss, err := st.CreateIssue(ctx, trOwner, proj.Key, pg.CreateIssueInput{
		Title: "Vector index rollout", Type: model.TypeDesign, Priority: model.P1,
	})
	if err != nil {
		t.Fatal(err)
	}

	emb := newDeterministicEmbedder("voyage-4", 7)
	q := NewQueue(st.Pool(), QueueConfig{BaseBackoff: 20 * time.Millisecond, WaitPollInterval: 20 * time.Millisecond})
	w := NewWorker(WorkerConfig{
		Pool:          st.Pool(),
		Queue:         q,
		Embedder:      emb,
		SweepInterval: 30 * time.Millisecond,
	})

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = w.Run(runCtx) }()

	// Wait for the initial issue ingest job.
	if res, err := q.Wait(ctx, iss.IngestJobID, 5*time.Second); err != nil || res.Status != model.IngestStatusDone {
		t.Fatalf("wait issue job: res=%+v err=%v", res, err)
	}

	oldRec, err := st.CreateRecord(ctx, trOwner, proj.Key, pg.CreateRecordInput{
		Name:    "2026-10-01-1400-decision-vector-index-v1.md",
		Kind:    "decision",
		Title:   "Vector index baseline configuration",
		Content: "# Vector Index v1\nInitial vector index storage decision for hybrid search.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := q.Wait(ctx, oldRec.IngestJobID, 5*time.Second); err != nil || res.Status != model.IngestStatusDone {
		t.Fatalf("wait old record: res=%+v err=%v", res, err)
	}

	newContent := fmt.Sprintf(`---
kind: decision
title: Vector index updated configuration
tags: [vector, index, hybrid]
---

# Vector Index v2

Relates to %s.
기존 결정 %s 내용을 대체한다. HNSW 인덱스와 하이브리드 검색 구성을 확정한다.
`, iss.Issue.Key, oldRec.Record.Name)

	newRec, err := st.CreateRecord(ctx, trOwner, proj.Key, pg.CreateRecordInput{
		Name:    "2026-10-08-1400-decision-vector-index-v2.md",
		Kind:    "decision",
		Title:   "Vector index updated configuration",
		Content: newContent,
	})
	if err != nil {
		t.Fatal(err)
	}

	waitRes, err := q.Wait(ctx, newRec.IngestJobID, 5*time.Second)
	if err != nil {
		t.Fatalf("Wait(newRec): %v", err)
	}
	if waitRes.TimedOut || waitRes.Status != model.IngestStatusDone {
		t.Fatalf("waitRes = %+v, want done", waitRes)
	}
	if len(waitRes.SupersedesCandidates) != 1 {
		t.Fatalf("supersedes_candidates = %+v, want 1 candidate", waitRes.SupersedesCandidates)
	}
	cand := waitRes.SupersedesCandidates[0]
	if cand.TargetID != oldRec.Record.ID || cand.TargetName != oldRec.Record.Name {
		t.Fatalf("unexpected candidate target: %+v", cand)
	}

	// Verify inferred about/related edges are confirmed while supersedes is candidate.
	gs := graph.NewStore(st.Pool())
	edges, err := gs.ListEdgesForSource(ctx, model.GraphNodeRecord, newRec.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawConfirmedAbout, sawConfirmedRelated, sawCandidateSupersedes bool
	for _, e := range edges {
		if e.Kind == model.EdgeKindAbout && e.Origin == model.EdgeOriginInferred && e.Status == model.EdgeStatusConfirmed {
			sawConfirmedAbout = true
		}
		if e.Kind == model.EdgeKindRelated && e.Origin == model.EdgeOriginInferred && e.Status == model.EdgeStatusConfirmed {
			sawConfirmedRelated = true
		}
		if e.Kind == model.EdgeKindSupersedes && e.Origin == model.EdgeOriginInferred && e.Status == model.EdgeStatusCandidate {
			sawCandidateSupersedes = true
		}
	}
	if !sawConfirmedAbout || !sawConfirmedRelated || !sawCandidateSupersedes {
		t.Fatalf("edge policy mismatch: about=%v related=%v supersedes_candidate=%v edges=%+v",
			sawConfirmedAbout, sawConfirmedRelated, sawCandidateSupersedes, edges)
	}

	// Reject the supersedes candidate and re-enqueue ingest for the same record;
	// rejected edge MUST stay rejected and not reappear as a candidate.
	if _, err := gs.RejectEdge(ctx, "owner", cand.EdgeID); err != nil {
		t.Fatalf("RejectEdge: %v", err)
	}
	reJobID, err := q.Enqueue(ctx, pg.EnqueueInput{
		SourceType:   model.IngestSourceRecord,
		SourceID:     newRec.Record.ID,
		SourceSHA256: newRec.Record.SHA256,
		Stage:        model.IngestStageAll,
	})
	if err != nil {
		t.Fatal(err)
	}
	reWait, err := q.Wait(ctx, reJobID, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if reWait.Status != model.IngestStatusDone || len(reWait.SupersedesCandidates) != 0 {
		t.Fatalf("expected 0 supersedes candidates after rejection + re-ingest, got %+v", reWait)
	}

	// Confirm raw source content in record_document was never modified.
	gotRec, err := st.GetRecord(ctx, proj.Key, newRec.Record.Name)
	if err != nil {
		t.Fatal(err)
	}
	if gotRec.Content == nil || *gotRec.Content != newContent {
		t.Fatal("domain record_document.content was modified by ingest pipeline")
	}
}

// Test 4 & 5: Blue-green embedding generation switch (zero-downtime during
// backfill + atomic swap + retired pruning) AND PGroonga + pgvector HNSW query.
func TestBlueGreenGenerationAndPGroongaHNSWQueries(t *testing.T) {
	st := openTestPGStore(t)
	ctx := context.Background()

	proj, err := st.CreateProject(ctx, trOwner, pg.CreateProjectInput{Key: "DEMO", Name: "Demo"})
	if err != nil {
		t.Fatal(err)
	}

	embV1 := newDeterministicEmbedder("voyage-4", 11)
	q := NewQueue(st.Pool(), QueueConfig{WaitPollInterval: 15 * time.Millisecond})
	gm := NewGenerationManager(st.Pool())
	w := NewWorker(WorkerConfig{
		Pool:          st.Pool(),
		Queue:         q,
		Embedder:      embV1,
		Generations:   gm,
		SweepInterval: 20 * time.Millisecond,
	})

	docs := []struct {
		projectKey string
		name       string
		title      string
		content    string
		archived   bool
	}{
		{
			projectKey: proj.Key,
			name:       "2026-10-01-0900-decision-crash-recovery.md",
			title:      "크래시 복구 정책",
			content:    "# 크래시 복구 정책\n\nPGroonga WAL 리소스 매니저와 크래시 세이퍼를 활성화해 비정상 종료 후 인덱스를 자동 복구한다.",
		},
		{
			projectKey: model.GlobalProjectKey,
			name:       "2026-10-02-0900-decision-global-guideline.md",
			title:      "전역 검색 운영 지침",
			content:    "# 전역 검색 운영 지침\n\n전역 스코프 문서도 프로젝트 조회 시 함께 검색되며 크래시 복구 검증을 통과해야 한다.",
		},
		{
			projectKey: proj.Key,
			name:       "2026-10-03-0900-decision-archived-draft.md",
			title:      "보관된 과거 초안",
			content:    "# 보관된 초안\n\n크래시 복구 정책 초안이지만 보관 처리되어 검색에서 제외된다.",
			archived:   true,
		},
		{
			projectKey: proj.Key,
			name:       "2026-10-04-0900-report-query-cache.md",
			title:      "쿼리 캐시 측정 보고",
			content:    "# 쿼리 캐시 측정 보고\n\n동일 질의의 임베딩 캐시 적중률을 높여 지연 시간을 줄인다.",
		},
	}

	for _, d := range docs {
		res, err := st.CreateRecord(ctx, trOwner, d.projectKey, pg.CreateRecordInput{
			Name:     d.name,
			Kind:     "decision",
			Title:    d.title,
			Content:  d.content,
			Archived: d.archived,
		})
		if err != nil {
			t.Fatalf("CreateRecord %s: %v", d.name, err)
		}
		for {
			processed, err := w.ProcessOnce(ctx)
			if err != nil {
				t.Fatalf("ProcessOnce: %v", err)
			}
			if !processed {
				break
			}
		}
		job, err := q.GetJob(ctx, res.IngestJobID)
		if err != nil || job.Status != model.IngestStatusDone {
			t.Fatalf("job %d status=%s err=%v", res.IngestJobID, job.Status, err)
		}
	}

	// Verify active generation 1 exists.
	gen1, err := gm.GetActiveGeneration(ctx)
	if err != nil {
		t.Fatalf("GetActiveGeneration: %v", err)
	}
	if gen1.State != GenerationActive || gen1.Model != "voyage-4" || gen1.Dimensions != 1024 {
		t.Fatalf("unexpected gen1: %+v", gen1)
	}

	// Verify PGroonga query on rag_chunk (target project + GLOBAL, excluding archived).
	if _, err := st.Pool().Exec(ctx, `ANALYZE rag_chunk`); err != nil {
		t.Fatalf("ANALYZE rag_chunk: %v", err)
	}
	pgRows, err := st.Pool().Query(ctx, `
		SELECT project_id, source_id, search_text
		FROM rag_chunk
		WHERE NOT archived
		  AND project_id IN ($1, 0)
		  AND search_text &@~ $2
		ORDER BY project_id ASC`,
		proj.ID, "크래시 복구",
	)
	if err != nil {
		t.Fatalf("PGroonga query: %v", err)
	}
	var pgroongaProjects []int64
	for pgRows.Next() {
		var pid, sid int64
		var txt string
		if err := pgRows.Scan(&pid, &sid, &txt); err != nil {
			pgRows.Close()
			t.Fatal(err)
		}
		pgroongaProjects = append(pgroongaProjects, pid)
	}
	pgRows.Close()
	if len(pgroongaProjects) != 2 || pgroongaProjects[0] != 0 || pgroongaProjects[1] != proj.ID {
		t.Fatalf("PGroonga matched projects = %v, want [0, %d]", pgroongaProjects, proj.ID)
	}

	// Helper to run an HNSW cosine similarity query against whichever generation
	// is currently active.
	queryActiveHNSW := func(queryVec []float32) (int64, string, error) {
		lit, err := embed.FormatVectorChecked(queryVec, 1024)
		if err != nil {
			return 0, "", err
		}
		tx, err := st.Pool().Begin(ctx)
		if err != nil {
			return 0, "", err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `SET LOCAL hnsw.ef_search = 100`); err != nil {
			return 0, "", err
		}
		if _, err := tx.Exec(ctx, `SET LOCAL hnsw.iterative_scan = relaxed_order`); err != nil {
			return 0, "", err
		}
		var genID int64
		var searchText string
		err = tx.QueryRow(ctx, `
			SELECT e.generation_id, c.search_text
			FROM rag_embedding e
			JOIN embedding_generation g ON g.id = e.generation_id AND g.state = 'active'
			JOIN rag_chunk c ON c.id = e.chunk_id
			WHERE NOT c.archived AND c.project_id IN ($1, 0)
			ORDER BY e.embedding <=> $2::vector ASC
			LIMIT 1`,
			proj.ID, lit,
		).Scan(&genID, &searchText)
		return genID, searchText, err
	}

	// Verify HNSW vector(1024) query against Gen 1 before blue-green switch.
	qVecV1, _, _ := embV1.EmbedQuery(ctx, "PGroonga WAL 크래시 복구 정책")
	hitGenID, hitText, err := queryActiveHNSW(qVecV1)
	if err != nil {
		t.Fatalf("HNSW query on gen1: %v", err)
	}
	if hitGenID != gen1.ID || !strings.Contains(hitText, "크래시 복구") {
		t.Fatalf("gen1 HNSW top hit = (gen=%d, text=%q)", hitGenID, hitText)
	}

	// Start Gen 2 in 'building' state and verify Gen 1 stays active & queryable
	// throughout the backfill batches.
	gen2, err := gm.StartBuildingGeneration(ctx, "mock", "voyage-4-v2", 1024)
	if err != nil {
		t.Fatalf("StartBuildingGeneration: %v", err)
	}
	if gen2.State != GenerationBuilding {
		t.Fatalf("gen2 state = %q, want building", gen2.State)
	}

	var mu sync.Mutex
	midBackfillReads := 0
	embV2 := newDeterministicEmbedder("voyage-4-v2", 37)
	embV2.onEmbed = func(_ []string) error {
		// While building gen2, active serving generation MUST still be gen1.
		gID, txt, qErr := queryActiveHNSW(qVecV1)
		if qErr != nil {
			return fmt.Errorf("mid-backfill query failed: %w", qErr)
		}
		if gID != gen1.ID || !strings.Contains(txt, "크래시 복구") {
			return fmt.Errorf("mid-backfill served gen=%d, want gen1=%d", gID, gen1.ID)
		}
		mu.Lock()
		midBackfillReads++
		mu.Unlock()
		return nil
	}

	stats, err := gm.BackfillGeneration(ctx, gen2.ID, embV2, 2)
	if err != nil {
		t.Fatalf("BackfillGeneration: %v", err)
	}
	embV2.onEmbed = nil
	if stats.ChunksEmbedded != 4 || stats.Batches < 2 {
		t.Fatalf("unexpected backfill stats: %+v", stats)
	}
	if midBackfillReads < 2 {
		t.Fatalf("expected >=2 uninterrupted mid-backfill reads on gen1, got %d", midBackfillReads)
	}

	// Atomically activate Gen 2 -> Gen 1 becomes retired, Gen 2 becomes active.
	activated, err := gm.ActivateGeneration(ctx, gen2.ID)
	if err != nil {
		t.Fatalf("ActivateGeneration: %v", err)
	}
	if activated.ID != gen2.ID || activated.State != GenerationActive || activated.ActivatedAt == nil {
		t.Fatalf("activated = %+v", activated)
	}

	// Verify active HNSW queries now serve Gen 2 embeddings.
	qVecV2, _, _ := embV2.EmbedQuery(ctx, "임베딩 캐시 적중률 쿼리 캐시")
	hitGen2ID, hitText2, err := queryActiveHNSW(qVecV2)
	if err != nil {
		t.Fatalf("HNSW query on gen2: %v", err)
	}
	if hitGen2ID != gen2.ID || !strings.Contains(hitText2, "쿼리 캐시") {
		t.Fatalf("gen2 HNSW top hit = (gen=%d, text=%q)", hitGen2ID, hitText2)
	}

	// Verify PruneRetired removes Gen 1 embeddings without touching Gen 2.
	pruned, err := gm.PruneRetired(ctx)
	if err != nil {
		t.Fatalf("PruneRetired: %v", err)
	}
	if pruned != 4 {
		t.Fatalf("pruned = %d, want 4", pruned)
	}
	var gen2Remaining int
	if err := st.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM rag_embedding WHERE generation_id = $1`, gen2.ID).Scan(&gen2Remaining); err != nil {
		t.Fatal(err)
	}
	if gen2Remaining != 4 {
		t.Fatalf("gen2 embeddings remaining = %d, want 4", gen2Remaining)
	}

	// Verify atomic spec chunk replacement leaves zero stale chunks after revision update.
	sp1, err := st.UpsertSpec(ctx, trOwner, proj.Key, "storage-layout", pg.UpsertSpecInput{
		Title:   "Storage Layout",
		Kind:    "spec",
		Content: "# Section A\nAlpha details.\n\n# Section B\nBeta details.\n\n# Section C\nGamma details.",
	})
	if err != nil {
		t.Fatal(err)
	}
	wV2 := NewWorker(WorkerConfig{Pool: st.Pool(), Queue: q, Embedder: embV2, Generations: gm})
	if ok, err := wV2.ProcessOnce(ctx); err != nil || !ok {
		t.Fatalf("process spec rev1: ok=%v err=%v", ok, err)
	}
	if j, err := q.GetJob(ctx, sp1.IngestJobID); err != nil || j.Status != model.IngestStatusDone {
		t.Fatalf("spec rev1 job status = %s, last_error = %v, err = %v", j.Status, j.LastError, err)
	}
	var countRev1 int
	if err := st.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM rag_chunk WHERE source_type = 'spec' AND source_id = $1`, sp1.Spec.ID).Scan(&countRev1); err != nil {
		t.Fatal(err)
	}
	if countRev1 != 3 {
		t.Fatalf("spec rev1 chunks = %d, want 3", countRev1)
	}

	sp2, err := st.UpsertSpec(ctx, trOwner, proj.Key, "storage-layout", pg.UpsertSpecInput{
		Title:         "Storage Layout",
		Kind:          "spec",
		Content:       "# Consolidated\nSingle unified section replacing three prior sections.",
		ChangeSummary: "Consolidate sections",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := wV2.ProcessOnce(ctx); err != nil || !ok {
		t.Fatalf("process spec rev2: ok=%v err=%v", ok, err)
	}
	if j, err := q.GetJob(ctx, sp2.IngestJobID); err != nil || j.Status != model.IngestStatusDone {
		t.Fatalf("spec rev2 job status = %s, last_error = %v, err = %v", j.Status, j.LastError, err)
	}
	var countRev2 int
	if err := st.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM rag_chunk WHERE source_type = 'spec' AND source_id = $1`, sp1.Spec.ID).Scan(&countRev2); err != nil {
		t.Fatal(err)
	}
	if countRev2 != 1 {
		t.Fatalf("spec rev2 chunks = %d, want 1 (stale chunks must be removed)", countRev2)
	}
}
