package cli

import (
	"strings"
	"testing"
)

func TestDocGetTakesTheProjectFromTheFlag(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/projects/CLI/documents/"+docDecisionName, canned{body: serverDoc("CLI", docDecisionName, "history", "n")})
	h := newHarness(t, srv, false)

	for label, args := range map[string][]string{
		"short flag":               {"doc", "get", "-p", "CLI", docDecisionName},
		"long flag":                {"doc", "get", "--project", "CLI", docDecisionName},
		"flag after the name":      {"doc", "get", docDecisionName, "-p", "CLI"},
		"flag with equals":         {"doc", "get", "--project=CLI", docDecisionName},
		"flag and same positional": {"doc", "get", "-p", "CLI", "CLI", docDecisionName},
		"positional only":          {"doc", "get", "CLI", docDecisionName},
	} {
		srv.reqs = nil
		if code := h.run(args...); code != 0 {
			t.Errorf("%s: code=%d stderr=%s", label, code, h.stderr)
			continue
		}
		if r := srv.last(); r.Path != "/v1/projects/CLI/documents/"+docDecisionName {
			t.Errorf("%s: path = %s", label, r.Path)
		}
	}
}

func TestDocGetRejectsConflictingOrMissingProject(t *testing.T) {
	srv := newFakeServer(t)
	h := newHarness(t, srv, false)

	for label, tc := range map[string]struct {
		args []string
		want string
	}{
		"flag differs from positional": {[]string{"doc", "get", "-p", "OTHER", "CLI", docDecisionName}, "OTHER"},
		"flag and three positionals":   {[]string{"doc", "get", "-p", "CLI", "CLI", docDecisionName, "x"}, "인자가 너무 많습니다"},
		"flag without a name":          {[]string{"doc", "get", "-p", "CLI"}, "<파일명>"},
		"name only":                    {[]string{"doc", "get", docDecisionName}, "-p"},
	} {
		if code := h.run(tc.args...); code != 2 || !strings.Contains(h.stderr.String(), tc.want) {
			t.Errorf("%s: code=%d stderr=%s", label, code, h.stderr)
		}
	}
	if len(srv.reqs) != 0 {
		t.Errorf("usage errors must not reach the server: %v", srv.paths())
	}
}

func TestUnknownSubcommandSuggestsTheRealCommand(t *testing.T) {
	h := newHarness(t, nil, false)
	for label, tc := range map[string]struct {
		args []string
		want []string
	}{
		"doc show":       {[]string{"doc", "show"}, []string{"가까운 명령: agb doc get"}},
		"issue comments": {[]string{"issue", "comments"}, []string{"agb issue comment", "agb issue activity"}},
	} {
		if code := h.run(tc.args...); code != 2 {
			t.Errorf("%s: code=%d", label, code)
		}
		for _, w := range tc.want {
			if !strings.Contains(h.stderr.String(), w) {
				t.Errorf("%s: stderr lacks %q:\n%s", label, w, h.stderr)
			}
		}
	}
}

func TestBareConfigPrintsItsCommands(t *testing.T) {
	h := newHarness(t, nil, false)
	if code := h.run("config"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	for _, w := range []string{"사용법: agb config <명령>", "show", "set-token"} {
		if !strings.Contains(h.stdout.String(), w) {
			t.Errorf("stdout lacks %q:\n%s", w, h.stdout)
		}
	}
	if h.stderr.Len() != 0 {
		t.Errorf("stderr=%s", h.stderr)
	}
	// The other groups keep asking for a subcommand.
	if code := h.run("issue"); code != 2 {
		t.Errorf("bare issue: code=%d", code)
	}
}

func TestSkillCheckMissingSkillMentionsSkillShow(t *testing.T) {
	h := newHarness(t, nil, false)
	if code := h.run("skill", "check", "--dir", t.TempDir()); code != 1 {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(h.stdout.String(), "agb skill show") {
		t.Errorf("stdout lacks the skill show guidance:\n%s", h.stdout)
	}
}

func TestHintsNameExistingSubcommands(t *testing.T) {
	var walk func(c *command, path string)
	walk = func(c *command, path string) {
		for name, targets := range c.hints {
			if c.find(name) != nil {
				t.Errorf("%s: hint %q is a real subcommand", path, name)
			}
			for _, target := range targets {
				if c.find(target) == nil {
					t.Errorf("%s: hint %q names %q, which does not exist", path, name, target)
				}
			}
		}
		for _, s := range c.subs {
			walk(s, path+" "+s.name)
		}
	}
	walk(root, "agb")
}
