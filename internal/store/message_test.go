package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
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

func TestStoredMessageWithControlTitleStillReads(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "APP")
	m := putMessage(t, s, "APP", "", "정상 제목", "")
	if _, err := s.db.ExecContext(ctx, `UPDATE message SET title = ? WHERE id = ?`, "예전\n제목\t유지", m.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMessage(ctx, m.ID)
	if err != nil || got.Title != "예전\n제목\t유지" {
		t.Fatalf("GetMessage = %+v, %v", got, err)
	}
	items, total, err := s.ListMessages(ctx, MessageFilter{Project: "APP", Limit: 10})
	if err != nil || total != 1 || len(items) != 1 || items[0].Title != "예전\n제목\t유지" {
		t.Fatalf("ListMessages = %+v (total=%d), %v", items, total, err)
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

func TestMessageRoutingCreateFilterSessionIsolationAndReplies(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	iss1 := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "이슈 1"})
	iss2 := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "이슈 2"})
	advance := msgClock(s, time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))

	// Target session need not pre-exist in session table; actor must exist.
	root, err := s.CreateMessage(ctx, trClaude, "AGB", MessageInput{
		To:        "reviewer",
		ToSession: &model.SessionRef{Actor: "owner", SessionID: "sess-a"},
		ToRole:    "subagent",
		Issue:     iss1.Key,
		Title:     "검토 요청",
		Body:      "첫 본문",
	})
	if err != nil {
		t.Fatalf("CreateMessage root: %v", err)
	}
	if root.ToSession == nil || *root.ToSession != (model.SessionRef{Actor: "owner", SessionID: "sess-a"}) ||
		deref(root.ToRole) != "subagent" || deref(root.Issue) != iss1.Key || root.ReplyTo != nil ||
		root.ReplyCount != 0 || root.Replies == nil || len(*root.Replies) != 0 {
		t.Fatalf("root = %+v", root)
	}

	advance(time.Minute)
	broadcast, err := s.CreateMessage(ctx, trClaude, "AGB", MessageInput{
		ToRole: "runner",
		Issue:  iss2.Key,
		Title:  "공지",
		Body:   "모두에게",
	})
	if err != nil {
		t.Fatalf("CreateMessage broadcast: %v", err)
	}
	if broadcast.ToSession != nil || deref(broadcast.ToRole) != "runner" || deref(broadcast.Issue) != iss2.Key {
		t.Fatalf("broadcast = %+v", broadcast)
	}

	// Replies in chronological order, including an expired or deleted parent reply check.
	advance(time.Minute)
	reply1, err := s.CreateMessage(ctx, trOwner, "AGB", MessageInput{
		ToSession: &model.SessionRef{Actor: "claude", SessionID: "s-claude"},
		ReplyTo:   &root.ID,
		Title:     "답장 1",
		Body:      "첫 번째 답장",
	})
	if err != nil {
		t.Fatalf("CreateMessage reply1: %v", err)
	}
	advance(time.Minute)
	reply2, err := s.CreateMessage(ctx, trClaude, "AGB", MessageInput{
		ReplyTo: &root.ID,
		Title:   "답장 2",
		Body:    "두 번째 답장",
	})
	if err != nil {
		t.Fatalf("CreateMessage reply2: %v", err)
	}
	if reply1.ReplyTo == nil || *reply1.ReplyTo != root.ID {
		t.Errorf("reply1.ReplyTo = %v, want %d", reply1.ReplyTo, root.ID)
	}

	// GetMessage loads replies ascending with body and nil nested replies, matching reply_count.
	gotRoot, err := s.GetMessage(ctx, root.ID)
	if err != nil {
		t.Fatalf("GetMessage(root): %v", err)
	}
	if gotRoot.ReplyCount != 2 || gotRoot.Replies == nil || len(*gotRoot.Replies) != 2 {
		t.Fatalf("gotRoot replies = %+v (reply_count=%d)", gotRoot.Replies, gotRoot.ReplyCount)
	}
	replies := *gotRoot.Replies
	if replies[0].ID != reply1.ID || replies[1].ID != reply2.ID ||
		replies[0].Body == nil || *replies[0].Body != "첫 번째 답장" ||
		replies[1].Body == nil || *replies[1].Body != "두 번째 답장" ||
		replies[0].Replies != nil || replies[1].Replies != nil {
		t.Errorf("replies = %+v", replies)
	}

	// Replying to a deleted or expired message in the same project is allowed.
	shortMsg := putMessage(t, s, "AGB", "", "만료될 메시지", "1h")
	deletedMsg := putMessage(t, s, "AGB", "", "지울 메시지", "")
	if _, err := s.DeleteMessage(ctx, trOwner, deletedMsg.ID); err != nil {
		t.Fatal(err)
	}
	advance(2 * time.Hour)
	if _, err := s.CreateMessage(ctx, trClaude, "AGB", MessageInput{ReplyTo: &shortMsg.ID, Title: "만료 답장"}); err != nil {
		t.Errorf("reply to expired message failed: %v", err)
	}
	if _, err := s.CreateMessage(ctx, trClaude, "AGB", MessageInput{ReplyTo: &deletedMsg.ID, Title: "삭제 답장"}); err != nil {
		t.Errorf("reply to deleted message failed: %v", err)
	}

	// Session isolation in default and unread lists:
	// - owner/sess-a sees root, broadcast, reply2, and the two replies to short/deleted (no reply1).
	// - owner/sess-b sees broadcast, reply2, and the two replies to short/deleted (no root, no reply1).
	// - claude/s-claude sees broadcast, reply1, reply2, and the two replies (no root).
	// - All: true sees everything regardless of viewer.
	viewerA := &model.SessionRef{Actor: "owner", SessionID: "sess-a"}
	viewerB := &model.SessionRef{Actor: "owner", SessionID: "sess-b"}
	viewerC := &model.SessionRef{Actor: "claude", SessionID: "s-claude"}

	listTitles := func(f MessageFilter) (string, []model.Message) {
		t.Helper()
		f.Project, f.Limit = "AGB", 50
		items, _, err := s.ListMessages(ctx, f)
		if err != nil {
			t.Fatalf("ListMessages(%+v): %v", f, err)
		}
		for _, it := range items {
			if it.Replies != nil {
				t.Errorf("list item %d carries non-nil Replies", it.ID)
			}
		}
		return titles(items), items
	}

	gotA, itemsA := listTitles(MessageFilter{Viewer: viewerA, UnreadBy: "AGB"})
	if gotA != "삭제 답장,만료 답장,답장 2,공지,검토 요청" {
		t.Errorf("viewerA unread = %s", gotA)
	}
	for _, it := range itemsA {
		if it.ID == root.ID && it.ReplyCount != 2 {
			t.Errorf("list root reply_count = %d, want 2", it.ReplyCount)
		}
	}
	gotB, _ := listTitles(MessageFilter{Viewer: viewerB, UnreadBy: "AGB"})
	if gotB != "삭제 답장,만료 답장,답장 2,공지" {
		t.Errorf("viewerB unread = %s", gotB)
	}
	gotC, _ := listTitles(MessageFilter{Viewer: viewerC, UnreadBy: "AGB"})
	if gotC != "삭제 답장,만료 답장,답장 2,답장 1,공지" {
		t.Errorf("viewerC unread = %s", gotC)
	}
	gotAll, _ := listTitles(MessageFilter{Viewer: viewerB, All: true})
	if gotAll != "삭제 답장,만료 답장,지울 메시지,만료될 메시지,답장 2,답장 1,공지,검토 요청" {
		t.Errorf("viewerB all = %s", gotAll)
	}

	// Explicit filters: to_session, to_role, issue, reply_to.
	if got, _ := listTitles(MessageFilter{Viewer: viewerB, ToSession: viewerA}); got != "검토 요청" {
		t.Errorf("filter to_session = %s", got)
	}
	if got, _ := listTitles(MessageFilter{ToRole: "runner"}); got != "공지" {
		t.Errorf("filter to_role = %s", got)
	}
	if got, _ := listTitles(MessageFilter{Issue: iss1.Key}); got != "검토 요청" {
		t.Errorf("filter issue = %s", got)
	}
	if got, _ := listTitles(MessageFilter{ReplyTo: &root.ID}); got != "답장 2,답장 1" {
		t.Errorf("filter reply_to = %s", got)
	}

	// Verify message.created event payload includes routing fields when non-empty.
	evs, _, err := s.ListEvents(ctx, EventFilter{Project: "AGB", Kinds: []string{model.EventMessageCreated}, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	bySub := map[string]map[string]any{}
	for _, ev := range evs {
		var d map[string]any
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			t.Fatal(err)
		}
		bySub[ev.Subject] = d
	}
	dRoot := bySub["1"]
	if dRoot["to"] != "reviewer" || dRoot["to_session"] != "owner/sess-a" || dRoot["to_role"] != "subagent" || dRoot["issue"] != iss1.Key || dRoot["reply_to"] != nil {
		t.Errorf("root event data = %v", dRoot)
	}
	dReply1 := bySub["3"]
	if dReply1["to_session"] != "claude/s-claude" || dReply1["reply_to"] != float64(root.ID) || dReply1["to"] != nil || dReply1["to_role"] != nil || dReply1["issue"] != nil {
		t.Errorf("reply1 event data = %v", dReply1)
	}
}

func TestCreateMessageRoutingValidationErrors(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	otherIss := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "OTH", Title: "다른 프로젝트 이슈"})
	otherMsg := putMessage(t, s, "OTH", "", "다른 프로젝트 메시지", "")

	cases := []struct {
		name   string
		in     MessageInput
		field  string
		reason string
	}{
		{"invalid to_session actor", MessageInput{Title: "t", ToSession: &model.SessionRef{Actor: "BAD", SessionID: "s1"}}, "to_session", "invalid"},
		{"invalid to_session id", MessageInput{Title: "t", ToSession: &model.SessionRef{Actor: "claude", SessionID: "bad id"}}, "to_session", "invalid"},
		{"unknown to_session actor", MessageInput{Title: "t", ToSession: &model.SessionRef{Actor: "gemini", SessionID: "s1"}}, "to_session", "unknown_actor"},
		{"invalid to_role", MessageInput{Title: "t", ToRole: "boss"}, "to_role", "invalid"},
		{"unknown issue", MessageInput{Title: "t", Issue: "AGB-999"}, "issue", "unknown_issue"},
		{"issue project mismatch", MessageInput{Title: "t", Issue: otherIss.Key}, "issue", "project_mismatch"},
		{"reply_to zero", MessageInput{Title: "t", ReplyTo: idp(0)}, "reply_to", "invalid"},
		{"reply_to negative", MessageInput{Title: "t", ReplyTo: idp(-1)}, "reply_to", "invalid"},
		{"reply_to unknown", MessageInput{Title: "t", ReplyTo: idp(9999)}, "reply_to", "unknown_message"},
		{"reply_to project mismatch", MessageInput{Title: "t", ReplyTo: &otherMsg.ID}, "reply_to", "project_mismatch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.CreateMessage(ctx, trClaude, "AGB", c.in)
			var verr *model.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			found := false
			for _, f := range verr.Fields {
				if f.Field == c.field && f.Reason == c.reason {
					found = true
				}
			}
			if !found {
				t.Errorf("fields = %+v, want field=%s reason=%s", verr.Fields, c.field, c.reason)
			}
		})
	}

	// Combines ValidateMessage errors with routing errors.
	_, err := s.CreateMessage(ctx, trClaude, "AGB", MessageInput{
		To:        "bad label",
		Title:     "",
		ToRole:    "boss",
		Issue:     "AGB-999",
		ReplyTo:   idp(0),
		ToSession: &model.SessionRef{Actor: "gemini", SessionID: "s1"},
	})
	want := []string{"to", "title", "to_session", "to_role", "issue", "reply_to"}
	if got := violationFields(err); !reflect.DeepEqual(got, want) {
		t.Errorf("combined fields = %v, want %v (%v)", got, want, err)
	}
}

func TestMessageProjectNormalizationAndToCaseInsensitive(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "APP")

	m1 := putMessage(t, s, "app", "Reviewer", "첫 번째", "")
	if m1.Project != "APP" || m1.To == nil || *m1.To != "Reviewer" {
		t.Fatalf("m1 = %+v, want project APP and preserved To Reviewer", m1)
	}
	m2 := putMessage(t, s, "  app ", "APP", "둘 번째", "")
	if m2.Project != "APP" || m2.To == nil || *m2.To != "APP" {
		t.Fatalf("m2 = %+v, want project APP and preserved To APP", m2)
	}

	// Event emitted with normalized project key.
	evts, _, err := s.ListEvents(ctx, EventFilter{Project: "APP", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	msgEvents := 0
	for _, ev := range evts {
		if ev.Kind == model.EventMessageCreated && ev.Project != nil && *ev.Project == "APP" {
			msgEvents++
		}
	}
	if msgEvents != 2 {
		t.Errorf("message.created events for APP = %d, want 2 (%+v)", msgEvents, evts)
	}

	// ListMessages normalizes project key and matches `to` case-insensitively.
	items, total, err := s.ListMessages(ctx, MessageFilter{Project: " app ", Limit: 10})
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("list normalized = %+v total=%d err=%v", items, total, err)
	}
	if items, total, err := s.ListMessages(ctx, MessageFilter{Project: "app", To: "reviewer", Limit: 10}); err != nil || total != 1 || *items[0].To != "Reviewer" {
		t.Errorf("to=reviewer: items=%+v total=%d err=%v", items, total, err)
	}
	if items, total, err := s.ListMessages(ctx, MessageFilter{Project: "APP", To: "app", Limit: 10}); err != nil || total != 1 || *items[0].To != "APP" {
		t.Errorf("to=app: items=%+v total=%d err=%v", items, total, err)
	}
	if items, total, err := s.ListMessages(ctx, MessageFilter{Project: "APP", To: "REVIEWER", Limit: 10}); err != nil || total != 1 || items[0].ID != m1.ID {
		t.Errorf("to=REVIEWER: items=%+v total=%d err=%v", items, total, err)
	}
}
