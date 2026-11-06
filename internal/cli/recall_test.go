package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

const recallSearchJSON = `{"items":[` +
	`{"type":"comment","project":"AGB","key":"AGB-7","comment_id":42,"title":"잠금 원인","kind":"finding","at":"2026-11-06T05:00:00.000Z","snippet":"[잠금] 원인"}` +
	`],"total":3,"limit":1,"offset":0}`

func TestRecall(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/search", canned{body: recallSearchJSON})
	srv.on("GET", "/v1/graph", canned{body: graphJSON})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0

	if code := h.run("recall", "잠금은", "왜", "났지", "--limit", "1", "-p", "AGB"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var search, graph bool
	for _, r := range srv.reqs {
		switch r.Path {
		case "/v1/search":
			search = r.Query.Get("q") == "잠금은 왜 났지" && r.Query.Get("limit") == "1" && r.Query.Get("project") == "AGB"
		case "/v1/graph":
			graph = r.Query.Get("node") == "issue:AGB-7" && r.Query.Get("depth") == "1"
		}
	}
	if !search || !graph {
		t.Errorf("requests = %+v", srv.reqs)
	}
	out := h.stdout.String()
	for _, want := range []string{"1. [comment] AGB AGB-7#42  finding  잠금 원인", "[잠금] 원인", "← about doc:AGB/2026-10-21-0900-incident-lock.md incident  잠금 사고", "→ parent issue:AGB-1 feature (backlog)  상위", "3건 중 1건"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	h = newHarness(t, srv, false)
	if code := h.run("recall", "잠금"); code != 0 {
		t.Fatalf("json: code=%d stderr=%s", code, h.stderr)
	}
	var r recallResult
	if err := json.Unmarshal(h.stdout.Bytes(), &r); err != nil || len(r.Items) != 1 || len(r.Items[0].Links) != 2 || r.Items[0].Links[0].Direction != "in" {
		t.Errorf("json = %s, %v", h.stdout, err)
	}
	if code := h.run("recall"); code != 2 {
		t.Errorf("no question: code %d", code)
	}
	if code := h.run("recall", "x", "--limit", "21"); code != 2 {
		t.Errorf("limit 21: code %d", code)
	}
}
