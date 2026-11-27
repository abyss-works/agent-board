package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// openAt23 returns a database at schema version 23, before message routing
// columns were added, and runs setup on it.
func openAt23(t *testing.T, setup func(db *sql.DB)) string {
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
		if n > 23 {
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
	mustExec(t, db, "pragma user_version = 23")
	setup(db)
	return path
}

func TestMigration24AddsMessageRoutingColumns(t *testing.T) {
	path := openAt23(t, func(db *sql.DB) {
		const at = "'2026-11-20T00:00:00.000Z'"
		const exp = "'2026-11-27T00:00:00.000Z'"
		mustExec(t, db, `insert into actor(id, kind, created_at) values ('claude', 'agent', `+at+`)`)
		mustExec(t, db, `insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
			values (1, 'APP', 'example-app', 'active', 2, `+at+`, `+at+`, 'claude', 'claude')`)
		mustExec(t, db, `insert into issue(id, project_id, number, key, title, body, type, status, priority, version,
			created_at, updated_at, status_changed_at, status_changed_by, created_by, updated_by)
			values (1, 1, 1, 'APP-1', '기존 이슈', '', 'feature', 'backlog', 'P2', 1, `+at+`, `+at+`, `+at+`, 'claude', 'claude', 'claude')`)
		mustExec(t, db, `insert into message(id, project_id, to_label, title, body, created_at, created_by, created_session, created_host, expires_at)
			values (1, 1, 'desk', '기존 메시지', '본문', `+at+`, 'claude', 's1', 'host', `+exp+`)`)
	})

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if v := queryStrings(t, s, `pragma user_version`); v[0] != strconv.Itoa(SchemaVersion) || SchemaVersion < 24 {
		t.Errorf("user_version = %v, SchemaVersion = %d", v, SchemaVersion)
	}

	for _, idx := range []string{"idx_message_to_session", "idx_message_to_role", "idx_message_issue", "idx_message_reply_to"} {
		var n int
		if err := s.db.QueryRow(`select count(*) from sqlite_master where type = 'index' and name = ?`, idx).Scan(&n); err != nil || n != 1 {
			t.Errorf("index %s missing (n=%d, err=%v)", idx, n, err)
		}
	}

	m, err := s.GetMessage(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetMessage(1): %v", err)
	}
	if m.Title != "기존 메시지" || m.To == nil || *m.To != "desk" ||
		m.ToSession != nil || m.ToRole != nil || m.Issue != nil || m.ReplyTo != nil ||
		m.ReplyCount != 0 || m.Replies == nil || len(*m.Replies) != 0 {
		t.Errorf("migrated message = %+v", m)
	}

	// Check constraint and foreign key checks on new columns.
	if _, err := s.db.Exec(`insert into message(project_id, to_role, title, body, created_at, created_by, created_session, created_host, expires_at)
		values (1, 'boss', 't', '', '2026-11-20T01:00:00.000Z', 'claude', 's1', 'host', '2026-11-27T01:00:00.000Z')`); err == nil {
		t.Error("invalid to_role was accepted")
	}
	if _, err := s.db.Exec(`insert into message(project_id, to_actor, to_session_id, title, body, created_at, created_by, created_session, created_host, expires_at)
		values (1, 'unknown', 's2', 't', '', '2026-11-20T01:00:00.000Z', 'claude', 's1', 'host', '2026-11-27T01:00:00.000Z')`); err == nil {
		t.Error("unknown to_actor was accepted")
	}
	if _, err := s.db.Exec(`insert into message(project_id, issue_id, title, body, created_at, created_by, created_session, created_host, expires_at)
		values (1, 9999, 't', '', '2026-11-20T01:00:00.000Z', 'claude', 's1', 'host', '2026-11-27T01:00:00.000Z')`); err == nil {
		t.Error("unknown issue_id was accepted")
	}
	if _, err := s.db.Exec(`insert into message(project_id, reply_to_id, title, body, created_at, created_by, created_session, created_host, expires_at)
		values (1, 9999, 't', '', '2026-11-20T01:00:00.000Z', 'claude', 's1', 'host', '2026-11-27T01:00:00.000Z')`); err == nil {
		t.Error("unknown reply_to_id was accepted")
	}

	if fk := queryStrings(t, s, `pragma foreign_key_check`); len(fk) != 0 {
		t.Errorf("foreign_key_check = %v", fk)
	}
}

func TestMigration24OnFreshDatabase(t *testing.T) {
	s := openTest(t)
	cols := queryStrings(t, s, `select group_concat(name, ',') from pragma_table_info('message')`)
	want := "id,project_id,to_label,title,body,created_at,created_by,created_session,created_host,expires_at,deleted_at,deleted_by,to_actor,to_session_id,to_role,issue_id,reply_to_id"
	if cols[0] != want {
		t.Errorf("columns = %s, want %s", cols[0], want)
	}
}
