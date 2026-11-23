package store

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// openAt25 returns a database at schema version 25, before the session
// last_project composite indexes were added, and runs setup on it.
func openAt25(t *testing.T, setup func(db *sql.DB)) string {
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
		if n > 25 {
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
	mustExec(t, db, "pragma user_version = 25")
	if setup != nil {
		setup(db)
	}
	return path
}

func TestMigration26AddsSessionCompositeIndexes(t *testing.T) {
	path := openAt25(t, func(db *sql.DB) {
		const at = "'2026-11-20T00:00:00.000Z'"
		mustExec(t, db, `insert into actor(id, kind, created_at) values ('owner', 'user', `+at+`)`)
		mustExec(t, db, `insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
			values (1, 'APP', 'example-app', 'active', 2, `+at+`, `+at+`, 'owner', 'owner')`)
		mustExec(t, db, `insert into issue(id, project_id, number, key, title, body, type, status, priority, version,
			created_at, updated_at, status_changed_at, status_changed_by, created_by, updated_by)
			values (1, 1, 1, 'APP-1', '기존 이슈', '', 'feature', 'backlog', 'P2', 1, `+at+`, `+at+`, `+at+`, 'owner', 'owner', 'owner')`)
		mustExec(t, db, `insert into activity(id, issue_id, kind, actor_id, session_id, client_host, at)
			values (1, 1, 'created', 'owner', 's1', 'desk', `+at+`)`)
		mustExec(t, db, `insert into issue_claim(id, issue_id, actor_id, session_id, claimed_at)
			values (1, 1, 'owner', 's1', `+at+`)`)
	})

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if v := queryStrings(t, s, `pragma user_version`); v[0] != strconv.Itoa(SchemaVersion) || SchemaVersion < 26 {
		t.Errorf("user_version = %v, SchemaVersion = %d", v, SchemaVersion)
	}
	for _, idx := range []string{"activity_actor_session", "idx_issue_claim_session"} {
		var n int
		if err := s.db.QueryRow(`select count(*) from sqlite_master where type = 'index' and name = ?`, idx).Scan(&n); err != nil || n != 1 {
			t.Errorf("index %s missing (n=%d, err=%v)", idx, n, err)
		}
	}
	if fk := queryStrings(t, s, `pragma foreign_key_check`); len(fk) != 0 {
		t.Errorf("foreign_key_check = %v", fk)
	}
}

func TestMigration26OnFreshDatabase(t *testing.T) {
	s := openTest(t)
	for _, idx := range []string{"activity_actor_session", "idx_issue_claim_session"} {
		var n int
		if err := s.db.QueryRow(`select count(*) from sqlite_master where type = 'index' and name = ?`, idx).Scan(&n); err != nil || n != 1 {
			t.Errorf("index %s missing on fresh db (n=%d, err=%v)", idx, n, err)
		}
	}
}
