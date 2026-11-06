package model

import (
	"reflect"
	"testing"
)

func TestFrontMatterLinks(t *testing.T) {
	content := "---\nkind: decision\nissue: AGB-32, AGB-50\nsupersedes: 2026-11-05-1333-report-x.md의 결론 중 \"y\"\n" +
		"related: [2026-11-06-1354-report-a.md, 'decisions/b.md']\nissues: [HAM-4]\n  issue: AGB-9\ntitle: AGB-1 is not a link\n---\nissue: AGB-2\n"
	issues, sup, rel := FrontMatterLinks(content)
	if !reflect.DeepEqual(issues, []string{"AGB-32", "AGB-50", "HAM-4"}) {
		t.Errorf("issues = %v", issues)
	}
	if !reflect.DeepEqual(sup, []string{"2026-11-05-1333-report-x.md"}) {
		t.Errorf("supersedes = %v", sup)
	}
	if !reflect.DeepEqual(rel, []string{"2026-11-06-1354-report-a.md", "decisions/b.md"}) {
		t.Errorf("related = %v", rel)
	}
	for _, c := range []string{"", "# 제목\nissue: AGB-1\n", "---\nissue: AGB-1\n", "---\nsupersedes: none\n---\n"} {
		if i, s, r := FrontMatterLinks(c); len(i)+len(s)+len(r) != 0 {
			t.Errorf("FrontMatterLinks(%q) = %v %v %v", c, i, s, r)
		}
	}
}

func TestParseNodeID(t *testing.T) {
	for id, want := range map[string][3]string{
		"issue:AGB-10":                   {"issue", "", "AGB-10"},
		"doc:AGB/2026-10-20-1544-x.md":   {"document", "AGB", "2026-10-20-1544-x.md"},
		"doc:AGT/archive/2026-06-a/b.md": {"document", "AGT", "archive/2026-06-a/b.md"},
	} {
		typ, project, key, ok := ParseNodeID(id)
		if !ok || [3]string{typ, project, key} != want {
			t.Errorf("ParseNodeID(%q) = %s %s %s %v", id, typ, project, key, ok)
		}
	}
	for _, id := range []string{"", "issue:", "issue:agb-1", "doc:AGB", "doc:AGB/", "doc:agb/x.md", "AGB-1", "comment:1"} {
		if _, _, _, ok := ParseNodeID(id); ok {
			t.Errorf("ParseNodeID(%q) must fail", id)
		}
	}
}
