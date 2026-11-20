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
	srv.on("PATCH", "/v1/sessions/claude/sess-1", canned{body: `{"location_conflicts":[]}`})
	h := newHarness(t, srv, false)
	if code := h.run("issue", "comment", "CLI-1", "--summary", "멈춤", "--repo", repo, "--body", "테스트가 RED다."); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var in struct{ Body string }
	json.Unmarshal([]byte(srv.reqs[0].Body), &in)
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

func TestLocationReposDefaultsToCurrentRepository(t *testing.T) {
	repo := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "project", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	got, err := locationRepos(nil)
	if err != nil || len(got) != 1 || got[0] != "." {
		t.Fatalf("got %v, %v; want [.]", got, err)
	}
}

func TestLocationReposFromRepoConfig(t *testing.T) {
	repo := gitRepo(t)
	cfg := `{"project":"CLI","location":{"repos":[".","sub"]}}`
	if err := os.WriteFile(filepath.Join(repo, ".agb.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(repo, "deep"))
	got, err := locationRepos(nil)
	if err != nil || len(got) != 2 || !strings.HasSuffix(filepath.ToSlash(got[1]), "/sub") || filepath.Base(got[0]) != filepath.Base(repo) {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestLocationReposFlagWinsOverConfig(t *testing.T) {
	repo := gitRepo(t)
	cfg := `{"project":"CLI","location":{"repos":["sub"]}}`
	if err := os.WriteFile(filepath.Join(repo, ".agb.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	got, err := locationRepos([]string{"other"})
	if err != nil || len(got) != 1 || got[0] != "other" {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestRepoConfigLocationValidation(t *testing.T) {
	for _, bad := range []string{
		`{"project":"CLI","location":{"repos":[]}}`,
		`{"project":"CLI","location":{"repos":[""]}}`,
		`{"project":"CLI","location":{"repos":["/abs"]}}`,
		`{"project":"CLI","location":{"dirs":["a"]}}`,
	} {
		if _, err := parseRepoConfig([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if _, err := parseRepoConfig([]byte(`{"project":"CLI","location":{"repos":[".","project"]}}`)); err != nil {
		t.Error(err)
	}
}

func TestPlainCommentIgnoresBrokenRepoConfig(t *testing.T) {
	repo := gitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".agb.json"), []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	srv := newFakeServer(t)
	srv.on("POST", "/v1/issues/CLI-1/comments", canned{status: 201, body: `{"id":1,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"s","client_host":"desk","at":"2026-10-17T05:46:06.000Z","changes":null,"summary":"","body":"x"}`})
	h := newHarness(t, srv, false)
	if code := h.run("issue", "comment", "CLI-1", "--body", "x"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
}

func TestDoneCommentWarnsOnBrokenRepoConfig(t *testing.T) {
	repo := gitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".agb.json"), []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	srv := newFakeServer(t)
	srv.on("PATCH", "/v1/issues/CLI-1", canned{status: 200, body: `{"key":"CLI-1","project":"CLI","status":"done","version":2}`})
	srv.on("POST", "/v1/issues/CLI-1/comments", canned{status: 201, body: `{"id":1,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"s","client_host":"desk","at":"2026-10-17T05:46:06.000Z","changes":null,"summary":"","body":"x"}`})
	h := newHarness(t, srv, false)
	if code := h.run("issue", "comment", "CLI-1", "--status", "done", "--body", "x"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "경고") {
		t.Errorf("no warning: %s", h.stderr)
	}
	if code := h.run("issue", "comment", "CLI-1", "--location", "--body", "x"); code == 0 {
		t.Error("--location with a broken .agb.json succeeded")
	}
}

func TestCommentLocationUpdatesSessionAndWarnsOnConflict(t *testing.T) {
	repo := gitRepo(t)
	if out, err := exec.Command("git", "-C", repo, "remote", "add", "origin", "https://example.com/acme/app.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v %s", err, out)
	}
	t.Chdir(repo)

	srv := newFakeServer(t)
	srv.on("POST", "/v1/issues/CLI-1/comments", canned{status: 201, body: `{"id":1,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"sess-1","client_host":"desk","at":"2026-10-17T05:46:06.000Z","changes":null,"summary":"중간 기록","body":"x"}`})
	srv.on("PATCH", "/v1/sessions/claude/sess-1", canned{body: `{"actor":"claude","session_id":"sess-1","named":true,"first_seen_at":"2026-11-20T00:00:00.000Z","last_seen_at":"2026-11-20T01:00:00.000Z","last_host":"desk","last_project":"CLI","parent":null,"role":"main","issue":"CLI-1","location":{"repo_remote":"https://example.com/acme/app.git","branch":"dev","worktree":"/w","head":"abc1234","dirty_count":1,"reported_at":"2026-11-20T01:00:00.000Z"},"location_conflicts":[{"actor":"codex","session_id":"s2"},{"actor":"owner","session_id":"s3"}],"stale":false,"updated_at":"2026-11-20T01:00:00.000Z"}`})

	h := newHarness(t, srv, false)
	if code := h.run("issue", "comment", "CLI-1", "--location", "--body", "진행 중"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var patchReq *recorded
	for i := range srv.reqs {
		if srv.reqs[i].Method == "PATCH" && srv.reqs[i].Path == "/v1/sessions/claude/sess-1" {
			patchReq = &srv.reqs[i]
		}
	}
	if patchReq == nil {
		t.Fatalf("no PATCH /v1/sessions/claude/sess-1 request; got %v", srv.reqs)
	}
	var payload struct {
		Location map[string]any `json:"location"`
	}
	if err := json.Unmarshal([]byte(patchReq.Body), &payload); err != nil {
		t.Fatalf("decode patch body: %v", err)
	}
	if payload.Location["repo_remote"] != "https://example.com/acme/app.git" || payload.Location["branch"] != "dev" ||
		payload.Location["worktree"] == nil || payload.Location["head"] == nil || payload.Location["dirty_count"] != float64(1) {
		t.Errorf("unexpected location payload: %+v", payload.Location)
	}
	wantWarn := "agb: 경고: 같은 저장소·브랜치를 쓰는 활성 세션이 있습니다: codex/s2, owner/s3"
	if !strings.Contains(h.stderr.String(), wantWarn) {
		t.Errorf("stderr = %q, want %q", h.stderr.String(), wantWarn)
	}
}

func TestCommentLocationIgnoresSessionPatchFailure(t *testing.T) {
	repo := gitRepo(t)
	srv := newFakeServer(t)
	srv.on("POST", "/v1/issues/CLI-1/comments", canned{status: 201, body: `{"id":1,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"sess-1","client_host":"desk","at":"2026-10-17T05:46:06.000Z","changes":null,"summary":"","body":"x"}`})
	// Intentionally leave PATCH /v1/sessions/claude/sess-1 unregistered so fakeServer returns 404.
	h := newHarness(t, srv, false)
	if code := h.run("issue", "comment", "CLI-1", "--repo", repo, "--body", "진행 중"); code != 0 {
		t.Fatalf("expected comment to succeed even on 404 session patch, got code=%d stderr=%s", code, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "경고") {
		t.Errorf("expected non-fatal warning on stderr, got %q", h.stderr.String())
	}
}

func TestIssueGetFallsBackToClaimSessionLocation(t *testing.T) {
	srv := newFakeServer(t)
	doing := strings.Replace(issueJSON, `"status":"backlog"`, `"status":"doing"`, 1)
	srv.on("GET", "/v1/issues/CLI-1", canned{body: doing})
	srv.on("GET", "/v1/issues/CLI-1/activity", canned{body: `{"items":[{"id":1,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"s","client_host":"desk","at":"2026-10-17T06:00:00.000Z","changes":null,"summary":"메모","body":"일반 댓글"}],"total":1,"limit":50,"offset":0}`})
	srv.on("GET", "/v1/issues/CLI-1/claims", canned{body: `{"items":[{"id":7,"issue":"CLI-1","actor":"codex","session_id":"s2","claimed_at":"2026-11-20T01:00:00.000Z","released_at":null,"outcome":null,"reason":null,"active":true}],"total":1,"limit":10,"offset":0}`})
	srv.on("GET", "/v1/sessions/codex/s2", canned{body: `{"actor":"codex","session_id":"s2","named":true,"first_seen_at":"2026-11-20T00:00:00.000Z","last_seen_at":"2026-11-20T01:00:00.000Z","last_host":"laptop","last_project":"CLI","parent":null,"role":"main","issue":"CLI-1","location":{"repo_remote":"https://example.com/acme/app.git","branch":"feat/x","worktree":"/work/app","head":"abc1234","dirty_count":2,"reported_at":"2026-11-20T01:30:00.000Z"},"location_conflicts":[{"actor":"claude","session_id":"sess-1"}],"stale":false,"updated_at":"2026-11-20T01:30:00.000Z","children":[]}`})

	h := newHarness(t, srv, true)
	if code := h.run("issue", "get", "CLI-1"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var claimsQueryLimit string
	for _, r := range srv.reqs {
		if r.Method == "GET" && r.Path == "/v1/issues/CLI-1/claims" {
			claimsQueryLimit = r.Query.Get("limit")
		}
	}
	if claimsQueryLimit != "10" {
		t.Errorf("claims query limit = %q, want 10", claimsQueryLimit)
	}
	out := h.stdout.String()
	for _, want := range []string{
		"== 마지막 작업 위치 (2026-11-20 10:30, codex/s2) ==",
		"다른 호스트(laptop)",
		"agb: 경고: 같은 저장소·브랜치를 쓰는 활성 세션이 있습니다: claude/sess-1",
		"/work/app",
		"https://example.com/acme/app.git",
		"feat/x (HEAD abc1234)",
		"커밋하지 않은 파일: 2개",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}

	// Missing claims/session (404) is silently ignored without failing issue get.
	srv2 := newFakeServer(t)
	srv2.on("GET", "/v1/issues/CLI-1", canned{body: doing})
	srv2.on("GET", "/v1/issues/CLI-1/activity", canned{body: `{"items":[],"total":0,"limit":50,"offset":0}`})
	h2 := newHarness(t, srv2, true)
	if code := h2.run("issue", "get", "CLI-1"); code != 0 {
		t.Fatalf("404 fallback failed: code=%d stderr=%s", code, h2.stderr)
	}
	if strings.Contains(h2.stdout.String(), "마지막 작업 위치") {
		t.Errorf("unexpected location section on 404 fallback:\n%s", h2.stdout.String())
	}
}
