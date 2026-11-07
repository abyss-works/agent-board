package model

import (
	"reflect"
	"testing"
)

func TestDocumentEnums(t *testing.T) {
	if want := []DocumentKind{DocumentDecision, DocumentIncident, DocumentReport, DocumentImplementation, DocumentOther}; !reflect.DeepEqual(DocumentKinds, want) {
		t.Fatalf("DocumentKinds = %v, want %v", DocumentKinds, want)
	}
	if want := []DocumentLocation{LocationHistory, LocationArchive, LocationLegacy}; !reflect.DeepEqual(DocumentLocations, want) {
		t.Fatalf("DocumentLocations = %v, want %v", DocumentLocations, want)
	}
	for _, v := range []string{"decision", "incident", "report", "implementation", "other"} {
		if got, ok := ParseDocumentKind(v); !ok || string(got) != v {
			t.Errorf("ParseDocumentKind(%q) = %q, %v", v, got, ok)
		}
	}
	for _, v := range []string{"", "plan", "Decision", "note", "decision,report"} {
		if _, ok := ParseDocumentKind(v); ok {
			t.Errorf("ParseDocumentKind(%q) must fail", v)
		}
	}
	if want := []DocumentState{StateCurrent, StateSuperseded, StateArchived}; !reflect.DeepEqual(DocumentStates, want) {
		t.Fatalf("DocumentStates = %v, want %v", DocumentStates, want)
	}
	if got, ok := ParseDocumentState("superseded"); !ok || got != StateSuperseded {
		t.Errorf("ParseDocumentState(superseded) = %q, %v", got, ok)
	}
	if _, ok := ParseDocumentState("all"); ok {
		t.Error("all is a filter, not a state")
	}
	if got, ok := ParseRelationKind("about"); !ok || got != RelAbout {
		t.Errorf("ParseRelationKind(about) = %q, %v", got, ok)
	}
	for _, v := range []string{"history", "archive", "legacy"} {
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
		"2026-10-20-1544-other-x.md",
	} {
		if got, ok := ParseDocumentName(name); ok {
			t.Errorf("ParseDocumentName(%q) = %q, want a rejection", name, got)
		}
	}
}

func TestParseLegacyName(t *testing.T) {
	valid := map[string]string{
		"decisions/2026-10-17-14-30-scope-stack-deploy.md":  "decisions",
		"reports/2026-10-12-src-structure.md":               "reports",
		"incidents/save-loss.md":                            "incidents",
		"archive/2026-06-hermes-sdlc/README.md":             "archive",
		"archive/2026-10-mgmt-framework/mgmt/ops/deploy.md": "archive",
		"decisions/결정 기록.md":                                "decisions",
	}
	for name, want := range valid {
		if got, ok := ParseLegacyName(name); !ok || got != want {
			t.Errorf("ParseLegacyName(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
	for _, name := range []string{
		"",
		"decisions/_template.md",
		"archive/2026-10-mgmt/decisions/decision.example.md",
		"decisions/x.txt",
		"decisions/sub/x.md",
		"archive/x.md",
		"history/2026-10-20-1544-decision-x.md",
		"2026-10-20-1544-decision-x.md",
		"/decisions/x.md",
		"decisions//x.md",
		"archive/../decisions/x.md",
		"archive/.hidden/x.md",
		"archive/a\\b/x.md",
		"decisions/a\nb.md",
		"draft/x.md",
	} {
		if got, ok := ParseLegacyName(name); ok {
			t.Errorf("ParseLegacyName(%q) = %q, want a rejection", name, got)
		}
	}
}

func TestParseFrontMatter(t *testing.T) {
	text := func(s string) *string { return &s }
	cases := []struct {
		name    string
		content string
		want    FrontMatter
		ok      bool
	}{
		{
			"a record document",
			"---\nkind: decision\ndate: 2026-10-20 15:44\nproject: agent-board\ntitle: 우선순위 큐 모델은 등급과 서버 계산 순서로 한다\ntags: [priority, queue, issue-lifecycle]\nauthor: claude\nstatus: 채택\nsupersedes: none\nissue: AGB-40\n---\n\n# 본문\n",
			FrontMatter{Kind: "decision", Date: text("2026-10-20 15:44"), Title: text("우선순위 큐 모델은 등급과 서버 계산 순서로 한다"), Tags: []string{"priority", "queue", "issue-lifecycle"}, Supersedes: []string{"none"}, Issues: []string{"AGB-40"}},
			true,
		},
		{
			"CRLF line endings",
			"---\r\nkind: report\r\ndate: 2026-10-19 17:00\r\ntitle: 비교\r\ntags: [a, b]\r\n---\r\n본문\r\n",
			FrontMatter{Kind: "report", Date: text("2026-10-19 17:00"), Title: text("비교"), Tags: []string{"a", "b"}},
			true,
		},
		{
			"date, title and tags absent",
			"---\nkind: incident\n---\n본문",
			FrontMatter{Kind: "incident", Tags: nil},
			true,
		},
		{
			"empty values are absent",
			"---\nkind: incident\ndate:\ntitle:   \ntags:\n---\n",
			FrontMatter{Kind: "incident", Tags: []string{}},
			true,
		},
		{
			"an empty block",
			"---\n---\n본문",
			FrontMatter{Tags: nil},
			true,
		},
		{
			"closing line without a final newline",
			"---\nkind: report\n---",
			FrontMatter{Kind: "report", Tags: nil},
			true,
		},
		{
			"trailing spaces on the delimiters",
			"---  \nkind: report\n---\t\n본문",
			FrontMatter{Kind: "report", Tags: nil},
			true,
		},
		{
			"double quoted values",
			"---\nkind: \"decision\"\ndate: \"2026-10-20 15:44\"\ntitle: \"큐: \\\"등급\\\" 모델\"\ntags: [\"a\", \"b c\"]\n---\n",
			FrontMatter{Kind: "decision", Date: text("2026-10-20 15:44"), Title: text(`큐: "등급" 모델`), Tags: []string{"a", "b c"}},
			true,
		},
		{
			"single quoted values",
			"---\nkind: 'decision'\ntitle: 'it''s a title'\ntags: ['x', y]\n---\n",
			FrontMatter{Kind: "decision", Title: text("it's a title"), Tags: []string{"x", "y"}},
			true,
		},
		{
			"a colon inside a plain value",
			"---\nkind: decision\ntitle: 질문: 답변\ndate: 2026-10-20 15:44\n---\n",
			FrontMatter{Kind: "decision", Date: text("2026-10-20 15:44"), Title: text("질문: 답변"), Tags: nil},
			true,
		},
		{
			"comments and blank lines in the block",
			"---\n# 주석\nkind: report # 종류\n\ntitle: C# 언어 # 설명\ntags: [a, b] # 목록\n---\n",
			FrontMatter{Kind: "report", Title: text("C# 언어"), Tags: []string{"a", "b"}},
			true,
		},
		{
			"tags that are not a flow list are ignored",
			"---\nkind: report\ntags: a, b\n---\n",
			FrontMatter{Kind: "report", Tags: []string{}},
			true,
		},
		{
			"a block list of tags is not read",
			"---\nkind: report\ntags:\n  - a\n  - b\ntitle: 제목\n---\n",
			FrontMatter{Kind: "report", Title: text("제목"), Tags: []string{}},
			true,
		},
		{
			"empty flow list and empty items",
			"---\nkind: report\ntags: [ a ,, b , ]\n---\n",
			FrontMatter{Kind: "report", Tags: []string{"a", "b"}},
			true,
		},
		{
			"an empty flow list",
			"---\nkind: report\ntags: []\n---\n",
			FrontMatter{Kind: "report", Tags: []string{}},
			true,
		},
		{
			"indented lines belong to the key above",
			"---\nkind: report\nscope: 첫 줄\n  이어지는 줄: 값\ntitle: 제목\n---\n",
			FrontMatter{Kind: "report", Title: text("제목"), Tags: nil},
			true,
		},
		{
			"a later --- in the body is not the block",
			"---\nkind: report\n---\n본문\n---\ntitle: 아님\n---\n",
			FrontMatter{Kind: "report", Tags: nil},
			true,
		},
		{
			"relations and issues",
			"---\nkind: decision\nsupersedes: 2026-11-05-1333-report-x.md의 결론 중 \"y\"\nrelated: [a.md, 'decisions/b.md']\nissue: AGB-32\nissues: [HAM-4, AGB-50]\n---\n",
			FrontMatter{Kind: "decision", Supersedes: []string{"2026-11-05-1333-report-x.md의 결론 중 \"y\""}, Related: []string{"a.md", "decisions/b.md"}, Issues: []string{"AGB-32", "HAM-4", "AGB-50"}},
			true,
		},
		{
			"empty relation values are empty lists",
			"---\nsupersedes:\nrelated: []\nissue:\n---\n",
			FrontMatter{Supersedes: []string{}, Related: []string{}, Issues: []string{}},
			true,
		},
		{"no block", "# 제목\n\n본문", FrontMatter{}, false},
		{"empty content", "", FrontMatter{}, false},
		{"only the opening line", "---", FrontMatter{}, false},
		{"opening line and no close", "---\nkind: report\ntitle: 제목\n", FrontMatter{}, false},
		{"text before the block", "서문\n---\nkind: report\n---\n", FrontMatter{}, false},
		{"blank line before the block", "\n---\nkind: report\n---\n", FrontMatter{}, false},
		{"byte order mark before the block", "\ufeff---\nkind: report\n---\n", FrontMatter{}, false},
		{"four dashes", "----\nkind: report\n----\n", FrontMatter{}, false},
		{"dashes with text", "--- yaml\nkind: report\n---\n", FrontMatter{}, false},
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

func TestIsPlaceholder(t *testing.T) {
	for v, want := range map[string]bool{
		"<문서 제목>":          true,
		" <a> ":            true,
		"\"<a>\"":          true,
		"[<주제어>, <주제어>]":   true,
		"[a, <주제어>]":       true,
		"<a> b":            false,
		"a <b>":            false,
		"[a, b]":           false,
		"":                 false,
		"<":                false,
		"<>":               true,
		"2026-10-20 15:44": false,
	} {
		if got := IsPlaceholder(v); got != want {
			t.Errorf("IsPlaceholder(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestFrontMatterEntries(t *testing.T) {
	got, ok := FrontMatterEntries("---\nkind: report\n# note\n  nested: x\ntitle:  \"a: b\"  # c\ntags: [x, y]\nempty:\nno colon\n---\n본문: 아님\n")
	want := []FrontMatterEntry{{"kind", "report"}, {"title", "a: b"}, {"tags", "[x, y]"}, {"empty", ""}}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %v, %v, want %v", got, ok, want)
	}
	if _, ok := FrontMatterEntries("본문"); ok {
		t.Error("content without a block must report false")
	}
}
