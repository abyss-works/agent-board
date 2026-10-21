package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

const (
	apiDocPriority = "2026-10-20-1544-decision-priority-model.md"
	apiDocLinear   = "2026-10-19-1700-report-linear-gap.md"
	apiDocLock     = "2026-10-21-0900-incident-db-lock.md"
	apiDocRelease  = "2026-10-19-1657-implementation-v020-release.md"
	apiDocBare     = "2026-10-18-1000-decision-no-meta.md"
)

// apiDocText builds a record document: a front matter block of the given kind
// and the extra lines, then the body.
func apiDocText(kind, body string, lines ...string) string {
	return "---\nkind: " + kind + "\n" + strings.Join(append(lines, ""), "\n") + "---\n\n" + body + "\n"
}

func apiDocSHA(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func docRequest(location, content string) string {
	b, err := json.Marshal(map[string]string{"location": location, "content": content})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func documentPath(project, name string) string {
	return "/v1/projects/" + project + "/documents/" + name
}

func (e *env) putDocument(t *testing.T, project, name, location, content string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(t, "PUT", documentPath(project, name), docRequest(location, content), headers)
}

// mustPutDocument stores a document and returns the response document.
func (e *env) mustPutDocument(t *testing.T, project, name, location, content string) model.Document {
	t.Helper()
	rec := e.putDocument(t, project, name, location, content, nil)
	if rec.Code != 201 && rec.Code != 200 {
		t.Fatalf("put %s: %d %s", name, rec.Code, rec.Body.String())
	}
	var d model.Document
	decodeJSON(t, rec, &d)
	return d
}

type documentList struct {
	Items  []model.Document `json:"items"`
	Total  int              `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

func (e *env) getDocuments(t *testing.T, target string) (*httptest.ResponseRecorder, documentList) {
	t.Helper()
	rec := e.do(t, "GET", target, "", nil)
	var l documentList
	if rec.Code == 200 {
		decodeJSON(t, rec, &l)
	}
	return rec, l
}

func apiDocNames(items []model.Document) string {
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	return strings.Join(names, ",")
}

// seedDocuments stores five documents in three projects: three of AGB (one in
// the archive and one without date, title and tags), one of OTH and one of THR.
func (e *env) seedDocuments(t *testing.T) {
	t.Helper()
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")
	e.mustPutDocument(t, "AGB", apiDocPriority, "history", apiDocText("decision",
		"우선순위 큐는 등급으로 정렬한다. 우선순위 등급은 P0부터 P3까지다. 큐 순서는 서버가 계산한다.",
		"date: 2026-10-20 15:44", "title: 우선순위 큐 모델은 등급과 서버 계산 순서로 한다", "tags: [priority, queue]"))
	e.mustPutDocument(t, "AGB", apiDocLinear, "history", apiDocText("report",
		"Linear는 여러 사람이 쓰는 도구다. 우선순위 비교 항목을 남긴다.",
		"date: 2026-10-19 17:00", "title: agb와 Linear 비교", "tags: [product, linear]"))
	e.mustPutDocument(t, "AGB", apiDocRelease, "archive", apiDocText("implementation",
		"배포 절차와 마이그레이션 확인.",
		"date: 2026-10-19 16:57", "title: v0.2.0 릴리즈", "tags: [release, migration]"))
	e.mustPutDocument(t, "AGB", apiDocBare, "history", apiDocText("decision", "메타데이터가 없는 결정 기록."))
	e.mustPutDocument(t, "OTH", apiDocLock, "history", apiDocText("incident",
		"쓰기 트랜잭션이 잠겨 요청이 실패했다.",
		"date: 2026-10-21 09:00", "title: 데이터베이스 잠금 사고", "tags: [sqlite, lock]"))
}

func TestPutDocumentCreatesAndRepeats(t *testing.T) {
	e := newEnv(t)
	// A clock that advances one second per stamp keeps the times of the writes apart.
	base, ticks := time.Date(2026, 10, 21, 2, 0, 0, 0, time.UTC), 0
	e.st.SetClock(func() time.Time { ticks++; return base.Add(time.Duration(ticks) * time.Second) })
	e.createProject(t, "AGB", "agent-board")
	// CRLF, tabs, Korean text and no final newline stay as they are.
	content := strings.TrimSuffix(apiDocText("decision", "# 본문\r\n\t탭 들여쓰기\r\n\r\n끝",
		"date: 2026-10-20 15:44", "title: 우선순위 큐 모델은 등급과 서버 계산 순서로 한다", "tags: [priority, queue]"), "\n")

	rec := e.putDocument(t, "AGB", apiDocPriority, "history", content, nil)
	if rec.Code != 201 || rec.Header().Get("Location") != documentPath("AGB", apiDocPriority) {
		t.Fatalf("create: %d %s Location=%q", rec.Code, rec.Body.String(), rec.Header().Get("Location"))
	}
	var d model.Document
	decodeJSON(t, rec, &d)
	if d.Project != "AGB" || d.Name != apiDocPriority || d.Location != model.LocationHistory || d.Kind != model.DocumentDecision ||
		d.SHA256 != apiDocSHA(content) || d.Size != len(content) || d.CreatedBy != "claude" || d.UpdatedBy != "claude" || d.CreatedAt != d.UpdatedAt {
		t.Errorf("document = %+v", d)
	}
	if d.Date == nil || *d.Date != "2026-10-20 15:44" || d.Title == nil || *d.Title != "우선순위 큐 모델은 등급과 서버 계산 순서로 한다" ||
		!reflect.DeepEqual(d.Tags, []string{"priority", "queue"}) {
		t.Errorf("metadata = date %v, title %v, tags %v", d.Date, d.Title, d.Tags)
	}
	// The object has the fields of the spec in its order, and no content.
	var keys []string
	dec := json.NewDecoder(strings.NewReader(rec.Body.String()))
	dec.Token()
	for dec.More() {
		tok, _ := dec.Token()
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		dec.Decode(&skip)
	}
	wantKeys := []string{"project", "name", "location", "kind", "date", "title", "tags", "sha256", "size", "created_at", "created_by", "updated_at", "updated_by"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("put response fields = %v, want %v", keys, wantKeys)
	}
	if rec.Header().Get("ETag") != "" {
		t.Errorf("a document has no version, so no ETag: %q", rec.Header().Get("ETag"))
	}

	// The same content and location again: 200 with the same object and no Location.
	again := e.putDocument(t, "AGB", apiDocPriority, "history", content, map[string]string{"X-Actor": "owner"})
	if again.Code != 200 || again.Body.String() != rec.Body.String() || again.Header().Get("Location") != "" {
		t.Errorf("repeat: %d %s Location=%q", again.Code, again.Body.String(), again.Header().Get("Location"))
	}

	// The same content in another folder moves the document.
	moved := e.putDocument(t, "AGB", apiDocPriority, "archive", content, map[string]string{"X-Actor": "owner"})
	var m model.Document
	decodeJSON(t, moved, &m)
	if moved.Code != 200 || moved.Header().Get("Location") != "" || m.Location != model.LocationArchive || m.UpdatedBy != "owner" || m.CreatedBy != "claude" ||
		m.CreatedAt != d.CreatedAt || !d.UpdatedAt.Before(m.UpdatedAt) || m.SHA256 != d.SHA256 {
		t.Errorf("move: %d %s", moved.Code, moved.Body.String())
	}

	// Other content is refused, with the hash of the stored content in the details.
	other := e.putDocument(t, "AGB", apiDocPriority, "archive", apiDocText("decision", "고친 본문", "title: 고침"), nil)
	var eb struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details []struct {
				Field   string `json:"field"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
				SHA256  string `json:"sha256"`
			} `json:"details"`
		} `json:"error"`
	}
	decodeJSON(t, other, &eb)
	if other.Code != 409 || eb.Error.Code != "conflict" || eb.Error.Message == "" || len(eb.Error.Details) != 1 ||
		eb.Error.Details[0].Reason != "immutable" || eb.Error.Details[0].SHA256 != d.SHA256 || eb.Error.Details[0].Field != "content" {
		t.Errorf("other content: %d %s", other.Code, other.Body.String())
	}

	// The original, byte for byte.
	got := e.do(t, "GET", documentPath("AGB", apiDocPriority), "", nil)
	var full model.Document
	decodeJSON(t, got, &full)
	if got.Code != 200 || full.Content == nil || *full.Content != content || full.Location != model.LocationArchive || full.SHA256 != d.SHA256 {
		t.Errorf("get: %d %s", got.Code, got.Body.String())
	}
	if !strings.Contains(got.Body.String(), `"size":`) || !strings.Contains(got.Body.String(), `"content":"---\nkind: decision\n`) {
		t.Errorf("get JSON lacks the content after the size: %s", got.Body.String())
	}
	if rec := e.do(t, "GET", "/v1/activity?project=AGB", "", nil); !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Errorf("a document write leaves no activity record: %s", rec.Body.String())
	}
}

func TestPutDocumentRules(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	ok := apiDocText("decision", "본문")
	head := apiDocText("decision", "")
	cases := []struct {
		label, name, location, content string
		want                           []string
	}{
		{"name without the pattern", "priority-model.md", "history", ok, []string{"name"}},
		{"unknown kind in the name", "2026-10-20-1544-plan-x.md", "history", apiDocText("plan", "본문"), []string{"name"}},
		{"upper case slug", "2026-10-20-1544-decision-Priority.md", "history", ok, []string{"name"}},
		{"kind of the front matter differs", "2026-10-20-1544-report-x.md", "history", ok, []string{"content"}},
		{"no front matter", apiDocPriority, "history", "# 제목\n본문\n", []string{"content"}},
		{"unclosed front matter", apiDocPriority, "history", "---\nkind: decision\n본문\n", []string{"content"}},
		{"empty content", apiDocPriority, "history", "", []string{"content"}},
		{"one byte over the limit", apiDocPriority, "history", head + strings.Repeat("a", model.MaxDocumentBytes-len(head)+1), []string{"content"}},
		{"unknown location", apiDocPriority, "draft", ok, []string{"location"}},
		{"empty location", apiDocPriority, "", ok, []string{"location"}},
		{"every violation at once", "x.md", "tmp", "본문", []string{"name", "location", "content"}},
	}
	for _, c := range cases {
		rec := e.putDocument(t, "AGB", c.name, c.location, c.content, nil)
		b := decodeErr(t, rec)
		var got []string
		for _, d := range b.Error.Details {
			got = append(got, d.Field)
			if d.Message == "" {
				t.Errorf("%s: detail without a message: %+v", c.label, d)
			}
		}
		if rec.Code != 422 || b.Error.Code != "validation_failed" || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %d %s", c.label, rec.Code, rec.Body.String())
		}
	}
	// A document of exactly the limit is accepted.
	if rec := e.putDocument(t, "AGB", apiDocPriority, "history", head+strings.Repeat("a", model.MaxDocumentBytes-len(head)), nil); rec.Code != 201 {
		t.Errorf("document at the limit: %d %s", rec.Code, rec.Body.String())
	}
	if rec, l := e.getDocuments(t, "/v1/projects/AGB/documents"); rec.Code != 200 || l.Total != 1 {
		t.Errorf("refused puts left documents behind: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPutDocumentBadRequests(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	good := apiDocText("decision", "본문")
	for label, c := range map[string]struct {
		body  string
		field string
	}{
		"unknown field":     {`{"location":"history","content":` + jsonString(good) + `,"sha256":"abc"}`, "sha256"},
		"a project field":   {`{"location":"history","content":` + jsonString(good) + `,"project":"AGB"}`, "project"},
		"location not text": {`{"location":1,"content":` + jsonString(good) + `}`, "location"},
		"content not text":  {`{"location":"history","content":5}`, "content"},
		"content is a list": {`{"location":"history","content":["a"]}`, "content"},
		"not JSON":          {`{"location":"history","content":`, ""},
		"not an object":     {`["history"]`, ""},
		"null body":         {`null`, ""},
	} {
		rec := e.do(t, "PUT", documentPath("AGB", apiDocPriority), c.body, nil)
		b := decodeErr(t, rec)
		if rec.Code != 400 || b.Error.Code != "bad_request" || (c.field != "" && (len(b.Error.Details) == 0 || b.Error.Details[0].Field != c.field)) {
			t.Errorf("%s: %d %s", label, rec.Code, rec.Body.String())
		}
	}
	// A missing body is not an object either.
	if rec := e.do(t, "PUT", documentPath("AGB", apiDocPriority), "", nil); rec.Code != 400 {
		t.Errorf("empty body: %d", rec.Code)
	}
	// Null fields are left for validation to report.
	rec := e.do(t, "PUT", documentPath("AGB", apiDocPriority), `{"location":null,"content":null}`, nil)
	b := decodeErr(t, rec)
	if rec.Code != 422 || len(b.Error.Details) != 2 || b.Error.Details[0].Field != "location" || b.Error.Details[1].Field != "content" {
		t.Errorf("null fields: %d %s", rec.Code, rec.Body.String())
	}
	// A body over 1MiB is refused before anything else.
	big := e.do(t, "PUT", documentPath("AGB", apiDocPriority), docRequest("history", good+strings.Repeat("a", 1<<20)), nil)
	if big.Code != 413 || decodeErr(t, big).Error.Code != "payload_too_large" {
		t.Errorf("large body: %d", big.Code)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestPutDocumentNeedsAnActiveProject(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	content := apiDocText("decision", "본문")
	e.mustPutDocument(t, "AGB", apiDocPriority, "history", content)

	rec := e.putDocument(t, "NOPE", apiDocPriority, "history", content, nil)
	if rec.Code != 404 || decodeErr(t, rec).Error.Code != "not_found" {
		t.Errorf("missing project: %d %s", rec.Code, rec.Body.String())
	}
	for _, status := range []string{"archived", "deleted"} {
		if rec := e.do(t, "PATCH", "/v1/projects/AGB", `{"status":"`+status+`"}`, nil); rec.Code != 200 {
			t.Fatalf("%s: %d %s", status, rec.Code, rec.Body.String())
		}
		for label, put := range map[string]struct{ name, content string }{
			"new":     {apiDocLinear, apiDocText("report", "본문")},
			"same":    {apiDocPriority, content},
			"invalid": {"x.md", "본문"},
		} {
			rec := e.putDocument(t, "AGB", put.name, "history", put.content, nil)
			b := decodeErr(t, rec)
			if rec.Code != 409 || b.Error.Code != "conflict" || len(b.Error.Details) != 1 || b.Error.Details[0].Reason != "project_not_active" {
				t.Errorf("%s project, %s document: %d %s", status, label, rec.Code, rec.Body.String())
			}
		}
		// Reading still works.
		if rec := e.do(t, "GET", documentPath("AGB", apiDocPriority), "", nil); rec.Code != 200 {
			t.Errorf("%s project: get %d", status, rec.Code)
		}
		if rec, l := e.getDocuments(t, "/v1/projects/AGB/documents"); rec.Code != 200 || l.Total != 1 {
			t.Errorf("%s project: list %d %s", status, rec.Code, rec.Body.String())
		}
	}
}

func TestGetDocument(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.mustPutDocument(t, "AGB", apiDocBare, "history", apiDocText("decision", "본문"))

	rec := e.do(t, "GET", documentPath("AGB", apiDocBare), "", nil)
	var d model.Document
	decodeJSON(t, rec, &d)
	if rec.Code != 200 || d.Content == nil || d.Date != nil || d.Title != nil || d.Tags == nil || len(d.Tags) != 0 {
		t.Errorf("get: %d %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`"date":null`, `"title":null`, `"tags":[]`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("JSON lacks %s: %s", want, rec.Body.String())
		}
	}
	for label, target := range map[string]string{
		"missing document":   documentPath("AGB", apiDocLinear),
		"name off pattern":   documentPath("AGB", "priority-model.md"),
		"missing project":    documentPath("NOPE", apiDocBare),
		"lower case project": documentPath("agb", apiDocBare),
	} {
		rec := e.do(t, "GET", target, "", nil)
		if rec.Code != 404 || decodeErr(t, rec).Error.Code != "not_found" {
			t.Errorf("%s: %d %s", label, rec.Code, rec.Body.String())
		}
	}
	if rec := e.do(t, "GET", documentPath("AGB", apiDocBare)+"?content=false", "", nil); rec.Code != 400 {
		t.Errorf("unknown query parameter: %d", rec.Code)
	}
}

func TestListProjectDocuments(t *testing.T) {
	e := newEnv(t)
	e.seedDocuments(t)

	rec, l := e.getDocuments(t, "/v1/projects/AGB/documents")
	want := strings.Join([]string{apiDocBare, apiDocRelease, apiDocLinear, apiDocPriority}, ",")
	if rec.Code != 200 || apiDocNames(l.Items) != want || l.Total != 4 || l.Limit != 50 || l.Offset != 0 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"content"`) || strings.Contains(rec.Body.String(), `"snippet"`) {
		t.Errorf("list items carry no content: %s", rec.Body.String())
	}
	if l.Items[1].Location != model.LocationArchive || l.Items[3].SHA256 == "" || l.Items[3].Size == 0 {
		t.Errorf("items = %+v", l.Items)
	}

	for target, c := range map[string]struct {
		names        string
		total, limit int
		offset       int
	}{
		"?limit=2":            {strings.Join([]string{apiDocBare, apiDocRelease}, ","), 4, 2, 0},
		"?limit=2&offset=2":   {strings.Join([]string{apiDocLinear, apiDocPriority}, ","), 4, 2, 2},
		"?limit=2&offset=3":   {apiDocPriority, 4, 2, 3},
		"?offset=4":           {"", 4, 50, 4},
		"?limit=500":          {want, 4, 500, 0},
		"?limit=1&offset=100": {"", 4, 1, 100},
	} {
		rec, l := e.getDocuments(t, "/v1/projects/AGB/documents"+target)
		if rec.Code != 200 || apiDocNames(l.Items) != c.names || l.Total != c.total || l.Limit != c.limit || l.Offset != c.offset {
			t.Errorf("%s: %d %s", target, rec.Code, rec.Body.String())
		}
	}
	if rec := e.do(t, "GET", "/v1/projects/AGB/documents?offset=4", "", nil); !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("an empty page is [] not null: %s", rec.Body.String())
	}
	for _, q := range []string{"?limit=0", "?limit=501", "?limit=abc", "?offset=-1", "?kind=decision", "?sort=name", "?project=OTH", "?q=x", "?limit=1&limit=2"} {
		if rec := e.do(t, "GET", "/v1/projects/AGB/documents"+q, "", nil); rec.Code != 400 || decodeErr(t, rec).Error.Code != "bad_request" {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body.String())
		}
	}
	if rec := e.do(t, "GET", "/v1/projects/NOPE/documents", "", nil); rec.Code != 404 {
		t.Errorf("missing project: %d", rec.Code)
	}
	e.createProject(t, "EMP", "empty")
	if rec := e.do(t, "GET", "/v1/projects/EMP/documents", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"items":[]`) || !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Errorf("empty project: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSearchDocuments(t *testing.T) {
	e := newEnv(t)
	e.seedDocuments(t)
	search := func(query string) (*httptest.ResponseRecorder, documentList) {
		t.Helper()
		return e.getDocuments(t, "/v1/documents"+query)
	}
	names := func(query string) string {
		t.Helper()
		rec, l := search(query)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", query, rec.Code, rec.Body.String())
		}
		return apiDocNames(l.Items)
	}
	join := func(n ...string) string { return strings.Join(n, ",") }

	// Without a query: newest first by the date of the front matter, across the projects;
	// a document without a date is last.
	rec, l := search("")
	all := join(apiDocLock, apiDocPriority, apiDocLinear, apiDocRelease, apiDocBare)
	if rec.Code != 200 || apiDocNames(l.Items) != all || l.Total != 5 || l.Limit != 50 || l.Offset != 0 {
		t.Fatalf("all: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"content"`) || strings.Contains(rec.Body.String(), `"snippet"`) {
		t.Errorf("results without a query carry no content or snippet: %s", rec.Body.String())
	}
	if l.Items[0].Project != "OTH" || l.Items[1].Project != "AGB" {
		t.Errorf("the projects of the results = %s, %s", l.Items[0].Project, l.Items[1].Project)
	}

	// A query: by relevance, with a snippet that marks the match.
	rec, l = search("?q=" + url.QueryEscape("우선순위"))
	if rec.Code != 200 || apiDocNames(l.Items) != join(apiDocPriority, apiDocLinear) || l.Total != 2 {
		t.Fatalf("q: %d %s", rec.Code, rec.Body.String())
	}
	for _, it := range l.Items {
		if it.Snippet == nil || !strings.Contains(*it.Snippet, "[우선순위]") || it.Content != nil {
			t.Errorf("%s: snippet %v, content %v", it.Name, it.Snippet, it.Content)
		}
	}

	for query, want := range map[string]string{
		"?q=" + url.QueryEscape("잠금"): apiDocLock,
		"?q=queue":                    apiDocPriority,
		"?q=" + url.QueryEscape("우선순위 서버"):  apiDocPriority,
		"?q=" + url.QueryEscape(`"우선순위 큐"`): apiDocPriority,
		"?q=%20%20": all,
		"?q=" + url.QueryEscape("없는검색어"): "",
		"?q=agent-board":                              "",
		"?project=OTH":                                apiDocLock,
		"?project=OTH,AGB":                            all,
		"?project=NOPE":                               "",
		"?kind=decision":                              join(apiDocPriority, apiDocBare),
		"?kind=decision,report":                       join(apiDocPriority, apiDocLinear, apiDocBare),
		"?location=archive":                           apiDocRelease,
		"?location=history,archive":                   all,
		"?project=AGB&kind=decision&location=history": join(apiDocPriority, apiDocBare),
		"?q=" + url.QueryEscape("우선순위") + "&kind=report":    apiDocLinear,
		"?q=" + url.QueryEscape("배포") + "&location=archive": apiDocRelease,
		"?q=" + url.QueryEscape("배포") + "&location=history": "",
		"?q=" + url.QueryEscape("우선순위") + "&project=OTH":    "",
	} {
		if got := names(query); got != want {
			t.Errorf("%s: %q, want %q", query, got, want)
		}
	}

	rec, l = search("?limit=2&offset=1")
	if rec.Code != 200 || apiDocNames(l.Items) != join(apiDocPriority, apiDocLinear) || l.Total != 5 || l.Limit != 2 || l.Offset != 1 {
		t.Errorf("page: %d %s", rec.Code, rec.Body.String())
	}
	rec, l = search("?q=" + url.QueryEscape("우선순위") + "&limit=1&offset=1")
	if rec.Code != 200 || apiDocNames(l.Items) != apiDocLinear || l.Total != 2 {
		t.Errorf("page of a search: %d %s", rec.Code, rec.Body.String())
	}
	if rec, _ := search("?q=" + url.QueryEscape("없는검색어")); !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("no match is [] not null: %s", rec.Body.String())
	}

	for _, query := range []string{
		"?kind=plan", "?kind=decision,plan", "?location=draft", "?project=agb", "?project=A", "?project=AGB,x",
		"?kind=decision&kind=report", "?project=AGB&project=OTH", "?q=a&q=b", "?limit=0", "?limit=201", "?offset=-1",
		"?sort=date", "?status=all", "?content=true",
		"?q=" + url.QueryEscape(strings.Repeat("a ", 11)),
	} {
		rec := e.do(t, "GET", "/v1/documents"+query, "", nil)
		if rec.Code != 400 || decodeErr(t, rec).Error.Code != "bad_request" {
			t.Errorf("%s: %d %s", query, rec.Code, rec.Body.String())
		}
	}
	// Ten terms are the most the issue search takes; the document search takes the same.
	if got := names("?q=" + url.QueryEscape(strings.Repeat("zzz ", 10))); got != "" {
		t.Errorf("ten terms: %q", got)
	}
	if rec, l := search("?limit=200"); rec.Code != 200 || l.Limit != 200 {
		t.Errorf("limit 200: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDocumentRoutesFollowTheConventions(t *testing.T) {
	e := newEnv(t)
	e.seedDocuments(t)
	for _, c := range []struct {
		method, target, allow string
	}{
		{"POST", documentPath("AGB", apiDocPriority), "GET, PUT"},
		{"PATCH", documentPath("AGB", apiDocPriority), "GET, PUT"},
		{"DELETE", documentPath("AGB", apiDocPriority), "GET, PUT"},
		{"PUT", "/v1/projects/AGB/documents", "GET"},
		{"POST", "/v1/projects/AGB/documents", "GET"},
		{"DELETE", "/v1/projects/AGB/documents", "GET"},
		{"PUT", "/v1/documents", "GET"},
		{"POST", "/v1/documents", "GET"},
		{"DELETE", "/v1/documents", "GET"},
	} {
		rec := e.do(t, c.method, c.target, `{}`, nil)
		if rec.Code != 405 || rec.Header().Get("Allow") != c.allow || decodeErr(t, rec).Error.Code != "method_not_allowed" {
			t.Errorf("%s %s: %d Allow=%q", c.method, c.target, rec.Code, rec.Header().Get("Allow"))
		}
	}
	for _, c := range []struct{ method, target, body string }{
		{"GET", documentPath("AGB", apiDocPriority), ""},
		{"PUT", documentPath("AGB", apiDocPriority), docRequest("history", apiDocText("decision", "본문"))},
		{"GET", "/v1/projects/AGB/documents", ""},
		{"GET", "/v1/documents", ""},
	} {
		if rec := e.do(t, c.method, c.target, c.body, map[string]string{"Authorization": "-"}); rec.Code != 401 {
			t.Errorf("%s %s without a token: %d", c.method, c.target, rec.Code)
		}
		for _, hdr := range []string{"X-Actor", "X-Session-Id", "X-Client-Host"} {
			if rec := e.do(t, c.method, c.target, c.body, map[string]string{hdr: "-"}); rec.Code != 400 {
				t.Errorf("%s %s without %s: %d", c.method, c.target, hdr, rec.Code)
			}
		}
		if rec := e.do(t, c.method, c.target, c.body, map[string]string{"X-Actor": "gemini"}); rec.Code != 400 {
			t.Errorf("%s %s as an undeclared actor: %d", c.method, c.target, rec.Code)
		}
	}
	// A user and an agent both write documents, and the request log names them.
	if rec := e.putDocument(t, "AGB", "2026-10-22-0900-report-by-owner.md", "history", apiDocText("report", "본문"), map[string]string{"X-Actor": "owner"}); rec.Code != 201 {
		t.Errorf("put as owner: %d %s", rec.Code, rec.Body.String())
	}
	if lg := e.lastLog(t); lg["method"] != "PUT" || lg["actor"] != "owner" || lg["status"] != float64(201) {
		t.Errorf("log = %v", lg)
	}
}
