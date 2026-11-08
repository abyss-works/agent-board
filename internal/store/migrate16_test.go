package store

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// openAt15 returns a database at schema version 15, as a server before the
// message space would leave it, and runs setup on it.
func openAt15(t *testing.T, setup func(db *sql.DB)) string {
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
		if n > 15 {
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
	mustExec(t, db, "pragma user_version = 15")
	setup(db)
	return path
}

func TestMigration16AddsMessageTable(t *testing.T) {
	path := openAt15(t, func(db *sql.DB) {
		mustExec(t, db, `insert into actor(id, kind, created_at) values ('claude', 'agent', 'x')`)
		mustExec(t, db, `insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
			values (1, 'AGB', 'AGB', 'active', 1, 'x', 'x', 'claude', 'claude')`)
	})
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if n := queryStrings(t, s, `select count(*) from project`); n[0] != "1" {
		t.Errorf("projects = %v", n)
	}
	if n := queryStrings(t, s, `select count(*) from sqlite_master where name = 'message_project'`); n[0] != "1" {
		t.Errorf("message index missing")
	}
	mustExec(t, s.db, `insert into message(project_id, title, body, created_at, created_by, created_session, created_host, expires_at)
		values (1, 't', '', 'x', 'claude', 's', 'h', 'y')`)
	if fk := queryStrings(t, s, `pragma foreign_key_check`); len(fk) != 0 {
		t.Errorf("foreign_key_check = %v", fk)
	}
}

func TestMigration16OnFreshDatabase(t *testing.T) {
	s := openTest(t)
	cols := queryStrings(t, s, `select group_concat(name, ',') from pragma_table_info('message')`)
	want := "id,project_id,to_label,title,body,created_at,created_by,created_session,created_host,expires_at,deleted_at,deleted_by"
	if cols[0] != want {
		t.Errorf("columns = %s", cols[0])
	}
}
