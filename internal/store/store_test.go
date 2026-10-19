package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "agb.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenAppliesPragmasAndMigrations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agb.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var mode string
	if err := s.db.QueryRow("pragma journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	var fk int
	if err := s.db.QueryRow("pragma foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
	var version int
	if err := s.db.QueryRow("pragma user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion {
		t.Errorf("user_version = %d, want %d", version, SchemaVersion)
	}
	for _, table := range []string{"actor", "project", "issue", "issue_label", "issue_dependency", "issue_ref", "activity", "issue_fts", "topic", "milestone", "token"} {
		var n int
		if err := s.db.QueryRow("select count(*) from sqlite_master where name = ?", table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("table %s missing", table)
		}
	}
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Errorf("wal file: %v", err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agb.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s.Close()
	var version int
	if err := s.db.QueryRow("pragma user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion {
		t.Errorf("user_version = %d after reopen", version)
	}
}

// TestMigrateAddsSummaryToVersion1Database upgrades a database that holds
// records written under schema version 1. Migration 0002 only adds the
// summary column: existing records stay as they were and read back without a
// summary.
func TestMigrateAddsSummaryToVersion1Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agb.db")
	legacy, err := sql.Open("sqlite", DSN(path))
	if err != nil {
		t.Fatal(err)
	}
	first, err := migrationFS.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	const at = "'2026-10-17T05:00:00.000Z'"
	seed := []string{
		string(first),
		`pragma user_version = 1`,
		`insert into actor(id, kind, created_at) values ('claude', 'agent', ` + at + `)`,
		`insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
		 values (1, 'AGB', 'agent-board', 'active', 2, ` + at + `, ` + at + `, 'claude', 'claude')`,
		`insert into issue(id, project_id, number, key, title, type, status, priority, created_at, updated_at, status_changed_at, status_changed_by, created_by, updated_by)
		 values (1, 1, 1, 'AGB-1', '기존 이슈', 'feature', 'backlog', 'P2', ` + at + `, ` + at + `, ` + at + `, 'claude', 'claude', 'claude')`,
		`insert into activity(issue_id, kind, actor_id, session_id, client_host, at, changes)
		 values (1, 'created', 'claude', 's1', 'desk', ` + at + `, '{"title":{"to":"기존 이슈"}}')`,
		`insert into activity(issue_id, kind, actor_id, session_id, client_host, at, body)
		 values (1, 'commented', 'claude', 's1', 'desk', '2026-10-17T05:01:00.000Z', '요약 열이 없던 시절의 댓글')`,
	}
	for _, stmt := range seed {
		if _, err := legacy.Exec(stmt); err != nil {
			t.Fatalf("seed %.40q: %v", stmt, err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a version 1 database: %v", err)
	}
	defer s.Close()
	var version int
	if err := s.db.QueryRow("pragma user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion {
		t.Errorf("user_version = %d, want %d", version, SchemaVersion)
	}

	rows, err := s.db.Query(`select name, type, "notnull" from pragma_table_info('activity')`)
	if err != nil {
		t.Fatal(err)
	}
	var columns []string
	for rows.Next() {
		var name, typ string
		var notNull int
		if err := rows.Scan(&name, &typ, &notNull); err != nil {
			t.Fatal(err)
		}
		if name == "summary" && (typ != "TEXT" || notNull != 0) {
			t.Errorf("summary column is %s, notnull %d; want nullable TEXT", typ, notNull)
		}
		columns = append(columns, name)
	}
	rows.Close()
	want := "id issue_id kind actor_id session_id client_host at changes body summary"
	if got := strings.Join(columns, " "); got != want {
		t.Errorf("activity columns = %q, want %q", got, want)
	}

	items, total, err := s.ListActivity(ctx, ActivityFilter{Asc: true, Limit: 10})
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("ListActivity = %d items, total %d, err %v", len(items), total, err)
	}
	if items[0].Kind != model.ActivityCreated || items[0].Summary != nil || items[0].Changes["title"].To == nil {
		t.Errorf("created record = %+v", items[0])
	}
	if items[1].Kind != model.ActivityCommented || items[1].Summary != nil || items[1].Body == nil || *items[1].Body != "요약 열이 없던 시절의 댓글" {
		t.Errorf("commented record = %+v", items[1])
	}

	c, err := s.AddComment(ctx, model.Trace{Actor: "claude", SessionID: "s2", ClientHost: "desk"}, "AGB-1", "새 본문", str("새 요약"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Summary == nil || *c.Summary != "새 요약" {
		t.Errorf("comment after migration = %+v", c)
	}
}

// TestMigrateAddsTopicAckAndReasonToVersion2Database upgrades a database
// written under schema version 2. Migration 0003 adds the topic table and four
// issue columns; every existing issue becomes acknowledged at migration time
// with no acknowledging actor, and nothing else about it changes.
func TestMigrateAddsTopicAckAndReasonToVersion2Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agb.db")
	legacy, err := sql.Open("sqlite", DSN(path))
	if err != nil {
		t.Fatal(err)
	}
	const at = "'2026-10-17T05:00:00.000Z'"
	seed := []string{}
	for _, name := range []string{"migrations/0001_init.sql", "migrations/0002_activity_summary.sql"} {
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		seed = append(seed, string(body))
	}
	seed = append(seed,
		`pragma user_version = 2`,
		`insert into actor(id, kind, created_at) values ('claude', 'agent', `+at+`)`,
		`insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
		 values (1, 'AGB', 'agent-board', 'active', 3, `+at+`, `+at+`, 'claude', 'claude')`,
		`insert into issue(id, project_id, number, key, title, type, status, priority, created_at, updated_at, status_changed_at, status_changed_by, created_by, updated_by)
		 values (1, 1, 1, 'AGB-1', '첫 이슈', 'feature', 'backlog', 'P2', `+at+`, `+at+`, `+at+`, 'claude', 'claude', 'claude'),
		        (2, 1, 2, 'AGB-2', '닫힌 이슈', 'bug', 'done', 'P1', `+at+`, `+at+`, `+at+`, 'claude', 'claude', 'claude')`,
	)
	for _, stmt := range seed {
		if _, err := legacy.Exec(stmt); err != nil {
			t.Fatalf("seed %.40q: %v", stmt, err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	before := time.Now().UTC().Truncate(time.Millisecond)
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a version 2 database: %v", err)
	}
	defer s.Close()
	after := time.Now().UTC().Add(time.Millisecond)

	var version int
	if err := s.db.QueryRow("pragma user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion || SchemaVersion < 3 {
		t.Errorf("user_version = %d, SchemaVersion = %d, want the current version, at least 3", version, SchemaVersion)
	}

	columns := func(table string) map[string]string {
		rows, err := s.db.Query(`select name, type || ':' || "notnull" from pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var name, kind string
			if err := rows.Scan(&name, &kind); err != nil {
				t.Fatal(err)
			}
			out[name] = kind
		}
		return out
	}
	issueCols := columns("issue")
	for name, want := range map[string]string{"topic_id": "INTEGER:0", "status_reason": "TEXT:0", "acknowledged_at": "TEXT:0", "acknowledged_by": "TEXT:0"} {
		if got := issueCols[name]; got != want {
			t.Errorf("issue.%s = %q, want %q", name, got, want)
		}
	}
	topicCols := columns("topic")
	for name, want := range map[string]string{"id": "INTEGER:0", "project_id": "INTEGER:1", "name": "TEXT:1", "parent_id": "INTEGER:0", "status": "TEXT:1",
		"created_at": "TEXT:1", "updated_at": "TEXT:1", "created_by": "TEXT:1", "updated_by": "TEXT:1"} {
		if got := topicCols[name]; got != want {
			t.Errorf("topic.%s = %q, want %q", name, got, want)
		}
	}

	rows, err := s.db.Query(`select key, title, topic_id, status_reason, acknowledged_at, acknowledged_by from issue order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var key, title string
		var topicID sql.NullInt64
		var reason, ackAt, ackBy sql.NullString
		if err := rows.Scan(&key, &title, &topicID, &reason, &ackAt, &ackBy); err != nil {
			t.Fatal(err)
		}
		n++
		if title == "" || topicID.Valid || reason.Valid || ackBy.Valid {
			t.Errorf("%s: title %q topic %v reason %v acknowledged_by %v; want the old values and nulls", key, title, topicID, reason, ackBy)
		}
		stamp, err := model.ParseTime(ackAt.String)
		if !ackAt.Valid || err != nil || stamp.String() != ackAt.String {
			t.Errorf("%s: acknowledged_at = %q (%v), want a millisecond UTC stamp", key, ackAt.String, err)
			continue
		}
		if stamp.Time().Before(before) || stamp.Time().After(after) {
			t.Errorf("%s: acknowledged_at %s is outside the migration window %s..%s", key, ackAt.String, before, after)
		}
	}
	if n != 2 {
		t.Errorf("issues after migration = %d, want 2", n)
	}

	// The store reads the new columns: old issues are acknowledged with no actor.
	old, err := s.GetIssue(context.Background(), "AGB-1")
	if err != nil {
		t.Fatal(err)
	}
	if old.AcknowledgedAt == nil || old.AcknowledgedBy != nil || old.StatusReason != nil || old.Title != "첫 이슈" || old.Version != 1 {
		t.Errorf("migrated issue = %+v", old)
	}
}

// TestMigrateAddsMilestoneToVersion3Database upgrades a database written under
// schema version 3. Migration 0004 adds the milestone table and the
// issue.milestone_id column; every existing issue ends up without a milestone
// and nothing else about it changes.
func TestMigrateAddsMilestoneToVersion3Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agb.db")
	legacy, err := sql.Open("sqlite", DSN(path))
	if err != nil {
		t.Fatal(err)
	}
	const at = "'2026-10-17T05:00:00.000Z'"
	var seed []string
	for _, name := range []string{"migrations/0001_init.sql", "migrations/0002_activity_summary.sql", "migrations/0003_topic_ack_reason.sql"} {
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		seed = append(seed, string(body))
	}
	seed = append(seed,
		`pragma user_version = 3`,
		`insert into actor(id, kind, created_at) values ('claude', 'agent', `+at+`)`,
		`insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
		 values (1, 'AGB', 'agent-board', 'active', 3, `+at+`, `+at+`, 'claude', 'claude')`,
		`insert into topic(id, project_id, name, parent_id, status, created_at, updated_at, created_by, updated_by)
		 values (1, 1, '이슈 흐름', null, 'active', `+at+`, `+at+`, 'claude', 'claude')`,
		`insert into issue(id, project_id, number, key, title, type, status, priority, topic_id, status_reason, acknowledged_at, acknowledged_by, version,
		                   created_at, updated_at, closed_at, status_changed_at, status_changed_by, created_by, updated_by)
		 values (1, 1, 1, 'AGB-1', '주제가 붙은 이슈', 'feature', 'todo', 'P2', 1, '백로그 → 할 일', `+at+`, 'claude', 4, `+at+`, `+at+`, null, `+at+`, 'claude', 'claude', 'claude'),
		        (2, 1, 2, 'AGB-2', '닫힌 이슈', 'bug', 'done', 'P1', null, null, `+at+`, null, 1, `+at+`, `+at+`, `+at+`, `+at+`, 'claude', 'claude', 'claude')`,
		`insert into activity(issue_id, kind, actor_id, session_id, client_host, at, changes)
		 values (1, 'created', 'claude', 's1', 'desk', `+at+`, '{"title":{"to":"주제가 붙은 이슈"}}')`,
	)
	for _, stmt := range seed {
		if _, err := legacy.Exec(stmt); err != nil {
			t.Fatalf("seed %.40q: %v", stmt, err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a version 3 database: %v", err)
	}
	defer s.Close()

	var version int
	if err := s.db.QueryRow("pragma user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion || SchemaVersion < 4 {
		t.Errorf("user_version = %d, SchemaVersion = %d, want the current version, at least 4", version, SchemaVersion)
	}

	columns := func(table string) map[string]string {
		rows, err := s.db.Query(`select name, type || ':' || "notnull" || ':' || coalesce(dflt_value, '') from pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var name, kind string
			if err := rows.Scan(&name, &kind); err != nil {
				t.Fatal(err)
			}
			out[name] = kind
		}
		return out
	}
	if got := columns("issue")["milestone_id"]; got != "INTEGER:0:" {
		t.Errorf("issue.milestone_id = %q, want a nullable INTEGER without a default", got)
	}
	milestoneCols := columns("milestone")
	wantCols := map[string]string{
		"id": "INTEGER:0:", "project_id": "INTEGER:1:", "name": "TEXT:1:", "description": "TEXT:1:''", "target_date": "TEXT:0:",
		"status": "TEXT:1:'open'", "closed_at": "TEXT:0:", "created_at": "TEXT:1:", "updated_at": "TEXT:1:", "created_by": "TEXT:1:", "updated_by": "TEXT:1:",
	}
	for name, want := range wantCols {
		if got := milestoneCols[name]; got != want {
			t.Errorf("milestone.%s = %q, want %q", name, got, want)
		}
	}
	if len(milestoneCols) != len(wantCols) {
		t.Errorf("milestone has %d columns, want %d: %v", len(milestoneCols), len(wantCols), milestoneCols)
	}
	var indexes int
	if err := s.db.QueryRow(`select count(*) from sqlite_master where type = 'index' and name in ('milestone_project', 'issue_milestone')`).Scan(&indexes); err != nil || indexes != 2 {
		t.Errorf("milestone indexes = %d, %v; want milestone_project and issue_milestone", indexes, err)
	}
	// The table checks its status values and the foreign keys hold.
	if _, err := s.db.Exec(`insert into milestone(project_id, name, status, created_at, updated_at, created_by, updated_by) values (1, 'x', 'bogus', ` + at + `, ` + at + `, 'claude', 'claude')`); err == nil {
		t.Error("a milestone with an unknown status was accepted")
	}
	rows, err := s.db.Query(`pragma foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Error("foreign_key_check reports a violation after the migration")
	}
	rows.Close()

	// Old issues keep every value and have no milestone.
	old, err := s.GetIssue(ctx, "AGB-1")
	if err != nil {
		t.Fatal(err)
	}
	if old.Milestone != nil || old.MilestoneName != nil || old.Topic == nil || *old.Topic != 1 || old.Title != "주제가 붙은 이슈" || old.Version != 4 ||
		old.StatusReason == nil || *old.StatusReason != "백로그 → 할 일" || old.AcknowledgedBy == nil || *old.AcknowledgedBy != "claude" {
		t.Errorf("migrated issue = %+v", old)
	}
	var withMilestone int
	if err := s.db.QueryRow(`select count(*) from issue where milestone_id is not null`).Scan(&withMilestone); err != nil || withMilestone != 0 {
		t.Errorf("issues with a milestone after the migration = %d, %v", withMilestone, err)
	}

	// The new feature works on the migrated data.
	tr := model.Trace{Actor: "claude", SessionID: "s2", ClientHost: "desk"}
	m, err := s.CreateMilestone(ctx, tr, "AGB", MilestoneInput{Name: "v0.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpdateIssue(ctx, tr, "AGB-1", IssuePatch{Milestone: Optional[int64]{Set: true, Value: &m.ID}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpdateIssue(ctx, tr, "AGB-2", IssuePatch{Milestone: Optional[int64]{Set: true, Value: &m.ID}}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMilestone(ctx, m.ID)
	if err != nil || got.Progress != (model.MilestoneProgress{Total: 2, Done: 1}) {
		t.Errorf("milestone after attaching the old issues = %+v, %v", got, err)
	}
}

// TestMigrateAddsTokenToVersion4Database upgrades a database written under
// schema version 4. Migration 0005 adds the token table and its index; the
// data that exists stays as it is and no token exists yet.
func TestMigrateAddsTokenToVersion4Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agb.db")
	legacy, err := sql.Open("sqlite", DSN(path))
	if err != nil {
		t.Fatal(err)
	}
	const at = "'2026-10-17T05:00:00.000Z'"
	var seed []string
	for _, name := range []string{"migrations/0001_init.sql", "migrations/0002_activity_summary.sql", "migrations/0003_topic_ack_reason.sql", "migrations/0004_milestone.sql"} {
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		seed = append(seed, string(body))
	}
	seed = append(seed,
		`pragma user_version = 4`,
		`insert into actor(id, kind, created_at) values ('owner', 'user', `+at+`), ('claude', 'agent', `+at+`)`,
		`insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
		 values (1, 'AGB', 'agent-board', 'active', 2, `+at+`, `+at+`, 'owner', 'owner')`,
		`insert into milestone(id, project_id, name, created_at, updated_at, created_by, updated_by) values (1, 1, 'v0.1.0', `+at+`, `+at+`, 'owner', 'owner')`,
		`insert into issue(id, project_id, number, key, title, type, status, priority, milestone_id, version,
		                   created_at, updated_at, closed_at, status_changed_at, status_changed_by, created_by, updated_by)
		 values (1, 1, 1, 'AGB-1', '마일스톤이 붙은 이슈', 'feature', 'todo', 'P2', 1, 3, `+at+`, `+at+`, null, `+at+`, 'claude', 'claude', 'claude')`,
	)
	for _, stmt := range seed {
		if _, err := legacy.Exec(stmt); err != nil {
			t.Fatalf("seed %.40q: %v", stmt, err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a version 4 database: %v", err)
	}
	defer s.Close()

	var version int
	if err := s.db.QueryRow("pragma user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 5 || SchemaVersion != 5 {
		t.Errorf("user_version = %d, SchemaVersion = %d, want 5", version, SchemaVersion)
	}

	rows, err := s.db.Query(`select name, type || ':' || "notnull" || ':' || coalesce(dflt_value, '') from pragma_table_info('token')`)
	if err != nil {
		t.Fatal(err)
	}
	cols := map[string]string{}
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			t.Fatal(err)
		}
		cols[name] = kind
	}
	rows.Close()
	wantCols := map[string]string{
		"id": "INTEGER:0:", "actor_id": "TEXT:1:", "client_host": "TEXT:1:", "token_hash": "TEXT:1:", "created_at": "TEXT:1:",
		"created_by": "TEXT:1:", "last_used_at": "TEXT:0:", "revoked_at": "TEXT:0:", "revoked_by": "TEXT:0:",
	}
	if !reflect.DeepEqual(cols, wantCols) {
		t.Errorf("token columns = %v, want %v", cols, wantCols)
	}
	var indexes int
	if err := s.db.QueryRow(`select count(*) from sqlite_master where type = 'index' and tbl_name = 'token' and name = 'token_actor'`).Scan(&indexes); err != nil || indexes != 1 {
		t.Errorf("token_actor index = %d, %v", indexes, err)
	}
	// The hash is unique: a second token cannot share it.
	insert := `insert into token(actor_id, client_host, token_hash, created_at, created_by) values ('claude', 'pc', 'h', ` + at + `, 'owner')`
	if _, err := s.db.Exec(insert); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(insert); err == nil {
		t.Error("two tokens with the same hash were accepted")
	}
	if _, err := s.db.Exec(`delete from token`); err != nil {
		t.Fatal(err)
	}
	fk, err := s.db.Query(`pragma foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if fk.Next() {
		t.Error("foreign_key_check reports a violation after the migration")
	}
	fk.Close()

	// Existing data is untouched and the feature works on it.
	old, err := s.GetIssue(ctx, "AGB-1")
	if err != nil || old.Title != "마일스톤이 붙은 이슈" || old.Version != 3 || old.Milestone == nil || *old.Milestone != 1 {
		t.Errorf("migrated issue = %+v, %v", old, err)
	}
	tok, err := s.CreateToken(ctx, TokenInput{Actor: "claude", ClientHost: "desk", Secret: "agb_secret", CreatedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.AuthenticateToken(ctx, "agb_secret"); err != nil || got.ID != tok.ID {
		t.Errorf("authenticate on the migrated database = %+v, %v", got, err)
	}
}

func TestEnsureActors(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	actors := []model.Actor{{ID: "owner", Kind: model.ActorUser}, {ID: "claude", Kind: model.ActorAgent}}
	if err := s.EnsureActors(ctx, actors); err != nil {
		t.Fatal(err)
	}
	// Re-declaring with a changed kind updates the row; nothing is deleted.
	if err := s.EnsureActors(ctx, []model.Actor{{ID: "claude", Kind: model.ActorUser}}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow("select count(*) from actor").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("actor rows = %d, want 2", n)
	}
	var kind string
	if err := s.db.QueryRow("select kind from actor where id = 'claude'").Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "user" {
		t.Errorf("claude kind = %q, want user", kind)
	}
}

func TestFTS5Available(t *testing.T) {
	s := openTest(t)
	if _, err := s.db.Exec("insert into issue_fts(issue_fts) values ('rebuild')"); err != nil {
		t.Fatalf("fts5 rebuild: %v", err)
	}
}
