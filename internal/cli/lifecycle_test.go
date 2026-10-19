package cli

import (
	"regexp"
	"strings"
	"testing"
)

// helpOf collects the help text of c and of every command below it.
func helpOf(c *command, path string, into map[string]string) {
	into[path] = c.help(path)
	for _, s := range c.subs {
		helpOf(s, path+" "+s.name, into)
	}
}

func TestHelpNamesNeitherTodoNorDecisionType(t *testing.T) {
	texts := map[string]string{"agb": Usage}
	for _, c := range root.subs {
		helpOf(c, "agb "+c.name, texts)
	}
	// The option --decision-needed and the overview section decisions are not the removed type.
	decisionType := regexp.MustCompile(`\bdecision\b`)
	for path, text := range texts {
		text = strings.ReplaceAll(text, "decision-needed", "")
		if strings.Contains(strings.ToLower(text), "todo") {
			t.Errorf("%s: the help names the removed status todo:\n%s", path, text)
		}
		if decisionType.MatchString(text) {
			t.Errorf("%s: the help names the removed type decision:\n%s", path, text)
		}
	}
}

func TestHelpListsTheCurrentStatusesAndTypes(t *testing.T) {
	const (
		statuses = "backlog, doing, review, done, cancelled, deleted"
		types    = "feature, bug, design, idea, observation"
	)
	h := newHarness(t, nil, true)
	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"issue", "list", "--help"}, []string{statuses, types, "open(열린 셋)"}},
		{[]string{"search", "--help"}, []string{statuses, types, "open(열린 셋)"}},
		{[]string{"issue", "create", "--help"}, []string{"feature(기본), bug, design, idea, observation", "기본 backlog"}},
		{[]string{"issue", "update", "--help"}, []string{statuses}},
		{[]string{"queue", "--help"}, []string{"기본 backlog"}},
		{[]string{"template", "show", "--help"}, []string{"(" + types + ")"}},
		{[]string{"project", "overview", "--help"}, []string{"결정 필요"}},
	} {
		if code := h.run(c.args...); code != 0 {
			t.Errorf("%v: code=%d stderr=%s", c.args, code, h.stderr)
			continue
		}
		for _, want := range c.want {
			if !strings.Contains(h.stdout.String(), want) {
				t.Errorf("%v: help lacks %q:\n%s", c.args, want, h.stdout)
			}
		}
	}
}
