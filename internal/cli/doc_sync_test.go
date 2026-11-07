package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// syncConfig is a repository that keeps general documents in notes/ and
// imported reference documents under ref/.
const syncConfig = `{
  "project": "CLI",
  "auto_sync": true,
  "documents": [
    {"glob": "notes/*.md", "name": "base"},
    {"glob": "ref/**/*.md", "name": "path", "imported": true, "kind": "report", "archived": true}
  ],
  "follow_up_headings": ["후속"]
}`

func putPath(name string) string { return "PUT /v1/projects/CLI/documents/" + name }

func decodeBody(t *testing.T, r recorded) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
		t.Fatalf("body %q: %v", r.Body, err)
	}
	return body
}

func (s *fakeServer) putRequests() []recorded {
	var out []recorded
	for _, r := range s.reqs {
		if r.Method == "PUT" {
			out = append(out, r)
		}
	}
	return out
}

func TestDocSyncUploadsWhatTheServerLacksInNameOrder(t *testing.T) {
	const (
		nB    = "2026-10-20-1544-report-b-second.md"
		nA    = "2026-10-19-1000-report-a-first.md"
		nSame = "2026-10-18-1000-report-same.md"
		nDiff = "2026-10-17-1000-report-differs.md"
		nRef  = "ref/2026/survey.md"
	)
	same := docText("report", "같은 기록")
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".agb.json":          syncConfig,
		"notes/" + nB:        docText("report", "둘째"),
		"notes/" + nA:        docText("report", "첫째"),
		"notes/" + nSame:     same,
		"notes/" + nDiff:     docText("report", "로컬"),
		"notes/_report.md":   "서식",
		"notes/x.example.md": "서식 예",
		"notes/readme.txt":   "글",
		"elsewhere/other.md": "규칙에 없는 폴더",
		nRef:                 "# 조사\n\n본문\n",
	})
	commitAll(t, root)
	srv := docSyncServer(t, []string{
		serverDoc("CLI", nSame, "history", docSHA(same)),
		serverDoc("CLI", nDiff, "history", "0000"),
	}, map[string]canned{
		putPath(nA):   {status: 201, body: serverDoc("CLI", nA, "history", "a")},
		putPath(nB):   {status: 201, body: serverDoc("CLI", nB, "history", "b")},
		putPath(nRef): {status: 201, body: serverDoc("CLI", nRef, "history", "r")},
	})
	h := newHarness(t, srv, false)
	if code := h.run("doc", "sync", "--root", root); code != 1 {
		// A document that differs from the server's is a conflict, which ends the run with 1.
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var got syncOutput
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if want := []string{nA, nB, nRef}; !reflect.DeepEqual(got.Uploaded, want) {
		t.Errorf("uploaded = %v, want %v (by name)", got.Uploaded, want)
	}
	if !reflect.DeepEqual(got.Unchanged, []string{nSame}) || len(got.Conflicts) != 1 || got.Conflicts[0].Name != nDiff {
		t.Errorf("unchanged = %v, conflicts = %+v", got.Unchanged, got.Conflicts)
	}
	puts := srv.putRequests()
	if len(puts) != 3 {
		t.Fatalf("puts = %v", srv.puts())
	}
	// The requests follow the names, not the folders.
	var order []string
	for _, r := range puts {
		order = append(order, strings.TrimPrefix(r.Path, "/v1/projects/CLI/documents/"))
	}
	if !reflect.DeepEqual(order, []string{nA, nB, nRef}) {
		t.Errorf("order of the puts = %v", order)
	}
	// A rule that says nothing sends the content alone; the rule of ref/ sends its defaults.
	if body := decodeBody(t, puts[0]); len(body) != 1 || body["content"] != docText("report", "첫째") {
		t.Errorf("body of a plain rule = %v", body)
	}
	body := decodeBody(t, puts[2])
	if len(body) != 4 || body["kind"] != "report" || body["imported"] != true || body["archived"] != true || body["content"] != "# 조사\n\n본문\n" {
		t.Errorf("body of the ref rule = %v", body)
	}
	for _, r := range puts {
		if strings.Contains(r.Body, "location") {
			t.Errorf("a put must not carry a location: %s", r.Body)
		}
	}
}

func TestDocSyncDoesNotFixMetadata(t *testing.T) {
	// A document that is on the server with the same content is left alone, whatever
	// its metadata is: the rule may say archived while the server has it current.
	const n = "2026-10-20-1544-report-there.md"
	content := docText("report", "있는 기록")
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".agb.json":  `{"project": "CLI", "documents": [{"glob": "notes/*.md", "name": "base", "archived": true}]}`,
		"notes/" + n: content,
	})
	commitAll(t, root)
	srv := docSyncServer(t, []string{serverDoc("CLI", n, "history", docSHA(content))}, nil)
	h := newHarness(t, srv, false)
	if code := h.run("doc", "sync", "--root", root); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if len(srv.puts()) != 0 {
		t.Errorf("puts = %v", srv.puts())
	}
	var got map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, has := got["moved"]; has {
		t.Errorf("there is no moved category any more: %s", h.stdout)
	}
}

func TestDocSyncUploadsOnlyCommittedFiles(t *testing.T) {
	const (
		nCommitted = "2026-10-20-1544-decision-committed-one.md"
		nEdited    = "2026-10-20-1545-decision-edited-one.md"
		nDraft     = "2026-10-20-1546-decision-draft-one.md"
		nIgnored   = "2026-10-20-1547-decision-ignored-one.md"
		nBig       = "2026-10-20-1548-decision-big-one.md"
	)
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".agb.json":           syncConfig,
		".gitignore":          "notes/*ignored*\n",
		"notes/" + nCommitted: docText("decision", "커밋"),
		"notes/" + nEdited:    docText("decision", "고치기 전"),
		"notes/" + nBig:       "x" + strings.Repeat("a", 256*1024),
	})
	commitAll(t, root)
	writeFiles(t, root, map[string]string{
		"notes/" + nEdited:  docText("decision", "고친 뒤"),
		"notes/" + nDraft:   docText("decision", "초안"),
		"notes/" + nIgnored: docText("decision", "무시"),
	})
	srv := docSyncServer(t, nil, map[string]canned{putPath(nCommitted): {status: 201, body: serverDoc("CLI", nCommitted, "history", "n")}})
	h := newHarness(t, srv, false)
	if code := h.run("doc", "sync", "--root", root); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if got := srv.puts(); !reflect.DeepEqual(got, []string{nCommitted}) {
		t.Errorf("puts = %v, want only %s", got, nCommitted)
	}
	var got syncOutput
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got.Skipped, "\n")
	for _, want := range []string{"notes/" + nEdited, "notes/" + nDraft, "notes/" + nBig + " (256KB 초과)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("skipped lacks %q: %v", want, got.Skipped)
		}
	}
	if strings.Contains(joined, nIgnored) {
		t.Errorf("an ignored file is not a candidate: %v", got.Skipped)
	}
}

func TestDocSyncFailsWithoutAGitRepositoryOrAConfig(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{".agb.json": syncConfig, "notes/" + docReportName: docText("report", "본문")})
	srv := docSyncServer(t, nil, nil)
	h := newHarness(t, srv, false)
	if code := h.run("doc", "sync", "--root", root); code != 1 || !strings.Contains(h.stderr.String(), "git 저장소") {
		t.Errorf("no repository: code=%d stderr=%s", code, h.stderr)
	}

	bare := t.TempDir()
	writeFiles(t, bare, map[string]string{"notes/" + docReportName: docText("report", "본문")})
	commitAll(t, bare)
	if code := h.run("doc", "sync", "--root", bare); code != 2 || !strings.Contains(h.stderr.String(), "agb init") {
		t.Errorf("no config: code=%d stderr=%s", code, h.stderr)
	}
	if len(srv.reqs) != 0 {
		t.Errorf("requests = %v, want none", srv.paths())
	}
}

func TestDocSyncUsesTheProjectOfTheConfigAndTheFlagOverridesIt(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{".agb.json": syncConfig, "notes/" + docReportName: docText("report", "본문")})
	commitAll(t, root)
	srv := docSyncServer(t, nil, map[string]canned{
		putPath(docReportName):                            {status: 201, body: serverDoc("CLI", docReportName, "history", "n")},
		"PUT /v1/projects/OTH/documents/" + docReportName: {status: 201, body: serverDoc("OTH", docReportName, "history", "n")},
	})
	h := newHarness(t, srv, false)
	if code := h.run("doc", "sync", "--root", root); code != 0 || len(srv.puts()) != 1 {
		t.Fatalf("config project: code=%d stderr=%s", code, h.stderr)
	}
	srv.reqs = nil
	h.run("doc", "sync", "-p", "OTH", "--root", root)
	for _, r := range srv.reqs {
		if strings.Contains(r.Path, "/CLI/") {
			t.Errorf("the flag must win over the config: %s", r.Path)
		}
	}
}

func TestDocSyncReportsRejectionsWarningsAndStopsOnProjectErrors(t *testing.T) {
	const (
		nOK   = "2026-10-20-1544-report-ok.md"
		nBad  = "2026-10-20-1545-report-bad.md"
		nRace = "2026-10-20-1546-report-race.md"
	)
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".agb.json":                             syncConfig,
		"notes/" + nOK:                          docText("report", "정상"),
		"notes/" + nBad:                         docText("report", "거절"),
		"notes/" + nRace:                        docText("report", "충돌"),
		"notes/2026-10-20-1547-report-bytes.md": "x\xff\xfe",
	})
	commitAll(t, root)
	withWarning := strings.Replace(serverDoc("CLI", nOK, "history", "ok"), `"updated_by":"claude"}`, `"updated_by":"claude","warnings":[{"field":"supersedes[0]","reason":"unresolved","message":"대상이 아직 없습니다."}]}`, 1)
	srv := docSyncServer(t, nil, map[string]canned{
		putPath(nOK):   {status: 201, body: withWarning},
		putPath(nBad):  {status: 422, body: `{"error":{"code":"validation_failed","message":"x","details":[{"field":"title","reason":"required","message":"title이 필요합니다."}]}}`},
		putPath(nRace): {status: 409, body: `{"error":{"code":"conflict","message":"x","details":[{"field":"content","reason":"immutable","message":"다른 원문","sha256":"abc"}]}}`},
	})
	h := newHarness(t, srv, false)
	if code := h.run("doc", "sync", "--root", root); code != 1 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var got struct {
		syncOutput
		Warnings []struct {
			Name, Field, Message string
		} `json:"warnings"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Uploaded, []string{nOK}) || len(got.Conflicts) != 1 || got.Conflicts[0].ServerSHA256 != "abc" || len(got.Rejected) != 2 {
		t.Errorf("result = %+v", got)
	}
	rejected := fmt.Sprint(got.Rejected)
	if !strings.Contains(rejected, "title: title이 필요합니다.") || !strings.Contains(rejected, "UTF-8") {
		t.Errorf("rejected = %v", got.Rejected)
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Name != nOK || got.Warnings[0].Field != "supersedes[0]" {
		t.Errorf("warnings = %+v", got.Warnings)
	}

	// A refusal about the project ends the run.
	srv = docSyncServer(t, nil, map[string]canned{putPath(nOK): {status: 409,
		body: `{"error":{"code":"conflict","message":"활성이 아닙니다.","details":[{"reason":"project_not_active"}]}}`}})
	h = newHarness(t, srv, false)
	if code := h.run("doc", "sync", "--root", root); code != 1 || !strings.Contains(h.stderr.String(), "project_not_active") {
		t.Errorf("inactive project: code=%d stderr=%s", code, h.stderr)
	}
	// An unknown project is a 404 on the list, before anything is sent.
	srv = newFakeServer(t)
	h = newHarness(t, srv, true)
	if code := h.run("doc", "sync", "--root", root); code != 1 || !strings.Contains(h.stderr.String(), "404") || len(srv.puts()) != 0 {
		t.Errorf("missing project: code=%d stderr=%s", code, h.stderr)
	}
}

func TestDocSyncSkipsNamesThatCollide(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".agb.json": `{"project": "CLI", "documents": [{"glob": "**/*.md", "name": "base", "imported": true}]}`,
		"a/same.md": "# 하나\n",
		"b/same.md": "# 둘\n",
	})
	commitAll(t, root)
	srv := docSyncServer(t, nil, map[string]canned{putPath("same.md"): {status: 201, body: serverDoc("CLI", docReportName, "history", "n")}})
	h := newHarness(t, srv, false)
	if code := h.run("doc", "sync", "--root", root); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var got syncOutput
	json.Unmarshal(h.stdout.Bytes(), &got)
	if len(srv.puts()) != 1 || len(got.Skipped) != 1 || !strings.Contains(got.Skipped[0], "b/same.md") || !strings.Contains(got.Skipped[0], "a/same.md") {
		t.Errorf("puts = %v, skipped = %v", srv.puts(), got.Skipped)
	}
}

func TestDocSyncReadsEveryPageOfTheServerList(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{".agb.json": syncConfig}
	var docs []string
	local := map[int]string{}
	for _, i := range []int{0, 499, 500, 1099} {
		content := docText("report", "쪽 "+strconv.Itoa(i))
		name := fmt.Sprintf("2026-01-01-0000-report-p%04d.md", i)
		files["notes/"+name] = content
		local[i] = docSHA(content)
	}
	for i := 0; i < 1100; i++ {
		sha, ok := local[i]
		if !ok {
			sha = fmt.Sprintf("%064d", i)
		}
		docs = append(docs, serverDoc("CLI", fmt.Sprintf("2026-01-01-0000-report-p%04d.md", i), "history", sha))
	}
	writeFiles(t, root, files)
	commitAll(t, root)
	srv := docSyncServer(t, docs, nil)
	h := newHarness(t, srv, false)
	if code := h.run("doc", "sync", "--root", root); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var got syncOutput
	json.Unmarshal(h.stdout.Bytes(), &got)
	if len(got.Unchanged) != 4 || len(got.Uploaded) != 0 || len(got.Conflicts) != 0 {
		t.Errorf("summary = %+v", got)
	}
	var offsets []string
	for _, r := range srv.reqs {
		if r.Method == "GET" {
			offsets = append(offsets, r.Query.Get("offset"))
		}
	}
	if !reflect.DeepEqual(offsets, []string{"", "500", "1000"}) {
		t.Errorf("list offsets = %v, want the first page and then 500 and 1000", offsets)
	}
}

func TestDocSyncUsageErrors(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{".agb.json": syncConfig})
	commitAll(t, root)
	srv := newFakeServer(t)
	h := newHarness(t, srv, false)
	for label, args := range map[string][]string{
		"a stray argument":           {"doc", "sync", "--root", root, "extra"},
		"a root that does not exist": {"doc", "sync", "--root", filepath.Join(root, "nope")},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%s: code=%d stderr=%s", label, code, h.stderr)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".agb.json"), []byte(`{"project": "CLI", "folders": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := h.run("doc", "sync", "--root", root); code != 2 || !strings.Contains(h.stderr.String(), "folders") {
		t.Errorf("invalid config: code=%d stderr=%s", code, h.stderr)
	}
	if len(srv.reqs) != 0 {
		t.Errorf("usage errors must not reach the server: %v", srv.paths())
	}
}

func TestDocSyncAndPutReportFollowUps(t *testing.T) {
	const (
		nNew  = "2026-10-20-1544-report-new-one.md"
		nSame = "2026-10-19-1700-report-same-one.md"
		nRef  = "ref/2026/old.md"
	)
	open := docText("report", "본문") + "\n## 후속\n\n- CSP 헤더\n"
	same := open + "\n"
	root := t.TempDir()
	writeFiles(t, root, map[string]string{".agb.json": syncConfig, "notes/" + nNew: open, "notes/" + nSame: same, nRef: open})
	commitAll(t, root)
	puts := map[string]canned{
		putPath(nNew): {status: 201, body: serverDoc("CLI", nNew, "history", docSHA(open))},
		putPath(nRef): {status: 201, body: serverDoc("CLI", nRef, "history", docSHA(open))},
	}
	srv := docSyncServer(t, []string{serverDoc("CLI", nSame, "history", docSHA(same))}, puts)

	// Only the document the sync uploads is read for follow-ups, and an imported one is not.
	h := newHarness(t, srv, false)
	if code := h.run("doc", "sync", "--root", root); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var got struct {
		FollowUps []docFollowUps `json:"follow_ups"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []docFollowUps{{Name: nNew, Sections: []followUp{{Section: "후속", Items: []string{"CSP 헤더"}}}}}
	if !reflect.DeepEqual(got.FollowUps, want) {
		t.Errorf("follow_ups = %#v", got.FollowUps)
	}
	puts[putPath(nSame)] = canned{status: 201, body: serverDoc("CLI", nSame, "history", docSHA(same))}
	ht := newHarness(t, docSyncServer(t, nil, puts), true)
	if code := ht.run("doc", "sync", "--root", root); code != 0 || !strings.Contains(ht.stdout.String(), "== follow-ups (2) ==\n") ||
		!strings.Contains(ht.stdout.String(), "\n"+nNew+"\n  [후속] CSP 헤더\n") {
		t.Errorf("table: code=%d out=%s", code, ht.stdout)
	}

	// Without follow_up_headings in the config there are no follow-ups.
	writeFiles(t, root, map[string]string{".agb.json": `{"project": "CLI", "documents": [{"glob": "notes/*.md", "name": "base"}]}`})
	commitAll(t, root)
	h2 := newHarness(t, docSyncServer(t, nil, puts), false)
	if code := h2.run("doc", "sync", "--root", root); code != 0 || !strings.Contains(h2.stdout.String(), `"follow_ups":[]`) {
		t.Errorf("no headings: code=%d out=%s", code, h2.stdout)
	}
}

// A file under a folder named history is classified by the rules of .agb.json
// alone, by put and by sync alike; the folder name is no input.
func TestDocPutAndSyncClassifyAFileByTheSameRule(t *testing.T) {
	const rel = "archive/x/history/a.md"
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".agb.json": `{"project": "CLI", "documents": [{"glob": "archive/**/*.md", "name": "path", "imported": true, "archived": true}]}`,
		rel:         "# 조사\n\n본문\n",
	})
	commitAll(t, root)
	srv := docSyncServer(t, nil, map[string]canned{putPath(rel): {status: 201, body: serverDoc("CLI", rel, "history", "a")}})
	h := newHarness(t, srv, false)

	if code := h.run("doc", "put", filepath.Join(root, filepath.FromSlash(rel))); code != 0 {
		t.Fatalf("put code=%d stderr=%s", code, h.stderr)
	}
	putReq := srv.last()
	if code := h.run("doc", "sync", "--root", root); code != 0 {
		t.Fatalf("sync code=%d stderr=%s", code, h.stderr)
	}
	puts := srv.putRequests()
	if len(puts) != 2 {
		t.Fatalf("puts = %v", srv.puts())
	}
	if puts[0].Path != puts[1].Path || puts[0].Body != puts[1].Body {
		t.Errorf("put and sync differ:\nput  %s %s\nsync %s %s", puts[0].Path, puts[0].Body, puts[1].Path, puts[1].Body)
	}
	if body := decodeBody(t, putReq); body["imported"] != true || body["archived"] != true || strings.Contains(putReq.Body, "location") {
		t.Errorf("body = %v", body)
	}
}
