package cli

import (
	"strings"
	"testing"
)

const findJSON = `{"items":[` +
	`{"type":"comment","project":"AGB","key":"AGB-7","comment_id":42,"title":"잠금 원인","kind":"finding","at":"2026-11-06T05:00:00.000Z","snippet":"[잠금] 원인은\n긴 트랜잭션"},` +
	`{"type":"document","project":"AGT","key":"decisions/2026-10-17-14-30-scope.md","comment_id":null,"title":null,"kind":"decision","at":"2026-10-17 14:30","snippet":"[잠금] 범위"}` +
	`],"total":2,"limit":20,"offset":0}`

func TestFind(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/search", canned{body: findJSON})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0

	if code := h.run("find", "잠금", "원인", "-p", "AGB,AGT", "--type", "comment,document", "--comment-kind", "finding", "--limit", "5"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	q := srv.last().Query
	if srv.last().Path != "/v1/search" || q.Get("q") != "잠금 원인" || q.Get("project") != "AGB,AGT" || q.Get("type") != "comment,document" || q.Get("comment_kind") != "finding" || q.Get("limit") != "5" {
		t.Errorf("request = %s?%s", srv.last().Path, q.Encode())
	}
	lines := strings.Split(strings.TrimRight(h.stdout.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("table:\n%s", h.stdout)
	}
	if got := strings.Fields(lines[0]); !equalStrings(got, []string{"TYPE", "PROJECT", "KEY", "KIND", "AT", "TITLE", "SNIPPET"}) {
		t.Errorf("header = %v", got)
	}
	if got := strings.Fields(lines[1]); !equalStrings(got[:4], []string{"comment", "AGB", "AGB-7#42", "finding"}) || !strings.Contains(lines[1], "[잠금] 원인은 긴 트랜잭션") {
		t.Errorf("comment row = %q", lines[1])
	}
	if got := strings.Fields(lines[2]); !equalStrings(got[:6], []string{"document", "AGT", "decisions/2026-10-17-14-30-scope.md", "decision", "2026-10-17", "14:30"}) {
		t.Errorf("document row = %q", lines[2])
	}
	if code := h.run("find"); code != 2 {
		t.Errorf("no query: code %d", code)
	}
}
