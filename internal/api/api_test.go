package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

const testToken = "test-token-0123456789abcdef0123456789" // dummy

var testActors = []model.Actor{{ID: "owner", Kind: model.ActorUser}, {ID: "claude", Kind: model.ActorAgent}}

type env struct {
	h   http.Handler
	log *bytes.Buffer
	st  *store.Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "agb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.EnsureActors(t.Context(), testActors); err != nil {
		t.Fatal(err)
	}
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	h := New(Options{Token: testToken, Actors: testActors, Version: "test", Logger: logger, Store: st})
	return &env{h: h, log: buf, st: st}
}

// do sends a request with the given headers. Headers with the value "-" are
// omitted, which lets tests remove one of the default good headers.
func (e *env) do(t *testing.T, method, target string, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	good := map[string]string{
		"Authorization": "Bearer " + testToken,
		"X-Actor":       "claude",
		"X-Session-Id":  "sess-1",
		"X-Client-Host": "desk-win10",
	}
	for k, v := range headers {
		good[k] = v
	}
	for k, v := range good {
		if v != "-" {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

type errBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Field   string `json:"field"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"details"`
	} `json:"error"`
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) errBody {
	t.Helper()
	var e errBody
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("error body not JSON: %v: %s", err, rec.Body.String())
	}
	return e
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("body not JSON: %v: %s", err, rec.Body.String())
	}
}

func (e *env) lastLog(t *testing.T) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(e.log.String()), "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("log line not JSON: %v: %s", err, lines[len(lines)-1])
	}
	return m
}

func TestHealthNeedsNoHeaders(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, "GET", "/v1/health", "", map[string]string{"Authorization": "-", "X-Actor": "-", "X-Session-Id": "-", "X-Client-Host": "-"})
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	decodeJSON(t, rec, &body)
	if body["status"] != "ok" || body["version"] != "test" || body["api_version"] != float64(1) {
		t.Errorf("body = %v", body)
	}
	lg := e.lastLog(t)
	if lg["path"] != "/v1/health" || lg["status"] != float64(200) || lg["method"] != "GET" {
		t.Errorf("log = %v", lg)
	}
}

func TestAuthRejected(t *testing.T) {
	e := newEnv(t)
	cases := map[string]string{
		"missing": "-",
		"basic":   "Basic abc",
		"wrong":   "Bearer wrong-token-0123456789abcdef0123456789",
		"prefix":  "Bearer " + testToken + "x",
		"empty":   "Bearer ",
	}
	for name, auth := range cases {
		rec := e.do(t, "GET", "/v1/actors", "", map[string]string{"Authorization": auth})
		if rec.Code != 401 {
			t.Errorf("%s: status = %d", name, rec.Code)
		}
		if b := decodeErr(t, rec); b.Error.Code != "unauthorized" {
			t.Errorf("%s: code = %q", name, b.Error.Code)
		}
		lg := e.lastLog(t)
		if lg["reject"] != "unauthorized" || lg["status"] != float64(401) {
			t.Errorf("%s: log = %v", name, lg)
		}
	}
	if strings.Contains(e.log.String(), testToken) {
		t.Error("request log leaks the token")
	}
}

func TestAuthBeforeTraceHeaders(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, "GET", "/v1/actors", "", map[string]string{"Authorization": "-", "X-Actor": "-"})
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401 (auth checked first)", rec.Code)
	}
}

func TestTraceHeadersRequiredOnEveryRequest(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		name   string
		hdr    map[string]string
		field  string
		reason string
	}{
		{"missing actor", map[string]string{"X-Actor": "-"}, "X-Actor", "missing_header"},
		{"missing session", map[string]string{"X-Session-Id": "-"}, "X-Session-Id", "missing_header"},
		{"missing host", map[string]string{"X-Client-Host": "-"}, "X-Client-Host", "missing_header"},
		{"empty actor", map[string]string{"X-Actor": ""}, "X-Actor", "missing_header"},
		{"uppercase actor", map[string]string{"X-Actor": "Claude"}, "X-Actor", "invalid_header"},
		{"unknown actor", map[string]string{"X-Actor": "gemini"}, "X-Actor", "unknown_actor"},
		{"bad session", map[string]string{"X-Session-Id": "a b"}, "X-Session-Id", "invalid_header"},
		{"long session", map[string]string{"X-Session-Id": strings.Repeat("a", 129)}, "X-Session-Id", "invalid_header"},
		{"bad host", map[string]string{"X-Client-Host": "-host"}, "X-Client-Host", "invalid_header"},
		{"host with space", map[string]string{"X-Client-Host": "my host"}, "X-Client-Host", "invalid_header"},
	}
	for _, c := range cases {
		for _, target := range []string{"/v1/actors", "/v1/actors/me", "/v1/nope"} {
			rec := e.do(t, "GET", target, "", c.hdr)
			if rec.Code != 400 {
				t.Errorf("%s %s: status = %d: %s", c.name, target, rec.Code, rec.Body.String())
				continue
			}
			b := decodeErr(t, rec)
			if b.Error.Code != "bad_request" || len(b.Error.Details) != 1 || b.Error.Details[0].Field != c.field || b.Error.Details[0].Reason != c.reason {
				t.Errorf("%s %s: body = %+v", c.name, target, b.Error)
			}
			lg := e.lastLog(t)
			wantReject := c.reason
			if c.reason != "unknown_actor" {
				wantReject = c.reason + ":" + c.field
			}
			if lg["reject"] != wantReject {
				t.Errorf("%s %s: log reject = %v, want %s", c.name, target, lg["reject"], wantReject)
			}
		}
	}
}

func TestDuplicateTraceHeaderRejected(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest("GET", "/v1/actors", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Add("X-Actor", "claude")
	req.Header.Add("X-Actor", "owner")
	req.Header.Set("X-Session-Id", "s")
	req.Header.Set("X-Client-Host", "h")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d", rec.Code)
	}
	b := decodeErr(t, rec)
	if b.Error.Details[0].Reason != "duplicate_header" || b.Error.Details[0].Field != "X-Actor" {
		t.Errorf("body = %+v", b.Error)
	}
}

func TestRequestLogFields(t *testing.T) {
	e := newEnv(t)
	e.do(t, "GET", "/v1/actors/me?x=1", "", map[string]string{"X-Session-Id": "sess-42", "X-Client-Host": "laptop"})
	lg := e.lastLog(t)
	for k, want := range map[string]any{
		"method": "GET", "path": "/v1/actors/me", "query": "x=1", "status": float64(200),
		"actor": "claude", "session_id": "sess-42", "client_host": "laptop",
	} {
		if lg[k] != want {
			t.Errorf("log[%s] = %v, want %v", k, lg[k], want)
		}
	}
	if _, ok := lg["duration_ms"]; !ok {
		t.Error("duration_ms missing")
	}
	if _, ok := lg["remote"]; !ok {
		t.Error("remote missing")
	}
	if _, ok := lg["reject"]; ok {
		t.Error("reject must be absent on success")
	}
	// Rejected requests still log the header values as received, truncated.
	e.do(t, "GET", "/v1/actors", "", map[string]string{"X-Session-Id": strings.Repeat("z", 300)})
	lg = e.lastLog(t)
	if s, _ := lg["session_id"].(string); len(s) != 128 {
		t.Errorf("session_id logged length = %d, want 128", len(s))
	}
	if lg["actor"] != "claude" {
		t.Errorf("actor should be logged on rejected request: %v", lg["actor"])
	}
}

func TestActors(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, "GET", "/v1/actors", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []model.Actor `json:"items"`
	}
	decodeJSON(t, rec, &list)
	if len(list.Items) != 2 || list.Items[0].ID != "owner" || list.Items[1].Kind != model.ActorAgent {
		t.Errorf("items = %+v", list.Items)
	}
	rec = e.do(t, "GET", "/v1/actors/me", "", map[string]string{"X-Actor": "owner"})
	var me model.Actor
	decodeJSON(t, rec, &me)
	if me.ID != "owner" || me.Kind != model.ActorUser {
		t.Errorf("me = %+v", me)
	}
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	e := newEnv(t)
	rec := e.do(t, "GET", "/v1/nope", "", nil)
	if rec.Code != 404 || decodeErr(t, rec).Error.Code != "not_found" {
		t.Errorf("unknown path: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "POST", "/v1/actors", "", nil)
	if rec.Code != 405 || decodeErr(t, rec).Error.Code != "method_not_allowed" {
		t.Errorf("POST /v1/actors: %d %s", rec.Code, rec.Body.String())
	}
	if allow := rec.Header().Get("Allow"); allow != "GET" {
		t.Errorf("Allow = %q", allow)
	}
	rec = e.do(t, "DELETE", "/v1/health", "", nil)
	if rec.Code != 405 {
		t.Errorf("DELETE /v1/health: %d", rec.Code)
	}
}
