package cli

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

const reasonIssueJSON = `{"key":"CLI-1","project":"CLI","number":1,"title":"첫 이슈","body":"","type":"feature","status":"cancelled","priority":"P1","labels":[],"parent":null,"depends_on":[],"refs":[],"recheck_condition":null,"status_reason":"CLI-9와 중복","version":4,"created_at":"2026-10-17T05:46:06.000Z","updated_at":"2026-10-17T07:10:21.000Z","closed_at":"2026-10-17T07:10:21.000Z","status_changed_at":"2026-10-17T07:10:21.000Z","status_changed_by":"claude","created_by":"owner","updated_by":"claude"}`

func TestIssueUpdateSendsStatusReason(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("PATCH", "/v1/issues/CLI-1", canned{body: reasonIssueJSON, etag: `"4"`})
	h := newHarness(t, srv, false)

	if code := h.run("issue", "update", "CLI-1", "--status", "cancelled", "--reason", "CLI-9와 중복"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(srv.last().Body), &got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "cancelled" || got["status_reason"] != "CLI-9와 중복" || len(got) != 2 {
		t.Errorf("patch = %v", got)
	}

	// Without --reason the key is absent, so the server picks the default message.
	if code := h.run("issue", "update", "CLI-1", "--status", "doing"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if strings.Contains(srv.last().Body, "status_reason") {
		t.Errorf("status_reason sent without --reason: %s", srv.last().Body)
	}

	// The CLI sends --reason as given; whether it fits the request is the server's rule.
	if code := h.run("issue", "update", "CLI-1", "--reason", "이유만"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if got := strings.TrimSpace(srv.last().Body); got != `{"status_reason":"이유만"}` {
		t.Errorf("reason only patch = %s", got)
	}
}

func TestIssueDeleteRequiresReason(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("DELETE", "/v1/issues/CLI-1", canned{body: reasonIssueJSON, etag: `"4"`})
	h := newHarness(t, srv, false)

	if code := h.run("issue", "delete", "CLI-1"); code != 2 || !strings.Contains(h.stderr.String(), "--reason") {
		t.Errorf("without --reason: code=%d stderr=%s", code, h.stderr)
	}
	if len(srv.reqs) != 0 {
		t.Errorf("a usage error must not reach the server: %d requests", len(srv.reqs))
	}
	if code := h.run("issue", "delete", "CLI-1", "--reason", "잘못 만든 이슈", "--if-match", "3"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	req := srv.last()
	if req.Method != "DELETE" || req.Query.Get("reason") != "잘못 만든 이슈" || req.Header.Get("If-Match") != `"3"` {
		t.Errorf("request = %+v", req)
	}
	if code := h.run("issue", "delete", "CLI-1", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "--reason") {
		t.Errorf("help: code=%d out=%s", code, h.stdout)
	}
}

func TestIssueViewShowsStatusReason(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues/CLI-1", canned{body: reasonIssueJSON, etag: `"4"`})
	srv.on("GET", "/v1/issues/CLI-2", canned{body: strings.Replace(reasonIssueJSON, `"status_reason":"CLI-9와 중복"`, `"status_reason":null`, 1)})
	h := newHarness(t, srv, true)
	if code := h.run("issue", "get", "CLI-1"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if out := h.stdout.String(); !strings.Contains(out, "status_reason:") || !strings.Contains(out, "CLI-9와 중복") {
		t.Errorf("issue view:\n%s", out)
	}
	if code := h.run("issue", "get", "CLI-2"); code != 0 || !regexp.MustCompile(`status_reason:\s+-\n`).MatchString(h.stdout.String()) {
		t.Errorf("no reason: code=%d\n%s", code, h.stdout)
	}
	if code := h.run("issue", "update", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "--reason") {
		t.Errorf("update help: code=%d out=%s", code, h.stdout)
	}
}
