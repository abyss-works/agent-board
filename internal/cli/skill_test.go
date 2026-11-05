package cli

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/skills"
)

func embeddedSkill(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(skills.FS, "tool-agb/"+name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSkillShowPrintsEmbeddedSkill(t *testing.T) {
	h := newHarness(t, nil, false)
	if code := h.run("skill", "show"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if h.stdout.String() != embeddedSkill(t, "SKILL.md") {
		t.Errorf("show output differs from the embedded SKILL.md")
	}
}

func TestSkillInstallWritesFilesAndIsIdempotent(t *testing.T) {
	h := newHarness(t, nil, false)
	dir := t.TempDir()
	if code := h.run("skill", "install", "--dir", dir); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	target := filepath.Join(dir, "tool-agb")
	for _, name := range []string{"SKILL.md", "references/setup.md"} {
		if readFile(t, filepath.Join(target, filepath.FromSlash(name))) != embeddedSkill(t, name) {
			t.Errorf("%s differs from the embedded file", name)
		}
	}
	if v := readFile(t, filepath.Join(target, ".version")); v != "t\n" {
		t.Errorf(".version = %q", v)
	}
	if !strings.Contains(h.stdout.String(), "설치했습니다") || !strings.Contains(h.stdout.String(), "이전 없음") {
		t.Errorf("first install output = %q", h.stdout)
	}

	if code := h.run("skill", "install", "--dir", dir); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if !strings.Contains(h.stdout.String(), "변경 없음") {
		t.Errorf("second install output = %q", h.stdout)
	}
}

func TestSkillInstallReplacesChangedCopy(t *testing.T) {
	h := newHarness(t, nil, false)
	dir := t.TempDir()
	target := filepath.Join(dir, "tool-agb")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"SKILL.md": "old", "stale.md": "gone", ".version": "v0.4.0\n"} {
		if err := os.WriteFile(filepath.Join(target, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if code := h.run("skill", "install", "--dir", dir); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if readFile(t, filepath.Join(target, "SKILL.md")) != embeddedSkill(t, "SKILL.md") {
		t.Error("SKILL.md was not replaced")
	}
	if _, err := os.Stat(filepath.Join(target, "stale.md")); !os.IsNotExist(err) {
		t.Errorf("stale file remains: %v", err)
	}
	if !strings.Contains(h.stdout.String(), "이전 v0.4.0") || !strings.Contains(h.stdout.String(), "새 t") {
		t.Errorf("output = %q", h.stdout)
	}
}

// link makes target a link to dst: a junction on Windows, which needs no
// privilege, and a symbolic link elsewhere.
func link(t *testing.T, dst, target string) {
	t.Helper()
	var err error
	if runtime.GOOS == "windows" {
		var out []byte
		out, err = exec.Command("cmd", "/c", "mklink", "/J", target, dst).CombinedOutput()
		if err != nil {
			t.Fatalf("mklink: %v %s", err, out)
		}
		return
	}
	if err = os.Symlink(dst, target); err != nil {
		t.Fatal(err)
	}
}

func TestSkillInstallReplacesLinkAndKeepsItsTarget(t *testing.T) {
	h := newHarness(t, nil, false)
	repo := filepath.Join(t.TempDir(), "repo-skill")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "SKILL.md"), []byte("repo copy"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "tool-agb")
	link(t, repo, target)

	if code := h.run("skill", "install", "--dir", dir); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !fi.IsDir() {
		t.Errorf("target is still a link: mode %v", fi.Mode())
	}
	if readFile(t, filepath.Join(target, "SKILL.md")) != embeddedSkill(t, "SKILL.md") {
		t.Error("installed SKILL.md differs from the embedded file")
	}
	if readFile(t, filepath.Join(repo, "SKILL.md")) != "repo copy" {
		t.Error("the link's target was changed")
	}
	if !strings.Contains(h.stdout.String(), "이전 링크") {
		t.Errorf("output = %q", h.stdout)
	}
}

func TestSkillInstallNeedsDirWhenHomeIsUnknown(t *testing.T) {
	h := newHarness(t, nil, false)
	h.app.SkillsDir = ""
	if code := h.run("skill", "install"); code != 2 {
		t.Fatalf("code=%d, want 2", code)
	}
	if !strings.Contains(h.stderr.String(), "--dir") {
		t.Errorf("stderr = %q", h.stderr)
	}
}
