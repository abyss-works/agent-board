package cli

import (
	"strings"
	"testing"
)

const (
	ackedIssueJSON   = `{"key":"CLI-1","project":"CLI","number":1,"title":"첫 이슈","body":"","type":"feature","status":"doing","priority":"P1","labels":[],"parent":null,"depends_on":[],"refs":[],"recheck_condition":null,"status_reason":null,"acknowledged_at":"2026-10-17T08:00:00.000Z","acknowledged_by":"owner","version":4,"created_at":"2026-10-17T05:46:06.000Z","updated_at":"2026-10-17T08:00:00.000Z","closed_at":null,"status_changed_at":"2026-10-17T07:10:21.000Z","status_changed_by":"claude","created_by":"claude","updated_by":"owner"}`
	unackedIssueJSON = `{"key":"CLI-2","project":"CLI","number":2,"title":"둘째 이슈","body":"","type":"bug","status":"backlog","priority":"P2","labels":[],"parent":null,"depends_on":[],"refs":[],"recheck_condition":null,"status_reason":null,"acknowledged_at":null,"acknowledged_by":null,"version":1,"created_at":"2026-10-17T05:46:06.000Z","updated_at":"2026-10-17T05:46:06.000Z","closed_at":null,"status_changed_at":"2026-10-17T05:46:06.000Z","status_changed_by":"claude","created_by":"claude","updated_by":"claude"}`
)

func TestIssueAckPostsEveryKey(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("POST", "/v1/issues/CLI-1/acknowledge", canned{body: ackedIssueJSON, etag: `"4"`})
	srv.on("POST", "/v1/issues/CLI-3/acknowledge", canned{body: strings.Replace(ackedIssueJSON, "CLI-1", "CLI-3", 1), etag: `"2"`})
	h := newHarness(t, srv, false)

	if code := h.run("issue", "ack", "CLI-1"); code != 0 {
		t.Fatalf("one key: code=%d stderr=%s", code, h.stderr)
	}
	if h.stdout.String() != ackedIssueJSON+"\n" {
		t.Errorf("a single key prints the server body verbatim: %q", h.stdout.String())
	}

	if code := h.run("issue", "ack", "CLI-1", "CLI-3"); code != 0 {
		t.Fatalf("two keys: code=%d stderr=%s", code, h.stderr)
	}
	lines := strings.Split(strings.TrimSpace(h.stdout.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"CLI-1"`) || !strings.Contains(lines[1], `"CLI-3"`) {
		t.Errorf("several keys print one response body per line:\n%s", h.stdout.String())
	}
	if got := srv.reqs[len(srv.reqs)-2:]; got[0].Method != "POST" || got[0].Path != "/v1/issues/CLI-1/acknowledge" || got[1].Path != "/v1/issues/CLI-3/acknowledge" {
		t.Errorf("requests = %+v", got)
	}

	// The first failure stops the run and keeps what was already done on screen.
	before := len(srv.reqs)
	if code := h.run("issue", "ack", "CLI-1", "CLI-9", "CLI-3"); code != 1 {
		t.Errorf("a missing key: code=%d", code)
	}
	if len(srv.reqs)-before != 2 || !strings.Contains(h.stdout.String(), `"CLI-1"`) || !strings.Contains(h.stderr.String(), "not_found") {
		t.Errorf("stop at the first failure: %d requests\nstdout=%s\nstderr=%s", len(srv.reqs)-before, h.stdout, h.stderr)
	}

	if code := h.run("issue", "ack"); code != 2 {
		t.Errorf("no key: code=%d", code)
	}
}

func TestIssueUnackDeletesTheMark(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("DELETE", "/v1/issues/CLI-2/acknowledge", canned{body: unackedIssueJSON, etag: `"2"`})
	h := newHarness(t, srv, false)
	if code := h.run("issue", "unack", "CLI-2"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Method != "DELETE" || req.Path != "/v1/issues/CLI-2/acknowledge" || h.stdout.String() != unackedIssueJSON+"\n" {
		t.Errorf("request = %+v stdout = %q", req, h.stdout.String())
	}
	if code := h.run("issue", "unack", "CLI-2", "CLI-3"); code != 2 {
		t.Errorf("two keys: code=%d", code)
	}
	if code := h.run("issue", "unack"); code != 2 {
		t.Errorf("no key: code=%d", code)
	}
}

func TestIssueListAcknowledgedFilterAndView(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues", canned{body: listJSON})
	srv.on("GET", "/v1/issues/CLI-1", canned{body: ackedIssueJSON})
	srv.on("GET", "/v1/issues/CLI-2", canned{body: unackedIssueJSON})
	h := newHarness(t, srv, true)

	for _, v := range []string{"true", "false"} {
		if code := h.run("issue", "list", "--acknowledged", v, "-p", "CLI"); code != 0 {
			t.Fatalf("--acknowledged %s: code=%d stderr=%s", v, code, h.stderr)
		}
		if got := srv.last().Query.Get("acknowledged"); got != v || srv.last().Query.Get("project") != "CLI" {
			t.Errorf("--acknowledged %s sent %q", v, srv.last().Query.Encode())
		}
	}
	if code := h.run("issue", "list"); code != 0 || srv.last().Query.Has("acknowledged") {
		t.Errorf("no filter must not send the parameter: %v", srv.last().Query)
	}
	if code := h.run("search", "설계", "--acknowledged", "false"); code != 0 || srv.last().Query.Get("acknowledged") != "false" {
		t.Errorf("search shares the filters: code=%d query=%v", code, srv.last().Query)
	}

	if code := h.run("issue", "get", "CLI-1"); code != 0 || !strings.Contains(h.stdout.String(), "yes (2026-10-17 17:00, owner)") {
		t.Errorf("acknowledged view: code=%d\n%s", code, h.stdout)
	}
	if code := h.run("issue", "get", "CLI-2"); code != 0 || !strings.Contains(h.stdout.String(), "acknowledged:") || strings.Contains(h.stdout.String(), "yes (") {
		t.Errorf("unacknowledged view: code=%d\n%s", code, h.stdout)
	}
	for _, args := range [][]string{{"issue", "list", "--help"}, {"search", "--help"}} {
		if code := h.run(args...); code != 0 || !strings.Contains(h.stdout.String(), "--acknowledged") {
			t.Errorf("%v: code=%d out=%s", args, code, h.stdout)
		}
	}
	if code := h.run("issue", "ack", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "<KEY>") {
		t.Errorf("ack help: code=%d out=%s", code, h.stdout)
	}
	if code := h.run("issue", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "unack") {
		t.Errorf("issue help: code=%d out=%s", code, h.stdout)
	}
}

func TestOverviewTableListsUnacknowledgedIssues(t *testing.T) {
	project := `{"key":"CLI","name":"cli 검증","status":"active","created_at":"2026-10-17T05:30:00.000Z","updated_at":"2026-10-17T05:30:00.000Z","created_by":"claude","updated_by":"claude"}`
	empty := `{"total":0,"items":[]}`
	body := `{"project":` + project + `,"generated_at":"2026-10-17T09:00:00.000Z","counts":{"total":1,"open":1,"by_status":{},"by_type":{},"by_priority":{}},` +
		`"doing":` + empty + `,"review":` + empty + `,"blocked":` + empty + `,"decisions":` + empty + `,"next":` + empty +
		`,"unacknowledged":{"total":1,"items":[` + unackedIssueJSON + `]},"recent_activity":` + empty + `}`
	srv := newFakeServer(t)
	srv.on("GET", "/v1/projects/CLI/overview", canned{body: body})
	h := newHarness(t, srv, true)
	if code := h.run("project", "overview", "CLI"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "== unacknowledged (1) ==") || !strings.Contains(out, "둘째 이슈") {
		t.Errorf("overview table:\n%s", out)
	}
	if strings.Index(out, "== next (0) ==") > strings.Index(out, "== unacknowledged (1) ==") || strings.Index(out, "== unacknowledged (1) ==") > strings.Index(out, "== recent_activity (0) ==") {
		t.Errorf("unacknowledged sits between next and recent_activity:\n%s", out)
	}
}
