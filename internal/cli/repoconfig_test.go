package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGlobMatch(t *testing.T) {
	for _, c := range []struct {
		pattern, path string
		want          bool
	}{
		{"notes/*.md", "notes/a.md", true},
		{"notes/*.md", "notes/sub/a.md", false},
		{"notes/*.md", "other/a.md", false},
		{"*.md", "a.md", true},
		{"*.md", "dir/a.md", false},
		{"notes/?.md", "notes/a.md", true},
		{"notes/?.md", "notes/ab.md", false},
		{"a*c.md", "abbc.md", true},
		{"a*c.md", "ac.md", true},
		{"a*c.md", "abd.md", false},
		{"docs/**/*.md", "docs/a.md", true},
		{"docs/**/*.md", "docs/x/a.md", true},
		{"docs/**/*.md", "docs/x/y/a.md", true},
		{"docs/**/*.md", "other/x/a.md", false},
		{"**/*.md", "a.md", true},
		{"**/*.md", "x/y/a.md", true},
		{"x/**", "x/a.md", true},
		{"x/**", "x/y/a.md", true},
		{"x/**", "x", false},
		{"ref/*/**/*.md", "ref/2026/a.md", true},
		{"ref/*/**/*.md", "ref/2026/sub/a.md", true},
		{"ref/*/**/*.md", "ref/a.md", false},
		{"한글/*.md", "한글/문서.md", true},
		{"a.md", "a.md", true},
		{"a.md", "b/a.md", false},
	} {
		if got := globMatch(c.pattern, c.path); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

const validConfig = `{
  "project": "AGB",
  "auto_sync": true,
  "documents": [
    {"glob": "notes/*.md", "name": "base"},
    {"glob": "ref/**/*.md", "name": "path", "imported": true, "kind": "report", "archived": true}
  ],
  "new": {"template": "tmpl/_{kind}.md", "output": "notes", "values": {"project": "demo"}},
  "follow_up_headings": ["TODO"]
}`

func TestParseRepoConfig(t *testing.T) {
	cfg, err := parseRepoConfig([]byte(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	want := &repoConfig{
		Project:  "AGB",
		AutoSync: true,
		Documents: []docRule{
			{Glob: "notes/*.md", Name: "base"},
			{Glob: "ref/**/*.md", Name: "path", Imported: true, Kind: "report", Archived: true},
		},
		New:              &newDocConfig{Template: "tmpl/_{kind}.md", Output: "notes", Values: map[string]string{"project": "demo"}},
		FollowUpHeadings: []string{"TODO"},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("config = %+v, want %+v", cfg, want)
	}

	// Only the project is needed; the rest is empty.
	cfg, err = parseRepoConfig([]byte(`{"project": "AGB"}`))
	if err != nil || cfg.Project != "AGB" || cfg.AutoSync || len(cfg.Documents) != 0 || cfg.New != nil {
		t.Errorf("minimal = %+v, %v", cfg, err)
	}
}

func TestParseRepoConfigRefusesWhatItDoesNotKnow(t *testing.T) {
	for label, c := range map[string]struct{ text, want string }{
		"empty":              {``, ".agb.json"},
		"not an object":      {`[]`, ".agb.json"},
		"trailing data":      {`{"project": "AGB"} {}`, ".agb.json"},
		"no project":         {`{"documents": []}`, "project"},
		"bad project":        {`{"project": "agb"}`, "project"},
		"unknown field":      {`{"project": "AGB", "folders": []}`, "folders"},
		"unknown rule key":   {`{"project": "AGB", "documents": [{"glob": "a/*.md", "name": "base", "location": "x"}]}`, "location"},
		"unknown new key":    {`{"project": "AGB", "new": {"template": "t", "output": "o", "mode": "x"}}`, "mode"},
		"wrong type":         {`{"project": "AGB", "auto_sync": "yes"}`, "auto_sync"},
		"no glob":            {`{"project": "AGB", "documents": [{"name": "base"}]}`, "documents[0].glob"},
		"no name mode":       {`{"project": "AGB", "documents": [{"glob": "*.md"}]}`, "documents[0].name"},
		"bad name mode":      {`{"project": "AGB", "documents": [{"glob": "*.md", "name": "full"}]}`, "documents[0].name"},
		"bad kind":           {`{"project": "AGB", "documents": [{"glob": "*.md", "name": "base", "kind": "Plan"}]}`, "documents[0].kind"},
		"kind with hyphen":   {`{"project": "AGB", "documents": [{"glob": "*.md", "name": "base", "kind": "a-b"}]}`, "documents[0].kind"},
		"other not imported": {`{"project": "AGB", "documents": [{"glob": "*.md", "name": "base", "kind": "other"}]}`, "documents[0].kind"},
		"partial doublestar": {`{"project": "AGB", "documents": [{"glob": "a**/x.md", "name": "base"}]}`, "documents[0].glob"},
		"absolute glob":      {`{"project": "AGB", "documents": [{"glob": "/a/*.md", "name": "base"}]}`, "documents[0].glob"},
		"backslash glob":     {`{"project": "AGB", "documents": [{"glob": "a\\*.md", "name": "base"}]}`, "documents[0].glob"},
		"new without out":    {`{"project": "AGB", "new": {"template": "t"}}`, "new.output"},
		"new without tmpl":   {`{"project": "AGB", "new": {"output": "o"}}`, "new.template"},
		"empty heading":      {`{"project": "AGB", "follow_up_headings": [""]}`, "follow_up_headings[0]"},
		"empty value key":    {`{"project": "AGB", "new": {"template": "t", "output": "o", "values": {"": "x"}}}`, "new.values"},
	} {
		_, err := parseRepoConfig([]byte(c.text))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %q", label, err, c.want)
		}
	}
}

func TestRepoConfigRuleIsTheFirstThatMatches(t *testing.T) {
	cfg, err := parseRepoConfig([]byte(`{"project": "AGB", "documents": [
		{"glob": "a/*.md", "name": "base", "kind": "report"},
		{"glob": "a/**/*.md", "name": "path"},
		{"glob": "**/*.md", "name": "path", "imported": true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{"a/x.md": 0, "a/b/x.md": 1, "z/x.md": 2, "x.md": 2} {
		r := cfg.ruleFor(path)
		if r == nil || r != &cfg.Documents[want] {
			t.Errorf("ruleFor(%q) = %+v, want rule %d", path, r, want)
		}
	}
	if (&repoConfig{Project: "AGB"}).ruleFor("a.md") != nil {
		t.Errorf("no rules, no match")
	}
}

func TestDocRuleName(t *testing.T) {
	r := docRule{Glob: "**/*.md", Name: "base"}
	if got := r.docName("ref/2026/a.md"); got != "a.md" {
		t.Errorf("base = %q", got)
	}
	r.Name = "path"
	if got := r.docName("ref/2026/a.md"); got != "ref/2026/a.md" {
		t.Errorf("path = %q", got)
	}
}

func TestLoadRepoConfig(t *testing.T) {
	root := t.TempDir()
	if cfg, err := loadRepoConfig(root); err != nil || cfg != nil {
		t.Errorf("no file: %v, %v", cfg, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".agb.json"), []byte(validConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadRepoConfig(root)
	if err != nil || cfg == nil || cfg.Project != "AGB" {
		t.Errorf("load: %v, %v", cfg, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".agb.json"), []byte(`{"project": "AGB", "bogus": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRepoConfig(root); err == nil || !strings.Contains(err.Error(), ".agb.json") {
		t.Errorf("invalid file: %v", err)
	}
}
