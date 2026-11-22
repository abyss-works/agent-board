package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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
func runHook(t *testing.T, h *harness, stdin string, args ...string) string {
	t.Helper()
	h.app.Stdin = strings.NewReader(stdin)
	if code := h.run(append([]string{"hook", "session-start"}, args...)...); code != 0 || h.stderr.Len() != 0 {
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

func TestHookSessionStartGivesUpOnAnUnclosedStdin(t *testing.T) {
	if hookStdinTimeout != time.Second || hookStdinMax != 1<<20 {
		t.Fatalf("hookStdinTimeout=%v hookStdinMax=%d", hookStdinTimeout, hookStdinMax)
	}
	old := hookStdinTimeout
	hookStdinTimeout = 40 * time.Millisecond
	defer func() { hookStdinTimeout = old }()

	h, _ := newMsgHarness(t, false)
	t.Chdir(hookRepo(t, "CLI"))
	pr, pw := io.Pipe()
	defer pw.Close()

	h.app.Stdin = pr
	start := time.Now()
	code := h.run("hook", "session-start")
	elapsed := time.Since(start)
	if code != 0 || h.stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, h.stderr)
	}
	if elapsed >= 500*time.Millisecond {
		t.Errorf("hook blocked on unclosed stdin for %v", elapsed)
	}
	if !strings.HasPrefix(h.stdout.String(), "확인하지 않은 메시지 2건 (프로젝트 CLI)\n") {
		t.Errorf("stdout = %q, want fallback to working folder", h.stdout.String())
	}

	// Oversized input (> 1 MiB) stops at hookStdinMax without hanging on an unclosed pipe.
	pr2, pw2 := io.Pipe()
	defer pw2.Close()
	go func() {
		pw2.Write([]byte(`{"cwd":"` + strings.Repeat("a", hookStdinMax+4096)))
	}()
	in := readHookInput(pr2, time.Second)
	if in != (hookStdin{}) {
		t.Errorf("oversized truncated JSON gave %+v, want empty hookStdin", in)
	}
}

func TestHookSessionStartDoesNotSpawnGitWithoutRepoConfig(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "git-ran")
	if runtime.GOOS == "windows" {
		script := "@echo ran > \"" + marker + "\"\r\n@exit /b 1\r\n"
		if err := os.WriteFile(filepath.Join(bin, "git.bat"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	} else {
		script := "#!/bin/sh\necho ran > \"" + marker + "\"\nexit 1\n"
		if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)

	plain := t.TempDir()
	t.Chdir(plain)
	h, _ := newMsgHarness(t, false)
	if out := runHook(t, h, hookInput(t, plain)); out != "" {
		t.Errorf("stdout = %q", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("hook spawned git while looking for .agb.json")
	}
}

func TestHookUsage(t *testing.T) {
	h := newHarness(t, nil, false)
	if code := h.run("hook"); code != 2 || h.stderr.Len() == 0 {
		t.Errorf("hook: code=%d stderr=%q", code, h.stderr)
	}
	if code := h.run("hook", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "session-start") || !strings.Contains(h.stdout.String(), "pre-commit") {
		t.Errorf("hook --help: code=%d out=%s", code, h.stdout)
	}
	if code := h.run("hook", "pre-commit", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "git commit") || h.stderr.Len() != 0 {
		t.Errorf("hook pre-commit --help: code=%d out=%s", code, h.stdout)
	}
	if code := h.run("hook", "session-start", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "cwd") {
		t.Errorf("hook session-start --help: code=%d out=%s", code, h.stdout)
	}
	if !strings.Contains(Usage, "  hook ") {
		t.Errorf("the top-level help lacks hook")
	}
}

// newHookActorHarness is a harness without AGB_ACTOR whose config file holds
// cfg, as in a session that the agent tool starts.
func newHookActorHarness(t *testing.T, cfg Config) (*harness, *fakeServer) {
	t.Helper()
	h, srv := newMsgHarness(t, false)
	delete(h.env, "AGB_ACTOR")
	if err := saveConfig(h.app.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}
	return h, srv
}

func TestHookSessionStartActsAsTheHookActorOfTheTool(t *testing.T) {
	h, srv := newHookActorHarness(t, Config{HookActors: map[string]string{"claude": "reviewer"}})
	out := runHook(t, h, hookInput(t, hookRepo(t, "CLI")), "--tool", "claude")
	if !strings.HasPrefix(out, "확인하지 않은 메시지 2건 (프로젝트 CLI)\n") {
		t.Errorf("stdout = %q", out)
	}
	if got := srv.last().Header.Get("X-Actor"); got != "reviewer" {
		t.Errorf("X-Actor = %q", got)
	}
}

func TestHookSessionStartActorOrder(t *testing.T) {
	both := Config{Actor: "base", HookActors: map[string]string{"claude": "reviewer"}}
	cases := map[string]struct {
		cfg        Config
		env        string
		args       []string
		want       string // the actor of the request; "" when no request is made
		wantNotice string
	}{
		"the flag comes first":                     {cfg: both, env: "fromenv", args: []string{"--tool", "claude", "--actor", "fromflag"}, want: "fromflag"},
		"AGB_ACTOR comes before the hook actor":    {cfg: both, env: "fromenv", args: []string{"--tool", "claude"}, want: "fromenv"},
		"the hook actor comes before the default":  {cfg: both, args: []string{"--tool", "claude"}, want: "reviewer"},
		"without --tool the default actor is used": {cfg: both, args: nil, want: "base"},
		"a tool without a hook actor":              {cfg: both, args: []string{"--tool", "other"}, want: "base"},
		"without --tool and without a default": {
			cfg:        Config{HookActors: map[string]string{"claude": "reviewer"}},
			wantNotice: "agb: 프로젝트 CLI의 미확인 메시지를 확인하지 못했습니다(행위자 미설정). --actor 또는 `agb setup <도구> --actor <ID>`로 설정하세요.\n",
		},
		"no actor anywhere": {
			args:       []string{"--tool", "claude"},
			wantNotice: "agb: 프로젝트 CLI의 미확인 메시지를 확인하지 못했습니다(훅 행위자 미설정). `agb setup claude --actor <ID>`로 설정하세요.\n",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h, srv := newHookActorHarness(t, c.cfg)
			if c.env != "" {
				h.env["AGB_ACTOR"] = c.env
			}
			out := runHook(t, h, hookInput(t, hookRepo(t, "CLI")), c.args...)
			if c.want == "" {
				if out != c.wantNotice || len(srv.reqs) != 0 {
					t.Errorf("stdout = %q (want %q), requests = %+v", out, c.wantNotice, srv.reqs)
				}
				return
			}
			if out == "" || srv.last().Header.Get("X-Actor") != c.want {
				t.Errorf("stdout = %q, X-Actor = %q, want %q", out, srv.last().Header.Get("X-Actor"), c.want)
			}
		})
	}
}

func TestHookSessionStartHookActorCaseAndEmptyConfig(t *testing.T) {
	t.Run("case-insensitive hook_actors lookup", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			stored string
			tool   string
		}{
			{"capitalized key in config", "Claude", "claude"},
			{"uppercase tool flag", "claude", "CLAUDE"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h, srv := newHookActorHarness(t, Config{HookActors: map[string]string{tc.stored: "reviewer"}})
				out := runHook(t, h, hookInput(t, hookRepo(t, "CLI")), "--tool", tc.tool)
				if !strings.HasPrefix(out, "확인하지 않은 메시지 2건 (프로젝트 CLI)\n") {
					t.Errorf("stdout = %q", out)
				}
				if got := srv.last().Header.Get("X-Actor"); got != "reviewer" {
					t.Errorf("X-Actor = %q, want reviewer", got)
				}
			})
		}
	})

	t.Run("empty hook_actors emits one-line notice regardless of token", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			hasToken bool
			args     []string
			want     string
		}{
			{
				name:     "without token and with tool",
				hasToken: false,
				args:     []string{"--tool", "Claude"},
				want:     "agb: 프로젝트 CLI의 미확인 메시지를 확인하지 못했습니다(훅 행위자 미설정). `agb setup claude --actor <ID>`로 설정하세요.\n",
			},
			{
				name:     "with shared token and with tool",
				hasToken: true,
				args:     []string{"--tool", "claude"},
				want:     "agb: 프로젝트 CLI의 미확인 메시지를 확인하지 못했습니다(훅 행위자 미설정). `agb setup claude --actor <ID>`로 설정하세요.\n",
			},
			{
				name:     "without token and without tool",
				hasToken: false,
				args:     nil,
				want:     "agb: 프로젝트 CLI의 미확인 메시지를 확인하지 못했습니다(행위자 미설정). --actor 또는 `agb setup <도구> --actor <ID>`로 설정하세요.\n",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h, srv := newHookActorHarness(t, Config{HookActors: map[string]string{}})
				if !tc.hasToken {
					delete(h.env, "AGB_TOKEN")
				}
				out := runHook(t, h, hookInput(t, hookRepo(t, "CLI")), tc.args...)
				if out != tc.want || len(srv.reqs) != 0 {
					t.Errorf("stdout = %q (want %q), reqs=%d", out, tc.want, len(srv.reqs))
				}
			})
		}
	})

	t.Run("empty hook_actors stays silent when repo config or url is missing", func(t *testing.T) {
		h, _ := newHookActorHarness(t, Config{HookActors: map[string]string{}})
		delete(h.env, "AGB_TOKEN")
		if out := runHook(t, h, hookInput(t, t.TempDir()), "--tool", "claude"); out != "" {
			t.Errorf("without .agb.json: stdout = %q", out)
		}
		delete(h.env, "AGB_URL")
		if out := runHook(t, h, hookInput(t, hookRepo(t, "CLI")), "--tool", "claude"); out != "" {
			t.Errorf("without url: stdout = %q", out)
		}
	})

	t.Run("actor present without token stays silent", func(t *testing.T) {
		h, srv := newHookActorHarness(t, Config{HookActors: map[string]string{"claude": "reviewer"}})
		delete(h.env, "AGB_TOKEN")
		if out := runHook(t, h, hookInput(t, hookRepo(t, "CLI")), "--tool", "claude"); out != "" || len(srv.reqs) != 0 {
			t.Errorf("stdout = %q, reqs = %d", out, len(srv.reqs))
		}
	})

	t.Run("local context defaults actor to owner without missing-actor notice", func(t *testing.T) {
		h, srv := newHookActorHarness(t, Config{
			CurrentContext: "local",
			Contexts:       map[string]ContextConfig{"local": {URL: srvURLPlaceholder(t), Token: "local-shared"}},
			HookActors:     map[string]string{},
		})
		delete(h.env, "AGB_URL")
		delete(h.env, "AGB_TOKEN")
		// Point local context at fakeServer URL.
		if err := saveConfig(h.app.ConfigPath, Config{
			CurrentContext: "local",
			Contexts:       map[string]ContextConfig{"local": {URL: srv.srv.URL, Token: "local-shared"}},
			HookActors:     map[string]string{},
		}); err != nil {
			t.Fatal(err)
		}
		out := runHook(t, h, hookInput(t, hookRepo(t, "CLI")), "--tool", "claude")
		if !strings.HasPrefix(out, "확인하지 않은 메시지 2건 (프로젝트 CLI)\n") || srv.last().Header.Get("X-Actor") != "owner" {
			t.Errorf("local context: stdout=%q reqs=%+v", out, srv.reqs)
		}
	})
}

func srvURLPlaceholder(t *testing.T) string {
	t.Helper()
	return "http://127.0.0.1:1"
}

// The hook actor is for the hook alone: another command without an actor
// still has none.
func TestHookActorIsNotTheActorOfOtherCommands(t *testing.T) {
	h, srv := newHookActorHarness(t, Config{HookActors: map[string]string{"claude": "reviewer"}})
	for _, args := range [][]string{{"msg", "list", "-p", "CLI"}, {"msg", "list", "-p", "CLI", "--tool", "claude"}} {
		if code := h.run(args...); code != 2 || len(srv.reqs) != 0 {
			t.Errorf("%v: code=%d requests=%+v stderr=%s", args, code, srv.reqs, h.stderr)
		}
	}
	if code := h.run("msg", "list", "-p", "CLI"); code != 2 || !strings.Contains(h.stderr.String(), "행위자가 없습니다") {
		t.Errorf("code=%d stderr=%s", code, h.stderr)
	}
}

func TestHookSessionStartSendsTheSessionIDOfTheInput(t *testing.T) {
	cases := map[string]struct {
		id   string // the session_id member as JSON; "" leaves it out
		args []string
		want string
	}{
		"a session ID":         {id: `"0a1b2c3d-4e5f-6789-abcd-ef0123456789"`, want: "0a1b2c3d-4e5f-6789-abcd-ef0123456789"},
		"128 characters":       {id: `"` + strings.Repeat("a", 128) + `"`, want: strings.Repeat("a", 128)},
		"129 characters":       {id: `"` + strings.Repeat("a", 129) + `"`, want: "sess-1"},
		"a space":              {id: `"has space"`, want: "sess-1"},
		"empty":                {id: `""`, want: "sess-1"},
		"not a string":         {id: `12`, want: "sess-1"},
		"none":                 {want: "sess-1"},
		"the flag comes first": {id: `"fromstdin"`, args: []string{"--session-id", "fromflag"}, want: "fromflag"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h, srv := newMsgHarness(t, false)
			cwd, err := json.Marshal(hookRepo(t, "CLI"))
			if err != nil {
				t.Fatal(err)
			}
			stdin := `{"cwd":` + string(cwd) + `}`
			if c.id != "" {
				stdin = `{"session_id":` + c.id + `,"cwd":` + string(cwd) + `}`
			}
			if out := runHook(t, h, stdin, c.args...); out == "" {
				t.Fatalf("no output")
			}
			if got := srv.last().Header.Get("X-Session-Id"); got != c.want {
				t.Errorf("X-Session-Id = %q, want %q", got, c.want)
			}
		})
	}
}

func TestHookSessionStartIgnoresAByteOrderMark(t *testing.T) {
	h, srv := newMsgHarness(t, false)
	out := runHook(t, h, "\ufeff"+hookInput(t, hookRepo(t, "CLI")))
	if !strings.HasPrefix(out, "확인하지 않은 메시지 2건 (프로젝트 CLI)\n") || srv.last().Header.Get("X-Session-Id") != "s" {
		t.Errorf("stdout = %q", out)
	}
}

func TestHookSessionStartIgnoresWhatItDoesNotKnow(t *testing.T) {
	h, _ := newMsgHarness(t, false)
	for _, args := range [][]string{{"--bogus"}, {"extra"}, {"--tool"}, {"--tool", "claude", "--later-flag", "v", "extra"}} {
		if out := runHook(t, h, hookInput(t, hookRepo(t, "CLI")), args...); out == "" {
			t.Errorf("%v: no output", args)
		}
	}
}

func TestConfigShowListsTheHookActors(t *testing.T) {
	h := newHarness(t, nil, true)
	if code := h.run("config", "show"); code != 0 || fieldValue(h.stdout.String(), "hook_actors") != "(없음)" {
		t.Errorf("empty table: code=%d\n%s", code, h.stdout)
	}
	if code := h.run("config", "show", "--json"); code != 0 || !strings.Contains(h.stdout.String(), `"hook_actors":{}`) {
		t.Errorf("empty json: code=%d %s", code, h.stdout)
	}
	saveConfig(h.app.ConfigPath, Config{HookActors: map[string]string{"claude": "reviewer", "another": "base"}})
	if code := h.run("config", "show"); code != 0 || fieldValue(h.stdout.String(), "hook_actors") != "another=base, claude=reviewer" {
		t.Errorf("table: code=%d\n%s", code, h.stdout)
	}
	if code := h.run("config", "show", "--json"); code != 0 || !strings.Contains(h.stdout.String(), `"hook_actors":{"another":"base","claude":"reviewer"}`) {
		t.Errorf("json: code=%d %s", code, h.stdout)
	}
}

// A plugin newer than the CLI may name a hook the CLI lacks. Exit code 2 from a
// hook that runs before a tool call blocks the call, so the unknown hook ends
// in silence. Other command groups keep their usage error.
func TestHookIgnoresASubcommandItDoesNotKnow(t *testing.T) {
	h := newHarness(t, nil, false)
	stdin := preCommitInput(t, badRepo(t), "Bash", "git commit -m x")
	for _, args := range [][]string{
		{"hook", "future-thing"},
		{"hook", "future-thing", "--tool", "claude"},
		{"hook", "future-thing", "--bogus", "extra", "--help"},
		{"hook", "bogus", "--", "x"},
	} {
		for _, in := range []string{"", stdin, `{"cwd":`} {
			h.app.Stdin = strings.NewReader(in)
			if code := h.run(args...); code != 0 || h.stdout.Len() != 0 || h.stderr.Len() != 0 {
				t.Errorf("%v: code=%d stdout=%q stderr=%q", args, code, h.stdout, h.stderr)
			}
		}
	}
	for _, args := range [][]string{{"msg", "bogus"}, {"doc", "future-thing", "--tool", "claude"}, {"future-thing"}} {
		if code := h.run(args...); code != 2 || h.stderr.Len() == 0 {
			t.Errorf("%v: code=%d stderr=%q", args, code, h.stderr)
		}
	}
}
