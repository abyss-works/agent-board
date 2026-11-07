package cli

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDocPutSendsTheFileAsItIs(t *testing.T) {
	// CRLF, a byte order mark, markup characters, Korean text and no final newline stay as they are.
	content := "\xef\xbb\xbf---\r\nkind: decision\r\n---\r\n\t본문 <b> & é\r\n끝(개행 없음)"
	root := t.TempDir()
	writeFiles(t, root, map[string]string{".agb.json": syncConfig, "notes/" + docDecisionName: content})
	put := serverDoc("CLI", docDecisionName, "history", docSHA(content))
	srv := newFakeServer(t)
	srv.on("PUT", "/v1/projects/CLI/documents/"+docDecisionName, canned{status: 201, body: put})
	h := newHarness(t, srv, false)

	if code := h.run("doc", "put", filepath.Join(root, "notes", docDecisionName)); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	req := srv.last()
	body := decodeBody(t, req)
	if req.Method != "PUT" || req.Path != "/v1/projects/CLI/documents/"+docDecisionName || body["content"] != content || len(body) != 1 {
		t.Errorf("request = %s %s %v", req.Method, req.Path, body)
	}
	if h.stdout.String() != put+"\n" {
		t.Errorf("stdout = %q", h.stdout.String())
	}

	// A relative path is resolved against the working directory.
	t.Chdir(filepath.Join(root, "notes"))
	if code := h.run("doc", "put", docDecisionName); code != 0 {
		t.Fatalf("bare name: code=%d stderr=%s", code, h.stderr)
	}
	if body := decodeBody(t, srv.last()); body["content"] != content {
		t.Errorf("bare name request = %v", body)
	}

	// On a terminal the stored document is shown.
	ht := newHarness(t, srv, true)
	if code := ht.run("doc", "put", filepath.Join(root, "notes", docDecisionName)); code != 0 {
		t.Fatalf("terminal: code=%d stderr=%s", code, ht.stderr)
	}
	for _, want := range []string{"project:", "CLI", "name:", docDecisionName, "state:", "current", "kind:", "decision", "title:", "sha256:", docSHA(content)} {
		if !strings.Contains(ht.stdout.String(), want) {
			t.Errorf("document view lacks %q:\n%s", want, ht.stdout)
		}
	}
	if strings.Contains(ht.stdout.String(), `"project"`) {
		t.Errorf("the terminal view must not be JSON:\n%s", ht.stdout)
	}
}

func TestDocPutAppliesTheRuleAndTheFlagsWin(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{".agb.json": syncConfig, "ref/2026/survey.md": "# 조사\n", "notes/" + docReportName: docText("report", "본문")})
	commitAll(t, root)
	srv := newFakeServer(t)
	srv.on("PUT", "/v1/projects/CLI/documents/ref/2026/survey.md", canned{status: 201, body: serverDoc("CLI", docReportName, "legacy", "n")})
	srv.on("PUT", "/v1/projects/CLI/documents/"+docReportName, canned{status: 201, body: serverDoc("CLI", docReportName, "history", "n")})
	h := newHarness(t, srv, false)

	// The rule of ref/ names the document by its path and brings its defaults.
	if code := h.run("doc", "put", filepath.Join(root, "ref", "2026", "survey.md")); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	body := decodeBody(t, srv.last())
	if body["kind"] != "report" || body["imported"] != true || body["archived"] != true || len(body) != 4 {
		t.Errorf("rule body = %v", body)
	}
	// A flag wins over the rule, a false too.
	if code := h.run("doc", "put", filepath.Join(root, "ref", "2026", "survey.md"), "--kind", "decision", "--archived=false", "--title", "조사 제목"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	body = decodeBody(t, srv.last())
	if body["kind"] != "decision" || body["archived"] != false || body["imported"] != true || body["title"] != "조사 제목" {
		t.Errorf("flags over the rule = %v", body)
	}

	// Every metadata flag is sent as the field it names; lists are lists; nothing else is sent.
	if code := h.run("doc", "put", filepath.Join(root, "notes", docReportName), "--kind", "report", "--date", "2026-10-19 17:00", "--title", "제목",
		"--tag", "a", "--tag", "b", "--supersedes", "x.md", "--supersedes", "y.md", "--related", "z.md", "--issue", "AGB-1", "--issue", "AGB-2",
		"--archived", "--path", "notes/r.md"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	body = decodeBody(t, srv.last())
	want := map[string]any{"kind": "report", "date": "2026-10-19 17:00", "title": "제목", "tags": []any{"a", "b"}, "supersedes": []any{"x.md", "y.md"},
		"related": []any{"z.md"}, "issues": []any{"AGB-1", "AGB-2"}, "archived": true, "path": "notes/r.md", "content": docText("report", "본문")}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("flag body = %v\nwant %v", body, want)
	}
}

func TestDocPutWithoutAConfig(t *testing.T) {
	root := gitRepo(t)
	writeFiles(t, root, map[string]string{"notes/" + docReportName: docText("report", "본문"), "old/design.md": "# 설계\n"})
	srv := newFakeServer(t)
	srv.on("PUT", "/v1/projects/CLI/documents/"+docReportName, canned{status: 201, body: serverDoc("CLI", docReportName, "history", "n")})
	srv.on("PUT", "/v1/projects/CLI/documents/old/design.md", canned{status: 201, body: serverDoc("CLI", docReportName, "legacy", "n")})
	h := newHarness(t, srv, false)

	// The project comes from the flag, the name is the file name.
	if code := h.run("doc", "put", filepath.Join(root, "notes", docReportName), "-p", "CLI"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if body := decodeBody(t, srv.last()); len(body) != 1 {
		t.Errorf("body = %v", body)
	}
	// An imported document is named by its path from the root of the repository.
	if code := h.run("doc", "put", filepath.Join(root, "old", "design.md"), "-p", "CLI", "--imported"); code != 0 {
		t.Fatalf("imported: code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Path != "/v1/projects/CLI/documents/old/design.md" || decodeBody(t, req)["imported"] != true {
		t.Errorf("imported request = %s %s", req.Path, req.Body)
	}

	// Outside a repository there is no path to name it by.
	loose := t.TempDir()
	writeFiles(t, loose, map[string]string{"x.md": "# x\n"})
	if code := h.run("doc", "put", filepath.Join(loose, "x.md"), "-p", "CLI", "--imported"); code != 2 || !strings.Contains(h.stderr.String(), "git 저장소") {
		t.Errorf("imported outside a repository: code=%d stderr=%s", code, h.stderr)
	}
}

func TestDocPutShowsWarningsAndFollowUpsAndGuidesOnMetadataDiffers(t *testing.T) {
	const nNew = "2026-10-20-1544-report-new-one.md"
	open := docText("report", "본문") + "\n## 후속\n\n- CSP 헤더\n"
	root := t.TempDir()
	writeFiles(t, root, map[string]string{".agb.json": syncConfig, "notes/" + nNew: open, "notes/" + docReportName: docText("report", "있는 기록")})
	warned := strings.Replace(serverDoc("CLI", nNew, "history", "n"), `"updated_by":"claude"}`, `"updated_by":"claude","warnings":[{"field":"related[0]","reason":"unresolved","message":"대상이 아직 없습니다."}]}`, 1)
	srv := newFakeServer(t)
	srv.on("PUT", "/v1/projects/CLI/documents/"+nNew, canned{status: 201, body: warned})
	srv.on("PUT", "/v1/projects/CLI/documents/"+docReportName, canned{status: 409,
		body: `{"error":{"code":"conflict","message":"메타데이터가 다릅니다.","details":[{"field":"archived","reason":"metadata_differs","message":"저장된 값은 false입니다."}]}}`})
	h := newHarness(t, srv, false)
	if code := h.run("doc", "put", filepath.Join(root, "notes", nNew)); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	for _, want := range []string{"경고: related[0]: 대상이 아직 없습니다.", "후속 항목이 있습니다", "  [후속] CSP 헤더"} {
		if !strings.Contains(h.stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, h.stderr)
		}
	}
	ht := newHarness(t, srv, true)
	if code := ht.run("doc", "put", filepath.Join(root, "notes", docReportName), "--archived"); code != 1 {
		t.Fatalf("metadata_differs: code=%d", code)
	}
	for _, want := range []string{"409", "metadata_differs", "agb doc archive", "agb doc link"} {
		if !strings.Contains(ht.stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, ht.stderr)
		}
	}
}

func TestDocPutUsageErrors(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".agb.json":              syncConfig,
		"notes/" + docReportName: docText("report", "본문"),
		"notes/bad-utf8.md":      "---\nkind: report\n---\n\xff\xfe\n",
	})
	bare := t.TempDir()
	writeFiles(t, bare, map[string]string{"a.md": "# a\n"})
	srv := newFakeServer(t)
	h := newHarness(t, srv, true)
	good := filepath.Join(root, "notes", docReportName)
	for label, args := range map[string][]string{
		"no project and no config": {"doc", "put", filepath.Join(bare, "a.md")},
		"no file":                  {"doc", "put"},
		"two files":                {"doc", "put", good, good},
		"a missing file":           {"doc", "put", filepath.Join(root, "notes", "missing.md")},
		"a folder":                 {"doc", "put", filepath.Join(root, "notes")},
		"a file that is not UTF-8": {"doc", "put", filepath.Join(root, "notes", "bad-utf8.md")},
		"an unknown option":        {"doc", "put", good, "--bogus"},
		"the old location option":  {"doc", "put", good, "--location", "history"},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%s: code=%d stderr=%s", label, code, h.stderr)
		}
	}
	if len(srv.reqs) != 0 {
		t.Errorf("usage errors must not reach the server: %v", srv.paths())
	}
	if h.run("doc", "put", filepath.Join(bare, "a.md")); !strings.Contains(h.stderr.String(), "--project") || !strings.Contains(h.stderr.String(), "agb init") {
		t.Errorf("the project error must name the flag and init: %s", h.stderr)
	}
}
