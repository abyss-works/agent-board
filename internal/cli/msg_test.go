package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	msgJSON = `{"id":3,"project":"CLI","to":"reviewer","title":"배포 끝","body":"## 결과\n\n배포했습니다.\n","state":"active","age":"2h","created_at":"2026-10-18T09:00:00.000Z","created_by":"claude","created_session":"sess-1","created_host":"desk","expires_at":"2026-10-25T09:00:00.000Z","deleted_at":null,"deleted_by":null}`

	msgListJSON = `{"items":[` +
		`{"id":5,"project":"CLI","to":null,"title":"수신자 없음","state":"active","age":"5m","created_at":"2026-10-18T09:00:00.000Z","created_by":"codex","created_session":"s","created_host":"h","expires_at":"2026-10-25T09:00:00.000Z","deleted_at":null,"deleted_by":null},` +
		`{"id":3,"project":"CLI","to":"reviewer","title":"배포 끝","state":"expired","age":"9d","created_at":"2026-10-08T09:00:00.000Z","created_by":"claude","created_session":"s","created_host":"h","expires_at":"2026-10-15T09:00:00.000Z","deleted_at":null,"deleted_by":null}],` +
		`"total":2,"limit":50,"offset":0}`
)

func newMsgHarness(t *testing.T, tty bool) (*harness, *fakeServer) {
	t.Helper()
	t.Chdir(t.TempDir()) // no .agb.json above the working folder
	srv := newFakeServer(t)
	srv.on("POST", "/v1/projects/CLI/messages", canned{status: 201, body: msgJSON})
	srv.on("GET", "/v1/projects/CLI/messages", canned{body: msgListJSON})
	srv.on("GET", "/v1/projects/EMP/messages", canned{body: `{"items":[],"total":0,"limit":50,"offset":0}`})
	srv.on("GET", "/v1/messages/3", canned{body: msgJSON})
	srv.on("DELETE", "/v1/messages/3", canned{body: strings.Replace(msgJSON, `"state":"active"`, `"state":"deleted"`, 1)})
	return newHarness(t, srv, tty), srv
}

func TestMsgPutSendsGivenFields(t *testing.T) {
	h, srv := newMsgHarness(t, false)
	if code := h.run("msg", "put", "-p", "CLI", "--title", "배포 끝"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Method != "POST" || req.Path != "/v1/projects/CLI/messages" || strings.TrimSpace(req.Body) != `{"title":"배포 끝"}` {
		t.Errorf("request = %s %s %s", req.Method, req.Path, req.Body)
	}
	if h.stdout.String() != msgJSON+"\n" {
		t.Errorf("stdout = %q", h.stdout.String())
	}

	if code := h.run("msg", "put", "--project", "CLI", "--title", "t", "--to", "desk", "--ttl", "3d", "--body", "짧은 본문"); code != 0 {
		t.Fatalf("all flags: code=%d stderr=%s", code, h.stderr)
	}
	want := `{"body":"짧은 본문","title":"t","to":"desk","ttl":"3d"}`
	if got := strings.TrimSpace(srv.last().Body); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	// The TTL is passed as typed; the server judges it.
	if code := h.run("msg", "put", "-p", "CLI", "--title", "t", "--ttl", "24h"); code != 0 || !strings.Contains(srv.last().Body, `"ttl":"24h"`) {
		t.Errorf("--ttl 24h: code=%d body=%s", code, srv.last().Body)
	}
}

func TestMsgPutReadsBodyFromFileAndStdin(t *testing.T) {
	h, srv := newMsgHarness(t, false)
	file := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(file, []byte("\xef\xbb\xbf파일 본문\n둘째 줄\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := h.run("msg", "put", "-p", "CLI", "--title", "t", "--body-file", file); code != 0 {
		t.Fatalf("file: code=%d stderr=%s", code, h.stderr)
	}
	if got := strings.TrimSpace(srv.last().Body); got != `{"body":"파일 본문\n둘째 줄\n","title":"t"}` {
		t.Errorf("file body = %s", got)
	}
	h.app.Stdin = strings.NewReader("표준 입력 본문\n")
	if code := h.run("msg", "put", "-p", "CLI", "--title", "t", "--body-file", "-"); code != 0 {
		t.Fatalf("stdin: code=%d stderr=%s", code, h.stderr)
	}
	if got := strings.TrimSpace(srv.last().Body); got != `{"body":"표준 입력 본문\n","title":"t"}` {
		t.Errorf("stdin body = %s", got)
	}
	if code := h.run("msg", "put", "-p", "CLI", "--title", "t", "--body-file", filepath.Join(t.TempDir(), "none.md")); code != 2 {
		t.Errorf("missing file: code=%d", code)
	}
	if code := h.run("msg", "put", "-p", "CLI", "--title", "t", "--body", "a", "--body-file", file); code != 2 {
		t.Errorf("body and body-file: code=%d", code)
	}
}

func TestMsgPutRequiresTitleAndProject(t *testing.T) {
	h, srv := newMsgHarness(t, false)
	n := len(srv.reqs)
	for _, args := range [][]string{
		{"msg", "put", "-p", "CLI"},
		{"msg", "put", "--title", "t"},
		{"msg", "put", "-p", "CLI", "--title", "t", "extra"},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%v: code=%d", args, code)
		}
	}
	if len(srv.reqs) != n {
		t.Errorf("a usage error must not reach the server")
	}
	if code := h.run("msg", "put", "--title", "t"); code != 2 || !strings.Contains(h.stderr.String(), "--project") {
		t.Errorf("stderr = %s", h.stderr)
	}
}

func TestMsgProjectDefaultsFromRepoConfig(t *testing.T) {
	h, srv := newMsgHarness(t, false)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".agb.json"), []byte(`{"project":"CLI","documents":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	code := h.run("msg", "put", "--title", "t")
	if code != 0 || srv.last().Path != "/v1/projects/CLI/messages" {
		t.Errorf("put: code=%d path=%s stderr=%s", code, srv.last().Path, h.stderr)
	}
	if code := h.run("msg", "list"); code != 0 || srv.last().Path != "/v1/projects/CLI/messages" {
		t.Errorf("list: code=%d path=%s", code, srv.last().Path)
	}
	if code := h.run("msg", "list", "-p", "EMP"); code != 0 || srv.last().Path != "/v1/projects/EMP/messages" {
		t.Errorf("an explicit project wins: code=%d path=%s", code, srv.last().Path)
	}
}

func TestMsgListTable(t *testing.T) {
	h, srv := newMsgHarness(t, true)
	if code := h.run("msg", "list", "-p", "CLI"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Path != "/v1/projects/CLI/messages" || req.Query.Encode() != "" {
		t.Errorf("request = %s ?%s", req.Path, req.Query.Encode())
	}
	lines := strings.Split(strings.TrimRight(h.stdout.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("table:\n%s", h.stdout)
	}
	if got := strings.Fields(lines[0]); !equalStrings(got, []string{"ID", "TO", "AGE", "BY", "TITLE"}) {
		t.Errorf("header = %v", got)
	}
	if got := strings.Fields(lines[1]); !equalStrings(got, []string{"5", "-", "5m", "codex", "수신자", "없음"}) {
		t.Errorf("row 1 = %v", got)
	}

	if code := h.run("msg", "list", "-p", "CLI", "--to", "reviewer", "--all"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if q := srv.last().Query; q.Get("to") != "reviewer" || q.Get("all") != "true" {
		t.Errorf("query = %v", q)
	}
	lines = strings.Split(strings.TrimRight(h.stdout.String(), "\n"), "\n")
	if got := strings.Fields(lines[0]); !equalStrings(got, []string{"ID", "STATE", "TO", "AGE", "BY", "TITLE"}) {
		t.Errorf("header with --all = %v", got)
	}
	if got := strings.Fields(lines[2]); len(got) < 3 || got[1] != "expired" {
		t.Errorf("row 2 = %v", got)
	}

	if code := h.run("msg", "list", "-p", "EMP"); code != 0 || strings.TrimSpace(h.stdout.String()) != "메시지가 없습니다." {
		t.Errorf("empty: code=%d out=%q", code, h.stdout.String())
	}
	hj := newHarness(t, srv, false)
	if code := hj.run("msg", "list", "-p", "CLI"); code != 0 || hj.stdout.String() != msgListJSON+"\n" {
		t.Errorf("json: code=%d out=%q", code, hj.stdout.String())
	}
}

func TestMsgListFieldsText(t *testing.T) {
	h, _ := newMsgHarness(t, true)
	if code := h.run("msg", "list", "-p", "CLI", "--fields", "id,to,title,created_by,age", "--text"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	want := "5\t\t수신자 없음\tcodex\t5m\n3\treviewer\t배포 끝\tclaude\t9d\n"
	if h.stdout.String() != want {
		t.Errorf("text = %q, want %q", h.stdout.String(), want)
	}
}

func TestMsgGet(t *testing.T) {
	h, srv := newMsgHarness(t, true)
	if code := h.run("msg", "get", "3"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Method != "GET" || req.Path != "/v1/messages/3" {
		t.Errorf("request = %s %s", req.Method, req.Path)
	}
	out := h.stdout.String()
	for _, want := range []string{"title:", "배포 끝", "reviewer", "active", "2h", "claude", "desk", "2026-10-25 18:00"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "\n\n## 결과\n\n배포했습니다.\n") {
		t.Errorf("body placement:\n%s", out)
	}

	// --fields body --text prints the body as it is.
	if code := h.run("msg", "get", "3", "--fields", "body", "--text"); code != 0 || h.stdout.String() != "## 결과\n\n배포했습니다.\n" {
		t.Errorf("raw body: code=%d out=%q", code, h.stdout.String())
	}
	hj := newHarness(t, srv, false)
	if code := hj.run("msg", "get", "3"); code != 0 || hj.stdout.String() != msgJSON+"\n" {
		t.Errorf("json: code=%d out=%q", code, hj.stdout.String())
	}
	for _, args := range [][]string{{"msg", "get"}, {"msg", "get", "abc"}, {"msg", "get", "0"}, {"msg", "get", "3", "4"}} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%v: code=%d", args, code)
		}
	}
	if code := h.run("msg", "get", "99"); code != 1 {
		t.Errorf("unknown id: code=%d", code)
	}
}

func TestMsgRm(t *testing.T) {
	h, srv := newMsgHarness(t, false)
	if code := h.run("msg", "rm", "3"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Method != "DELETE" || req.Path != "/v1/messages/3" {
		t.Errorf("request = %s %s", req.Method, req.Path)
	}
	if !strings.Contains(h.stdout.String(), `"state":"deleted"`) {
		t.Errorf("stdout = %q", h.stdout.String())
	}
	ht := newHarness(t, srv, true)
	if code := ht.run("msg", "rm", "3"); code != 0 || !strings.Contains(ht.stdout.String(), "deleted") {
		t.Errorf("table: code=%d out=%q", code, ht.stdout.String())
	}
	for _, args := range [][]string{{"msg", "rm"}, {"msg", "rm", "x"}, {"msg", "rm", "3", "4"}} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%v: code=%d", args, code)
		}
	}
	if code := h.run("msg", "rm", "99"); code != 1 {
		t.Errorf("unknown id: code=%d", code)
	}
}

func TestMsgHelp(t *testing.T) {
	h, _ := newMsgHarness(t, true)
	if code := h.run("msg", "--help"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	for _, want := range []string{"put", "list", "get", "rm"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("group help lacks %q:\n%s", want, h.stdout)
		}
	}
	if code := h.run("msg", "put", "--help"); code != 0 {
		t.Fatalf("put help: code=%d", code)
	}
	for _, want := range []string{"--title", "--to", "--ttl", "--body-file", "7일", "90일"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("put help lacks %q:\n%s", want, h.stdout)
		}
	}
}

func TestMsgListPagingAndFooter(t *testing.T) {
	h, srv := newMsgHarness(t, true)
	if code := h.run("msg", "list", "-p", "CLI", "--limit", "2", "--offset", "4"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if q := srv.last().Query; q.Get("limit") != "2" || q.Get("offset") != "4" {
		t.Errorf("query = %v", q)
	}
	srv.on("GET", "/v1/projects/PAG/messages", canned{body: strings.Replace(msgListJSON, `"total":2`, `"total":9`, 1)})
	if code := h.run("msg", "list", "-p", "PAG"); code != 0 || !strings.Contains(h.stdout.String(), "9건 중 2건 표시 (offset 0)") {
		t.Errorf("truncated list: code=%d out=%s", code, h.stdout)
	}
	if code := h.run("msg", "list", "-p", "CLI"); code != 0 || strings.Contains(h.stdout.String(), "건 중") {
		t.Errorf("complete list must have no footer:\n%s", h.stdout)
	}
}
