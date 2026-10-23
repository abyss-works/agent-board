package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDocumentEnums(t *testing.T) {
	if want := []DocumentKind{DocumentDecision, DocumentIncident, DocumentReport, DocumentImplementation}; !reflect.DeepEqual(DocumentKinds, want) {
		t.Fatalf("DocumentKinds = %v, want %v", DocumentKinds, want)
	}
	if want := []DocumentLocation{LocationHistory, LocationArchive}; !reflect.DeepEqual(DocumentLocations, want) {
		t.Fatalf("DocumentLocations = %v, want %v", DocumentLocations, want)
	}
	for _, v := range []string{"decision", "incident", "report", "implementation"} {
		if got, ok := ParseDocumentKind(v); !ok || string(got) != v {
			t.Errorf("ParseDocumentKind(%q) = %q, %v", v, got, ok)
		}
	}
	for _, v := range []string{"", "plan", "Decision", "note", "decision,report"} {
		if _, ok := ParseDocumentKind(v); ok {
			t.Errorf("ParseDocumentKind(%q) must fail", v)
		}
	}
	for _, v := range []string{"history", "archive"} {
		if got, ok := ParseDocumentLocation(v); !ok || string(got) != v {
			t.Errorf("ParseDocumentLocation(%q) = %q, %v", v, got, ok)
		}
	}
	for _, v := range []string{"", "draft", "History", "history/", "spec"} {
		if _, ok := ParseDocumentLocation(v); ok {
			t.Errorf("ParseDocumentLocation(%q) must fail", v)
		}
	}
}

func TestParseDocumentName(t *testing.T) {
	valid := map[string]DocumentKind{
		"2026-10-20-1544-decision-priority-model.md":      DocumentDecision,
		"2026-10-19-1700-report-linear-gap.md":            DocumentReport,
		"2026-10-19-1657-implementation-v020-release.md":  DocumentImplementation,
		"2026-10-21-0900-incident-db-lock-2.md":           DocumentIncident,
		"2026-10-21-0900-decision-a.md":                   DocumentDecision,
		"2026-10-21-0900-decision-decision-report.md":     DocumentDecision,
		"2026-10-21-0900-report-0123456789.md":            DocumentReport,
		"1999-01-01-0000-report-x.md":                     DocumentReport,
		"2026-10-21-0900-implementation-a-b-c-d-e-f-g.md": DocumentImplementation,
	}
	for name, want := range valid {
		if got, ok := ParseDocumentName(name); !ok || got != want {
			t.Errorf("ParseDocumentName(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
	for _, name := range []string{
		"",
		"_decision.md",
		"2026-10-20-1544-decision-priority-model",
		"2026-10-20-1544-decision-priority-model.txt",
		"2026-10-20-1544-decision-priority-model.md.md",
		"2026-10-20-1544-plan-priority-model.md",
		"2026-10-20-1544-Decision-priority-model.md",
		"2026-10-20-1544-decision-Priority.md",
		"2026-10-20-1544-decision-priority_model.md",
		"2026-10-20-1544-decision-우선순위.md",
		"2026-10-20-1544-decision-.md",
		"2026-10-20-1544-decision.md",
		"2026-10-20-154-decision-x.md",
		"2026-10-20-15444-decision-x.md",
		"26-10-20-1544-decision-x.md",
		"2026-10-20-15-44-decision-x.md",
		"2026-10-17-14-30-handoff.md",
		" 2026-10-20-1544-decision-x.md",
		"2026-10-20-1544-decision-x.md ",
		"2026-10-20-1544-decision-x.md\n",
		"history/2026-10-20-1544-decision-x.md",
		"../2026-10-20-1544-decision-x.md",
		"2026-10-20-1544-decision-a b.md",
	} {
		if got, ok := ParseDocumentName(name); ok {
			t.Errorf("ParseDocumentName(%q) = %q, want a rejection", name, got)
		}
	}
}

func TestParseFrontMatter(t *testing.T) {
	text := func(s string) *string { return &s }
	cases := []struct {
		name    string
		content string
		want    DocumentMeta
		ok      bool
	}{
		{
			"a record document",
			"---\nkind: decision\ndate: 2026-10-20 15:44\nproject: agent-board\ntitle: 우선순위 큐 모델은 등급과 서버 계산 순서로 한다\ntags: [priority, queue, issue-lifecycle]\nauthor: claude\nstatus: 채택\nsupersedes: none\nissue: AGB-40\n---\n\n# 본문\n",
			DocumentMeta{Kind: "decision", Date: text("2026-10-20 15:44"), Title: text("우선순위 큐 모델은 등급과 서버 계산 순서로 한다"), Tags: []string{"priority", "queue", "issue-lifecycle"}},
			true,
		},
		{
			"CRLF line endings",
			"---\r\nkind: report\r\ndate: 2026-10-19 17:00\r\ntitle: 비교\r\ntags: [a, b]\r\n---\r\n본문\r\n",
			DocumentMeta{Kind: "report", Date: text("2026-10-19 17:00"), Title: text("비교"), Tags: []string{"a", "b"}},
			true,
		},
		{
			"date, title and tags absent",
			"---\nkind: incident\n---\n본문",
			DocumentMeta{Kind: "incident", Tags: []string{}},
			true,
		},
		{
			"empty values are absent",
			"---\nkind: incident\ndate:\ntitle:   \ntags:\n---\n",
			DocumentMeta{Kind: "incident", Tags: []string{}},
			true,
		},
		{
			"an empty block",
			"---\n---\n본문",
			DocumentMeta{Tags: []string{}},
			true,
		},
		{
			"closing line without a final newline",
			"---\nkind: report\n---",
			DocumentMeta{Kind: "report", Tags: []string{}},
			true,
		},
		{
			"trailing spaces on the delimiters",
			"---  \nkind: report\n---\t\n본문",
			DocumentMeta{Kind: "report", Tags: []string{}},
			true,
		},
		{
			"double quoted values",
			"---\nkind: \"decision\"\ndate: \"2026-10-20 15:44\"\ntitle: \"큐: \\\"등급\\\" 모델\"\ntags: [\"a\", \"b c\"]\n---\n",
			DocumentMeta{Kind: "decision", Date: text("2026-10-20 15:44"), Title: text(`큐: "등급" 모델`), Tags: []string{"a", "b c"}},
			true,
		},
		{
			"single quoted values",
			"---\nkind: 'decision'\ntitle: 'it''s a title'\ntags: ['x', y]\n---\n",
			DocumentMeta{Kind: "decision", Title: text("it's a title"), Tags: []string{"x", "y"}},
			true,
		},
		{
			"a colon inside a plain value",
			"---\nkind: decision\ntitle: 질문: 답변\ndate: 2026-10-20 15:44\n---\n",
			DocumentMeta{Kind: "decision", Date: text("2026-10-20 15:44"), Title: text("질문: 답변"), Tags: []string{}},
			true,
		},
		{
			"comments and blank lines in the block",
			"---\n# 주석\nkind: report # 종류\n\ntitle: C# 언어 # 설명\ntags: [a, b] # 목록\n---\n",
			DocumentMeta{Kind: "report", Title: text("C# 언어"), Tags: []string{"a", "b"}},
			true,
		},
		{
			"tags that are not a flow list are ignored",
			"---\nkind: report\ntags: a, b\n---\n",
			DocumentMeta{Kind: "report", Tags: []string{}},
			true,
		},
		{
			"a block list of tags is not read",
			"---\nkind: report\ntags:\n  - a\n  - b\ntitle: 제목\n---\n",
			DocumentMeta{Kind: "report", Title: text("제목"), Tags: []string{}},
			true,
		},
		{
			"empty flow list and empty items",
			"---\nkind: report\ntags: [ a ,, b , ]\n---\n",
			DocumentMeta{Kind: "report", Tags: []string{"a", "b"}},
			true,
		},
		{
			"an empty flow list",
			"---\nkind: report\ntags: []\n---\n",
			DocumentMeta{Kind: "report", Tags: []string{}},
			true,
		},
		{
			"indented lines belong to the key above",
			"---\nkind: report\nscope: 첫 줄\n  이어지는 줄: 값\ntitle: 제목\n---\n",
			DocumentMeta{Kind: "report", Title: text("제목"), Tags: []string{}},
			true,
		},
		{
			"a later --- in the body is not the block",
			"---\nkind: report\n---\n본문\n---\ntitle: 아님\n---\n",
			DocumentMeta{Kind: "report", Tags: []string{}},
			true,
		},
		{"no block", "# 제목\n\n본문", DocumentMeta{}, false},
		{"empty content", "", DocumentMeta{}, false},
		{"only the opening line", "---", DocumentMeta{}, false},
		{"opening line and no close", "---\nkind: report\ntitle: 제목\n", DocumentMeta{}, false},
		{"text before the block", "서문\n---\nkind: report\n---\n", DocumentMeta{}, false},
		{"blank line before the block", "\n---\nkind: report\n---\n", DocumentMeta{}, false},
		{"byte order mark before the block", "\ufeff---\nkind: report\n---\n", DocumentMeta{}, false},
		{"four dashes", "----\nkind: report\n----\n", DocumentMeta{}, false},
		{"dashes with text", "--- yaml\nkind: report\n---\n", DocumentMeta{}, false},
	}
	for _, c := range cases {
		got, ok := ParseFrontMatter(c.content)
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v (date %v, title %v)\nwant %+v", c.name, got, deref(got.Date), deref(got.Title), c.want)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func violationFields(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		return nil
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("err = %v, want a *ValidationError", err)
	}
	var out []string
	for _, f := range ve.Fields {
		out = append(out, f.Field)
	}
	return out
}

func TestValidateDocument(t *testing.T) {
	const name = "2026-10-20-1544-decision-priority-model.md"
	front := func(kind string) string { return "---\nkind: " + kind + "\ntitle: 제목\n---\n본문\n" }
	cases := []struct {
		label, name, location, content string
		want                           []string // field of each violation, in order
	}{
		{"valid in history", name, "history", front("decision"), nil},
		{"valid in archive", name, "archive", front("decision"), nil},
		{"bad name", "priority-model.md", "history", front("decision"), []string{"name"}},
		{"name of another kind than the front matter", "2026-10-20-1544-report-x.md", "history", front("decision"), []string{"content"}},
		{"unknown location", name, "draft", front("decision"), []string{"location"}},
		{"empty location", name, "", front("decision"), []string{"location"}},
		{"empty content", name, "history", "", []string{"content"}},
		{"no front matter", name, "history", "# 제목\n본문", []string{"content"}},
		{"unclosed front matter", name, "history", "---\nkind: decision\n", []string{"content"}},
		{"front matter without kind", name, "history", "---\ntitle: 제목\n---\n", []string{"content"}},
		{"front matter kind of another document", name, "history", front("report"), []string{"content"}},
		{"front matter kind is not a known kind", name, "history", front("plan"), []string{"content"}},
		{"every violation at once", "x.md", "tmp", "본문", []string{"name", "location", "content"}},
		{"bad name, and the kind cannot be compared", "x.md", "history", front("decision"), []string{"name"}},
	}
	for _, c := range cases {
		_, err := ValidateDocument(c.name, c.location, c.content)
		if got := violationFields(t, err); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: violations = %v (%v), want %v", c.label, got, err, c.want)
		}
	}

	// The metadata comes back with a valid document.
	meta, err := ValidateDocument(name, "history", "---\nkind: decision\ndate: 2026-10-20 15:44\ntitle: 제목\ntags: [a, b]\n---\n")
	if err != nil || meta.Kind != "decision" || meta.Date == nil || *meta.Date != "2026-10-20 15:44" || meta.Title == nil || *meta.Title != "제목" || !reflect.DeepEqual(meta.Tags, []string{"a", "b"}) {
		t.Errorf("meta = %+v, %v", meta, err)
	}
}

func TestValidateDocumentSize(t *testing.T) {
	const name = "2026-10-20-1544-report-big.md"
	head := "---\nkind: report\n---\n"
	pad := func(n int, unit string) string { return strings.Repeat(unit, n) }
	if MaxDocumentBytes != 262144 {
		t.Fatalf("MaxDocumentBytes = %d, want 262144", MaxDocumentBytes)
	}
	for label, c := range map[string]struct {
		content string
		ok      bool
	}{
		"exactly the limit":      {head + pad(MaxDocumentBytes-len(head), "a"), true},
		"one byte over":          {head + pad(MaxDocumentBytes-len(head)+1, "a"), false},
		"bytes, not characters":  {head + pad((MaxDocumentBytes-len(head))/3+1, "가"), false},
		"multibyte under limit":  {head + pad((MaxDocumentBytes-len(head))/3, "가"), true},
		"a large oversize body ": {head + pad(2*MaxDocumentBytes, "a"), false},
	} {
		_, err := ValidateDocument(name, "history", c.content)
		got := violationFields(t, err)
		if c.ok && got != nil || !c.ok && !reflect.DeepEqual(got, []string{"content"}) {
			t.Errorf("%s: violations = %v", label, got)
		}
	}
}

func TestDocumentJSON(t *testing.T) {
	at, err := ParseTime("2026-10-21T02:00:00.000Z")
	if err != nil {
		t.Fatal(err)
	}
	date, title := "2026-10-20 15:44", "우선순위 큐 모델은 등급과 서버 계산 순서로 한다"
	d := Document{
		Project: "AGB", Name: "2026-10-20-1544-decision-priority-model.md", Location: LocationHistory, Kind: DocumentDecision,
		Date: &date, Title: &title, Tags: []string{"priority", "queue"}, SHA256: "9f2c", Size: 6120,
		CreatedAt: at, CreatedBy: "claude", UpdatedAt: at, UpdatedBy: "claude",
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"project":"AGB","name":"2026-10-20-1544-decision-priority-model.md","location":"history","kind":"decision","date":"2026-10-20 15:44",` +
		`"title":"우선순위 큐 모델은 등급과 서버 계산 순서로 한다","tags":["priority","queue"],"sha256":"9f2c","size":6120,` +
		`"created_at":"2026-10-21T02:00:00.000Z","created_by":"claude","updated_at":"2026-10-21T02:00:00.000Z","updated_by":"claude"}`
	if string(b) != want {
		t.Errorf("document JSON =\n%s\nwant\n%s", b, want)
	}

	// The content sits between size and created_at; a snippet closes the object.
	content, snippet := "---\nkind: decision\n---\n", "[우선순위] 큐"
	d.Content, d.Snippet = &content, &snippet
	b, _ = json.Marshal(d)
	for _, w := range []string{`"size":6120,"content":"---\nkind: decision\n---\n","created_at"`, `"updated_by":"claude","snippet":"[우선순위] 큐"}`} {
		if !strings.Contains(string(b), w) {
			t.Errorf("JSON lacks %s: %s", w, b)
		}
	}

	// A document without front matter fields shows null date and title.
	b, _ = json.Marshal(Document{Tags: []string{}})
	if !strings.Contains(string(b), `"date":null,"title":null,"tags":[]`) {
		t.Errorf("empty metadata JSON = %s", b)
	}
}
