package cli

import (
	"strings"
	"testing"
)

const findJSON = `{"items":[` +
	`{"type":"comment","project":"AGB","key":"AGB-7","comment_id":42,"title":"잠금 원인","kind":"finding","status":"doing","state":null,"supersedes":null,"superseded_by":null,"at":"2026-11-06T05:00:00.000Z","snippet":"[잠금] 원인은\n긴 트랜잭션"},` +
	`{"type":"document","project":"XYZ","key":"2026-10-17-1430-decision-scope.md","comment_id":null,"title":null,"kind":"decision","status":null,"state":"superseded","supersedes":["2026-10-10-0900-decision-scope-draft.md"],"superseded_by":["2026-11-01-1000-decision-scope-v2.md"],"at":"2026-10-17 14:30","snippet":"[잠금] 범위"}` +
	`],"total":2,"limit":20,"offset":0}`

func TestFind(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/search", canned{body: findJSON})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0

	if code := h.run("find", "잠금", "원인", "-p", "AGB,XYZ", "--type", "comment,document", "--comment-kind", "finding", "--limit", "5"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	q := srv.last().Query
	if srv.last().Path != "/v1/search" || q.Get("q") != "잠금 원인" || q.Get("project") != "AGB,XYZ" || q.Get("type") != "comment,document" || q.Get("comment_kind") != "finding" || q.Get("limit") != "5" {
		t.Errorf("request = %s?%s", srv.last().Path, q.Encode())
	}
	// find looks for what is open now: open issues, and the documents the server calls current.
	if q.Get("status") != "open" || q.Has("state") {
		t.Errorf("default scope = status %q, state %q (present %v)", q.Get("status"), q.Get("state"), q.Has("state"))
	}
	out := h.stdout.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if got := strings.Fields(lines[0]); !equalStrings(got, []string{"TYPE", "PROJECT", "KEY", "KIND", "STATE", "AT", "TITLE", "SNIPPET"}) {
		t.Errorf("header = %v", got)
	}
	if got := strings.Fields(lines[1]); !equalStrings(got[:5], []string{"comment", "AGB", "AGB-7#42", "finding", "doing"}) || !strings.Contains(lines[1], "[잠금] 원인은 긴 트랜잭션") {
		t.Errorf("comment row = %q", lines[1])
	}
	if got := strings.Fields(lines[2]); !equalStrings(got[:7], []string{"document", "XYZ", "2026-10-17-1430-decision-scope.md", "decision", "superseded", "2026-10-17", "14:30"}) {
		t.Errorf("document row = %q", lines[2])
	}
	for _, want := range []string{
		"XYZ/2026-10-17-1430-decision-scope.md",
		"supersedes: 2026-10-10-0900-decision-scope-draft.md",
		"superseded_by: 2026-11-01-1000-decision-scope-v2.md",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if code := h.run("find"); code != 2 {
		t.Errorf("no query: code %d", code)
	}
}

func TestFindWidensTheScope(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/search", canned{body: findJSON})
	h := newHarness(t, srv, true)
	if code := h.run("find", "잠금", "--status", "all", "--state", "current,archived"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if q := srv.last().Query; q.Get("status") != "all" || q.Get("state") != "current,archived" {
		t.Errorf("query = %s", q.Encode())
	}
	if code := h.run("find", "잠금", "--status", "closed"); code != 0 || srv.last().Query.Get("status") != "closed" {
		t.Errorf("closed: code=%d query=%s", code, srv.last().Query.Encode())
	}
}

func TestFindJSONIsTheServerBody(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/search", canned{body: findJSON})
	h := newHarness(t, srv, false)
	if code := h.run("find", "잠금"); code != 0 || h.stdout.String() != findJSON+"\n" {
		t.Errorf("json: code=%d out=%q", code, h.stdout.String())
	}
}
