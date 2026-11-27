package store

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// openAt20 returns a database at schema version 20, before session waiting
// state columns were added, and runs setup on it.
func openAt20(t *testing.T, setup func(db *sql.DB)) string {
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
		if n > 20 {
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
	mustExec(t, db, "pragma user_version = 20")
	setup(db)
	return path
}

// Migration 21 adds nullable state, state_reason, and state_at columns to
// session, leaving existing rows NULL and enforcing the state check constraint.
func TestMigration21AddsSessionWaitingColumns(t *testing.T) {
	path := openAt20(t, func(db *sql.DB) {
		mustExec(t, db, `insert into actor(id, kind, created_at) values ('claude', 'agent', '2026-11-20T00:00:00.000Z')`)
		mustExec(t, db, `insert into session(actor_id, session_id, first_seen_at, last_seen_at, last_host, updated_at)
			values ('claude', 's1', '2026-11-20T00:00:00.000Z', '2026-11-20T00:00:00.000Z', 'desk', '2026-11-20T00:00:00.000Z')`)
	})
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if v := queryStrings(t, s, `pragma user_version`); v[0] != strconv.Itoa(SchemaVersion) || SchemaVersion < 21 {
		t.Errorf("user_version = %v, SchemaVersion = %d", v, SchemaVersion)
	}
	got, err := s.GetSession(t.Context(), "claude", "s1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.State != nil || got.StateReason != nil || got.StateAt != nil {
		t.Errorf("existing row waiting fields must be NULL: state=%v reason=%v at=%v", got.State, got.StateReason, got.StateAt)
	}
	for _, valid := range []string{"waiting_approval", "waiting_input", "waiting_external"} {
		if _, err := s.db.Exec(`update session set state = ? where actor_id = 'claude' and session_id = 's1'`, valid); err != nil {
			t.Errorf("valid state %q rejected: %v", valid, err)
		}
	}
	if _, err := s.db.Exec(`update session set state = null where actor_id = 'claude' and session_id = 's1'`); err != nil {
		t.Errorf("null state rejected: %v", err)
	}
	for _, invalid := range []string{"paused", "", "waiting"} {
		if _, err := s.db.Exec(`update session set state = ? where actor_id = 'claude' and session_id = 's1'`, invalid); err == nil {
			t.Errorf("invalid state %q was accepted by check constraint", invalid)
		}
	}
	if fk := queryStrings(t, s, `pragma foreign_key_check`); len(fk) != 0 {
		t.Errorf("foreign_key_check = %v", fk)
	}
}
