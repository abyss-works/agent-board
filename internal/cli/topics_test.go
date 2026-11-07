package cli

import (
	"strings"
	"testing"
)

func TestHelpTopics(t *testing.T) {
	h := newHarness(t, nil, true)
	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"help", "repo-config"}, []string{"auto_sync", "documents", "follow_up_headings", "glob", "\"name\": \"base\"", "agb doc sync"}},
		{[]string{"help", "doc-format"}, []string{"1048576", "metadata_differs", "not_a_name", "self_reference", "unresolved", "immutable", "supersedes", "--imported", "current", "superseded", "archived"}},
		{[]string{"help", "migrate-v0.8"}, []string{"agb init", "agb doc sync", "agb doc list --state superseded", "moved"}},
		{[]string{"init", "--help"}, []string{"agb help repo-config"}},
		{[]string{"doc", "--help"}, []string{"agb help repo-config", "agb help doc-format"}},
		{[]string{"help", "doc"}, []string{"agb help repo-config", "agb help doc-format"}},
		{[]string{"doc", "put", "--help"}, []string{"agb help doc-format"}},
		{[]string{"--help"}, []string{"주제 (agb help <주제>):", "repo-config", "doc-format", "migrate-v0.8", "agb help <주제> 로 주제별 명세"}},
		{[]string{"help"}, []string{"주제 (agb help <주제>):", "repo-config"}},
	} {
		h.stdout.Reset()
		if code := h.run(c.args...); code != 0 {
			t.Errorf("%v: code=%d stderr=%s", c.args, code, h.stderr)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(h.stdout.String(), w) {
				t.Errorf("%v: output lacks %q:\n%s", c.args, w, h.stdout)
			}
		}
	}
	if code := h.run("help", "no-such-topic"); code != 2 {
		t.Errorf("unknown topic: code=%d", code)
	}
}

// The top-level help lists every topic with its summary.
func TestUsageListsEveryTopic(t *testing.T) {
	for _, tp := range helpTopics {
		if !strings.Contains(Usage, "  "+tp.name) || !strings.Contains(Usage, tp.summary) {
			t.Errorf("Usage lacks the topic %s: %s", tp.name, tp.summary)
		}
		if root.find(tp.name) != nil {
			t.Errorf("the topic %s has the name of a command", tp.name)
		}
	}
}

// The example in the help must be a .agb.json the CLI accepts, and its rules
// must match the files they claim to match.
func TestRepoConfigHelpExampleParses(t *testing.T) {
	cfg, err := parseRepoConfig([]byte(repoConfigExample))
	if err != nil {
		t.Fatalf("the example of help repo-config is refused: %v", err)
	}
	if !cfg.AutoSync || cfg.New == nil || len(cfg.FollowUpHeadings) == 0 {
		t.Errorf("the example lacks the keys it shows: %+v", cfg)
	}
	for rel, want := range map[string]string{
		"history/2026-10-20-1544-decision-x.md": "history/*.md",
		"archive/old.md":                        "archive/*.md",
		"archive/a/b/c.md":                      "archive/*/**/*.md",
		"decisions/d.md":                        "decisions/*.md",
	} {
		r := cfg.ruleFor(rel)
		if r == nil || r.Glob != want {
			t.Errorf("%s: rule = %+v, want glob %s", rel, r, want)
		}
	}
	if !strings.Contains(helpTopicText("repo-config"), strings.TrimSpace(repoConfigExample)) {
		t.Error("the topic does not show the example")
	}
}
