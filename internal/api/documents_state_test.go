package api

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

const (
	stOld = "2026-10-01-1000-decision-old.md"
	stNew = "2026-10-02-1000-decision-new.md"
	stRep = "2026-10-03-1000-report-notes.md"
)

// putFields puts a document with the fields of a v0.9 client.
func (e *env) putFields(t *testing.T, project, name string, fields map[string]any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return e.do(t, "PUT", documentPath(project, name), string(b), headers)
}

type docResult struct {
	model.Document
	Warnings []model.DocumentWarning `json:"warnings"`
}

func decodeDoc(t *testing.T, rec *httptest.ResponseRecorder) docResult {
	t.Helper()
	var d docResult
	decodeJSON(t, rec, &d)
	return d
}

func details(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var out []string
	for _, d := range decodeErr(t, rec).Error.Details {
		out = append(out, d.Field+":"+d.Reason)
	}
	return out
}

func TestPutDocumentFieldsAndPrecedence(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.mustPutDocument(t, "AGB", stOld, "history", apiDocText("decision", "옛"))

	// The front matter decides what the request leaves out; the request wins where it speaks.
	content := apiDocText("decision", "새", "date: 2026-10-02 10:00", "title: 프런트매터 제목", "tags: [fm]", "supersedes: "+stOld)
	rec := e.putFields(t, "AGB", stNew, map[string]any{"content": content, "title": "인자 제목", "tags": []string{}, "archived": false, "path": "records/new.md"}, nil)
	d := decodeDoc(t, rec)
	if rec.Code != 201 || *d.Title != "인자 제목" || len(d.Tags) != 0 || d.Tags == nil || *d.Date != "2026-10-02 10:00" || !reflect.DeepEqual(d.Supersedes, []string{stOld}) ||
		d.Path == nil || *d.Path != "records/new.md" || d.State != model.StateCurrent || d.Warnings == nil || len(d.Warnings) != 0 {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Location") != documentPath("AGB", stNew) {
		t.Errorf("Location = %q", rec.Header().Get("Location"))
	}
	// The same request again is the same document; a field by argument that equals the front matter too.
	if again := e.putFields(t, "AGB", stNew, map[string]any{"content": content, "title": "인자 제목", "tags": []string{}, "path": "records/new.md", "supersedes": []string{stOld}}, nil); again.Code != 200 || again.Body.String() != rec.Body.String() {
		t.Errorf("again: %d %s", again.Code, again.Body.String())
	}
	// Archived at registration.
	rec = e.putFields(t, "AGB", stRep, map[string]any{"content": apiDocText("report", "보고"), "archived": true}, nil)
	if d := decodeDoc(t, rec); rec.Code != 201 || d.State != model.StateArchived || !d.Archived {
		t.Errorf("archived put: %d %s", rec.Code, rec.Body.String())
	}
	// The old target is superseded now.
	if got := decodeDoc(t, e.do(t, "GET", documentPath("AGB", stOld), "", nil)); got.State != model.StateSuperseded || !reflect.DeepEqual(got.SupersededBy, []string{stNew}) {
		t.Errorf("target = %+v", got.Document)
	}
}

func TestPutDocumentRefusals(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")
	e.mustPutDocument(t, "AGB", stOld, "history", apiDocText("decision", "옛"))
	body := func(extra map[string]any) map[string]any {
		m := map[string]any{"content": apiDocText("decision", "새")}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	for label, c := range map[string]struct {
		fields map[string]any
		want   []string
	}{
		"unresolved target":              {body(map[string]any{"related": []string{"nothing.md"}}), []string{"related[0]:unresolved"}},
		"a bare name of another project": {body(map[string]any{"related": []string{"x.md"}}), []string{"related[0]:unresolved"}},
		"a sentence":                     {body(map[string]any{"supersedes": []string{"일부 문장"}}), []string{"supersedes[0]:not_a_name"}},
		"unresolved issue":               {body(map[string]any{"issues": []string{"AGB-9"}}), []string{"issues[0]:unresolved"}},
		"self reference":                 {body(map[string]any{"related": []string{stNew}}), []string{"related[0]:self_reference"}},
		"placeholder":                    {body(map[string]any{"title": "<문서 제목>"}), []string{"title:placeholder"}},
		"title missing":                  {map[string]any{"content": "# 본문"}, []string{"title:required"}},
		"kind other for a record":        {body(map[string]any{"kind": "other"}), []string{"kind:"}},
		"bad date":                       {body(map[string]any{"date": "어제"}), []string{"date:"}},
		"bad path":                       {body(map[string]any{"path": "a/../.b"}), []string{"path:"}},
	} {
		rec := e.putFields(t, "AGB", stNew, c.fields, nil)
		if rec.Code != 422 || decodeErr(t, rec).Error.Code != "validation_failed" || !reflect.DeepEqual(details(t, rec), c.want) {
			t.Errorf("%s: %d %s", label, rec.Code, rec.Body.String())
		}
	}
	for _, d := range decodeErr(t, e.putFields(t, "AGB", stNew, body(map[string]any{"related": []string{"nothing.md"}}), nil)).Error.Details {
		if d.Message == "" {
			t.Errorf("detail without a message: %+v", d)
		}
	}
	if rec, l := e.getDocuments(t, "/v1/projects/AGB/documents"); l.Total != 1 {
		t.Errorf("refused puts left documents behind: %d %s", rec.Code, rec.Body.String())
	}
	// Types are checked before the rules.
	for label, fields := range map[string]string{
		"tags is text":       `{"content":"x","tags":"a"}`,
		"supersedes is text": `{"content":"x","supersedes":"a.md"}`,
		"archived is text":   `{"content":"x","archived":"yes"}`,
		"title is a number":  `{"content":"x","title":5}`,
		"an unknown field":   `{"content":"x","status":"current"}`,
		"state is not input": `{"content":"x","state":"archived"}`,
		"warnings not input": `{"content":"x","warnings":[]}`,
		"location is a list": `{"content":"x","location":["history"]}`,
	} {
		if rec := e.do(t, "PUT", documentPath("AGB", stNew), fields, nil); rec.Code != 400 {
			t.Errorf("%s: %d %s", label, rec.Code, rec.Body.String())
		}
	}
}

func TestPutDocumentMetadataDiffers(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	content := apiDocText("decision", "본문", "tags: [a]")
	e.mustPutDocument(t, "AGB", stOld, "history", content)

	rec := e.putFields(t, "AGB", stOld, map[string]any{"content": content, "title": "다른 제목", "archived": true}, nil)
	b := decodeErr(t, rec)
	if rec.Code != 409 || b.Error.Code != "conflict" || !reflect.DeepEqual(details(t, rec), []string{"title:metadata_differs", "archived:metadata_differs"}) ||
		!strings.Contains(b.Error.Message, "archive") || !strings.Contains(b.Error.Message, "relations") {
		t.Errorf("metadata differs: %d %s", rec.Code, rec.Body.String())
	}
	// Other content keeps its own reason.
	rec = e.putFields(t, "AGB", stOld, map[string]any{"content": content + "x"}, nil)
	if rec.Code != 409 || !reflect.DeepEqual(details(t, rec), []string{"content:immutable"}) {
		t.Errorf("immutable: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPutDocumentOfAnImportedDocumentWarns(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	content := "---\nrelated: [later.md]\n---\n# 가져온 문서\n"
	rec := e.putFields(t, "AGB", "notes/a.md", map[string]any{"content": content, "imported": true, "path": "notes/a.md"}, nil)
	d := decodeDoc(t, rec)
	if rec.Code != 201 || !d.Imported || d.Kind != model.DocumentOther || d.Title == nil || *d.Title != "가져온 문서" ||
		len(d.Warnings) != 1 || d.Warnings[0].Field != "related[0]" || d.Warnings[0].Reason != "unresolved" ||
		!reflect.DeepEqual(d.UnresolvedRefs, []model.UnresolvedRef{{Kind: model.RelRelated, Target: "later.md"}}) {
		t.Fatalf("imported put: %d %s", rec.Code, rec.Body.String())
	}
	// The target comes later and the relation resolves.
	e.putFields(t, "AGB", "later.md", map[string]any{"content": "x", "imported": true}, nil)
	got := decodeDoc(t, e.do(t, "GET", documentPath("AGB", "notes/a.md"), "", nil))
	if !reflect.DeepEqual(got.Related, []string{"later.md"}) || len(got.UnresolvedRefs) != 0 {
		t.Errorf("after the target came: %+v", got.Document)
	}
}

func TestPutDocumentRefusesLocation(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	content := jsonString(apiDocText("decision", "본문"))
	for _, loc := range []string{`"history"`, `"archive"`, `""`, `null`} {
		rec := e.do(t, "PUT", documentPath("AGB", stNew), `{"content":`+content+`,"location":`+loc+`}`, nil)
		if rec.Code != 400 || !reflect.DeepEqual(details(t, rec), []string{"location:unknown_field"}) ||
			!strings.Contains(rec.Body.String(), "location은 더 이상 받지 않습니다. CLI를 v0.9 이상으로 갱신하세요.") {
			t.Errorf("location %s: %d %s", loc, rec.Code, rec.Body.String())
		}
	}
}

func TestArchiveEndpoints(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.mustPutDocument(t, "AGB", stOld, "history", apiDocText("decision", "옛"))
	path := documentPath("AGB", stOld) + "/archive"

	rec := e.do(t, "POST", path, "", map[string]string{"X-Actor": "owner", "X-Session-Id": "s-9", "X-Client-Host": "pc"})
	d := decodeDoc(t, rec)
	if rec.Code != 200 || !d.Archived || d.State != model.StateArchived || d.UpdatedBy != "owner" || d.Content != nil || d.Warnings == nil {
		t.Fatalf("archive: %d %s", rec.Code, rec.Body.String())
	}
	if again := e.do(t, "POST", path, "", nil); again.Code != 200 || again.Body.String() != rec.Body.String() {
		t.Errorf("archive again: %d %s", again.Code, again.Body.String())
	}
	if rec := e.do(t, "DELETE", path, "", nil); rec.Code != 200 || decodeDoc(t, rec).State != model.StateCurrent {
		t.Errorf("unarchive: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "DELETE", path, "", nil); rec.Code != 200 {
		t.Errorf("unarchive again: %d", rec.Code)
	}

	// The activity: newest first, with the trace of the request, and an envelope.
	rec = e.do(t, "GET", documentPath("AGB", stOld)+"/activity", "", nil)
	var list struct {
		Items  []model.DocumentActivity `json:"items"`
		Total  int                      `json:"total"`
		Limit  int                      `json:"limit"`
		Offset int                      `json:"offset"`
	}
	decodeJSON(t, rec, &list)
	if rec.Code != 200 || list.Total != 2 || list.Limit != 50 || len(list.Items) != 2 || list.Items[0].Actor != "claude" || list.Items[1].Actor != "owner" ||
		list.Items[1].SessionID != "s-9" || list.Items[1].ClientHost != "pc" || list.Items[1].Project != "AGB" || list.Items[1].Document != stOld ||
		string(list.Items[1].Changes["archived"].From) != "false" || string(list.Items[1].Changes["archived"].To) != "true" {
		t.Fatalf("activity: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "GET", documentPath("AGB", stOld)+"/activity?limit=1&offset=1", "", nil); !strings.Contains(rec.Body.String(), `"total":2`) || !strings.Contains(rec.Body.String(), `"limit":1`) {
		t.Errorf("paged activity: %s", rec.Body.String())
	}
	if rec := e.do(t, "GET", documentPath("AGB", stOld)+"/activity?limit=0", "", nil); rec.Code != 400 {
		t.Errorf("bad limit: %d", rec.Code)
	}
	// A document without changes has an empty list.
	e.mustPutDocument(t, "AGB", stNew, "history", apiDocText("decision", "새"))
	if rec := e.do(t, "GET", documentPath("AGB", stNew)+"/activity", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("empty activity: %d %s", rec.Code, rec.Body.String())
	}
	// No issue activity.
	if rec := e.do(t, "GET", "/v1/activity?project=AGB", "", nil); !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Errorf("issue activity: %s", rec.Body.String())
	}
	// Unknowns.
	for label, c := range map[string]struct{ method, target string }{
		"archive of a missing document":   {"POST", documentPath("AGB", "x.md") + "/archive"},
		"archive of a missing project":    {"POST", documentPath("NOPE", stOld) + "/archive"},
		"unarchive of a missing document": {"DELETE", documentPath("AGB", "x.md") + "/archive"},
		"activity of a missing document":  {"GET", documentPath("AGB", "x.md") + "/activity"},
	} {
		if rec := e.do(t, c.method, c.target, "", nil); rec.Code != 404 {
			t.Errorf("%s: %d %s", label, rec.Code, rec.Body.String())
		}
	}
	// A path name of an imported document, escaped or not.
	e.putDocument(t, "AGB", "archive/2026-06-x/n.md", "legacy", "# n\n", nil)
	for _, name := range []string{"archive/2026-06-x/n.md", url.PathEscape("archive/2026-06-x/n.md")} {
		if rec := e.do(t, "DELETE", documentPath("AGB", name)+"/archive", "", nil); rec.Code != 200 || decodeDoc(t, rec).State != model.StateCurrent {
			t.Errorf("unarchive %s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestRelationEndpoints(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")
	issue := e.createIssue(t, `{"project":"AGB","title":"이슈"}`).Key
	e.mustPutDocument(t, "AGB", stOld, "history", apiDocText("decision", "옛"))
	e.mustPutDocument(t, "AGB", stNew, "history", apiDocText("decision", "새"))
	e.mustPutDocument(t, "OTH", stRep, "history", apiDocText("report", "다른"))
	path := documentPath("AGB", stNew) + "/relations"
	post := func(kind, target string) *httptest.ResponseRecorder {
		return e.do(t, "POST", path, `{"kind":"`+kind+`","target":"`+target+`"}`, nil)
	}

	rec := post("supersedes", stOld)
	d := decodeDoc(t, rec)
	if rec.Code != 200 || !reflect.DeepEqual(d.Supersedes, []string{stOld}) || d.Warnings == nil || d.Content != nil {
		t.Fatalf("supersedes: %d %s", rec.Code, rec.Body.String())
	}
	if got := decodeDoc(t, e.do(t, "GET", documentPath("AGB", stOld), "", nil)); got.State != model.StateSuperseded {
		t.Errorf("target state = %v", got.State)
	}
	if rec := post("about", issue); rec.Code != 200 || !reflect.DeepEqual(decodeDoc(t, rec).Issues, []string{issue}) {
		t.Errorf("about: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("related", "OTH/"+stRep); rec.Code != 200 || !reflect.DeepEqual(decodeDoc(t, rec).Related, []string{"OTH/" + stRep}) {
		t.Errorf("related across projects: %d %s", rec.Code, rec.Body.String())
	}

	for label, c := range map[string]struct {
		rec  *httptest.ResponseRecorder
		code int
		want []string
	}{
		"unresolved":                   {post("related", "nothing.md"), 422, []string{"target:unresolved"}},
		"unresolved issue":             {post("about", "AGB-9"), 422, []string{"target:unresolved"}},
		"bare name of another project": {post("related", stRep), 422, []string{"target:unresolved"}},
		"self":                         {post("related", stNew), 422, []string{"target:self_reference"}},
		"a sentence":                   {post("supersedes", "일부 문장"), 422, []string{"target:not_a_name"}},
		"a cycle":                      {e.do(t, "POST", documentPath("AGB", stOld)+"/relations", `{"kind":"supersedes","target":"`+stNew+`"}`, nil), 409, []string{":cycle"}},
		"bad kind":                     {post("replaces", stOld), 422, []string{"kind:"}},
		"no kind":                      {e.do(t, "POST", path, `{"target":"`+stOld+`"}`, nil), 422, []string{"kind:"}},
		"no target":                    {e.do(t, "POST", path, `{"kind":"related"}`, nil), 422, []string{"target:"}},
		"empty target":                 {post("related", ""), 422, []string{"target:"}},
		"unknown field":                {e.do(t, "POST", path, `{"kind":"related","target":"`+stOld+`","extra":1}`, nil), 400, nil},
		"not an object":                {e.do(t, "POST", path, `[]`, nil), 400, nil},
		"no body":                      {e.do(t, "POST", path, ``, nil), 400, nil},
		"target is a list":             {e.do(t, "POST", path, `{"kind":"related","target":["a.md"]}`, nil), 400, nil},
	} {
		if c.rec.Code != c.code || (c.want != nil && !reflect.DeepEqual(details(t, c.rec), c.want)) {
			t.Errorf("%s: %d %s", label, c.rec.Code, c.rec.Body.String())
		}
	}

	// Removing, with the same body; removing what is not there is fine.
	del := func(kind, target string) *httptest.ResponseRecorder {
		return e.do(t, "DELETE", path, `{"kind":"`+kind+`","target":"`+target+`"}`, nil)
	}
	if rec := del("supersedes", stOld); rec.Code != 200 || len(decodeDoc(t, rec).Supersedes) != 0 {
		t.Errorf("remove: %d %s", rec.Code, rec.Body.String())
	}
	if rec := del("supersedes", stOld); rec.Code != 200 {
		t.Errorf("remove again: %d %s", rec.Code, rec.Body.String())
	}
	if got := decodeDoc(t, e.do(t, "GET", documentPath("AGB", stOld), "", nil)); got.State != model.StateCurrent {
		t.Errorf("target state after removing = %v", got.State)
	}

	// The activity has one record per change.
	rec = e.do(t, "GET", path[:len(path)-len("relations")]+"activity", "", nil)
	var list struct {
		Items []model.DocumentActivity `json:"items"`
		Total int                      `json:"total"`
	}
	decodeJSON(t, rec, &list)
	if list.Total != 4 || len(list.Items[0].Changes) != 1 || string(list.Items[0].Changes["supersedes"].To) != `[]` || string(list.Items[3].Changes["supersedes"].To) != `["`+stOld+`"]` {
		t.Errorf("activity: %s", rec.Body.String())
	}
}

func TestRelationEndpointOfAnImportedDocumentWarns(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.putDocument(t, "AGB", "notes/a.md", "legacy", "# a\n", nil)
	rec := e.do(t, "POST", documentPath("AGB", "notes/a.md")+"/relations", `{"kind":"related","target":"later.md"}`, nil)
	d := decodeDoc(t, rec)
	if rec.Code != 200 || len(d.Warnings) != 1 || d.Warnings[0].Field != "target" || d.Warnings[0].Reason != "unresolved" || len(d.UnresolvedRefs) != 1 {
		t.Errorf("warn: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "DELETE", documentPath("AGB", "notes/a.md")+"/relations", `{"kind":"related","target":"later.md"}`, nil); rec.Code != 200 || len(decodeDoc(t, rec).UnresolvedRefs) != 0 {
		t.Errorf("remove unresolved: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDocumentChangesNeedAnActiveProject(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.mustPutDocument(t, "AGB", stOld, "history", apiDocText("decision", "옛"))
	if rec := e.do(t, "PATCH", "/v1/projects/AGB", `{"status":"archived"}`, nil); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	for label, rec := range map[string]*httptest.ResponseRecorder{
		"archive":   e.do(t, "POST", documentPath("AGB", stOld)+"/archive", "", nil),
		"unarchive": e.do(t, "DELETE", documentPath("AGB", stOld)+"/archive", "", nil),
		"relation":  e.do(t, "POST", documentPath("AGB", stOld)+"/relations", `{"kind":"related","target":"x.md"}`, nil),
	} {
		if rec.Code != 409 || !reflect.DeepEqual(details(t, rec), []string{":project_not_active"}) {
			t.Errorf("%s: %d %s", label, rec.Code, rec.Body.String())
		}
	}
	if rec := e.do(t, "GET", documentPath("AGB", stOld)+"/activity", "", nil); rec.Code != 200 {
		t.Errorf("activity of an archived project: %d", rec.Code)
	}
}

func TestDocumentSubRoutesFollowTheConventions(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.mustPutDocument(t, "AGB", stOld, "history", apiDocText("decision", "옛"))
	doc := documentPath("AGB", stOld)
	for _, c := range []struct{ method, target, allow string }{
		{"POST", doc, "GET, PUT"},
		{"DELETE", doc, "GET, PUT"},
		{"GET", doc + "/archive", "DELETE, POST"},
		{"PUT", doc + "/archive", "DELETE, POST"},
		{"GET", doc + "/relations", "DELETE, POST"},
		{"PUT", doc + "/relations", "DELETE, POST"},
		{"POST", doc + "/activity", "GET"},
		{"DELETE", doc + "/activity", "GET"},
	} {
		rec := e.do(t, c.method, c.target, `{}`, nil)
		if rec.Code != 405 || rec.Header().Get("Allow") != c.allow || decodeErr(t, rec).Error.Code != "method_not_allowed" {
			t.Errorf("%s %s: %d Allow=%q", c.method, c.target, rec.Code, rec.Header().Get("Allow"))
		}
	}
	for _, c := range []struct{ method, target, body string }{
		{"POST", doc + "/archive", ""},
		{"DELETE", doc + "/archive", ""},
		{"POST", doc + "/relations", `{"kind":"related","target":"x.md"}`},
		{"GET", doc + "/activity", ""},
	} {
		if rec := e.do(t, c.method, c.target, c.body, map[string]string{"Authorization": "-"}); rec.Code != 401 {
			t.Errorf("%s %s without a token: %d", c.method, c.target, rec.Code)
		}
		for _, hdr := range []string{"X-Actor", "X-Session-Id", "X-Client-Host"} {
			if rec := e.do(t, c.method, c.target, c.body, map[string]string{hdr: "-"}); rec.Code != 400 {
				t.Errorf("%s %s without %s: %d", c.method, c.target, hdr, rec.Code)
			}
		}
	}
	if rec := e.do(t, "POST", doc+"/archive?x=1", "", nil); rec.Code != 400 {
		t.Errorf("unknown query parameter: %d", rec.Code)
	}
}

func TestDocumentListsFilterByStateAndImported(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.mustPutDocument(t, "AGB", stOld, "history", apiDocText("decision", "옛"))
	e.putFields(t, "AGB", stNew, map[string]any{"content": apiDocText("decision", "새"), "supersedes": []string{stOld}}, nil)
	e.putFields(t, "AGB", stRep, map[string]any{"content": apiDocText("report", "보관"), "archived": true}, nil)
	e.putFields(t, "AGB", "notes/a.md", map[string]any{"content": "# 가져온\n", "imported": true}, nil)
	names := func(target string) string {
		t.Helper()
		rec, l := e.getDocuments(t, target)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", target, rec.Code, rec.Body.String())
		}
		return apiDocNames(l.Items)
	}
	join := func(n ...string) string { return strings.Join(n, ",") }
	for target, want := range map[string]string{
		"/v1/documents":                                          join(stNew, "notes/a.md"),
		"/v1/documents?state=current":                            join(stNew, "notes/a.md"),
		"/v1/documents?state=superseded":                         stOld,
		"/v1/documents?state=archived":                           stRep,
		"/v1/documents?state=superseded,archived":                join(stRep, stOld),
		"/v1/documents?state=all":                                join(stRep, stNew, stOld, "notes/a.md"),
		"/v1/documents?state=all&imported=true":                  "notes/a.md",
		"/v1/documents?state=all&imported=false":                 join(stRep, stNew, stOld),
		"/v1/documents?imported=true&kind=other":                 "notes/a.md",
		"/v1/documents?q=" + url.QueryEscape("옛") + "&state=all": stOld,
		"/v1/documents?q=" + url.QueryEscape("옛"):                "",
		// The inventory of a project has every document.
		"/v1/projects/AGB/documents": join("notes/a.md", stOld, stNew, stRep),
	} {
		got := names(target)
		// Without a query the order is newest first by date, which the names decide here.
		if !sameSet(got, want) {
			t.Errorf("%s: %q, want %q", target, got, want)
		}
	}
	for _, c := range []struct{ query, field string }{
		{"?location=history", "location"},
		{"?location=", "location"},
		{"?state=old", "state"},
		{"?imported=maybe", "imported"},
	} {
		rec := e.do(t, "GET", "/v1/documents"+c.query, "", nil)
		b := decodeErr(t, rec)
		if rec.Code != 400 || b.Error.Code != "bad_request" || len(b.Error.Details) != 1 || b.Error.Details[0].Field != c.field {
			t.Errorf("%s: %d %s", c.query, rec.Code, rec.Body.String())
		}
	}
	rec := e.do(t, "GET", "/v1/documents?location=history", "", nil)
	if b := decodeErr(t, rec); b.Error.Details[0].Message != "location은 없어졌습니다. 상태는 state, 가져온 문서는 imported를 쓰세요." {
		t.Errorf("location message = %q", b.Error.Details[0].Message)
	}
	// The inventory takes no filters.
	for _, q := range []string{"?state=all", "?imported=true", "?location=history"} {
		if rec := e.do(t, "GET", "/v1/projects/AGB/documents"+q, "", nil); rec.Code != 400 {
			t.Errorf("inventory %s: %d", q, rec.Code)
		}
	}
}

func sameSet(a, b string) bool {
	x, y := strings.Split(a, ","), strings.Split(b, ",")
	if a == "" {
		x = nil
	}
	if b == "" {
		y = nil
	}
	if len(x) != len(y) {
		return false
	}
	seen := map[string]int{}
	for _, v := range x {
		seen[v]++
	}
	for _, v := range y {
		seen[v]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestPutDocumentNoRelationValues(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	withFront := strings.Replace(apiDocText("report", "새"), "title:", "supersedes: none\nrelated: none\ntitle:", 1)
	for label, c := range map[string]struct {
		name   string
		fields map[string]any
	}{
		"arguments":    {stNew, map[string]any{"content": apiDocText("decision", "새"), "supersedes": []string{"none"}, "related": []string{"", "none"}}},
		"front matter": {stRep, map[string]any{"content": withFront}},
	} {
		rec := e.putFields(t, "AGB", c.name, c.fields, nil)
		if d := decodeDoc(t, rec); rec.Code != 201 || len(d.Supersedes) != 0 || len(d.Related) != 0 || len(d.UnresolvedRefs) != 0 {
			t.Errorf("%s: %d %s", label, rec.Code, rec.Body.String())
		}
	}
}
