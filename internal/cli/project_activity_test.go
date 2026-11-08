package cli

import (
	"strings"
	"testing"
)

const projectActivityJSON = `{"items":[` +
	`{"id":3,"project":"AGB","resource":"milestone","resource_id":2,"kind":"updated","actor":"claude","session_id":"s","client_host":"pc","at":"2026-11-08T01:00:00.000Z","changes":{"status":{"from":"open","to":"closed"}}},` +
	`{"id":1,"project":"AGB","resource":"project","resource_id":1,"kind":"created","actor":"owner","session_id":"s","client_host":"pc","at":"2026-11-07T01:00:00.000Z","changes":{"name":{"from":null,"to":"보드"}}}` +
	`],"total":2,"limit":50,"offset":0}`

func TestProjectActivity(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/projects/AGB/activity", canned{body: projectActivityJSON})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0
	if code := h.run("project", "activity", "AGB", "--resource", "milestone", "--id", "2", "--limit", "5", "--offset", "1"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	r := srv.last()
	if r.Method != "GET" || r.Path != "/v1/projects/AGB/activity" || r.Query.Get("resource") != "milestone" || r.Query.Get("id") != "2" || r.Query.Get("limit") != "5" || r.Query.Get("offset") != "1" {
		t.Errorf("request = %s %s?%s", r.Method, r.Path, r.Query.Encode())
	}
	lines := strings.Split(strings.TrimRight(h.stdout.String(), "\n"), "\n")
	if got := strings.Fields(lines[0]); !equalStrings(got, []string{"AT", "ACTOR", "KIND", "TARGET", "CHANGES"}) {
		t.Errorf("header = %v", got)
	}
	if !strings.Contains(lines[1], "claude") || !strings.Contains(lines[1], "milestone#2") || !strings.Contains(lines[1], "status: open → closed") ||
		!strings.Contains(lines[2], "project#1") || !strings.Contains(lines[2], "created") || !strings.Contains(lines[2], "name: - → 보드") {
		t.Errorf("rows:\n%s", h.stdout)
	}

	hj := newHarness(t, srv, false)
	if code := hj.run("project", "activity", "AGB"); code != 0 || hj.stdout.String() != projectActivityJSON+"\n" {
		t.Errorf("json: code=%d out=%q", code, hj.stdout.String())
	}
	if r := srv.last(); r.Query.Get("resource") != "" || r.Query.Get("id") != "" {
		t.Errorf("unset filters were sent: %s", r.Query.Encode())
	}
	srv.on("GET", "/v1/projects/AGB/activity", canned{body: `{"items":[],"total":0,"limit":50,"offset":0}`})
	if code := h.run("project", "activity", "AGB"); code != 0 || !strings.Contains(h.stdout.String(), "활동 기록이 없습니다.") {
		t.Errorf("empty: code=%d out=%q", code, h.stdout.String())
	}
	if code := h.run("project", "activity"); code != 2 {
		t.Errorf("no key: code=%d", code)
	}
}

func TestProjectHelpListsActivity(t *testing.T) {
	h := newHarness(t, nil, true)
	if code := h.run("project", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "activity") {
		t.Errorf("code=%d out=%s", code, h.stdout)
	}
}
