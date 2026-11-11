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
func newSetupHarness(t *testing.T) (*harness, *fakeClaude) {
	t.Helper()
	h := newHarness(t, nil, false)
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
	for _, want := range []string{"claude plugin marketplace add abyss-works/agent-board#main", "claude plugin install agb@agent-board", "~/.claude/skills"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("setup claude --help lacks %q:\n%s", want, h.stdout)
		}
	}
	if !strings.Contains(Usage, "  setup ") {
		t.Errorf("the top-level help lacks setup")
	}
}
