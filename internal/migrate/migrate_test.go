package migrate_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/migrate"
	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
	"github.com/wsk0715/agent-board/internal/store/pg"
)

var testPGDBSeq atomic.Int64

func sha256File(t *testing.T, path string) (string, int64, time.Time) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), fi.Size(), fi.ModTime()
}

func sha256Text(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func makeFloat32Blob(vals ...float32) []byte {
	buf := make([]byte, len(vals)*4)
	for i, v := range vals {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// buildFull24TableSQLiteFixture creates a real schema-v26 SQLite database via
// store.Open and populates every one of the 24 tables with realistic data.
func buildFull24TableSQLiteFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "source.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() {
		if st != nil {
			_ = st.Close()
		}
	}()

	db := st.DB()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}

	ts1 := "2026-10-01T10:00:00.000Z"
	ts2 := "2026-10-02T11:00:00.000Z"
	ts3 := "2026-10-03T12:00:00.000Z"
	tsExp := "2026-12-01T00:00:00.000Z"

	// 1. actor
	exec(`INSERT INTO actor(id, kind, created_at) VALUES
		('owner', 'user', ?),
		('worker', 'agent', ?),
		('reviewer', 'agent', ?)`, ts1, ts1, ts1)

	// 2. project
	exec(`INSERT INTO project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by) VALUES
		(1, 'DEMO', 'Demo App', 'active', 6, ?, ?, 'owner', 'owner'),
		(2, 'OPS', 'Operations', 'archived', 2, ?, ?, 'owner', 'owner')`, ts1, ts2, ts1, ts2)

	// 3. token
	exec(`INSERT INTO token(id, actor_id, client_host, token_hash, created_at, created_by, last_used_at, revoked_at, revoked_by, revoked_session, revoked_host) VALUES
		(1, 'worker', 'devbox', 'hash_active_1', ?, 'owner', ?, NULL, NULL, NULL, NULL),
		(2, 'reviewer', 'devbox', 'hash_revoked_2', ?, 'owner', ?, ?, 'owner', 'sess-root', 'devbox')`,
		ts1, ts2, ts1, ts2, ts3)

	// 4. milestone (open 100% done, open non-version, closed version, deleted)
	exec(`INSERT INTO milestone(id, project_id, name, description, target_date, status, closed_at, created_at, updated_at, created_by, updated_by) VALUES
		(1, 1, 'v0.1.0', 'Initial release', '2026-10-15', 'open', NULL, ?, ?, 'owner', 'owner'),
		(2, 1, 'security-hardening', 'Topic style milestone', NULL, 'open', NULL, ?, ?, 'owner', 'owner'),
		(3, 1, 'v0.2.0', 'Closed release', NULL, 'closed', ?, ?, ?, 'owner', 'owner'),
		(4, 2, 'deprecated-ms', 'Deleted milestone', NULL, 'deleted', NULL, ?, ?, 'owner', 'owner')`,
		ts1, ts1, ts1, ts1, ts3, ts1, ts3, ts1, ts2)

	// 5. topic (hierarchical + non-ASCII)
	exec(`INSERT INTO topic(id, project_id, name, parent_id, status, created_at, updated_at, created_by, updated_by) VALUES
		(1, 1, 'Backend', NULL, 'active', ?, ?, 'owner', 'owner'),
		(2, 1, 'Auth', 1, 'active', ?, ?, 'owner', 'owner'),
		(3, 1, '인프라', NULL, 'active', ?, ?, 'owner', 'owner')`,
		ts1, ts1, ts1, ts1, ts1, ts1)

	// 6. issue (2 parent issues DEMO-1, DEMO-2; 3 child issues DEMO-3, DEMO-4, DEMO-5; 1 standalone OPS-1)
	exec(`INSERT INTO issue(
		id, project_id, number, key, title, body, type, status, priority,
		parent_id, recheck_condition, decision_needed, topic_id, milestone_id, status_reason,
		acknowledged_at, acknowledged_by, verification_state, verification_note, verified_at, verified_by,
		version, created_at, updated_at, closed_at, status_changed_at, status_changed_by, created_by, updated_by
	) VALUES
		(1, 1, 1, 'DEMO-1', 'Authentication Overhaul', 'Parent issue body for auth', 'design', 'doing', 'P1',
		 NULL, NULL, 'Choose OAuth flow', 2, 2, 'backlog -> doing',
		 ?, 'owner', 'pending', 'Verify login flow in staging', NULL, NULL,
		 2, ?, ?, NULL, ?, 'worker', 'owner', 'worker'),
		(2, 1, 2, 'DEMO-2', 'Initial Bootstrap', 'Completed parent issue', 'feature', 'done', 'P2',
		 NULL, NULL, NULL, 1, 1, 'review -> done',
		 ?, 'owner', 'verified', 'Checked on staging', ?, 'owner',
		 3, ?, ?, ?, ?, 'owner', 'owner', 'owner'),
		(3, 1, 3, 'DEMO-3', 'Implement JWT validator', 'Child of DEMO-1', 'feature', 'backlog', 'P1',
		 1, NULL, NULL, 2, 2, NULL,
		 NULL, NULL, 'none', NULL, NULL, NULL,
		 1, ?, ?, NULL, ?, 'worker', 'worker', 'worker'),
		(4, 1, 4, 'DEMO-4', 'Add login rate limiter', 'Second child of DEMO-1', 'bug', 'review', 'P0',
		 1, NULL, NULL, 3, 2, 'doing -> review',
		 NULL, NULL, 'pending', 'Check burst traffic', NULL, NULL,
		 2, ?, ?, NULL, ?, 'worker', 'worker', 'worker'),
		(5, 1, 5, 'DEMO-5', 'Setup CI workflow', 'Child of DEMO-2', 'feature', 'done', 'P2',
		 2, NULL, NULL, NULL, 1, 'review -> done',
		 ?, 'owner', 'verified', 'Pipeline green', ?, 'owner',
		 2, ?, ?, ?, ?, 'owner', 'worker', 'owner'),
		(6, 2, 1, 'OPS-1', 'Monitor disk latency', 'Observation issue', 'observation', 'backlog', 'P3',
		 NULL, 'Check when IOPS > 5000', NULL, NULL, NULL, NULL,
		 NULL, NULL, 'none', NULL, NULL, NULL,
		 1, ?, ?, NULL, ?, 'owner', 'owner', 'owner')`,
		ts1, ts1, ts2, ts2,
		ts1, ts3, ts1, ts3, ts3, ts3,
		ts1, ts1, ts1,
		ts1, ts2, ts2,
		ts1, ts3, ts1, ts3, ts3, ts3,
		ts1, ts1, ts1)

	// 7. issue_label (include 'backend-auth' on DEMO-1 to test deduplication with topic 2 'Backend/Auth')
	exec(`INSERT INTO issue_label(issue_id, label) VALUES
		(1, 'backend-auth'),
		(1, 'security'),
		(3, 'api'),
		(6, 'ops')`)

	// 8. issue_dependency (child depends on parent + child depends on child)
	exec(`INSERT INTO issue_dependency(issue_id, depends_on_id) VALUES
		(3, 1),
		(4, 3)`)

	// 11. document (insert before issue_ref and document_relation)
	doc1Body := "# Auth Architecture Decision\n\nWe use stateless signed tokens with short TTL."
	doc2Body := "# Auth Architecture v2\n\nSupersedes previous auth decision with rotation."
	doc3Body := "# Legacy Ops Runbook\n\nArchived operational notes."
	exec(`INSERT INTO document(id, project_id, name, kind, date, title, tags, archived, imported, path, sha256, size, content, created_at, created_by, updated_at, updated_by) VALUES
		(1, 1, '2026-10-01-1000-decision-auth-v1.md', 'decision', '2026-10-01 10:00', 'Auth v1 Decision', '["auth","security"]', 0, 0, NULL, ?, ?, ?, ?, 'owner', ?, 'owner'),
		(2, 1, '2026-10-02-1100-decision-auth-v2.md', 'decision', '2026-10-02', 'Auth v2 Decision', '["auth","rotation"]', 0, 0, NULL, ?, ?, ?, ?, 'worker', ?, 'worker'),
		(3, 2, 'archive/legacy-runbook.md', 'other', NULL, 'Legacy Runbook', '[]', 1, 1, 'archive/legacy-runbook.md', ?, ?, ?, ?, 'owner', ?, 'owner')`,
		sha256Text(doc1Body), len(doc1Body), doc1Body, ts1, ts1,
		sha256Text(doc2Body), len(doc2Body), doc2Body, ts2, ts2,
		sha256Text(doc3Body), len(doc3Body), doc3Body, ts1, ts2)

	// 9. issue_ref (external url, issue ref to promoted parent DEMO-1, doc ref to doc 1)
	exec(`INSERT INTO issue_ref(issue_id, ref, target_issue_id, target_document_id) VALUES
		(1, 'url:https://example.com/spec', NULL, NULL),
		(3, 'issue:DEMO-1', 1, NULL),
		(1, 'doc:2026-10-01-1000-decision-auth-v1.md', NULL, 1)`)

	// 10. activity (created, updated with changes JSON, commented with summary/comment_kind)
	exec(`INSERT INTO activity(id, issue_id, kind, actor_id, session_id, client_host, at, changes, body, summary, comment_kind) VALUES
		(1, 1, 'created', 'owner', 'sess-root', 'devbox', ?, '{"title":{"to":"Authentication Overhaul"}}', NULL, NULL, NULL),
		(2, 1, 'commented', 'worker', 'sess-child', 'devbox', ?, NULL, 'Decided to keep stateless verification.', 'Stateless verification decision', 'decision'),
		(3, 3, 'updated', 'worker', 'sess-child', 'devbox', ?, '{"priority":{"from":"P2","to":"P1"}}', NULL, NULL, NULL),
		(4, 4, 'commented', 'reviewer', 'sess-grand', 'devbox', ?, NULL, 'Rate limit burst verified under load.', 'Burst check finding', 'finding')`,
		ts1, ts2, ts2, ts3)

	// 12. document_relation (supersedes doc, about promoted parent issue, removed related doc, unresolved target_text)
	exec(`INSERT INTO document_relation(id, document_id, kind, target_document_id, target_issue_id, target_text, removed_at, removed_by, removed_session, removed_host) VALUES
		(1, 2, 'supersedes', 1, NULL, NULL, NULL, NULL, NULL, NULL),
		(2, 1, 'about', NULL, 1, NULL, NULL, NULL, NULL, NULL),
		(3, 2, 'related', 3, NULL, NULL, ?, 'owner', 'sess-root', 'devbox'),
		(4, 2, 'related', NULL, NULL, 'missing-doc.md', NULL, NULL, NULL, NULL)`, ts3)

	// 13. document_activity
	exec(`INSERT INTO document_activity(id, document_id, actor_id, session_id, client_host, at, changes) VALUES
		(1, 3, 'owner', 'sess-root', 'devbox', ?, '{"archived":{"from":false,"to":true}}')`, ts2)

	// 14. resource_activity (project, milestone, and topic — topic must be skipped)
	exec(`INSERT INTO resource_activity(id, project_id, resource, resource_id, kind, actor_id, session_id, client_host, at, changes) VALUES
		(1, 1, 'project', 1, 'created', 'owner', 'sess-root', 'devbox', ?, '{"key":{"to":"DEMO"}}'),
		(2, 1, 'milestone', 1, 'created', 'owner', 'sess-root', 'devbox', ?, '{"name":{"to":"v0.1.0"}}'),
		(3, 1, 'topic', 1, 'created', 'owner', 'sess-root', 'devbox', ?, '{"name":{"to":"Backend"}}')`,
		ts1, ts1, ts1)

	// 15. message (including reply_to_id where reply has LOWER id than parent to test topological sort!)
	exec(`INSERT INTO message(id, project_id, to_label, to_actor, to_session_id, to_role, issue_id, reply_to_id, title, body, created_at, created_by, created_session, created_host, expires_at, deleted_at, deleted_by) VALUES
		(2, 1, 'auth', 'worker', 'sess-child', 'main', 1, NULL, 'Auth Review Needed', 'Please review auth flow.', ?, 'owner', 'sess-root', 'devbox', ?, NULL, NULL),
		(1, 1, NULL, 'owner', 'sess-root', 'subagent', 1, 2, 'Re: Auth Review Needed', 'Review complete.', ?, 'worker', 'sess-child', 'devbox', ?, NULL, NULL),
		(3, 1, NULL, NULL, NULL, NULL, NULL, NULL, 'Expired note', 'Soft deleted message', ?, 'owner', 'sess-root', 'devbox', ?, ?, 'owner')`,
		ts1, tsExp, ts2, tsExp, ts1, tsExp, ts3)

	// 16. message_read
	exec(`INSERT INTO message_read(message_id, reader, read_at, read_by, read_session, read_host) VALUES
		(2, 'worker', ?, 'worker', 'sess-child', 'devbox')`, ts2)

	// 17. session (grandchild -> child -> root where actor IDs sort child before parent to prove topological sorting!)
	exec(`INSERT INTO session(actor_id, session_id, first_seen_at, last_seen_at, last_host, parent_actor_id, parent_session_id, role, issue_id, location, state, state_reason, state_at, updated_at) VALUES
		('worker', 'sess-root', ?, ?, 'devbox', NULL, NULL, 'main', 1, '{"branch":"dev"}', NULL, NULL, NULL, ?),
		('owner', 'sess-child', ?, ?, 'devbox', 'worker', 'sess-root', 'subagent', 3, '{"branch":"feat"}', 'waiting_input', 'Need decision', ?, ?),
		('reviewer', 'sess-grand', ?, ?, 'devbox', 'owner', 'sess-child', 'runner', 4, NULL, NULL, NULL, NULL, ?)`,
		ts1, ts3, ts2,
		ts2, ts3, ts3, ts3,
		ts3, ts3, ts3)

	// 18. issue_claim (active claim on promoted parent issue DEMO-1 + released claim on DEMO-3)
	exec(`INSERT INTO issue_claim(id, issue_id, actor_id, session_id, claimed_at, released_at, outcome, reason) VALUES
		(1, 1, 'worker', 'sess-root', ?, NULL, NULL, 'working on parent'),
		(2, 3, 'worker', 'sess-root', ?, ?, 'done', 'finished subtask')`,
		ts1, ts1, ts2)

	// 19. event (project-scoped and global/null project_id)
	exec(`INSERT INTO event(id, at, project_id, kind, subject, actor, session_id, data) VALUES
		(1, ?, 1, 'issue.created', 'DEMO-1', 'owner', 'sess-root', '{"activity_id":1}'),
		(2, ?, NULL, 'system.check', 'health', 'worker', 'sess-root', '{}')`,
		ts1, ts2)

	// 20-22. FTS5 triggers already populated issue_fts, document_fts, comment_fts.
	// 23. embedding (derived table)
	vec := makeFloat32Blob(0.1, 0.2, 0.3, 0.4)
	exec(`INSERT INTO embedding(source, source_id, chunk, model, text_sha256, text, vector, created_at) VALUES
		('document', 1, 0, 'legacy-model', ?, 'chunk text', ?, ?)`,
		sha256Text("chunk text"), vec, ts1)

	// 24. similar_document (derived table)
	exec(`INSERT INTO similar_document(document_id, similar_id, score, model) VALUES
		(1, 2, 0.91, 'legacy-model')`)

	// Checkpoint WAL and close cleanly so the file is self-contained.
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	if err := st.Close(); err != nil {
		t.Fatalf("close source sqlite: %v", err)
	}
	st = nil
	return dbPath
}

func TestPlanReadOnlyAndFull24TableAnalysis(t *testing.T) {
	srcPath := buildFull24TableSQLiteFixture(t)
	hashBefore, sizeBefore, mtimeBefore := sha256File(t, srcPath)

	ctx := context.Background()
	report, err := migrate.Plan(ctx, srcPath)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Verify source SQLite file was never modified.
	hashAfter, sizeAfter, mtimeAfter := sha256File(t, srcPath)
	if hashBefore != hashAfter || sizeBefore != sizeAfter || !mtimeBefore.Equal(mtimeAfter) {
		t.Fatalf("source SQLite file mutated during Plan: before=(%s,%d,%v) after=(%s,%d,%v)",
			hashBefore, sizeBefore, mtimeBefore, hashAfter, sizeAfter, mtimeAfter)
	}

	if report.SchemaVersion != migrate.ExpectedSQLiteSchemaVersion {
		t.Fatalf("schema version = %d, want %d", report.SchemaVersion, migrate.ExpectedSQLiteSchemaVersion)
	}
	if len(report.Tables) != 24 {
		t.Fatalf("tables count = %d, want 24", len(report.Tables))
	}

	counts := map[string]int64{}
	for _, tc := range report.Tables {
		counts[tc.Name] = tc.RowCount
	}
	wantCounts := map[string]int64{
		"actor": 3, "project": 2, "token": 2, "milestone": 4, "topic": 3,
		"issue": 6, "issue_label": 4, "issue_dependency": 2, "issue_ref": 3,
		"activity": 4, "document": 3, "document_relation": 4, "document_activity": 1,
		"resource_activity": 3, "message": 3, "message_read": 1, "session": 3,
		"issue_claim": 2, "event": 2,
		"issue_fts": 6, "document_fts": 3, "comment_fts": 4, "embedding": 1, "similar_document": 1,
	}
	for tbl, want := range wantCounts {
		if counts[tbl] != want {
			t.Errorf("table %s count = %d, want %d", tbl, counts[tbl], want)
		}
	}

	// Epic promotions: DEMO-1 (2 children) and DEMO-2 (1 child).
	if report.TotalPromotedEpics != 2 || report.TotalAssignedChildIssues != 3 {
		t.Fatalf("epic promotions = %d (children=%d), want 2 (children=3)",
			report.TotalPromotedEpics, report.TotalAssignedChildIssues)
	}
	if report.EpicPromotions[0].SourceIssueKey != "DEMO-1" || report.EpicPromotions[0].ChildCount != 2 || report.EpicPromotions[0].TargetEpicStatus != "open" {
		t.Errorf("unexpected epic promotion[0]: %+v", report.EpicPromotions[0])
	}
	if report.EpicPromotions[1].SourceIssueKey != "DEMO-2" || report.EpicPromotions[1].ChildCount != 1 || report.EpicPromotions[1].TargetEpicStatus != "closed" {
		t.Errorf("unexpected epic promotion[1]: %+v", report.EpicPromotions[1])
	}

	// Topic mappings: Backend -> backend, Backend/Auth -> backend-auth (1 duplicate on DEMO-1), 인프라 -> topic-3.
	if len(report.TopicMappings) != 3 {
		t.Fatalf("topic mappings = %d, want 3", len(report.TopicMappings))
	}
	if report.TopicMappings[1].NormalizedLabel != "backend-auth" || report.TopicMappings[1].DuplicateLabelCount != 1 {
		t.Errorf("unexpected topic mapping[1]: %+v", report.TopicMappings[1])
	}
	if report.TopicMappings[2].NormalizedLabel != "topic-3" || !model.ValidLabel(report.TopicMappings[2].NormalizedLabel) {
		t.Errorf("unexpected topic mapping[2]: %+v", report.TopicMappings[2])
	}

	// Milestones: open->planned=2, closed->closed=1, deleted->deleted=1;
	// v0.1.0 is 100% done while open; security-hardening is non-version.
	if report.MilestoneSummary.StatusConversions["open->planned"] != 2 ||
		report.MilestoneSummary.StatusConversions["closed->closed"] != 1 ||
		report.MilestoneSummary.StatusConversions["deleted->deleted"] != 1 {
		t.Errorf("unexpected milestone conversions: %+v", report.MilestoneSummary.StatusConversions)
	}
	if len(report.MilestoneSummary.CompletedOpenCandidates) != 1 || report.MilestoneSummary.CompletedOpenCandidates[0].Name != "v0.1.0" {
		t.Errorf("unexpected completed open candidates: %+v", report.MilestoneSummary.CompletedOpenCandidates)
	}
	if len(report.MilestoneSummary.NonVersionCandidates) != 1 || report.MilestoneSummary.NonVersionCandidates[0].Name != "security-hardening" {
		t.Errorf("unexpected non-version candidates: %+v", report.MilestoneSummary.NonVersionCandidates)
	}

	// Document & relation & resource_activity checks.
	if len(report.DocumentIssues) != 0 {
		t.Errorf("unexpected document issues: %+v", report.DocumentIssues)
	}
	if report.UnresolvedDocumentRelations != 1 || report.RemovedDocumentRelations != 1 {
		t.Errorf("doc relations unresolved=%d removed=%d, want 1, 1", report.UnresolvedDocumentRelations, report.RemovedDocumentRelations)
	}
	if report.TopicResourceActivityCount != 1 {
		t.Errorf("topic resource activity count = %d, want 1", report.TopicResourceActivityCount)
	}
	if len(report.DuplicateOpenClaims) != 0 || len(report.BlockingIssues) != 0 {
		t.Errorf("unexpected duplicate claims=%+v blocking=%+v", report.DuplicateOpenClaims, report.BlockingIssues)
	}

	// FormatTable & JSON output smoke test.
	var buf bytes.Buffer
	if err := report.FormatTable(&buf); err != nil {
		t.Fatalf("FormatTable: %v", err)
	}
	out := buf.String()
	for _, needle := range []string{"Migration Plan", "DEMO-1", "backend-auth", "security-hardening", "Topic Resource Activity Rows"} {
		if !strings.Contains(out, needle) {
			t.Errorf("FormatTable missing %q:\n%s", needle, out)
		}
	}
	if _, err := json.Marshal(report); err != nil {
		t.Fatalf("json.Marshal(report): %v", err)
	}
}

func TestPlanDetectsBlockingDocumentsAndDuplicateOpenClaims(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "bad.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	db := st.DB()
	ts := "2026-10-01T10:00:00.000Z"

	for _, q := range []string{
		`INSERT INTO actor(id, kind, created_at) VALUES ('owner', 'user', '` + ts + `')`,
		`INSERT INTO project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
		 VALUES (1, 'DEMO', 'Demo', 'active', 2, '` + ts + `', '` + ts + `', 'owner', 'owner')`,
		`INSERT INTO issue(id, project_id, number, key, title, body, type, status, priority, version, created_at, updated_at, status_changed_at, status_changed_by, created_by, updated_by)
		 VALUES (1, 1, 1, 'DEMO-1', 'T', '', 'feature', 'backlog', 'P2', 1, '` + ts + `', '` + ts + `', '` + ts + `', 'owner', 'owner', 'owner')`,
		// Drop open claim unique index to simulate corrupted duplicate active claims.
		`DROP INDEX idx_issue_claim_open`,
		`INSERT INTO issue_claim(id, issue_id, actor_id, session_id, claimed_at) VALUES
		 (1, 1, 'owner', 's1', '` + ts + `'),
		 (2, 1, 'owner', 's2', '` + ts + `')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}

	// Insert >2MiB document, NUL byte document, and invalid UTF-8 document.
	oversized := strings.Repeat("a", model.MaxDocumentBytes+1)
	withNul := "hello\x00world"
	invalidUTF8 := string([]byte{0xff, 0xfe, 'x'})
	for idx, tc := range []struct {
		name    string
		content string
	}{
		{"2026-10-01-1000-report-big.md", oversized},
		{"2026-10-01-1001-report-nul.md", withNul},
		{"2026-10-01-1002-report-badutf8.md", invalidUTF8},
	} {
		if _, err := db.Exec(
			`INSERT INTO document(id, project_id, name, kind, title, tags, archived, imported, sha256, size, content, created_at, created_by, updated_at, updated_by)
			 VALUES (?, 1, ?, 'report', 'Bad', '[]', 0, 0, ?, ?, ?, ?, 'owner', ?, 'owner')`,
			idx+1, tc.name, sha256Text(tc.content), len(tc.content), tc.content, ts, ts,
		); err != nil {
			t.Fatalf("insert bad doc %s: %v", tc.name, err)
		}
	}
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	_ = st.Close()

	report, err := migrate.Plan(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	reasons := map[string]bool{}
	for _, di := range report.DocumentIssues {
		reasons[di.Reason] = true
	}
	for _, want := range []string{"exceeds_2mib", "contains_nul_byte", "invalid_utf8"} {
		if !reasons[want] {
			t.Errorf("expected document issue reason %q, got %+v", want, report.DocumentIssues)
		}
	}
	if len(report.BlockingIssues) < 3 {
		t.Errorf("expected >= 3 blocking issues, got %+v", report.BlockingIssues)
	}
	if len(report.DuplicateOpenClaims) != 1 || report.DuplicateOpenClaims[0].IssueID != 1 || len(report.DuplicateOpenClaims[0].ClaimIDs) != 2 {
		t.Errorf("expected 1 duplicate open claim on issue 1, got %+v", report.DuplicateOpenClaims)
	}
}

func TestRedactDSNAndSanitizeError(t *testing.T) {
	dsn := "postgres://alice:s3cr3tP%40ss!@db.internal:5432/appdb?sslmode=disable"
	msg := fmt.Sprintf("dial failed for %s (user=alice:s3cr3tP%%40ss! password=s3cr3tP@ss!)", dsn)
	redacted := migrate.RedactDSN(msg, dsn)
	for _, secret := range []string{"s3cr3t", "alice:s3cr3t", "P%40ss!", "P@ss!"} {
		if strings.Contains(redacted, secret) {
			t.Errorf("redacted message leaked %q: %s", secret, redacted)
		}
	}
	kv := migrate.RedactDSN("connect host=127.0.0.1 user=bob password=topsecretvalue dbname=x", "")
	if strings.Contains(kv, "topsecretvalue") {
		t.Errorf("kv redaction leaked password: %s", kv)
	}
	sanitized := migrate.SanitizeError(errors.New("connect failed: "+dsn), dsn)
	if strings.Contains(sanitized.Error(), "s3cr3t") {
		t.Errorf("SanitizeError leaked secret: %v", sanitized)
	}
}

// --- Live PostgreSQL 17 integration tests ---

func testPGDSN(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("AGB_TEST_PG_DSN"))
	if dsn == "" {
		t.Skip("AGB_TEST_PG_DSN not set")
	}
	return dsn
}

func createIsolatedPGDB(t *testing.T, baseDSN string) (*pg.Store, string) {
	t.Helper()
	ctx := context.Background()
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse AGB_TEST_PG_DSN: %v", err)
	}
	dbName := fmt.Sprintf("agb_t_mig_%d_%d", os.Getpid(), testPGDBSeq.Add(1))
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
	dsn := u.String()
	st, err := pg.Open(ctx, dsn, 8)
	if err != nil {
		t.Fatalf("pg.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, dsn
}

func TestRunVerifyAndSequenceSyncOnPG17(t *testing.T) {
	baseDSN := testPGDSN(t)
	srcPath := buildFull24TableSQLiteFixture(t)
	hashBefore, sizeBefore, _ := sha256File(t, srcPath)

	st, dsn := createIsolatedPGDB(t, baseDSN)
	ctx := context.Background()

	// 1. Run full 1:1 migration (default EnqueueBackfill = true when nil).
	res, err := migrate.Run(ctx, migrate.Options{
		SourcePath: srcPath,
		TargetDSN:  dsn,
		Pool:       st.Pool(),
	})
	if err != nil {
		t.Fatalf("migrate.Run: %v", err)
	}

	if res.PromotedEpics != 2 || res.AssignedChildIssues != 3 {
		t.Fatalf("promoted epics=%d assigned children=%d, want 2, 3", res.PromotedEpics, res.AssignedChildIssues)
	}
	if res.SkippedTopicResourceActivity != 1 {
		t.Errorf("skipped topic resource_activity = %d, want 1", res.SkippedTopicResourceActivity)
	}
	// Expected backfill jobs: 6 issues + 2 comment activities + 3 record documents = 11.
	if res.EnqueuedBackfillJobs != 11 {
		t.Errorf("enqueued backfill jobs = %d, want 11", res.EnqueuedBackfillJobs)
	}
	// Derived 5 tables must be reported in SkippedDerivedTables.
	for _, derived := range []string{"issue_fts", "document_fts", "comment_fts", "embedding", "similar_document"} {
		if res.SkippedDerivedTables[derived] < 1 {
			t.Errorf("expected derived table %s count >= 1, got %d", derived, res.SkippedDerivedTables[derived])
		}
	}

	// 2. Post-migration integrity verification.
	ver, err := migrate.Verify(ctx, srcPath, st.Pool())
	if err != nil {
		t.Fatalf("migrate.Verify: %v", err)
	}
	if !ver.OK {
		var buf bytes.Buffer
		_ = ver.FormatTable(&buf)
		t.Fatalf("Verify failed:\n%s", buf.String())
	}
	if !ver.GlobalSeedOK {
		t.Fatalf("GLOBAL seed check failed")
	}
	if ver.DocumentHashChecked != 3 || len(ver.DocumentHashMismatches) != 0 {
		t.Errorf("doc hash checked=%d mismatches=%v", ver.DocumentHashChecked, ver.DocumentHashMismatches)
	}
	if ver.EpicFKChecked != 2 || len(ver.EpicFKViolations) != 0 {
		t.Errorf("epic fk checked=%d violations=%v", ver.EpicFKChecked, ver.EpicFKViolations)
	}
	if len(ver.SequenceChecks) != len(migrate.PGIdentityTables) {
		t.Errorf("sequence checks = %d, want %d", len(ver.SequenceChecks), len(migrate.PGIdentityTables))
	}

	// 3. Verify GLOBAL id=0 preservation and SQLite project id=1..2 1:1 preservation.
	var globalKey, globalScope string
	if err := st.Pool().QueryRow(ctx, `SELECT key, scope_kind FROM project WHERE id = 0`).Scan(&globalKey, &globalScope); err != nil {
		t.Fatalf("query GLOBAL project: %v", err)
	}
	if globalKey != "GLOBAL" || globalScope != "global" {
		t.Fatalf("GLOBAL project = (%q, %q), want (GLOBAL, global)", globalKey, globalScope)
	}

	// 4. Verify verification_state 4 columns preserved on DEMO-2.
	var vState string
	var vNote, vBy *string
	var vAt *time.Time
	if err := st.Pool().QueryRow(ctx,
		`SELECT verification_state, verification_note, verified_at, verified_by FROM issue WHERE key = 'DEMO-2'`,
	).Scan(&vState, &vNote, &vAt, &vBy); err != nil {
		t.Fatalf("query DEMO-2 verification columns: %v", err)
	}
	if vState != "verified" || vNote == nil || *vNote != "Checked on staging" || vAt == nil || vBy == nil || *vBy != "owner" {
		t.Errorf("unexpected verification columns on DEMO-2: state=%q note=%v at=%v by=%v", vState, vNote, vAt, vBy)
	}

	// 5. Verify sequence synchronization: creating new objects via pg.Store must NOT collide!
	tr := pg.Trace{Actor: "owner", SessionID: "sess-post-mig", ClientHost: "devbox"}
	newProj, err := st.CreateProject(ctx, tr, pg.CreateProjectInput{Key: "NEWP", Name: "New Project"})
	if err != nil {
		t.Fatalf("CreateProject after migration: %v", err)
	}
	if newProj.ID <= 2 {
		t.Errorf("new project ID = %d, expected > 2", newProj.ID)
	}
	newEpic, err := st.CreateEpic(ctx, tr, "DEMO", pg.CreateEpicInput{Title: "Post Migration Epic"})
	if err != nil {
		t.Fatalf("CreateEpic after migration: %v", err)
	}
	if newEpic.ID <= 2 {
		t.Errorf("new epic ID = %d, expected > 2", newEpic.ID)
	}
	newMS, err := st.CreateMilestone(ctx, tr, "DEMO", pg.CreateMilestoneInput{Name: "v0.3.0"})
	if err != nil {
		t.Fatalf("CreateMilestone after migration: %v", err)
	}
	if newMS.ID <= 4 {
		t.Errorf("new milestone ID = %d, expected > 4", newMS.ID)
	}
	newIss, err := st.CreateIssue(ctx, tr, "DEMO", pg.CreateIssueInput{
		Title:    "Post migration issue",
		Type:     model.TypeFeature,
		Priority: model.P2,
		EpicID:   &newEpic.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue after migration: %v", err)
	}
	if newIss.Issue.ID <= 6 {
		t.Errorf("new issue ID = %d, expected > 6", newIss.Issue.ID)
	}
	newComment, err := st.AddComment(ctx, tr, "DEMO-1", pg.AddCommentInput{Body: "Post migration comment on promoted parent issue"})
	if err != nil {
		t.Fatalf("AddComment on promoted parent issue DEMO-1: %v", err)
	}
	if newComment.Activity.ID <= 4 {
		t.Errorf("new activity ID = %d, expected > 4", newComment.Activity.ID)
	}
	newRec, err := st.CreateRecord(ctx, tr, "DEMO", pg.CreateRecordInput{
		Name:    "2026-10-10-1000-report-post-mig.md",
		Kind:    "report",
		Title:   "Post Migration Report",
		Content: "# Post Migration\n\nSequence test.",
	})
	if err != nil {
		t.Fatalf("CreateRecord after migration: %v", err)
	}
	if newRec.Record.ID <= 3 {
		t.Errorf("new record_document ID = %d, expected > 3", newRec.Record.ID)
	}

	// 6. Re-running migration into a non-empty database must fail with ErrTargetNotEmpty.
	_, err = migrate.Run(ctx, migrate.Options{SourcePath: srcPath, Pool: st.Pool()})
	if !errors.Is(err, migrate.ErrTargetNotEmpty) {
		t.Errorf("second Run error = %v, want ErrTargetNotEmpty", err)
	}

	// 7. Verify source SQLite remained untouched throughout Run + Verify.
	hashAfter, sizeAfter, _ := sha256File(t, srcPath)
	if hashBefore != hashAfter || sizeBefore != sizeAfter {
		t.Fatalf("source SQLite mutated during Run/Verify")
	}
}

func TestRunNoBackfillAndDuplicateOpenClaimDefenseOnPG17(t *testing.T) {
	baseDSN := testPGDSN(t)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dupclaims.db")
	sqlSt, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	db := sqlSt.DB()
	ts1 := "2026-10-01T10:00:00.000Z"
	ts2 := "2026-10-01T11:00:00.000Z"
	for _, q := range []string{
		`INSERT INTO actor(id, kind, created_at) VALUES ('owner', 'user', '` + ts1 + `')`,
		`INSERT INTO project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
		 VALUES (1, 'DEMO', 'Demo', 'active', 2, '` + ts1 + `', '` + ts1 + `', 'owner', 'owner')`,
		`INSERT INTO issue(id, project_id, number, key, title, body, type, status, priority, version, created_at, updated_at, status_changed_at, status_changed_by, created_by, updated_by)
		 VALUES (1, 1, 1, 'DEMO-1', 'T', '', 'feature', 'backlog', 'P2', 1, '` + ts1 + `', '` + ts1 + `', '` + ts1 + `', 'owner', 'owner', 'owner')`,
		`DROP INDEX idx_issue_claim_open`,
		`INSERT INTO issue_claim(id, issue_id, actor_id, session_id, claimed_at) VALUES
		 (1, 1, 'owner', 's1', '` + ts1 + `'),
		 (2, 1, 'owner', 's2', '` + ts2 + `')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	_ = sqlSt.Close()

	st, _ := createIsolatedPGDB(t, baseDSN)
	ctx := context.Background()

	res, err := migrate.Run(ctx, migrate.Options{
		SourcePath:      dbPath,
		Pool:            st.Pool(),
		EnqueueBackfill: migrate.Bool(false),
	})
	if err != nil {
		t.Fatalf("migrate.Run with duplicate open claims: %v", err)
	}
	if res.ResolvedDuplicateClaims != 1 {
		t.Errorf("ResolvedDuplicateClaims = %d, want 1", res.ResolvedDuplicateClaims)
	}
	if res.EnqueuedBackfillJobs != 0 {
		t.Errorf("EnqueuedBackfillJobs = %d, want 0 with EnqueueBackfill=false", res.EnqueuedBackfillJobs)
	}

	ver, err := migrate.Verify(ctx, dbPath, st.Pool())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ver.OK {
		t.Fatalf("Verify failed: %v", ver.Failures)
	}

	// Verify claim id=1 was closed as taken_over and claim id=2 remains open.
	var openID int64
	if err := st.Pool().QueryRow(ctx, `SELECT id FROM issue_claim WHERE released_at IS NULL`).Scan(&openID); err != nil {
		t.Fatalf("query open claim: %v", err)
	}
	if openID != 2 {
		t.Errorf("open claim id = %d, want 2", openID)
	}
	var outcome sql.NullString
	if err := st.Pool().QueryRow(ctx, `SELECT outcome FROM issue_claim WHERE id = 1`).Scan(&outcome); err != nil {
		t.Fatalf("query resolved claim: %v", err)
	}
	if !outcome.Valid || outcome.String != "taken_over" {
		t.Errorf("resolved claim outcome = %+v, want taken_over", outcome)
	}
}
