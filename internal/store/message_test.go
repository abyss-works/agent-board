package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/abyss-works/agent-board/internal/model"
)

func putMessage(t *testing.T, s *Store, project, to, title, ttl string) *model.Message {
	t.Helper()
	m, err := s.CreateMessage(t.Context(), trClaude, project, MessageInput{To: to, Title: title, Body: "본문 " + title, TTL: ttl})
	if err != nil {
		t.Fatalf("CreateMessage(%s): %v", title, err)
	}
	return m
}

func titles(items []model.Message) string {
	out := make([]string, len(items))
	for i, m := range items {
		out[i] = m.Title
	}
	return strings.Join(out, ",")
}

// msgClock fixes the store clock; the returned function moves it.
func msgClock(s *Store, start time.Time) func(d time.Duration) {
	now := start
	s.SetClock(func() time.Time { return now })
	return func(d time.Duration) { now = now.Add(d) }
}

func TestCreateAndGetMessage(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	start := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	msgClock(s, start)

	m := putMessage(t, s, "AGB", "reviewer", "배포 끝", "")
	if m.ID < 1 || m.Project != "AGB" || m.To == nil || *m.To != "reviewer" || m.Title != "배포 끝" || m.Body == nil || *m.Body != "본문 배포 끝" ||
		m.State != model.MessageActive || m.Age != "0m" || m.CreatedBy != "claude" || m.CreatedSession != "s-claude" || m.CreatedHost != "laptop" ||
		m.DeletedAt != nil || m.DeletedBy != nil {
		t.Errorf("message = %+v", m)
	}
	if want := model.NewTime(start.Add(model.DefaultMessageTTL)); m.ExpiresAt != want {
		t.Errorf("expires_at = %v, want the default 7 days: %v", m.ExpiresAt, want)
	}
	got, err := s.GetMessage(ctx, m.ID)
	if err != nil || got.ID != m.ID || got.Body == nil || *got.Body != *m.Body {
		t.Errorf("GetMessage = %+v, %v", got, err)
	}
	plain := putMessage(t, s, "AGB", "", "수신자 없음", "3d")
	if plain.To != nil || plain.ExpiresAt != model.NewTime(start.Add(72*time.Hour)) {
		t.Errorf("plain = %+v", plain)
	}
	if _, err := s.GetMessage(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing id err = %v", err)
	}
}

func TestCreateMessageRejects(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	if _, err := s.CreateMessage(ctx, trClaude, "NOPE", MessageInput{Title: "t"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown project err = %v", err)
	}
	_, err := s.CreateMessage(ctx, trClaude, "AGB", MessageInput{Title: "", TTL: "91d", To: "a b"})
	if got := strings.Join(violationFields(err), ","); got != "to,title,ttl" {
		t.Errorf("fields = %s (%v)", got, err)
	}
	if _, err := s.CreateMessage(ctx, trClaude, "AGB", MessageInput{Title: "t", Body: strings.Repeat("a", model.MaxBodyBytes+1)}); err == nil {
		t.Error("an oversized body must fail")
	}
	st := model.ProjectArchived
	if _, err := s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &st}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMessage(ctx, trClaude, "AGB", MessageInput{Title: "t"}); !errors.Is(err, ErrProjectNotActive) {
		t.Errorf("archived project err = %v", err)
	}
}

func TestListMessagesDefaultHidesExpiredAndDeleted(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	advance := msgClock(s, time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC))

	putMessage(t, s, "AGB", "desk", "짧음", "1h")
	advance(time.Minute)
	removed := putMessage(t, s, "AGB", "desk", "지움", "")
	advance(time.Minute)
	putMessage(t, s, "AGB", "codex", "코덱스", "")
	advance(time.Minute)
	putMessage(t, s, "AGB", "", "수신자 없음", "")
	advance(time.Minute)
	putMessage(t, s, "OTH", "desk", "다른 프로젝트", "")
	if _, err := s.DeleteMessage(ctx, trClaude, removed.ID); err != nil {
		t.Fatal(err)
	}

	f := MessageFilter{Project: "AGB", Limit: 50}
	items, total, err := s.ListMessages(ctx, f)
	if err != nil || total != 3 || titles(items) != "수신자 없음,코덱스,짧음" {
		t.Fatalf("list = %s total %d err %v", titles(items), total, err)
	}
	for _, m := range items {
		if m.Body != nil {
			t.Errorf("list item %d carries a body", m.ID)
		}
	}

	advance(2 * time.Hour) // the short one expires
	items, total, _ = s.ListMessages(ctx, f)
	if total != 2 || titles(items) != "수신자 없음,코덱스" {
		t.Errorf("after expiry: %s total %d", titles(items), total)
	}

	f.To = "desk"
	if items, total, _ = s.ListMessages(ctx, f); total != 0 || len(items) != 0 {
		t.Errorf("to=desk: %s", titles(items))
	}
	f.To = "codex"
	if items, total, _ = s.ListMessages(ctx, f); titles(items) != "코덱스" || total != 1 {
		t.Errorf("to=codex: %s", titles(items))
	}

	f = MessageFilter{Project: "AGB", All: true, Limit: 50}
	items, total, _ = s.ListMessages(ctx, f)
	if total != 4 || titles(items) != "수신자 없음,코덱스,지움,짧음" {
		t.Fatalf("all: %s total %d", titles(items), total)
	}
	states := map[string]string{}
	for _, m := range items {
		states[m.Title] = m.State
	}
	if states["짧음"] != model.MessageExpired || states["지움"] != model.MessageDeleted || states["코덱스"] != model.MessageActive {
		t.Errorf("states = %v", states)
	}
	f.To = "desk"
	if items, total, _ = s.ListMessages(ctx, f); total != 2 || titles(items) != "지움,짧음" {
		t.Errorf("all with to=desk: %s", titles(items))
	}

	if _, _, err := s.ListMessages(ctx, MessageFilter{Project: "NOPE", Limit: 50}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown project err = %v", err)
	}
}

func TestListMessagesPaging(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	advance := msgClock(s, time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC))
	for _, title := range []string{"a", "b", "c"} {
		putMessage(t, s, "AGB", "", title, "")
		advance(time.Second)
	}
	// Same created_at: the later id comes first.
	putMessage(t, s, "AGB", "", "d", "")
	putMessage(t, s, "AGB", "", "e", "")
	items, total, _ := s.ListMessages(ctx, MessageFilter{Project: "AGB", Limit: 2, Offset: 1})
	if total != 5 || titles(items) != "d,c" {
		t.Errorf("page = %s total %d", titles(items), total)
	}
}

func TestDeleteMessageIsSoft(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	advance := msgClock(s, time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC))
	m := putMessage(t, s, "AGB", "desk", "지울 것", "")
	advance(5 * time.Minute)

	d, err := s.DeleteMessage(ctx, trOwner, m.ID)
	if err != nil || d.State != model.MessageDeleted || d.DeletedBy == nil || *d.DeletedBy != "owner" || d.DeletedAt == nil || d.Age != "5m" {
		t.Fatalf("delete = %+v, %v", d, err)
	}
	var rows int
	s.db.QueryRow(`select count(*) from message where id = ? and deleted_at is not null`, m.ID).Scan(&rows)
	if rows != 1 {
		t.Errorf("the row must stay with deleted_at set, got %d", rows)
	}
	got, err := s.GetMessage(ctx, m.ID)
	if err != nil || got.State != model.MessageDeleted || got.Body == nil || *got.Body != "본문 지울 것" {
		t.Errorf("get after delete = %+v, %v", got, err)
	}

	// Deleting again keeps who and when of the first deletion.
	advance(time.Hour)
	again, err := s.DeleteMessage(ctx, trClaude, m.ID)
	if err != nil || *again.DeletedBy != "owner" || *again.DeletedAt != *d.DeletedAt {
		t.Errorf("second delete = %+v, %v", again, err)
	}
	if _, err := s.DeleteMessage(ctx, trOwner, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing id err = %v", err)
	}
}

func TestMessageAge(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	advance := msgClock(s, time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC))
	m := putMessage(t, s, "AGB", "", "t", "90d")
	var elapsed time.Duration
	for _, c := range []struct {
		to   time.Duration
		want string
	}{{30 * time.Second, "0m"}, {59*time.Minute + 30*time.Second, "59m"}, {time.Hour, "1h"}, {23*time.Hour + 59*time.Minute, "23h"}, {24 * time.Hour, "1d"}, {49 * time.Hour, "2d"}} {
		advance(c.to - elapsed)
		elapsed = c.to
		got, _ := s.GetMessage(ctx, m.ID)
		if got.Age != c.want {
			t.Errorf("after %v age = %s, want %s", c.to, got.Age, c.want)
		}
	}
}

func TestDeleteMessageNeedsActiveProject(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	m := putMessage(t, s, "AGB", "", "t", "")
	st := model.ProjectArchived
	if _, err := s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &st}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteMessage(ctx, trOwner, m.ID); !errors.Is(err, ErrProjectNotActive) {
		t.Errorf("delete in archived project err = %v", err)
	}
	if got, _ := s.GetMessage(ctx, m.ID); got.State != model.MessageActive {
		t.Errorf("state = %s", got.State)
	}
}
