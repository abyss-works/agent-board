package cli

import (
	"os/exec"
	"strings"
	"testing"
)

// mgmtRepo is a management repository whose AGENTS.md names agb as the
// knowledge store of project CLI, with one committed record document that
// has follow-ups, one committed and later edited, and one never committed.
func mgmtRepo(t *testing.T, knowledge string) (root, committed string) {
	t.Helper()
	root = gitRepo(t)
	committed = "2026-10-20-1544-report-committed.md"
	writeFiles(t, root, map[string]string{
		"AGENTS.md":            "# AGENTS\n\n- 이슈 추적: agb, 프로젝트 키 `CLI`\n- 지식 저장소: " + knowledge + "\n",
		"history/" + committed: docText("report", "본문") + "\n## 후속\n\n- CSP 헤더\n",
		"history/2026-10-20-1545-report-edited.md": docText("report", "처음"),
		"history/_report.md":                       "서식",
	})
	for _, args := range [][]string{
		{"add", "AGENTS.md", "history"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "docs"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	writeFiles(t, root, map[string]string{
		"history/2026-10-20-1545-report-edited.md":      docText("report", "고침"),
		"history/2026-10-20-1546-report-uncommitted.md": docText("report", "초안"),
	})
	return root, committed
}

const agendaJSON = `{"projects":[]}`

func TestAgendaUploadsCommittedRecords(t *testing.T) {
	root, committed := mgmtRepo(t, "agb, 프로젝트 키 `CLI`")
	srv := docSyncServer(t, nil, map[string]canned{
		"GET /v1/agenda": {body: agendaJSON},
		"PUT /v1/projects/CLI/documents/" + committed: {status: 201, body: serverDoc("CLI", committed, "history", "x")},
	})
	t.Chdir(root)
	h := newHarness(t, srv, false)
	if code := h.run("agenda"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if h.stdout.String() != agendaJSON+"\n" {
		t.Errorf("stdout must stay the agenda: %q", h.stdout.String())
	}
	var puts []string
	for _, r := range srv.reqs {
		if r.Method == "PUT" {
			puts = append(puts, r.Path)
		}
	}
	if !equalStrings(puts, []string{"/v1/projects/CLI/documents/" + committed}) {
		t.Errorf("only committed, unchanged documents are uploaded: %v", puts)
	}
	for _, want := range []string{"기록 문서 1건을 올렸습니다(CLI): " + committed, "  [후속] CSP 헤더"} {
		if !strings.Contains(h.stderr.String(), want) {
			t.Errorf("stderr misses %q:\n%s", want, h.stderr)
		}
	}
}

func TestDoneCommentUploadsRecordsAndWarnsOnFailure(t *testing.T) {
	root, committed := mgmtRepo(t, "agb, 프로젝트 키 `CLI`")
	comment := `{"id":1,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"s","client_host":"desk","at":"2026-10-17T05:46:06.000Z","changes":null,"summary":null,"body":"x"}`
	srv := docSyncServer(t, nil, map[string]canned{
		"PATCH /v1/issues/CLI-1":                      {body: `{}`},
		"POST /v1/issues/CLI-1/comments":              {status: 201, body: comment},
		"PUT /v1/projects/CLI/documents/" + committed: {status: 500, body: `{"error":{"code":"internal","message":"서버 오류"}}`},
	})
	t.Chdir(root)
	h := newHarness(t, srv, false)
	if code := h.run("issue", "comment", "CLI-1", "--status", "done", "--body", "x"); code != 0 {
		t.Fatalf("a failed upload must not fail the comment: code=%d stderr=%s", code, h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "경고: 기록 문서를 자동으로 올리지 못했습니다") {
		t.Errorf("stderr = %s", h.stderr)
	}

	// A comment that does not close the issue does not touch the documents.
	srv.reqs = nil
	if code := h.run("issue", "comment", "CLI-1", "--status", "doing", "--body", "x"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	for _, r := range srv.reqs {
		if strings.Contains(r.Path, "/documents") {
			t.Errorf("doing comment reached %s %s", r.Method, r.Path)
		}
	}
}

func TestRecordsAreLeftAloneOutsideAgbManagedRepositories(t *testing.T) {
	for name, dir := range map[string]func(t *testing.T) string{
		"knowledge store elsewhere": func(t *testing.T) string { r, _ := mgmtRepo(t, "`docs/` 폴더"); return r },
		"not a repository":          func(t *testing.T) string { return t.TempDir() },
	} {
		t.Run(name, func(t *testing.T) {
			srv := docSyncServer(t, nil, map[string]canned{"GET /v1/agenda": {body: agendaJSON}})
			t.Chdir(dir(t))
			h := newHarness(t, srv, false)
			if code := h.run("agenda"); code != 0 || h.stderr.Len() != 0 {
				t.Errorf("code=%d stderr=%s", code, h.stderr)
			}
			if len(srv.reqs) != 1 {
				t.Errorf("requests = %v", srv.paths())
			}
		})
	}
}
