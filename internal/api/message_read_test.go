package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func TestAckMessageAndUnreadList(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	a := e.putMessage(t, "AGB", `{"to":"desk","title":"a"}`)
	e.putMessage(t, "AGB", `{"to":"codex","title":"b"}`)
	ack := fmt.Sprintf("/v1/messages/%d/ack", a.ID)

	rec := e.do(t, "POST", ack, `{"reader":"alpha"}`, map[string]string{"X-Actor": "owner"})
	var first model.MessageRead
	decodeJSON(t, rec, &first)
	if rec.Code != 200 || first.MessageID != a.ID || first.Reader != "alpha" || first.ReadBy != "owner" || first.ReadSession != "sess-1" || first.ReadHost != "desk-win10" || first.ReadAt.Time().IsZero() {
		t.Fatalf("ack: %d %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`"message_id":`, `"reader":"alpha"`, `"read_at":"`, `"read_by":"owner"`, `"read_session":"sess-1"`, `"read_host":"desk-win10"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("JSON shape lacks %s: %s", want, rec.Body.String())
		}
	}

	// Acknowledging again answers with the first record.
	rec = e.do(t, "POST", ack, `{"reader":"alpha"}`, nil)
	var again model.MessageRead
	decodeJSON(t, rec, &again)
	if rec.Code != 200 || again != first {
		t.Errorf("second ack: %d %s", rec.Code, rec.Body.String())
	}

	if l := e.listMessages(t, "AGB", "?unread_by=alpha"); messageTitles(l.Items) != "b" || l.Total != 1 {
		t.Errorf("unread by alpha = %+v", l)
	}
	if l := e.listMessages(t, "AGB", "?unread_by=beta"); messageTitles(l.Items) != "b,a" || l.Total != 2 {
		t.Errorf("unread by beta = %+v", l)
	}
	if l := e.listMessages(t, "AGB", ""); messageTitles(l.Items) != "b,a" || l.Total != 2 {
		t.Errorf("default list = %+v", l)
	}
	if l := e.listMessages(t, "AGB", "?unread_by=beta&to=desk&limit=1"); messageTitles(l.Items) != "a" || l.Total != 1 {
		t.Errorf("unread by beta to desk = %+v", l)
	}
	if l := e.listMessages(t, "AGB", "?unread_by=alpha&all=true"); messageTitles(l.Items) != "b" {
		t.Errorf("unread by alpha with all = %+v", l)
	}
	if body := e.do(t, "GET", "/v1/projects/AGB/messages", "", nil).Body.String(); strings.Contains(body, `"reads"`) {
		t.Errorf("a list must leave the reads out: %s", body)
	}

	// The single message carries its reads; the list does not change them.
	rec = e.do(t, "GET", fmt.Sprintf("/v1/messages/%d", a.ID), "", nil)
	var got model.Message
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || len(got.Reads) != 1 || got.Reads[0] != first {
		t.Errorf("get: %d %s", rec.Code, rec.Body.String())
	}
	fresh := e.putMessage(t, "AGB", `{"title":"c"}`)
	if rec = e.do(t, "GET", fmt.Sprintf("/v1/messages/%d", fresh.ID), "", nil); !strings.Contains(rec.Body.String(), `"reads":[]`) {
		t.Errorf("a message without reads: %s", rec.Body.String())
	}

	// A deleted message still takes a read.
	if rec = e.do(t, "DELETE", fmt.Sprintf("/v1/messages/%d", a.ID), "", nil); rec.Code != 200 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec = e.do(t, "POST", ack, `{"reader":"beta"}`, nil); rec.Code != 200 {
		t.Errorf("ack of a deleted message: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAckMessageErrors(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	m := e.putMessage(t, "AGB", `{"title":"t"}`)
	ack := fmt.Sprintf("/v1/messages/%d/ack", m.ID)
	cases := []struct {
		name, body string
		code       int
		want       string
	}{
		{"missing reader", `{}`, 422, `"field":"reader"`},
		{"null reader", `{"reader":null}`, 422, `"field":"reader"`},
		{"empty reader", `{"reader":""}`, 422, `"field":"reader"`},
		{"reader with a space", `{"reader":"a b"}`, 422, `"field":"reader"`},
		{"long reader", `{"reader":"` + strings.Repeat("a", model.MaxMessageToRunes+1) + `"}`, 422, `"field":"reader"`},
		{"unknown field", `{"reader":"alpha","at":"2030-01-01T00:00:00.000Z"}`, 400, "unknown_field"},
		{"reader is not text", `{"reader":3}`, 400, "type_mismatch"},
		{"no body", ``, 400, ""},
	}
	for _, c := range cases {
		rec := e.do(t, "POST", ack, c.body, nil)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body.String())
		}
	}
	// A message that is not there answers like GET /v1/messages/{id}.
	for _, id := range []string{"9999", "0", "abc"} {
		rec := e.do(t, "POST", "/v1/messages/"+id+"/ack", `{"reader":"alpha"}`, nil)
		get := e.do(t, "GET", "/v1/messages/"+id, "", nil)
		if rec.Code != 404 || rec.Body.String() != get.Body.String() {
			t.Errorf("POST /v1/messages/%s/ack: %d %s; get: %s", id, rec.Code, rec.Body.String(), get.Body.String())
		}
	}
	if rec := e.do(t, "POST", ack+"?bogus=1", `{"reader":"alpha"}`, nil); rec.Code != 400 {
		t.Errorf("unknown query: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "DELETE", ack, "", nil); rec.Code != 405 {
		t.Errorf("DELETE: %d", rec.Code)
	}
	if rec := e.do(t, "POST", ack, `{"reader":"alpha"}`, map[string]string{"Authorization": "-", "X-Actor": "-"}); rec.Code != 401 {
		t.Errorf("without auth: %d", rec.Code)
	}
	for _, q := range []string{"?unread_by=a%20b", "?unread_by=" + strings.Repeat("a", model.MaxMessageToRunes+1), "?unread_by=a&unread_by=b"} {
		if rec := e.do(t, "GET", "/v1/projects/AGB/messages"+q, "", nil); rec.Code != 400 || !strings.Contains(rec.Body.String(), `"field":"unread_by"`) {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body.String())
		}
	}
	// Nothing above left a read.
	if rec := e.do(t, "GET", fmt.Sprintf("/v1/messages/%d", m.ID), "", nil); !strings.Contains(rec.Body.String(), `"reads":[]`) {
		t.Errorf("reads after the rejected requests: %s", rec.Body.String())
	}

	if rec := e.do(t, "PATCH", "/v1/projects/AGB", `{"status":"archived"}`, nil); rec.Code != 200 {
		t.Fatalf("archive: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "POST", ack, `{"reader":"alpha"}`, nil); rec.Code != 409 || !strings.Contains(rec.Body.String(), "project_not_active") {
		t.Errorf("ack in an archived project: %d %s", rec.Code, rec.Body)
	}
}
