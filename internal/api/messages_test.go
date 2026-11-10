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
