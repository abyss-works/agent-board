//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// document covers the document in every shape the API gives it: the put
// result, the list item (neither has Content), the single document (Content)
// and the search item (Snippet).
type document struct {
	Project   string   `json:"project"`
	Name      string   `json:"name"`
	Location  string   `json:"location"`
	Kind      string   `json:"kind"`
	Date      *string  `json:"date"`
	Title     *string  `json:"title"`
	Tags      []string `json:"tags"`
	SHA256    string   `json:"sha256"`
	Size      int      `json:"size"`
	Content   *string  `json:"content"`
	CreatedAt string   `json:"created_at"`
	CreatedBy string   `json:"created_by"`
	UpdatedAt string   `json:"updated_at"`
	UpdatedBy string   `json:"updated_by"`
	Snippet   *string  `json:"snippet"`
}

type documentConflict struct {
	Error struct {
		Code    string `json:"code"`
		Details []struct {
			Field  string `json:"field"`
			Reason string `json:"reason"`
			SHA256 string `json:"sha256"`
		} `json:"details"`
	} `json:"error"`
}

const maxDocumentBytes = 262144

func contentSHA(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// documentText builds a record document. Empty date, title and tags are left
// out of the front matter.
func documentText(kind, date, title, tags, body string) string {
	var b strings.Builder
	b.WriteString("---\nkind: " + kind + "\n")
	for _, kv := range [][2]string{{"date", date}, {"title", title}, {"tags", tags}} {
		if kv[1] != "" {
			b.WriteString(kv[0] + ": " + kv[1] + "\n")
		}
	}
	b.WriteString("---\n\n" + body + "\n")
	return b.String()
}

func documentPath(project, name string) string {
	return "/v1/projects/" + project + "/documents/" + name
}

func putDocument(t testing.TB, c caller, project, name, location, content string) response {
	t.Helper()
	return c.do(t, "PUT", documentPath(project, name), map[string]any{"location": location, "content": content})
}

// mustPutDocument stores a document and returns it with the status of the put.
func mustPutDocument(t testing.TB, c caller, project, name, location, content string) (document, int) {
	t.Helper()
	r := putDocument(t, c, project, name, location, content)
	if r.status != 200 && r.status != 201 {
		t.Fatalf("put %s/%s: %s", project, name, r)
	}
	return decode[document](t, r), r.status
}

func documentNames(items []document) string {
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	return strings.Join(names, ",")
}

// jsonKeys returns the sorted keys of the JSON object in body.
func jsonKeys(t testing.TB, body []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("not a JSON object: %v (%s)", err, body)
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestDocuments(t *testing.T) {
	p := createProject(t, "DA")
	q := createProject(t, "DB")
	// A word no other run has, so a search over the shared database finds this run's documents only.
	word := "e2e" + strings.ToLower(runID)

	const (
		nDecision = "2026-10-20-1544-decision-priority-model.md"
		nReport   = "2026-10-19-1700-report-linear-gap.md"
		nRelease  = "2026-10-19-1657-implementation-v020-release.md"
		nBare     = "2026-10-18-1000-decision-no-meta.md"
		nLock     = "2026-10-21-0900-incident-db-lock.md"
		nAtLimit  = "2026-10-20-1544-decision-at-limit.md"
		nKorean   = "2026-10-20-1544-decision-korean.md"
	)
	// CRLF, tabs, markup characters, Korean text and no final newline stay as they are.
	decision := strings.TrimSuffix(documentText("decision", "2026-10-20 15:44", "우선순위 큐 모델은 등급과 서버 계산 순서로 한다 "+word, "[priority, queue]",
		"우선순위 큐는 등급으로 정렬한다.\r\n\t"+word+" "+word+" "+word+" <b>굵게</b> & 끝"), "\n")
	report := documentText("report", "2026-10-19 17:00", "agb와 Linear 비교", "[product, linear]", "Linear는 여러 사람이 쓰는 도구다. 우선순위 비교 "+word)
	release := documentText("implementation", "2026-10-19 16:57", "v0.2.0 릴리즈", "[release]", "배포 절차 "+word)
	bare := documentText("decision", "", "", "", "메타데이터가 없는 기록 "+word)
	lock := documentText("incident", "2026-10-21 09:00", "데이터베이스 잠금 사고", "[sqlite, lock]", "쓰기 트랜잭션이 잠겼다. "+word)

	t.Run("register, repeat, move", func(t *testing.T) {
		r := putDocument(t, as("claude"), p.Key, nDecision, "history", decision)
		d := decodeAs[document](t, r, 201)
		if r.header.Get("Location") != documentPath(p.Key, nDecision) {
			t.Errorf("Location = %q", r.header.Get("Location"))
		}
		if d.Project != p.Key || d.Name != nDecision || d.Location != "history" || d.Kind != "decision" || d.SHA256 != contentSHA(decision) || d.Size != len(decision) ||
			d.CreatedBy != "claude" || d.UpdatedBy != "claude" || d.CreatedAt != d.UpdatedAt || d.Content != nil || d.Snippet != nil {
			t.Errorf("document = %+v", d)
		}
		if d.Date == nil || *d.Date != "2026-10-20 15:44" || d.Title == nil || !strings.HasPrefix(*d.Title, "우선순위 큐 모델은") || !reflect.DeepEqual(d.Tags, []string{"priority", "queue"}) {
			t.Errorf("metadata = date %v, title %v, tags %v", d.Date, d.Title, d.Tags)
		}
		wantTime(t, "created_at", d.CreatedAt)
		wantTime(t, "updated_at", d.UpdatedAt)
		wantKeys := []string{"created_at", "created_by", "date", "kind", "location", "name", "project", "sha256", "size", "tags", "title", "updated_at", "updated_by"}
		if got := jsonKeys(t, r.body); !reflect.DeepEqual(got, wantKeys) {
			t.Errorf("put result fields = %v, want %v", got, wantKeys)
		}

		// The same content and location again, by another actor: 200, the same object, no Location.
		again := owner.do(t, "PUT", documentPath(p.Key, nDecision), map[string]any{"location": "history", "content": decision})
		expect(t, again, 200)
		if string(again.body) != string(r.body) || again.header.Get("Location") != "" {
			t.Errorf("repeat = %s Location=%q, want %s", again.body, again.header.Get("Location"), r.body)
		}

		// The same content in the archive moves the document.
		tick()
		moved := decodeAs[document](t, putDocument(t, owner, p.Key, nDecision, "archive", decision), 200)
		if moved.Location != "archive" || moved.UpdatedBy != "owner" || moved.CreatedBy != "claude" || moved.CreatedAt != d.CreatedAt || moved.UpdatedAt <= d.UpdatedAt || moved.SHA256 != d.SHA256 {
			t.Errorf("moved = %+v", moved)
		}
		back := decodeAs[document](t, putDocument(t, owner, p.Key, nDecision, "history", decision), 200)
		if back.Location != "history" {
			t.Errorf("moved back = %+v", back)
		}

		// The original, byte for byte, in its folder.
		one := expect(t, owner.do(t, "GET", documentPath(p.Key, nDecision), nil), 200)
		full := decode[document](t, one)
		if full.Content == nil || *full.Content != decision || full.SHA256 != d.SHA256 || full.Location != "history" {
			t.Errorf("single document = %s", one)
		}
		wantFull := append(append([]string{}, wantKeys...), "content")
		sort.Strings(wantFull)
		if got := jsonKeys(t, one.body); !reflect.DeepEqual(got, wantFull) {
			t.Errorf("single document fields = %v, want %v", got, wantFull)
		}

		// A document is a project and a name: the same name in another project is another document.
		other := documentText("decision", "2026-10-20 15:44", "다른 프로젝트의 같은 이름", "", "내용이 다르다 "+word)
		if _, status := mustPutDocument(t, owner, q.Key, nDecision, "history", other); status != 201 {
			t.Errorf("same name in another project: status %d", status)
		}
		// A document leaves no activity record.
		acts := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/activity?project="+p.Key, nil), 200)
		if acts.Total != 0 {
			t.Errorf("activity records of a project with documents = %d", acts.Total)
		}
	})

	t.Run("a document is never changed", func(t *testing.T) {
		stored := decode[document](t, owner.do(t, "GET", documentPath(p.Key, nDecision), nil))
		for label, c := range map[string]struct{ location, content string }{
			"other body":              {"history", strings.Replace(decision, "굵게", "가늘게", 1)},
			"a trailing newline more": {"history", decision + "\n"},
			"other line breaks":       {"history", strings.ReplaceAll(decision, "\r\n", "\n")},
			"other content and move":  {"archive", decision + " "},
		} {
			r := putDocument(t, as("claude"), p.Key, nDecision, c.location, c.content)
			wantError(t, r, 409, "conflict")
			var e documentConflict
			json.Unmarshal(r.body, &e)
			if len(e.Error.Details) != 1 || e.Error.Details[0].Reason != "immutable" || e.Error.Details[0].Field != "content" || e.Error.Details[0].SHA256 != stored.SHA256 {
				t.Errorf("%s: details = %+v", label, e.Error.Details)
			}
		}
		after := decode[document](t, owner.do(t, "GET", documentPath(p.Key, nDecision), nil))
		if !reflect.DeepEqual(after, stored) {
			t.Errorf("a refused put changed the document:\n got %+v\nwant %+v", after, stored)
		}
		// There is nothing to edit or delete.
		for _, method := range []string{"PATCH", "DELETE", "POST"} {
			r := owner.do(t, method, documentPath(p.Key, nDecision), map[string]any{})
			wantError(t, r, 405, "method_not_allowed")
			if r.header.Get("Allow") != "GET, PUT" {
				t.Errorf("%s: Allow = %q", method, r.header.Get("Allow"))
			}
		}
		for _, method := range []string{"PUT", "PATCH", "DELETE", "POST"} {
			r := owner.do(t, method, "/v1/projects/"+p.Key+"/documents", map[string]any{})
			wantError(t, r, 405, "method_not_allowed")
			if r.header.Get("Allow") != "GET" {
				t.Errorf("%s list: Allow = %q", method, r.header.Get("Allow"))
			}
		}
		wantError(t, owner.do(t, "POST", "/v1/documents", map[string]any{}), 405, "method_not_allowed")
	})

	t.Run("rules", func(t *testing.T) {
		ok := documentText("decision", "", "", "", "본문")
		head := documentText("decision", "", "", "", "")
		put := func(name, location, content string) response {
			return putDocument(t, owner, p.Key, name, location, content)
		}
		for _, c := range []struct {
			label, name, location, content string
			field                          string
		}{
			{"name without the pattern", "priority-model.md", "history", ok, "name"},
			{"unknown kind in the name", "2026-10-20-1544-plan-x.md", "history", documentText("plan", "", "", "", "본문"), "name"},
			{"upper case slug", "2026-10-20-1544-decision-Priority.md", "history", ok, "name"},
			{"slug with an underscore", "2026-10-20-1544-decision-a_b.md", "history", ok, "name"},
			{"not markdown", "2026-10-20-1544-decision-x.txt", "history", ok, "name"},
			{"kind of the front matter differs", "2026-10-20-1544-report-x.md", "history", ok, "content"},
			{"no front matter", "2026-10-20-1544-decision-x.md", "history", "# 제목\n본문\n", "content"},
			{"unclosed front matter", "2026-10-20-1544-decision-x.md", "history", "---\nkind: decision\n본문\n", "content"},
			{"front matter after a blank line", "2026-10-20-1544-decision-x.md", "history", "\n" + ok, "content"},
			{"front matter without kind", "2026-10-20-1544-decision-x.md", "history", "---\ntitle: 제목\n---\n본문\n", "content"},
			{"empty content", "2026-10-20-1544-decision-x.md", "history", "", "content"},
			{"one byte over the limit", "2026-10-20-1544-decision-x.md", "history", head + strings.Repeat("a", maxDocumentBytes-len(head)+1), "content"},
			{"bytes, not characters", "2026-10-20-1544-decision-x.md", "history", head + strings.Repeat("가", (maxDocumentBytes-len(head))/3+1), "content"},
			{"unknown location", "2026-10-20-1544-decision-x.md", "draft", ok, "location"},
			{"empty location", "2026-10-20-1544-decision-x.md", "", ok, "location"},
		} {
			e := wantError(t, put(c.name, c.location, c.content), 422, "validation_failed")
			if !e.hasField(c.field) {
				t.Errorf("%s: details = %+v, want the field %s", c.label, e.Error.Details, c.field)
			}
		}
		e := wantError(t, put("x.md", "tmp", "본문"), 422, "validation_failed")
		if got := e.fields(); !reflect.DeepEqual(got, []string{"content", "location", "name"}) {
			t.Errorf("every violation at once = %v", got)
		}
		wantError(t, owner.do(t, "PUT", documentPath(p.Key, "2026-10-20-1544-decision-x.md"), map[string]any{"location": nil, "content": nil}), 422, "validation_failed")

		// Exactly the limit is accepted, and 256KB of Korean text, which is three bytes a character.
		if d, status := mustPutDocument(t, owner, p.Key, nAtLimit, "history", head+strings.Repeat("a", maxDocumentBytes-len(head))); status != 201 || d.Size != maxDocumentBytes {
			t.Errorf("at the limit: status %d, size %d", status, d.Size)
		}
		korean := head + strings.Repeat("가", (maxDocumentBytes-len(head))/3)
		if d, status := mustPutDocument(t, owner, p.Key, nKorean, "history", korean); status != 201 || d.Size != len(korean) {
			t.Errorf("korean at the limit: status %d, size %d", status, d.Size)
		}

		// The body of the request is a JSON object with the two fields.
		path := documentPath(p.Key, "2026-10-20-1544-decision-x.md")
		for _, body := range []map[string]any{
			{"location": "history", "content": ok, "sha256": "abc"},
			{"location": "history", "content": ok, "project": p.Key},
			{"location": 1, "content": ok},
			{"location": "history", "content": 5},
		} {
			wantError(t, owner.do(t, "PUT", path, body), 400, "bad_request")
		}
		wantError(t, owner.doRaw(t, "PUT", path, []byte(`{"location":"history","content":`)), 400, "bad_request")
		wantError(t, owner.doRaw(t, "PUT", path, []byte(`["history"]`)), 400, "bad_request")
		huge := owner.do(t, "PUT", path, map[string]any{"location": "history", "content": head + strings.Repeat("a", 1<<20)})
		wantError(t, huge, 413, "payload_too_large")
		wantError(t, putDocument(t, owner, projectKey("NOPE"), "2026-10-20-1544-decision-x.md", "history", ok), 404, "not_found")

		if total := decodeAs[list[document]](t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/documents", nil), 200).Total; total != 3 {
			t.Errorf("documents after the refused puts = %d, want the first one and the 2 at the limit", total)
		}
	})

	// The rest works on the same five documents.
	for _, d := range []struct {
		project, name, location, content string
	}{
		{p.Key, nReport, "history", report}, {p.Key, nRelease, "archive", release}, {p.Key, nBare, "history", bare}, {q.Key, nLock, "history", lock},
	} {
		if _, status := mustPutDocument(t, as("claude"), d.project, d.name, d.location, d.content); status != 201 {
			t.Fatalf("put %s: status %d", d.name, status)
		}
	}

	t.Run("list and get", func(t *testing.T) {
		base := "/v1/projects/" + p.Key + "/documents"
		// The documents of the project by name; the two at the size limit are among them.
		all := decodeAs[list[document]](t, owner.do(t, "GET", base+"?limit=500", nil), 200)
		names := documentNames(all.Items)
		wantPrefix := strings.Join([]string{nBare, nRelease, nReport}, ",") + ","
		if all.Total != 6 || all.Limit != 500 || !strings.HasPrefix(names, wantPrefix) || !strings.Contains(names, nAtLimit) || !strings.Contains(names, nKorean) {
			t.Fatalf("list = %s (total %d)", names, all.Total)
		}
		if !sort.StringsAreSorted(strings.Split(names, ",")) {
			t.Errorf("the list is not by name: %s", names)
		}
		raw := owner.do(t, "GET", base+"?limit=500", nil)
		if strings.Contains(string(raw.body), `"content"`) || strings.Contains(string(raw.body), `"snippet"`) {
			t.Errorf("list items carry no content or snippet")
		}
		for _, it := range all.Items {
			if it.Name == nRelease && (it.Location != "archive" || it.Kind != "implementation") {
				t.Errorf("release = %+v", it)
			}
			if it.Name == nBare && (it.Date != nil || it.Title != nil || it.Tags == nil || len(it.Tags) != 0) {
				t.Errorf("document without metadata = %+v", it)
			}
		}
		def := decodeAs[list[document]](t, owner.do(t, "GET", base, nil), 200)
		if def.Limit != 50 || def.Offset != 0 {
			t.Errorf("defaults: limit %d, offset %d", def.Limit, def.Offset)
		}
		page := decodeAs[list[document]](t, owner.do(t, "GET", base+"?limit=2&offset=1", nil), 200)
		if documentNames(page.Items) != strings.Join([]string{nRelease, nReport}, ",") || page.Total != 6 || page.Limit != 2 || page.Offset != 1 {
			t.Errorf("page = %s (total %d, limit %d, offset %d)", documentNames(page.Items), page.Total, page.Limit, page.Offset)
		}
		past := owner.do(t, "GET", base+"?offset=100", nil)
		if got := decodeAs[list[document]](t, past, 200); len(got.Items) != 0 || got.Total != 6 || !strings.Contains(string(past.body), `"items":[]`) {
			t.Errorf("a page past the end = %s", past)
		}
		for _, bad := range []string{"?limit=0", "?limit=501", "?limit=x", "?offset=-1", "?kind=decision", "?sort=name", "?q=x", "?limit=1&limit=2"} {
			wantError(t, owner.do(t, "GET", base+bad, nil), 400, "bad_request")
		}
		wantError(t, owner.do(t, "GET", "/v1/projects/"+projectKey("NOPE")+"/documents", nil), 404, "not_found")
		empty := createProject(t, "DC")
		if got := decodeAs[list[document]](t, owner.do(t, "GET", "/v1/projects/"+empty.Key+"/documents", nil), 200); got.Total != 0 || got.Items == nil {
			t.Errorf("project without documents = %+v", got)
		}

		for _, missing := range []string{documentPath(p.Key, nLock), documentPath(p.Key, "priority-model.md"), documentPath(projectKey("NOPE"), nBare), documentPath(strings.ToLower(p.Key), nBare)} {
			wantError(t, owner.do(t, "GET", missing, nil), 404, "not_found")
		}
		wantError(t, owner.do(t, "GET", documentPath(p.Key, nBare)+"?content=false", nil), 400, "bad_request")
		single := decodeAs[document](t, owner.do(t, "GET", documentPath(q.Key, nDecision), nil), 200)
		if single.Content == nil || !strings.Contains(*single.Content, "내용이 다르다") || single.Project != q.Key {
			t.Errorf("the same name in the other project = %+v", single)
		}
	})

	t.Run("search", func(t *testing.T) {
		projects := p.Key + "," + q.Key
		get := func(query string) list[document] {
			t.Helper()
			return decodeAs[list[document]](t, owner.do(t, "GET", "/v1/documents?"+query, nil), 200)
		}
		names := func(query string) string { return documentNames(get(query).Items) }
		join := func(n ...string) string { return strings.Join(n, ",") }
		scope := "project=" + projects + "&"

		// Without a query: newest first by the date of the front matter; no date last.
		// The two documents at the size limit have no date either and sort after the dated ones.
		withoutQuery := get(scope + "limit=200")
		if !strings.HasPrefix(documentNames(withoutQuery.Items), join(nLock, nDecision, nDecision, nReport, nRelease)) || withoutQuery.Total != 8 {
			t.Errorf("newest first = %s (total %d)", documentNames(withoutQuery.Items), withoutQuery.Total)
		}
		if withoutQuery.Items[0].Project != q.Key || withoutQuery.Items[1].Project != p.Key && withoutQuery.Items[2].Project != p.Key {
			t.Errorf("projects of the first results = %s %s %s", withoutQuery.Items[0].Project, withoutQuery.Items[1].Project, withoutQuery.Items[2].Project)
		}
		raw := owner.do(t, "GET", "/v1/documents?"+scope+"limit=200", nil)
		if strings.Contains(string(raw.body), `"content"`) || strings.Contains(string(raw.body), `"snippet"`) {
			t.Errorf("results without a query carry no content or snippet")
		}
		if got := get("project=" + p.Key + "&limit=200"); got.Limit != 200 || got.Total != 6 {
			t.Errorf("one project = %+v", got)
		}

		// A query: by relevance, the document that is about the word first, with a snippet.
		r := owner.do(t, "GET", "/v1/documents?"+scope+"q="+url.QueryEscape(word), nil)
		hits := decodeAs[list[document]](t, r, 200)
		if hits.Total != 6 || documentNames(hits.Items[:1]) != nDecision || hits.Items[0].Project != p.Key {
			t.Fatalf("q=%s: %s (total %d)", word, documentNames(hits.Items), hits.Total)
		}
		for _, it := range hits.Items {
			if it.Snippet == nil || !strings.Contains(*it.Snippet, "["+word) || it.Content != nil {
				t.Errorf("%s: snippet %v", it.Name, it.Snippet)
			}
		}
		if got := hits.Items[0]; got.SHA256 != contentSHA(decision) {
			t.Errorf("first hit = %+v", got)
		}
		// The word is in six documents; the name of the decision is in two projects.
		for query, want := range map[string]string{
			"q=" + url.QueryEscape(word+" 잠금"):                           nLock,
			"q=" + url.QueryEscape(word+" sqlite"):                       nLock,
			"q=" + url.QueryEscape(word+" 트랜잭션"):                         nLock,
			"q=" + url.QueryEscape(word+" queue"):                        nDecision,
			"q=" + url.QueryEscape(word+" priority"):                     join(nDecision, nDecision),
			"q=" + url.QueryEscape(word+" 우선순위 큐"):                       nDecision,
			"q=" + url.QueryEscape(`"우선순위 큐"`):                           nDecision,
			"q=" + url.QueryEscape(word+" 존재하지않는검색어"):                    "",
			"q=" + url.QueryEscape(word) + "&kind=report":                nReport,
			"q=" + url.QueryEscape(word) + "&location=archive":           nRelease,
			"q=" + url.QueryEscape(word) + "&kind=report,implementation": join(nRelease, nReport),
			"q=" + url.QueryEscape(word) + "&kind=incident":              nLock,
			"q=%20%20&kind=incident":                                     nLock,
		} {
			if got := names(scope + query); sortedJoin(got) != sortedJoin(want) {
				t.Errorf("%s: %q, want %q", query, got, want)
			}
		}
		// The filters, without a query.
		for query, want := range map[string]string{
			"project=" + q.Key: join(nLock, nDecision),
			"project=" + p.Key + "&kind=decision&limit=2":                     join(nDecision, nKorean),
			"project=" + projects + "&kind=incident":                          nLock,
			"project=" + p.Key + "&location=archive":                          nRelease,
			"project=" + p.Key + "&kind=report,implementation":                join(nReport, nRelease),
			"project=" + projects + "&location=archive,history&kind=incident": nLock,
			"project=" + projectKey("NOPE"):                                   "",
		} {
			if got := names(query); got != want {
				t.Errorf("%s: %q, want %q", query, got, want)
			}
		}
		page := get(scope + "limit=2&offset=1")
		if page.Total != 8 || page.Limit != 2 || page.Offset != 1 || len(page.Items) != 2 {
			t.Errorf("page = %+v", page)
		}
		hitPage := get(scope + "q=" + url.QueryEscape(word) + "&limit=2&offset=4")
		if hitPage.Total != 6 || len(hitPage.Items) != 2 {
			t.Errorf("page of a search = %+v", hitPage)
		}
		for _, bad := range []string{
			"kind=plan", "kind=decision,plan", "location=draft", "project=agb", "project=A", "project=" + p.Key + ",x", "kind=decision&kind=report",
			"q=a&q=b", "limit=0", "limit=201", "offset=-1", "sort=date", "status=all", "content=true", "q=" + url.QueryEscape(strings.Repeat("a ", 11)),
		} {
			wantError(t, owner.do(t, "GET", "/v1/documents?"+bad, nil), 400, "bad_request")
		}
		// Text typed by a person never breaks the query syntax.
		for _, text := range []string{`agent-board`, `"`, `C++`, `a OR b`, `NEAR(`, `*`, `-x`, `title:foo`, `(`, `'`} {
			expect(t, owner.do(t, "GET", "/v1/documents?"+scope+"q="+url.QueryEscape(text), nil), 200)
		}
	})

	t.Run("a project that is not active", func(t *testing.T) {
		r := createProject(t, "DD")
		content := documentText("report", "", "", "", "본문")
		const name = "2026-10-20-1544-report-frozen.md"
		if _, status := mustPutDocument(t, owner, r.Key, name, "history", content); status != 201 {
			t.Fatalf("status %d", status)
		}
		for _, status := range []string{"archived", "deleted"} {
			expect(t, owner.do(t, "PATCH", "/v1/projects/"+r.Key, map[string]any{"status": status}), 200)
			for label, put := range map[string]struct{ name, content string }{
				"a new document":      {"2026-10-21-0900-report-new.md", content},
				"the same document":   {name, content},
				"an invalid document": {"x.md", "본문"},
				"another content":     {name, content + "x"},
			} {
				e := wantError(t, putDocument(t, owner, r.Key, put.name, "history", put.content), 409, "conflict")
				if !e.hasReason("project_not_active") {
					t.Errorf("%s project, %s: %+v", status, label, e.Error.Details)
				}
			}
			// Reading works like for any other project.
			if got := decodeAs[document](t, owner.do(t, "GET", documentPath(r.Key, name), nil), 200); got.Content == nil || *got.Content != content {
				t.Errorf("%s project: get = %+v", status, got)
			}
			if got := decodeAs[list[document]](t, owner.do(t, "GET", "/v1/projects/"+r.Key+"/documents", nil), 200); got.Total != 1 {
				t.Errorf("%s project: list total = %d", status, got.Total)
			}
			if got := decodeAs[list[document]](t, owner.do(t, "GET", "/v1/documents?project="+r.Key, nil), 200); got.Total != 1 {
				t.Errorf("%s project: search total = %d", status, got.Total)
			}
		}
		expect(t, owner.do(t, "PATCH", "/v1/projects/"+r.Key, map[string]any{"status": "active"}), 200)
		if _, status := mustPutDocument(t, owner, r.Key, "2026-10-21-0900-report-new.md", "history", content); status != 201 {
			t.Errorf("after the project is active again: status %d", status)
		}
	})

	t.Run("credentials and trace headers", func(t *testing.T) {
		targets := []struct{ method, path string }{
			{"GET", documentPath(p.Key, nBare)},
			{"GET", "/v1/projects/" + p.Key + "/documents"},
			{"GET", "/v1/documents"},
			{"PUT", documentPath(p.Key, "2026-10-22-0900-report-nobody.md")},
		}
		body := []byte(`{"location":"history","content":"x"}`)
		for _, tg := range targets {
			raw := []byte(nil)
			if tg.method == "PUT" {
				raw = body
			}
			good := owner.headers(raw != nil)
			for _, mod := range []func(h http.Header){
				func(h http.Header) { h.Del("Authorization") },
				func(h http.Header) { h.Set("Authorization", "Bearer "+strings.Repeat("x", 40)) },
			} {
				h := good.Clone()
				mod(h)
				wantError(t, send(t, tg.method, tg.path, raw, h), 401, "unauthorized")
			}
			for _, name := range []string{"X-Actor", "X-Session-Id", "X-Client-Host"} {
				h := good.Clone()
				h.Del(name)
				wantError(t, send(t, tg.method, tg.path, raw, h), 400, "bad_request")
			}
			h := good.Clone()
			h.Set("X-Actor", "nobody")
			wantError(t, send(t, tg.method, tg.path, raw, h), 400, "bad_request")
			// An agent works with its own token and not with the shared one.
			h = good.Clone()
			h.Set("X-Actor", "claude")
			wantError(t, send(t, tg.method, tg.path, raw, h), 403, "forbidden")
		}
		// Nothing of the refused puts was stored.
		wantError(t, owner.do(t, "GET", documentPath(p.Key, "2026-10-22-0900-report-nobody.md"), nil), 404, "not_found")
		for _, agent := range []string{"claude", "codex"} {
			ag := as(agent)
			name := fmt.Sprintf("2026-10-22-0900-report-by-%s.md", agent)
			d := decodeAs[document](t, putDocument(t, ag, p.Key, name, "history", documentText("report", "", "", "", "본문")), 201)
			if d.CreatedBy != agent {
				t.Errorf("created_by = %q, want %q", d.CreatedBy, agent)
			}
			expect(t, ag.do(t, "GET", documentPath(p.Key, name), nil), 200)
			expect(t, ag.do(t, "GET", "/v1/documents?project="+p.Key, nil), 200)
		}
	})
}

// sortedJoin puts a comma separated list in order, for results whose order the
// test leaves to the server.
func sortedJoin(list string) string {
	if list == "" {
		return ""
	}
	parts := strings.Split(list, ",")
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
