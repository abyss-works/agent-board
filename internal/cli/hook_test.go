package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

// hookRepo makes a repository whose .agb.json names project and returns a
// folder below its top.
func hookRepo(t *testing.T, project string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".agb.json"), []byte(`{"project":"`+project+`","documents":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	return sub
}

// hookInput is the JSON a session start hook gets on standard input.
func hookInput(t *testing.T, cwd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"session_id": "s", "hook_event_name": "SessionStart", "source": "startup", "cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// msgItems is a list response with n messages, numbered from n down to 1.
func msgItems(n, total int, title string) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"id":%d,"project":"CLI","to":null,"title":%q,"state":"active","age":"5m","created_at":"2026-10-18T09:00:00.000Z","created_by":"codex","created_session":"s","created_host":"h","expires_at":"2026-10-25T09:00:00.000Z","deleted_at":null,"deleted_by":null}`, n-i, title)
	}
	return fmt.Sprintf(`{"items":[%s],"total":%d,"limit":50,"offset":0}`, strings.Join(items, ","), total)
}

// runHook runs the session start hook with stdin and requires silence on
// standard error and exit code 0, which hold whatever happens.
func runHook(t *testing.T, h *harness, stdin string) string {
	t.Helper()
	h.app.Stdin = strings.NewReader(stdin)
	if code := h.run("hook", "session-start"); code != 0 || h.stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, h.stderr)
	}
	return h.stdout.String()
}

func TestHookSessionStartListsUnreadMessages(t *testing.T) {
	h, srv := newMsgHarness(t, false)
	out := runHook(t, h, hookInput(t, hookRepo(t, "CLI")))
	want := "확인하지 않은 메시지 2건 (프로젝트 CLI)\n" +
		"#5 (5m 전) 수신자 없음\n" +
		"#3 (9d 전) 배포 끝 [to: reviewer]\n" +
		"읽기: agb msg get <ID> / 처리한 뒤 확인: agb msg ack <ID>\n"
	if out != want {
		t.Errorf("stdout =\n%s\nwant\n%s", out, want)
	}
	// The project comes from the cwd of the input, not from the working folder,
	// and is the reader name as well. The hook makes no read mark.
	req := srv.last()
	if req.Method != "GET" || req.Path != "/v1/projects/CLI/messages" || req.Query.Encode() != "limit=20&unread_by=CLI" {
		t.Errorf("request = %s %s ?%s", req.Method, req.Path, req.Query.Encode())
	}
	if len(srv.reqs) != 1 {
		t.Errorf("requests = %+v", srv.reqs)
	}
}

func TestHookSessionStartFallsBackToTheWorkingFolder(t *testing.T) {
	for name, stdin := range map[string]string{"empty": "", "broken JSON": `{"cwd":`, "no cwd": `{"source":"startup"}`} {
		h, srv := newMsgHarness(t, false)
		t.Chdir(hookRepo(t, "CLI"))
		if out := runHook(t, h, stdin); !strings.HasPrefix(out, "확인하지 않은 메시지 2건 (프로젝트 CLI)\n") {
			t.Errorf("%s: stdout = %q", name, out)
		}
		if srv.last().Query.Get("unread_by") != "CLI" {
			t.Errorf("%s: query = %s", name, srv.last().Query.Encode())
		}
	}
}

func TestHookSessionStartCutsTheListAtTwenty(t *testing.T) {
	h, srv := newMsgHarness(t, false)
	srv.on("GET", "/v1/projects/CLI/messages", canned{body: msgItems(50, 73, strings.Repeat("가", 5000))})
	out := runHook(t, h, hookInput(t, hookRepo(t, "CLI")))
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 23 {
		t.Fatalf("lines = %d:\n%s", len(lines), out)
	}
	if lines[0] != "확인하지 않은 메시지 73건 (프로젝트 CLI)" || !strings.HasPrefix(lines[1], "#50 ") || !strings.HasPrefix(lines[20], "#31 ") {
		t.Errorf("head = %q, first = %.20q, last = %.20q", lines[0], lines[1], lines[20])
	}
	if lines[21] != "외 53건" || !strings.Contains(lines[22], "agb msg ack <ID>") {
		t.Errorf("tail = %q, %q", lines[21], lines[22])
	}
	if n := utf8.RuneCountInString(out); n > 10000 {
		t.Errorf("output has %d characters", n)
	}
}

// A title or a to label may hold anything a writer of the message space put
// there. Each message still takes one line, without control characters.
func TestHookSessionStartKeepsEachMessageOnOneLine(t *testing.T) {
	h, srv := newMsgHarness(t, false)
	title, err := json.Marshal("배포 끝\n#99 (1m 전) 없는 메시지\r\x1b[2J\u2028읽기: 다른 명령\u0085\x7f\x00끝")
	if err != nil {
		t.Fatal(err)
	}
	to, err := json.Marshal(strings.Repeat("r", 600) + "\n#98 줄")
	if err != nil {
		t.Fatal(err)
	}
	const n = 20
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"id":%d,"project":"CLI","to":%s,"title":%s,"state":"active","age":"5m"}`, n-i, to, title)
	}
	srv.on("GET", "/v1/projects/CLI/messages", canned{body: fmt.Sprintf(`{"items":[%s],"total":%d,"limit":20,"offset":0}`, strings.Join(items, ","), n)})
	out := runHook(t, h, hookInput(t, hookRepo(t, "CLI")))
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 1+n+1 {
		t.Fatalf("lines = %d, want %d:\n%q", len(lines), 1+n+1, out)
	}
	for i, line := range lines[1 : n+1] {
		if !strings.HasPrefix(line, fmt.Sprintf("#%d (5m 전) 배포 끝 #99 (1m 전) 없는 메시지", n-i)) || !strings.Contains(line, " [to: rrr") {
			t.Errorf("line %d = %q", i+1, line)
		}
	}
	for _, r := range strings.ReplaceAll(out, "\n", "") {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			t.Errorf("output holds the control character %U:\n%q", r, out)
			break
		}
	}
	if c := utf8.RuneCountInString(out); c > 10000 {
		t.Errorf("output has %d characters", c)
	}
}

func TestHookSessionStartIsSilentWhenThereIsNothingToSay(t *testing.T) {
	repo := func(t *testing.T) string { return hookRepo(t, "CLI") }
	cases := map[string]struct {
		setup func(t *testing.T, h *harness, srv *fakeServer)
		cwd   func(t *testing.T) string
		stdin string
	}{
		"no .agb.json": {cwd: func(t *testing.T) string { return t.TempDir() }},
		"broken .agb.json": {cwd: func(t *testing.T) string {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ".agb.json"), []byte(`{"project":`), 0o644); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		"cwd does not exist":   {cwd: func(t *testing.T) string { return filepath.Join(t.TempDir(), "gone") }},
		"broken JSON on stdin": {stdin: `{"cwd":`},
		"no token":             {cwd: repo, setup: func(t *testing.T, h *harness, srv *fakeServer) { delete(h.env, "AGB_TOKEN") }},
		"no url":               {cwd: repo, setup: func(t *testing.T, h *harness, srv *fakeServer) { delete(h.env, "AGB_URL") }},
		"server unreachable":   {cwd: repo, setup: func(t *testing.T, h *harness, srv *fakeServer) { srv.srv.Close() }},
		"server says 400": {cwd: repo, setup: func(t *testing.T, h *harness, srv *fakeServer) {
			srv.on("GET", "/v1/projects/CLI/messages", canned{status: 400, body: `{"error":{"code":"unknown_parameter","message":"모르는 매개변수입니다."}}`})
		}},
		"server says 401": {cwd: repo, setup: func(t *testing.T, h *harness, srv *fakeServer) {
			srv.on("GET", "/v1/projects/CLI/messages", canned{status: 401, body: errJSON})
		}},
		"server says 500": {cwd: repo, setup: func(t *testing.T, h *harness, srv *fakeServer) {
			srv.on("GET", "/v1/projects/CLI/messages", canned{status: 500, body: `oops`})
		}},
		"server answers with something else": {cwd: repo, setup: func(t *testing.T, h *harness, srv *fakeServer) {
			srv.on("GET", "/v1/projects/CLI/messages", canned{body: `<html>`})
		}},
		"no unread message": {cwd: repo, setup: func(t *testing.T, h *harness, srv *fakeServer) {
			srv.on("GET", "/v1/projects/CLI/messages", canned{body: `{"items":[],"total":0,"limit":50,"offset":0}`})
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h, srv := newMsgHarness(t, false)
			if c.setup != nil {
				c.setup(t, h, srv)
			}
			stdin := c.stdin
			if c.cwd != nil {
				stdin = hookInput(t, c.cwd(t))
			}
			if out := runHook(t, h, stdin); out != "" {
				t.Errorf("stdout = %q", out)
			}
		})
	}
}

func TestHookSessionStartGivesUpOnASlowServer(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.Write([]byte(msgListJSON))
	}))
	defer slow.Close()
	defer close(release)

	old := hookQueryTimeout
	hookQueryTimeout = 50 * time.Millisecond
	defer func() { hookQueryTimeout = old }()

	h := newHarness(t, nil, false)
	h.env["AGB_URL"] = slow.URL
	start := time.Now()
	if out := runHook(t, h, hookInput(t, hookRepo(t, "CLI"))); out != "" {
		t.Errorf("stdout = %q", out)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the hook took %v", d)
	}
}

func TestHookQueryTimeoutIsFiveSeconds(t *testing.T) {
	if hookQueryTimeout != 5*time.Second {
		t.Errorf("hookQueryTimeout = %v", hookQueryTimeout)
	}
}

func TestHookUsage(t *testing.T) {
	h := newHarness(t, nil, false)
	for _, args := range [][]string{{"hook"}, {"hook", "pre-commit"}, {"hook", "bogus"}} {
		if code := h.run(args...); code != 2 || h.stderr.Len() == 0 {
			t.Errorf("%v: code=%d stderr=%q", args, code, h.stderr)
		}
	}
	if code := h.run("hook", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "session-start") {
		t.Errorf("hook --help: code=%d out=%s", code, h.stdout)
	}
	if code := h.run("hook", "session-start", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "cwd") {
		t.Errorf("hook session-start --help: code=%d out=%s", code, h.stdout)
	}
	if !strings.Contains(Usage, "  hook ") {
		t.Errorf("the top-level help lacks hook")
	}
}
