package store

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// openAt21 returns a database at schema version 21, before issue_claim was
// added, and runs setup on it.
func openAt21(t *testing.T, setup func(db *sql.DB)) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agb.db")
	db, err := sql.Open("sqlite", DSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		n, _ := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
		if n > 21 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", e.Name(), err)
		}
	}
	mustExec(t, db, "pragma user_version = 21")
	setup(db)
	return path
}

func TestMigration22CreatesIssueClaimTable(t *testing.T) {
	path := openAt21(t, func(db *sql.DB) {
		const at = "'2026-11-20T00:00:00.000Z'"
		mustExec(t, db, `insert into actor(id, kind, created_at) values ('claude', 'agent', `+at+`)`)
		mustExec(t, db, `insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
			values (1, 'AGB', 'agent-board', 'active', 2, `+at+`, `+at+`, 'claude', 'claude')`)
		mustExec(t, db, `insert into issue(id, project_id, number, key, title, type, status, priority, created_at, updated_at, status_changed_at, status_changed_by, created_by, updated_by)
			values (1, 1, 1, 'AGB-1', '기존 이슈', 'feature', 'doing', 'P2', `+at+`, `+at+`, `+at+`, 'claude', 'claude', 'claude')`)
	})

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if v := queryStrings(t, s, `pragma user_version`); v[0] != strconv.Itoa(SchemaVersion) || SchemaVersion < 22 {
		t.Errorf("user_version = %v, SchemaVersion = %d", v, SchemaVersion)
	}

	// Inserting an open claim succeeds, and a second open claim on the same issue is blocked by the partial unique index.
	if _, err := s.db.Exec(`insert into issue_claim(issue_id, actor_id, session_id, claimed_at)
		values (1, 'claude', 's1', '2026-11-20T01:00:00.000Z')`); err != nil {
		t.Fatalf("first open claim: %v", err)
	}
	if _, err := s.db.Exec(`insert into issue_claim(issue_id, actor_id, session_id, claimed_at)
		values (1, 'claude', 's2', '2026-11-20T01:01:00.000Z')`); err == nil {
		t.Error("second open claim on the same issue was allowed")
	}

	// Released claims on the same issue are allowed alongside an open claim.
	for _, oc := range []string{"done", "abandoned", "expired", "taken_over"} {
		if _, err := s.db.Exec(`insert into issue_claim(issue_id, actor_id, session_id, claimed_at, released_at, outcome)
			values (1, 'claude', 's-old', '2026-11-20T00:10:00.000Z', '2026-11-20T00:20:00.000Z', ?)`, oc); err != nil {
			t.Errorf("released claim with outcome %q rejected: %v", oc, err)
		}
	}

	// Check constraints reject invalid outcome and mismatched released_at/outcome pairs.
	if _, err := s.db.Exec(`insert into issue_claim(issue_id, actor_id, session_id, claimed_at, released_at, outcome)
		values (1, 'claude', 's3', '2026-11-20T00:10:00.000Z', '2026-11-20T00:20:00.000Z', 'invalid')`); err == nil {
		t.Error("invalid outcome was accepted")
	}
	if _, err := s.db.Exec(`insert into issue_claim(issue_id, actor_id, session_id, claimed_at, released_at, outcome)
		values (1, 'claude', 's3', '2026-11-20T00:10:00.000Z', '2026-11-20T00:20:00.000Z', null)`); err == nil {
		t.Error("released_at without outcome was accepted")
	}
	if _, err := s.db.Exec(`insert into issue_claim(issue_id, actor_id, session_id, claimed_at, released_at, outcome)
		values (1, 'claude', 's3', '2026-11-20T00:10:00.000Z', null, 'done')`); err == nil {
		t.Error("outcome without released_at was accepted")
	}

	if fk := queryStrings(t, s, `pragma foreign_key_check`); len(fk) != 0 {
		t.Errorf("foreign_key_check = %v", fk)
	}
}
