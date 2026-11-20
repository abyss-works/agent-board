package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

// testClock is a clock tests move by hand.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

var sessionActors = []model.Actor{{ID: "owner", Kind: model.ActorUser}, {ID: "claude", Kind: model.ActorAgent}, {ID: "codex", Kind: model.ActorAgent}}

// newSessionEnv builds an environment whose handler and store share a clock
// that tests move by hand.
func newSessionEnv(t *testing.T) (*env, *testClock) {
	t.Helper()
	clock := &testClock{now: time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)}
	e := newEnvWith(t, sessionActors, func(o *Options) { o.Now = clock.Now })
	e.st.SetClock(clock.Now)
	return e, clock
}

func sess(id string) map[string]string { return map[string]string{"X-Session-Id": id} }

func (e *env) session(t *testing.T, actor, id string) *model.Session {
	t.Helper()
	s, err := e.st.GetSession(t.Context(), actor, id)
	if err != nil {
		t.Fatalf("session %s/%s: %v", actor, id, err)
	}
	return s
}

func (e *env) sessionCount(t *testing.T) int {
	t.Helper()
	_, total, err := e.st.ListSessions(t.Context(), store.SessionFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	return total
}

func wantStatus(t *testing.T, rec interface {
	Result() *http.Response
}, want int, what string) {
	t.Helper()
	if got := rec.Result().StatusCode; got != want {
		t.Fatalf("%s: status %d, want %d", what, got, want)
	}
}

func wantDetail(t *testing.T, eb errBody, field, reason string) {
	t.Helper()
	for _, d := range eb.Error.Details {
		if d.Field == field && (reason == "" || d.Reason == reason) {
			return
		}
	}
	t.Errorf("details = %+v, want %s (%s)", eb.Error.Details, field, reason)
}

func TestAuthenticatedRequestRegistersTheSession(t *testing.T) {
	e, clock := newSessionEnv(t)
	start := clock.Now()
	if rec := e.do(t, "GET", "/v1/actors/me", "", sess("claude-abc")); rec.Code != 200 {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}
	got := e.session(t, "claude", "claude-abc")
	if got.FirstSeenAt != model.NewTime(start) || got.LastSeenAt != got.FirstSeenAt || got.UpdatedAt != got.FirstSeenAt || got.LastHost != "desk-win10" {
		t.Errorf("registered = %+v", got)
	}

	// A call within a minute of the last write is not written.
	clock.add(30 * time.Second)
	e.do(t, "GET", "/v1/actors/me", "", map[string]string{"X-Session-Id": "claude-abc", "X-Client-Host": "laptop"})
	if got := e.session(t, "claude", "claude-abc"); got.LastSeenAt != model.NewTime(start) || got.LastHost != "desk-win10" {
		t.Errorf("within a minute: %+v", got)
	}
	clock.add(31 * time.Second)
	e.do(t, "GET", "/v1/actors/me", "", map[string]string{"X-Session-Id": "claude-abc", "X-Client-Host": "laptop"})
	got = e.session(t, "claude", "claude-abc")
	if got.LastSeenAt != model.NewTime(start.Add(61*time.Second)) || got.LastHost != "laptop" || got.UpdatedAt != model.NewTime(start) {
		t.Errorf("after a minute: %+v", got)
	}
	// Any route and status counts once the request is authenticated.
	if rec := e.do(t, "GET", "/v1/nowhere", "", sess("claude-404")); rec.Code != 404 {
		t.Fatalf("unknown path: %d", rec.Code)
	}
	e.session(t, "claude", "claude-404")
}

func TestRejectedAndUnauthenticatedRequestsRegisterNoSession(t *testing.T) {
	e, _ := newSessionEnv(t)
	for _, c := range []struct {
		name    string
		method  string
		path    string
		headers map[string]string
		status  int
	}{
		{"bad token", "GET", "/v1/actors/me", map[string]string{"Authorization": "Bearer nope"}, 401},
		{"actor mismatch", "GET", "/v1/actors/me", map[string]string{"Authorization": "Bearer " + testToken}, 403},
		{"bad host", "GET", "/v1/actors/me", map[string]string{"X-Client-Host": "bad host!"}, 400},
		{"health", "GET", "/v1/health", nil, 200},
		{"shared actor list", "GET", "/v1/actors", map[string]string{"X-Actor": "-", "Authorization": "Bearer " + testToken}, 200},
		{"device authorization", "POST", "/v1/device-authorizations", map[string]string{"Authorization": "-"}, 0},
	} {
		body := ""
		if c.method == "POST" {
			body = `{"actor":"claude","client_host":"desk"}`
		}
		rec := e.do(t, c.method, c.path, body, c.headers)
		if c.status != 0 && rec.Code != c.status {
			t.Errorf("%s: status %d, want %d (%s)", c.name, rec.Code, c.status, rec.Body)
		}
		if n := e.sessionCount(t); n != 0 {
			t.Fatalf("%s: %d sessions", c.name, n)
		}
	}
}

func TestSessionWriteFailureLeavesTheRequestAlone(t *testing.T) {
	e, _ := newSessionEnv(t)
	if _, err := e.st.DB().Exec(`drop table session`); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "GET", "/v1/actors/me", "", nil)
	if rec.Code != 200 {
		t.Fatalf("me with a broken session table: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(e.log.String(), `"level":"WARN"`) {
		t.Errorf("no warning logged: %s", e.log)
	}
}

// sessionJSON decodes a session response.
func sessionJSON(t *testing.T, rec interface{ Result() *http.Response }, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("session body: %v: %s", err, body)
	}
	return m
}

func TestGetSession(t *testing.T) {
	e, _ := newSessionEnv(t)
	e.do(t, "GET", "/v1/actors/me", "", sess("cli-20261120"))
	rec := e.do(t, "GET", "/v1/sessions/claude/cli-20261120", "", sess("cli-20261120"))
	wantStatus(t, rec, 200, "get")
	m := sessionJSON(t, rec, rec.Body.Bytes())
	for _, k := range []string{"actor", "session_id", "named", "first_seen_at", "last_seen_at", "last_host", "last_project", "parent", "role", "issue", "location", "stale", "updated_at", "children"} {
		if _, ok := m[k]; !ok {
			t.Errorf("field %s missing: %s", k, rec.Body)
		}
	}
	if m["named"] != false || m["stale"] != false || m["last_project"] != nil {
		t.Errorf("get = %s", rec.Body)
	}
	wantStatus(t, e.do(t, "GET", "/v1/sessions/claude/none", "", nil), 404, "missing")
	wantStatus(t, e.do(t, "GET", "/v1/sessions/claude/bad%20id", "", nil), 404, "malformed")
	wantStatus(t, e.do(t, "DELETE", "/v1/sessions/claude/cli-20261120", "", nil), 405, "delete")
	wantStatus(t, e.do(t, "PUT", "/v1/sessions", "", nil), 405, "put list")
}

func TestPatchSession(t *testing.T) {
	e, clock := newSessionEnv(t)
	created := clock.Now()
	e.do(t, "POST", "/v1/projects", `{"key":"APP","name":"app"}`, map[string]string{"X-Actor": "owner"})
	if rec := e.do(t, "POST", "/v1/issues", `{"project":"APP","title":"t","body":"b"}`, map[string]string{"X-Actor": "owner"}); rec.Code != 201 {
		t.Fatalf("issue: %d %s", rec.Code, rec.Body)
	}
	path := "/v1/sessions/claude/sess-1"
	e.do(t, "GET", "/v1/actors/me", "", nil) // registers claude/sess-1
	clock.add(time.Minute)

	rec := e.do(t, "PATCH", path, `{"role":"runner"}`, map[string]string{"X-Actor": "codex"})
	wantStatus(t, rec, 403, "another agent")
	if eb := decodeErr(t, rec); eb.Error.Code != "forbidden" {
		t.Errorf("code = %s", eb.Error.Code)
	}
	wantStatus(t, e.do(t, "PATCH", path, `{"role":"runner"}`, map[string]string{"X-Actor": "owner"}), 200, "user")
	wantStatus(t, e.do(t, "PATCH", "/v1/sessions/claude/none", `{}`, nil), 404, "missing")

	for _, c := range []struct {
		body, field, reason string
	}{
		{`{"parent":{"actor":"claude","session_id":"nope"}}`, "parent", "unknown_session"},
		{`{"parent":{"actor":"claude","session_id":"sess-1"}}`, "parent", "cycle"},
		{`{"role":"boss"}`, "role", ""},
		{`{"issue":"APP-999"}`, "issue", "unknown_issue"},
		{`{"location":{"foo":"x"}}`, "location.foo", ""},
		{`{"location":{"dirty_count":-1}}`, "location.dirty_count", ""},
		{`{"location":{"branch":"` + strings.Repeat("b", 256) + `"}}`, "location.branch", ""},
		{`{"location":{"head":7}}`, "location.head", ""},
	} {
		rec := e.do(t, "PATCH", path, c.body, nil)
		wantStatus(t, rec, 422, c.body)
		wantDetail(t, decodeErr(t, rec), c.field, c.reason)
	}
	rec = e.do(t, "PATCH", path, `{"last_seen_at":"2026-11-20T00:00:00.000Z"}`, nil)
	wantStatus(t, rec, 400, "unknown field")
	wantDetail(t, decodeErr(t, rec), "last_seen_at", "unknown_field")

	before := e.session(t, "claude", "sess-1")
	clock.add(time.Minute)
	rec = e.do(t, "PATCH", path, `{}`, nil)
	wantStatus(t, rec, 200, "empty")
	if got := e.session(t, "claude", "sess-1"); got.UpdatedAt != before.UpdatedAt {
		t.Errorf("empty patch moved updated_at: %s -> %s", before.UpdatedAt, got.UpdatedAt)
	}

	rec = e.do(t, "PATCH", path, `{"issue":"APP-1","location":{"repo_remote":"https://user:tok@github.com/a/b.git","dirty_count":2}}`, nil)
	wantStatus(t, rec, 200, "issue and location")
	var got model.Session
	decodeJSON(t, rec, &got)
	if got.Issue == nil || *got.Issue != "APP-1" || got.Location == nil || *got.Location.RepoRemote != "https://github.com/a/b.git" ||
		got.Location.ReportedAt != model.NewTime(clock.Now()) || got.Location.Branch != nil || got.Role == nil || *got.Role != "runner" {
		t.Errorf("patched = %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"branch":null`) {
		t.Errorf("location keys not reported are not null: %s", rec.Body)
	}
	rec = e.do(t, "PATCH", path, `{"issue":null}`, nil)
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.Issue != nil {
		t.Errorf("issue null: %d %s", rec.Code, rec.Body)
	}
	if got.FirstSeenAt != model.NewTime(created) {
		t.Errorf("first_seen_at = %s", got.FirstSeenAt)
	}
}

var childIDRE = regexp.MustCompile(`^sess-1\.[a-z0-9]{6}$`)

func TestStartChildSession(t *testing.T) {
	e, clock := newSessionEnv(t)
	rec := e.do(t, "POST", "/v1/sessions", `{}`, nil)
	wantStatus(t, rec, 201, "start")
	var child model.Session
	decodeJSON(t, rec, &child)
	if !childIDRE.MatchString(child.SessionID) || child.Actor != "claude" || child.Role == nil || *child.Role != "subagent" ||
		child.Parent == nil || *child.Parent != (model.SessionRef{Actor: "claude", SessionID: "sess-1"}) || child.LastHost != "desk-win10" {
		t.Errorf("child = %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), `"children"`) {
		t.Errorf("a created child carries children: %s", rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/v1/sessions/claude/"+child.SessionID {
		t.Errorf("Location = %q", loc)
	}
	if p := e.session(t, "claude", "sess-1"); p.Role == nil || *p.Role != "main" {
		t.Errorf("parent role = %v", p.Role)
	}

	clock.add(time.Second)
	rec = e.do(t, "POST", "/v1/sessions", `{"session_id":"sess-1.named","role":"runner"}`, nil)
	wantStatus(t, rec, 201, "named child")
	rec = e.do(t, "POST", "/v1/sessions", `{"session_id":"sess-1.named"}`, nil)
	wantStatus(t, rec, 409, "same id")
	wantDetail(t, decodeErr(t, rec), "", "exists")
	rec = e.do(t, "POST", "/v1/sessions", `{"session_id":"bad id"}`, nil)
	wantStatus(t, rec, 422, "bad id")
	wantDetail(t, decodeErr(t, rec), "session_id", "")
	clock.add(time.Second)
	rec = e.do(t, "POST", "/v1/sessions", `{"session_id":""}`, nil)
	wantStatus(t, rec, 201, "empty id")

	long := "s" + strings.Repeat("x", 124)
	rec = e.do(t, "POST", "/v1/sessions", `{}`, sess(long))
	wantStatus(t, rec, 422, "too long")
	wantDetail(t, decodeErr(t, rec), "session_id", "too_long")
	wantStatus(t, e.do(t, "POST", "/v1/sessions", `{"parent":"x"}`, nil), 400, "unknown field")

	rec = e.do(t, "GET", "/v1/sessions/claude/sess-1", "", nil)
	var parent model.Session
	decodeJSON(t, rec, &parent)
	if parent.Children == nil || len(*parent.Children) != 3 || (*parent.Children)[0].SessionID != child.SessionID || (*parent.Children)[1].SessionID != "sess-1.named" {
		t.Errorf("children = %s", rec.Body)
	}
}

func TestListSessionsAPI(t *testing.T) {
	e, clock := newSessionEnv(t)
	e.do(t, "POST", "/v1/projects", `{"key":"APP","name":"app"}`, map[string]string{"X-Actor": "owner", "X-Session-Id": "cli-20261120"})
	clock.add(time.Hour)
	e.do(t, "GET", "/v1/actors/me", "", sess("b-sess"))
	clock.add(time.Minute)
	e.do(t, "GET", "/v1/actors/me", "", sess("a-sess"))
	e.do(t, "POST", "/v1/sessions", `{"session_id":"a-sess.kid"}`, sess("a-sess"))

	ids := func(query string, want ...string) {
		t.Helper()
		rec := e.do(t, "GET", "/v1/sessions"+query, "", sess("a-sess"))
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", query, rec.Code, rec.Body)
		}
		var l struct {
			Items []model.Session `json:"items"`
			Total int             `json:"total"`
			Limit int             `json:"limit"`
		}
		decodeJSON(t, rec, &l)
		got := make([]string, len(l.Items))
		for n, it := range l.Items {
			got[n] = it.Actor + "/" + it.SessionID
		}
		if strings.Join(got, " ") != strings.Join(want, " ") || (!strings.Contains(query, "limit") && l.Total != len(want)) {
			t.Errorf("%s = %v (total %d), want %v", query, got, l.Total, want)
		}
	}
	ids("", "claude/a-sess", "claude/a-sess.kid", "claude/b-sess", "owner/cli-20261120")
	ids("?active=true", "claude/a-sess", "claude/a-sess.kid", "claude/b-sess")
	ids("?active=false", "owner/cli-20261120")
	ids("?parent=claude/a-sess", "claude/a-sess.kid")
	ids("?named=false", "owner/cli-20261120")
	ids("?project=APP", "owner/cli-20261120")
	ids("?actor=owner&named=false", "owner/cli-20261120")
	ids("?limit=1&offset=1", "claude/a-sess.kid")
	if rec := e.do(t, "GET", "/v1/sessions?limit=1", "", sess("a-sess")); !strings.Contains(rec.Body.String(), `"limit":1,"offset":0,"total":4`) {
		t.Errorf("page envelope = %s", rec.Body)
	}

	for _, q := range []string{"?active=yes", "?named=1", "?parent=claude", "?parent=claude/bad%20id", "?actor=BAD", "?project=bad", "?limit=201", "?foo=1"} {
		wantStatus(t, e.do(t, "GET", "/v1/sessions"+q, "", nil), 400, q)
	}
	wantStatus(t, e.do(t, "GET", "/v1/sessions?project=NOPE", "", nil), 404, "unknown project")
}
