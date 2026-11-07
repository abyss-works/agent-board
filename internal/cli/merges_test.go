package cli

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// mergedRepo is a repository whose dev branch merged feat/x (two commits)
// with --no-ff and then deleted the branch. It returns the base commit the
// branch started from and the merge commit.
func mergedRepo(t *testing.T) (dir, base, merge string) {
	t.Helper()
	dir = gitRepo(t)
	gitRun(t, dir, "remote", "add", "origin", "https://user:secret@example.com/o/r.git")
	base = gitRun(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "switch", "-q", "-c", "feat/x")
	gitRun(t, dir, "commit", "-q", "--allow-empty", "-m", "feat: 첫 변경")
	gitRun(t, dir, "commit", "-q", "--allow-empty", "-m", "feat: 둘째 변경")
	gitRun(t, dir, "switch", "-q", "dev")
	gitRun(t, dir, "commit", "-q", "--allow-empty", "-m", "fix: 다른 일")
	gitRun(t, dir, "merge", "-q", "--no-ff", "feat/x", "-m", "merge: feat/x를 dev에 병합")
	merge = gitRun(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "branch", "-q", "-D", "feat/x")
	return dir, base, merge
}

func activityJSON(bodies ...string) string {
	var items []string
	for n, b := range bodies {
		body, _ := json.Marshal(b)
		items = append(items, fmt.Sprintf(`{"id":%d,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"s","client_host":"desk","at":"2026-10-17T05:46:06.000Z","changes":null,"summary":null,"body":%s}`, 10-n, body))
	}
	return `{"items":[` + strings.Join(items, ",") + `],"total":` + fmt.Sprint(len(items)) + `,"limit":200,"offset":0}`
}

func TestDoneLinksTheMergeOfTheWorkBranch(t *testing.T) {
	dir, base, merge := mergedRepo(t)
	loc := "착수\n\n## 작업 위치\n\n- 호스트: desk\n\n### /elsewhere/r\n\n- 원격: https://example.com/o/r.git\n- 브랜치: feat/x (HEAD " + base[:7] + ")\n- 커밋하지 않은 파일: 0개\n"
	issue := strings.Replace(reasonIssueJSON, `"status":"cancelled"`, `"status":"done"`, 1)
	srv := newFakeServer(t)
	srv.on("PATCH", "/v1/issues/CLI-1", canned{body: issue})
	srv.on("GET", "/v1/issues/CLI-1/activity", canned{body: activityJSON("다른 댓글", loc)})
	srv.on("POST", "/v1/issues/CLI-1/comments", canned{status: 201, body: `{"id":11}`})
	h := newHarness(t, srv, false)
	if code := h.run("issue", "comment", "CLI-1", "--status", "done", "--repo", dir, "--body", "끝냈습니다."); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var patches []map[string]any
	var comment struct{ Body string }
	for _, r := range srv.reqs {
		switch r.Method {
		case "PATCH":
			var p map[string]any
			json.Unmarshal([]byte(r.Body), &p)
			patches = append(patches, p)
		case "POST":
			json.Unmarshal([]byte(r.Body), &comment)
		}
	}
	if len(patches) != 2 || patches[0]["status"] != "done" {
		t.Fatalf("patches = %v", patches)
	}
	if refs, _ := json.Marshal(patches[1]["refs"]); string(refs) != `["commit:`+merge+`"]` {
		t.Errorf("refs patch = %s", refs)
	}
	if v := srv.reqs[len(srv.reqs)-2].Header.Get("If-Match"); v != `"4"` {
		t.Errorf("refs patch If-Match = %q", v)
	}
	// --repo also attaches the location, so the merges come after it.
	_, merges, _ := strings.Cut(comment.Body, "\n\n## 병합\n")
	for _, want := range []string{"merge: feat/x를 dev에 병합", "feat: 첫 변경", "feat: 둘째 변경", merge[:12], "https://example.com/o/r.git"} {
		if !strings.Contains(merges, want) {
			t.Errorf("merges part misses %q:\n%s", want, comment.Body)
		}
	}
	if strings.Contains(merges, "fix: 다른 일") {
		t.Errorf("merges part lists a commit of dev itself:\n%s", comment.Body)
	}
}

func TestDoneWithoutALocationSkipsLinking(t *testing.T) {
	dir, _, _ := mergedRepo(t)
	srv := newFakeServer(t)
	srv.on("PATCH", "/v1/issues/CLI-1", canned{body: reasonIssueJSON})
	srv.on("GET", "/v1/issues/CLI-1/activity", canned{body: activityJSON("위치 없는 댓글")})
	srv.on("POST", "/v1/issues/CLI-1/comments", canned{status: 201, body: `{"id":11}`})
	h := newHarness(t, srv, false)
	if code := h.run("issue", "comment", "CLI-1", "--status", "done", "--repo", dir, "--body", "끝"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if h.stderr.Len() != 0 {
		t.Errorf("no location means no commits to link, so no warning: stderr = %s", h.stderr)
	}
	n := 0
	for _, r := range srv.reqs {
		if r.Method == "PATCH" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("only the status is patched: %d patches", n)
	}
}

func TestLocationRecordsTheRemoteWithoutCredentials(t *testing.T) {
	dir, _, _ := mergedRepo(t)
	loc, err := workLocation("desk", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loc, "- 원격: https://example.com/o/r.git\n") || strings.Contains(loc, "secret") {
		t.Errorf("location:\n%s", loc)
	}
}

func TestFindMerge(t *testing.T) {
	dir, base, merge := mergedRepo(t)
	tip := gitRun(t, dir, "rev-parse", merge+"^2")
	for name, c := range map[string]struct {
		r    locatedRepo
		want string
	}{
		"head on the work branch":       {locatedRepo{branch: "feat/y", head: tip[:7]}, merge},
		"head at the base, branch name": {locatedRepo{branch: "feat/x", head: base[:7]}, merge},
		"unknown head, branch name":     {locatedRepo{branch: "feat/x", head: "deadbee"}, merge},
		"a longer branch name only":     {locatedRepo{branch: "feat", head: base[:7]}, ""},
		"the integration branch itself": {locatedRepo{branch: "dev", head: base[:7]}, ""},
	} {
		got, ok := findMerge(dir, c.r)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("%s: findMerge = %q %v, want %q", name, got, ok, c.want)
		}
	}
	// Work that is not merged yet has no merge.
	gitRun(t, dir, "switch", "-q", "-c", "feat/z")
	gitRun(t, dir, "commit", "-q", "--allow-empty", "-m", "wip")
	open := gitRun(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "switch", "-q", "dev")
	if got, ok := findMerge(dir, locatedRepo{branch: "feat/z", head: open[:7]}); ok {
		t.Errorf("unmerged: %q", got)
	}
}
