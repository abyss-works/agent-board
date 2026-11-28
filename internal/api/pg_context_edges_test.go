package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/graph"
	"github.com/wsk0715/agent-board/internal/ingest"
	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/search"
	pgstore "github.com/wsk0715/agent-board/internal/store/pg"
)

type failingReranker struct{}

func (failingReranker) Provider() string { return "mock" }
func (failingReranker) Model() string    { return "mock-fail" }
func (failingReranker) Rerank(_ context.Context, _ string, _ []pgstore.RerankDocument, _ int) ([]pgstore.RerankResult, pgstore.Usage, error) {
	return nil, pgstore.Usage{}, errors.New("upstream unavailable")
}

func newPGFullStackTestHandler(t *testing.T, reranker pgstore.Reranker) (http.Handler, *pgstore.Store) {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("AGB_TEST_PG_DSN"))
	if baseDSN == "" {
		t.Skip("AGB_TEST_PG_DSN not set")
	}
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse AGB_TEST_PG_DSN: %v", err)
	}
	ctx := context.Background()
	dbName := fmt.Sprintf("agb_t_ctx_%d_%d", os.Getpid(), apiTestDBSeq.Add(1))
	pool, err := pgxpool.New(ctx, baseDSN)
	if err != nil {
		t.Fatalf("connect admin pool: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE DATABASE `+dbName); err != nil {
		pool.Close()
		t.Fatalf("CREATE DATABASE %s: %v", dbName, err)
	}
	pool.Close()
	t.Cleanup(func() {
		p, err := pgxpool.New(context.Background(), baseDSN)
		if err != nil {
			return
		}
		defer p.Close()
		_, _ = p.Exec(context.Background(), `DROP DATABASE IF EXISTS `+dbName+` WITH (FORCE)`)
	})
	u.Path = "/" + dbName

	pgs, err := pgstore.Open(ctx, u.String(), 8)
	if err != nil {
		t.Fatalf("pgstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = pgs.Close() })

	actors := []model.Actor{
		{ID: "owner", Kind: model.ActorUser},
		{ID: "worker", Kind: model.ActorAgent},
	}
	if err := pgs.EnsureActors(ctx, actors); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	gs := graph.NewStore(pgs.Pool())
	q := ingest.NewQueue(pgs.Pool(), ingest.QueueConfig{WaitPollInterval: 20 * time.Millisecond})
	w := ingest.NewWorker(ingest.WorkerConfig{
		Pool:          pgs.Pool(),
		Queue:         q,
		GraphStore:    gs,
		SweepInterval: 50 * time.Millisecond,
		Logger:        logger,
	})
	wCtx, wCancel := context.WithCancel(context.Background())
	wDone := make(chan struct{})
	go func() {
		defer close(wDone)
		_ = w.Run(wCtx)
	}()
	t.Cleanup(func() {
		wCancel()
		<-wDone
		q.Close()
	})

	eng := search.NewEngine(search.EngineConfig{
		Pool:       pgs.Pool(),
		GraphStore: gs,
		Reranker:   reranker,
		Logger:     logger,
	})

	h := New(Options{
		Token:         testToken,
		Actors:        actors,
		Version:       "test",
		Logger:        logger,
		PGStore:       pgs,
		GraphStore:    gs,
		IngestQueue:   q,
		ContextEngine: eng,
	})
	t.Cleanup(func() {
		if c, ok := h.(io.Closer); ok {
			_ = c.Close()
		}
	})
	return h, pgs
}

func TestPGContextQueryWaitEdgesAndReadyQueue(t *testing.T) {
	h, _ := newPGFullStackTestHandler(t, nil)

	// Setup project.
	rec := pgDo(h, "POST", "/v1/projects", []byte(`{"key":"APP","name":"App"}`), "")
	if rec.Code != 201 {
		t.Fatalf("create project = %d %s", rec.Code, rec.Body.String())
	}

	// 1. Create initial decision record with ?wait=true.
	rec1Body := `{"name":"2026-10-01-1000-decision-cache-policy.md","kind":"decision","title":"캐시 만료 정책 v1","content":"# 캐시 만료 정책\n기존 캐시 만료 시간은 5분으로 고정한다."}`
	rec = pgDo(h, "POST", "/v1/projects/APP/records?wait=true&wait_timeout_ms=5000", []byte(rec1Body), "")
	if rec.Code != 201 {
		t.Fatalf("create record v1 = %d %s", rec.Code, rec.Body.String())
	}
	var r1Resp struct {
		ID                   int64                         `json:"id"`
		IngestStatus         string                        `json:"ingest_status"`
		SupersedesCandidates []pgstore.SupersedesCandidate `json:"supersedes_candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &r1Resp); err != nil {
		t.Fatal(err)
	}
	if r1Resp.IngestStatus != "done" {
		t.Fatalf("r1 ingest_status = %q, want done", r1Resp.IngestStatus)
	}

	// 2. Create newer decision record superseding v1 with ?wait=true -> returns supersedes_candidates immediately.
	rec2Body := `{"name":"2026-10-05-1000-decision-cache-policy-v2.md","kind":"decision","title":"캐시 만료 정책 v2","content":"# 캐시 만료 정책 개정\n기존 결정 2026-10-01-1000-decision-cache-policy.md 를 대체한다. 새 캐시 만료 시간은 30분이다."}`
	rec = pgDo(h, "POST", "/v1/projects/APP/records?wait=true&wait_timeout_ms=5000", []byte(rec2Body), "")
	if rec.Code != 201 {
		t.Fatalf("create record v2 = %d %s", rec.Code, rec.Body.String())
	}
	var r2Resp struct {
		ID                   int64                         `json:"id"`
		IngestStatus         string                        `json:"ingest_status"`
		SupersedesCandidates []pgstore.SupersedesCandidate `json:"supersedes_candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &r2Resp); err != nil {
		t.Fatal(err)
	}
	if r2Resp.IngestStatus != "done" || len(r2Resp.SupersedesCandidates) == 0 {
		t.Fatalf("expected wait=true to return done with supersedes_candidates, got %+v", r2Resp)
	}
	candEdgeID := r2Resp.SupersedesCandidates[0].EdgeID

	// GET /v1/edges/candidates filters by project, source_type, source_id with total.
	rec = pgDo(h, "GET", fmt.Sprintf("/v1/edges/candidates?project=APP&source_type=record&source_id=%d", r2Resp.ID), nil, "")
	if rec.Code != 200 {
		t.Fatalf("GET /v1/edges/candidates = %d %s", rec.Code, rec.Body.String())
	}
	var candList struct {
		Items []pgstore.EdgeCandidate `json:"items"`
		Total int                     `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &candList); err != nil || candList.Total != 1 || len(candList.Items) != 1 {
		t.Fatalf("candidates list = %s", rec.Body.String())
	}

	// 3. Confirm candidate -> old record excluded from default /v1/context/query, new record returned.
	rec = pgDo(h, "POST", fmt.Sprintf("/v1/edges/%d/confirm", candEdgeID), nil, "")
	if rec.Code != 200 {
		t.Fatalf("confirm edge = %d %s", rec.Code, rec.Body.String())
	}
	// Re-confirming returns 409 edge_already_decided.
	rec = pgDo(h, "POST", fmt.Sprintf("/v1/edges/%d/confirm", candEdgeID), nil, "")
	if rec.Code != 409 || decodeErr(t, rec).Error.Code != "edge_already_decided" {
		t.Fatalf("re-confirm = %d %s, want 409 edge_already_decided", rec.Code, rec.Body.String())
	}

	rec = pgDo(h, "POST", "/v1/context/query", []byte(`{"query":"캐시 만료 정책을","project":"APP"}`), "")
	if rec.Code != 200 {
		t.Fatalf("context query = %d %s", rec.Code, rec.Body.String())
	}
	var ctxResp pgstore.ContextQueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ctxResp); err != nil {
		t.Fatal(err)
	}
	if len(ctxResp.Items) == 0 {
		t.Fatalf("expected context query items, got empty response: %s", rec.Body.String())
	}
	for _, it := range ctxResp.Items {
		if it.NodeType == model.GraphNodeRecord && it.NodeID == r1Resp.ID {
			t.Fatalf("superseded record %d should be excluded from default query: %+v", r1Resp.ID, ctxResp.Items)
		}
	}
	if ctxResp.Items[0].NodeID != r2Resp.ID {
		t.Fatalf("expected latest record %d first, got %+v", r2Resp.ID, ctxResp.Items[0])
	}

	// include_superseded=true returns old record with superseded_by pointing to r2.
	rec = pgDo(h, "POST", "/v1/context/query", []byte(`{"query":"5분으로 고정","project":"APP","include_superseded":true}`), "")
	if rec.Code != 200 {
		t.Fatalf("context query include_superseded = %d %s", rec.Code, rec.Body.String())
	}
	var supResp pgstore.ContextQueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &supResp); err != nil {
		t.Fatal(err)
	}
	var sawOldWithSupBy bool
	for _, it := range supResp.Items {
		if it.NodeID == r1Resp.ID && it.SupersededBy != nil && it.SupersededBy.NodeID == r2Resp.ID {
			sawOldWithSupBy = true
		}
	}
	if !sawOldWithSupBy {
		t.Fatalf("expected old record with superseded_by=%d, got %+v", r2Resp.ID, supResp.Items)
	}

	// 4. Reject flow: third record produces candidate -> reject removes from candidates.
	rec3Body := `{"name":"2026-10-06-1000-decision-cache-policy-v3.md","kind":"decision","title":"캐시 만료 정책 v3","content":"# 캐시 만료 정책 검토\n기존 결정 2026-10-05-1000-decision-cache-policy-v2.md 를 대체한다."}`
	rec = pgDo(h, "POST", "/v1/projects/APP/records?wait=true&wait_timeout_ms=5000", []byte(rec3Body), "")
	if rec.Code != 201 {
		t.Fatalf("create record v3 = %d %s", rec.Code, rec.Body.String())
	}
	var r3Resp struct {
		ID                   int64                         `json:"id"`
		SupersedesCandidates []pgstore.SupersedesCandidate `json:"supersedes_candidates"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &r3Resp)
	if len(r3Resp.SupersedesCandidates) == 0 {
		t.Fatalf("expected candidate on v3: %s", rec.Body.String())
	}
	for _, cand := range r3Resp.SupersedesCandidates {
		rec = pgDo(h, "POST", fmt.Sprintf("/v1/edges/%d/reject", cand.EdgeID), nil, "")
		if rec.Code != 200 {
			t.Fatalf("reject edge %d = %d %s", cand.EdgeID, rec.Code, rec.Body.String())
		}
		// Rejecting an already-rejected edge returns 409 edge_already_decided.
		rec = pgDo(h, "POST", fmt.Sprintf("/v1/edges/%d/reject", cand.EdgeID), nil, "")
		if rec.Code != 409 || decodeErr(t, rec).Error.Code != "edge_already_decided" {
			t.Fatalf("re-reject edge %d = %d %s, want 409 edge_already_decided", cand.EdgeID, rec.Code, rec.Body.String())
		}
	}
	rec = pgDo(h, "GET", fmt.Sprintf("/v1/edges/candidates?project=APP&source_type=record&source_id=%d", r3Resp.ID), nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Fatalf("expected 0 candidates after reject, got %d %s", rec.Code, rec.Body.String())
	}

	// 5. Spec ?wait=true + explicit POST /v1/edges + DELETE /v1/edges/{id} + 409 cycle.
	rec = pgDo(h, "PUT", "/v1/projects/APP/specs/cache-spec?wait=true&wait_timeout_ms=5000", []byte(`{"title":"Cache Spec","content":"# Cache Spec\nTTL details"}`), "")
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"ingest_status":"done"`) {
		t.Fatalf("PUT spec wait=true = %d %s", rec.Code, rec.Body.String())
	}

	// 6. Ready queue: issue B depends_on issue A -> B blocked until A is done.
	rec = pgDo(h, "POST", "/v1/projects/APP/issues", []byte(`{"title":"Issue A","priority":"P1"}`), "")
	if rec.Code != 201 {
		t.Fatalf("create issue A = %d %s", rec.Code, rec.Body.String())
	}
	var issA struct {
		ID  int64  `json:"id"`
		Key string `json:"key"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &issA)

	rec = pgDo(h, "POST", "/v1/projects/APP/issues", []byte(`{"title":"Issue B","priority":"P0"}`), "")
	if rec.Code != 201 {
		t.Fatalf("create issue B = %d %s", rec.Code, rec.Body.String())
	}
	var issB struct {
		ID  int64  `json:"id"`
		Key string `json:"key"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &issB)

	// B depends_on A.
	rec = pgDo(h, "POST", "/v1/edges", []byte(fmt.Sprintf(
		`{"project":"APP","source_type":"issue","source_id":%d,"target_type":"issue","target_id":%d,"kind":"depends_on"}`,
		issB.ID, issA.ID,
	)), "")
	if rec.Code != 201 {
		t.Fatalf("create depends_on edge = %d %s", rec.Code, rec.Body.String())
	}
	var depEdge model.Edge
	_ = json.Unmarshal(rec.Body.Bytes(), &depEdge)

	// Reverse edge A depends_on B must fail with 409 cycle.
	rec = pgDo(h, "POST", "/v1/edges", []byte(fmt.Sprintf(
		`{"project":"APP","source_type":"issue","source_id":%d,"target_type":"issue","target_id":%d,"kind":"depends_on"}`,
		issA.ID, issB.ID,
	)), "")
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), `"cycle"`) {
		t.Fatalf("cycle edge = %d %s, want 409 cycle", rec.Code, rec.Body.String())
	}

	// Ready queue exposes only A while B is blocked.
	rec = pgDo(h, "GET", "/v1/projects/APP/ready", nil, "")
	if rec.Code != 200 {
		t.Fatalf("GET ready = %d %s", rec.Code, rec.Body.String())
	}
	var ready1 struct {
		Project string               `json:"project"`
		Items   []pgstore.ReadyIssue `json:"items"`
		Total   int                  `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ready1)
	if ready1.Project != "APP" || ready1.Total != 1 || len(ready1.Items) != 1 || ready1.Items[0].Key != issA.Key {
		t.Fatalf("ready before done = %+v, want only %s", ready1, issA.Key)
	}

	// Mark A done -> B immediately appears in ready queue.
	rec = pgDo(h, "PATCH", "/v1/issues/"+issA.Key, []byte(`{"status":"done"}`), "")
	if rec.Code != 200 {
		t.Fatalf("mark A done = %d %s", rec.Code, rec.Body.String())
	}
	rec = pgDo(h, "GET", "/v1/projects/APP/ready", nil, "")
	if rec.Code != 200 {
		t.Fatalf("GET ready after done = %d %s", rec.Code, rec.Body.String())
	}
	var ready2 struct {
		Items []pgstore.ReadyIssue `json:"items"`
		Total int                  `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ready2)
	if ready2.Total != 1 || len(ready2.Items) != 1 || ready2.Items[0].Key != issB.Key {
		t.Fatalf("ready after done = %+v, want %s", ready2, issB.Key)
	}

	// DELETE /v1/edges/{id} transitions edge to removed.
	rec = pgDo(h, "DELETE", fmt.Sprintf("/v1/edges/%d", depEdge.ID), nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"removed"`) {
		t.Fatalf("DELETE edge = %d %s", rec.Code, rec.Body.String())
	}

	// Validation: empty query and unknown field rejected cleanly.
	rec = pgDo(h, "POST", "/v1/context/query", []byte(`{"query":""}`), "")
	if rec.Code != 422 {
		t.Fatalf("empty query = %d %s, want 422", rec.Code, rec.Body.String())
	}
	rec = pgDo(h, "POST", "/v1/context/query", []byte(`{"query":"ok","bogus":1}`), "")
	if rec.Code != 400 {
		t.Fatalf("unknown field = %d %s, want 400", rec.Code, rec.Body.String())
	}
}

func TestPGContextQueryRerankerFailureReturns200Fallback(t *testing.T) {
	h, _ := newPGFullStackTestHandler(t, failingReranker{})

	if rec := pgDo(h, "POST", "/v1/projects", []byte(`{"key":"FBK","name":"Fallback"}`), ""); rec.Code != 201 {
		t.Fatalf("create project = %d %s", rec.Code, rec.Body.String())
	}
	if rec := pgDo(h, "PUT", "/v1/projects/FBK/specs/retry?wait=true&wait_timeout_ms=5000", []byte(`{"title":"Retry Policy","content":"# Retry\nExponential backoff retry guide"}`), ""); rec.Code != 201 {
		t.Fatalf("put spec = %d %s", rec.Code, rec.Body.String())
	}

	rec := pgDo(h, "POST", "/v1/context/query", []byte(`{"query":"backoff retry","project":"FBK"}`), "")
	if rec.Code != 200 {
		t.Fatalf("reranker failure should return 200 fallback, got %d %s", rec.Code, rec.Body.String())
	}
	var resp pgstore.ContextQueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.FallbackUsed || resp.Reranked || resp.FallbackMode != "rrf" || len(resp.Items) == 0 {
		t.Fatalf("unexpected fallback response: %+v", resp)
	}
}
