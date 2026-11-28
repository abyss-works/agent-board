package graph

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store/pg"
)

var graphTestDBSeq atomic.Int64

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
	dbName := fmt.Sprintf("agb_t_graph_%d_%d", os.Getpid(), graphTestDBSeq.Add(1))
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
		{ID: "worker", Kind: model.ActorAgent},
	}); err != nil {
		t.Fatalf("EnsureActors: %v", err)
	}
	return st
}

var trOwner = pg.Trace{Actor: "owner", SessionID: "sess-graph", ClientHost: "devbox"}

func TestValidateAndOverlapUnit(t *testing.T) {
	if err := validateEdgeInput(pg.UpsertEdgeInput{
		ProjectID: 1,
		Source:    pg.NodeRef{Type: model.GraphNodeIssue, ID: 1},
		Kind:      model.EdgeKindRelated,
	}); err == nil {
		t.Fatal("expected error when neither target nor unresolved_target_text is set")
	}

	self := pg.UpsertEdgeInput{
		ProjectID: 1,
		Source:    pg.NodeRef{Type: model.GraphNodeIssue, ID: 7},
		Target:    &pg.NodeRef{Type: model.GraphNodeIssue, ID: 7},
		Kind:      model.EdgeKindDependsOn,
	}
	if !isSelfLoop(self) {
		t.Fatal("expected self-loop detection")
	}

	if ok, _ := detectSupersedeCue("기존 캐시 정책을 대체한다"); !ok {
		t.Fatal("expected Korean supersede cue detection")
	}
	if ok, _ := detectSupersedeCue("This decision supersedes the old cache rule"); !ok {
		t.Fatal("expected English supersede cue detection")
	}

	a := tokenizeForOverlap("PostgreSQL 벡터 검색 인덱스 구성 결정")
	b := tokenizeForOverlap("PostgreSQL 벡터 검색 인덱스 교체 전략")
	score, shared := tokenOverlap(a, b)
	if score < 0.4 || len(shared) < 3 {
		t.Fatalf("unexpected overlap score=%v shared=%v", score, shared)
	}
}

func TestExplicitEdgesIdempotencyAndCycleDetection(t *testing.T) {
	st := openTestPGStore(t)
	ctx := context.Background()
	gs := NewStore(st.Pool())

	proj, err := st.CreateProject(ctx, trOwner, pg.CreateProjectInput{Key: "DEMO", Name: "Demo"})
	if err != nil {
		t.Fatal(err)
	}

	i1, err := st.CreateIssue(ctx, trOwner, proj.Key, pg.CreateIssueInput{Title: "First", Type: model.TypeFeature, Priority: model.P1})
	if err != nil {
		t.Fatal(err)
	}
	i2, err := st.CreateIssue(ctx, trOwner, proj.Key, pg.CreateIssueInput{Title: "Second", Type: model.TypeFeature, Priority: model.P1})
	if err != nil {
		t.Fatal(err)
	}
	i3, err := st.CreateIssue(ctx, trOwner, proj.Key, pg.CreateIssueInput{Title: "Third", Type: model.TypeFeature, Priority: model.P1})
	if err != nil {
		t.Fatal(err)
	}

	ref1 := pg.NodeRef{Type: model.GraphNodeIssue, ID: i1.Issue.ID}
	ref2 := pg.NodeRef{Type: model.GraphNodeIssue, ID: i2.Issue.ID}
	ref3 := pg.NodeRef{Type: model.GraphNodeIssue, ID: i3.Issue.ID}

	// Self-loop blocked.
	if _, err := gs.UpsertExplicitEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: ref1, Target: &ref1, Kind: model.EdgeKindDependsOn,
	}); !errors.Is(err, ErrCycle) {
		t.Fatalf("self-loop err = %v, want ErrCycle", err)
	}

	// i1 -> i2 -> i3 depends_on chain.
	e12, err := gs.UpsertExplicitEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: ref1, Target: &ref2, Kind: model.EdgeKindDependsOn,
	})
	if err != nil {
		t.Fatalf("i1->i2: %v", err)
	}
	if e12.Origin != model.EdgeOriginExplicit || e12.Status != model.EdgeStatusConfirmed {
		t.Fatalf("unexpected origin/status: %+v", e12)
	}
	// Idempotent re-upsert keeps the same edge ID.
	e12Again, err := gs.UpsertExplicitEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: ref1, Target: &ref2, Kind: model.EdgeKindDependsOn,
	})
	if err != nil || e12Again.ID != e12.ID {
		t.Fatalf("idempotent upsert got id=%d err=%v, want id=%d", e12Again.ID, err, e12.ID)
	}

	if _, err := gs.UpsertExplicitEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: ref2, Target: &ref3, Kind: model.EdgeKindDependsOn,
	}); err != nil {
		t.Fatalf("i2->i3: %v", err)
	}

	// Closing the cycle i3 -> i1 must fail with ErrCycle.
	if _, err := gs.UpsertExplicitEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: ref3, Target: &ref1, Kind: model.EdgeKindDependsOn,
	}); !errors.Is(err, ErrCycle) {
		t.Fatalf("i3->i1 cycle err = %v, want ErrCycle", err)
	}

	// Verify supersedes cycle detection as well.
	r1, err := st.CreateRecord(ctx, trOwner, proj.Key, pg.CreateRecordInput{
		Name: "2026-10-01-1000-decision-cache-v1.md", Kind: "decision", Title: "Cache v1", Content: "# Cache v1\nInitial",
	})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := st.CreateRecord(ctx, trOwner, proj.Key, pg.CreateRecordInput{
		Name: "2026-10-02-1000-decision-cache-v2.md", Kind: "decision", Title: "Cache v2", Content: "# Cache v2\nNext",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec1 := pg.NodeRef{Type: model.GraphNodeRecord, ID: r1.Record.ID}
	rec2 := pg.NodeRef{Type: model.GraphNodeRecord, ID: r2.Record.ID}

	if _, err := gs.UpsertExplicitEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: rec2, Target: &rec1, Kind: model.EdgeKindSupersedes,
	}); err != nil {
		t.Fatalf("rec2 supersedes rec1: %v", err)
	}
	if _, err := gs.UpsertExplicitEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: rec1, Target: &rec2, Kind: model.EdgeKindSupersedes,
	}); !errors.Is(err, ErrCycle) {
		t.Fatalf("rec1 supersedes rec2 cycle err = %v, want ErrCycle", err)
	}
}

func TestInferredEdgesPolicyAndRejectedProtection(t *testing.T) {
	st := openTestPGStore(t)
	ctx := context.Background()
	gs := NewStore(st.Pool())

	proj, err := st.CreateProject(ctx, trOwner, pg.CreateProjectInput{Key: "CORE", Name: "Core"})
	if err != nil {
		t.Fatal(err)
	}
	iss, err := st.CreateIssue(ctx, trOwner, proj.Key, pg.CreateIssueInput{Title: "Query latency", Type: model.TypeDesign, Priority: model.P1})
	if err != nil {
		t.Fatal(err)
	}
	r1, err := st.CreateRecord(ctx, trOwner, proj.Key, pg.CreateRecordInput{
		Name: "2026-10-01-0900-decision-index-v1.md", Kind: "decision", Title: "Index v1", Content: "# Index v1\nOld plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := st.CreateRecord(ctx, trOwner, proj.Key, pg.CreateRecordInput{
		Name: "2026-10-05-0900-decision-index-v2.md", Kind: "decision", Title: "Index v2", Content: "# Index v2\nNew plan",
	})
	if err != nil {
		t.Fatal(err)
	}

	rec1 := pg.NodeRef{Type: model.GraphNodeRecord, ID: r1.Record.ID}
	rec2 := pg.NodeRef{Type: model.GraphNodeRecord, ID: r2.Record.ID}
	issRef := pg.NodeRef{Type: model.GraphNodeIssue, ID: iss.Issue.ID}
	conf := 0.88
	ev := "high semantic similarity"

	// about and related are immediately confirmed.
	aboutEdge, err := gs.UpsertInferredEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: rec2, Target: &issRef, Kind: model.EdgeKindAbout, Confidence: &conf, Evidence: &ev,
	})
	if err != nil {
		t.Fatalf("inferred about: %v", err)
	}
	if aboutEdge.Origin != model.EdgeOriginInferred || aboutEdge.Status != model.EdgeStatusConfirmed {
		t.Fatalf("about edge = (%s, %s), want (inferred, confirmed)", aboutEdge.Origin, aboutEdge.Status)
	}

	relEdge, err := gs.UpsertInferredEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: rec2, Target: &rec1, Kind: model.EdgeKindRelated, Confidence: &conf, Evidence: &ev,
	})
	if err != nil {
		t.Fatalf("inferred related: %v", err)
	}
	if relEdge.Origin != model.EdgeOriginInferred || relEdge.Status != model.EdgeStatusConfirmed {
		t.Fatalf("related edge = (%s, %s), want (inferred, confirmed)", relEdge.Origin, relEdge.Status)
	}

	// supersedes MUST stay candidate and never auto-confirm.
	supEdge, err := gs.UpsertInferredEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: rec2, Target: &rec1, Kind: model.EdgeKindSupersedes, Confidence: &conf, Evidence: &ev,
	})
	if err != nil {
		t.Fatalf("inferred supersedes: %v", err)
	}
	if supEdge.Origin != model.EdgeOriginInferred || supEdge.Status != model.EdgeStatusCandidate {
		t.Fatalf("supersedes edge = (%s, %s), want (inferred, candidate)", supEdge.Origin, supEdge.Status)
	}

	cands, err := gs.ListCandidatesForSource(ctx, model.GraphNodeRecord, r2.Record.ID)
	if err != nil {
		t.Fatalf("ListCandidatesForSource: %v", err)
	}
	if len(cands) != 1 || cands[0].EdgeID != supEdge.ID || cands[0].TargetName != r1.Record.Name {
		t.Fatalf("candidates = %+v", cands)
	}

	// Reject the candidate, verify double-decision returns ErrEdgeAlreadyDecided.
	rejected, err := gs.RejectEdge(ctx, "owner", supEdge.ID)
	if err != nil {
		t.Fatalf("RejectEdge: %v", err)
	}
	if rejected.Status != model.EdgeStatusRejected {
		t.Fatalf("rejected status = %s, want rejected", rejected.Status)
	}
	if _, err := gs.ConfirmEdge(ctx, "owner", supEdge.ID); !errors.Is(err, pg.ErrEdgeAlreadyDecided) {
		t.Fatalf("ConfirmEdge after reject err = %v, want ErrEdgeAlreadyDecided", err)
	}

	// Re-inserting the same inferred supersedes pair MUST NOT overwrite or recreate a candidate.
	newConf := 0.99
	reInferred, err := gs.UpsertInferredEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: rec2, Target: &rec1, Kind: model.EdgeKindSupersedes, Confidence: &newConf, Evidence: &ev,
	})
	if err != nil {
		t.Fatalf("re-infer after reject: %v", err)
	}
	if reInferred.ID != supEdge.ID || reInferred.Status != model.EdgeStatusRejected {
		t.Fatalf("re-inferred edge = %+v, want preserved rejected edge %d", reInferred, supEdge.ID)
	}
	candsAfter, err := gs.ListCandidatesForSource(ctx, model.GraphNodeRecord, r2.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(candsAfter) != 0 {
		t.Fatalf("expected 0 candidates after rejection, got %+v", candsAfter)
	}
}

func TestExtractorPreservesSourceAndExtractsExplicitAndCandidateEdges(t *testing.T) {
	st := openTestPGStore(t)
	ctx := context.Background()
	gs := NewStore(st.Pool())
	ext := NewExtractor(st.Pool(), gs)

	proj, err := st.CreateProject(ctx, trOwner, pg.CreateProjectInput{Key: "DEMO", Name: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	iss, err := st.CreateIssue(ctx, trOwner, proj.Key, pg.CreateIssueInput{Title: "Search ranking", Type: model.TypeFeature, Priority: model.P1})
	if err != nil {
		t.Fatal(err)
	}
	specRes, err := st.UpsertSpec(ctx, trOwner, proj.Key, "search-ranking", pg.UpsertSpecInput{
		Title: "Search Ranking Spec", Kind: "spec", Content: "# Search Ranking\nHybrid scoring details.",
	})
	if err != nil {
		t.Fatal(err)
	}
	oldRec, err := st.CreateRecord(ctx, trOwner, proj.Key, pg.CreateRecordInput{
		Name:    "2026-10-01-1100-decision-ranking-weights.md",
		Kind:    "decision",
		Title:   "Ranking weights initial choice",
		Content: "# Ranking weights\nInitial reciprocal rank fusion weights for hybrid search.",
	})
	if err != nil {
		t.Fatal(err)
	}

	rawBody := fmt.Sprintf(`# Updated Ranking Weights

Related to issue:%s and spec:%s.
This decision supersedes the prior %s approach by switching reciprocal rank fusion weights and reranker cutoff thresholds.
`, iss.Issue.Key, specRes.Spec.Slug, oldRec.Record.Name)

	newRec, err := st.CreateRecord(ctx, trOwner, proj.Key, pg.CreateRecordInput{
		Name:    "2026-10-10-1100-decision-ranking-weights-v2.md",
		Kind:    "decision",
		Title:   "Ranking weights updated choice",
		Content: rawBody,
	})
	if err != nil {
		t.Fatal(err)
	}

	report, err := ext.ExtractAndStore(ctx, ExtractSource{
		ProjectID:  proj.ID,
		ProjectKey: proj.Key,
		SourceType: model.GraphNodeRecord,
		SourceID:   newRec.Record.ID,
		KeyOrName:  newRec.Record.Name,
		Kind:       newRec.Record.Kind,
		Title:      newRec.Record.Title,
		Body:       rawBody,
	})
	if err != nil {
		t.Fatalf("ExtractAndStore: %v", err)
	}
	if len(report.Explicit) < 2 {
		t.Fatalf("expected explicit refs for issue and spec, got %+v", report.Explicit)
	}

	cands, err := gs.ListCandidatesForSource(ctx, model.GraphNodeRecord, newRec.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].TargetID != oldRec.Record.ID || cands[0].TargetName != oldRec.Record.Name {
		t.Fatalf("expected 1 supersedes candidate pointing to old record, got %+v", cands)
	}
	if cands[0].Confidence == nil || *cands[0].Confidence <= 0 || cands[0].Evidence == nil || *cands[0].Evidence == "" {
		t.Fatalf("expected confidence and evidence on candidate: %+v", cands[0])
	}

	// Confirm source record content in domain table was never mutated.
	reloaded, err := st.GetRecord(ctx, proj.Key, newRec.Record.Name)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Content == nil || *reloaded.Content != rawBody {
		t.Fatal("source record content was modified unexpectedly")
	}
}

func TestDiamondDAGCycleCheckAndConcurrency(t *testing.T) {
	st := openTestPGStore(t)
	ctx := context.Background()
	gs := NewStore(st.Pool())

	proj, err := st.CreateProject(ctx, trOwner, pg.CreateProjectInput{Key: "DAG", Name: "DAG Test"})
	if err != nil {
		t.Fatal(err)
	}

	// Build a 30-layer diamond DAG (60 nodes, 2^30 distinct paths).
	const layers = 30
	nodes := make([][2]pg.NodeRef, layers)
	for i := 0; i < layers; i++ {
		for j := 0; j < 2; j++ {
			iss, err := st.CreateIssue(ctx, trOwner, proj.Key, pg.CreateIssueInput{
				Title: fmt.Sprintf("L%d-N%d", i, j), Type: model.TypeFeature, Priority: model.P2,
			})
			if err != nil {
				t.Fatal(err)
			}
			nodes[i][j] = pg.NodeRef{Type: model.GraphNodeIssue, ID: iss.Issue.ID}
		}
	}
	for i := 0; i < layers-1; i++ {
		for src := 0; src < 2; src++ {
			for dst := 0; dst < 2; dst++ {
				target := nodes[i+1][dst]
				if _, err := gs.UpsertExplicitEdge(ctx, pg.UpsertEdgeInput{
					ProjectID: proj.ID,
					Source:    nodes[i][src],
					Target:    &target,
					Kind:      model.EdgeKindDependsOn,
				}); err != nil {
					t.Fatalf("layer %d (%d->%d): %v", i, src, dst, err)
				}
			}
		}
	}

	start := time.Now()
	// Forward edge across the full diamond must succeed quickly in O(V+E).
	sink := nodes[layers-1][1]
	if _, err := gs.UpsertExplicitEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: nodes[0][0], Target: &sink, Kind: model.EdgeKindDependsOn,
	}); err != nil {
		t.Fatalf("forward diamond edge failed: %v", err)
	}
	// Back-edge from sink to root must return ErrCycle quickly.
	root := nodes[0][0]
	if _, err := gs.UpsertExplicitEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: nodes[layers-1][0], Target: &root, Kind: model.EdgeKindDependsOn,
	}); !errors.Is(err, ErrCycle) {
		t.Fatalf("back-edge err = %v, want ErrCycle", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("diamond DAG cycle check took %v, expected O(V+E) completion", elapsed)
	}

	// Concurrent ConfirmEdge vs UpsertInferredEdge on supersedes edges must not deadlock.
	recs := make([]pg.NodeRef, 12)
	for i := range recs {
		r, err := st.CreateRecord(ctx, trOwner, proj.Key, pg.CreateRecordInput{
			Name:    fmt.Sprintf("2026-10-%02d-1000-decision-node-%d.md", i+1, i),
			Kind:    "decision",
			Title:   fmt.Sprintf("Node %d", i),
			Content: fmt.Sprintf("# Node %d\nBody", i),
		})
		if err != nil {
			t.Fatal(err)
		}
		recs[i] = pg.NodeRef{Type: model.GraphNodeRecord, ID: r.Record.ID}
	}
	var candidateIDs []int64
	for i := 1; i < len(recs); i++ {
		target := recs[i-1]
		e, err := gs.UpsertInferredEdge(ctx, pg.UpsertEdgeInput{
			ProjectID: proj.ID, Source: recs[i], Target: &target, Kind: model.EdgeKindSupersedes,
		})
		if err != nil {
			t.Fatal(err)
		}
		candidateIDs = append(candidateIDs, e.ID)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(candidateIDs)*4)
	for idx, edgeID := range candidateIDs {
		wg.Add(2)
		go func(id int64) {
			defer wg.Done()
			if _, err := gs.ConfirmEdge(ctx, "owner", id); err != nil && !errors.Is(err, pg.ErrEdgeAlreadyDecided) && !errors.Is(err, ErrCycle) {
				errCh <- fmt.Errorf("ConfirmEdge(%d): %w", id, err)
			}
		}(edgeID)
		go func(i int) {
			defer wg.Done()
			target := recs[i]
			conf := 0.9
			if _, err := gs.UpsertInferredEdge(ctx, pg.UpsertEdgeInput{
				ProjectID: proj.ID, Source: recs[i+1], Target: &target, Kind: model.EdgeKindSupersedes, Confidence: &conf,
			}); err != nil && !errors.Is(err, ErrCycle) {
				errCh <- fmt.Errorf("UpsertInferredEdge(%d): %w", i, err)
			}
		}(idx)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	// Concurrent upserts of the same about and related edges must succeed without 23505 errors.
	const workers = 8
	var aboutIDs sync.Map
	var relIDs sync.Map
	errCh2 := make(chan error, workers*4)
	for w := 0; w < workers; w++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			target := nodes[0][0]
			e, err := gs.UpsertExplicitEdge(ctx, pg.UpsertEdgeInput{
				ProjectID: proj.ID, Source: recs[0], Target: &target, Kind: model.EdgeKindAbout,
			})
			if err != nil {
				errCh2 <- fmt.Errorf("concurrent explicit about: %w", err)
				return
			}
			aboutIDs.Store(e.ID, true)
		}()
		go func() {
			defer wg.Done()
			target := recs[1]
			conf := 0.8
			e, err := gs.UpsertInferredEdge(ctx, pg.UpsertEdgeInput{
				ProjectID: proj.ID, Source: recs[0], Target: &target, Kind: model.EdgeKindRelated, Confidence: &conf,
			})
			if err != nil {
				errCh2 <- fmt.Errorf("concurrent inferred related: %w", err)
				return
			}
			relIDs.Store(e.ID, true)
		}()
	}
	wg.Wait()
	close(errCh2)
	for err := range errCh2 {
		t.Fatal(err)
	}
	var aboutCount, relCount int
	aboutIDs.Range(func(_, _ any) bool { aboutCount++; return true })
	relIDs.Range(func(_, _ any) bool { relCount++; return true })
	if aboutCount != 1 || relCount != 1 {
		t.Fatalf("expected single converged edge IDs, got about=%d related=%d", aboutCount, relCount)
	}
}

type queryCounterTracer struct {
	issueLookups atomic.Int64
}

func (q *queryCounterTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "FROM issue WHERE key") {
		q.issueLookups.Add(1)
	}
	return ctx
}

func (q *queryCounterTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestExtractorDeduplicatesAndCapsRepeatedReferences(t *testing.T) {
	st := openTestPGStore(t)
	ctx := context.Background()

	proj, err := st.CreateProject(ctx, trOwner, pg.CreateProjectInput{Key: "CAP", Name: "Cap Test"})
	if err != nil {
		t.Fatal(err)
	}
	iss, err := st.CreateIssue(ctx, trOwner, proj.Key, pg.CreateIssueInput{
		Title: "Target issue", Type: model.TypeFeature, Priority: model.P1,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := st.CreateRecord(ctx, trOwner, proj.Key, pg.CreateRecordInput{
		Name:    "2026-10-01-1000-note-repeat.md",
		Kind:    "note",
		Title:   "Repeat test",
		Content: "seed",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Build a traced pool against the same test database to count exact DB lookups.
	cfg := st.Pool().Config().Copy()
	tracer := &queryCounterTracer{}
	cfg.ConnConfig.Tracer = tracer
	tracedPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pgxpool.NewWithConfig: %v", err)
	}
	defer tracedPool.Close()

	ext := NewExtractor(tracedPool, NewStore(tracedPool))

	// Case 1: Same issue key repeated 10,000 times must resolve with only 1 DB lookup.
	repeatedBody := strings.Repeat("See "+iss.Issue.Key+" and issue:"+iss.Issue.Key+". ", 10000)
	report, err := ext.ExtractAndStore(ctx, ExtractSource{
		ProjectID:  proj.ID,
		ProjectKey: proj.Key,
		SourceType: model.GraphNodeRecord,
		SourceID:   rec.Record.ID,
		KeyOrName:  rec.Record.Name,
		Kind:       rec.Record.Kind,
		Title:      rec.Record.Title,
		Body:       repeatedBody,
	})
	if err != nil {
		t.Fatalf("ExtractAndStore repeated: %v", err)
	}
	if len(report.Explicit) != 1 {
		t.Fatalf("expected 1 explicit edge, got %d", len(report.Explicit))
	}
	if got := tracer.issueLookups.Load(); got != 1 {
		t.Fatalf("issue DB lookups for 10,000 repeats = %d, want 1", got)
	}

	// Case 2: 300 distinct unresolved keys must be bounded by maxExtractedRefsPerDoc.
	tracer.issueLookups.Store(0)
	var distinctBuf strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&distinctBuf, "FAKE-%d ", i)
	}
	if _, err := ext.ExtractAndStore(ctx, ExtractSource{
		ProjectID:  proj.ID,
		ProjectKey: proj.Key,
		SourceType: model.GraphNodeRecord,
		SourceID:   rec.Record.ID,
		KeyOrName:  rec.Record.Name,
		Kind:       rec.Record.Kind,
		Title:      rec.Record.Title,
		Body:       distinctBuf.String(),
	}); err != nil {
		t.Fatalf("ExtractAndStore distinct: %v", err)
	}
	if got := tracer.issueLookups.Load(); got > maxExtractedRefsPerDoc {
		t.Fatalf("issue DB lookups for 300 distinct keys = %d, want <= %d", got, maxExtractedRefsPerDoc)
	}
}
