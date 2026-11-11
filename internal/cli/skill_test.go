package cli

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// Agent tools that truncate long command output show only the start of the
// skill, so the task-to-command index must end within its first 2,000
// characters and every row must point to the section that has the details.
func TestSkillShowIndexFitsInFirstCharacters(t *testing.T) {
	h := newHarness(t, nil, false)
	if code := h.run("skill", "show"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	out := h.stdout.String()
	const heading = "## 할 일별 명령\n"
	start := strings.Index(out, heading)
	if start < 0 {
		t.Fatalf("skill show lacks the %q section", heading)
	}
	end := strings.Index(out[start+len(heading):], "\n## ")
	if end < 0 {
		t.Fatal("index section has no following section")
	}
	end += start + len(heading)
	if n := len([]rune(out[:end])); n > 2000 {
		t.Errorf("index ends at character %d, want within 2000", n)
	}
	index := out[start:end]
	for _, want := range []string{
		"agb agenda", "agb project overview", "agb issue create", "agb issue update",
		"agb issue comment", "agb issue activity", "agb doc new", "agb doc put",
		"agb doc get -p", "agb search", "--fields", "--text", "agb msg put",
		"agb msg list", "agb msg ack",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index lacks %q", want)
		}
	}
	for _, row := range strings.Split(index, "\n") {
		if !strings.HasPrefix(row, "| ") || strings.HasPrefix(row, "| 할 일 ") {
			continue
		}
		if !strings.HasSuffix(row, "절) |") && !strings.HasSuffix(row, "\") |") {
			t.Errorf("index row has no section pointer: %s", row)
		}
		pointer := row[strings.LastIndex(row, "(")+1:]
		if strings.HasSuffix(pointer, "절) |") {
			for _, n := range regexp.MustCompile(`\d+`).FindAllString(pointer, -1) {
				if !strings.Contains(out, "\n## "+n+". ") {
					t.Errorf("index row points to section %s, which skill show lacks: %s", n, row)
				}
			}
			continue
		}
		for _, q := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(pointer, -1) {
			if !strings.Contains(out, "\n## "+q[1]+"\n") && !strings.Contains(out, "\n### "+q[1]+"\n") {
				t.Errorf("index row points to section %q, which skill show lacks: %s", q[1], row)
			}
		}
	}
	if !strings.Contains(index, "agb msg put -p <KEY> --title") {
		t.Error("index shows agb msg put without the required --title")
	}
}
