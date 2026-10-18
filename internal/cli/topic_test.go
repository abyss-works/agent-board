package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

const topicsJSON = `{"items":[` +
	`{"id":1,"project":"CLI","name":"이슈 흐름","parent":null,"path":"이슈 흐름","status":"active","created_at":"2026-10-18T06:30:00.000Z","updated_at":"2026-10-18T06:30:00.000Z","created_by":"owner","updated_by":"owner"},` +
	`{"id":4,"project":"CLI","name":"트리아지","parent":1,"path":"이슈 흐름/트리아지","status":"active","created_at":"2026-10-18T06:30:00.000Z","updated_at":"2026-10-18T06:30:00.000Z","created_by":"owner","updated_by":"owner"},` +
	`{"id":2,"project":"CLI","name":"인프라","parent":null,"path":"인프라","status":"active","created_at":"2026-10-18T06:30:00.000Z","updated_at":"2026-10-18T06:30:00.000Z","created_by":"owner","updated_by":"owner"}]}`

const topicJSON = `{"id":5,"project":"CLI","name":"상태 사유","parent":1,"path":"이슈 흐름/상태 사유","status":"active","created_at":"2026-10-18T06:30:00.000Z","updated_at":"2026-10-18T06:30:00.000Z","created_by":"claude","updated_by":"claude"}`

func newTopicHarness(t *testing.T, tty bool) (*harness, *fakeServer) {
	t.Helper()
	srv := newFakeServer(t)
	srv.on("GET", "/v1/projects/CLI/topics", canned{body: topicsJSON})
	srv.on("POST", "/v1/projects/CLI/topics", canned{status: 201, body: topicJSON})
	srv.on("PATCH", "/v1/topics/5", canned{body: topicJSON})
	srv.on("DELETE", "/v1/topics/5", canned{body: strings.Replace(topicJSON, `"status":"active"`, `"status":"deleted"`, 1)})
	srv.on("POST", "/v1/issues", canned{status: 201, body: issueJSON})
	srv.on("PATCH", "/v1/issues/CLI-1", canned{body: issueJSON})
	srv.on("GET", "/v1/issues", canned{body: listJSON})
	return newHarness(t, srv, tty), srv
}

func (f *fakeServer) paths() []string {
	out := make([]string, len(f.reqs))
	for i, r := range f.reqs {
		out[i] = r.Method + " " + r.Path
	}
	return out
}

func TestTopicListShowsATree(t *testing.T) {
	h, srv := newTopicHarness(t, true)
	if code := h.run("topic", "list", "-p", "CLI"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if got := srv.last().Query.Encode(); got != "" {
		t.Errorf("query = %q, want the server default", got)
	}
	lines := strings.Split(strings.TrimRight(h.stdout.String(), "\n"), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "ID") || !strings.Contains(lines[1], "이슈 흐름") || !strings.Contains(lines[3], "인프라") {
		t.Fatalf("table:\n%s", h.stdout)
	}
	// Topics under a top-level topic are indented in the NAME column.
	nameCol := strings.Index(lines[0], "NAME")
	top, child := lines[1], lines[2]
	if !strings.HasPrefix(top[nameCol:], "이슈 흐름") || !strings.HasPrefix(child[nameCol:], "  트리아지") || strings.HasPrefix(child[nameCol:], "   ") {
		t.Errorf("indentation:\n%s", h.stdout)
	}

	if code := h.run("topic", "list", "-p", "CLI", "--status", "all"); code != 0 || srv.last().Query.Get("status") != "all" {
		t.Errorf("--status: code=%d query=%v", code, srv.last().Query)
	}
	// JSON is the server body as it is.
	hj := newHarness(t, srv, false)
	if code := hj.run("topic", "list", "-p", "CLI"); code != 0 || hj.stdout.String() != topicsJSON+"\n" {
		t.Errorf("json: code=%d out=%q", code, hj.stdout.String())
	}
	if code := h.run("topic", "list"); code != 2 || !strings.Contains(h.stderr.String(), "--project") {
		t.Errorf("without a project: code=%d stderr=%s", code, h.stderr)
	}
}

func TestTopicCreateUpdateDelete(t *testing.T) {
	h, srv := newTopicHarness(t, false)

	if code := h.run("topic", "create", "-p", "CLI", "인프라"); code != 0 {
		t.Fatalf("top-level: code=%d stderr=%s", code, h.stderr)
	}
	if got := strings.TrimSpace(srv.last().Body); got != `{"name":"인프라"}` {
		t.Errorf("top-level body = %s", got)
	}
	// A parent by name is looked up first.
	srv.reqs = nil
	if code := h.run("topic", "create", "-p", "CLI", "상태 사유", "--parent", "이슈 흐름"); code != 0 {
		t.Fatalf("by name: code=%d stderr=%s", code, h.stderr)
	}
	if !equalStrings(srv.paths(), []string{"GET /v1/projects/CLI/topics", "POST /v1/projects/CLI/topics"}) {
		t.Errorf("requests = %v", srv.paths())
	}
	var got map[string]any
	json.Unmarshal([]byte(srv.last().Body), &got)
	if got["name"] != "상태 사유" || got["parent"] != float64(1) {
		t.Errorf("body = %v", got)
	}
	// A numeric parent needs no lookup.
	srv.reqs = nil
	if code := h.run("topic", "create", "-p", "CLI", "x", "--parent", "2"); code != 0 || len(srv.reqs) != 1 {
		t.Errorf("by id: code=%d requests=%v", code, srv.paths())
	}
	// A child topic cannot be a parent, and an unknown name is a usage error that sends nothing.
	srv.reqs = nil
	for _, parent := range []string{"이슈 흐름/트리아지", "없는 대주제"} {
		if code := h.run("topic", "create", "-p", "CLI", "y", "--parent", parent); code != 2 {
			t.Errorf("parent %q: code=%d stderr=%s", parent, code, h.stderr)
		}
	}
	for _, r := range srv.reqs {
		if r.Method != "GET" {
			t.Errorf("a failed lookup wrote: %+v", r)
		}
	}
	if code := h.run("topic", "create", "-p", "CLI"); code != 2 {
		t.Errorf("no name: code=%d", code)
	}
	if code := h.run("topic", "create", "인프라"); code != 2 {
		t.Errorf("no project: code=%d", code)
	}

	// Update.
	srv.reqs = nil
	if code := h.run("topic", "update", "5", "--name", "새 이름", "--status", "deleted"); code != 0 {
		t.Fatalf("update: code=%d stderr=%s", code, h.stderr)
	}
	if got := strings.TrimSpace(srv.last().Body); got != `{"name":"새 이름","status":"deleted"}` || srv.last().Method != "PATCH" {
		t.Errorf("update body = %s", got)
	}
	if code := h.run("topic", "update", "5", "--no-parent"); code != 0 || strings.TrimSpace(srv.last().Body) != `{"parent":null}` {
		t.Errorf("--no-parent: code=%d body=%s", code, srv.last().Body)
	}
	if code := h.run("topic", "update", "5", "--parent", "2"); code != 0 || strings.TrimSpace(srv.last().Body) != `{"parent":2}` {
		t.Errorf("--parent id: code=%d body=%s", code, srv.last().Body)
	}
	if code := h.run("topic", "update", "5", "--parent", "인프라"); code != 2 || !strings.Contains(h.stderr.String(), "-p") {
		t.Errorf("a parent name without a project: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("topic", "update", "5", "-p", "CLI", "--parent", "인프라"); code != 0 || strings.TrimSpace(srv.last().Body) != `{"parent":2}` {
		t.Errorf("--parent name: code=%d body=%s", code, srv.last().Body)
	}
	if code := h.run("topic", "update", "5"); code != 2 {
		t.Errorf("empty patch: code=%d", code)
	}
	if code := h.run("topic", "update", "five", "--name", "x"); code != 2 {
		t.Errorf("bad id: code=%d", code)
	}
	if code := h.run("topic", "update", "5", "--parent", "2", "--no-parent"); code != 2 {
		t.Errorf("both parent flags: code=%d", code)
	}

	// Delete.
	if code := h.run("topic", "delete", "5"); code != 0 || srv.last().Method != "DELETE" || srv.last().Path != "/v1/topics/5" || !strings.Contains(h.stdout.String(), `"deleted"`) {
		t.Errorf("delete: code=%d req=%+v", code, srv.last())
	}
	if code := h.run("topic", "delete"); code != 2 {
		t.Errorf("delete without id: code=%d", code)
	}
}

func TestIssueTopicOption(t *testing.T) {
	h, srv := newTopicHarness(t, false)
	body := func() map[string]any {
		var m map[string]any
		json.Unmarshal([]byte(srv.last().Body), &m)
		return m
	}

	// create: a path is resolved with the topic list of the issue's project.
	if code := h.run("issue", "create", "-p", "CLI", "-t", "제목", "--topic", "이슈 흐름/트리아지"); code != 0 {
		t.Fatalf("path: code=%d stderr=%s", code, h.stderr)
	}
	if body()["topic"] != float64(4) {
		t.Errorf("create body = %v", srv.last().Body)
	}
	if code := h.run("issue", "create", "-p", "CLI", "-t", "제목", "--topic", "인프라"); code != 0 || body()["topic"] != float64(2) {
		t.Errorf("top-level path: code=%d body=%s", code, srv.last().Body)
	}
	srv.reqs = nil
	if code := h.run("issue", "create", "-p", "CLI", "-t", "제목", "--topic", "4"); code != 0 || body()["topic"] != float64(4) || len(srv.reqs) != 1 {
		t.Errorf("by id: code=%d body=%s requests=%v", code, srv.last().Body, srv.paths())
	}
	if code := h.run("issue", "create", "-p", "CLI", "-t", "제목"); code != 0 {
		t.Fatalf("no topic: code=%d", code)
	}
	if _, has := body()["topic"]; has {
		t.Errorf("topic sent without --topic: %s", srv.last().Body)
	}
	// An unknown path is a usage error and nothing is written.
	srv.reqs = nil
	if code := h.run("issue", "create", "-p", "CLI", "-t", "제목", "--topic", "없는 주제"); code != 2 || !strings.Contains(h.stderr.String(), "agb topic list -p CLI") {
		t.Errorf("unknown path: code=%d stderr=%s", code, h.stderr)
	}
	for _, r := range srv.reqs {
		if r.Method == "POST" {
			t.Errorf("an unresolved topic must not create the issue: %+v", r)
		}
	}
	if code := h.run("issue", "create", "-p", "CLI", "-t", "제목", "--topic", "none"); code != 0 {
		t.Errorf("none on create: code=%d", code)
	}

	// update: the project comes from the issue key; none clears the topic.
	srv.reqs = nil
	if code := h.run("issue", "update", "CLI-1", "--topic", "이슈 흐름/트리아지"); code != 0 || body()["topic"] != float64(4) {
		t.Errorf("update: code=%d body=%s", code, srv.last().Body)
	}
	if !equalStrings(srv.paths(), []string{"GET /v1/projects/CLI/topics", "PATCH /v1/issues/CLI-1"}) {
		t.Errorf("update requests = %v", srv.paths())
	}
	if code := h.run("issue", "update", "CLI-1", "--topic", "none"); code != 0 {
		t.Fatalf("clear: code=%d stderr=%s", code, h.stderr)
	}
	if v, has := body()["topic"]; !has || v != nil {
		t.Errorf("--topic none must send topic:null: %s", srv.last().Body)
	}
	if code := h.run("issue", "update", "CLI-1", "--topic", "없는 주제"); code != 2 {
		t.Errorf("update unknown path: code=%d", code)
	}

	// list: IDs and none pass through, paths are resolved, flags repeat as OR.
	if code := h.run("issue", "list", "-p", "CLI", "--topic", "이슈 흐름"); code != 0 || srv.last().Query.Get("topic") != "1" {
		t.Errorf("list by path: code=%d query=%v", code, srv.last().Query)
	}
	if code := h.run("issue", "list", "--topic", "4", "--topic", "none"); code != 0 || srv.last().Query.Get("topic") != "4,none" {
		t.Errorf("list by id and none: code=%d query=%v", code, srv.last().Query)
	}
	if code := h.run("issue", "list", "--topic", "이슈 흐름"); code != 2 {
		t.Errorf("list by path without a project: code=%d", code)
	}
	if code := h.run("issue", "list", "-p", "CLI,OTH", "--topic", "이슈 흐름"); code != 2 {
		t.Errorf("list by path with several projects: code=%d", code)
	}
	if code := h.run("search", "설계", "-p", "CLI", "--topic", "인프라"); code != 0 || srv.last().Query.Get("topic") != "2" {
		t.Errorf("search: code=%d query=%v", code, srv.last().Query)
	}
}

func TestIssueViewShowsTopicPath(t *testing.T) {
	srv := newFakeServer(t)
	withTopic := strings.Replace(issueJSON, `"recheck_condition":null,`, `"recheck_condition":null,"topic":4,"topic_path":"이슈 흐름/트리아지",`, 1)
	srv.on("GET", "/v1/issues/CLI-1", canned{body: withTopic})
	srv.on("GET", "/v1/issues/CLI-2", canned{body: issueJSON})
	h := newHarness(t, srv, true)
	if code := h.run("issue", "get", "CLI-1"); code != 0 || !strings.Contains(h.stdout.String(), "이슈 흐름/트리아지 (4)") {
		t.Errorf("with a topic: code=%d\n%s", code, h.stdout)
	}
	if code := h.run("issue", "get", "CLI-2"); code != 0 || !strings.Contains(h.stdout.String(), "topic:") {
		t.Errorf("without a topic: code=%d\n%s", code, h.stdout)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"topic", "--help"}, "create"},
		{[]string{"topic", "create", "--help"}, "--parent"},
		{[]string{"topic", "update", "--help"}, "--no-parent"},
		{[]string{"issue", "create", "--help"}, "--topic"},
		{[]string{"issue", "update", "--help"}, "--topic"},
		{[]string{"issue", "list", "--help"}, "--topic"},
		{[]string{"--help"}, "topic"},
	} {
		if code := h.run(c.args...); code != 0 || !strings.Contains(h.stdout.String(), c.want) {
			t.Errorf("%v: code=%d out=%s", c.args, code, h.stdout)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
