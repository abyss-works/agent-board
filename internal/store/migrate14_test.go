package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// openAt13 returns a database at schema version 13, as a server before the
// document state would leave it, and runs setup on it.
func openAt13(t *testing.T, setup func(db *sql.DB)) string {
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
		if n > 13 {
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
	if _, err := db.Exec("pragma user_version = 13"); err != nil {
		t.Fatal(err)
	}
	setup(db)
	return path
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

const (
	mA = "2026-10-01-1000-decision-old.md"
	mB = "2026-10-02-1000-decision-new.md"
	mC = "2026-10-03-1000-report-partial.md"
	mD = "2026-09-01-1000-report-arch.md"
	mE = "2026-10-04-1000-decision-self.md"
	mF = "2026-10-05-1000-decision-loop-a.md"
	mG = "2026-10-06-1000-decision-loop-b.md"
	mH = "2026-10-07-1000-decision-other.md"
)

func fm(lines ...string) string {
	return "---\n" + strings.Join(lines, "\n") + "\n---\n본문\n"
}

func insertOldDoc(t *testing.T, db *sql.DB, id, project int, name, location, kind, content string) {
	t.Helper()
	mustExec(t, db, `insert into document(id, project_id, name, location, kind, date, title, tags, sha256, size, content, created_at, created_by, updated_at, updated_by)
		values (?, ?, ?, ?, ?, '2026-10-01 10:00', ?, '["t"]', 'h', ?, ?, '2026-10-01T00:00:00.000Z', 'claude', '2026-10-01T00:00:00.000Z', 'claude')`,
		id, project, name, location, kind, "제목 "+name, len(content), content)
}

func seed13(t *testing.T, db *sql.DB) {
	mustExec(t, db, `insert into actor(id, kind, created_at) values ('claude', 'agent', 'x')`)
	for i, k := range []string{"AGB", "AGT"} {
		mustExec(t, db, `insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
			values (?, ?, ?, 'active', 10, 'x', 'x', 'claude', 'claude')`, i+1, k, k)
	}
	for _, i := range []struct {
		id, project int
		key, status string
	}{{1, 1, "AGB-1", "doing"}, {2, 1, "AGB-2", "deleted"}, {3, 2, "AGT-1", "backlog"}} {
		mustExec(t, db, `insert into issue(id, project_id, number, key, title, body, type, status, priority, version, created_at, updated_at, status_changed_at, status_changed_by, created_by, updated_by)
			values (?, ?, ?, ?, 't', '', 'feature', ?, 'P2', 1, 'x', 'x', 'x', 'claude', 'claude', 'claude')`, i.id, i.project, i.id, i.key, i.status)
	}
	insertOldDoc(t, db, 1, 1, mA, "history", "decision", fm("kind: decision", "title: a"))
	insertOldDoc(t, db, 2, 1, mB, "history", "decision", fm("kind: decision", "supersedes: "+mA, "issue: AGB-1"))
	insertOldDoc(t, db, 3, 1, mC, "history", "report", fm("kind: report", `supersedes: `+mA+`의 결론 중 "x"`))
	insertOldDoc(t, db, 4, 1, mD, "archive", "report", fm("kind: report", "related: [missing.md, "+mA+"]", "issues: [AGB-1, AGB-9, AGT-1]"))
	insertOldDoc(t, db, 5, 1, "decisions/2026-10-17-14-30-scope.md", "legacy", "decision", "# 범위\n\n본문 related: x.md\n")
	insertOldDoc(t, db, 6, 1, "archive/2026-06-x/notes.md", "legacy", "other", fm("title: 조사", "supersedes: none", "related: <없으면 생략>"))
	insertOldDoc(t, db, 7, 1, mE, "history", "decision", fm("kind: decision", "supersedes: "+mE))
	insertOldDoc(t, db, 8, 1, mF, "history", "decision", fm("kind: decision", "supersedes: "+mG))
	insertOldDoc(t, db, 9, 1, mG, "history", "decision", fm("kind: decision", "supersedes: "+mF))
	insertOldDoc(t, db, 10, 2, mH, "history", "decision", fm("kind: decision", "supersedes: "+mA, "related: history/"+mH))
	// A folder prefix is how other records cite a document by path.
	insertOldDoc(t, db, 11, 1, "2026-10-08-1000-report-citing.md", "history", "report", fm("kind: report", "related: history/"+mA))

	mustExec(t, db, `insert into similar_document(document_id, similar_id, score, model) values (1, 2, 0.9, 'm')`)
	mustExec(t, db, `insert into embedding(source, source_id, chunk, model, text_sha256, text, vector, created_at) values ('document', 2, 0, 'm', 's', 't', x'00', 'x')`)
	for _, r := range []string{"issue:AGB-2", "issue:AGB-1", "issue:NOPE-1", "doc:" + mA, "doc:nope.md", "doc:" + mH, "commit:abc"} {
		mustExec(t, db, `insert into issue_ref(issue_id, ref) values (1, ?)`, r)
	}
}

func queryStrings(t *testing.T, s *Store, q string) []string {
	t.Helper()
	rows, err := s.db.Query(q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []string
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
			parts[i] = "NULL"
			if v.Valid {
				parts[i] = v.String
			}
		}
		out = append(out, strings.Join(parts, "|"))
	}
	sort.Strings(out)
	return out
}

func TestMigration14RewritesDocuments(t *testing.T) {
	path := openAt13(t, func(db *sql.DB) { seed13(t, db) })
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var version int
	s.db.QueryRow("pragma user_version").Scan(&version)
	if version != 14 || SchemaVersion != 14 {
		t.Errorf("user_version = %d, SchemaVersion = %d", version, SchemaVersion)
	}
	// Ids, names and content stay; the flags follow the old location.
	got := queryStrings(t, s, `select id, name, kind, archived, imported, coalesce(path, 'NULL'), tags, title from document`)
	want := []string{
		"1|" + mA + "|decision|0|0|NULL|[\"t\"]|제목 " + mA,
		"10|" + mH + "|decision|0|0|NULL|[\"t\"]|제목 " + mH,
		"11|2026-10-08-1000-report-citing.md|report|0|0|NULL|[\"t\"]|제목 2026-10-08-1000-report-citing.md",
		"2|" + mB + "|decision|0|0|NULL|[\"t\"]|제목 " + mB,
		"3|" + mC + "|report|0|0|NULL|[\"t\"]|제목 " + mC,
		"4|" + mD + "|report|1|0|NULL|[\"t\"]|제목 " + mD,
		"5|decisions/2026-10-17-14-30-scope.md|decision|0|1|decisions/2026-10-17-14-30-scope.md|[\"t\"]|제목 decisions/2026-10-17-14-30-scope.md",
		"6|archive/2026-06-x/notes.md|other|1|1|archive/2026-06-x/notes.md|[\"t\"]|제목 archive/2026-06-x/notes.md",
		"7|" + mE + "|decision|0|0|NULL|[\"t\"]|제목 " + mE,
		"8|" + mF + "|decision|0|0|NULL|[\"t\"]|제목 " + mF,
		"9|" + mG + "|decision|0|0|NULL|[\"t\"]|제목 " + mG,
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("documents =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if n := queryStrings(t, s, `select count(*) from similar_document`); n[0] != "0" {
		t.Errorf("similar_document must be cleared: %v", n)
	}
	if n := queryStrings(t, s, `select count(*) from embedding where source = 'document'`); n[0] != "1" {
		t.Errorf("embedding rows must stay: %v", n)
	}
	// The index finds the documents again.
	if n := queryStrings(t, s, `select count(*) from document_fts where document_fts match '"조사"'`); n[0] != "1" {
		t.Errorf("document_fts match = %v", n)
	}
	// The triggers are back: a new document is indexed.
	mustExec(t, s.db, `insert into document(project_id, name, kind, sha256, size, content, title, created_at, created_by, updated_at, updated_by)
		values (1, 'x.md', 'report', 'h', 1, 'zzyzx', 'zzyzx', 'x', 'claude', 'x', 'claude')`)
	if n := queryStrings(t, s, `select count(*) from document_fts where document_fts match 'zzyzx'`); n[0] != "1" {
		t.Errorf("trigger did not index a new document: %v", n)
	}
	if _, err := s.db.Exec(`insert into document(project_id, name, kind, sha256, size, content, created_at, created_by, updated_at, updated_by)
		values (1, 'y.md', 'other', 'h', 1, 'c', 'x', 'claude', 'x', 'claude')`); err == nil {
		t.Error("kind other must need imported")
	}
	if fk := queryStrings(t, s, `pragma foreign_key_check`); len(fk) != 0 {
		t.Errorf("foreign_key_check = %v", fk)
	}
}

func TestMigration14BackfillsRelations(t *testing.T) {
	path := openAt13(t, func(db *sql.DB) { seed13(t, db) })
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	got := queryStrings(t, s, `select d.id, r.kind, coalesce(t.name, 'NULL'), coalesce(i.key, 'NULL'), coalesce(r.target_text, 'NULL')
		from document_relation r join document d on d.id = r.document_id
		left join document t on t.id = r.target_document_id left join issue i on i.id = r.target_issue_id`)
	want := []string{
		// B supersedes A, and is about AGB-1.
		"2|supersedes|" + mA + "|NULL|NULL",
		"2|about|NULL|AGB-1|NULL",
		// C's value is a sentence around a file name: a partial replacement is related.
		"3|related|" + mA + "|NULL|NULL",
		// D: a missing file stays as text; an existing one resolves; issues resolve or stay as text.
		"4|related|NULL|NULL|missing.md",
		"4|related|" + mA + "|NULL|NULL",
		"4|about|NULL|AGB-1|NULL",
		"4|about|NULL|NULL|AGB-9",
		"4|about|NULL|AGT-1|NULL",
		// E names itself: a self reference cannot be stored.
		// F supersedes G; G would close the loop, so it is related.
		"8|supersedes|" + mG + "|NULL|NULL",
		"9|related|" + mF + "|NULL|NULL",
		// H lies in another project: the name is not found there.
		"10|supersedes|NULL|NULL|" + mA,
		// A cited by its path resolves by the file name.
		"11|related|" + mA + "|NULL|NULL",
	}
	// H's related names itself through a folder prefix, which is a self reference.
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("relations =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestMigration14ResolvesIssueRefs(t *testing.T) {
	path := openAt13(t, func(db *sql.DB) { seed13(t, db) })
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := queryStrings(t, s, `select r.ref, coalesce(i.key, 'NULL'), coalesce(d.name, 'NULL') from issue_ref r
		left join issue i on i.id = r.target_issue_id left join document d on d.id = r.target_document_id`)
	want := []string{
		"commit:abc|NULL|NULL",
		"doc:" + mA + "|NULL|" + mA,
		"doc:" + mH + "|NULL|NULL", // H lies in another project than the issue
		"doc:nope.md|NULL|NULL",
		"issue:AGB-1|AGB-1|NULL",
		"issue:AGB-2|AGB-2|NULL", // a deleted issue resolves
		"issue:NOPE-1|NULL|NULL",
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("issue_ref =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestMigration14OnEmptyDatabaseAndTwice(t *testing.T) {
	path := openAt13(t, func(db *sql.DB) {})
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

func TestMigration14FromTheStartAddsTheTables(t *testing.T) {
	s := openTest(t)
	for _, table := range []string{"document_relation", "document_activity"} {
		if n := queryStrings(t, s, fmt.Sprintf(`select count(*) from sqlite_master where name = '%s'`, table)); n[0] != "1" {
			t.Errorf("table %s missing", table)
		}
	}
}
