package cli

import (
	"encoding/json"
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

func TestIssueCreateSendsDecisionNeeded(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("POST", "/v1/issues", canned{status: 201, body: issueJSON, etag: `"1"`})
	h := newHarness(t, srv, false)
	body := func() map[string]any {
		t.Helper()
		var got map[string]any
		if err := json.Unmarshal([]byte(srv.last().Body), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	if code := h.run("issue", "create", "-p", "CLI", "-t", "제목", "--decision-needed", "A안과 B안 중 어느 쪽으로 갈까요?"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if got := body()["decision_needed"]; got != "A안과 B안 중 어느 쪽으로 갈까요?" {
		t.Errorf("decision_needed = %v", got)
	}

	// Leaving it out and none both mean that no question is set: the key is not sent.
	for _, args := range [][]string{
		{"issue", "create", "-p", "CLI", "-t", "제목"},
		{"issue", "create", "-p", "CLI", "-t", "제목", "--decision-needed", "none"},
	} {
		if code := h.run(args...); code != 0 {
			t.Fatalf("%v: code=%d stderr=%s", args, code, h.stderr)
		}
		if _, has := body()["decision_needed"]; has {
			t.Errorf("%v: decision_needed sent: %s", args, srv.last().Body)
		}
	}

	// An empty question is a usage error and nothing reaches the server.
	before := len(srv.reqs)
	if code := h.run("issue", "create", "-p", "CLI", "-t", "제목", "--decision-needed", ""); code != 2 || !strings.Contains(h.stderr.String(), "--decision-needed") {
		t.Errorf("empty question: code=%d stderr=%s", code, h.stderr)
	}
	if len(srv.reqs) != before {
		t.Errorf("a usage error must not reach the server: %d requests", len(srv.reqs)-before)
	}
}

func TestIssueUpdateSendsDecisionNeeded(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("PATCH", "/v1/issues/CLI-1", canned{body: issueJSON, etag: `"4"`})
	h := newHarness(t, srv, false)
	patch := func(args ...string) string {
		t.Helper()
		if code := h.run(append([]string{"issue", "update", "CLI-1"}, args...)...); code != 0 {
			t.Fatalf("%v: code=%d stderr=%s", args, code, h.stderr)
		}
		return strings.TrimSpace(srv.last().Body)
	}

	// The question alone is a change to send.
	if got := patch("--decision-needed", "어느 쪽으로 갈까요?"); got != `{"decision_needed":"어느 쪽으로 갈까요?"}` {
		t.Errorf("set: patch = %s", got)
	}
	// none clears it: merge patch null.
	if got := patch("--decision-needed", "none"); got != `{"decision_needed":null}` {
		t.Errorf("none: patch = %s", got)
	}
	if got := patch("--decision-needed", "질문", "--priority", "P0"); got != `{"decision_needed":"질문","priority":"P0"}` {
		t.Errorf("with another field: patch = %s", got)
	}
	// Without the flag the key is not sent.
	if got := patch("--priority", "P0"); got != `{"priority":"P0"}` {
		t.Errorf("without the flag: patch = %s", got)
	}

	// An empty question is a usage error and nothing reaches the server.
	before := len(srv.reqs)
	if code := h.run("issue", "update", "CLI-1", "--decision-needed", ""); code != 2 || !strings.Contains(h.stderr.String(), "--decision-needed") {
		t.Errorf("empty question: code=%d stderr=%s", code, h.stderr)
	}
	if len(srv.reqs) != before {
		t.Errorf("a usage error must not reach the server: %d requests", len(srv.reqs)-before)
	}
}

func TestDecisionNeededOptionIsInTheHelp(t *testing.T) {
	h := newHarness(t, nil, true)
	for _, args := range [][]string{{"issue", "create", "--help"}, {"issue", "update", "--help"}} {
		if code := h.run(args...); code != 0 || !strings.Contains(h.stdout.String(), "--decision-needed <질문|none>") {
			t.Errorf("%v: code=%d out=%s", args, code, h.stdout)
		}
	}
}
