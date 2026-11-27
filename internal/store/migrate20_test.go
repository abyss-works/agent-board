package store

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// openAt19 returns a database at schema version 19, as a server before the
// sessions would leave it, and runs setup on it.
func openAt19(t *testing.T, setup func(db *sql.DB)) string {
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
		if n > 19 {
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
	mustExec(t, db, "pragma user_version = 19")
	setup(db)
	return path
}

// Migration 20 adds the session table empty: past activity makes no session.
func TestMigration20AddsAnEmptySessionTable(t *testing.T) {
	path := openAt19(t, func(db *sql.DB) {
		mustExec(t, db, `insert into actor(id, kind, created_at) values ('claude', 'agent', 'x')`)
		mustExec(t, db, `insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
			values (1, 'APP', 'APP', 'active', 1, '2026-10-01T00:00:00.000Z', '2026-10-01T00:00:00.000Z', 'claude', 'claude')`)
		mustExec(t, db, `insert into resource_activity(project_id, resource, resource_id, kind, actor_id, session_id, client_host, at, changes)
			values (1, 'project', 1, 'created', 'claude', 's1', 'pc', '2026-10-01T00:00:00.000Z', '{}')`)
	})
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if v := queryStrings(t, s, `pragma user_version`); v[0] != strconv.Itoa(SchemaVersion) || SchemaVersion < 20 {
		t.Errorf("user_version = %v, SchemaVersion = %d", v, SchemaVersion)
	}
	if n := queryStrings(t, s, `select count(*) from session`); n[0] != "0" {
		t.Errorf("sessions after the migration = %v", n)
	}
	if n := queryStrings(t, s, `select count(*) from resource_activity`); n[0] != "1" {
		t.Errorf("resource activity = %v", n)
	}
	if fk := queryStrings(t, s, `pragma foreign_key_check`); len(fk) != 0 {
		t.Errorf("foreign_key_check = %v", fk)
	}
}
