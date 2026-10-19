package cli

import (
	"encoding/json"
	"fmt"
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

const decisionQuestion = "A안과 B안 중 어느 쪽으로 갈까요?"

// withDecision gives an issue object of the fixtures a decision question.
func withDecision(issue, question string) string {
	return strings.Replace(issue, `"recheck_condition":null,`, `"recheck_condition":null,"decision_needed":"`+question+`",`, 1)
}

// issueList wraps issue objects in the list envelope.
func issueList(items ...string) string {
	return fmt.Sprintf(`{"items":[%s],"total":%d,"limit":50,"offset":0}`, strings.Join(items, ","), len(items))
}

// plainSummary is the one issue of listJSON: no decision question.
func plainSummary() string {
	return strings.TrimSuffix(strings.TrimPrefix(listJSON, `{"items":[`), `],"total":1,"limit":50,"offset":0}`)
}

// queueItem makes a queue item of an issue object.
func queueItem(summary string) string {
	return strings.TrimSuffix(summary, "}") + `,"ready":true,"blocked_by":[]}`
}

// tableLines splits a printed table into its lines.
func tableLines(out string) []string {
	return strings.Split(strings.TrimRight(out, "\n"), "\n")
}

func TestIssueViewShowsDecisionNeeded(t *testing.T) {
	asking := withDecision(issueJSON, decisionQuestion)
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues/CLI-1", canned{body: asking})
	srv.on("GET", "/v1/issues/CLI-2", canned{body: issueJSON})
	srv.on("GET", "/v1/issues/CLI-3", canned{body: strings.Replace(issueJSON, `"recheck_condition":null,`, `"recheck_condition":null,"decision_needed":null,`, 1)})
	srv.on("POST", "/v1/issues", canned{status: 201, body: asking, etag: `"1"`})
	srv.on("PATCH", "/v1/issues/CLI-1", canned{body: asking, etag: `"4"`})
	h := newHarness(t, srv, true)

	if code := h.run("issue", "get", "CLI-1"); code != 0 || fieldValue(h.stdout.String(), "결정 필요") != decisionQuestion {
		t.Errorf("with a question: code=%d\n%s", code, h.stdout)
	}
	// The line is left out when the issue has no question: absent or null.
	for _, key := range []string{"CLI-2", "CLI-3"} {
		if code := h.run("issue", "get", key); code != 0 || strings.Contains(h.stdout.String(), "결정 필요") {
			t.Errorf("%s without a question: code=%d\n%s", key, code, h.stdout)
		}
	}
	// Create and update print the same view.
	for _, args := range [][]string{
		{"issue", "create", "-p", "CLI", "-t", "제목", "--decision-needed", decisionQuestion},
		{"issue", "update", "CLI-1", "--decision-needed", decisionQuestion},
	} {
		if code := h.run(args...); code != 0 || fieldValue(h.stdout.String(), "결정 필요") != decisionQuestion {
			t.Errorf("%v: code=%d\n%s", args, code, h.stdout)
		}
	}
	// JSON stays the server body.
	hj := newHarness(t, srv, false)
	if code := hj.run("issue", "get", "CLI-1"); code != 0 || hj.stdout.String() != asking+"\n" {
		t.Errorf("json: code=%d out=%q", code, hj.stdout.String())
	}
}

func TestIssueTableAddsDecisionColumn(t *testing.T) {
	plain := plainSummary()
	asking := withDecision(plain, decisionQuestion)
	other := strings.Replace(plain, "CLI-1", "CLI-2", 1)
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues", canned{body: issueList(asking, other)})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0

	// The column is the last one. Rows without a question show "-".
	for _, args := range [][]string{{"issue", "list"}, {"search", "설계"}} {
		if code := h.run(args...); code != 0 {
			t.Fatalf("%v: code=%d stderr=%s", args, code, h.stderr)
		}
		lines := tableLines(h.stdout.String())
		if len(lines) != 3 {
			t.Fatalf("%v: table:\n%s", args, h.stdout)
		}
		if !regexp.MustCompile(`^KEY\s+STATUS\s+PRI\s+TYPE\s+LABELS\s+UPDATED\s+TITLE\s+DECISION$`).MatchString(lines[0]) {
			t.Errorf("%v: header = %q", args, lines[0])
		}
		if !strings.HasSuffix(lines[1], "  "+decisionQuestion) || !strings.HasSuffix(lines[2], "  -") {
			t.Errorf("%v: rows:\n%s", args, h.stdout)
		}
	}

	// A long question is cut to the terminal width, as the last column of a table is.
	srv.on("GET", "/v1/issues", canned{body: issueList(withDecision(plain, strings.Repeat("긴 질문 ", 30)), other)})
	h.app.TermWidth = 80
	if code := h.run("issue", "list"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	lines := tableLines(h.stdout.String())
	for _, l := range lines {
		if displayWidth(l) > 80 {
			t.Errorf("line wider than 80 (%d): %q", displayWidth(l), l)
		}
	}
	if !strings.HasSuffix(lines[1], "…") {
		t.Errorf("a long question ends with an ellipsis: %q", lines[1])
	}

	// Without a question in any row the table keeps its columns.
	srv.on("GET", "/v1/issues", canned{body: listJSON})
	if code := h.run("issue", "list"); code != 0 || strings.Contains(h.stdout.String(), "DECISION") {
		t.Errorf("no question: code=%d\n%s", code, h.stdout)
	}
}

func TestQueueTableAddsDecisionColumn(t *testing.T) {
	plain := plainSummary()
	srv := newFakeServer(t)
	srv.on("GET", "/v1/queue", canned{body: issueList(queueItem(withDecision(plain, decisionQuestion)), queueItem(strings.Replace(plain, "CLI-1", "CLI-2", 1)))})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0

	if code := h.run("queue"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	lines := tableLines(h.stdout.String())
	if len(lines) != 3 || !regexp.MustCompile(`^KEY\s+PRI\s+STATUS\s+READY\s+BLOCKED_BY\s+TITLE\s+DECISION$`).MatchString(lines[0]) {
		t.Fatalf("table:\n%s", h.stdout)
	}
	if !strings.HasSuffix(lines[1], "  "+decisionQuestion) || !strings.HasSuffix(lines[2], "  -") {
		t.Errorf("rows:\n%s", h.stdout)
	}

	srv.on("GET", "/v1/queue", canned{body: issueList(queueItem(plain))})
	if code := h.run("queue"); code != 0 || strings.Contains(h.stdout.String(), "DECISION") {
		t.Errorf("no question: code=%d\n%s", code, h.stdout)
	}
}

func TestBoardTablesAddDecisionColumn(t *testing.T) {
	plain := plainSummary()
	asking := withDecision(plain, decisionQuestion)
	srv := newFakeServer(t)
	srv.on("GET", "/v1/board", canned{body: `{"project":"CLI","columns":[` +
		`{"status":"backlog","total":2,"issues":[` + asking + `,` + strings.Replace(plain, "CLI-1", "CLI-2", 1) + `]},` +
		`{"status":"doing","total":1,"issues":[` + strings.Replace(plain, "CLI-1", "CLI-3", 1) + `]}]}`})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0

	if code := h.run("board"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	out := h.stdout.String()
	backlog, doing := out[strings.Index(out, "== backlog"):strings.Index(out, "== doing")], out[strings.Index(out, "== doing"):]
	if !regexp.MustCompile(`(?m)^KEY\s+PRI\s+TYPE\s+LABELS\s+TITLE\s+DECISION$`).MatchString(backlog) || !strings.Contains(backlog, "  "+decisionQuestion+"\n") {
		t.Errorf("the column with a question gets the DECISION column:\n%s", backlog)
	}
	if strings.Contains(doing, "DECISION") {
		t.Errorf("the column without one keeps its columns:\n%s", doing)
	}
}

func TestOverviewTablesAddDecisionColumn(t *testing.T) {
	project := `{"key":"CLI","name":"cli 검증","status":"active","created_at":"2026-10-17T05:30:00.000Z","updated_at":"2026-10-17T05:30:00.000Z","created_by":"claude","updated_by":"claude"}`
	empty := `{"total":0,"items":[]}`
	plain := plainSummary()
	body := `{"project":` + project + `,"generated_at":"2026-10-17T09:00:00.000Z","counts":{"total":2,"open":2,"by_status":{},"by_type":{},"by_priority":{}},` +
		`"doing":` + empty + `,"review":` + empty + `,"blocked":` + empty +
		`,"decisions":{"total":1,"items":[` + withDecision(plain, decisionQuestion) + `]}` +
		`,"next":{"total":1,"items":[` + queueItem(strings.Replace(plain, "CLI-1", "CLI-2", 1)) + `]}` +
		`,"unacknowledged":` + empty + `,"recent_activity":` + empty + `}`
	srv := newFakeServer(t)
	srv.on("GET", "/v1/projects/CLI/overview", canned{body: body})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0

	if code := h.run("project", "overview", "CLI"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	out := h.stdout.String()
	decisions, next := out[strings.Index(out, "== decisions (1) =="):strings.Index(out, "== next (1) ==")], out[strings.Index(out, "== next (1) =="):]
	if !strings.Contains(decisions, "DECISION") || !strings.Contains(decisions, decisionQuestion) {
		t.Errorf("the decisions section shows the question:\n%s", decisions)
	}
	if strings.Contains(next, "DECISION") || strings.Count(out, "DECISION") != 1 {
		t.Errorf("a section without a question keeps its columns:\n%s", out)
	}
}

func TestActivityShowsDecisionNeededAndAcknowledgedChanges(t *testing.T) {
	record := func(id int, changes string) string {
		return fmt.Sprintf(`{"id":%d,"issue":"CLI-1","project":"CLI","kind":"updated","actor":"claude","session_id":"s","client_host":"h","at":"2026-10-17T07:10:21.000Z","changes":%s,"summary":null,"body":null}`, id, changes)
	}
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues/CLI-1/activity", canned{body: `{"items":[` +
		record(4, `{"acknowledged":{"from":true,"to":false},"title":{"from":"이전 제목","to":"새 제목"}}`) + `,` +
		record(3, `{"decision_needed":{"from":"어느 쪽으로 갈까요?","to":null}}`) + `,` +
		record(2, `{"decision_needed":{"from":"A안?","to":"B안?"}}`) + `,` +
		record(1, `{"decision_needed":{"from":null,"to":"어느 쪽으로 갈까요?"}}`) + `],"total":4,"limit":50,"offset":0}`})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0

	if code := h.run("issue", "activity", "CLI-1"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	for _, want := range []string{
		"acknowledged: true → false; title: 이전 제목 → 새 제목",
		"decision_needed: 어느 쪽으로 갈까요? → -",
		"decision_needed: A안? → B안?",
		"decision_needed: - → 어느 쪽으로 갈까요?",
	} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("activity lacks %q:\n%s", want, h.stdout)
		}
	}
}
