package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "dev"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "first"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "wip.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCommentLocationAppendsGitState(t *testing.T) {
	repo := gitRepo(t)
	srv := newFakeServer(t)
	srv.on("POST", "/v1/issues/CLI-1/comments", canned{status: 201, body: `{"id":1,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"s","client_host":"desk","at":"2026-10-17T05:46:06.000Z","changes":null,"summary":"멈춤","body":"x"}`})
	h := newHarness(t, srv, false)
	if code := h.run("issue", "comment", "CLI-1", "--summary", "멈춤", "--repo", repo, "--body", "테스트가 RED다."); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var in struct{ Body string }
	json.Unmarshal([]byte(srv.last().Body), &in)
	for _, want := range []string{"테스트가 RED다.\n\n## 작업 위치", "- 호스트: desk", "- 브랜치: dev (HEAD ", "- 원격 추적 브랜치: 없음", "- 커밋하지 않은 파일: 1개", "`?? wip.go`"} {
		if !strings.Contains(in.Body, want) {
			t.Errorf("missing %q in\n%s", want, in.Body)
		}
	}
	if code := h.run("issue", "comment", "CLI-1", "--repo", t.TempDir(), "--body", "x"); code != 1 || !strings.Contains(h.stderr.String(), "git 저장소가 아닙니다") {
		t.Errorf("not a repo: code=%d stderr=%s", code, h.stderr)
	}
}

func TestIssueGetShowsLatestLocationOfDoingIssue(t *testing.T) {
	srv := newFakeServer(t)
	doing := strings.Replace(issueJSON, `"status":"backlog"`, `"status":"doing"`, 1)
	srv.on("GET", "/v1/issues/CLI-1", canned{body: doing})
	srv.on("GET", "/v1/issues/CLI-1/activity", canned{body: `{"items":[
{"id":3,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"s","client_host":"laptop","at":"2026-10-17T06:00:00.000Z","changes":null,"summary":"메모","body":"그냥 메모"},
{"id":2,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"s","client_host":"laptop","at":"2026-10-17T05:50:00.000Z","changes":null,"summary":"멈춤","body":"멈춘다.\n\n## 작업 위치\n\n- 호스트: laptop\n\n### /w/repo\n\n- 브랜치: feat/x (HEAD abc1234)\n"},
{"id":1,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"s","client_host":"laptop","at":"2026-10-17T05:40:00.000Z","changes":null,"summary":"옛것","body":"## 작업 위치\n\n- 호스트: old\n"}],"total":3,"limit":50,"offset":0}`})
	h := newHarness(t, srv, true)
	if code := h.run("issue", "get", "CLI-1"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	out := h.stdout.String()
	for _, want := range []string{"== 마지막 작업 위치 (2026-10-17 14:50, claude) ==", "다른 호스트(laptop)", "feat/x (HEAD abc1234)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "호스트: old") {
		t.Errorf("older location shown:\n%s", out)
	}
	h = newHarness(t, srv, false)
	if code := h.run("issue", "get", "CLI-1"); code != 0 || strings.Contains(h.stdout.String(), "작업 위치") {
		t.Errorf("json mode: code=%d out=%s", code, h.stdout)
	}
}
