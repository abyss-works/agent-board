package store

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

func TestAckMessageHidesItFromThatReaderOnly(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	advance := msgClock(s, time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC))
	a := putMessage(t, s, "AGB", "desk", "a", "")
	advance(time.Minute)
	putMessage(t, s, "AGB", "codex", "b", "")
	advance(time.Minute)

	r, err := s.AckMessage(ctx, trOwner, a.ID, "alpha")
	if err != nil {
		t.Fatalf("AckMessage: %v", err)
	}
	if r.MessageID != a.ID || r.Reader != "alpha" || r.ReadBy != "owner" || r.ReadSession != trOwner.SessionID || r.ReadHost != trOwner.ClientHost ||
		r.ReadAt != model.NewTime(time.Date(2026, 11, 8, 0, 2, 0, 0, time.UTC)) {
		t.Errorf("read = %+v", r)
	}

	list := func(f MessageFilter) (string, int) {
		t.Helper()
		f.Project, f.Limit = "AGB", 50
		items, total, err := s.ListMessages(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range items {
			if m.Reads != nil {
				t.Errorf("list item %d carries reads", m.ID)
			}
		}
		return titles(items), total
	}
	if got, total := list(MessageFilter{UnreadBy: "alpha"}); got != "b" || total != 1 {
		t.Errorf("unread by alpha = %s total %d", got, total)
	}
	if got, total := list(MessageFilter{UnreadBy: "beta"}); got != "b,a" || total != 2 {
		t.Errorf("unread by beta = %s total %d", got, total)
	}
	if got, total := list(MessageFilter{}); got != "b,a" || total != 2 {
		t.Errorf("default list = %s total %d", got, total)
	}
	// The filter joins the others.
	if got, _ := list(MessageFilter{UnreadBy: "beta", To: "desk"}); got != "a" {
		t.Errorf("unread by beta to desk = %s", got)
	}
	if got, _ := list(MessageFilter{UnreadBy: "alpha", To: "desk"}); got != "" {
		t.Errorf("unread by alpha to desk = %s", got)
	}
	if _, err := s.DeleteMessage(ctx, trOwner, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := list(MessageFilter{UnreadBy: "beta"}); got != "b" {
		t.Errorf("unread by beta after delete = %s", got)
	}
	if got, _ := list(MessageFilter{UnreadBy: "beta", All: true}); got != "b,a" {
		t.Errorf("unread by beta with all = %s", got)
	}
	if got, _ := list(MessageFilter{UnreadBy: "alpha", All: true}); got != "b" {
		t.Errorf("unread by alpha with all = %s", got)
	}
}

func TestAckMessageAgainKeepsTheFirstRecord(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	advance := msgClock(s, time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC))
	m := putMessage(t, s, "AGB", "", "t", "")
	first, err := s.AckMessage(ctx, trOwner, m.ID, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	advance(time.Hour)
	again, err := s.AckMessage(ctx, trClaude, m.ID, "alpha")
	if err != nil || *again != *first {
		t.Errorf("second ack = %+v, %v; first %+v", again, err, first)
	}
	if _, err := s.AckMessage(ctx, trClaude, m.ID, "beta"); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetMessage(ctx, m.ID)
	if err != nil || len(got.Reads) != 2 || got.Reads[0] != *first || got.Reads[1].Reader != "beta" || got.Reads[1].ReadBy != "claude" {
		t.Errorf("reads = %+v, %v", got.Reads, err)
	}
	if n := queryStrings(t, s, `select count(*) from message_read`); n[0] != "2" {
		t.Errorf("rows = %v", n)
	}
	// A message nobody acknowledged has an empty array, not none.
	if plain := putMessage(t, s, "AGB", "", "u", ""); plain.Reads == nil || len(plain.Reads) != 0 {
		t.Errorf("reads of a new message = %#v", plain.Reads)
	}
}

func TestAckMessageRejects(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	m := putMessage(t, s, "AGB", "", "t", "")
	if _, err := s.AckMessage(ctx, trOwner, 9999, "alpha"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing id err = %v", err)
	}
	for _, reader := range []string{"", "a b", "a\tb", strings.Repeat("가", model.MaxMessageToRunes+1)} {
		_, err := s.AckMessage(ctx, trOwner, m.ID, reader)
		if got := strings.Join(violationFields(err), ","); got != "reader" {
			t.Errorf("reader %q: fields = %s (%v)", reader, got, err)
		}
	}
	if _, err := s.AckMessage(ctx, trOwner, m.ID, strings.Repeat("가", model.MaxMessageToRunes)); err != nil {
		t.Errorf("a reader of the longest length: %v", err)
	}
	if n := queryStrings(t, s, `select count(*) from message_read`); n[0] != "1" {
		t.Errorf("rows = %v", n)
	}
}

func TestAckMessageTakesDeletedAndExpiredMessages(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	advance := msgClock(s, time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC))
	deleted := putMessage(t, s, "AGB", "", "d", "")
	expired := putMessage(t, s, "AGB", "", "e", "1h")
	if _, err := s.DeleteMessage(ctx, trOwner, deleted.ID); err != nil {
		t.Fatal(err)
	}
	advance(2 * time.Hour)
	for _, id := range []int64{deleted.ID, expired.ID} {
		if _, err := s.AckMessage(ctx, trOwner, id, "alpha"); err != nil {
			t.Errorf("ack %d: %v", id, err)
		}
	}
	// The message row is as it was.
	if got, _ := s.GetMessage(ctx, expired.ID); got.State != model.MessageExpired || got.DeletedAt != nil || len(got.Reads) != 1 {
		t.Errorf("expired = %+v", got)
	}
}

func TestAckMessageNeedsActiveProject(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	m := putMessage(t, s, "AGB", "", "t", "")
	if _, err := s.AckMessage(ctx, trOwner, m.ID, "alpha"); err != nil {
		t.Fatal(err)
	}
	st := model.ProjectArchived
	if _, err := s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &st}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AckMessage(ctx, trOwner, m.ID, "beta"); !errors.Is(err, ErrProjectNotActive) {
		t.Errorf("ack in archived project err = %v", err)
	}
	// Acknowledging again writes nothing, so it passes.
	if _, err := s.AckMessage(ctx, trOwner, m.ID, "alpha"); err != nil {
		t.Errorf("second ack in archived project err = %v", err)
	}
}

func TestMigration19AddsMessageReadAndKeepsMessages(t *testing.T) {
	path := openAt17(t, func(db *sql.DB) {
		mustExec(t, db, `insert into actor(id, kind, created_at) values ('claude', 'agent', 'x')`)
		mustExec(t, db, `insert into project(id, key, name, status, next_number, created_at, updated_at, created_by, updated_by)
			values (1, 'AGB', 'AGB', 'active', 1, '2026-10-01T00:00:00.000Z', '2026-10-01T00:00:00.000Z', 'claude', 'claude')`)
		mustExec(t, db, `insert into message(id, project_id, title, body, created_at, created_by, created_session, created_host, expires_at)
			values (7, 1, 't', 'b', '2026-10-01T00:00:00.000Z', 'claude', 's', 'h', '2099-01-01T00:00:00.000Z')`)
	})
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if v := queryStrings(t, s, `pragma user_version`); v[0] != strconv.Itoa(SchemaVersion) || SchemaVersion < 19 {
		t.Errorf("user_version = %v, SchemaVersion = %d", v, SchemaVersion)
	}
	if n := queryStrings(t, s, `select (select count(*) from message) || ',' || (select count(*) from message_read)`); n[0] != "1,0" {
		t.Errorf("rows = %v", n)
	}
	if _, err := s.AckMessage(t.Context(), trClaude, 7, "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`insert into message_read(message_id, reader, read_at, read_by, read_session, read_host) values (7, 'alpha', 'x', 'claude', 's', 'h')`); err == nil {
		t.Error("a second row of one message and reader must fail")
	}
	if fk := queryStrings(t, s, `pragma foreign_key_check`); len(fk) != 0 {
		t.Errorf("foreign_key_check = %v", fk)
	}
}
