package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// openAt14 returns a database at schema version 14, as a server before the
// free document kind would leave it, and runs setup on it.
func openAt14(t *testing.T, setup func(db *sql.DB)) string {
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
		if n > 14 {
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
	mustExec(t, db, "pragma user_version = 14")
	setup(db)
	return path
}

func seed14(t *testing.T, db *sql.DB) {
	mustExec(t, db, `insert into actor(id, kind, created_at) values ('claude', 'agent', 'x')`)
	mustExec(t, db, `insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
		values (1, 'AGB', 'AGB', 'active', 10, 'x', 'x', 'claude', 'claude')`)
	mustExec(t, db, `insert into issue(id, project_id, number, key, title, body, type, status, priority, version, created_at, updated_at, status_changed_at, status_changed_by, created_by, updated_by)
		values (1, 1, 1, 'AGB-1', 't', '', 'feature', 'doing', 'P2', 1, 'x', 'x', 'x', 'claude', 'claude', 'claude')`)
	ins := func(id int, name, kind string, imported int) {
		mustExec(t, db, `insert into document(id, project_id, name, kind, title, tags, imported, sha256, size, content, created_at, created_by, updated_at, updated_by)
			values (?, 1, ?, ?, ?, '["t"]', ?, 'h', 1, ?, 'x', 'claude', 'x', 'claude')`, id, name, kind, "제목 "+name, imported, "본문 "+name)
	}
	ins(1, mA, "decision", 0)
	ins(2, mB, "decision", 0)
	ins(3, "archive/legacy.md", "other", 1)
	mustExec(t, db, `insert into document_relation(document_id, kind, target_document_id) values (2, 'supersedes', 1)`)
	mustExec(t, db, `insert into document_relation(document_id, kind, target_issue_id) values (2, 'about', 1)`)
	mustExec(t, db, `insert into document_activity(document_id, actor_id, session_id, client_host, at, changes) values (2, 'claude', 's', 'h', 'x', '{}')`)
	mustExec(t, db, `insert into issue_ref(issue_id, ref, target_document_id) values (1, 'doc:`+mA+`', 1)`)
	mustExec(t, db, `insert into similar_document(document_id, similar_id, score, model) values (1, 2, 0.9, 'm')`)
	mustExec(t, db, `insert into embedding(source, source_id, chunk, model, text_sha256, text, vector, created_at) values ('document', 2, 0, 'm', 's', 't', x'00', 'x')`)
}

func TestMigration15KeepsDocumentsAndLinks(t *testing.T) {
	path := openAt14(t, func(db *sql.DB) { seed14(t, db) })
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var version int
	s.db.QueryRow("pragma user_version").Scan(&version)
	if version != 15 || SchemaVersion != 15 {
		t.Errorf("user_version = %d, SchemaVersion = %d", version, SchemaVersion)
	}
	got := queryStrings(t, s, `select id, name, kind, archived, imported, coalesce(path, 'NULL'), tags, title from document`)
	want := []string{
		"1|" + mA + "|decision|0|0|NULL|[\"t\"]|제목 " + mA,
		"2|" + mB + "|decision|0|0|NULL|[\"t\"]|제목 " + mB,
		"3|archive/legacy.md|other|0|1|NULL|[\"t\"]|제목 archive/legacy.md",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("documents =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for q, want := range map[string]string{
		`select count(*) from document_relation`:                            "2",
		`select count(*) from document_activity`:                            "1",
		`select target_document_id from issue_ref where issue_id = 1`:       "1",
		`select count(*) from similar_document`:                             "0",
		`select count(*) from embedding where source = 'document'`:          "1",
		`select count(*) from document_fts where document_fts match '"제목"'`: "3",
	} {
		if n := queryStrings(t, s, q); n[0] != want {
			t.Errorf("%s = %v, want %s", q, n, want)
		}
	}
	if fk := queryStrings(t, s, `pragma foreign_key_check`); len(fk) != 0 {
		t.Errorf("foreign_key_check = %v", fk)
	}
}

func TestMigration15AcceptsAnyKindOfValidLength(t *testing.T) {
	path := openAt14(t, func(db *sql.DB) { seed14(t, db) })
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	insert := func(name, kind string, imported int) error {
		_, err := s.db.Exec(`insert into document(project_id, name, kind, imported, sha256, size, content, title, created_at, created_by, updated_at, updated_by)
			values (1, ?, ?, ?, 'h', 1, 'zzyzx', 'zzyzx', 'x', 'claude', 'x', 'claude')`, name, kind, imported)
		return err
	}
	if err := insert("a.md", "lesson", 0); err != nil {
		t.Errorf("kind lesson: %v", err)
	}
	// The triggers are back: the new document is indexed.
	if n := queryStrings(t, s, `select count(*) from document_fts where document_fts match 'zzyzx'`); n[0] != "1" {
		t.Errorf("trigger did not index a new document: %v", n)
	}
	if err := insert("b.md", "other", 0); err == nil {
		t.Error("kind other must need imported")
	}
	if err := insert("c.md", "", 0); err == nil {
		t.Error("an empty kind must be refused")
	}
	if err := insert("d.md", strings.Repeat("a", 33), 0); err == nil {
		t.Error("a kind over 32 characters must be refused")
	}
	if err := insert("e.md", strings.Repeat("a", 32), 0); err != nil {
		t.Errorf("a kind of 32 characters: %v", err)
	}
}

func TestMigration15OnEmptyDatabaseAndTwice(t *testing.T) {
	path := openAt14(t, func(db *sql.DB) {})
	s, err := Open(path)
	if err != nil {
		t.Fatalf("empty database: %v", err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	s.Close()
}
