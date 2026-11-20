package api

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func (e *env) putMessage(t *testing.T, project, body string) model.Message {
	t.Helper()
	rec := e.do(t, "POST", "/v1/projects/"+project+"/messages", body, nil)
	if rec.Code != 201 {
		t.Fatalf("put message %s: %d %s", body, rec.Code, rec.Body.String())
	}
	var m model.Message
	decodeJSON(t, rec, &m)
	return m
}

type messageList struct {
	Items  []model.Message `json:"items"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

func (e *env) listMessages(t *testing.T, project, query string) messageList {
	t.Helper()
	rec := e.do(t, "GET", "/v1/projects/"+project+"/messages"+query, "", nil)
	if rec.Code != 200 {
		t.Fatalf("list messages %s: %d %s", query, rec.Code, rec.Body.String())
	}
	var l messageList
	decodeJSON(t, rec, &l)
	return l
}

func messageTitles(items []model.Message) string {
	out := make([]string, len(items))
	for i, m := range items {
		out[i] = m.Title
	}
	return strings.Join(out, ",")
}

func TestCreateGetAndDeleteMessage(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")

	rec := e.do(t, "POST", "/v1/projects/AGB/messages", `{"to":"reviewer","title":"배포 끝","body":"본문","ttl":"3d"}`, nil)
	var m model.Message
	decodeJSON(t, rec, &m)
	path := fmt.Sprintf("/v1/messages/%d", m.ID)
	if rec.Code != 201 || rec.Header().Get("Location") != path {
		t.Fatalf("create: %d %s %q", rec.Code, rec.Body.String(), rec.Header().Get("Location"))
	}
	if m.Project != "AGB" || m.To == nil || *m.To != "reviewer" || m.Title != "배포 끝" || m.Body == nil || *m.Body != "본문" || m.State != "active" ||
		m.CreatedBy != "claude" || m.CreatedSession != "sess-1" || m.CreatedHost != "desk-win10" || m.DeletedAt != nil {
		t.Errorf("message = %+v", m)
	}
	if d := m.ExpiresAt.Time().Sub(m.CreatedAt.Time()).Hours(); d != 72 {
		t.Errorf("ttl 3d gave %v hours", d)
	}
	for _, want := range []string{`"to":"reviewer"`, `"deleted_at":null`, `"state":"active"`, `"age":"0m"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("JSON shape lacks %s: %s", want, rec.Body.String())
		}
	}

	// Only the title is required.
	rec = e.do(t, "POST", "/v1/projects/AGB/messages", `{"title":"제목만","to":null}`, nil)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"to":null`) || !strings.Contains(rec.Body.String(), `"body":""`) {
		t.Errorf("minimal create: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, "GET", path, "", nil)
	var got model.Message
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.ID != m.ID || got.Body == nil || *got.Body != "본문" {
		t.Errorf("get: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, "DELETE", path, "", map[string]string{"X-Actor": "owner"})
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.State != "deleted" || got.DeletedBy == nil || *got.DeletedBy != "owner" {
		t.Errorf("delete: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "GET", path, "", nil)
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.State != "deleted" || got.Body == nil || *got.Body != "본문" {
		t.Errorf("get after delete: %d %s", rec.Code, rec.Body.String())
	}
}

func TestListMessages(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")
	a := e.putMessage(t, "AGB", `{"to":"desk","title":"a"}`)
	e.putMessage(t, "AGB", `{"to":"codex","title":"b"}`)
	e.putMessage(t, "OTH", `{"title":"other"}`)
	if rec := e.do(t, "DELETE", fmt.Sprintf("/v1/messages/%d", a.ID), "", nil); rec.Code != 200 {
		t.Fatalf("delete: %d", rec.Code)
	}

	l := e.listMessages(t, "AGB", "")
	if messageTitles(l.Items) != "b" || l.Total != 1 || l.Limit != 50 || l.Offset != 0 || l.Items[0].Body != nil {
		t.Errorf("default list = %+v", l)
	}
	if l = e.listMessages(t, "AGB", "?all=true"); messageTitles(l.Items) != "b,a" || l.Total != 2 || l.Items[1].State != "deleted" {
		t.Errorf("all list = %+v", l)
	}
	if l = e.listMessages(t, "AGB", "?to=codex"); messageTitles(l.Items) != "b" {
		t.Errorf("to filter = %+v", l)
	}
	if l = e.listMessages(t, "AGB", "?to=desk"); len(l.Items) != 0 || l.Total != 0 {
		t.Errorf("to=desk = %+v", l)
	}
	if l = e.listMessages(t, "AGB", "?all=true&limit=1&offset=1"); messageTitles(l.Items) != "a" || l.Total != 2 || l.Limit != 1 || l.Offset != 1 {
		t.Errorf("paging = %+v", l)
	}
	if !strings.Contains(e.do(t, "GET", "/v1/projects/AGB/messages", "", nil).Body.String(), `"items":[`) {
		t.Error("items must be an array")
	}
	if l = e.listMessages(t, "AGB", "?all=false"); l.Total != 1 {
		t.Errorf("all=false = %+v", l)
	}
}

func TestMessageErrors(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	cases := []struct {
		name, body string
		code       int
		field      string
	}{
		{"missing title", `{"to":"desk"}`, 422, "title"},
		{"empty title", `{"title":""}`, 422, "title"},
		{"newline in title", `{"title":"첫줄\n둘째줄"}`, 422, "title"},
		{"carriage return in title", `{"title":"첫줄\r둘째줄"}`, 422, "title"},
		{"tab in title", `{"title":"첫줄\t둘째줄"}`, 422, "title"},
		{"ascii control in title", `{"title":"제어\u0000문자"}`, 422, "title"},
		{"del in title", `{"title":"삭제\u007f문자"}`, 422, "title"},
		{"bad ttl", `{"title":"t","ttl":"soon"}`, 422, "ttl"},
		{"ttl over max", `{"title":"t","ttl":"91d"}`, 422, "ttl"},
		{"bad to", `{"title":"t","to":"a b"}`, 422, "to"},
		{"large body", `{"title":"t","body":"` + strings.Repeat("a", model.MaxBodyBytes+1) + `"}`, 422, "body"},
		{"unknown field", `{"title":"t","expires_at":"2030-01-01T00:00:00.000Z"}`, 400, ""},
		{"title is not text", `{"title":3}`, 400, ""},
	}
	for _, c := range cases {
		rec := e.do(t, "POST", "/v1/projects/AGB/messages", c.body, nil)
		if rec.Code != c.code || (c.field != "" && !strings.Contains(rec.Body.String(), `"field":"`+c.field+`"`)) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body.String())
		}
	}
	if rec := e.do(t, "POST", "/v1/projects/NOPE/messages", `{"title":"t"}`, nil); rec.Code != 404 {
		t.Errorf("unknown project: %d", rec.Code)
	}
	if rec := e.do(t, "GET", "/v1/projects/NOPE/messages", "", nil); rec.Code != 404 {
		t.Errorf("list unknown project: %d", rec.Code)
	}
	for _, id := range []string{"9999", "0", "abc"} {
		for _, method := range []string{"GET", "DELETE"} {
			if rec := e.do(t, method, "/v1/messages/"+id, "", nil); rec.Code != 404 {
				t.Errorf("%s /v1/messages/%s: %d", method, id, rec.Code)
			}
		}
	}
	if rec := e.do(t, "GET", "/v1/projects/AGB/messages?bogus=1", "", nil); rec.Code != 400 {
		t.Errorf("unknown query: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "GET", "/v1/projects/AGB/messages?all=maybe", "", nil); rec.Code != 400 {
		t.Errorf("bad all: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "PATCH", "/v1/messages/1", `{}`, nil); rec.Code != 405 {
		t.Errorf("PATCH: %d", rec.Code)
	}
	if rec := e.do(t, "POST", "/v1/projects/AGB/messages", `{"title":"t"}`, map[string]string{"Authorization": "-", "X-Actor": "-"}); rec.Code != 401 {
		t.Errorf("without auth: %d", rec.Code)
	}
}

func TestMessageWritesNeedActiveProject(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	m := e.putMessage(t, "AGB", `{"title":"t"}`)
	if rec := e.do(t, "PATCH", "/v1/projects/AGB", `{"status":"archived"}`, nil); rec.Code != 200 {
		t.Fatalf("archive: %d %s", rec.Code, rec.Body)
	}
	for _, rec := range []*httptest.ResponseRecorder{
		e.do(t, "POST", "/v1/projects/AGB/messages", `{"title":"t"}`, nil),
		e.do(t, "DELETE", fmt.Sprintf("/v1/messages/%d", m.ID), "", nil),
	} {
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), "project_not_active") || !strings.Contains(rec.Body.String(), "메시지") {
			t.Errorf("%d %s", rec.Code, rec.Body)
		}
	}
}

func TestMessageRoutingCreateListGetAndSessionIsolation(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")
	if rec := e.do(t, "POST", "/v1/issues", `{"project":"AGB","title":"이슈 1"}`, map[string]string{"X-Actor": "owner"}); rec.Code != 201 {
		t.Fatalf("create issue AGB-1: %d %s", rec.Code, rec.Body.String())
	}

	// Create message routed to claude/sess-a with role and issue.
	rec := e.do(t, "POST", "/v1/projects/AGB/messages",
		`{"to":"reviewer","to_session":{"actor":"claude","session_id":"sess-a"},"to_role":"subagent","issue":"AGB-1","title":"세션 A 메시지","body":"본문 A"}`,
		nil)
	if rec.Code != 201 {
		t.Fatalf("create m1: %d %s", rec.Code, rec.Body.String())
	}
	var m1 model.Message
	decodeJSON(t, rec, &m1)
	if m1.ToSession == nil || *m1.ToSession != (model.SessionRef{Actor: "claude", SessionID: "sess-a"}) ||
		m1.ToRole == nil || *m1.ToRole != "subagent" ||
		m1.Issue == nil || *m1.Issue != "AGB-1" ||
		m1.ReplyTo != nil || m1.ReplyCount != 0 ||
		m1.Replies == nil || len(*m1.Replies) != 0 {
		t.Fatalf("m1 = %+v (%s)", m1, rec.Body.String())
	}
	for _, want := range []string{`"to_session":{"actor":"claude","session_id":"sess-a"}`, `"to_role":"subagent"`, `"issue":"AGB-1"`, `"reply_to":null`, `"reply_count":0`, `"replies":[]`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("m1 JSON lacks %s: %s", want, rec.Body.String())
		}
	}

	// Broadcast message and message for owner/sess-b.
	m2 := e.putMessage(t, "AGB", `{"to_role":"runner","title":"공지"}`)
	m3 := e.putMessage(t, "AGB", `{"to_session":{"actor":"owner","session_id":"sess-b"},"title":"세션 B 메시지"}`)
	r1 := e.putMessage(t, "AGB", fmt.Sprintf(`{"reply_to":%d,"title":"답장 1","body":"첫 답장"}`, m1.ID))
	r2 := e.putMessage(t, "AGB", fmt.Sprintf(`{"reply_to":%d,"title":"답장 2","body":"둘째 답장"}`, m1.ID))

	// GetMessage returns replies ascending and matching reply_count, with no nested replies field in child items.
	rec = e.do(t, "GET", fmt.Sprintf("/v1/messages/%d", m1.ID), "", nil)
	if rec.Code != 200 {
		t.Fatalf("get m1: %d %s", rec.Code, rec.Body.String())
	}
	var gotM1 model.Message
	decodeJSON(t, rec, &gotM1)
	if gotM1.ReplyCount != 2 || gotM1.Replies == nil || len(*gotM1.Replies) != 2 {
		t.Fatalf("gotM1 replies = %+v (count=%d)", gotM1.Replies, gotM1.ReplyCount)
	}
	rs := *gotM1.Replies
	if rs[0].ID != r1.ID || rs[1].ID != r2.ID || rs[0].Body == nil || *rs[0].Body != "첫 답장" || rs[0].Replies != nil || rs[1].Replies != nil {
		t.Errorf("replies = %+v", rs)
	}

	// Session isolation checks.
	listAs := func(actor, sessID, query string) messageList {
		t.Helper()
		rec := e.do(t, "GET", "/v1/projects/AGB/messages"+query, "", map[string]string{
			"X-Actor":      actor,
			"X-Session-Id": sessID,
		})
		if rec.Code != 200 {
			t.Fatalf("list (%s/%s %s): %d %s", actor, sessID, query, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), `"replies"`) {
			t.Errorf("list must omit replies: %s", rec.Body.String())
		}
		var l messageList
		decodeJSON(t, rec, &l)
		return l
	}

	if l := listAs("claude", "sess-a", "?unread_by=AGB"); messageTitles(l.Items) != "답장 2,답장 1,공지,세션 A 메시지" {
		t.Errorf("claude/sess-a unread = %s", messageTitles(l.Items))
	}
	if l := listAs("owner", "sess-b", "?unread_by=AGB"); messageTitles(l.Items) != "답장 2,답장 1,세션 B 메시지,공지" {
		t.Errorf("owner/sess-b unread = %s", messageTitles(l.Items))
	}
	if l := listAs("owner", "sess-c", ""); messageTitles(l.Items) != "답장 2,답장 1,공지" {
		t.Errorf("owner/sess-c default = %s", messageTitles(l.Items))
	}
	if l := listAs("owner", "sess-c", "?all=true"); messageTitles(l.Items) != "답장 2,답장 1,세션 B 메시지,공지,세션 A 메시지" {
		t.Errorf("owner/sess-c all = %s", messageTitles(l.Items))
	}

	// Explicit query filters.
	if l := listAs("owner", "sess-c", "?to_session=claude/sess-a"); messageTitles(l.Items) != "세션 A 메시지" || l.Items[0].ReplyCount != 2 {
		t.Errorf("to_session filter = %+v", l.Items)
	}
	if l := listAs("owner", "sess-c", "?to_role=runner"); messageTitles(l.Items) != "공지" || l.Items[0].ID != m2.ID {
		t.Errorf("to_role filter = %+v", l.Items)
	}
	if l := listAs("claude", "sess-a", "?issue=AGB-1"); messageTitles(l.Items) != "세션 A 메시지" {
		t.Errorf("issue filter = %s", messageTitles(l.Items))
	}
	if l := listAs("owner", "sess-c", fmt.Sprintf("?reply_to=%d", m1.ID)); messageTitles(l.Items) != "답장 2,답장 1" {
		t.Errorf("reply_to filter = %s", messageTitles(l.Items))
	}
	_ = m3
}

func TestMessageRoutingErrors(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")
	if rec := e.do(t, "POST", "/v1/issues", `{"project":"OTH","title":"타 프로젝트"}`, map[string]string{"X-Actor": "owner"}); rec.Code != 201 {
		t.Fatalf("create OTH-1: %d %s", rec.Code, rec.Body.String())
	}
	otherMsg := e.putMessage(t, "OTH", `{"title":"other"}`)

	postCases := []struct {
		name, body, field, reason string
	}{
		{"to_session string", `{"title":"t","to_session":"claude/s1"}`, "to_session", "invalid"},
		{"to_session bad actor", `{"title":"t","to_session":{"actor":"BAD","session_id":"s1"}}`, "to_session", "invalid"},
		{"to_session bad id", `{"title":"t","to_session":{"actor":"claude","session_id":"bad id"}}`, "to_session", "invalid"},
		{"to_session missing key", `{"title":"t","to_session":{"actor":"claude"}}`, "to_session", "invalid"},
		{"to_session unknown actor", `{"title":"t","to_session":{"actor":"gemini","session_id":"s1"}}`, "to_session", "unknown_actor"},
		{"to_role invalid", `{"title":"t","to_role":"boss"}`, "to_role", "invalid"},
		{"issue unknown", `{"title":"t","issue":"AGB-999"}`, "issue", "unknown_issue"},
		{"issue mismatch", `{"title":"t","issue":"OTH-1"}`, "issue", "project_mismatch"},
		{"reply_to zero", `{"title":"t","reply_to":0}`, "reply_to", "invalid"},
		{"reply_to negative", `{"title":"t","reply_to":-1}`, "reply_to", "invalid"},
		{"reply_to string", `{"title":"t","reply_to":"1"}`, "reply_to", "invalid"},
		{"reply_to float", `{"title":"t","reply_to":1.5}`, "reply_to", "invalid"},
		{"reply_to unknown", `{"title":"t","reply_to":9999}`, "reply_to", "unknown_message"},
		{"reply_to mismatch", fmt.Sprintf(`{"title":"t","reply_to":%d}`, otherMsg.ID), "reply_to", "project_mismatch"},
	}
	for _, c := range postCases {
		rec := e.do(t, "POST", "/v1/projects/AGB/messages", c.body, nil)
		eb := decodeErr(t, rec)
		if rec.Code != 422 || len(eb.Error.Details) == 0 || eb.Error.Details[0].Field != c.field || eb.Error.Details[0].Reason != c.reason {
			t.Errorf("%s: code=%d body=%s", c.name, rec.Code, rec.Body.String())
		}
	}

	queryCases := []struct {
		query, field string
	}{
		{"?to_session=claude", "to_session"},
		{"?to_session=BAD/s1", "to_session"},
		{"?to_role=boss", "to_role"},
		{"?issue=bad-key", "issue"},
		{"?reply_to=0", "reply_to"},
		{"?reply_to=abc", "reply_to"},
	}
	for _, c := range queryCases {
		rec := e.do(t, "GET", "/v1/projects/AGB/messages"+c.query, "", nil)
		eb := decodeErr(t, rec)
		if rec.Code != 400 || len(eb.Error.Details) == 0 || eb.Error.Details[0].Field != c.field {
			t.Errorf("%s: code=%d body=%s", c.query, rec.Code, rec.Body.String())
		}
	}
}
