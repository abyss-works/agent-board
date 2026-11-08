package model

import "testing"

func TestParseNodeID(t *testing.T) {
	for id, want := range map[string][3]string{
		"issue:AGB-10":                   {"issue", "", "AGB-10"},
		"doc:AGB/2026-10-20-1544-x.md":   {"document", "AGB", "2026-10-20-1544-x.md"},
		"doc:XYZ/archive/2026-06-a/b.md": {"document", "XYZ", "archive/2026-06-a/b.md"},
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
