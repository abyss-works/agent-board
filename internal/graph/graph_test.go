package graph

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

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
