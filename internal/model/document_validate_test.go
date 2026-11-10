package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

// violation is the field and reason of one violation.
type violation struct{ field, reason string }

func violations(t *testing.T, err error) []violation {
	t.Helper()
	if err == nil {
		return nil
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("err = %v, want a *ValidationError", err)
	}
	var out []violation
	for _, f := range ve.Fields {
		out = append(out, violation{f.Field, f.Reason})
	}
	return out
}

const recordName = "2026-10-20-1544-decision-priority-model.md"

func front(lines ...string) string {
	return "---\n" + strings.Join(append(lines, ""), "\n") + "---\n본문\n"
}

func TestValidateDocumentPrecedence(t *testing.T) {
	content := front("kind: decision", "date: 2026-10-20 15:44", "title: 프런트매터 제목", "tags: [fm]",
		"supersedes: a.md", "related: [b.md, c.md]", "issue: AGB-1", "issues: [AGB-2]")

	// The front matter decides when no argument is sent.
	meta, err := ValidateDocument(recordName, content, DocumentArgs{})
	if err != nil {
		t.Fatal(err)
	}
	want := DocumentMeta{
		Kind: "decision", Date: ptr("2026-10-20 15:44"), Title: ptr("프런트매터 제목"), Tags: []string{"fm"},
		Supersedes: []string{"a.md"}, Related: []string{"b.md", "c.md"}, Issues: []string{"AGB-1", "AGB-2"},
	}
	if !reflect.DeepEqual(meta, want) {
		t.Errorf("front matter meta = %+v, want %+v", meta, want)
	}

	// An argument replaces its field, and only its field. A list replaces the
	// whole list; an empty list is a value.
	meta, err = ValidateDocument(recordName, content, DocumentArgs{
		Title: ptr("인자 제목"), Date: ptr("2026-10-21"), Tags: []string{}, Related: []string{"z.md"}, Archived: ptr(true), Path: ptr("history/x.md"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want = DocumentMeta{
		Kind: "decision", Date: ptr("2026-10-21"), Title: ptr("인자 제목"), Tags: []string{},
		Supersedes: []string{"a.md"}, Related: []string{"z.md"}, Issues: []string{"AGB-1", "AGB-2"}, Archived: true, Path: ptr("history/x.md"),
	}
	if !reflect.DeepEqual(meta, want) {
		t.Errorf("argument meta = %+v, want %+v", meta, want)
	}

	// Everything by argument: no front matter needed.
	meta, err = ValidateDocument(recordName, "본문만", DocumentArgs{Title: ptr("제목"), Kind: ptr("decision"), Supersedes: []string{"a.md"}})
	if err != nil || meta.Kind != "decision" || *meta.Title != "제목" || !reflect.DeepEqual(meta.Supersedes, []string{"a.md"}) {
		t.Errorf("argument-only meta = %+v, %v", meta, err)
	}

	// Archived, imported and path are not read from the front matter.
	meta, err = ValidateDocument(recordName, front("kind: decision", "title: 제목", "archived: true", "imported: true", "path: x/y.md"), DocumentArgs{})
	if err != nil || meta.Archived || meta.Imported || meta.Path != nil {
		t.Errorf("front matter must not set archived, imported or path: %+v, %v", meta, err)
	}
}

func TestValidateDocumentFallbacks(t *testing.T) {
	// A record document takes kind and date from its name.
	meta, err := ValidateDocument(recordName, front("title: 제목"), DocumentArgs{})
	if err != nil || meta.Kind != "decision" || meta.Date == nil || *meta.Date != "2026-10-20 15:44" {
		t.Errorf("record fallbacks = %+v, %v", meta, err)
	}
	// An imported document: kind other, date from the name, title from the first heading.
	imp := DocumentArgs{Imported: ptr(true)}
	cases := []struct {
		name, content string
		kind          DocumentKind
		date, title   string
	}{
		{"decisions/2026-10-17-14-30-scope.md", "# 범위 결정\n\n본문", DocumentOther, "2026-10-17 14:30", "범위 결정"},
		{"decisions/2026-10-17-1430-scope.md", "\xef\xbb\xbf# 범위\n", DocumentOther, "2026-10-17 14:30", "범위"},
		{"reports/2026-10-12-src-structure.md", "본문만", DocumentOther, "2026-10-12", ""},
		{"incidents/save-loss.md", "---\nkind: report\ndate: 2026-09-01 10:00\ntitle: 저장 손실\n---\n# 다른 제목", "report", "2026-09-01 10:00", "저장 손실"},
		{"archive/2026-06-x/notes.md", "---\ntitle: 닫히지 않음\n# 제목", DocumentOther, "", "제목"},
		{"notes.md", "본문", DocumentOther, "", ""},
		{"2026-13-45-bad.md", "본문", DocumentOther, "", ""},
	}
	str := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	for _, c := range cases {
		meta, err := ValidateDocument(c.name, c.content, imp)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if meta.Kind != c.kind || str(meta.Date) != c.date || str(meta.Title) != c.title || !meta.Imported {
			t.Errorf("%s: kind %s date %q title %q imported %v; want %s %q %q", c.name, meta.Kind, str(meta.Date), str(meta.Title), meta.Imported, c.kind, c.date, c.title)
		}
	}
}

func TestValidateDocumentRules(t *testing.T) {
	imp := DocumentArgs{Imported: ptr(true)}
	ok := front("kind: decision", "title: 제목")
	cases := []struct {
		label, name, content string
		args                 DocumentArgs
		want                 []violation
	}{
		{"valid record", recordName, ok, DocumentArgs{}, nil},
		{"valid record by arguments alone", recordName, "본문", DocumentArgs{Title: ptr("제목")}, nil},
		{"bad record name", "priority-model.md", ok, DocumentArgs{}, []violation{{"name", ""}}},
		{"bad kind format in the name", "2026-10-20-1544-Plan-x.md", ok, DocumentArgs{}, []violation{{"name", ""}}},
		{"new kind in the name and the front matter", "2026-10-20-1544-lesson-x.md", front("kind: lesson", "title: 제목"), DocumentArgs{}, nil},
		{"new kind in the name only", "2026-10-20-1544-lesson-x.md", front("title: 제목"), DocumentArgs{}, nil},
		{"kind other for a record", recordName, ok, DocumentArgs{Kind: ptr("other")}, []violation{{"kind", ""}}},
		{"kind differs from the name", recordName, front("kind: report", "title: 제목"), DocumentArgs{}, []violation{{"kind", ""}}},
		{"kind argument differs from the name", recordName, ok, DocumentArgs{Kind: ptr("report")}, []violation{{"kind", ""}}},
		{"kind argument fixes the front matter", recordName, front("kind: report", "title: 제목"), DocumentArgs{Kind: ptr("decision")}, nil},
		{"kind of a bad format", recordName, ok, DocumentArgs{Kind: ptr("Plan")}, []violation{{"kind", ""}}},
		{"kind with a hyphen", recordName, ok, DocumentArgs{Kind: ptr("a-b")}, []violation{{"kind", ""}}},
		{"missing title", recordName, front("kind: decision"), DocumentArgs{}, []violation{{"title", "required"}}},
		{"blank title", recordName, front("kind: decision", "title:   "), DocumentArgs{}, []violation{{"title", "required"}}},
		{"bad date", recordName, front("title: 제목", "date: 어제"), DocumentArgs{}, []violation{{"date", ""}}},
		{"impossible date", recordName, front("title: 제목", "date: 2026-02-30"), DocumentArgs{}, []violation{{"date", ""}}},
		{"date with seconds", recordName, front("title: 제목", "date: 2026-10-20 15:44:01"), DocumentArgs{}, []violation{{"date", ""}}},
		{"placeholder title", recordName, front("kind: decision", "title: <문서 제목>"), DocumentArgs{}, []violation{{"title", "placeholder"}}},
		{"placeholder date", recordName, front("title: 제목", "date: <yyyy-mm-dd HH:MM>"), DocumentArgs{}, []violation{{"date", "placeholder"}}},
		{"placeholder kind", recordName, front("kind: <decision>", "title: 제목"), DocumentArgs{}, []violation{{"kind", "placeholder"}}},
		{"placeholder tag", recordName, front("title: 제목", "tags: [a, <주제어>]"), DocumentArgs{}, []violation{{"tags[1]", "placeholder"}}},
		{"placeholder issue", recordName, front("title: 제목", "issue: <AGB-N>"), DocumentArgs{}, []violation{{"issues[0]", "placeholder"}}},
		{"placeholder related item", recordName, front("title: 제목", "related: [<문서 이름>]"), DocumentArgs{}, []violation{{"related[0]", "placeholder"}}},
		{"placeholder in the front matter fixed by an argument", recordName, front("title: <문서 제목>"), DocumentArgs{Title: ptr("제목")}, nil},
		{"placeholder argument", recordName, ok, DocumentArgs{Title: ptr("<문서 제목>")}, []violation{{"title", "placeholder"}}},
		{"placeholder in another key is not the server's concern", recordName, front("title: 제목", "status: <채택>"), DocumentArgs{}, nil},
		{"supersedes with a sentence", recordName, front("title: 제목", `supersedes: a.md의 결론 중 "x"`), DocumentArgs{}, []violation{{"supersedes[0]", "not_a_name"}}},
		{"related with a sentence", recordName, front("title: 제목", "related: [a.md, 일부 문서]"), DocumentArgs{}, []violation{{"related[1]", "not_a_name"}}},
		{"supersedes without .md", recordName, front("title: 제목", "supersedes: a"), DocumentArgs{}, []violation{{"supersedes[0]", "not_a_name"}}},
		{"supersedes none is no relation", recordName, front("title: 제목", "supersedes: none", "related: none"), DocumentArgs{}, nil},
		{"none inside a list is no relation", recordName, front("title: 제목", "related: [none, a.md]"), DocumentArgs{}, nil},
		{"none and empty arguments are no relation", recordName, ok, DocumentArgs{Supersedes: []string{"none"}, Related: []string{"", "none"}}, nil},
		{"None is not none", recordName, front("title: 제목", "supersedes: None"), DocumentArgs{}, []violation{{"supersedes[0]", "not_a_name"}}},
		{"cross-project name is a name", recordName, front("title: 제목", "supersedes: XYZ/a.md"), DocumentArgs{}, nil},
		{"every violation at once", "x.md", "본문", DocumentArgs{Supersedes: []string{"뭔가"}}, []violation{{"name", ""}, {"title", "required"}, {"supersedes[0]", "not_a_name"}}},
		{"too large", recordName, ok + strings.Repeat("a", MaxDocumentBytes), DocumentArgs{}, []violation{{"content", ""}}},

		{"imported path name", "decisions/x.md", "본문", imp, nil},
		{"imported name without .md", "decisions/x.txt", "본문", imp, []violation{{"name", ""}}},
		{"imported template name", "decisions/_template.md", "본문", imp, []violation{{"name", ""}}},
		{"imported example name", "decisions/decision.example.md", "본문", imp, []violation{{"name", ""}}},
		{"imported hidden segment", "archive/.hidden/x.md", "본문", imp, []violation{{"name", ""}}},
		{"imported empty segment", "decisions//x.md", "본문", imp, []violation{{"name", ""}}},
		{"imported backslash", `decisions\x.md`, "본문", imp, []violation{{"name", ""}}},
		{"imported needs no title", "x.md", "본문", imp, nil},
		{"imported keeps placeholders", "x.md", front("title: <문서 제목>"), imp, nil},
		{"imported keeps sentences", "x.md", front(`supersedes: a.md의 일부`), imp, nil},
		{"imported kind of a bad format", "x.md", front("kind: Plan"), imp, []violation{{"kind", ""}}},
		{"imported kind of a new kind", "x.md", front("kind: lesson"), imp, nil},
		{"imported kind other", "x.md", front("kind: other"), imp, nil},
		{"imported bad date", "x.md", front("date: 어제"), imp, []violation{{"date", ""}}},
		{"imported record name", recordName, "본문", imp, nil},
		{"imported too large", "x.md", strings.Repeat("a", MaxDocumentBytes+1), imp, []violation{{"content", ""}}},

		{"path with a hidden segment", recordName, ok, DocumentArgs{Path: ptr("a/.b/c.md")}, []violation{{"path", ""}}},
		{"empty path", recordName, ok, DocumentArgs{Path: ptr("")}, []violation{{"path", ""}}},
		{"path over 500 runes", recordName, ok, DocumentArgs{Path: ptr(strings.Repeat("가", 501))}, []violation{{"path", ""}}},
		{"path of 500 runes", recordName, ok, DocumentArgs{Path: ptr(strings.Repeat("가", 500))}, nil},
	}
	for _, c := range cases {
		_, err := ValidateDocument(c.name, c.content, c.args)
		if got := violations(t, err); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: violations = %v (%v), want %v", c.label, got, err, c.want)
		}
	}
}

func TestMaxDocumentBytesIsOneMiB(t *testing.T) {
	if MaxDocumentBytes != 1024*1024 {
		t.Errorf("MaxDocumentBytes = %d, want %d", MaxDocumentBytes, 1024*1024)
	}
}

func TestValidateDocumentSizeBoundary(t *testing.T) {
	head := "---\ntitle: 제목\n---\n"
	pad := func(n int, unit string) string { return strings.Repeat(unit, n) }
	for label, c := range map[string]struct {
		content string
		ok      bool
	}{
		"exactly the limit":     {head + pad(MaxDocumentBytes-len(head), "a"), true},
		"one byte over":         {head + pad(MaxDocumentBytes-len(head)+1, "a"), false},
		"bytes, not characters": {head + pad((MaxDocumentBytes-len(head))/3+1, "가"), false},
		"multibyte under limit": {head + pad((MaxDocumentBytes-len(head))/3, "가"), true},
	} {
		_, err := ValidateDocument(recordName, c.content, DocumentArgs{})
		got := violations(t, err)
		if c.ok && got != nil || !c.ok && !reflect.DeepEqual(got, []violation{{"content", ""}}) {
			t.Errorf("%s: violations = %v", label, got)
		}
	}
}

func TestDocumentArgsLocation(t *testing.T) {
	apply := func(name string, loc DocumentLocation, in DocumentArgs) DocumentArgs {
		in.ApplyLocation(name, loc)
		return in
	}
	if a := apply("x.md", LocationHistory, DocumentArgs{}); a.HasMetadata() {
		t.Errorf("history sets nothing: %+v", a)
	}
	if a := apply("x.md", LocationArchive, DocumentArgs{}); a.Archived == nil || !*a.Archived || a.Imported != nil {
		t.Errorf("archive = %+v", a)
	}
	if a := apply("decisions/x.md", LocationLegacy, DocumentArgs{}); a.Imported == nil || !*a.Imported || a.Archived != nil {
		t.Errorf("legacy = %+v", a)
	}
	if a := apply("archive/2026-06-x/n.md", LocationLegacy, DocumentArgs{}); a.Imported == nil || a.Archived == nil || !*a.Archived {
		t.Errorf("legacy archive subfolder = %+v", a)
	}
	// Archived or imported sent explicitly: the location is not read.
	if a := apply("x.md", LocationArchive, DocumentArgs{Imported: ptr(false)}); a.Archived != nil {
		t.Errorf("explicit imported must stop the location: %+v", a)
	}
	if a := apply("x.md", LocationLegacy, DocumentArgs{Archived: ptr(false)}); a.Imported != nil {
		t.Errorf("explicit archived must stop the location: %+v", a)
	}
	if (DocumentArgs{}).HasMetadata() || !(DocumentArgs{Tags: []string{}}).HasMetadata() {
		t.Error("HasMetadata must see an empty list as sent and nothing as not sent")
	}
}

func TestDocumentJSON(t *testing.T) {
	at, err := ParseTime("2026-10-21T02:00:00.000Z")
	if err != nil {
		t.Fatal(err)
	}
	d := DocumentResult{Document: Document{
		Project: "AGB", Name: "a.md", Kind: "decision", Tags: []string{}, State: StateCurrent,
		Supersedes: []string{}, SupersededBy: []string{}, Related: []string{"XYZ/b.md"}, Issues: []string{"AGB-1"}, UnresolvedRefs: []UnresolvedRef{{RelSupersedes, "x.md"}},
		SHA256: "9f2c", Size: 6, CreatedAt: at, CreatedBy: "claude", UpdatedAt: at, UpdatedBy: "claude", Location: LocationHistory,
	}, Warnings: []DocumentWarning{{"supersedes[0]", "unresolved", "없습니다."}}}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"project":"AGB","name":"a.md","kind":"decision","date":null,"title":null,"tags":[],"state":"current","archived":false,"imported":false,"path":null,` +
		`"supersedes":[],"superseded_by":[],"related":["XYZ/b.md"],"issues":["AGB-1"],"unresolved_refs":[{"kind":"supersedes","target":"x.md"}],` +
		`"sha256":"9f2c","size":6,"created_at":"2026-10-21T02:00:00.000Z","created_by":"claude","updated_at":"2026-10-21T02:00:00.000Z","updated_by":"claude",` +
		`"location":"history","warnings":[{"field":"supersedes[0]","reason":"unresolved","message":"없습니다."}]}`
	if string(b) != want {
		t.Errorf("document JSON =\n%s\nwant\n%s", b, want)
	}
}

func TestValidateDocumentDropsNoRelationValues(t *testing.T) {
	meta, err := ValidateDocument(recordName, front("title: 제목", "supersedes: none", "related: [none, a.md, ]"), DocumentArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Supersedes) != 0 || !reflect.DeepEqual(meta.Related, []string{"a.md"}) {
		t.Errorf("supersedes = %v, related = %v", meta.Supersedes, meta.Related)
	}
	// An argument that is only none still replaces the front matter.
	meta, err = ValidateDocument(recordName, front("title: 제목", "supersedes: a.md"), DocumentArgs{Supersedes: []string{"none"}})
	if err != nil || len(meta.Supersedes) != 0 {
		t.Errorf("supersedes = %v, err = %v", meta.Supersedes, err)
	}
}

func TestNameWithKindOtherSaysOtherIsForImported(t *testing.T) {
	_, err := ValidateDocument("2026-11-08-1000-other-x.md", front("title: 제목"), DocumentArgs{})
	if got := violations(t, err); len(got) != 1 || got[0].field != "name" || !strings.Contains(err.Error(), "other는 imported 문서에만") {
		t.Errorf("err = %v", err)
	}
}
