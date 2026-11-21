package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

// openAt24 returns a database at schema version 24, before verification gate
// columns were added, and runs setup on it.
func openAt24(t *testing.T, setup func(db *sql.DB)) string {
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
		if n > 24 {
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
	mustExec(t, db, "pragma user_version = 24")
	setup(db)
	return path
}

func TestMigration25AddsVerificationColumns(t *testing.T) {
	path := openAt24(t, func(db *sql.DB) {
		const at = "'2026-11-20T00:00:00.000Z'"
		mustExec(t, db, `insert into actor(id, kind, created_at) values ('owner', 'user', `+at+`), ('claude', 'agent', `+at+`)`)
		mustExec(t, db, `insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
			values (1, 'APP', 'example-app', 'active', 3, `+at+`, `+at+`, 'owner', 'owner')`)
		mustExec(t, db, `insert into issue(id, project_id, number, key, title, body, type, status, priority, acknowledged_at, acknowledged_by, version,
			created_at, updated_at, status_changed_at, status_changed_by, created_by, updated_by)
			values (1, 1, 1, 'APP-1', '기존 확인 이슈', '', 'feature', 'done', 'P2', `+at+`, 'owner', 1, `+at+`, `+at+`, `+at+`, 'owner', 'owner', 'owner')`)
		mustExec(t, db, `insert into issue(id, project_id, number, key, title, body, type, status, priority, acknowledged_at, acknowledged_by, version,
			created_at, updated_at, status_changed_at, status_changed_by, created_by, updated_by)
			values (2, 1, 2, 'APP-2', '기존 미확인 이슈', '', 'feature', 'backlog', 'P2', null, null, 1, `+at+`, `+at+`, `+at+`, 'claude', 'claude', 'claude')`)
	})

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if v := queryStrings(t, s, `pragma user_version`); v[0] != strconv.Itoa(SchemaVersion) || SchemaVersion < 25 {
		t.Errorf("user_version = %v, SchemaVersion = %d", v, SchemaVersion)
	}

	var idxCount int
	if err := s.db.QueryRow(`select count(*) from sqlite_master where type = 'index' and name = 'idx_issue_verification_pending'`).Scan(&idxCount); err != nil || idxCount != 1 {
		t.Errorf("index idx_issue_verification_pending missing (n=%d, err=%v)", idxCount, err)
	}

	for _, key := range []string{"APP-1", "APP-2"} {
		got, err := s.GetIssue(context.Background(), key)
		if err != nil {
			t.Fatalf("GetIssue(%s): %v", key, err)
		}
		if got.VerificationState != model.VerificationNone || got.VerificationNote != nil || got.VerifiedAt != nil || got.VerifiedBy != nil {
			t.Errorf("%s after migration = state=%q note=%v at=%v by=%v; want none/nil",
				key, got.VerificationState, got.VerificationNote, got.VerifiedAt, got.VerifiedBy)
		}
	}

	// Check constraint and foreign key checks on new columns.
	if _, err := s.db.Exec(`update issue set verification_state = 'invalid' where id = 1`); err == nil {
		t.Error("invalid verification_state was accepted")
	}
	if _, err := s.db.Exec(`update issue set verified_by = 'ghost' where id = 1`); err == nil {
		t.Error("unknown verified_by was accepted")
	}
	if fk := queryStrings(t, s, `pragma foreign_key_check`); len(fk) != 0 {
		t.Errorf("foreign_key_check = %v", fk)
	}
}

func TestMigration25OnFreshDatabase(t *testing.T) {
	s := openTest(t)
	cols := queryStrings(t, s, `select name from pragma_table_info('issue')`)
	want := map[string]bool{"verification_state": false, "verification_note": false, "verified_at": false, "verified_by": false}
	for _, c := range cols {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for col, found := range want {
		if !found {
			t.Errorf("issue table missing column %q", col)
		}
	}
}
