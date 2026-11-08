package store

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// openAt17 returns a database at schema version 17, as a server before the
// resource activity would leave it, and runs setup on it.
func openAt17(t *testing.T, setup func(db *sql.DB)) string {
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
		if n > 17 {
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
	mustExec(t, db, "pragma user_version = 17")
	setup(db)
	return path
}

func TestMigration18KeepsRowsAndAddsTheTable(t *testing.T) {
	path := openAt17(t, func(db *sql.DB) {
		mustExec(t, db, `insert into actor(id, kind, created_at) values ('claude', 'agent', 'x')`)
		mustExec(t, db, `insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
			values (1, 'AGB', 'AGB', 'active', 1, '2026-10-01T00:00:00.000Z', '2026-10-01T00:00:00.000Z', 'claude', 'claude')`)
		mustExec(t, db, `insert into topic(id, project_id, name, status, created_at, updated_at, created_by, updated_by)
			values (1, 1, 't', 'active', '2026-10-01T00:00:00.000Z', '2026-10-01T00:00:00.000Z', 'claude', 'claude')`)
		mustExec(t, db, `insert into milestone(id, project_id, name, created_at, updated_at, created_by, updated_by)
			values (1, 1, 'm', '2026-10-01T00:00:00.000Z', '2026-10-01T00:00:00.000Z', 'claude', 'claude')`)
		mustExec(t, db, `insert into token(id, actor_id, client_host, token_hash, created_at, created_by, revoked_at, revoked_by)
			values (1, 'claude', 'pc', 'h1', '2026-10-01T00:00:00.000Z', 'claude', '2026-10-02T00:00:00.000Z', 'claude')`)
		mustExec(t, db, `insert into token(id, actor_id, client_host, token_hash, created_at, created_by)
			values (2, 'claude', 'pc', 'h2', '2026-10-01T00:00:00.000Z', 'claude')`)
	})
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if v := queryStrings(t, s, `pragma user_version`); v[0] != strconv.Itoa(SchemaVersion) || SchemaVersion < 18 {
		t.Errorf("user_version = %v, SchemaVersion = %d", v, SchemaVersion)
	}
	for table, want := range map[string]string{"project": "1", "topic": "1", "milestone": "1", "token": "2", "resource_activity": "0"} {
		if n := queryStrings(t, s, `select count(*) from `+table); n[0] != want {
			t.Errorf("%s rows = %v, want %s", table, n, want)
		}
	}
	// Existing revocations have no session or device; the columns are empty.
	if r := queryStrings(t, s, `select revoked_by || '|' || ifnull(revoked_session, 'null') || '|' || ifnull(revoked_host, 'null') from token where id = 1`); r[0] != "claude|null|null" {
		t.Errorf("revoked token = %v", r)
	}
	idx := queryStrings(t, s, `select group_concat(name, ',') from (select name from sqlite_master where type = 'index' and tbl_name = 'resource_activity' and sql is not null order by name)`)
	if idx[0] != "resource_activity_project,resource_activity_resource" {
		t.Errorf("indexes = %s", idx[0])
	}
	if fk := queryStrings(t, s, `pragma foreign_key_check`); len(fk) != 0 {
		t.Errorf("foreign_key_check = %v", fk)
	}
	// Writes after the migration are recorded and the token can be revoked with a trace.
	if _, err := s.RevokeToken(t.Context(), 2, trClaude); err != nil {
		t.Fatal(err)
	}
	name := "x"
	if _, err := s.UpdateMilestone(t.Context(), trClaude, 1, MilestonePatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if n := queryStrings(t, s, `select count(*) from resource_activity`); n[0] != "1" {
		t.Errorf("records = %v", n)
	}
}

func TestResourceWritesChangeTheFingerprint(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	before, _ := s.FingerprintNow(ctx)
	if _, err := s.CreateMilestone(ctx, trOwner, "AGB", MilestoneInput{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.FingerprintNow(ctx)
	if after == before {
		t.Errorf("a milestone write left the fingerprint %q", before)
	}
	if again, _ := s.FingerprintNow(ctx); again != after {
		t.Errorf("fingerprint changed without a write: %q, %q", after, again)
	}
}
