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
		{"token exchange", "POST", "/v1/tokens", map[string]string{"Authorization": "-"}, 0},
	} {
		body := ""
		if c.path == "/v1/device-authorizations" {
			body = `{"actor":"claude","client_host":"desk"}`
		} else if c.path == "/v1/tokens" {
			body = `{"device_code":"missing"}`
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

func TestConcurrentFirstRequestsDoNotRaceSessionRegistration(t *testing.T) {
	e, _ := newSessionEnv(t)
	tok := e.bearer(t, "claude")
	tx, err := e.st.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	codes := make([]int, 2)
	var wg sync.WaitGroup
	req := func(idx int) {
		defer wg.Done()
		rec := e.do(t, "GET", "/v1/sessions/claude/race-sess", "", map[string]string{
			"Authorization": "Bearer " + tok,
			"X-Session-Id":  "race-sess",
		})
		codes[idx] = rec.Code
	}
	wg.Add(1)
	go req(0)
	time.Sleep(50 * time.Millisecond)
	wg.Add(1)
	go req(1)
	time.Sleep(50 * time.Millisecond)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	for i, code := range codes {
		if code != 200 {
			t.Errorf("concurrent request %d: status %d, want 200", i, code)
		}
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
	for _, k := range []string{"actor", "session_id", "named", "first_seen_at", "last_seen_at", "last_host", "last_project", "parent", "role", "issue", "location", "state", "state_reason", "state_at", "stale", "updated_at", "children"} {
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
	if strings.Contains(rec.Body.String(), `"children"`) {
		t.Errorf("a patched session carries children: %s", rec.Body)
	}
	rec = e.do(t, "POST", "/v1/sessions", `{"session_id":"sess-1.child"}`, nil)
	wantStatus(t, rec, 201, "child for cycle")
	rec = e.do(t, "PATCH", path, `{"parent":{"actor":"claude","session_id":"sess-1.child"}}`, nil)
	wantStatus(t, rec, 422, "descendant cycle")
	wantDetail(t, decodeErr(t, rec), "parent", "cycle")
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

	for _, q := range []string{"?active=yes", "?named=1", "?waiting=maybe", "?parent=claude", "?parent=claude/bad%20id", "?actor=BAD", "?project=bad", "?limit=201", "?foo=1"} {
		wantStatus(t, e.do(t, "GET", "/v1/sessions"+q, "", nil), 400, q)
	}
	wantStatus(t, e.do(t, "GET", "/v1/sessions?project=NOPE", "", nil), 404, "unknown project")
}

func TestPatchSessionWaitingStateRules(t *testing.T) {
	e, clock := newSessionEnv(t)
	path := "/v1/sessions/claude/sess-1"
	e.do(t, "GET", "/v1/actors/me", "", nil) // registers claude/sess-1

	// State is null, state_reason only -> 422 requires_state.
	rec := e.do(t, "PATCH", path, `{"state_reason":"이유"}`, nil)
	wantStatus(t, rec, 422, "reason without state")
	wantDetail(t, decodeErr(t, rec), "state_reason", "requires_state")

	// state: null with non-null state_reason -> 422 requires_state.
	rec = e.do(t, "PATCH", path, `{"state":null,"state_reason":"이유"}`, nil)
	wantStatus(t, rec, 422, "null state with reason")
	wantDetail(t, decodeErr(t, rec), "state_reason", "requires_state")

	// Invalid state strings and non-string -> 422 field state.
	for _, body := range []string{`{"state":"paused"}`, `{"state":""}`, `{"state":123}`, `{"state":true}`} {
		rec = e.do(t, "PATCH", path, body, nil)
		wantStatus(t, rec, 422, body)
		wantDetail(t, decodeErr(t, rec), "state", "")
	}

	// Invalid state_reason -> 422 field state_reason.
	for _, body := range []string{
		`{"state":"waiting_input","state_reason":""}`,
		`{"state":"waiting_input","state_reason":"   "}`,
		`{"state":"waiting_input","state_reason":"` + strings.Repeat("가", 201) + `"}`,
		`{"state":"waiting_input","state_reason":"줄바꿈\n포함"}`,
		`{"state":"waiting_input","state_reason":"널\u0000포함"}`,
		`{"state":"waiting_input","state_reason":42}`,
	} {
		rec = e.do(t, "PATCH", path, body, nil)
		wantStatus(t, rec, 422, body)
		wantDetail(t, decodeErr(t, rec), "state_reason", "")
	}

	// Set waiting_approval + reason -> 200, state_at and updated_at updated.
	clock.add(time.Minute)
	t1 := model.NewTime(clock.Now())
	rec = e.do(t, "PATCH", path, `{"state":"waiting_approval","state_reason":"배포 승인 대기"}`, nil)
	wantStatus(t, rec, 200, "set waiting_approval")
	var got model.Session
	decodeJSON(t, rec, &got)
	if got.State == nil || *got.State != "waiting_approval" || got.StateReason == nil || *got.StateReason != "배포 승인 대기" ||
		got.StateAt == nil || *got.StateAt != t1 || got.UpdatedAt != t1 {
		t.Errorf("set waiting = %s", rec.Body)
	}
	// Verify waiting state stayed persisted (not auto-cleared by its own PATCH).
	if persisted := e.session(t, "claude", "sess-1"); persisted.State == nil || *persisted.State != "waiting_approval" {
		t.Fatalf("persisted state was cleared: %+v", persisted)
	}

	// Update only state_reason -> 200, reason updated, state_at kept, waiting NOT auto-cleared.
	clock.add(time.Minute)
	t2 := model.NewTime(clock.Now())
	rec = e.do(t, "PATCH", path, `{"state_reason":"사유만 변경"}`, nil)
	wantStatus(t, rec, 200, "reason only")
	decodeJSON(t, rec, &got)
	if got.State == nil || *got.State != "waiting_approval" || got.StateReason == nil || *got.StateReason != "사유만 변경" ||
		got.StateAt == nil || *got.StateAt != t1 || got.UpdatedAt != t2 {
		t.Errorf("reason only = %s", rec.Body)
	}
	if persisted := e.session(t, "claude", "sess-1"); persisted.State == nil || *persisted.State != "waiting_approval" {
		t.Fatalf("reason-only PATCH auto-cleared state: %+v", persisted)
	}

	// Clear only state_reason with null -> state and state_at kept.
	clock.add(time.Minute)
	t2b := model.NewTime(clock.Now())
	rec = e.do(t, "PATCH", path, `{"state_reason":null}`, nil)
	wantStatus(t, rec, 200, "clear reason only")
	decodeJSON(t, rec, &got)
	if got.State == nil || *got.State != "waiting_approval" || got.StateReason != nil || got.StateAt == nil || *got.StateAt != t1 || got.UpdatedAt != t2b {
		t.Errorf("clear reason only = %s", rec.Body)
	}

	// Set waiting_input with reason, then waiting_external without reason -> new state_at, reason reset to null.
	clock.add(time.Minute)
	rec = e.do(t, "PATCH", path, `{"state":"waiting_input","state_reason":"입력 필요"}`, nil)
	wantStatus(t, rec, 200, "set waiting_input")
	clock.add(time.Minute)
	t3 := model.NewTime(clock.Now())
	rec = e.do(t, "PATCH", path, `{"state":"waiting_external"}`, nil)
	wantStatus(t, rec, 200, "switch to waiting_external")
	decodeJSON(t, rec, &got)
	if got.State == nil || *got.State != "waiting_external" || got.StateReason != nil || got.StateAt == nil || *got.StateAt != t3 {
		t.Errorf("switch state without reason = %s", rec.Body)
	}

	// Explicit state: null -> all three null.
	clock.add(time.Minute)
	t4 := model.NewTime(clock.Now())
	rec = e.do(t, "PATCH", path, `{"state":null}`, nil)
	wantStatus(t, rec, 200, "clear state")
	decodeJSON(t, rec, &got)
	if got.State != nil || got.StateReason != nil || got.StateAt != nil || got.UpdatedAt != t4 {
		t.Errorf("clear state = %s", rec.Body)
	}
}

func TestSessionWaitingAutoClearAndFilter(t *testing.T) {
	e, clock := newSessionEnv(t)
	e.do(t, "POST", "/v1/projects", `{"key":"APP","name":"app"}`, map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-1"})
	rec := e.do(t, "POST", "/v1/issues", `{"project":"APP","title":"t","body":"b"}`, map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-1"})
	wantStatus(t, rec, 201, "seed issue")

	// Register claude/sess-1 and put it in waiting_input.
	e.do(t, "GET", "/v1/actors/me", "", nil)
	clock.add(time.Minute)
	rec = e.do(t, "PATCH", "/v1/sessions/claude/sess-1", `{"state":"waiting_input","state_reason":"확인 요청"}`, nil)
	wantStatus(t, rec, 200, "set waiting")

	// Filter GET /v1/sessions by waiting=true and waiting=false.
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"?waiting=true", "claude/sess-1"},
		{"?waiting=false", "owner/owner-1"},
	} {
		rec = e.do(t, "GET", "/v1/sessions"+tc.query, "", map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-1"})
		wantStatus(t, rec, 200, tc.query)
		var l struct {
			Items []model.Session `json:"items"`
			Total int             `json:"total"`
		}
		decodeJSON(t, rec, &l)
		if l.Total != 1 || len(l.Items) != 1 || l.Items[0].Actor+"/"+l.Items[0].SessionID != tc.want {
			t.Errorf("%s = %+v, want %s", tc.query, l.Items, tc.want)
		}
	}

	// GET request by waiting session keeps waiting state.
	rec = e.do(t, "GET", "/v1/issues", "", nil)
	wantStatus(t, rec, 200, "get issues")
	if s := e.session(t, "claude", "sess-1"); s.State == nil || *s.State != "waiting_input" {
		t.Fatalf("GET cleared waiting state: %+v", s)
	}

	// Failed write (422) by waiting session keeps waiting state.
	rec = e.do(t, "POST", "/v1/issues", `{"project":"APP","title":""}`, nil)
	wantStatus(t, rec, 422, "invalid issue create")
	if s := e.session(t, "claude", "sess-1"); s.State == nil || *s.State != "waiting_input" {
		t.Fatalf("422 write cleared waiting state: %+v", s)
	}

	// Successful write by another session (owner) keeps claude's waiting state.
	rec = e.do(t, "POST", "/v1/issues/APP-1/comments", `{"body":"owner note"}`, map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-1"})
	wantStatus(t, rec, 201, "owner comment")
	if s := e.session(t, "claude", "sess-1"); s.State == nil || *s.State != "waiting_input" {
		t.Fatalf("other session write cleared claude waiting state: %+v", s)
	}

	// Successful 2xx write (comment) by waiting session auto-clears waiting state.
	clock.add(time.Minute)
	fpBefore, _ := e.st.FingerprintNow(t.Context())
	rec = e.do(t, "POST", "/v1/issues/APP-1/comments", `{"body":"재개합니다"}`, nil)
	wantStatus(t, rec, 201, "claude comment")
	s := e.session(t, "claude", "sess-1")
	if s.State != nil || s.StateReason != nil || s.StateAt != nil || s.UpdatedAt != model.NewTime(clock.Now()) {
		t.Errorf("after 2xx write: %+v", s)
	}
	if fpAfter, _ := e.st.FingerprintNow(t.Context()); fpAfter == fpBefore {
		t.Errorf("auto-clear did not change fingerprint: %q", fpBefore)
	}

	// PATCH {"role":"main"} on self (without state/state_reason) auto-clears waiting state.
	clock.add(time.Minute)
	rec = e.do(t, "PATCH", "/v1/sessions/claude/sess-1", `{"state":"waiting_approval","state_reason":"승인 대기"}`, nil)
	wantStatus(t, rec, 200, "re-set waiting")
	clock.add(time.Minute)
	rec = e.do(t, "PATCH", "/v1/sessions/claude/sess-1", `{"role":"main"}`, nil)
	wantStatus(t, rec, 200, "patch role on self")
	var patched model.Session
	decodeJSON(t, rec, &patched)
	if patched.Role == nil || *patched.Role != "main" || patched.State != nil || patched.StateReason != nil || patched.StateAt != nil {
		t.Errorf("patch role response = %s", rec.Body)
	}
	if persisted := e.session(t, "claude", "sess-1"); persisted.State != nil || persisted.StateReason != nil || persisted.StateAt != nil {
		t.Errorf("patch role persisted = %+v", persisted)
	}
}

func TestAutoClearDBErrorDoesNotBreakWriteResponse(t *testing.T) {
	e, _ := newSessionEnv(t)
	e.do(t, "POST", "/v1/projects", `{"key":"APP","name":"app"}`, map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-1"})
	e.do(t, "POST", "/v1/issues", `{"project":"APP","title":"t","body":"b"}`, map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-1"})
	if _, err := e.st.DB().Exec(`drop table session`); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "POST", "/v1/issues/APP-1/comments", `{"body":"note"}`, nil)
	wantStatus(t, rec, 201, "comment with broken session table")
	if !strings.Contains(e.log.String(), "session waiting clear failed") {
		t.Errorf("expected warning log for clear failure, got: %s", e.log.String())
	}
}
