package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const msgReadJSON = `{"message_id":3,"reader":"alpha","read_at":"2026-10-18T10:00:00.000Z","read_by":"claude","read_session":"sess-1","read_host":"desk"}`

// newMsgReadHarness is newMsgHarness with the ack route of messages 3 and 4.
func newMsgReadHarness(t *testing.T, tty bool) (*harness, *fakeServer) {
	t.Helper()
	h, srv := newMsgHarness(t, tty)
	srv.on("POST", "/v1/messages/3/ack", canned{body: msgReadJSON})
	srv.on("POST", "/v1/messages/4/ack", canned{body: strings.Replace(msgReadJSON, `"message_id":3`, `"message_id":4`, 1)})
	return h, srv
}

// chdirRepo moves into a folder below a repository whose .agb.json names project.
func chdirRepo(t *testing.T, project string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".agb.json"), []byte(`{"project":"`+project+`","documents":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
}

func TestMsgAckSendsTheReader(t *testing.T) {
	h, srv := newMsgReadHarness(t, false)
	if code := h.run("msg", "ack", "3", "--as", "alpha"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Method != "POST" || req.Path != "/v1/messages/3/ack" || strings.TrimSpace(req.Body) != `{"reader":"alpha"}` {
		t.Errorf("request = %s %s %s", req.Method, req.Path, req.Body)
	}
	if h.stdout.String() != msgReadJSON+"\n" {
		t.Errorf("stdout = %q", h.stdout.String())
	}

	// Several ids are taken in order, one line of JSON each.
	n := len(srv.reqs)
	if code := h.run("msg", "ack", "--as", "alpha", "3", "4"); code != 0 {
		t.Fatalf("two ids: code=%d stderr=%s", code, h.stderr)
	}
	if got := srv.reqs[n:]; len(got) != 2 || got[0].Path != "/v1/messages/3/ack" || got[1].Path != "/v1/messages/4/ack" {
		t.Errorf("requests = %+v", got)
	}
	if lines := strings.Split(strings.TrimSpace(h.stdout.String()), "\n"); len(lines) != 2 || !strings.Contains(lines[1], `"message_id":4`) {
		t.Errorf("stdout = %q", h.stdout.String())
	}
	// The first id that fails stops the rest.
	n = len(srv.reqs)
	if code := h.run("msg", "ack", "--as", "alpha", "99", "3"); code != 1 || len(srv.reqs) != n+1 {
		t.Errorf("unknown id first: code=%d requests=%d", code, len(srv.reqs)-n)
	}

	ht := newHarness(t, srv, true)
	if code := ht.run("msg", "ack", "3", "--as", "alpha"); code != 0 {
		t.Fatalf("table: code=%d stderr=%s", code, ht.stderr)
	}
	for _, want := range []string{"3", "alpha", "2026-10-18 19:00", "claude", "desk"} {
		if !strings.Contains(ht.stdout.String(), want) {
			t.Errorf("view lacks %q:\n%s", want, ht.stdout)
		}
	}
}

func TestMsgAckUsageErrors(t *testing.T) {
	h, srv := newMsgReadHarness(t, false)
	n := len(srv.reqs)
	for _, args := range [][]string{
		{"msg", "ack", "--as", "alpha"},
		{"msg", "ack", "abc", "--as", "alpha"},
		{"msg", "ack", "3", "0", "--as", "alpha"},
		{"msg", "ack", "3", "--as", ""},
		{"msg", "ack", "3"},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%v: code=%d", args, code)
		}
	}
	if len(srv.reqs) != n {
		t.Errorf("a usage error must not reach the server")
	}
	// Outside a repository with a .agb.json the reader has to be named.
	if code := h.run("msg", "ack", "3"); code != 2 || !strings.Contains(h.stderr.String(), "--as") {
		t.Errorf("code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("msg", "list", "-p", "CLI", "--unread"); code != 2 || !strings.Contains(h.stderr.String(), "--as") {
		t.Errorf("list --unread: code=%d stderr=%s", code, h.stderr)
	}
	if len(srv.reqs) != n {
		t.Errorf("a usage error must not reach the server")
	}
}

func TestMsgReaderDefaultsToTheRepositoryProject(t *testing.T) {
	h, srv := newMsgReadHarness(t, false)
	chdirRepo(t, "CLI")

	if code := h.run("msg", "ack", "3"); code != 0 || strings.TrimSpace(srv.last().Body) != `{"reader":"CLI"}` {
		t.Errorf("ack: code=%d body=%s stderr=%s", code, srv.last().Body, h.stderr)
	}
	if code := h.run("msg", "ack", "3", "--as", "beta"); code != 0 || strings.TrimSpace(srv.last().Body) != `{"reader":"beta"}` {
		t.Errorf("ack --as: code=%d body=%s", code, srv.last().Body)
	}
	if code := h.run("msg", "list", "--unread"); code != 0 || srv.last().Path != "/v1/projects/CLI/messages" || srv.last().Query.Get("unread_by") != "CLI" {
		t.Errorf("list --unread: code=%d %s ?%s", code, srv.last().Path, srv.last().Query.Encode())
	}
	// Another message space is still read as the project of this repository.
	if code := h.run("msg", "list", "-p", "EMP", "--unread"); code != 0 || srv.last().Path != "/v1/projects/EMP/messages" || srv.last().Query.Get("unread_by") != "CLI" {
		t.Errorf("list -p EMP --unread: code=%d %s ?%s", code, srv.last().Path, srv.last().Query.Encode())
	}
	if code := h.run("msg", "list", "-p", "EMP", "--unread", "--as", "beta", "--to", "desk", "--all"); code != 0 || srv.last().Query.Encode() != "all=true&to=desk&unread_by=beta" {
		t.Errorf("list --unread --as: code=%d ?%s", code, srv.last().Query.Encode())
	}
	// Without --unread the list asks for no reader.
	if code := h.run("msg", "list"); code != 0 || srv.last().Query.Encode() != "" {
		t.Errorf("list: code=%d ?%s", code, srv.last().Query.Encode())
	}
}

func TestMsgListRejectsAsWithoutUnread(t *testing.T) {
	h, srv := newMsgReadHarness(t, false)
	n := len(srv.reqs)
	if code := h.run("msg", "list", "-p", "CLI", "--as", "alpha"); code != 2 || !strings.Contains(h.stderr.String(), "--unread") {
		t.Errorf("code=%d stderr=%s", code, h.stderr)
	}
	if len(srv.reqs) != n {
		t.Errorf("a usage error must not reach the server")
	}
}

func TestMsgGetShowsReads(t *testing.T) {
	h, srv := newMsgHarness(t, true)
	withReads := strings.Replace(msgJSON, `"deleted_by":null}`, `"deleted_by":null,"reads":[`+msgReadJSON+`,`+
		strings.NewReplacer(`"alpha"`, `"beta"`, `"claude"`, `"codex"`).Replace(msgReadJSON)+`]}`, 1)
	srv.on("GET", "/v1/messages/3", canned{body: withReads})
	if code := h.run("msg", "get", "3"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	out := h.stdout.String()
	for _, want := range []string{"read:", "alpha  2026-10-18 19:00 (claude, desk)", "beta  2026-10-18 19:00 (codex, desk)"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "\n\n## 결과\n\n배포했습니다.\n") {
		t.Errorf("body placement:\n%s", out)
	}
	hj := newHarness(t, srv, false)
	if code := hj.run("msg", "get", "3"); code != 0 || hj.stdout.String() != withReads+"\n" {
		t.Errorf("json: code=%d out=%q", code, hj.stdout.String())
	}
}

func TestMsgReadHelp(t *testing.T) {
	h, _ := newMsgHarness(t, true)
	if code := h.run("msg", "--help"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	for _, want := range []string{"ack", "확인 표시"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("group help lacks %q:\n%s", want, h.stdout)
		}
	}
	if code := h.run("msg", "ack", "--help"); code != 0 {
		t.Fatalf("ack help: code=%d", code)
	}
	for _, want := range []string{"<ID>...", "--as", ".agb.json", "처리한 뒤"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("ack help lacks %q:\n%s", want, h.stdout)
		}
	}
	if code := h.run("msg", "list", "--help"); code != 0 {
		t.Fatalf("list help: code=%d", code)
	}
	for _, want := range []string{"--unread", "--as"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("list help lacks %q:\n%s", want, h.stdout)
		}
	}
}
