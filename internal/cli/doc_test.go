package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/wsk0715/agent-board/internal/model"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
	kind := "other"
	if k, ok := model.ParseDocumentName(name); ok {
		kind = string(k)
	}
	return fmt.Sprintf(`{"project":%q,"name":%q,"location":%q,"kind":%q,"date":"2026-10-20 15:44","title":"우선순위 큐 모델은 등급과 서버 계산 순서로 한다",`+
		`"tags":["priority","queue"],"state":"current","archived":false,"imported":false,"path":null,"supersedes":[],"superseded_by":[],"related":[],"issues":[],"unresolved_refs":[],`+
		`"sha256":%q,"size":6120,"created_at":"2026-10-21T02:00:00.000Z","created_by":"claude","updated_at":"2026-10-21T02:00:00.000Z","updated_by":"claude"}`,
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

// commitAll makes root a git repository and commits everything in it.
func commitAll(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "dev"},
		{"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "docs"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
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

const docSearchJSON = `{"items":[` +
	`{"project":"AGB","name":"2026-10-20-1544-decision-priority-model.md","kind":"decision","date":"2026-10-20 15:44","title":"우선순위 큐 모델은 등급과 서버 계산 순서로 한다","tags":["priority","queue"],"state":"current","archived":false,"imported":false,"path":null,"supersedes":["2026-10-18-0900-decision-queue-draft.md"],"superseded_by":[],"related":[],"issues":["AGB-21"],"unresolved_refs":[],"sha256":"a","size":10,"created_at":"2026-10-21T02:00:00.000Z","created_by":"claude","updated_at":"2026-10-21T02:00:00.000Z","updated_by":"claude","location":"history","snippet":"...[우선순위] 큐는 등급으로..."},` +
	`{"project":"XYZ","name":"2026-10-18-1000-decision-no-meta.md","kind":"decision","date":null,"title":null,"tags":[],"state":"archived","archived":true,"imported":false,"path":null,"supersedes":[],"superseded_by":["2026-11-01-1000-decision-meta.md"],"related":[],"issues":[],"unresolved_refs":[],"sha256":"b","size":10,"created_at":"2026-10-21T02:00:00.000Z","created_by":"claude","updated_at":"2026-10-21T02:00:00.000Z","updated_by":"claude","location":"archive","snippet":"메타데이터가 없는 [우선순위] 기록"}` +
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
	if got := strings.Fields(lines[0]); !equalStrings(got, []string{"PROJECT", "NAME", "STATE", "KIND", "DATE", "TITLE", "SNIPPET"}) {
		t.Errorf("header = %v", got)
	}
	for i, want := range [][]string{
		{"AGB", "2026-10-20-1544-decision-priority-model.md", "current", "decision", "2026-10-20", "15:44"},
		{"XYZ", "2026-10-18-1000-decision-no-meta.md", "archived", "decision", "-", "-"},
	} {
		if got := strings.Fields(lines[i+1]); !equalStrings(got[:len(want)], want) {
			t.Errorf("row %d = %v, want it to start with %v", i+1, got, want)
		}
	}
	if !strings.Contains(lines[1], "우선순위 큐 모델은 등급과 서버 계산 순서로 한다") || !strings.Contains(lines[1], "...[우선순위] 큐는 등급으로...") ||
		!strings.Contains(lines[2], "메타데이터가 없는 [우선순위] 기록") {
		t.Errorf("title and snippet:\n%s", h.stdout)
	}
	for _, want := range []string{"AGB/2026-10-20-1544-decision-priority-model.md", "supersedes: 2026-10-18-0900-decision-queue-draft.md",
		"XYZ/2026-10-18-1000-decision-no-meta.md", "superseded_by: 2026-11-01-1000-decision-meta.md", "7건 중 2건 표시"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, h.stdout)
		}
	}

	// The words may be separate arguments; the filters are passed on.
	if code := h.run("doc", "search", "-p", "AGB,XYZ", "--kind", "decision,report", "--limit", "5", "우선순위", "큐"); code != 0 {
		t.Fatalf("filters: code=%d stderr=%s", code, h.stderr)
	}
	want := "kind=decision%2Creport&limit=5&project=AGB%2CXYZ&q=%EC%9A%B0%EC%84%A0%EC%88%9C%EC%9C%84+%ED%81%90"
	if got := srv.last().Query.Encode(); got != want {
		t.Errorf("query = %s, want %s", got, want)
	}
	if code := h.run("doc", "search", "--state", "all", "--imported", "true", "큐"); code != 0 || srv.last().Query.Get("state") != "all" || srv.last().Query.Get("imported") != "true" {
		t.Errorf("state and imported: code=%d query=%s", code, srv.last().Query.Encode())
	}
	if code := h.run("doc", "search", "--location", "history", "큐"); code != 2 {
		t.Errorf("--location is gone: code=%d", code)
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

func TestDocList(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/documents", canned{body: docSearchJSON})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0

	if code := h.run("doc", "list", "-p", "AGB", "--kind", "decision", "--state", "superseded,archived", "--imported", "false", "--limit", "5"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if got := srv.last().Query.Encode(); got != "imported=false&kind=decision&limit=5&project=AGB&state=superseded%2Carchived" {
		t.Errorf("query = %s", got)
	}
	lines := strings.Split(strings.TrimRight(h.stdout.String(), "\n"), "\n")
	if got := strings.Fields(lines[0]); !equalStrings(got, []string{"PROJECT", "NAME", "STATE", "KIND", "DATE", "TITLE"}) {
		t.Errorf("header = %v", got)
	}
	if got := strings.Fields(lines[1]); !equalStrings(got[:4], []string{"AGB", "2026-10-20-1544-decision-priority-model.md", "current", "decision"}) {
		t.Errorf("row = %v", got)
	}
	if strings.Contains(h.stdout.String(), "...[우선순위]") || !strings.Contains(h.stdout.String(), "7건 중 2건 표시") {
		t.Errorf("list has no snippet and says a page is partial:\n%s", h.stdout)
	}
	if !strings.Contains(h.stdout.String(), "superseded_by: 2026-11-01-1000-decision-meta.md") {
		t.Errorf("list shows the relations:\n%s", h.stdout)
	}
	if code := h.run("doc", "list", "--location", "archive"); code != 2 {
		t.Errorf("--location is gone: code=%d", code)
	}
	if code := h.run("doc", "list"); code != 0 || srv.last().Query.Encode() != "" {
		t.Errorf("no filter: code=%d query=%s", code, srv.last().Query.Encode())
	}
	srv.on("GET", "/v1/documents", canned{body: `{"items":[],"total":0,"limit":50,"offset":0}`})
	if code := h.run("doc", "list", "-p", "AGB"); code != 0 || strings.TrimSpace(h.stdout.String()) != "기록 문서가 없습니다." {
		t.Errorf("empty: code=%d out=%q", code, h.stdout.String())
	}
	if code := h.run("doc", "list", "x"); code != 2 {
		t.Errorf("positional argument: code=%d", code)
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
	for _, want := range []string{"project:", "name:", docDecisionName, "state:", "current", "kind:", "decision", "date:", "title:", "tags:", "priority, queue", "sha256:", "size:", "created:", "updated:"} {
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
		{[]string{"doc", "new", "--help"}, "--slug"},
		{[]string{"doc", "check", "--help"}, "--root"},
		{[]string{"doc", "put", "--help"}, "--supersedes"},
		{[]string{"init", "--help"}, ".agb.json"},
		{[]string{"doc", "sync", "--help"}, "--root"},
		{[]string{"doc", "search", "--help"}, "--kind"},
		{[]string{"doc", "get", "--help"}, "--content"},
		{[]string{"doc", "list", "--help"}, "--state"},
		{[]string{"doc", "list", "--help"}, "--imported"},
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

func TestDocGetShowsStateAndRelations(t *testing.T) {
	doc := `{"project":"AGB","name":"2026-10-20-1544-decision-priority-model.md","kind":"decision","date":"2026-10-20 15:44","title":"우선순위 모델","tags":["priority"],` +
		`"state":"superseded","archived":false,"imported":true,"path":"notes/priority.md","supersedes":["2026-10-18-0900-decision-queue-draft.md","OTH/2026-10-01-1000-report-x.md"],` +
		`"superseded_by":["2026-11-01-1000-decision-priority-v2.md"],"related":["2026-10-19-1100-incident-queue-lock.md"],"issues":["AGB-21","AGB-22"],` +
		`"unresolved_refs":[{"kind":"supersedes","target":"2026-09-01-0900-decision-missing.md"}],"sha256":"abc","size":12,` +
		`"created_at":"2026-10-21T02:00:00.000Z","created_by":"claude","updated_at":"2026-10-21T02:00:00.000Z","updated_by":"claude","location":"legacy"}`
	srv := newFakeServer(t)
	srv.on("GET", "/v1/projects/AGB/documents/"+docDecisionName, canned{body: doc})
	h := newHarness(t, srv, true)
	if code := h.run("doc", "get", "AGB", docDecisionName); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	out := h.stdout.String()
	if strings.Contains(out, "location:") {
		t.Errorf("the location is gone from the view:\n%s", out)
	}
	for _, want := range []string{"state:", "superseded", "archived:", "imported:", "yes", "path:", "notes/priority.md",
		"supersedes:", "2026-10-18-0900-decision-queue-draft.md, OTH/2026-10-01-1000-report-x.md", "superseded_by:", "2026-11-01-1000-decision-priority-v2.md",
		"related:", "2026-10-19-1100-incident-queue-lock.md", "issues:", "AGB-21, AGB-22",
		"unresolved:", "supersedes 2026-09-01-0900-decision-missing.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
}
