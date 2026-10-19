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
	if version != SchemaVersion || SchemaVersion < 5 {
		t.Errorf("user_version = %d, SchemaVersion = %d, want the current version, at least 5", version, SchemaVersion)
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

// TestMigrateRemovesTodoAndDecisionFromVersion5Database upgrades a database
// written under schema version 5. Migration 0006 rebuilds the issue table: the
// todo status and the decision type leave its CHECK constraints, issues in todo
// move to backlog, and issues of type decision become design issues that carry
// their title as decision_needed. Everything else about the issues, the rows
// that point at them and the search index stays as it was.
func TestMigrateRemovesTodoAndDecisionFromVersion5Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agb.db")
	legacy, err := sql.Open("sqlite", DSN(path))
	if err != nil {
		t.Fatal(err)
	}
	const at = "'2026-10-17T05:00:00.000Z'"
	const later = "'2026-10-17T06:30:00.000Z'"
	longTitle := strings.Repeat("결", 600)
	var seed []string
	for _, name := range []string{"migrations/0001_init.sql", "migrations/0002_activity_summary.sql", "migrations/0003_topic_ack_reason.sql",
		"migrations/0004_milestone.sql", "migrations/0005_token.sql"} {
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		seed = append(seed, string(body))
	}
	const issueColumns = `id, project_id, number, key, title, type, status, priority, parent_id, recheck_condition, topic_id, milestone_id, status_reason,
		acknowledged_at, acknowledged_by, version, created_at, updated_at, closed_at, status_changed_at, status_changed_by, created_by, updated_by`
	seed = append(seed,
		`pragma user_version = 5`,
		`insert into actor(id, kind, created_at) values ('owner', 'user', `+at+`), ('claude', 'agent', `+at+`)`,
		`insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
		 values (1, 'AGB', 'agent-board', 'active', 8, `+at+`, `+at+`, 'owner', 'owner')`,
		`insert into topic(id, project_id, name, parent_id, status, created_at, updated_at, created_by, updated_by)
		 values (1, 1, '이슈 흐름', null, 'active', `+at+`, `+at+`, 'owner', 'owner')`,
		`insert into milestone(id, project_id, name, created_at, updated_at, created_by, updated_by) values (1, 1, 'v0.1.0', `+at+`, `+at+`, 'owner', 'owner')`,
		`insert into issue(`+issueColumns+`) values
		 (1, 1, 1, 'AGB-1', '할 일이던 이슈', 'feature', 'todo', 'P1', null, null, 1, 1, '백로그 → 할 일', `+at+`, 'owner', 4, `+at+`, `+later+`, null, `+later+`, 'claude', 'claude', 'claude'),
		 (2, 1, 2, 'AGB-2', '저장소를 SQLite로 둘지 정한다', 'decision', 'review', 'P0', null, null, null, null, '진행 → 검토', null, null, 2, `+at+`, `+later+`, null, `+later+`, 'claude', 'claude', 'claude'),
		 (4, 1, 4, 'AGB-4', '이미 정한 결정', 'decision', 'done', 'P2', null, null, null, null, '완료', `+at+`, 'owner', 3, `+at+`, `+later+`, `+later+`, `+later+`, 'owner', 'owner', 'owner'),
		 (5, 1, 5, 'AGB-5', '하위 이슈', 'bug', 'todo', 'P2', 1, null, null, null, null, null, null, 1, `+at+`, `+at+`, null, `+at+`, 'claude', 'claude', 'claude'),
		 (6, 1, 6, 'AGB-6', '관측', 'observation', 'backlog', 'P3', 7, '다음 릴리즈', null, null, null, null, null, 1, `+at+`, `+at+`, null, `+at+`, 'claude', 'claude', 'claude'),
		 (7, 1, 7, 'AGB-7', '취소한 설계', 'design', 'cancelled', 'P2', null, null, null, null, 'AGB-6과 중복', null, null, 2, `+at+`, `+later+`, `+later+`, `+later+`, 'owner', 'owner', 'owner')`,
		`insert into issue_label(issue_id, label) values (1, 'api'), (5, 'cli')`,
		`insert into issue_ref(issue_id, ref) values (1, 'doc:api.md')`,
		`insert into issue_dependency(issue_id, depends_on_id) values (5, 2)`,
		`insert into activity(issue_id, kind, actor_id, session_id, client_host, at, changes)
		 values (1, 'created', 'claude', 's1', 'desk', `+at+`, '{"status":{"to":"backlog"},"title":{"to":"할 일이던 이슈"}}'),
		        (1, 'updated', 'claude', 's1', 'desk', `+later+`, '{"status":{"from":"backlog","to":"todo"},"status_reason":{"from":null,"to":"백로그 → 할 일"}}'),
		        (2, 'created', 'claude', 's1', 'desk', `+at+`, '{"type":{"to":"decision"}}')`,
		`insert into activity(issue_id, kind, actor_id, session_id, client_host, at, body) values (2, 'commented', 'owner', 's2', 'pc', `+later+`, '선택지를 정리했다.')`,
	)
	for _, stmt := range seed {
		if _, err := legacy.Exec(stmt); err != nil {
			t.Fatalf("seed %.40q: %v", stmt, err)
		}
	}
	// A title longer than any API accepts, to see the question cut at 500 characters.
	if _, err := legacy.Exec(`insert into issue(`+issueColumns+`) values
		(3, 1, 3, 'AGB-3', ?, 'decision', 'backlog', 'P2', null, null, null, null, null, `+at+`, 'owner', 1, `+at+`, `+at+`, null, `+at+`, 'owner', 'owner', 'owner')`, longTitle); err != nil {
		t.Fatal(err)
	}
	var activitiesBefore int
	if err := legacy.QueryRow(`select count(*) from activity`).Scan(&activitiesBefore); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a version 5 database: %v", err)
	}
	defer s.Close()

	var version int
	if err := s.db.QueryRow("pragma user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion || SchemaVersion < 6 {
		t.Errorf("user_version = %d, SchemaVersion = %d, want the current version, at least 6", version, SchemaVersion)
	}

	type want struct {
		typ      model.IssueType
		status   model.IssueStatus
		question *string
	}
	wants := map[string]want{
		"AGB-1": {model.TypeFeature, model.StatusBacklog, nil},
		"AGB-2": {model.TypeDesign, model.StatusReview, str("저장소를 SQLite로 둘지 정한다")},
		"AGB-3": {model.TypeDesign, model.StatusBacklog, str(strings.Repeat("결", 500))},
		"AGB-4": {model.TypeDesign, model.StatusDone, str("이미 정한 결정")},
		"AGB-5": {model.TypeBug, model.StatusBacklog, nil},
		"AGB-6": {model.TypeObservation, model.StatusBacklog, nil},
		"AGB-7": {model.TypeDesign, model.StatusCancelled, nil},
	}
	for key, w := range wants {
		got, err := s.GetIssue(ctx, key)
		if err != nil {
			t.Errorf("%s: %v", key, err)
			continue
		}
		if got.Type != w.typ || got.Status != w.status || !reflect.DeepEqual(got.DecisionNeeded, w.question) {
			t.Errorf("%s: type %s, status %s, decision_needed %v; want %s, %s, %v", key, got.Type, got.Status, got.DecisionNeeded, w.typ, w.status, w.question)
		}
	}

	// Nothing else about an issue changes: not the reason, the stamps, the version, the acknowledgement or the links.
	one, err := s.GetIssue(ctx, "AGB-1")
	if err != nil {
		t.Fatal(err)
	}
	if one.StatusReason == nil || *one.StatusReason != "백로그 → 할 일" || one.StatusChangedBy != "claude" || one.StatusChangedAt.String() != "2026-10-17T06:30:00.000Z" ||
		one.UpdatedAt.String() != "2026-10-17T06:30:00.000Z" || one.Version != 4 || one.AcknowledgedBy == nil || *one.AcknowledgedBy != "owner" ||
		one.Topic == nil || *one.Topic != 1 || one.Milestone == nil || *one.Milestone != 1 ||
		!reflect.DeepEqual(one.Labels, []string{"api"}) || !reflect.DeepEqual(one.Refs, []string{"doc:api.md"}) {
		t.Errorf("AGB-1 after the migration = %+v", one)
	}
	two, err := s.GetIssue(ctx, "AGB-2")
	if err != nil {
		t.Fatal(err)
	}
	if two.StatusReason == nil || *two.StatusReason != "진행 → 검토" || two.AcknowledgedAt != nil || two.Version != 2 || two.Priority != model.P0 {
		t.Errorf("AGB-2 after the migration = %+v", two)
	}
	long, err := s.GetIssue(ctx, "AGB-3")
	if err != nil || long.Title != longTitle {
		t.Errorf("the title of AGB-3 must stay whole: %d characters, %v", len([]rune(long.Title)), err)
	}
	five, err := s.GetIssue(ctx, "AGB-5")
	if err != nil || five.Parent == nil || *five.Parent != "AGB-1" || !reflect.DeepEqual(five.DependsOn, []string{"AGB-2"}) || !reflect.DeepEqual(five.Labels, []string{"cli"}) {
		t.Errorf("AGB-5 links after the migration = %+v, %v", five, err)
	}
	six, err := s.GetIssue(ctx, "AGB-6")
	if err != nil || six.RecheckCondition == nil || *six.RecheckCondition != "다음 릴리즈" || six.Parent == nil || *six.Parent != "AGB-7" {
		t.Errorf("AGB-6 after the migration = %+v, %v", six, err)
	}

	// The migration writes no activity records and keeps the old ones as they were.
	var activitiesAfter int
	if err := s.db.QueryRow(`select count(*) from activity`).Scan(&activitiesAfter); err != nil || activitiesAfter != activitiesBefore {
		t.Errorf("activity records = %d, want %d (%v)", activitiesAfter, activitiesBefore, err)
	}
	acts, _, err := s.ListActivity(ctx, ActivityFilter{Issues: []string{"AGB-1"}, Kinds: []model.ActivityKind{model.ActivityUpdated}, Limit: 1})
	if err != nil || len(acts) != 1 || string(acts[0].Changes["status"].To) != `"todo"` {
		t.Errorf("the old record of AGB-1 = %+v, %v", acts, err)
	}

	// The table refuses the removed values and everything that points at it holds.
	for name, stmt := range map[string]string{
		"status todo":   `update issue set status = 'todo' where id = 1`,
		"type decision": `update issue set type = 'decision' where id = 1`,
	} {
		if _, err := s.db.Exec(stmt); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	fk, err := s.db.Query(`pragma foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if fk.Next() {
		t.Error("foreign_key_check reports a violation after the migration")
	}
	fk.Close()
	if _, err := s.db.Exec(`insert into issue_label(issue_id, label) values (99, 'orphan')`); err == nil {
		t.Error("a label of an issue that does not exist was accepted")
	}
	if _, err := s.db.Exec(`delete from issue where id = 1`); err == nil {
		t.Error("an issue that others point at was deleted")
	}
	var leftovers, deferred int
	if err := s.db.QueryRow(`select count(*) from sqlite_master where name = 'issue_old'`).Scan(&leftovers); err != nil || leftovers != 0 {
		t.Errorf("the copy issue_old was left behind: %d, %v", leftovers, err)
	}
	if err := s.db.QueryRow(`pragma defer_foreign_keys`).Scan(&deferred); err != nil || deferred != 0 {
		t.Errorf("foreign keys are still deferred after the migration: %d, %v", deferred, err)
	}
	var indexes, triggers int
	if err := s.db.QueryRow(`select (select count(*) from sqlite_master where type = 'index' and name in ('issue_project_status', 'issue_parent', 'issue_milestone')),
		(select count(*) from sqlite_master where type = 'trigger' and name in ('issue_ai', 'issue_ad', 'issue_au'))`).Scan(&indexes, &triggers); err != nil || indexes != 3 || triggers != 3 {
		t.Errorf("indexes = %d, triggers = %d, %v; want 3 and 3", indexes, triggers, err)
	}

	// The search index still matches the issue table and follows later edits.
	if _, err := s.db.Exec(`insert into issue_fts(issue_fts, rank) values ('integrity-check', 1)`); err != nil {
		t.Errorf("search index against the rebuilt table: %v", err)
	}
	search := func(q string) []string {
		items, _, err := s.ListIssues(ctx, IssueFilter{Statuses: model.IssueStatuses, Query: q, Sort: []Sort{{Field: "key"}}, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return keysOf(items, func(i model.IssueSummary) string { return i.Key })
	}
	if got := search("저장소를"); !reflect.DeepEqual(got, []string{"AGB-2"}) {
		t.Errorf("search before an edit = %v", got)
	}
	tr := model.Trace{Actor: "claude", SessionID: "s3", ClientHost: "desk"}
	if _, _, err := s.UpdateIssue(ctx, tr, "AGB-2", IssuePatch{Title: str("데이터베이스를 정한다")}, nil); err != nil {
		t.Fatal(err)
	}
	if got := search("데이터베이스를"); !reflect.DeepEqual(got, []string{"AGB-2"}) {
		t.Errorf("search after an edit = %v", got)
	}
	if got := search("저장소를"); len(got) != 0 {
		t.Errorf("the old title is still indexed: %v", got)
	}

	// The migrated database takes new issues, with and without a question.
	created, err := s.CreateIssue(ctx, tr, IssueInput{Project: "AGB", Title: "새 이슈", Body: tplBody(model.TypeFeature), DecisionNeeded: str("어느 쪽으로 할지")})
	if err != nil || created.Key != "AGB-8" || created.DecisionNeeded == nil || *created.DecisionNeeded != "어느 쪽으로 할지" {
		t.Errorf("new issue = %+v, %v", created, err)
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
