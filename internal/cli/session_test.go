package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const (
	sessionChildJSON = `{"actor":"claude","session_id":"sess-1.ab12cd","named":true,"first_seen_at":"2026-11-20T01:00:00.000Z","last_seen_at":"2026-11-20T01:00:00.000Z","last_host":"desk","last_project":null,"parent":{"actor":"claude","session_id":"sess-1"},"role":"subagent","issue":"APP-12","location":null,"stale":false,"updated_at":"2026-11-20T01:00:00.000Z"}`

	sessionJSON = `{"actor":"claude","session_id":"sess-1","named":true,"first_seen_at":"2026-11-20T00:00:00.000Z","last_seen_at":"2026-11-20T01:00:00.000Z","last_host":"desk","last_project":"APP","parent":null,"role":"main","issue":null,` +
		`"location":{"repo_remote":"https://example.com/acme/app.git","branch":"dev","worktree":"/work/app","head":"abc1234","dirty_count":2,"reported_at":"2026-11-20T01:00:00.000Z"},"stale":false,"updated_at":"2026-11-20T01:00:00.000Z","children":[` + sessionChildJSON + `]}`

	sessionPatchedJSON = `{"actor":"claude","session_id":"sess-1","named":true,"first_seen_at":"2026-11-20T00:00:00.000Z","last_seen_at":"2026-11-20T01:00:00.000Z","last_host":"desk","last_project":"APP","parent":null,"role":"main","issue":null,` +
		`"location":{"repo_remote":"https://example.com/acme/app.git","branch":"dev","worktree":"/work/app","head":"abc1234","dirty_count":2,"reported_at":"2026-11-20T01:00:00.000Z"},"stale":false,"updated_at":"2026-11-20T01:00:00.000Z"}`

	sessionListJSON = `{"items":[` + sessionChildJSON + `,` +
		`{"actor":"codex","session_id":"cli-20261119","named":false,"first_seen_at":"2026-11-19T00:00:00.000Z","last_seen_at":"2026-11-19T03:00:00.000Z","last_host":"laptop","last_project":"APP","parent":null,"role":null,"issue":null,"location":null,"stale":true,"updated_at":"2026-11-19T00:00:00.000Z"}` +
		`],"total":2,"limit":50,"offset":0}`
)

func newSessionHarness(t *testing.T, tty bool) (*harness, *fakeServer) {
	t.Helper()
	t.Chdir(t.TempDir())
	srv := newFakeServer(t)
	srv.on("GET", "/v1/sessions", canned{body: sessionListJSON})
	srv.on("GET", "/v1/sessions/claude/sess-1", canned{body: sessionJSON})
	srv.on("GET", "/v1/sessions/codex/cli-20261119", canned{body: sessionChildJSON})
	srv.on("PATCH", "/v1/sessions/claude/sess-1", canned{body: sessionPatchedJSON})
	srv.on("PATCH", "/v1/sessions/claude/sess-1.ab12cd", canned{body: sessionChildJSON})
	srv.on("POST", "/v1/sessions", canned{status: 201, body: sessionChildJSON})
	return newHarness(t, srv, tty), srv
}

func TestSessionUsageErrors(t *testing.T) {
	h, srv := newSessionHarness(t, false)
	for _, args := range [][]string{
		{"session", "list", "--active", "--stale"},
		{"session", "list", "--named", "maybe"},
		{"session", "list", "--parent", "claude"},
		{"session", "set"},
		{"session", "set", "--location", "--clear-location"},
		{"session", "set", "--repo", "."},
		{"session", "set", "--parent", "nobody"},
		{"session", "get", "claude"},
		{"session", "get", "a/b", "c/d"},
		{"session", "start", "extra"},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%v: code=%d stderr=%s", args, code, h.stderr)
		}
	}
	if len(srv.reqs) != 0 {
		t.Errorf("usage errors must not reach the server: %v", srv.paths())
	}
}

func TestSessionListSendsFiltersAndPrintsATable(t *testing.T) {
	h, srv := newSessionHarness(t, true)
	if code := h.run("session", "list", "-p", "APP", "--actor", "claude", "--parent", "claude/sess-1", "--active", "--named", "true", "--limit", "5", "--offset", "1"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	q := srv.last().Query
	for k, want := range map[string]string{"project": "APP", "actor": "claude", "parent": "claude/sess-1", "active": "true", "named": "true", "limit": "5", "offset": "1"} {
		if got := q.Get(k); got != want {
			t.Errorf("query %s = %q, want %q", k, got, want)
		}
	}
	out := h.stdout.String()
	for _, want := range []string{"ACTOR", "SESSION", "ROLE", "PARENT", "ISSUE", "HOST", "PROJECT", "LAST_SEEN", "STALE",
		"sess-1.ab12cd", "claude/sess-1", "APP-12", "2026-11-20 10:00", "cli-20261119", "yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
	if code := h.run("session", "list", "--stale"); code != 0 || srv.last().Query.Get("active") != "false" {
		t.Errorf("--stale: code=%d query=%v", code, srv.last().Query)
	}
	if code := h.run("--actor", "owner", "session", "list", "--actor", "claude"); code != 0 ||
		srv.last().Header.Get("X-Actor") != "owner" || srv.last().Query.Get("actor") != "claude" {
		t.Errorf("--actor before and after: code=%d header=%q query=%v stderr=%s", code, srv.last().Header.Get("X-Actor"), srv.last().Query, h.stderr)
	}
}

func TestSessionListFieldsAfterTheCommand(t *testing.T) {
	h, _ := newSessionHarness(t, true)
	if code := h.run("session", "list", "--fields", "actor", "--text"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if got := h.stdout.String(); got != "claude\ncodex\n" {
		t.Errorf("stdout = %q", got)
	}
	if code := h.run("--fields", "actor,session_id", "--text", "session", "list"); code != 0 {
		t.Fatalf("before command: code=%d stderr=%s", code, h.stderr)
	}
	if got := h.stdout.String(); got != "claude\tsess-1.ab12cd\ncodex\tcli-20261119\n" {
		t.Errorf("before command stdout = %q", got)
	}
}

func TestSessionGetDefaultsToTheCurrentSession(t *testing.T) {
	h, srv := newSessionHarness(t, true)
	if code := h.run("session", "get"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Method != "GET" || req.Path != "/v1/sessions/claude/sess-1" {
		t.Errorf("request = %s %s", req.Method, req.Path)
	}
	out := h.stdout.String()
	for _, want := range []string{"sess-1", "main", "APP", "https://example.com/acme/app.git", "dev", "abc1234", "sess-1.ab12cd", "subagent"} {
		if !strings.Contains(out, want) {
			t.Errorf("get lacks %q:\n%s", want, out)
		}
	}
	if code := h.run("session", "get", "codex/cli-20261119"); code != 0 || srv.last().Path != "/v1/sessions/codex/cli-20261119" {
		t.Errorf("named: code=%d path=%s", code, srv.last().Path)
	}
	if code := h.run("--session-id", "sess-1.ab12cd", "session", "set", "--role", "runner"); code != 0 || srv.last().Path != "/v1/sessions/claude/sess-1.ab12cd" {
		t.Errorf("global session id: code=%d path=%s stderr=%s", code, srv.last().Path, h.stderr)
	}
}

func TestSessionSetSendsAMergePatch(t *testing.T) {
	h, srv := newSessionHarness(t, false)
	if code := h.run("session", "set", "--role", "none", "--issue", "APP-12", "--parent", "claude/main-1"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	req := srv.last()
	if req.Method != "PATCH" || req.Path != "/v1/sessions/claude/sess-1" ||
		strings.TrimSpace(req.Body) != `{"issue":"APP-12","parent":{"actor":"claude","session_id":"main-1"},"role":null}` {
		t.Errorf("request = %s %s %s", req.Method, req.Path, req.Body)
	}
	if code := h.run("session", "set", "claude/sess-1", "--parent", "none", "--issue", "none", "--clear-location"); code != 0 {
		t.Fatalf("clear: code=%d stderr=%s", code, h.stderr)
	}
	if got := strings.TrimSpace(srv.last().Body); got != `{"issue":null,"location":null,"parent":null}` {
		t.Errorf("clear body = %s", got)
	}
	if code := h.run("session", "set", "--role", "main", "--table"); code != 0 || strings.Contains(h.stdout.String(), "자식 세션") {
		t.Errorf("table output after set: code=%d out=%s stderr=%s", code, h.stdout, h.stderr)
	}
}

func TestSessionSetLocationReadsGit(t *testing.T) {
	h, srv := newSessionHarness(t, false)
	repo := gitRepo(t)
	if code := h.run("session", "set", "--location", "--repo", repo); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var body struct {
		Location map[string]any `json:"location"`
	}
	if err := json.Unmarshal([]byte(srv.last().Body), &body); err != nil {
		t.Fatal(err)
	}
	loc := body.Location
	if loc["branch"] != "dev" || loc["dirty_count"] != float64(1) || loc["head"] == nil || loc["worktree"] == nil {
		t.Errorf("location = %v", loc)
	}
	if wt, _ := loc["worktree"].(string); !filepath.IsAbs(wt) {
		t.Errorf("worktree %q is not absolute", wt)
	}
	if _, ok := loc["repo_remote"]; ok {
		t.Errorf("a repository without origin sent a remote: %v", loc)
	}

	n := len(srv.reqs)
	if code := h.run("session", "set", "--location", "--repo", t.TempDir()); code != 1 {
		t.Errorf("not a repository: code=%d", code)
	}
	if !strings.Contains(h.stderr.String(), "git 저장소가 아닙니다") || len(srv.reqs) != n {
		t.Errorf("not a repository: stderr=%s requests=%d", h.stderr, len(srv.reqs)-n)
	}
}

func TestSessionStart(t *testing.T) {
	h, srv := newSessionHarness(t, true)
	if code := h.run("session", "start", "--role", "subagent", "--issue", "APP-12"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Method != "POST" || req.Path != "/v1/sessions" || strings.TrimSpace(req.Body) != `{"issue":"APP-12","role":"subagent"}` {
		t.Errorf("request = %s %s %s", req.Method, req.Path, req.Body)
	}
	out := h.stdout.String()
	if !strings.HasPrefix(out, "sess-1.ab12cd\n") || !strings.Contains(out, "agb --session-id sess-1.ab12cd ") {
		t.Errorf("stdout = %q", out)
	}
	if code := h.run("session", "start", "--id", "sess-1.x"); code != 0 || strings.TrimSpace(srv.last().Body) != `{"session_id":"sess-1.x"}` {
		t.Errorf("--id: code=%d body=%s", code, srv.last().Body)
	}
	if code := h.run("session", "start", "--json"); code != 0 || strings.TrimSpace(h.stdout.String()) != sessionChildJSON {
		t.Errorf("--json: code=%d stdout=%s", code, h.stdout)
	}
}

func TestUsageListsSession(t *testing.T) {
	if !strings.Contains(Usage, "\n  session ") {
		t.Error("agb --help does not list session")
	}
}
