package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	docDecisionName = "2026-10-20-1544-decision-priority-model.md"
	docReportName   = "2026-10-19-1700-report-linear-gap.md"
)

func docSHA(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// docText is a record document of the given kind.
func docText(kind, body string) string {
	return "---\nkind: " + kind + "\ndate: 2026-10-20 15:44\ntitle: 제목 " + body + "\ntags: [a, b]\n---\n\n" + body + "\n"
}

// serverDoc is the JSON a server sends for a document without its content.
func serverDoc(project, name, location, sha string) string {
	kind := strings.SplitN(name[len("2026-10-20-1544-"):], "-", 2)[0]
	return fmt.Sprintf(`{"project":%q,"name":%q,"location":%q,"kind":%q,"date":"2026-10-20 15:44","title":"우선순위 큐 모델은 등급과 서버 계산 순서로 한다",`+
		`"tags":["priority","queue"],"sha256":%q,"size":6120,"created_at":"2026-10-21T02:00:00.000Z","created_by":"claude","updated_at":"2026-10-21T02:00:00.000Z","updated_by":"claude"}`,
		project, name, location, kind, sha)
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDocPutSendsTheFileAsItIs(t *testing.T) {
	// CRLF, a byte order mark, markup characters, Korean text and no final newline stay as they are.
	content := "\xef\xbb\xbf---\r\nkind: decision\r\n---\r\n\t본문 <b> & é\r\n끝(개행 없음)"
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"history/" + docDecisionName: content, "archive/" + docReportName: docText("report", "옮긴 기록")})
	put := serverDoc("CLI", docDecisionName, "history", docSHA(content))
	srv := newFakeServer(t)
	srv.on("PUT", "/v1/projects/CLI/documents/"+docDecisionName, canned{status: 201, body: put})
	srv.on("PUT", "/v1/projects/CLI/documents/"+docReportName, canned{status: 200, body: serverDoc("CLI", docReportName, "archive", "abc")})
	srv.on("PUT", "/v1/projects/DUP/documents/"+docDecisionName, canned{status: 409, body: `{"error":{"code":"conflict","message":"같은 이름의 기록 문서가 다른 내용으로 이미 있습니다.","details":[{"field":"content","reason":"immutable","message":"서버에 저장된 내용의 sha256은 abc입니다.","sha256":"abc"}]}}`})
	h := newHarness(t, srv, false)

	if code := h.run("doc", "put", filepath.Join(root, "history", docDecisionName), "-p", "CLI"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	req := srv.last()
	var body map[string]string
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		t.Fatal(err)
	}
	if req.Method != "PUT" || req.Path != "/v1/projects/CLI/documents/"+docDecisionName || body["location"] != "history" || body["content"] != content || len(body) != 2 {
		t.Errorf("request = %s %s %v", req.Method, req.Path, body)
	}
	if h.stdout.String() != put+"\n" {
		t.Errorf("stdout = %q", h.stdout.String())
	}

	// The folder of the file is the location, and the name is the file name.
	if code := h.run("doc", "put", "--project", "CLI", filepath.Join(root, "archive", docReportName)); code != 0 {
		t.Fatalf("archive: code=%d stderr=%s", code, h.stderr)
	}
	req = srv.last()
	json.Unmarshal([]byte(req.Body), &body)
	if req.Path != "/v1/projects/CLI/documents/"+docReportName || body["location"] != "archive" {
		t.Errorf("archive request = %s %v", req.Path, body)
	}

	// A relative path is resolved against the working directory.
	t.Chdir(filepath.Join(root, "history"))
	if code := h.run("doc", "put", docDecisionName, "-p", "CLI"); code != 0 {
		t.Fatalf("bare name: code=%d stderr=%s", code, h.stderr)
	}
	json.Unmarshal([]byte(srv.last().Body), &body)
	if body["location"] != "history" || body["content"] != content {
		t.Errorf("bare name request = %v", body)
	}
	t.Chdir(root)
	if code := h.run("doc", "put", "history/"+docDecisionName, "-p", "CLI"); code != 0 {
		t.Fatalf("relative path: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("doc", "put", "./archive/"+docReportName, "-p", "CLI"); code != 0 {
		t.Fatalf("dot path: code=%d stderr=%s", code, h.stderr)
	}
	json.Unmarshal([]byte(srv.last().Body), &body)
	if body["location"] != "archive" {
		t.Errorf("dot path request = %v", body)
	}

	// On a terminal the stored document is shown.
	ht := newHarness(t, srv, true)
	if code := ht.run("doc", "put", filepath.Join(root, "history", docDecisionName), "-p", "CLI"); code != 0 {
		t.Fatalf("terminal: code=%d stderr=%s", code, ht.stderr)
	}
	for _, want := range []string{"project:", "CLI", "name:", docDecisionName, "location:", "history", "kind:", "decision", "date:", "2026-10-20 15:44", "title:",
		"tags:", "priority, queue", "sha256:", docSHA(content), "size:", "6120", "2026-10-21 11:00 (claude)"} {
		if !strings.Contains(ht.stdout.String(), want) {
			t.Errorf("document view lacks %q:\n%s", want, ht.stdout)
		}
	}
	if strings.Contains(ht.stdout.String(), `"project"`) {
		t.Errorf("the terminal view must not be JSON:\n%s", ht.stdout)
	}

	// Other content under the name: the server's answer is shown and the exit code is 1.
	if code := ht.run("doc", "put", filepath.Join(root, "history", docDecisionName), "-p", "DUP"); code != 1 {
		t.Fatalf("conflict: code=%d", code)
	}
	for _, want := range []string{"409", "conflict", "immutable", "abc"} {
		if !strings.Contains(ht.stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, ht.stderr)
		}
	}
}

func TestDocPutUsageErrors(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"history/" + docDecisionName: docText("decision", "본문"),
		"draft/" + docDecisionName:   docText("decision", "본문"),
		"history/bad-utf8.md":        "---\nkind: decision\n---\n\xff\xfe\n",
		docDecisionName:              docText("decision", "본문"),
	})
	srv := newFakeServer(t)
	h := newHarness(t, srv, true)
	good := filepath.Join(root, "history", docDecisionName)
	for label, args := range map[string][]string{
		"no project": {"doc", "put", good},
		"no file":    {"doc", "put", "-p", "CLI"},
		"two files":  {"doc", "put", good, good, "-p", "CLI"},
		"a folder that is not history or archive": {"doc", "put", filepath.Join(root, "draft", docDecisionName), "-p", "CLI"},
		"a file outside any folder":               {"doc", "put", filepath.Join(root, docDecisionName), "-p", "CLI"},
		"a missing file":                          {"doc", "put", filepath.Join(root, "history", "missing.md"), "-p", "CLI"},
		"a folder instead of a file":              {"doc", "put", filepath.Join(root, "history"), "-p", "CLI"},
		"a file that is not UTF-8":                {"doc", "put", filepath.Join(root, "history", "bad-utf8.md"), "-p", "CLI"},
		"an unknown option":                       {"doc", "put", good, "-p", "CLI", "--bogus"},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%s: code=%d stderr=%s", label, code, h.stderr)
		}
	}
	if len(srv.reqs) != 0 {
		t.Errorf("usage errors must not reach the server: %v", srv.paths())
	}
	if code := h.run("doc", "put", filepath.Join(root, "draft", docDecisionName), "-p", "CLI"); !strings.Contains(h.stderr.String(), "history") || !strings.Contains(h.stderr.String(), "archive") {
		t.Errorf("the folder error must name the folders: code=%d stderr=%s", code, h.stderr)
	}
}

// docSyncServer answers the document list of project CLI from docs, a page at a
// time as asked, and the document puts from puts. Anything else is a 404.
func docSyncServer(t *testing.T, docs []string, puts map[string]canned) *fakeServer {
	t.Helper()
	f := &fakeServer{routes: map[string]canned{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.reqs = append(f.reqs, recorded{r.Method, r.URL.Path, r.URL.Query(), r.Header.Clone(), string(body)})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method == "GET" && r.URL.Path == "/v1/projects/CLI/documents" {
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if limit <= 0 || limit > 500 {
				limit = 50
			}
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			offset = min(offset, len(docs))
			fmt.Fprintf(w, `{"items":[%s],"total":%d,"limit":%d,"offset":%d}`, strings.Join(docs[offset:min(offset+limit, len(docs))], ","), len(docs), limit, offset)
			return
		}
		c, ok := puts[r.Method+" "+r.URL.Path]
		if !ok {
			c = canned{404, `{"error":{"code":"not_found","message":"대상이 없습니다."}}`, ""}
		}
		if c.status == 0 {
			c.status = 200
		}
		w.WriteHeader(c.status)
		w.Write([]byte(c.body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

type syncOutput struct {
	Uploaded  []string `json:"uploaded"`
	Moved     []string `json:"moved"`
	Unchanged []string `json:"unchanged"`
	Conflicts []struct {
		Name         string `json:"name"`
		LocalSHA256  string `json:"local_sha256"`
		ServerSHA256 string `json:"server_sha256"`
	} `json:"conflicts"`
	Skipped  []string `json:"skipped"`
	Rejected []struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	} `json:"rejected"`
}

func sorted(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}

func (s *fakeServer) puts() []string {
	var out []string
	for _, r := range s.reqs {
		if r.Method == "PUT" {
			out = append(out, strings.TrimPrefix(r.Path, "/v1/projects/CLI/documents/"))
		}
	}
	return sorted(out)
}

func TestDocSyncSortsTheFilesIntoCategories(t *testing.T) {
	const (
		nUploaded  = "2026-10-20-1544-decision-new-one.md"
		nUnchanged = "2026-10-19-1700-report-same-one.md"
		nMovedIn   = "2026-10-19-1657-implementation-moved-in.md"
		nMovedBack = "2026-10-19-1000-implementation-moved-back.md"
		nConflict  = "2026-10-18-1000-decision-conflict-one.md"
		nArchNew   = "2026-10-17-0900-incident-arch-new.md"
		nRace      = "2026-10-16-0900-report-race-one.md"
		nRefused   = "2026-10-15-0900-report-refused-one.md"
		nBadUTF8   = "2026-10-14-0900-report-bad-utf8.md"
		nLegacy    = "2026-10-17-14-30-handoff.md"
		nServer    = "2026-10-01-0900-report-server-only.md"
	)
	same, movedIn, movedBack := docText("report", "같은 기록"), docText("implementation", "옮긴 기록"), docText("implementation", "되돌린 기록")
	conflict := docText("decision", "고친 기록")
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"history/" + nUploaded:                              docText("decision", "새 기록"),
		"history/" + nUnchanged:                             same,
		"history/" + nMovedBack:                             movedBack,
		"history/" + nConflict:                              conflict,
		"history/" + nRace:                                  docText("report", "경합"),
		"history/" + nRefused:                               docText("report", "거절"),
		"history/" + nBadUTF8:                               "---\nkind: report\n---\n\xff\n",
		"history/_decision.md":                              "---\nkind: decision\n---\n서식\n",
		"history/notes.txt":                                 "메모",
		"history/README":                                    "읽어 보세요",
		"history/sub/2026-10-13-0900-report-in-a-folder.md": docText("report", "하위 폴더"),
		"archive/" + nMovedIn:                               movedIn,
		"archive/" + nArchNew:                               docText("incident", "새 보관 기록"),
		"archive/" + nLegacy:                                "# 이전 체계의 문서\n",
		"archive/_x.md":                                     "서식",
	})
	puts := map[string]canned{
		"PUT /v1/projects/CLI/documents/" + nUploaded:  {status: 201, body: serverDoc("CLI", nUploaded, "history", "n")},
		"PUT /v1/projects/CLI/documents/" + nArchNew:   {status: 201, body: serverDoc("CLI", nArchNew, "archive", "n")},
		"PUT /v1/projects/CLI/documents/" + nMovedIn:   {status: 200, body: serverDoc("CLI", nMovedIn, "archive", docSHA(movedIn))},
		"PUT /v1/projects/CLI/documents/" + nMovedBack: {status: 200, body: serverDoc("CLI", nMovedBack, "history", docSHA(movedBack))},
		"PUT /v1/projects/CLI/documents/" + nRace:      {status: 409, body: `{"error":{"code":"conflict","message":"같은 이름의 기록 문서가 다른 내용으로 이미 있습니다.","details":[{"field":"content","reason":"immutable","message":"m","sha256":"serversha"}]}}`},
		"PUT /v1/projects/CLI/documents/" + nRefused:   {status: 422, body: `{"error":{"code":"validation_failed","message":"요청 값이 규칙에 맞지 않습니다.","details":[{"field":"content","message":"프런트매터의 kind(\"report\")가 파일명의 종류(decision)와 같아야 합니다."}]}}`},
	}
	srv := docSyncServer(t, []string{
		serverDoc("CLI", nUnchanged, "history", docSHA(same)),
		serverDoc("CLI", nMovedIn, "history", docSHA(movedIn)),
		serverDoc("CLI", nMovedBack, "archive", docSHA(movedBack)),
		serverDoc("CLI", nConflict, "history", "othersha"),
		serverDoc("CLI", nServer, "history", "serversideonly"),
	}, puts)
	h := newHarness(t, srv, false)

	code := h.run("doc", "sync", "-p", "CLI", "--root", root)
	if code != 1 {
		t.Fatalf("code=%d (conflicts and refused files end with 1) stderr=%s", code, h.stderr)
	}
	var got syncOutput
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not the summary: %v\n%s", err, h.stdout)
	}
	if !reflect.DeepEqual(sorted(got.Uploaded), sorted([]string{nUploaded, nArchNew})) {
		t.Errorf("uploaded = %v", got.Uploaded)
	}
	if !reflect.DeepEqual(sorted(got.Moved), sorted([]string{nMovedIn, nMovedBack})) {
		t.Errorf("moved = %v", got.Moved)
	}
	if !reflect.DeepEqual(got.Unchanged, []string{nUnchanged}) {
		t.Errorf("unchanged = %v", got.Unchanged)
	}
	conflicts := map[string][2]string{}
	for _, c := range got.Conflicts {
		conflicts[c.Name] = [2]string{c.LocalSHA256, c.ServerSHA256}
	}
	if len(got.Conflicts) != 2 || conflicts[nConflict] != [2]string{docSHA(conflict), "othersha"} || conflicts[nRace][1] != "serversha" || conflicts[nRace][0] != docSHA(docText("report", "경합")) {
		t.Errorf("conflicts = %+v", got.Conflicts)
	}
	if !reflect.DeepEqual(got.Skipped, []string{nLegacy}) {
		t.Errorf("skipped = %v", got.Skipped)
	}
	rejected := map[string]string{}
	for _, r := range got.Rejected {
		rejected[r.Name] = r.Reason
	}
	if len(got.Rejected) != 2 || !strings.Contains(rejected[nRefused], "kind") || !strings.Contains(rejected[nBadUTF8], "UTF-8") {
		t.Errorf("rejected = %+v", got.Rejected)
	}
	// The files that are templates, not markdown, in a folder or not record documents are not mentioned.
	for _, w := range []string{"_decision", "notes", "README", "in-a-folder", "_x.md", nServer} {
		if strings.Contains(h.stdout.String(), w) {
			t.Errorf("the summary mentions %q:\n%s", w, h.stdout)
		}
	}
	// Only the files that need it are sent: new ones, moved ones, and the one whose
	// list entry was missing; never a conflict, an unchanged file or one the CLI refused.
	wantPuts := sorted([]string{nUploaded, nArchNew, nMovedIn, nMovedBack, nRace, nRefused})
	if !reflect.DeepEqual(srv.puts(), wantPuts) {
		t.Errorf("puts = %v, want %v", srv.puts(), wantPuts)
	}
	for _, r := range srv.reqs {
		if r.Method == "PUT" {
			var body map[string]string
			json.Unmarshal([]byte(r.Body), &body)
			name := strings.TrimPrefix(r.Path, "/v1/projects/CLI/documents/")
			wantLocation := "history"
			if name == nArchNew || name == nMovedIn {
				wantLocation = "archive"
			}
			if body["location"] != wantLocation {
				t.Errorf("%s was sent as %q, want %q", name, body["location"], wantLocation)
			}
		}
	}
	if !strings.Contains(h.stderr.String(), "충돌 2건") || !strings.Contains(h.stderr.String(), "거절 2건") {
		t.Errorf("stderr = %s", h.stderr)
	}

	// The summary on a terminal: the counts and the names per category.
	ht := newHarness(t, srv, true)
	if code := ht.run("doc", "sync", "-p", "CLI", "--root", root); code != 1 {
		t.Fatalf("terminal: code=%d", code)
	}
	out := ht.stdout.String()
	for _, want := range []string{"uploaded 2", "moved 2", "unchanged 1", "conflicts 2", "== uploaded (2) ==", "== moved (2) ==", "== unchanged (1) ==", "== conflicts (2) ==", "== skipped (1) ==", "== rejected (2) ==",
		nUploaded, nUnchanged, nConflict, nLegacy} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"uploaded"`) {
		t.Errorf("the terminal summary must not be JSON:\n%s", out)
	}
}

func TestDocSyncWithNothingToFix(t *testing.T) {
	same := docText("report", "같은 기록")
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"history/" + docReportName: same, "history/_report.md": "서식"})
	srv := docSyncServer(t, []string{serverDoc("CLI", docReportName, "history", docSHA(same))}, nil)
	h := newHarness(t, srv, false)
	if code := h.run("doc", "sync", "-p", "CLI", "--root", root); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var got syncOutput
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil || !reflect.DeepEqual(got.Unchanged, []string{docReportName}) {
		t.Errorf("summary = %+v, %v", got, err)
	}
	// Every category is a list, never null.
	for _, key := range []string{`"uploaded":[]`, `"moved":[]`, `"conflicts":[]`, `"skipped":[]`, `"rejected":[]`} {
		if !strings.Contains(h.stdout.String(), key) {
			t.Errorf("the summary lacks %s: %s", key, h.stdout)
		}
	}
	if len(srv.puts()) != 0 {
		t.Errorf("puts = %v", srv.puts())
	}
	// A repeated run with the working directory as the root is the same.
	t.Chdir(root)
	if code := h.run("doc", "sync", "-p", "CLI"); code != 0 || !strings.Contains(h.stdout.String(), docReportName) {
		t.Errorf("default root: code=%d out=%s", code, h.stdout)
	}
	if code := h.run("doc", "sync", "--project", "CLI", "--root", "."); code != 0 {
		t.Errorf("root .: code=%d", code)
	}
}

func TestDocSyncReadsEveryPageOfTheServerList(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{}
	var docs []string
	local := map[int]string{}
	for _, i := range []int{0, 499, 500, 1099} {
		content := docText("report", "쪽 "+strconv.Itoa(i))
		name := fmt.Sprintf("2026-01-01-0000-report-p%04d.md", i)
		files["history/"+name] = content
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
	srv := docSyncServer(t, docs, nil)
	h := newHarness(t, srv, false)
	if code := h.run("doc", "sync", "-p", "CLI", "--root", root); code != 0 {
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
			if r.Query.Get("limit") != "500" {
				t.Errorf("limit = %q, want 500", r.Query.Get("limit"))
			}
			offsets = append(offsets, r.Query.Get("offset"))
		}
	}
	if !reflect.DeepEqual(offsets, []string{"", "500", "1000"}) {
		t.Errorf("list offsets = %v, want the first page and then 500 and 1000", offsets)
	}
}

func TestDocSyncStopsOnErrorsThatAreNotAboutAFile(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"history/" + docReportName: docText("report", "본문")})
	// An unknown project is a 404 on the list, before anything is sent.
	srv := newFakeServer(t)
	h := newHarness(t, srv, true)
	if code := h.run("doc", "sync", "-p", "CLI", "--root", root); code != 1 || !strings.Contains(h.stderr.String(), "404") {
		t.Errorf("missing project: code=%d stderr=%s", code, h.stderr)
	}
	if len(srv.puts()) != 0 {
		t.Errorf("puts after a failed list: %v", srv.puts())
	}
	// A put the server refuses for the project (not active) ends the run.
	srv = docSyncServer(t, nil, map[string]canned{"PUT /v1/projects/CLI/documents/" + docReportName: {status: 409,
		body: `{"error":{"code":"conflict","message":"활성이 아닙니다.","details":[{"reason":"project_not_active"}]}}`}})
	h = newHarness(t, srv, false)
	if code := h.run("doc", "sync", "-p", "CLI", "--root", root); code != 1 || !strings.Contains(h.stderr.String(), "project_not_active") {
		t.Errorf("inactive project: code=%d stderr=%s", code, h.stderr)
	}
}

func TestDocSyncUsageErrors(t *testing.T) {
	root := t.TempDir()
	empty := t.TempDir()
	writeFiles(t, root, map[string]string{"history/" + docReportName: docText("report", "본문")})
	srv := newFakeServer(t)
	h := newHarness(t, srv, false)
	for label, args := range map[string][]string{
		"no project":                     {"doc", "sync", "--root", root},
		"a stray argument":               {"doc", "sync", "-p", "CLI", "--root", root, "extra"},
		"a root without history/archive": {"doc", "sync", "-p", "CLI", "--root", empty},
		"a root that does not exist":     {"doc", "sync", "-p", "CLI", "--root", filepath.Join(empty, "nope")},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%s: code=%d stderr=%s", label, code, h.stderr)
		}
	}
	if len(srv.reqs) != 0 {
		t.Errorf("usage errors must not reach the server: %v", srv.paths())
	}
	// One of the two folders is enough.
	writeFiles(t, empty, map[string]string{"archive/" + docReportName: docText("report", "본문")})
	srv = docSyncServer(t, nil, map[string]canned{"PUT /v1/projects/CLI/documents/" + docReportName: {status: 201, body: serverDoc("CLI", docReportName, "archive", "n")}})
	h = newHarness(t, srv, false)
	if code := h.run("doc", "sync", "-p", "CLI", "--root", empty); code != 0 {
		t.Errorf("archive only: code=%d stderr=%s", code, h.stderr)
	}
}

const docSearchJSON = `{"items":[` +
	`{"project":"AGB","name":"2026-10-20-1544-decision-priority-model.md","location":"history","kind":"decision","date":"2026-10-20 15:44","title":"우선순위 큐 모델은 등급과 서버 계산 순서로 한다","tags":["priority","queue"],"sha256":"a","size":10,"created_at":"2026-10-21T02:00:00.000Z","created_by":"claude","updated_at":"2026-10-21T02:00:00.000Z","updated_by":"claude","snippet":"...[우선순위] 큐는 등급으로..."},` +
	`{"project":"AGT","name":"2026-10-18-1000-decision-no-meta.md","location":"archive","kind":"decision","date":null,"title":null,"tags":[],"sha256":"b","size":10,"created_at":"2026-10-21T02:00:00.000Z","created_by":"claude","updated_at":"2026-10-21T02:00:00.000Z","updated_by":"claude","snippet":"메타데이터가 없는 [우선순위] 기록"}` +
	`],"total":7,"limit":2,"offset":0}`

func TestDocSearch(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/documents", canned{body: docSearchJSON})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0

	if code := h.run("doc", "search", "우선순위 큐"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Method != "GET" || req.Path != "/v1/documents" || req.Query.Encode() != "q="+"%EC%9A%B0%EC%84%A0%EC%88%9C%EC%9C%84+%ED%81%90" {
		t.Errorf("request = %s %s?%s", req.Method, req.Path, req.Query.Encode())
	}
	lines := strings.Split(strings.TrimRight(h.stdout.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("table:\n%s", h.stdout)
	}
	if got := strings.Fields(lines[0]); !equalStrings(got, []string{"PROJECT", "NAME", "KIND", "DATE", "TITLE", "SNIPPET"}) {
		t.Errorf("header = %v", got)
	}
	for i, want := range [][]string{
		{"AGB", "2026-10-20-1544-decision-priority-model.md", "decision", "2026-10-20", "15:44"},
		{"AGT", "2026-10-18-1000-decision-no-meta.md", "decision", "-", "-"},
	} {
		if got := strings.Fields(lines[i+1]); !equalStrings(got[:len(want)], want) {
			t.Errorf("row %d = %v, want it to start with %v", i+1, got, want)
		}
	}
	if !strings.Contains(lines[1], "우선순위 큐 모델은 등급과 서버 계산 순서로 한다") || !strings.Contains(lines[1], "...[우선순위] 큐는 등급으로...") ||
		!strings.Contains(lines[2], "메타데이터가 없는 [우선순위] 기록") {
		t.Errorf("title and snippet:\n%s", h.stdout)
	}
	if !strings.Contains(h.stdout.String(), "7건 중 2건 표시") {
		t.Errorf("a partial page must say so:\n%s", h.stdout)
	}

	// The words may be separate arguments; the filters are passed on.
	if code := h.run("doc", "search", "-p", "AGB,AGT", "--kind", "decision,report", "--limit", "5", "우선순위", "큐"); code != 0 {
		t.Fatalf("filters: code=%d stderr=%s", code, h.stderr)
	}
	want := "kind=decision%2Creport&limit=5&project=AGB%2CAGT&q=%EC%9A%B0%EC%84%A0%EC%88%9C%EC%9C%84+%ED%81%90"
	if got := srv.last().Query.Encode(); got != want {
		t.Errorf("query = %s, want %s", got, want)
	}
	if code := h.run("doc", "search", "--project", "AGB", `"우선순위 큐"`); code != 0 || srv.last().Query.Get("q") != `"우선순위 큐"` || srv.last().Query.Get("project") != "AGB" {
		t.Errorf("phrase: code=%d query=%v", code, srv.last().Query)
	}

	// JSON is the server body as it is.
	hj := newHarness(t, srv, false)
	if code := hj.run("doc", "search", "우선순위"); code != 0 || hj.stdout.String() != docSearchJSON+"\n" {
		t.Errorf("json: code=%d out=%q", code, hj.stdout.String())
	}

	srv.on("GET", "/v1/documents", canned{body: `{"items":[],"total":0,"limit":50,"offset":0}`})
	if code := h.run("doc", "search", "없는검색어"); code != 0 || strings.TrimSpace(h.stdout.String()) != "검색 결과가 없습니다." {
		t.Errorf("empty: code=%d out=%q", code, h.stdout.String())
	}
	srv.reqs = nil
	for label, args := range map[string][]string{
		"no query":        {"doc", "search"},
		"only a filter":   {"doc", "search", "-p", "AGB"},
		"an unknown flag": {"doc", "search", "x", "--bogus"},
		"a bad limit":     {"doc", "search", "x", "--limit", "abc"},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%s: code=%d stderr=%s", label, code, h.stderr)
		}
	}
	if len(srv.reqs) != 0 {
		t.Errorf("usage errors must not reach the server: %v", srv.paths())
	}
	srv.on("GET", "/v1/documents", canned{status: 400, body: `{"error":{"code":"bad_request","message":"질의 매개변수가 규칙에 맞지 않습니다.","details":[{"field":"kind","reason":"invalid_query","message":"\"plan\"는 허용되지 않는 값입니다."}]}}`})
	if code := h.run("doc", "search", "x", "--kind", "plan"); code != 1 || !strings.Contains(h.stderr.String(), "kind") {
		t.Errorf("server error: code=%d stderr=%s", code, h.stderr)
	}
}

func TestDocGet(t *testing.T) {
	content := "---\r\nkind: decision\r\n---\r\n\t본문 <b> & é\r\n끝(개행 없음)"
	contentJSON, _ := json.Marshal(content)
	full := strings.Replace(serverDoc("CLI", docDecisionName, "history", docSHA(content)), `"size":6120,`, `"size":6120,"content":`+string(contentJSON)+`,`, 1)
	srv := newFakeServer(t)
	srv.on("GET", "/v1/projects/CLI/documents/"+docDecisionName, canned{body: full})
	h := newHarness(t, srv, true)

	if code := h.run("doc", "get", "CLI", docDecisionName); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Method != "GET" || req.Path != "/v1/projects/CLI/documents/"+docDecisionName {
		t.Errorf("request = %s %s", req.Method, req.Path)
	}
	out := h.stdout.String()
	for _, want := range []string{"project:", "name:", docDecisionName, "location:", "history", "kind:", "decision", "date:", "title:", "tags:", "priority, queue", "sha256:", "size:", "created:", "updated:"} {
		if !strings.Contains(out, want) {
			t.Errorf("fields lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "끝(개행 없음)") {
		t.Errorf("the fields view must not print the content:\n%s", out)
	}

	// JSON is the server body as it is, with the content.
	hj := newHarness(t, srv, false)
	if code := hj.run("doc", "get", "CLI", docDecisionName); code != 0 || hj.stdout.String() != full+"\n" {
		t.Errorf("json: code=%d out=%q", code, hj.stdout.String())
	}

	// --content prints the original and nothing else, on a terminal, in a pipe and with --json or --table.
	for label, hh := range map[string]*harness{"terminal": h, "pipe": hj} {
		for _, extra := range [][]string{nil, {"--json"}, {"--table"}} {
			args := append([]string{"doc", "get", "CLI", docDecisionName, "--content"}, extra...)
			if code := hh.run(args...); code != 0 || hh.stdout.String() != content {
				t.Errorf("%s %v: code=%d stdout=%q, want the content byte for byte", label, extra, code, hh.stdout.String())
			}
			if hh.stderr.Len() != 0 {
				t.Errorf("%s %v: stderr=%s", label, extra, hh.stderr)
			}
		}
	}
	if code := h.run("doc", "get", "--content", "CLI", docDecisionName); code != 0 || h.stdout.String() != content {
		t.Errorf("--content before the arguments: code=%d stdout=%q", code, h.stdout.String())
	}

	srv.reqs = nil
	for label, args := range map[string][]string{
		"no arguments":    {"doc", "get"},
		"no name":         {"doc", "get", "CLI"},
		"three arguments": {"doc", "get", "CLI", docDecisionName, "extra"},
		"an unknown flag": {"doc", "get", "CLI", docDecisionName, "--bogus"},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%s: code=%d stderr=%s", label, code, h.stderr)
		}
	}
	if len(srv.reqs) != 0 {
		t.Errorf("usage errors must not reach the server: %v", srv.paths())
	}
	if code := h.run("doc", "get", "CLI", docReportName); code != 1 || !strings.Contains(h.stderr.String(), "404") {
		t.Errorf("missing document: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("doc", "get", "CLI", docReportName, "--content"); code != 1 || h.stdout.Len() != 0 {
		t.Errorf("missing document with --content: code=%d stdout=%q", code, h.stdout.String())
	}
}

func TestDocHelp(t *testing.T) {
	h := newHarness(t, nil, true)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--help"}, "doc "},
		{[]string{"doc", "--help"}, "sync"},
		{[]string{"help", "doc"}, "search"},
		{[]string{"doc", "put", "--help"}, "history"},
		{[]string{"doc", "sync", "--help"}, "--root"},
		{[]string{"doc", "search", "--help"}, "--kind"},
		{[]string{"doc", "get", "--help"}, "--content"},
	} {
		if code := h.run(c.args...); code != 0 || !strings.Contains(h.stdout.String(), c.want) {
			t.Errorf("%v: code=%d out=%s", c.args, code, h.stdout)
		}
	}
	if code := h.run("doc"); code != 2 {
		t.Errorf("group without sub: code=%d", code)
	}
	if code := h.run("doc", "bogus"); code != 2 {
		t.Errorf("unknown sub: code=%d", code)
	}
}
