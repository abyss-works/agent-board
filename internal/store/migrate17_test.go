package store

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// openAt16 returns a database at schema version 16, as a server before the
// removal marks of document relations would leave it, and runs setup on it.
func openAt16(t *testing.T, setup func(db *sql.DB)) string {
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
		if n > 16 {
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
	mustExec(t, db, "pragma user_version = 16")
	setup(db)
	return path
}

func TestMigration17KeepsRelationsAndMarksRemoval(t *testing.T) {
	path := openAt16(t, func(db *sql.DB) {
		seed14(t, db)
	})
	db, err := sql.Open("sqlite", DSN(path))
	if err != nil {
		t.Fatal(err)
	}
	before := dumpRelations(t, db)
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if v := queryStrings(t, s, `pragma user_version`); v[0] != strconv.Itoa(SchemaVersion) || SchemaVersion < 17 {
		t.Errorf("user_version = %v, SchemaVersion = %d", v, SchemaVersion)
	}
	got := queryStrings(t, s, `select id || '|' || document_id || '|' || kind || '|' || ifnull(target_document_id, '') || '|' || ifnull(target_issue_id, '') || '|' || ifnull(target_text, '')
		from document_relation order by id`)
	if strings.Join(got, "\n") != strings.Join(before, "\n") || len(got) != 3 {
		t.Errorf("relations = %v, before = %v", got, before)
	}
	if n := queryStrings(t, s, `select count(*) from document_relation where removed_at is not null or removed_by is not null or removed_session is not null or removed_host is not null`); n[0] != "0" {
		t.Errorf("rows already marked removed: %v", n)
	}
	if fk := queryStrings(t, s, `pragma foreign_key_check`); len(fk) != 0 {
		t.Errorf("foreign_key_check = %v", fk)
	}

	// A removed relation no longer holds the target; a live one still does.
	mustExec(t, s.db, `update document_relation set removed_at = 'x', removed_by = 'claude', removed_session = 's', removed_host = 'h' where id = 1`)
	mustExec(t, s.db, `insert into document_relation(document_id, kind, target_document_id) values (2, 'supersedes', 1)`)
	if _, err := s.db.Exec(`insert into document_relation(document_id, kind, target_document_id) values (2, 'supersedes', 1)`); err == nil {
		t.Error("two live relations to the same document")
	}
	mustExec(t, s.db, `update document_relation set removed_at = 'x' where document_id = 2 and kind = 'about'`)
	mustExec(t, s.db, `insert into document_relation(document_id, kind, target_issue_id) values (2, 'about', 1)`)
	mustExec(t, s.db, `update document_relation set removed_at = 'x' where target_text = 'missing.md'`)
	mustExec(t, s.db, `insert into document_relation(document_id, kind, target_text) values (3, 'related', 'missing.md')`)
}

func TestMigration17OnFreshDatabase(t *testing.T) {
	s := openTest(t)
	cols := queryStrings(t, s, `select group_concat(name, ',') from pragma_table_info('document_relation')`)
	want := "id,document_id,kind,target_document_id,target_issue_id,target_text,removed_at,removed_by,removed_session,removed_host"
	if cols[0] != want {
		t.Errorf("columns = %s", cols[0])
	}
	idx := queryStrings(t, s, `select group_concat(name, ',') from (select name from sqlite_master where type = 'index' and tbl_name = 'document_relation' and sql is not null order by name)`)
	wantIdx := "document_relation_document,document_relation_issue,document_relation_target,document_relation_target_issue,document_relation_text"
	if idx[0] != wantIdx {
		t.Errorf("indexes = %s", idx[0])
	}
	for _, name := range strings.Split(wantIdx, ",") {
		def := queryStrings(t, s, `select sql from sqlite_master where name = '`+name+`'`)
		if !strings.Contains(def[0], "removed_at is null") {
			t.Errorf("%s is not limited to live relations: %s", name, def[0])
		}
	}
}

func dumpRelations(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`select id || '|' || document_id || '|' || kind || '|' || ifnull(target_document_id, '') || '|' || ifnull(target_issue_id, '') || '|' || ifnull(target_text, '')
		from document_relation order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}
