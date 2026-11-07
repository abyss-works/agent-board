package cli

import (
	"strings"
	"testing"
)

const graphJSON = `{"nodes":[` +
	`{"id":"issue:AGB-7","type":"issue","project":"AGB","key":"AGB-7","title":"잠금 조사","kind":"bug","status":"done","refs":["commit:abc1234"]},` +
	`{"id":"doc:AGB/2026-10-21-0900-incident-lock.md","type":"document","project":"AGB","key":"2026-10-21-0900-incident-lock.md","title":"잠금 사고","kind":"incident","status":null,"refs":[]},` +
	`{"id":"issue:AGB-1","type":"issue","project":"AGB","key":"AGB-1","title":"상위","kind":"feature","status":"backlog","refs":[]}],` +
	`"edges":[{"from":"doc:AGB/2026-10-21-0900-incident-lock.md","to":"issue:AGB-7","type":"about"},{"from":"issue:AGB-7","to":"issue:AGB-1","type":"parent"}]}`

func TestGraphCommand(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/graph", canned{body: graphJSON})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0
	if code := h.run("graph", "AGB-7", "--depth", "2"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if q := srv.last().Query; q.Get("node") != "issue:AGB-7" || q.Get("depth") != "2" {
		t.Errorf("query = %s", q.Encode())
	}
	out := h.stdout.String()
	for _, want := range []string{"issue:AGB-7  bug  잠금 조사", "refs: commit:abc1234", "←", "about", "doc:AGB/2026-10-21-0900-incident-lock.md", "→", "parent", "issue:AGB-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if code := h.run("graph", "AGB/archive/2026-06-a/b.md"); code != 0 || srv.last().Query.Get("node") != "doc:AGB/archive/2026-06-a/b.md" {
		t.Errorf("document node: code %d query %s", code, srv.last().Query.Encode())
	}
	if code := h.run("graph", "agb-7"); code != 2 {
		t.Errorf("bad node: code %d", code)
	}
}

const graphFoldedJSON = `{"nodes":[` +
	`{"id":"doc:AGB/2026-11-06-1537-decision-graph.md","type":"document","project":"AGB","key":"2026-11-06-1537-decision-graph.md","title":"그래프","kind":"decision","status":null,"state":"superseded","refs":[],"folded":{"superseded":1,"archived":2}},` +
	`{"id":"issue:AGB-75","type":"issue","project":"AGB","key":"AGB-75","title":"그래프 이슈","kind":"design","status":"doing","state":null,"refs":[],"folded":null},` +
	`{"id":"doc:AGB/2026-11-07-1000-decision-graph-v2.md","type":"document","project":"AGB","key":"2026-11-07-1000-decision-graph-v2.md","title":"그래프 2판","kind":"decision","status":null,"state":"current","refs":[],"folded":{"superseded":0,"archived":1}}],` +
	`"edges":[{"from":"doc:AGB/2026-11-06-1537-decision-graph.md","to":"issue:AGB-75","type":"about"},{"from":"doc:AGB/2026-11-07-1000-decision-graph-v2.md","to":"doc:AGB/2026-11-06-1537-decision-graph.md","type":"supersedes"}]}`

func TestGraphStateAndFoldedNeighbours(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/graph", canned{body: graphFoldedJSON})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0
	if code := h.run("graph", "AGB/2026-11-06-1537-decision-graph.md"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if srv.last().Query.Has("state") {
		t.Errorf("the default state is the server's: %s", srv.last().Query.Encode())
	}
	out := h.stdout.String()
	lines := strings.Split(out, "\n")
	headerOK := false
	for _, l := range lines {
		if f := strings.Fields(l); len(f) >= 5 && f[0] == "DIR" && f[3] == "KIND" && f[4] == "STATE" {
			headerOK = true
		}
	}
	if !headerOK {
		t.Errorf("table header lacks STATE:\n%s", out)
	}
	for _, want := range []string{"doc:AGB/2026-11-06-1537-decision-graph.md  decision  그래프", "state: superseded",
		"접힌 이웃", "doc:AGB/2026-11-06-1537-decision-graph.md  superseded 1, archived 2", "doc:AGB/2026-11-07-1000-decision-graph-v2.md  archived 1", "--state all",
		"doing", "current"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if code := h.run("graph", "AGB-75", "--state", "all"); code != 0 || srv.last().Query.Get("state") != "all" {
		t.Errorf("--state all: code=%d query=%s", code, srv.last().Query.Encode())
	}
}
