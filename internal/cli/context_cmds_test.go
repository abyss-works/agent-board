package cli

import (
	"strings"
	"testing"
)

func TestContextCommand(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("POST", "/v1/context/query", canned{body: `{"cutoff_applied":true,"score_gap_1_2":0.4,"fallback_used":true,"fallback_mode":"rrf","items":[` +
		`{"node_type":"record","project":"DEMO","key_or_name":"decision-a","kind":"decision","title":"결정 A","score":0.91,"summary":"요약문","content":"본문 전체",` +
		`"superseded_by":{"node_type":"record","key_or_name":"decision-b"},"neighbors":[{"direction":"inbound","kind":"about","node_type":"issue","key_or_name":"DEMO-1","title":"이슈"}]}]}`})
	h := newHarness(t, srv, true)
	if code := h.run("context", "락", "경합", "-p", "demo", "--max-results", "2", "--cutoff", "0", "--type", "spec,record", "--no-global", "--include-superseded"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	body := decodeBody(t, srv.last())
	if body["query"] != "락 경합" || body["project"] != "demo" || body["max_results"] != float64(2) || body["include_global"] != false || body["include_superseded"] != true {
		t.Errorf("body = %v", body)
	}
	if v, ok := body["confidence_cutoff"]; !ok || v != float64(0) {
		t.Errorf("an explicit zero cutoff must be sent: %v", body)
	}
	for _, want := range []string{"결정 A", "score 0.910", "대체됨 → record decision-b", "← about issue DEMO-1", "본문 전체", "컷오프", "rrf"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, h.stdout.String())
		}
	}
	if code := h.run("context"); code != 2 {
		t.Errorf("missing query: code %d", code)
	}
}

func TestReadyEdgeSpecEpicCommands(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/projects/DEMO/ready", canned{body: `{"items":[{"key":"DEMO-3","title":"할 일","type":"feature","status":"backlog","priority":"P1"}],"total":1,"offset":0}`})
	srv.on("GET", "/v1/edges/candidates", canned{body: `{"items":[{"id":7,"kind":"supersedes","source_type":"spec","source_key_or_name":"a","target_key_or_name":"b","confidence":0.8,"status":"candidate"}],"total":1,"offset":0}`})
	srv.on("POST", "/v1/edges/7/confirm", canned{body: `{"id":7,"kind":"supersedes","status":"confirmed"}`})
	srv.on("POST", "/v1/edges/8/reject", canned{body: `{"id":8,"kind":"related","status":"rejected"}`})
	srv.on("PUT", "/v1/projects/DEMO/specs/api", canned{body: `{"ingest_status":"done"}`})
	srv.on("POST", "/v1/projects/DEMO/records", canned{body: `{"ingest_status":"done"}`})
	srv.on("GET", "/v1/projects/DEMO/epics", canned{body: `{"items":[{"key":"E-1","title":"에픽","status":"open"}],"total":1,"offset":0}`})
	srv.on("PATCH", "/v1/epics/E-1", canned{body: `{"key":"E-1","status":"closed"}`})
	srv.on("PATCH", "/v1/milestones/4", canned{body: `{"id":4,"name":"m","status":"active"}`})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0

	run := func(want int, args ...string) {
		t.Helper()
		if code := h.run(args...); code != want {
			t.Fatalf("%v: code=%d stderr=%s", args, code, h.stderr)
		}
	}
	run(0, "ready", "DEMO", "--epic", "E-1", "--include-claimed", "--limit", "5")
	if q := srv.last().Query; q.Get("epic") != "E-1" || q.Get("include_claimed") != "true" || q.Get("limit") != "5" || !strings.Contains(h.stdout.String(), "DEMO-3") {
		t.Errorf("ready: %s / %s", q.Encode(), h.stdout.String())
	}
	run(0, "edge", "candidates", "-p", "DEMO", "--kind", "supersedes")
	if !strings.Contains(h.stdout.String(), "spec:a") || srv.last().Query.Get("kind") != "supersedes" {
		t.Errorf("candidates: %s", h.stdout.String())
	}
	run(0, "edge", "confirm", "7")
	if !strings.Contains(h.stdout.String(), "confirmed") {
		t.Errorf("confirm: %s", h.stdout.String())
	}
	run(0, "edge", "reject", "8")
	run(2, "edge", "confirm", "x")

	h.app.Stdin = strings.NewReader("# 본문")
	run(0, "spec", "put", "DEMO", "api", "--body-file", "-", "--title", "API", "--tags", "a,b", "--wait")
	if r := srv.last(); r.Query.Get("wait") != "true" || decodeBody(t, r)["content"] != "# 본문" {
		t.Errorf("spec put: %s %s", r.Query.Encode(), r.Body)
	}
	run(2, "spec", "put", "DEMO", "api")
	run(0, "record", "add", "DEMO", "note-1", "--kind", "decision", "--body", "내용")
	if r := srv.last(); r.Query.Has("wait") || decodeBody(t, r)["name"] != "note-1" {
		t.Errorf("record add: %s %s", r.Query.Encode(), r.Body)
	}
	run(2, "record", "add", "DEMO", "note-2", "--body", "x")

	run(0, "epic", "list", "-p", "DEMO")
	run(0, "epic", "close", "E-1")
	if decodeBody(t, srv.last())["status"] != "closed" {
		t.Errorf("epic close: %s", srv.last().Body)
	}
	run(0, "milestone", "start", "4")
	if decodeBody(t, srv.last())["status"] != "active" {
		t.Errorf("milestone start: %s", srv.last().Body)
	}
}
