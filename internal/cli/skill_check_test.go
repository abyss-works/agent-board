package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installedSkill installs the embedded skill under a fresh folder and returns
// the folder that holds tool-agb.
func installedSkill(t *testing.T, h *harness) string {
	t.Helper()
	dir := t.TempDir()
	if code := h.run("skill", "install", "--dir", dir); code != 0 {
		t.Fatalf("install: code=%d stderr=%s", code, h.stderr)
	}
	return dir
}

func healthServer(t *testing.T, body string) *fakeServer {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/health", canned{body: body})
	return srv
}

const healthWithRelease = `{"status":"ok","version":"git-abc1234","api_version":1,"release":"v0.8.1"}`

func TestSkillCheckAllMatch(t *testing.T) {
	h := newHarness(t, healthServer(t, healthWithRelease), false)
	h.app.Version = "v0.8.1"
	dir := installedSkill(t, h)
	if code := h.run("skill", "check", "--dir", dir); code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, h.stdout, h.stderr)
	}
	out := h.stdout.String()
	for _, w := range []string{"CLI v0.8.1", "서버 v0.8.1", "스킬: 같음"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

func TestSkillCheckCLIOlderThanServer(t *testing.T) {
	h := newHarness(t, healthServer(t, healthWithRelease), false)
	h.app.Version = "v0.7.0"
	dir := installedSkill(t, h)
	if code := h.run("skill", "check", "--dir", dir); code != 1 {
		t.Fatalf("code=%d, want 1; out=%s", code, h.stdout)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "CLI가 서버보다 오래됐습니다") || !strings.Contains(out, `go install -trimpath -ldflags "-X main.version=v0.8.1" github.com/wsk0715/agent-board/cmd/agb@main`) {
		t.Errorf("output = %s", out)
	}
}

func TestSkillCheckCLINewerThanServer(t *testing.T) {
	h := newHarness(t, healthServer(t, healthWithRelease), false)
	h.app.Version = "v0.9.0"
	dir := installedSkill(t, h)
	if code := h.run("skill", "check", "--dir", dir); code != 0 {
		t.Fatalf("code=%d, want 0; out=%s", code, h.stdout)
	}
	if !strings.Contains(h.stdout.String(), "서버가 CLI보다 오래됐습니다") {
		t.Errorf("output = %s", h.stdout)
	}
}

func TestSkillCheckSkipsVersionCompare(t *testing.T) {
	for _, c := range []struct {
		name, cli, health, want string
	}{
		{"dev build", "dev", healthWithRelease, "개발 빌드"},
		{"dev snapshot", "v0.9.0-dev.abc1234", healthWithRelease, "개발 빌드"},
		{"server without release", "v0.8.1", `{"status":"ok","version":"dev","api_version":1}`, "서버가 릴리즈를 알리지 않아"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, healthServer(t, c.health), false)
			h.app.Version = c.cli
			dir := installedSkill(t, h)
			if code := h.run("skill", "check", "--dir", dir); code != 0 {
				t.Fatalf("code=%d out=%s", code, h.stdout)
			}
			if !strings.Contains(h.stdout.String(), "판 비교를 건너뜁니다") || !strings.Contains(h.stdout.String(), c.want) {
				t.Errorf("output = %s", h.stdout)
			}
		})
	}
}

func TestSkillCheckMissingSkill(t *testing.T) {
	h := newHarness(t, healthServer(t, healthWithRelease), false)
	h.app.Version = "v0.8.1"
	if code := h.run("skill", "check", "--dir", t.TempDir()); code != 1 {
		t.Fatalf("code=%d, want 1", code)
	}
	if !strings.Contains(h.stdout.String(), "설치되어 있지 않습니다") || !strings.Contains(h.stdout.String(), "agb skill install") {
		t.Errorf("output = %s", h.stdout)
	}
}

func TestSkillCheckDifferentCopy(t *testing.T) {
	h := newHarness(t, healthServer(t, healthWithRelease), false)
	h.app.Version = "v0.8.1"
	dir := installedSkill(t, h)
	if err := os.WriteFile(filepath.Join(dir, "tool-agb", "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := h.run("skill", "check", "--dir", dir); code != 1 {
		t.Fatalf("code=%d, want 1", code)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "이 CLI에 내장된 스킬과 다릅니다") || !strings.Contains(out, "agb skill install") {
		t.Errorf("output = %s", out)
	}
	if readFile(t, filepath.Join(dir, "tool-agb", "SKILL.md")) != "old" {
		t.Error("check modified the skill")
	}
}

func TestSkillCheckLinkSameAndDifferent(t *testing.T) {
	h := newHarness(t, healthServer(t, healthWithRelease), false)
	h.app.Version = "v0.8.1"
	copyDir := installedSkill(t, h)
	repo := filepath.Join(copyDir, "tool-agb")
	dir := t.TempDir()
	link(t, repo, filepath.Join(dir, "tool-agb"))

	if code := h.run("skill", "check", "--dir", dir); code != 0 {
		t.Fatalf("same: code=%d out=%s", code, h.stdout)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "링크") || !strings.Contains(out, filepath.Base(repo)) || !strings.Contains(out, "스킬: 같음") {
		t.Errorf("same output = %s", out)
	}

	if err := os.WriteFile(filepath.Join(repo, "SKILL.md"), []byte("repo copy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := h.run("skill", "check", "--dir", dir); code != 1 {
		t.Fatalf("different: code=%d out=%s", code, h.stdout)
	}
	out = h.stdout.String()
	if !strings.Contains(out, "링크") || !strings.Contains(out, "CLI를 다시 설치하세요") || strings.Contains(out, "agb skill install") {
		t.Errorf("different output = %s", out)
	}
	if _, err := os.Lstat(filepath.Join(dir, "tool-agb")); err != nil || readFile(t, filepath.Join(repo, "SKILL.md")) != "repo copy" {
		t.Error("check changed the link or its target")
	}
}

func TestSkillCheckHealthFailureStillChecksSkill(t *testing.T) {
	h := newHarness(t, nil, false)
	h.env["AGB_URL"] = "http://127.0.0.1:1"
	dir := installedSkill(t, h)
	if code := h.run("skill", "check", "--dir", dir); code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, h.stdout, h.stderr)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "서버 확인에 실패했습니다") || !strings.Contains(out, "스킬: 같음") {
		t.Errorf("output = %s", out)
	}
}

func TestSkillCheckNeedsDirWhenHomeIsUnknown(t *testing.T) {
	h := newHarness(t, nil, false)
	h.app.SkillsDir = ""
	if code := h.run("skill", "check"); code != 2 || !strings.Contains(h.stderr.String(), "--dir") {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
}
