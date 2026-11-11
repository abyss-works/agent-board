package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeClaude stands for the claude executable: it records the argument lists
// and fails the call numbered failAt, counting from 1, with failOutput.
type fakeClaude struct {
	calls      [][]string
	failAt     int
	failOutput string
	onCall     func()
}

func (f *fakeClaude) run(ctx context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if f.onCall != nil {
		f.onCall()
	}
	if len(f.calls) == f.failAt {
		return []byte(f.failOutput), errors.New("exit status 1")
	}
	return []byte("ok " + args[len(args)-1] + "\n"), nil
}

// newSetupHarness has a home of its own: the default skills folder and the
// skills folder of Claude Code are two folders below it, neither made yet.
// The working folder has no .agb.json above it.
func newSetupHarness(t *testing.T) (*harness, *fakeClaude) {
	t.Helper()
	t.Chdir(t.TempDir())
	return withFakeClaude(t, newHarness(t, nil, false))
}

// withFakeClaude gives h a home of its own, a fake claude executable and a
// config file with what a hook needs besides its actor: the address of the
// harness's server, or one that nothing listens on, and a token of the actor
// claude.
func withFakeClaude(t *testing.T, h *harness) (*harness, *fakeClaude) {
	t.Helper()
	url := h.env["AGB_URL"]
	if url == "" {
		url = "http://127.0.0.1:1"
	}
	if err := saveConfig(h.app.ConfigPath, Config{URL: url, Tokens: map[string]string{"claude": "agb_file-token-of-claude"}}); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	h.app.SkillsDir = filepath.Join(home, ".agents", "skills")
	h.app.ClaudeSkillsDir = filepath.Join(home, ".claude", "skills")
	f := &fakeClaude{}
	h.app.RunClaude = f.run
	return h, f
}

func installed(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "tool-agb", "SKILL.md"))
	return err == nil
}

var setupClaudeCalls = [][]string{
	{"plugin", "marketplace", "add", "abyss-works/agent-board#main"},
	{"plugin", "install", "agb@agent-board"},
}

func TestSetupClaudeRunsTheStepsInOrder(t *testing.T) {
	h, f := newSetupHarness(t)
	f.onCall = func() {
		if installed(h.app.SkillsDir) || installed(h.app.ClaudeSkillsDir) {
			t.Errorf("the skill was installed before call %d of claude", len(f.calls))
		}
	}
	if code := h.run("setup", "claude"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if !reflect.DeepEqual(f.calls, setupClaudeCalls) {
		t.Errorf("claude calls = %v", f.calls)
	}
	// The folder Claude Code reads is another one than the default, so both get the skill.
	if !installed(h.app.SkillsDir) || !installed(h.app.ClaudeSkillsDir) {
		t.Errorf("installed: default=%v claude=%v", installed(h.app.SkillsDir), installed(h.app.ClaudeSkillsDir))
	}
	out := h.stdout.String()
	if strings.Count(out, "설치했습니다") != 2 || !strings.Contains(out, "ok abyss-works/agent-board#main") || !strings.Contains(out, "ok agb@agent-board") {
		t.Errorf("stdout = %s", out)
	}

	// A second run succeeds, runs the same steps and leaves the copies as they are.
	f.calls, f.onCall = nil, nil
	if code := h.run("setup", "claude"); code != 0 {
		t.Fatalf("second run: code=%d stderr=%s", code, h.stderr)
	}
	if !reflect.DeepEqual(f.calls, setupClaudeCalls) || strings.Count(h.stdout.String(), "변경 없음") != 2 {
		t.Errorf("second run: calls=%v stdout=%s", f.calls, h.stdout)
	}

	// A copy in the folder of Claude Code that went stale is brought up to date.
	stale := filepath.Join(h.app.ClaudeSkillsDir, "tool-agb", "SKILL.md")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := h.run("setup", "claude"); code != 0 || readFile(t, stale) != embeddedSkill(t, "SKILL.md") {
		t.Errorf("stale copy: code=%d stderr=%s", code, h.stderr)
	}
}

func TestSetupClaudeInstallsOnceWhenBothFoldersAreOne(t *testing.T) {
	h, _ := newSetupHarness(t)
	if err := os.MkdirAll(h.app.SkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(h.app.ClaudeSkillsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	link(t, h.app.SkillsDir, h.app.ClaudeSkillsDir)
	if code := h.run("setup", "claude"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if n := strings.Count(h.stdout.String(), "설치했습니다"); n != 1 || strings.Contains(h.stdout.String(), "변경 없음") {
		t.Errorf("stdout = %s", h.stdout)
	}
	if !installed(h.app.SkillsDir) {
		t.Errorf("the skill is not installed")
	}
}

func TestSetupClaudeKeepsALinkedSkillInTheClaudeFolder(t *testing.T) {
	h, _ := newSetupHarness(t)
	other := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "SKILL.md"), []byte("linked copy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.app.ClaudeSkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(h.app.ClaudeSkillsDir, "tool-agb")
	link(t, other, target)
	if code := h.run("setup", "claude"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if fi, err := os.Lstat(target); err != nil || fi.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
		t.Errorf("the link was replaced: %v %v", fi, err)
	}
	if readFile(t, filepath.Join(other, "SKILL.md")) != "linked copy" || !installed(h.app.SkillsDir) {
		t.Errorf("the link's target changed or the default folder lacks the skill")
	}
}

func TestSetupClaudeWithoutClaudeInstallsNothing(t *testing.T) {
	h, _ := newSetupHarness(t)
	h.app.RunClaude = func(ctx context.Context, args ...string) ([]byte, error) {
		return nil, &exec.Error{Name: "claude", Err: exec.ErrNotFound}
	}
	if code := h.run("setup", "claude"); code != 2 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	for _, want := range []string{"claude", "PATH", "agb setup claude"} {
		if !strings.Contains(h.stderr.String(), want) {
			t.Errorf("stderr lacks %q: %s", want, h.stderr)
		}
	}
	if installed(h.app.SkillsDir) || installed(h.app.ClaudeSkillsDir) {
		t.Errorf("the skill was installed without claude")
	}

	// The same holds for the real lookup when no folder of PATH has claude.
	h.app.RunClaude = nil
	t.Setenv("PATH", t.TempDir())
	if code := h.run("setup", "claude"); code != 2 || installed(h.app.SkillsDir) {
		t.Errorf("PATH without claude: code=%d stderr=%s", code, h.stderr)
	}
}

func TestSetupClaudeStopsAtTheStepThatFails(t *testing.T) {
	for failAt, step := range map[int]string{1: "마켓플레이스 등록", 2: "플러그인 설치"} {
		h, f := newSetupHarness(t)
		f.failAt, f.failOutput = failAt, "✘ boom\n"
		if code := h.run("setup", "claude"); code != 1 {
			t.Fatalf("%s: code=%d stderr=%s", step, code, h.stderr)
		}
		if !strings.Contains(h.stderr.String(), step) || !strings.Contains(h.stderr.String(), "✘ boom") {
			t.Errorf("%s: stderr = %s", step, h.stderr)
		}
		if len(f.calls) != failAt || installed(h.app.SkillsDir) || installed(h.app.ClaudeSkillsDir) {
			t.Errorf("%s: calls=%v, skill installed=%v", step, f.calls, installed(h.app.SkillsDir))
		}
	}
}

func TestSetupUsage(t *testing.T) {
	h, f := newSetupHarness(t)
	for _, args := range [][]string{{"setup"}, {"setup", "codex"}, {"setup", "claude", "extra"}} {
		if code := h.run(args...); code != 2 || h.stderr.Len() == 0 {
			t.Errorf("%v: code=%d stderr=%q", args, code, h.stderr)
		}
	}
	// Without a home folder there is nowhere to put the skill, so nothing runs.
	h.app.SkillsDir = ""
	if code := h.run("setup", "claude"); code != 2 {
		t.Errorf("no home: code=%d", code)
	}
	if len(f.calls) != 0 {
		t.Errorf("claude calls = %v", f.calls)
	}
	if code := h.run("setup", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "claude") {
		t.Errorf("setup --help: code=%d out=%s", code, h.stdout)
	}
	if code := h.run("setup", "claude", "--help"); code != 0 {
		t.Errorf("setup claude --help: code=%d", code)
	}
	for _, want := range []string{"claude plugin marketplace add abyss-works/agent-board#main", "claude plugin install agb@agent-board", "~/.claude/skills", "--actor", "hook_actors", "훅 점검"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("setup claude --help lacks %q:\n%s", want, h.stdout)
		}
	}
	if !strings.Contains(Usage, "  setup ") {
		t.Errorf("the top-level help lacks setup")
	}
}

// ranNothing reports whether a run of agb setup claude stopped before its
// first step: no call of claude, no skill, no hook actor.
func ranNothing(h *harness, f *fakeClaude) bool {
	cfg, err := loadConfig(h.app.ConfigPath)
	return len(f.calls) == 0 && !installed(h.app.SkillsDir) && !installed(h.app.ClaudeSkillsDir) && err == nil && cfg.HookActors == nil
}

func TestSetupClaudeNeedsAnActor(t *testing.T) {
	h, f := newSetupHarness(t)
	delete(h.env, "AGB_ACTOR")
	if code := h.run("setup", "claude"); code != 2 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	for _, want := range []string{"--actor <ID>", "AGB_ACTOR"} {
		if !strings.Contains(h.stderr.String(), want) {
			t.Errorf("stderr lacks %q: %s", want, h.stderr)
		}
	}
	if !ranNothing(h, f) {
		t.Errorf("a step ran without an actor: calls=%v", f.calls)
	}
	if code := h.run("setup", "claude", "--actor", "Not An ID"); code != 2 || !ranNothing(h, f) {
		t.Errorf("malformed actor: code=%d calls=%v", code, f.calls)
	}
}

// The hook runs without the environment of the shell that ran the setup, so
// a token or an address that only the environment holds does not count.
func TestSetupClaudeNeedsATokenInTheConfigFile(t *testing.T) {
	h, f := newSetupHarness(t)
	saveConfig(h.app.ConfigPath, Config{URL: "http://127.0.0.1:1"})
	if code := h.run("setup", "claude"); code != 2 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	for _, want := range []string{"AGB_TOKEN", "agb login --actor claude"} {
		if !strings.Contains(h.stderr.String(), want) {
			t.Errorf("stderr lacks %q: %s", want, h.stderr)
		}
	}
	if !ranNothing(h, f) {
		t.Errorf("a step ran without a token in the file: calls=%v", f.calls)
	}
	// The token of another actor does not count; the actor's own does, and so does the shared token.
	saveConfig(h.app.ConfigPath, Config{URL: "http://127.0.0.1:1", Tokens: map[string]string{"reviewer": "agb_token-of-reviewer"}})
	if code := h.run("setup", "claude"); code != 2 || !ranNothing(h, f) {
		t.Errorf("token of another actor: code=%d calls=%v", code, f.calls)
	}
	if code := h.run("setup", "claude", "--actor", "reviewer"); code != 0 || len(f.calls) != 2 {
		t.Errorf("token of the actor: code=%d calls=%v stderr=%s", code, f.calls, h.stderr)
	}
	saveConfig(h.app.ConfigPath, Config{URL: "http://127.0.0.1:1", Token: "shared-token-value"})
	if code := h.run("setup", "claude"); code != 0 {
		t.Errorf("shared token: code=%d stderr=%s", code, h.stderr)
	}
}

func TestSetupClaudeNeedsTheAddressInTheConfigFile(t *testing.T) {
	h, f := newSetupHarness(t)
	h.env["AGB_URL"] = "http://127.0.0.1:1"
	saveConfig(h.app.ConfigPath, Config{Tokens: map[string]string{"claude": "agb_file-token-of-claude"}})
	for _, args := range [][]string{{"setup", "claude"}, {"setup", "claude", "--url", "http://127.0.0.1:1"}} {
		if code := h.run(args...); code != 2 {
			t.Fatalf("%v: code=%d stderr=%s", args, code, h.stderr)
		}
		for _, want := range []string{"AGB_URL", "agb config set url"} {
			if !strings.Contains(h.stderr.String(), want) {
				t.Errorf("%v: stderr lacks %q: %s", args, want, h.stderr)
			}
		}
		if !ranNothing(h, f) {
			t.Errorf("%v: a step ran without an address in the file: calls=%v", args, f.calls)
		}
	}
}

// The check asks what the hook will ask: with the address, the token and the
// session ID that the config file gives, whatever the environment holds.
func TestSetupClaudeChecksTheHookWithoutTheEnvironment(t *testing.T) {
	mh, srv := newMsgHarness(t, false)
	h, _ := withFakeClaude(t, mh)
	other := newFakeServer(t)
	other.on("GET", "/v1/projects/CLI/messages", canned{body: `{"items":[],"total":0,"limit":50,"offset":0}`})
	h.env["AGB_URL"] = other.srv.URL
	t.Chdir(hookRepo(t, "CLI"))
	if code := h.run("setup", "claude", "--url", other.srv.URL, "--session-id", "fromflag"); code != 0 || h.stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if !strings.Contains(h.stdout.String(), "훅 점검: 프로젝트 CLI의 확인하지 않은 메시지 2건\n") {
		t.Errorf("stdout = %s", h.stdout)
	}
	if len(other.reqs) != 0 || len(srv.reqs) != 1 {
		t.Fatalf("requests: config file's server %d, environment's server %d", len(srv.reqs), len(other.reqs))
	}
	req := srv.last()
	if got := req.Header.Get("Authorization"); got != "Bearer agb_file-token-of-claude" {
		t.Errorf("Authorization = %q", got)
	}
	if got := req.Header.Get("X-Session-Id"); got != defaultSessionID(noon) {
		t.Errorf("X-Session-Id = %q", got)
	}
	if got := req.Header.Get("X-Actor"); got != "claude" {
		t.Errorf("X-Actor = %q", got)
	}
}

func TestSetupClaudeSavesTheHookActor(t *testing.T) {
	h, f := newSetupHarness(t)
	t.Chdir(t.TempDir())
	before := Config{
		URL: "http://127.0.0.1:1", Token: "shared-token-value", Tokens: map[string]string{"reviewer": "agb_token-of-reviewer"},
		SessionID: "keep", ClientHost: "desk2", HookActors: map[string]string{"another": "base"},
	}
	if err := saveConfig(h.app.ConfigPath, before); err != nil {
		t.Fatal(err)
	}
	f.onCall = func() {
		if cfg, _ := loadConfig(h.app.ConfigPath); !reflect.DeepEqual(cfg, before) {
			t.Errorf("the config file changed before call %d of claude: %+v", len(f.calls), cfg)
		}
	}
	if code := h.run("setup", "claude"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	want := before
	want.HookActors = map[string]string{"another": "base", "claude": "claude"}
	if cfg, err := loadConfig(h.app.ConfigPath); err != nil || !reflect.DeepEqual(cfg, want) {
		t.Errorf("config = %+v (%v)", cfg, err)
	}
	if !strings.Contains(h.stdout.String(), "훅 행위자 저장: claude → claude: ") {
		t.Errorf("stdout = %s", h.stdout)
	}

	// Another run replaces the hook actor.
	f.onCall = nil
	if code := h.run("setup", "claude", "--actor", "reviewer"); code != 0 {
		t.Fatalf("second run: code=%d stderr=%s", code, h.stderr)
	}
	want.HookActors = map[string]string{"another": "base", "claude": "reviewer"}
	if cfg, err := loadConfig(h.app.ConfigPath); err != nil || !reflect.DeepEqual(cfg, want) {
		t.Errorf("second run: config = %+v (%v)", cfg, err)
	}
	if !strings.Contains(h.stdout.String(), "훅 행위자 저장: claude → reviewer: ") {
		t.Errorf("second run: stdout = %s", h.stdout)
	}
}

// A step that fails leaves the hook actor as it was.
func TestSetupClaudeKeepsTheHookActorWhenAStepFails(t *testing.T) {
	h, f := newSetupHarness(t)
	f.failAt = 2
	if code := h.run("setup", "claude"); code != 1 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if cfg, err := loadConfig(h.app.ConfigPath); err != nil || cfg.HookActors != nil {
		t.Errorf("config = %+v (%v)", cfg, err)
	}
}

func TestSetupClaudeChecksTheHook(t *testing.T) {
	cases := map[string]struct {
		setup func(t *testing.T, srv *fakeServer)
		repo  string // the project of the working folder; "" for a folder without one
		want  string
	}{
		"messages":    {repo: "CLI", want: "훅 점검: 프로젝트 CLI의 확인하지 않은 메시지 2건\n"},
		"no message":  {repo: "EMP", want: "훅 점검: 프로젝트 EMP의 확인하지 않은 메시지 0건\n"},
		"no project":  {want: "훅 점검: 이 폴더에서는 프로젝트를 판정할 수 없습니다\n"},
		"unreachable": {repo: "CLI", setup: func(t *testing.T, srv *fakeServer) { srv.srv.Close() }, want: "훅 점검: 서버에 닿지 않습니다\n"},
		"server error": {repo: "CLI", setup: func(t *testing.T, srv *fakeServer) {
			srv.on("GET", "/v1/projects/CLI/messages", canned{status: 401, body: errJSON})
		}, want: "훅 점검: 서버가 오류를 돌려주었습니다 (401"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			mh, srv := newMsgHarness(t, false)
			h, _ := withFakeClaude(t, mh)
			if c.repo != "" {
				t.Chdir(hookRepo(t, c.repo))
			}
			if c.setup != nil {
				c.setup(t, srv)
			}
			if code := h.run("setup", "claude"); code != 0 || h.stderr.Len() != 0 {
				t.Fatalf("code=%d stderr=%s", code, h.stderr)
			}
			if out := h.stdout.String(); strings.Count(out, "훅 점검: ") != 1 || !strings.Contains(out, c.want) {
				t.Errorf("stdout = %s", out)
			}
			// The check asks as the actor it saved and writes none of the messages.
			if strings.Contains(h.stdout.String(), "agb msg ack") {
				t.Errorf("stdout holds the hook text: %s", h.stdout)
			}
			if c.repo != "" && c.setup == nil && srv.last().Header.Get("X-Actor") != "claude" {
				t.Errorf("X-Actor = %q", srv.last().Header.Get("X-Actor"))
			}
		})
	}
}

// The hook reads the default config file alone, so a setup against another
// file would save and check what the hook never sees.
func TestSetupClaudeRefusesAnotherConfigFile(t *testing.T) {
	h, f := newSetupHarness(t)
	other := filepath.Join(t.TempDir(), "config.json")
	full := Config{URL: "http://127.0.0.1:1", Tokens: map[string]string{"claude": "agb_file-token-of-claude"}}
	if err := saveConfig(other, full); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"setup", "claude", "--config", other}, {"--config", other, "setup", "claude"}, {"setup", "claude", "--config", h.app.ConfigPath}} {
		if code := h.run(args...); code != 2 {
			t.Fatalf("%v: code=%d stderr=%s", args, code, h.stderr)
		}
		if !strings.Contains(h.stderr.String(), "--config") {
			t.Errorf("%v: stderr = %s", args, h.stderr)
		}
		if cfg, err := loadConfig(other); !ranNothing(h, f) || err != nil || !reflect.DeepEqual(cfg, full) {
			t.Errorf("%v: a step ran or a config file changed: calls=%v other=%+v (%v)", args, f.calls, cfg, err)
		}
	}
}
