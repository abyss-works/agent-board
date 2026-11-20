package store

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// openAt22 returns a database at schema version 22, before event was added,
// and runs setup on it.
func openAt22(t *testing.T, setup func(db *sql.DB)) string {
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
		if n > 22 {
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
	mustExec(t, db, "pragma user_version = 22")
	setup(db)
	return path
}

func TestMigration23CreatesEventTable(t *testing.T) {
	path := openAt22(t, func(db *sql.DB) {
		const at = "'2026-11-20T00:00:00.000Z'"
		mustExec(t, db, `insert into actor(id, kind, created_at) values ('claude', 'agent', `+at+`)`)
		mustExec(t, db, `insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
			values (1, 'APP', 'example-app', 'active', 1, `+at+`, `+at+`, 'claude', 'claude')`)
	})

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if v := queryStrings(t, s, `pragma user_version`); v[0] != strconv.Itoa(SchemaVersion) || SchemaVersion < 23 {
		t.Errorf("user_version = %v, SchemaVersion = %d", v, SchemaVersion)
	}

	for _, idx := range []string{"idx_event_project_id", "idx_event_kind_id"} {
		var n int
		if err := s.db.QueryRow(`select count(*) from sqlite_master where type = 'index' and name = ?`, idx).Scan(&n); err != nil || n != 1 {
			t.Errorf("index %s missing (n=%d, err=%v)", idx, n, err)
		}
	}

	// Default data is '{}' and autoincrement never reuses deleted IDs.
	res, err := s.db.Exec(`insert into event(at, project_id, kind, subject, actor, session_id)
		values ('2026-11-20T01:00:00.000Z', 1, 'project.created', 'APP', 'claude', 's1')`)
	if err != nil {
		t.Fatalf("insert event 1: %v", err)
	}
	id1, _ := res.LastInsertId()
	var data string
	if err := s.db.QueryRow(`select data from event where id = ?`, id1).Scan(&data); err != nil || data != "{}" {
		t.Errorf("default data = %q, err=%v, want {}", data, err)
	}
	mustExec(t, s.db, `delete from event where id = `+strconv.FormatInt(id1, 10))
	res2, err := s.db.Exec(`insert into event(at, project_id, kind, subject, actor, session_id)
		values ('2026-11-20T01:01:00.000Z', null, 'session.created', 'claude/s1', 'claude', 's1')`)
	if err != nil {
		t.Fatalf("insert event 2: %v", err)
	}
	id2, _ := res2.LastInsertId()
	if id2 <= id1 {
		t.Errorf("id2 = %d <= id1 = %d; expected monotonic autoincrement", id2, id1)
	}

	// Foreign key rejects unknown project_id.
	if _, err := s.db.Exec(`insert into event(at, project_id, kind, subject, actor, session_id)
		values ('2026-11-20T01:02:00.000Z', 9999, 'issue.created', 'NOPE-1', 'claude', 's1')`); err == nil {
		t.Error("invalid project_id was accepted")
	}

	if fk := queryStrings(t, s, `pragma foreign_key_check`); len(fk) != 0 {
		t.Errorf("foreign_key_check = %v", fk)
	}
}
