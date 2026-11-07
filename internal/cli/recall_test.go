package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

const recallSearchJSON = `{"items":[` +
	`{"type":"comment","project":"AGB","key":"AGB-7","comment_id":42,"title":"잠금 원인","kind":"finding","status":"done","state":null,"supersedes":null,"superseded_by":null,"at":"2026-11-06T05:00:00.000Z","snippet":"[잠금] 원인"}` +
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
	for _, want := range []string{"1. [comment] AGB AGB-7#42  finding (done)  잠금 원인", "[잠금] 원인", "← about doc:AGB/2026-10-21-0900-incident-lock.md incident  잠금 사고", "→ parent issue:AGB-1 feature (backlog)  상위", "3건 중 1건"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	// recall takes the scope of the server: every issue that is not deleted, and the current documents.
	for _, r := range srv.reqs {
		if r.Path == "/v1/search" && (r.Query.Has("status") || r.Query.Has("state")) {
			t.Errorf("recall sent a scope by itself: %s", r.Query.Encode())
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

func TestRecallWidensTheScopeAndShowsDocumentStates(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/search", canned{body: `{"items":[` +
		`{"type":"document","project":"AGT","key":"2026-10-17-1430-decision-scope.md","comment_id":null,"title":"범위","kind":"decision","status":null,"state":"superseded","supersedes":["2026-10-10-0900-decision-scope-draft.md"],"superseded_by":["2026-11-01-1000-decision-scope-v2.md"],"at":"2026-10-17 14:30","snippet":"[범위]"}` +
		`],"total":1,"limit":5,"offset":0}`})
	srv.on("GET", "/v1/graph", canned{body: `{"nodes":[` +
		`{"id":"doc:AGT/2026-10-17-1430-decision-scope.md","type":"document","project":"AGT","key":"2026-10-17-1430-decision-scope.md","title":"범위","kind":"decision","status":null,"state":"superseded","refs":[],"folded":null},` +
		`{"id":"doc:AGT/2026-11-01-1000-decision-scope-v2.md","type":"document","project":"AGT","key":"2026-11-01-1000-decision-scope-v2.md","title":"범위 2판","kind":"decision","status":null,"state":"current","refs":[],"folded":null}],` +
		`"edges":[{"from":"doc:AGT/2026-11-01-1000-decision-scope-v2.md","to":"doc:AGT/2026-10-17-1430-decision-scope.md","type":"supersedes"}]}`})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0
	if code := h.run("recall", "범위", "--status", "all", "--state", "all"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if q := srv.reqs[0].Query; q.Get("status") != "all" || q.Get("state") != "all" {
		t.Errorf("search query = %s", q.Encode())
	}
	out := h.stdout.String()
	for _, want := range []string{"1. [document] AGT 2026-10-17-1430-decision-scope.md  decision (superseded)  범위",
		"supersedes: 2026-10-10-0900-decision-scope-draft.md", "superseded_by: 2026-11-01-1000-decision-scope-v2.md",
		"← supersedes doc:AGT/2026-11-01-1000-decision-scope-v2.md decision (current)  범위 2판"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
