package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/model"
)

var testDBSeq atomic.Int64

func testPGDSN(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("AGB_TEST_PG_DSN"))
	if dsn == "" {
		t.Skip("AGB_TEST_PG_DSN not set")
	}
	return dsn
}

func createIsolatedTestDB(t *testing.T, baseDSN string) string {
	t.Helper()
	ctx := context.Background()
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse AGB_TEST_PG_DSN: %v", err)
	}
	dbName := fmt.Sprintf("agb_t_pg_%d_%d", os.Getpid(), testDBSeq.Add(1))
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
	return u.String()
}

func openCleanStore(t *testing.T) (*Store, string) {
	t.Helper()
	baseDSN := testPGDSN(t)
	dsn := createIsolatedTestDB(t, baseDSN)
	ctx := context.Background()

	st, err := Open(ctx, dsn, 8)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.EnsureActors(ctx, []model.Actor{
		{ID: "owner", Kind: model.ActorUser},
		{ID: "worker", Kind: model.ActorAgent},
	}); err != nil {
		t.Fatalf("EnsureActors: %v", err)
	}
	return st, dsn
}

var (
	trOwner  = Trace{Actor: "owner", SessionID: "sess-1", ClientHost: "devbox"}
	trWorker = Trace{Actor: "worker", SessionID: "sess-2", ClientHost: "devbox"}
)

func TestMigrations26TablesAndGlobalSeed(t *testing.T) {
	st, dsn := openCleanStore(t)
	ctx := context.Background()

	wantTables := []string{
		"actor", "token", "project", "epic", "milestone", "issue", "issue_label",
		"spec_document", "spec_revision", "record_document", "issue_ref",
		"activity", "document_activity", "resource_activity",
		"message", "message_read", "session", "issue_claim", "event",
		"ingest_job", "normalized_artifact", "rag_chunk", "embedding_generation",
		"rag_embedding", "query_embedding_cache", "graph_edge",
	}
	rows, err := st.Pool().Query(ctx,
		`SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	got := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		got[name] = true
	}
	rows.Close()

	for _, tbl := range wantTables {
		if !got[tbl] {
			t.Errorf("missing expected table %q", tbl)
		}
	}
	if !got["schema_migrations"] {
		t.Errorf("missing schema_migrations table")
	}

	// Verify idempotent re-open on already migrated database.
	st2, err := Open(ctx, dsn, 4)
	if err != nil {
		t.Fatalf("re-open migrated db: %v", err)
	}
	_ = st2.Close()

	// Verify DBSize > 0.
	size, err := st.DBSize(ctx)
	if err != nil || size <= 0 {
		t.Fatalf("DBSize = %d, err = %v", size, err)
	}

	// Verify GLOBAL seed project has id = 0 and cannot be deleted.
	globalProj, err := st.GetProject(ctx, model.GlobalProjectKey)
	if err != nil {
		t.Fatalf("GetProject(GLOBAL): %v", err)
	}
	if globalProj.ID != model.GlobalProjectID || globalProj.ScopeKind != model.ScopeGlobal {
		t.Fatalf("GLOBAL project = %+v, want id=0 scope_kind=global", globalProj)
	}
	if _, err := st.DeleteProject(ctx, trOwner, model.GlobalProjectKey); !errors.Is(err, ErrGlobalProjectProtected) {
		t.Fatalf("DeleteProject(GLOBAL) err = %v, want ErrGlobalProjectProtected", err)
	}

	// Verify next project gets id = 1 without colliding with GLOBAL id = 0.
	proj, err := st.CreateProject(ctx, trOwner, CreateProjectInput{Key: "DEMO", Name: "Demo Project"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if proj.ID != 1 || proj.ScopeKind != model.ScopeProject {
		t.Fatalf("first regular project = %+v, want id=1 scope_kind=project", proj)
	}
}

func TestPGroongaCompositePartialIndexExplainAnalyze(t *testing.T) {
	st, _ := openCleanStore(t)
	ctx := context.Background()

	p1, err := st.CreateProject(ctx, trOwner, CreateProjectInput{Key: "ALPHA", Name: "Alpha"})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := st.CreateProject(ctx, trOwner, CreateProjectInput{Key: "BETA", Name: "Beta"})
	if err != nil {
		t.Fatal(err)
	}

	// Populate rag_chunk with active and archived rows across projects and GLOBAL.
	for i := 1; i <= 200; i++ {
		pid := p2.ID
		archived := false
		text := fmt.Sprintf("일반 운영 로그 항목 번호 %d 시스템 점검 기록", i)
		switch {
		case i == 10:
			pid = p1.ID
			text = "검색 투영 인덱스 크래시 안전성과 쿼리 플래너 검증 보고서"
		case i == 20:
			pid = model.GlobalProjectID
			text = "전역 아키텍처 원칙과 검색 투영 인덱스 운영 지침"
		case i == 30:
			pid = p1.ID
			archived = true
			text = "보관된 검색 투영 인덱스 과거 초안"
		}
		if _, err := st.Pool().Exec(ctx,
			`INSERT INTO rag_chunk (project_id, source_type, source_id, chunk_index, chunk_text, search_text, content_sha256, archived)
			 VALUES ($1, 'record', $2, 0, $3, $3, $4, $5)`,
			pid, int64(i), text, sha256Hex(text), archived,
		); err != nil {
			t.Fatalf("insert chunk %d: %v", i, err)
		}
	}
	if _, err := st.Pool().Exec(ctx, `ANALYZE rag_chunk`); err != nil {
		t.Fatalf("ANALYZE rag_chunk: %v", err)
	}

	// Confirm the query matches both target project and GLOBAL (id=0), excluding archived rows.
	var matchedIDs []int64
	mRows, err := st.Pool().Query(ctx,
		`SELECT project_id FROM rag_chunk
		 WHERE NOT archived AND project_id IN ($1, 0) AND search_text &@~ $2
		 ORDER BY project_id ASC`,
		p1.ID, "검색 투영 인덱스",
	)
	if err != nil {
		t.Fatalf("pgroonga query: %v", err)
	}
	for mRows.Next() {
		var pid int64
		if err := mRows.Scan(&pid); err != nil {
			mRows.Close()
			t.Fatal(err)
		}
		matchedIDs = append(matchedIDs, pid)
	}
	mRows.Close()
	if len(matchedIDs) != 2 || matchedIDs[0] != 0 || matchedIDs[1] != p1.ID {
		t.Fatalf("matched project_ids = %v, want [0, %d]", matchedIDs, p1.ID)
	}

	// Run EXPLAIN (ANALYZE) and assert idx_rag_chunk_pgroonga_active is selected via index scan.
	planRows, err := st.Pool().Query(ctx,
		`EXPLAIN (ANALYZE, FORMAT TEXT)
		 SELECT id, project_id FROM rag_chunk
		 WHERE NOT archived AND project_id IN ($1, 0) AND search_text &@~ $2`,
		p1.ID, "검색 투영 인덱스",
	)
	if err != nil {
		t.Fatalf("EXPLAIN ANALYZE: %v", err)
	}
	var planLines []string
	for planRows.Next() {
		var line string
		if err := planRows.Scan(&line); err != nil {
			planRows.Close()
			t.Fatal(err)
		}
		planLines = append(planLines, line)
	}
	planRows.Close()
	plan := strings.Join(planLines, "\n")
	t.Logf("EXPLAIN (ANALYZE) plan:\n%s", plan)

	if !strings.Contains(plan, "idx_rag_chunk_pgroonga_active") {
		t.Fatalf("expected idx_rag_chunk_pgroonga_active in EXPLAIN plan:\n%s", plan)
	}
	if !strings.Contains(plan, "Index Scan") && !strings.Contains(plan, "Bitmap Index Scan") {
		t.Fatalf("expected Index Scan or Bitmap Index Scan in EXPLAIN plan:\n%s", plan)
	}
}

func TestActiveMilestoneScopeLockAndUnverifiedCloseGuard(t *testing.T) {
	st, _ := openCleanStore(t)
	ctx := context.Background()

	if _, err := st.CreateProject(ctx, trOwner, CreateProjectInput{Key: "APP", Name: "App"}); err != nil {
		t.Fatal(err)
	}
	ep, err := st.CreateEpic(ctx, trOwner, "APP", CreateEpicInput{Title: "Core Engine"})
	if err != nil {
		t.Fatal(err)
	}
	if ep.Key != "APP-EPIC-1" {
		t.Fatalf("epic key = %q, want APP-EPIC-1", ep.Key)
	}

	ms, err := st.CreateMilestone(ctx, trOwner, "APP", CreateMilestoneInput{Name: "v1.0", Description: "First release"})
	if err != nil {
		t.Fatal(err)
	}
	if ms.Status != model.MilestonePlanned {
		t.Fatalf("initial milestone status = %q, want planned", ms.Status)
	}
	active := model.MilestoneActive
	ms, err = st.UpdateMilestone(ctx, trOwner, ms.ID, MilestonePatch{Status: &active})
	if err != nil {
		t.Fatal(err)
	}

	// Non-P0 non-bug into active milestone without force -> ErrMilestoneScopeLocked.
	_, err = st.CreateIssue(ctx, trOwner, "APP", CreateIssueInput{
		Title:       "Regular feature",
		Type:        model.TypeFeature,
		Priority:    model.P2,
		EpicID:      &ep.ID,
		MilestoneID: &ms.ID,
	})
	if !errors.Is(err, ErrMilestoneScopeLocked) {
		t.Fatalf("CreateIssue non-P0 feature into active milestone err = %v, want ErrMilestoneScopeLocked", err)
	}

	// force=true without reason -> ValidationError.
	_, err = st.CreateIssue(ctx, trOwner, "APP", CreateIssueInput{
		Title:       "Forced feature missing reason",
		Type:        model.TypeFeature,
		Priority:    model.P2,
		MilestoneID: &ms.ID,
		Force:       true,
		Reason:      "   ",
	})
	var verr *model.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("force without reason err = %v, want *model.ValidationError", err)
	}

	// force=true with non-empty reason -> allowed and recorded in activity.
	forced, err := st.CreateIssue(ctx, trOwner, "APP", CreateIssueInput{
		Title:       "Forced feature with reason",
		Type:        model.TypeFeature,
		Priority:    model.P2,
		MilestoneID: &ms.ID,
		Force:       true,
		Reason:      "urgent customer deadline",
	})
	if err != nil {
		t.Fatalf("CreateIssue with force+reason: %v", err)
	}
	acts, total, err := st.ListIssueActivities(ctx, forced.Issue.Key, 10, 0)
	if err != nil || total != 1 {
		t.Fatalf("activities = %d, err = %v", total, err)
	}
	rawOverride, ok := acts[0].Changes["milestone_scope_override"]
	if !ok {
		t.Fatalf("missing milestone_scope_override in activity changes: %+v", acts[0].Changes)
	}
	var ov map[string]any
	if err := json.Unmarshal(rawOverride.To, &ov); err != nil || ov["force"] != true || ov["reason"] != "urgent customer deadline" {
		t.Fatalf("override payload = %s, parsed = %+v", string(rawOverride.To), ov)
	}

	// Bug issue is allowed into active milestone without force; set verification_state=pending.
	note := "device QA needed"
	bugRes, err := st.CreateIssue(ctx, trOwner, "APP", CreateIssueInput{
		Title:             "Critical bug",
		Type:              model.TypeBug,
		Priority:          model.P1,
		MilestoneID:       &ms.ID,
		VerificationState: model.VerificationPending,
		VerificationNote:  &note,
	})
	if err != nil {
		t.Fatalf("CreateIssue bug into active milestone: %v", err)
	}

	// Closing milestone while pending verification issue exists -> *UnverifiedIssuesError.
	closed := model.MilestoneClosed
	_, err = st.UpdateMilestone(ctx, trOwner, ms.ID, MilestonePatch{Status: &closed})
	var uerr *UnverifiedIssuesError
	if !errors.As(err, &uerr) || len(uerr.Issues) != 1 || uerr.Issues[0].Key != bugRes.Issue.Key {
		t.Fatalf("close milestone with pending issue err = %v, want *UnverifiedIssuesError for %s", err, bugRes.Issue.Key)
	}

	// Verify the issue and close milestone -> succeeds.
	verified := model.VerificationVerified
	if _, err := st.UpdateIssue(ctx, trOwner, bugRes.Issue.Key, &bugRes.Issue.Version, IssuePatch{
		VerificationState: &verified,
	}); err != nil {
		t.Fatalf("verify issue: %v", err)
	}
	msClosed, err := st.UpdateMilestone(ctx, trOwner, ms.ID, MilestonePatch{Status: &closed})
	if err != nil {
		t.Fatalf("close milestone after verify: %v", err)
	}
	if msClosed.Status != model.MilestoneClosed || msClosed.ClosedAt == nil {
		t.Fatalf("closed milestone = %+v", msClosed)
	}
}

func TestSpecsRecordsValidationImmutabilityAndIngestNotify(t *testing.T) {
	st, _ := openCleanStore(t)
	ctx := context.Background()

	if _, err := st.CreateProject(ctx, trOwner, CreateProjectInput{Key: "DOC", Name: "Docs"}); err != nil {
		t.Fatal(err)
	}

	// Open a dedicated LISTEN connection on agb_ingest_job to verify pg_notify.
	listenConn, err := st.Pool().Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire listen conn: %v", err)
	}
	defer listenConn.Release()
	if _, err := listenConn.Exec(ctx, `LISTEN `+IngestNotifyChannel); err != nil {
		t.Fatalf("LISTEN: %v", err)
	}

	expectNotify := func(wantJobID int64) {
		t.Helper()
		wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		n, err := listenConn.Conn().WaitForNotification(wctx)
		if err != nil {
			t.Fatalf("WaitForNotification for job %d: %v", wantJobID, err)
		}
		if n.Channel != IngestNotifyChannel || n.Payload != fmt.Sprintf("%d", wantJobID) {
			t.Fatalf("notification = (%s, %q), want (%s, %d)", n.Channel, n.Payload, IngestNotifyChannel, wantJobID)
		}
	}

	// Validation: > 2 MiB -> ErrPayloadTooLarge, invalid UTF-8 -> ErrInvalidUTF8, NUL -> ErrBinaryPayload.
	tooLarge := strings.Repeat("a", model.MaxDocumentBytes+1)
	if _, err := st.UpsertSpec(ctx, trOwner, "DOC", "big", UpsertSpecInput{Title: "Big", Content: tooLarge}); !errors.Is(err, model.ErrPayloadTooLarge) {
		t.Fatalf("spec > 2MiB err = %v, want ErrPayloadTooLarge", err)
	}
	if _, err := st.UpsertSpec(ctx, trOwner, "DOC", "bad-utf8", UpsertSpecInput{Title: "Bad", Content: string([]byte{0xff, 0xfe})}); !errors.Is(err, model.ErrInvalidUTF8) {
		t.Fatalf("spec invalid utf8 err = %v, want ErrInvalidUTF8", err)
	}
	if _, err := st.UpsertSpec(ctx, trOwner, "DOC", "bin", UpsertSpecInput{Title: "Bin", Content: "abc\x00def"}); !errors.Is(err, model.ErrBinaryPayload) {
		t.Fatalf("spec nul byte err = %v, want ErrBinaryPayload", err)
	}

	// Upsert spec rev 1 & rev 2 -> append-only revisions + pg_notify.
	sp1, err := st.UpsertSpec(ctx, trOwner, "DOC", "architecture", UpsertSpecInput{
		Title: "Architecture", Tags: []string{"core"}, Content: "# v1\nhello", ChangeSummary: "initial",
	})
	if err != nil || !sp1.Created || sp1.Spec.CurrentRevisionNo != 1 || sp1.IngestJobID <= 0 {
		t.Fatalf("UpsertSpec v1 = %+v, err = %v", sp1, err)
	}
	expectNotify(sp1.IngestJobID)

	sp2, err := st.UpsertSpec(ctx, trOwner, "DOC", "architecture", UpsertSpecInput{
		Title: "Architecture v2", Tags: []string{"core", "pg"}, Content: "# v2\nupdated", ChangeSummary: "rev2",
	})
	if err != nil || sp2.Created || sp2.Spec.CurrentRevisionNo != 2 || sp2.IngestJobID <= sp1.IngestJobID {
		t.Fatalf("UpsertSpec v2 = %+v, err = %v", sp2, err)
	}
	expectNotify(sp2.IngestJobID)

	revs, err := st.ListSpecRevisions(ctx, "DOC", "architecture")
	if err != nil || len(revs) != 2 || revs[0].RevisionNo != 2 || revs[1].RevisionNo != 1 {
		t.Fatalf("revisions = %+v, err = %v", revs, err)
	}

	// Record validation & immutability.
	if _, err := st.CreateRecord(ctx, trOwner, "DOC", CreateRecordInput{Name: "big.md", Kind: "decision", Content: tooLarge}); !errors.Is(err, model.ErrPayloadTooLarge) {
		t.Fatalf("record > 2MiB err = %v, want ErrPayloadTooLarge", err)
	}
	rec1, err := st.CreateRecord(ctx, trOwner, "DOC", CreateRecordInput{
		Name: "2026-11-28-decision-a.md", Kind: "decision", Title: "Decision A", Content: "# Decision A\nbody",
	})
	if err != nil || !rec1.Created || rec1.IngestJobID <= 0 {
		t.Fatalf("CreateRecord = %+v, err = %v", rec1, err)
	}
	expectNotify(rec1.IngestJobID)

	// Same sha256 -> idempotent (Created=false).
	recSame, err := st.CreateRecord(ctx, trOwner, "DOC", CreateRecordInput{
		Name: "2026-11-28-decision-a.md", Kind: "decision", Title: "Decision A", Content: "# Decision A\nbody",
	})
	if err != nil || recSame.Created || recSame.Record.ID != rec1.Record.ID {
		t.Fatalf("idempotent CreateRecord = %+v, err = %v", recSame, err)
	}

	// Different content -> *ImmutableError with stored sha256.
	_, err = st.CreateRecord(ctx, trOwner, "DOC", CreateRecordInput{
		Name: "2026-11-28-decision-a.md", Kind: "decision", Title: "Decision A", Content: "# Decision A\ntampered",
	})
	var ierr *ImmutableError
	if !errors.As(err, &ierr) || ierr.SHA256 != rec1.Record.SHA256 {
		t.Fatalf("tampered CreateRecord err = %v, want *ImmutableError with sha256 %s", err, rec1.Record.SHA256)
	}

	// Archive transition allowed.
	arch, err := st.SetRecordArchived(ctx, trOwner, "DOC", "2026-11-28-decision-a.md", true)
	if err != nil || !arch.Archived {
		t.Fatalf("SetRecordArchived = %+v, err = %v", arch, err)
	}

	// Claims, messages, tokens, events smoke check.
	iss, err := st.CreateIssue(ctx, trOwner, "DOC", CreateIssueInput{Title: "Work item", Type: model.TypeFeature})
	if err != nil {
		t.Fatal(err)
	}
	expectNotify(iss.IngestJobID)

	if _, created, err := st.ClaimIssue(ctx, trWorker, iss.Issue.Key, "working"); err != nil || !created {
		t.Fatalf("ClaimIssue = %v, %v", created, err)
	}
	if _, _, err := st.ClaimIssue(ctx, trOwner, iss.Issue.Key, "conflict"); err == nil {
		t.Fatalf("expected ErrIssueClaimed for second session")
	}
	if _, err := st.ReleaseIssueClaim(ctx, trWorker, iss.Issue.Key, model.ClaimOutcomeDone, nil); err != nil {
		t.Fatalf("ReleaseIssueClaim: %v", err)
	}

	msg, err := st.CreateMessage(ctx, trOwner, "DOC", CreateMessageInput{Title: "Hello", Body: "World"})
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if _, err := st.MarkMessageRead(ctx, trWorker, msg.ID, "worker"); err != nil {
		t.Fatalf("MarkMessageRead: %v", err)
	}
	evs, _, err := st.ListEvents(ctx, EventFilter{After: 0, Limit: 50})
	if err != nil || len(evs) == 0 {
		t.Fatalf("ListEvents = %d, err = %v", len(evs), err)
	}
}

func TestConcurrentFirstWriteSpecAndRecord(t *testing.T) {
	st, _ := openCleanStore(t)
	ctx := context.Background()

	if _, err := st.CreateProject(ctx, trOwner, CreateProjectInput{Key: "RACE", Name: "Race"}); err != nil {
		t.Fatal(err)
	}

	// Concurrent first UpsertSpec on the same slug: both must succeed with revisions 1 and 2.
	specErrs := make(chan error, 2)
	specRevs := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			res, err := st.UpsertSpec(ctx, trOwner, "RACE", "concurrent-spec", UpsertSpecInput{
				Title:   fmt.Sprintf("Title %d", idx),
				Content: fmt.Sprintf("# body %d", idx),
			})
			specErrs <- err
			specRevs <- res.Spec.CurrentRevisionNo
		}(i)
	}
	seenRevs := map[int]bool{}
	for i := 0; i < 2; i++ {
		if err := <-specErrs; err != nil {
			t.Fatalf("concurrent UpsertSpec err = %v", err)
		}
		seenRevs[<-specRevs] = true
	}
	if !seenRevs[1] || !seenRevs[2] {
		t.Fatalf("expected revisions 1 and 2, got %v", seenRevs)
	}

	// Concurrent first CreateRecord with identical content: both succeed (one Created=true, one Created=false).
	recErrs := make(chan error, 2)
	recCreated := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		go func() {
			res, err := st.CreateRecord(ctx, trOwner, "RACE", CreateRecordInput{
				Name: "2026-11-28-decision-same.md", Kind: "decision", Title: "Same", Content: "# identical",
			})
			recErrs <- err
			recCreated <- res.Created
		}()
	}
	createdCount := 0
	for i := 0; i < 2; i++ {
		if err := <-recErrs; err != nil {
			t.Fatalf("concurrent identical CreateRecord err = %v", err)
		}
		if <-recCreated {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("createdCount = %d, want 1", createdCount)
	}

	// Concurrent first CreateRecord with different content: one succeeds, one gets *ImmutableError.
	diffErrs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			_, err := st.CreateRecord(ctx, trOwner, "RACE", CreateRecordInput{
				Name: "2026-11-28-decision-diff.md", Kind: "decision", Title: "Diff", Content: fmt.Sprintf("# body %d", idx),
			})
			diffErrs <- err
		}(i)
	}
	okCount, immCount := 0, 0
	for i := 0; i < 2; i++ {
		err := <-diffErrs
		var ierr *ImmutableError
		switch {
		case err == nil:
			okCount++
		case errors.As(err, &ierr):
			immCount++
		default:
			t.Fatalf("unexpected concurrent diff CreateRecord err = %v", err)
		}
	}
	if okCount != 1 || immCount != 1 {
		t.Fatalf("okCount=%d immCount=%d, want 1 and 1", okCount, immCount)
	}
}

func TestPaginationTotalsClaimReasonAndMarkReadIdempotency(t *testing.T) {
	st, _ := openCleanStore(t)
	ctx := context.Background()

	if _, err := st.CreateProject(ctx, trOwner, CreateProjectInput{Key: "PAGE", Name: "Page"}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := st.CreateEpic(ctx, trOwner, "PAGE", CreateEpicInput{Title: fmt.Sprintf("Epic %d", i)}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateIssue(ctx, trOwner, "PAGE", CreateIssueInput{Title: fmt.Sprintf("Issue %d", i)}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpsertSpec(ctx, trOwner, "PAGE", fmt.Sprintf("spec-%d", i), UpsertSpecInput{Title: fmt.Sprintf("Spec %d", i), Content: "body"}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateRecord(ctx, trOwner, "PAGE", CreateRecordInput{Name: fmt.Sprintf("rec-%d.md", i), Kind: "decision", Title: "T", Content: "body"}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateMessage(ctx, trOwner, "PAGE", CreateMessageInput{Title: fmt.Sprintf("Msg %d", i), Body: "hello"}); err != nil {
			t.Fatal(err)
		}
	}

	epics, epTotal, err := st.ListEpics(ctx, "PAGE", EpicFilter{Limit: 1, Offset: 1})
	if err != nil || len(epics) != 1 || epTotal != 3 {
		t.Fatalf("ListEpics len=%d total=%d err=%v", len(epics), epTotal, err)
	}
	issues, issTotal, err := st.ListIssues(ctx, "PAGE", IssueFilter{Limit: 1, Offset: 1})
	if err != nil || len(issues) != 1 || issTotal != 3 {
		t.Fatalf("ListIssues len=%d total=%d err=%v", len(issues), issTotal, err)
	}
	specs, spTotal, err := st.ListSpecs(ctx, "PAGE", SpecFilter{Limit: 1, Offset: 1})
	if err != nil || len(specs) != 1 || spTotal != 3 {
		t.Fatalf("ListSpecs len=%d total=%d err=%v", len(specs), spTotal, err)
	}
	recs, recTotal, err := st.ListRecords(ctx, "PAGE", RecordFilter{Limit: 1, Offset: 1})
	if err != nil || len(recs) != 1 || recTotal != 3 {
		t.Fatalf("ListRecords len=%d total=%d err=%v", len(recs), recTotal, err)
	}
	msgs, msgTotal, err := st.ListMessages(ctx, "PAGE", MessageFilter{Limit: 1, Offset: 1})
	if err != nil || len(msgs) != 1 || msgTotal != 3 {
		t.Fatalf("ListMessages len=%d total=%d err=%v", len(msgs), msgTotal, err)
	}

	// ClaimIssue validates reason with CleanClaimReason and preserves reason on idempotent re-claim.
	if _, _, err := st.ClaimIssue(ctx, trWorker, issues[0].Key, "bad\x07ctrl"); err == nil {
		t.Fatalf("expected validation error for control char in claim reason")
	}
	c1, created, err := st.ClaimIssue(ctx, trWorker, issues[0].Key, "  implementing feature  ")
	if err != nil || !created || c1.Reason == nil || *c1.Reason != "implementing feature" {
		t.Fatalf("ClaimIssue c1 = %+v created=%v err=%v", c1, created, err)
	}
	c2, created2, err := st.ClaimIssue(ctx, trWorker, issues[0].Key, "")
	if err != nil || created2 || c2.Reason == nil || *c2.Reason != "implementing feature" {
		t.Fatalf("idempotent ClaimIssue lost reason: c2=%+v created=%v err=%v", c2, created2, err)
	}

	// Duplicate MarkMessageRead does not re-emit message.read event.
	beforeEvs, _, _ := st.ListEvents(ctx, EventFilter{After: 0, Kinds: []string{model.EventMessageRead}, Limit: 50})
	if _, err := st.MarkMessageRead(ctx, trWorker, msgs[0].ID, "worker"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MarkMessageRead(ctx, trWorker, msgs[0].ID, "worker"); err != nil {
		t.Fatal(err)
	}
	afterEvs, _, _ := st.ListEvents(ctx, EventFilter{After: 0, Kinds: []string{model.EventMessageRead}, Limit: 50})
	if len(afterEvs)-len(beforeEvs) != 1 {
		t.Fatalf("duplicate MarkMessageRead emitted %d events, want 1", len(afterEvs)-len(beforeEvs))
	}

	// Epic validation: bad key and cross-project/missing source_issue_id -> ValidationError (never FK 500).
	var verr *model.ValidationError
	if _, err := st.CreateEpic(ctx, trOwner, "PAGE", CreateEpicInput{Key: "bad key!", Title: "Bad"}); !errors.As(err, &verr) {
		t.Fatalf("bad epic key err = %v, want ValidationError", err)
	}
	missingID := int64(999999)
	if _, err := st.CreateEpic(ctx, trOwner, "PAGE", CreateEpicInput{Title: "Bad Ref", SourceIssueID: &missingID}); !errors.As(err, &verr) {
		t.Fatalf("missing source_issue_id err = %v, want ValidationError", err)
	}
}
