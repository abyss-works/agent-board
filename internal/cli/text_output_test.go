package cli

import (
	"strings"
	"testing"
)

func textRun(t *testing.T, h *harness, want int, args ...string) string {
	t.Helper()
	if code := h.run(args...); code != want {
		t.Fatalf("%v: code=%d (want %d) stdout=%s stderr=%s", args, code, want, h.stdout, h.stderr)
	}
	return h.stdout.String()
}

func TestTextListPrintsOneLinePerItem(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues", canned{body: listJSON})
	h := newHarness(t, srv, true)
	got := textRun(t, h, 0, "issue", "list", "-p", "CLI", "--fields", "key,status,title", "--text")
	if got != "CLI-1\tbacklog\t첫 이슈\n" {
		t.Errorf("out=%q", got)
	}
	// flags may come before the command, and --text forces text even on a pipe
	h = newHarness(t, srv, false)
	got = textRun(t, h, 0, "--text", "--fields", "key,title", "issue", "list")
	if got != "CLI-1\t첫 이슈\n" {
		t.Errorf("out=%q", got)
	}
}

func TestTextSingleFieldIsRawAndMultiFieldIsEscaped(t *testing.T) {
	srv := newFakeServer(t)
	body := strings.Replace(issueJSON, `"body":"## 배경\n\n본문"`, `"body":"줄1\n\"따옴표\" \\ 끝\t탭\n"`, 1)
	srv.on("GET", "/v1/issues/CLI-1", canned{body: body})
	h := newHarness(t, srv, false)
	if got := textRun(t, h, 0, "issue", "get", "CLI-1", "--fields", "body", "--text"); got != "줄1\n\"따옴표\" \\ 끝\t탭\n" {
		t.Errorf("raw=%q", got)
	}
	got := textRun(t, h, 0, "issue", "get", "CLI-1", "--fields", "key,body,version,parent,labels", "--text")
	if want := "CLI-1\t줄1\\n\"따옴표\" \\\\ 끝\\t탭\\n\t3\t\t[\"api\"]\n"; got != want {
		t.Errorf("multi=%q want %q", got, want)
	}
	// an array in raw mode prints one element per line
	if got := textRun(t, h, 0, "issue", "get", "CLI-1", "--fields", "labels", "--text"); got != "api\n" {
		t.Errorf("array=%q", got)
	}
	// a number is its JSON text
	if got := textRun(t, h, 0, "issue", "get", "CLI-1", "--fields", "version", "--text"); got != "3\n" {
		t.Errorf("number=%q", got)
	}
}

func TestTextMissingFieldKeepsTheColumn(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues", canned{body: listJSON})
	h := newHarness(t, srv, false)
	if got := textRun(t, h, 0, "issue", "list", "--fields", "key,nope,title", "--text"); got != "CLI-1\t\t첫 이슈\n" {
		t.Errorf("out=%q", got)
	}
	// JSON keeps skipping missing keys
	if got := textRun(t, h, 0, "issue", "list", "--fields", "key,nope"); !strings.Contains(got, `"items":[{"key":"CLI-1"}]`) {
		t.Errorf("json=%s", got)
	}
}

func TestTextWithoutFieldsIsAUsageError(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("PATCH", "/v1/issues/CLI-1", canned{body: issueJSON})
	h := newHarness(t, srv, false)
	textRun(t, h, 2, "issue", "list", "--text")
	if !strings.Contains(h.stderr.String(), "--text") || !strings.Contains(h.stderr.String(), "--fields") {
		t.Errorf("stderr=%s", h.stderr)
	}
	// it fails before any request, so a write is never sent
	textRun(t, h, 2, "issue", "update", "CLI-1", "--title", "x", "--text")
	if len(srv.reqs) != 0 {
		t.Errorf("reqs=%v", srv.reqs)
	}
}

func TestFieldsDotPathsInJSON(t *testing.T) {
	root := checkRepo(t)
	writeFiles(t, root, map[string]string{"history/" + freshName: decisionTemplate})
	h := newHarness(t, nil, false)
	got := strings.TrimSpace(textRun(t, h, 1, "doc", "check", "--root", root, "--fields", "files.name"))
	if got != `{"files":[{"name":"history/`+freshName+`"}]}` {
		t.Errorf("json=%s", got)
	}
}

func TestDocCheckHonorsFieldsAndTextAndKeepsExitCode(t *testing.T) {
	root := checkRepo(t)
	writeFiles(t, root, map[string]string{"history/" + freshName: decisionTemplate, "history/" + goodNewName: filledDecision})
	h := newHarness(t, nil, false)
	out := textRun(t, h, 1, "doc", "check", "--root", root, "--fields", "files.name,files.problems", "--text")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "history/"+freshName+"\t[") {
		t.Errorf("out=%q", out)
	}
	out = textRun(t, h, 1, "doc", "check", "--root", root, "--fields", "files.name", "--text")
	if out != "history/"+freshName+"\n" {
		t.Errorf("names=%q", out)
	}
	// no problems: exit 0, no lines
	good := checkRepo(t)
	writeFiles(t, good, map[string]string{"history/" + goodNewName: filledDecision})
	if out := textRun(t, h, 0, "doc", "check", "--root", good, "--fields", "files.name", "--text"); out != "" {
		t.Errorf("clean=%q", out)
	}
	if out := textRun(t, h, 0, "doc", "check", "--root", good, "--fields", "checked", "--text"); out != "1\n" {
		t.Errorf("checked=%q", out)
	}
}

func TestDocSyncHonorsFieldsAndText(t *testing.T) {
	const nA = "2026-10-19-1000-report-a-first.md"
	root := t.TempDir()
	writeFiles(t, root, map[string]string{".agb.json": syncConfig, "notes/" + nA: docText("report", "첫째")})
	commitAll(t, root)
	puts := map[string]canned{putPath(nA): {status: 201, body: serverDoc("CLI", nA, "history", "a")}}
	h := newHarness(t, docSyncServer(t, nil, puts), false)
	if out := textRun(t, h, 0, "doc", "sync", "--root", root, "--fields", "uploaded,conflicts", "--text"); out != `["`+nA+`"]`+"\t[]\n" {
		t.Errorf("out=%q", out)
	}
	h = newHarness(t, docSyncServer(t, nil, puts), false)
	if out := textRun(t, h, 0, "doc", "sync", "--root", root, "--fields", "uploaded"); strings.TrimSpace(out) != `{"uploaded":["`+nA+`"]}` {
		t.Errorf("json=%q", out)
	}
	// a terminal without flags keeps the summary
	h = newHarness(t, docSyncServer(t, nil, puts), true)
	if out := textRun(t, h, 0, "doc", "sync", "--root", root); !strings.HasPrefix(out, "uploaded 1") {
		t.Errorf("table=%q", out)
	}
}

func TestRecallHonorsFieldsAndText(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/search", canned{body: recallSearchJSON})
	srv.on("GET", "/v1/graph", canned{body: graphJSON})
	h := newHarness(t, srv, false)
	// items are the rows; dot paths reach into each hit
	if out := textRun(t, h, 0, "recall", "잠금", "--fields", "hit.key,hit.title", "--text"); out != "AGB-7\t잠금 원인\n" {
		t.Errorf("hits=%q", out)
	}
}

func TestIssueActivityTextForComments(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues/CLI-1/activity", canned{body: `{"items":[{"id":502,"kind":"commented","actor":"codex","at":"2026-10-17T07:11:21.000Z","body":"댓글 본문\n둘째 줄"}],"total":1,"limit":50,"offset":0}`})
	h := newHarness(t, srv, false)
	out := textRun(t, h, 0, "issue", "activity", "CLI-1", "--fields", "actor,at,body", "--text")
	if out != "codex\t2026-10-17T07:11:21.000Z\t댓글 본문\\n둘째 줄\n" {
		t.Errorf("out=%q", out)
	}
}

func TestTextOnNonObjectResponseIsStillAnError(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues", canned{body: `[1,2]`})
	h := newHarness(t, srv, false)
	textRun(t, h, 1, "issue", "list", "--fields", "key", "--text")
	if !strings.Contains(h.stderr.String(), "--fields") {
		t.Errorf("stderr=%s", h.stderr)
	}
}

func TestTextLinesAndSelectFieldsEdges(t *testing.T) {
	body := []byte(`{"n":null,"ok":true,"files":[{"name":"a","tags":["x","y"],"meta":{"k":1}},{"name":"b"}]}`)
	cases := []struct{ spec, want string }{
		{"files.name", "a\nb\n"},
		{"files.name,files.tags", "a\t[\"x\",\"y\"]\nb\t\n"},
		{"files.meta.k", "1\n\n"},
		{"n,ok", "\ttrue\n"},
		{"ok", "true\n"},
		{"files.name,n", "[\"a\",\"b\"]\t\n"},
	}
	for _, c := range cases {
		got, err := textLines(body, c.spec)
		if err != nil || got != c.want {
			t.Errorf("%s: got %q err %v, want %q", c.spec, got, err, c.want)
		}
	}
	if got, _ := textLines([]byte(`{"items":[],"total":0}`), "key,title"); got != "" {
		t.Errorf("empty list: %q", got)
	}
	if got, _ := selectFields(body, "files,files.name"); !strings.Contains(string(got), `"tags"`) {
		t.Errorf("a whole path keeps everything below it: %s", got)
	}
	if got, _ := selectFields(body, "files.meta.k,ok"); string(got) != `{"files":[{"meta":{"k":1}},{}],"ok":true}` {
		t.Errorf("nested json: %s", got)
	}
}
