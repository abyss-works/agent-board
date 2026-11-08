package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"sort"
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
	ins := func(id int, name, kind string, archived, imported int, path any) {
		mustExec(t, db, `insert into document(id, project_id, name, kind, date, title, tags, archived, imported, path, sha256, size, content, created_at, created_by, updated_at, updated_by)
			values (?, 1, ?, ?, '2026-10-01 10:00', ?, '["t","u"]', ?, ?, ?, ?, ?, ?, ?, 'claude', ?, 'claude')`,
			id, name, kind, "제목 "+name, archived, imported, path, "sha"+name, 10+id, "본문 "+name, "c"+name, "u"+name)
	}
	ins(1, mA, "decision", 0, 0, nil)
	ins(2, mB, "decision", 1, 0, nil)
	ins(3, "archive/legacy.md", "other", 1, 1, "archive/legacy.md")
	mustExec(t, db, `insert into document_relation(document_id, kind, target_document_id) values (2, 'supersedes', 1)`)
	mustExec(t, db, `insert into document_relation(document_id, kind, target_issue_id) values (2, 'about', 1)`)
	mustExec(t, db, `insert into document_relation(document_id, kind, target_text) values (3, 'related', 'missing.md')`)
	mustExec(t, db, `insert into document_activity(document_id, actor_id, session_id, client_host, at, changes) values (2, 'claude', 's', 'h', '2026-10-02T00:00:00.000Z', '{"archived":{"from":false,"to":true}}')`)
	mustExec(t, db, `insert into issue_ref(issue_id, ref, target_document_id) values (1, 'doc:`+mA+`', 1)`)
	mustExec(t, db, `insert into issue_ref(issue_id, ref, target_issue_id) values (1, 'issue:AGB-1', 1)`)
	mustExec(t, db, `insert into issue_ref(issue_id, ref) values (1, 'commit:abc')`)
	mustExec(t, db, `insert into similar_document(document_id, similar_id, score, model) values (1, 2, 0.9, 'm')`)
	mustExec(t, db, `insert into embedding(source, source_id, chunk, model, text_sha256, text, vector, created_at) values ('document', 2, 0, 'm', 's', 't', x'00', 'x')`)
}

// dumpTables returns every row of the tables that survive migration 15, all
// columns, as sorted text.
func dumpTables(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, table := range []string{"document", "document_relation", "document_activity", "issue_ref", "embedding"} {
		sel := "*"
		if table == "document_relation" { // later migrations add columns
			sel = "id, document_id, kind, target_document_id, target_issue_id, target_text"
		}
		rows, err := db.Query(`select ` + sel + ` from ` + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]sql.NullString, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			parts := make([]string, len(cols))
			for i, v := range vals {
				parts[i] = cols[i] + "=NULL"
				if v.Valid {
					parts[i] = cols[i] + "=" + v.String
				}
			}
			out[table] = append(out[table], strings.Join(parts, "|"))
		}
		rows.Close()
		sort.Strings(out[table])
		if len(out[table]) == 0 {
			t.Fatalf("seed has no %s rows", table)
		}
	}
	return out
}

func TestMigration15KeepsDocumentsAndLinks(t *testing.T) {
	var before map[string][]string
	path := openAt14(t, func(db *sql.DB) {
		seed14(t, db)
		before = dumpTables(t, db)
	})
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var version int
	s.db.QueryRow("pragma user_version").Scan(&version)
	if version != SchemaVersion {
		t.Errorf("user_version = %d, SchemaVersion = %d", version, SchemaVersion)
	}
	after := dumpTables(t, s.db)
	for table, want := range before {
		if !reflect.DeepEqual(after[table], want) {
			t.Errorf("%s = %q, want %q", table, after[table], want)
		}
	}
	for q, want := range map[string]string{
		`select count(*) from similar_document`:                             "0",
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
