package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	issueJSON = `{"key":"CLI-1","project":"CLI","number":1,"title":"첫 이슈","body":"## 배경\n\n본문","type":"feature","status":"todo","priority":"P1","labels":["api"],"parent":null,"depends_on":[],"refs":["doc:api.md"],"recheck_condition":null,"version":3,"created_at":"2026-10-17T05:46:06.000Z","updated_at":"2026-10-17T07:10:21.000Z","closed_at":null,"status_changed_at":"2026-10-17T07:10:21.000Z","status_changed_by":"claude","created_by":"owner","updated_by":"claude"}`
	listJSON  = `{"items":[{"key":"CLI-1","project":"CLI","number":1,"title":"첫 이슈","type":"feature","status":"todo","priority":"P1","labels":["api"],"parent":null,"depends_on":[],"refs":[],"recheck_condition":null,"version":3,"created_at":"2026-10-17T05:46:06.000Z","updated_at":"2026-10-17T07:10:21.000Z","closed_at":null,"status_changed_at":"2026-10-17T07:10:21.000Z","status_changed_by":"claude","created_by":"owner","updated_by":"claude"}],"total":1,"limit":50,"offset":0}`
	errJSON   = `{"error":{"code":"validation_failed","message":"요청 값이 규칙에 맞지 않습니다.","details":[{"field":"priority","message":"P0, P1, P2, P3 중 하나여야 합니다."},{"field":"depends_on[1]","reason":"not_found","message":"이슈 CLI-99가 없습니다."}]}}`
)

type recorded struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   string
}

type canned struct {
	status int
	body   string
	etag   string
}

type fakeServer struct {
	srv    *httptest.Server
	reqs   []recorded
	routes map[string]canned
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{routes: map[string]canned{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.reqs = append(f.reqs, recorded{r.Method, r.URL.Path, r.URL.Query(), r.Header.Clone(), string(body)})
		c, ok := f.routes[r.Method+" "+r.URL.Path]
		if !ok {
			c = canned{404, `{"error":{"code":"not_found","message":"대상이 없습니다."}}`, ""}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if c.etag != "" {
			w.Header().Set("ETag", c.etag)
		}
		status := c.status
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		w.Write([]byte(c.body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) on(method, path string, c canned) { f.routes[method+" "+path] = c }

func (f *fakeServer) last() recorded { return f.reqs[len(f.reqs)-1] }

type harness struct {
	app    *App
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	env    map[string]string
}

func newHarness(t *testing.T, srv *fakeServer, tty bool) *harness {
	t.Helper()
	h := &harness{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, env: map[string]string{
		"AGB_TOKEN": "test-token-0123456789abcdef0123456789", "AGB_ACTOR": "claude", "AGB_SESSION_ID": "sess-1", "AGB_CLIENT_HOST": "desk",
	}}
	if srv != nil {
		h.env["AGB_URL"] = srv.srv.URL
	}
	h.app = &App{
		Version:          "t",
		Getenv:           func(k string) string { return h.env[k] },
		Hostname:         func() (string, error) { return "test-host", nil },
		Now:              func() time.Time { return noon },
		Location:         time.FixedZone("KST", 9*3600),
		ConfigPath:       filepath.Join(t.TempDir(), "agb", "config.json"),
		Stdin:            strings.NewReader(""),
		Stdout:           h.stdout,
		Stderr:           h.stderr,
		StdoutIsTerminal: tty,
		TermWidth:        80,
	}
	return h
}

func (h *harness) run(args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	return h.app.Run(context.Background(), args)
}

func TestJSONOutputIsServerBodyVerbatim(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues/CLI-1", canned{body: issueJSON, etag: `"3"`})
	h := newHarness(t, srv, false)
	if code := h.run("issue", "get", "CLI-1"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if h.stdout.String() != issueJSON+"\n" {
		t.Errorf("stdout = %q", h.stdout.String())
	}
	req := srv.last()
	if req.Header.Get("X-Actor") != "claude" || req.Header.Get("X-Session-Id") != "sess-1" || req.Header.Get("X-Client-Host") != "desk" || !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ") {
		t.Errorf("headers = %v", req.Header)
	}
}

func TestTableOutputOnTerminal(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues", canned{body: listJSON})
	srv.on("GET", "/v1/issues/CLI-1", canned{body: issueJSON, etag: `"3"`})
	h := newHarness(t, srv, true)
	if code := h.run("issue", "list"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "KEY") || !strings.Contains(out, "CLI-1") || !strings.Contains(out, "첫 이슈") || strings.Contains(out, `"items"`) {
		t.Errorf("table output:\n%s", out)
	}
	if code := h.run("issue", "get", "CLI-1"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	out = h.stdout.String()
	if !strings.Contains(out, "## 배경") || !strings.Contains(out, "2026-10-17 16:10") || !strings.Contains(out, "doc:api.md") {
		t.Errorf("issue view:\n%s", out)
	}
	if code := h.run("issue", "list", "--json"); code != 0 || h.stdout.String() != listJSON+"\n" {
		t.Errorf("--json on a terminal: code=%d out=%q", code, h.stdout.String())
	}
	h2 := newHarness(t, srv, false)
	if code := h2.run("--table", "issue", "list"); code != 0 || !strings.Contains(h2.stdout.String(), "KEY") {
		t.Errorf("--table on a pipe: code=%d out=%q", code, h2.stdout.String())
	}
}

func TestIssueListBuildsQuery(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues", canned{body: listJSON})
	h := newHarness(t, srv, false)
	code := h.run("issue", "list", "--project", "CLI", "--status", "todo,doing", "--label", "api", "--label", "cli",
		"--type", "bug", "--priority", "P0,P1", "--parent", "none", "--created-by", "claude", "-q", "설계 api", "--sort", "-updated_at", "--limit", "5", "--offset", "10")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	want := url.Values{"project": {"CLI"}, "status": {"todo,doing"}, "label": {"api", "cli"}, "type": {"bug"}, "priority": {"P0,P1"},
		"parent": {"none"}, "created_by": {"claude"}, "q": {"설계 api"}, "sort": {"-updated_at"}, "limit": {"5"}, "offset": {"10"}}
	if got := srv.last().Query; got.Encode() != want.Encode() {
		t.Errorf("query = %v, want %v", got, want)
	}
}

func TestIssueCreateReadsBodyFromStdin(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("POST", "/v1/issues", canned{status: 201, body: issueJSON, etag: `"1"`})
	h := newHarness(t, srv, false)
	h.app.Stdin = strings.NewReader("\ufeff## 배경\n\n한국어 본문\n")
	code := h.run("issue", "create", "--project", "CLI", "--title", "첫 이슈", "--type", "observation", "--priority", "P1",
		"--label", "api", "--label", "cli", "--parent", "CLI-9", "--depends-on", "CLI-2", "--ref", "doc:api.md", "--recheck", "다음 릴리즈", "--body-file", "-")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(srv.last().Body), &got); err != nil {
		t.Fatal(err)
	}
	if got["project"] != "CLI" || got["title"] != "첫 이슈" || got["type"] != "observation" || got["priority"] != "P1" || got["parent"] != "CLI-9" || got["recheck_condition"] != "다음 릴리즈" {
		t.Errorf("fields = %v", got)
	}
	if got["body"] != "## 배경\n\n한국어 본문\n" {
		t.Errorf("body = %q (BOM must be stripped)", got["body"])
	}
	if l, _ := got["labels"].([]any); len(l) != 2 {
		t.Errorf("labels = %v", got["labels"])
	}
	if _, has := got["status"]; has {
		t.Errorf("unset flags must not be sent: %v", got)
	}
	if h.stdout.String() != issueJSON+"\n" {
		t.Errorf("stdout = %q", h.stdout.String())
	}
}

func TestIssueCreateBodyFromFileAndUsageErrors(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("POST", "/v1/issues", canned{status: 201, body: issueJSON})
	h := newHarness(t, srv, false)
	path := filepath.Join(t.TempDir(), "body.md")
	os.WriteFile(path, []byte("파일 본문"), 0o600)
	if code := h.run("issue", "create", "--project", "CLI", "--title", "t", "--body-file", path); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if !strings.Contains(srv.last().Body, "파일 본문") {
		t.Errorf("body = %s", srv.last().Body)
	}
	if code := h.run("issue", "create", "--project", "CLI"); code != 2 || !strings.Contains(h.stderr.String(), "--title") {
		t.Errorf("missing title: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("issue", "create", "--project", "CLI", "--title", "t", "--body", "a", "--body-file", path); code != 2 {
		t.Errorf("both body flags: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("issue", "create", "--project", "CLI", "--title", "t", "--body-file", filepath.Join(t.TempDir(), "missing")); code != 2 {
		t.Errorf("missing file: code=%d stderr=%s", code, h.stderr)
	}
	if len(srv.reqs) != 1 {
		t.Errorf("usage errors must not reach the server: %d requests", len(srv.reqs))
	}
}

func TestIssueUpdateSendsMergePatch(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("PATCH", "/v1/issues/CLI-1", canned{body: issueJSON, etag: `"4"`})
	h := newHarness(t, srv, false)
	if code := h.run("issue", "update", "CLI-1", "--status", "doing", "--no-parent", "--title", "새 제목"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	req := srv.last()
	var got map[string]any
	json.Unmarshal([]byte(req.Body), &got)
	if got["status"] != "doing" || got["title"] != "새 제목" {
		t.Errorf("patch = %v", got)
	}
	if v, has := got["parent"]; !has || v != nil {
		t.Errorf("--no-parent must send parent:null: %s", req.Body)
	}
	if req.Header.Get("If-Match") != "" {
		t.Errorf("If-Match sent without a read: %q", req.Header.Get("If-Match"))
	}
	if code := h.run("issue", "update", "CLI-1", "--priority", "P0", "--if-match", "3"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if srv.last().Header.Get("If-Match") != `"3"` {
		t.Errorf("bare version must be quoted: %q", srv.last().Header.Get("If-Match"))
	}
	if code := h.run("issue", "update", "CLI-1"); code != 2 {
		t.Errorf("empty patch: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("issue", "update", "CLI-1", "--parent", "CLI-2", "--no-parent"); code != 2 {
		t.Errorf("conflicting parent flags: code=%d", code)
	}
}

func TestIssueUpdateAddLabelReadsThenPatchesWithIfMatch(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues/CLI-1", canned{body: issueJSON, etag: `"3"`})
	srv.on("PATCH", "/v1/issues/CLI-1", canned{body: issueJSON, etag: `"4"`})
	h := newHarness(t, srv, false)
	if code := h.run("issue", "update", "CLI-1", "--add-label", "cli", "--add-label", "api", "--remove-ref", "doc:api.md", "--add-depends-on", "CLI-2"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if len(srv.reqs) != 2 || srv.reqs[0].Method != "GET" || srv.reqs[1].Method != "PATCH" {
		t.Fatalf("requests = %+v", srv.reqs)
	}
	patch := srv.reqs[1]
	if patch.Header.Get("If-Match") != `"3"` {
		t.Errorf("If-Match = %q", patch.Header.Get("If-Match"))
	}
	var got map[string]any
	json.Unmarshal([]byte(patch.Body), &got)
	labels, _ := got["labels"].([]any)
	refs, _ := got["refs"].([]any)
	deps, _ := got["depends_on"].([]any)
	if len(labels) != 2 || labels[0] != "api" || labels[1] != "cli" || len(refs) != 0 || len(deps) != 1 || deps[0] != "CLI-2" {
		t.Errorf("patch = %s", patch.Body)
	}
	if _, has := got["title"]; has {
		t.Errorf("untouched fields must not be sent: %s", patch.Body)
	}
}

// A proxy that compresses responses may weaken the ETag (W/"3"). The CLI
// takes the version from the issue body, so the If-Match it sends stays valid.
func TestIssueUpdateIfMatchUsesBodyVersionWhenETagIsWeak(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues/CLI-1", canned{body: issueJSON, etag: `W/"3"`})
	srv.on("PATCH", "/v1/issues/CLI-1", canned{body: issueJSON, etag: `"4"`})
	h := newHarness(t, srv, false)
	if code := h.run("issue", "update", "CLI-1", "--add-ref", "doc:x.md"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if len(srv.reqs) != 2 || srv.reqs[1].Header.Get("If-Match") != `"3"` {
		t.Fatalf("requests = %+v", srv.reqs)
	}
}

func TestPreconditionFailedIsReported(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues/CLI-1", canned{body: issueJSON, etag: `"3"`})
	srv.on("PATCH", "/v1/issues/CLI-1", canned{status: 412, body: `{"error":{"code":"precondition_failed","message":"If-Match의 버전이 현재와 다릅니다."}}`})
	h := newHarness(t, srv, true)
	if code := h.run("issue", "update", "CLI-1", "--add-label", "x"); code != 1 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(h.stderr.String(), "precondition_failed") || !strings.Contains(h.stderr.String(), "다시") {
		t.Errorf("stderr = %s", h.stderr)
	}
}

func TestAPIErrorOutput(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("POST", "/v1/issues", canned{status: 422, body: errJSON})
	h := newHarness(t, srv, true)
	if code := h.run("issue", "create", "--project", "CLI", "--title", "t"); code != 1 {
		t.Fatalf("code=%d", code)
	}
	errOut := h.stderr.String()
	for _, want := range []string{"422", "validation_failed", "요청 값이 규칙에 맞지 않습니다.", "priority", "P0, P1, P2, P3", "depends_on[1]", "not_found"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
	if h.stdout.Len() != 0 {
		t.Errorf("stdout must stay empty on error: %q", h.stdout.String())
	}
	h2 := newHarness(t, srv, false)
	if code := h2.run("issue", "create", "--project", "CLI", "--title", "t"); code != 1 || h2.stderr.String() != errJSON+"\n" {
		t.Errorf("json mode error: code=%d stderr=%q", code, h2.stderr.String())
	}
}

func TestNetworkErrorIsExitOne(t *testing.T) {
	h := newHarness(t, nil, false)
	h.env["AGB_URL"] = "http://127.0.0.1:1"
	if code := h.run("health"); code != 1 || h.stderr.Len() == 0 {
		t.Errorf("code=%d stderr=%q", code, h.stderr.String())
	}
}

func TestMissingActorIsUsageError(t *testing.T) {
	srv := newFakeServer(t)
	h := newHarness(t, srv, false)
	delete(h.env, "AGB_ACTOR")
	if code := h.run("whoami"); code != 2 || !strings.Contains(h.stderr.String(), "AGB_ACTOR") {
		t.Errorf("code=%d stderr=%s", code, h.stderr)
	}
	if len(srv.reqs) != 0 {
		t.Error("request sent without an actor")
	}
}

func TestHealthNeedsOnlyURL(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/health", canned{body: `{"status":"ok","version":"0.1.0","api_version":1}`})
	h := newHarness(t, srv, true)
	delete(h.env, "AGB_ACTOR")
	delete(h.env, "AGB_TOKEN")
	if code := h.run("health"); code != 0 || !strings.Contains(h.stdout.String(), "ok") || !strings.Contains(h.stdout.String(), "0.1.0") {
		t.Errorf("code=%d out=%q err=%q", code, h.stdout.String(), h.stderr.String())
	}
}

func TestStatusAndViewCommands(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/actors/me", canned{body: `{"id":"claude","kind":"agent"}`})
	srv.on("GET", "/v1/actors", canned{body: `{"items":[{"id":"owner","kind":"user"},{"id":"claude","kind":"agent"}]}`})
	srv.on("GET", "/v1/board", canned{body: `{"project":"CLI","columns":[{"status":"backlog","total":0,"issues":[]},{"status":"todo","total":1,"issues":[{"key":"CLI-1","project":"CLI","number":1,"title":"첫 이슈","type":"feature","status":"todo","priority":"P1","labels":["api"],"parent":null,"depends_on":[],"refs":[],"recheck_condition":null,"version":3,"created_at":"2026-10-17T05:46:06.000Z","updated_at":"2026-10-17T07:10:21.000Z","closed_at":null,"status_changed_at":"2026-10-17T07:10:21.000Z","status_changed_by":"claude","created_by":"owner","updated_by":"claude"}]}]}`})
	srv.on("GET", "/v1/queue", canned{body: `{"items":[{"key":"CLI-1","project":"CLI","number":1,"title":"첫 이슈","type":"feature","status":"todo","priority":"P1","labels":[],"parent":null,"depends_on":["CLI-2"],"refs":[],"recheck_condition":null,"version":1,"created_at":"2026-10-17T05:46:06.000Z","updated_at":"2026-10-17T05:46:06.000Z","closed_at":null,"status_changed_at":"2026-10-17T05:46:06.000Z","status_changed_by":"owner","created_by":"owner","updated_by":"owner","ready":false,"blocked_by":["CLI-2"]}],"total":1,"limit":1,"offset":0}`})
	srv.on("GET", "/v1/labels", canned{body: `{"items":[{"label":"api","count":9},{"label":"cli","count":4}]}`})
	srv.on("GET", "/v1/activity", canned{body: `{"items":[{"id":501,"issue":"CLI-1","project":"CLI","kind":"updated","actor":"claude","session_id":"s","client_host":"h","at":"2026-10-17T07:10:21.000Z","changes":{"status":{"from":"todo","to":"doing"}},"summary":null,"body":null},{"id":502,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"codex","session_id":"s","client_host":"h","at":"2026-10-17T07:11:21.000Z","changes":null,"summary":null,"body":"댓글 본문\n둘째 줄"},{"id":503,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"s","client_host":"h","at":"2026-10-17T07:12:21.000Z","changes":null,"summary":"서버 랭킹 함수 구현","body":"랭킹 상세 본문"}],"total":3,"limit":50,"offset":0}`})
	srv.on("GET", "/v1/issues", canned{body: listJSON})
	h := newHarness(t, srv, true)

	cases := []struct {
		args  []string
		path  string
		query string
		want  []string
	}{
		{[]string{"whoami"}, "/v1/actors/me", "", []string{"claude", "agent"}},
		{[]string{"actors"}, "/v1/actors", "", []string{"owner", "user", "claude"}},
		{[]string{"board", "--project", "CLI", "--closed-days", "0", "--limit", "5"}, "/v1/board", "closed_days=0&limit=5&project=CLI", []string{"todo", "CLI-1"}},
		{[]string{"board"}, "/v1/board", "", []string{"backlog"}},
		{[]string{"queue", "--project", "CLI", "--status", "todo,backlog", "--limit", "1"}, "/v1/queue", "limit=1&project=CLI&status=todo%2Cbacklog", []string{"CLI-1", "CLI-2"}},
		{[]string{"labels", "--project", "CLI"}, "/v1/labels", "project=CLI", []string{"api", "9", "cli", "4"}},
		{[]string{"activity", "--project", "CLI", "--kind", "updated", "--field", "status", "--since", "2026-10-17T00:00:00Z", "--sort", "at"}, "/v1/activity", "field=status&kind=updated&project=CLI&since=2026-10-17T00%3A00%3A00Z&sort=at", []string{"claude", "todo", "doing", "댓글 본문", "서버 랭킹 함수 구현"}},
		{[]string{"search", "설계", "api", "--project", "CLI", "--status", "all"}, "/v1/issues", "project=CLI&q=%EC%84%A4%EA%B3%84+api&status=all", []string{"CLI-1"}},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			if code := h.run(tc.args...); code != 0 {
				t.Fatalf("code=%d stderr=%s", code, h.stderr)
			}
			req := srv.last()
			if req.Path != tc.path || req.Query.Encode() != tc.query {
				t.Errorf("request = %s?%s, want %s?%s", req.Path, req.Query.Encode(), tc.path, tc.query)
			}
			for _, w := range tc.want {
				if !strings.Contains(h.stdout.String(), w) {
					t.Errorf("stdout lacks %q:\n%s", w, h.stdout.String())
				}
			}
		})
	}
}

func TestProjectCommands(t *testing.T) {
	projectJSON := `{"key":"CLI","name":"cli 검증","status":"active","created_at":"2026-10-17T05:30:00.000Z","updated_at":"2026-10-17T05:30:00.000Z","created_by":"claude","updated_by":"claude"}`
	srv := newFakeServer(t)
	srv.on("GET", "/v1/projects", canned{body: `{"items":[` + projectJSON + `],"total":1,"limit":50,"offset":0}`})
	srv.on("POST", "/v1/projects", canned{status: 201, body: projectJSON})
	srv.on("GET", "/v1/projects/CLI", canned{body: projectJSON})
	srv.on("PATCH", "/v1/projects/CLI", canned{body: projectJSON})
	srv.on("DELETE", "/v1/projects/CLI", canned{body: projectJSON})
	srv.on("GET", "/v1/projects/CLI/overview", canned{body: `{"project":` + projectJSON + `,"generated_at":"2026-10-17T09:00:00.000Z","counts":{"total":2,"open":1,"by_status":{"backlog":1,"todo":0,"doing":0,"review":0,"done":1,"cancelled":0,"deleted":0},"by_type":{"feature":1,"bug":0,"design":0,"decision":0,"idea":0,"observation":0},"by_priority":{"P0":0,"P1":1,"P2":0,"P3":0}},"doing":{"total":0,"items":[]},"review":{"total":0,"items":[]},"blocked":{"total":0,"items":[]},"decisions":{"total":0,"items":[]},"next":{"total":1,"items":[{"key":"CLI-1","project":"CLI","number":1,"title":"다음 할 일","type":"feature","status":"todo","priority":"P1","labels":[],"parent":null,"depends_on":[],"refs":[],"recheck_condition":null,"version":1,"created_at":"2026-10-17T05:46:06.000Z","updated_at":"2026-10-17T05:46:06.000Z","closed_at":null,"status_changed_at":"2026-10-17T05:46:06.000Z","status_changed_by":"owner","created_by":"owner","updated_by":"owner","ready":true,"blocked_by":[]}]},"recent_activity":{"total":0,"items":[]}}`})
	h := newHarness(t, srv, true)

	cases := []struct {
		args   []string
		method string
		path   string
		query  string
		body   string
		want   []string
	}{
		{[]string{"project", "list", "--status", "all", "--sort", "-updated_at"}, "GET", "/v1/projects", "sort=-updated_at&status=all", "", []string{"CLI", "cli 검증", "active"}},
		{[]string{"project", "create", "CLI", "cli 검증"}, "POST", "/v1/projects", "", `{"key":"CLI","name":"cli 검증"}`, []string{"CLI"}},
		{[]string{"project", "get", "CLI"}, "GET", "/v1/projects/CLI", "", "", []string{"cli 검증", "2026-10-17 14:30"}},
		{[]string{"project", "update", "CLI", "--name", "새 이름", "--status", "archived"}, "PATCH", "/v1/projects/CLI", "", `{"name":"새 이름","status":"archived"}`, []string{"CLI"}},
		{[]string{"project", "delete", "CLI"}, "DELETE", "/v1/projects/CLI", "", "", []string{"CLI"}},
		{[]string{"project", "overview", "CLI", "--limit", "3"}, "GET", "/v1/projects/CLI/overview", "limit=3", "", []string{"cli 검증", "다음 할 일", "backlog", "P1"}},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			if code := h.run(tc.args...); code != 0 {
				t.Fatalf("code=%d stderr=%s", code, h.stderr)
			}
			req := srv.last()
			if req.Method != tc.method || req.Path != tc.path || req.Query.Encode() != tc.query || strings.TrimSpace(req.Body) != tc.body {
				t.Errorf("request = %s %s?%s %q", req.Method, req.Path, req.Query.Encode(), req.Body)
			}
			for _, w := range tc.want {
				if !strings.Contains(h.stdout.String(), w) {
					t.Errorf("stdout lacks %q:\n%s", w, h.stdout.String())
				}
			}
		})
	}
	if code := h.run("project", "create", "CLI"); code != 2 {
		t.Errorf("create without name: code=%d", code)
	}
	if code := h.run("project", "update", "CLI"); code != 2 {
		t.Errorf("update without fields: code=%d", code)
	}
}

func TestIssueCommentDeleteActivity(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("POST", "/v1/issues/CLI-1/comments", canned{status: 201, body: `{"id":7,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"sess-1","client_host":"desk","at":"2026-10-17T07:11:21.000Z","changes":null,"summary":null,"body":"댓글"}`})
	srv.on("DELETE", "/v1/issues/CLI-1", canned{body: issueJSON, etag: `"4"`})
	srv.on("GET", "/v1/issues/CLI-1/activity", canned{body: `{"items":[],"total":0,"limit":10,"offset":0}`})
	h := newHarness(t, srv, true)
	if code := h.run("issue", "comment", "CLI-1", "--body", "댓글"); code != 0 || strings.TrimSpace(srv.last().Body) != `{"body":"댓글"}` {
		t.Errorf("comment: code=%d body=%s stderr=%s", code, srv.last().Body, h.stderr)
	}
	if !strings.Contains(h.stdout.String(), "댓글") {
		t.Errorf("comment output:\n%s", h.stdout.String())
	}
	if code := h.run("issue", "comment", "CLI-1"); code != 2 {
		t.Errorf("comment without body: code=%d", code)
	}
	if code := h.run("issue", "comment", "CLI-1", "--summary", "요약만"); code != 2 {
		t.Errorf("comment with summary but no body: code=%d", code)
	}
	if code := h.run("issue", "delete", "CLI-1", "--reason", "시험", "--if-match", `"3"`); code != 0 || srv.last().Method != "DELETE" || srv.last().Header.Get("If-Match") != `"3"` {
		t.Errorf("delete: code=%d req=%+v", code, srv.last())
	}
	if code := h.run("issue", "activity", "CLI-1", "--limit", "10"); code != 0 || srv.last().Query.Get("limit") != "10" {
		t.Errorf("activity: code=%d req=%+v", code, srv.last())
	}
}

func TestIssueCommentSendsSummaryOnlyWhenGiven(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("POST", "/v1/issues/CLI-1/comments", canned{status: 201, body: `{"id":7,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"sess-1","client_host":"desk","at":"2026-10-17T07:11:21.000Z","changes":null,"summary":"서버 랭킹 함수 구현","body":"댓글"}`})
	h := newHarness(t, srv, true)

	if code := h.run("issue", "comment", "CLI-1", "--summary", "서버 랭킹 함수 구현", "--body", "댓글"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if got := strings.TrimSpace(srv.last().Body); got != `{"body":"댓글","summary":"서버 랭킹 함수 구현"}` {
		t.Errorf("request body = %s", got)
	}
	for _, want := range []string{"summary:", "서버 랭킹 함수 구현", "댓글"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, h.stdout.String())
		}
	}

	h.app.Stdin = strings.NewReader("본문 첫 줄\n둘째 줄\n")
	if code := h.run("issue", "comment", "CLI-1", "--summary", "랭킹 UI 위치 변경", "--body-file", "-"); code != 0 {
		t.Fatalf("stdin body: code=%d stderr=%s", code, h.stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(srv.last().Body), &got); err != nil {
		t.Fatal(err)
	}
	if got["summary"] != "랭킹 UI 위치 변경" || got["body"] != "본문 첫 줄\n둘째 줄\n" || len(got) != 2 {
		t.Errorf("request fields = %v", got)
	}

	for _, args := range [][]string{
		{"issue", "comment", "CLI-1", "--body", "댓글"},
		{"issue", "comment", "CLI-1", "--summary", "", "--body", "댓글"},
	} {
		if code := h.run(args...); code != 0 {
			t.Fatalf("%v: code=%d stderr=%s", args, code, h.stderr)
		}
		if got := strings.TrimSpace(srv.last().Body); got != `{"body":"댓글"}` {
			t.Errorf("%v: request body = %s, want no summary key", args, got)
		}
	}
}

func TestConfigCommands(t *testing.T) {
	h := newHarness(t, nil, true)
	for k := range h.env {
		delete(h.env, k)
	}
	if code := h.run("config", "path"); code != 0 || strings.TrimSpace(h.stdout.String()) != h.app.ConfigPath {
		t.Errorf("path: code=%d out=%q", code, h.stdout.String())
	}
	if code := h.run("config", "set", "url", "http://127.0.0.1:8421/"); code != 0 {
		t.Fatalf("set url: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("config", "set", "actor", "claude"); code != 0 {
		t.Fatalf("set actor: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("config", "set", "bogus", "x"); code != 2 {
		t.Errorf("unknown key: code=%d", code)
	}
	if code := h.run("config", "set", "token", "value"); code != 2 || !strings.Contains(h.stderr.String(), "set-token") {
		t.Errorf("token through args must be refused: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("config", "set", "actor", "Bad Actor"); code != 2 {
		t.Errorf("invalid actor accepted: code=%d", code)
	}
	h.app.Stdin = strings.NewReader("secret-token-0123456789abcdef0123456789\n")
	if code := h.run("config", "set-token"); code != 0 {
		t.Fatalf("set-token: code=%d stderr=%s", code, h.stderr)
	}
	if strings.Contains(h.stdout.String()+h.stderr.String(), "secret-token") {
		t.Errorf("token echoed: %s %s", h.stdout, h.stderr)
	}
	cfg, err := loadConfig(h.app.ConfigPath)
	if err != nil || cfg.URL != "http://127.0.0.1:8421" || cfg.Actor != "claude" || cfg.Token != "secret-token-0123456789abcdef0123456789" {
		t.Errorf("config = %+v %v", cfg, err)
	}
	if code := h.run("config", "show"); code != 0 {
		t.Fatalf("show: code=%d stderr=%s", code, h.stderr)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "http://127.0.0.1:8421") || !strings.Contains(out, "claude") || strings.Contains(out, "secret-token") || !strings.Contains(out, "token") {
		t.Errorf("show:\n%s", out)
	}
	if code := h.run("config", "show", "--json"); code != 0 || strings.Contains(h.stdout.String(), "secret-token") {
		t.Errorf("show --json leaks token: %s", h.stdout.String())
	}
	if code := h.run("config", "unset", "actor"); code != 0 {
		t.Fatalf("unset: code=%d stderr=%s", code, h.stderr)
	}
	if cfg, _ := loadConfig(h.app.ConfigPath); cfg.Actor != "" || cfg.URL == "" {
		t.Errorf("after unset = %+v", cfg)
	}
	h.app.Stdin = strings.NewReader("\n")
	if code := h.run("config", "set-token"); code != 2 {
		t.Errorf("empty token accepted: code=%d", code)
	}
}

func TestConfigFlagOverridesPath(t *testing.T) {
	h := newHarness(t, nil, true)
	other := filepath.Join(t.TempDir(), "other.json")
	if code := h.run("--config", other, "config", "set", "url", "http://x"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("file not written at --config path: %v", err)
	}
	if _, err := os.Stat(h.app.ConfigPath); err == nil {
		t.Error("default path written despite --config")
	}
}

func TestConfigFileFeedsCommands(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/actors/me", canned{body: `{"id":"owner","kind":"user"}`})
	h := newHarness(t, nil, false)
	for k := range h.env {
		delete(h.env, k)
	}
	saveConfig(h.app.ConfigPath, Config{URL: srv.srv.URL, Token: "file-token-0123456789abcdef0123456789", Actor: "owner", SessionID: "file-sess"})
	if code := h.run("whoami"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	req := srv.last()
	if req.Header.Get("Authorization") != "Bearer file-token-0123456789abcdef0123456789" || req.Header.Get("X-Actor") != "owner" || req.Header.Get("X-Session-Id") != "file-sess" || req.Header.Get("X-Client-Host") != "test-host" {
		t.Errorf("headers = %v", req.Header)
	}
}

func TestHelpAndUnknownCommands(t *testing.T) {
	h := newHarness(t, nil, true)
	if code := h.run("help"); code != 0 {
		t.Errorf("help: code=%d", code)
	}
	for _, w := range []string{"serve", "version", "config", "issue", "project", "board", "queue", "labels", "search", "activity", "health", "whoami", "actors"} {
		if !strings.Contains(h.stdout.String(), w) {
			t.Errorf("help lacks %q", w)
		}
	}
	if code := h.run("issue", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "create") {
		t.Errorf("issue --help: code=%d out=%s", code, h.stdout)
	}
	if code := h.run("issue", "create", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "--body-file") {
		t.Errorf("issue create --help: code=%d out=%s", code, h.stdout)
	}
	if code := h.run("issue", "comment", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "--summary") || !strings.Contains(h.stdout.String(), "--body-file") {
		t.Errorf("issue comment --help: code=%d out=%s", code, h.stdout)
	}
	if code := h.run("help", "issue", "update"); code != 0 || !strings.Contains(h.stdout.String(), "--add-label") {
		t.Errorf("help issue update: code=%d out=%s", code, h.stdout)
	}
	if code := h.run("bogus"); code != 2 || h.stderr.Len() == 0 {
		t.Errorf("unknown: code=%d", code)
	}
	if code := h.run("issue", "bogus"); code != 2 {
		t.Errorf("unknown sub: code=%d", code)
	}
	if code := h.run("issue"); code != 2 {
		t.Errorf("group without sub: code=%d", code)
	}
	if code := h.run("issue", "get"); code != 2 || !strings.Contains(h.stderr.String(), "KEY") {
		t.Errorf("missing key: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("issue", "get", "--bogus", "CLI-1"); code != 2 {
		t.Errorf("unknown flag: code=%d", code)
	}
}
