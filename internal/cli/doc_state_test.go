package cli

import (
	"strings"
	"testing"
)

const stateDocPath = "/v1/projects/AGB/documents/" + docDecisionName

func TestDocArchiveAndUnarchive(t *testing.T) {
	archived := strings.Replace(serverDoc("AGB", docDecisionName, "archive", "n"), `"state":"current","archived":false`, `"state":"archived","archived":true`, 1)
	srv := newFakeServer(t)
	srv.on("POST", stateDocPath+"/archive", canned{body: archived})
	srv.on("DELETE", stateDocPath+"/archive", canned{body: serverDoc("AGB", docDecisionName, "history", "n")})
	h := newHarness(t, srv, true)

	if code := h.run("doc", "archive", "AGB", docDecisionName); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if r := srv.last(); r.Method != "POST" || r.Path != stateDocPath+"/archive" || r.Body != "" {
		t.Errorf("request = %s %s %q", r.Method, r.Path, r.Body)
	}
	for _, want := range []string{"state:", "archived", "archived:", "yes"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("view lacks %q:\n%s", want, h.stdout)
		}
	}
	if code := h.run("doc", "unarchive", "AGB", docDecisionName); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if r := srv.last(); r.Method != "DELETE" || r.Path != stateDocPath+"/archive" {
		t.Errorf("request = %s %s", r.Method, r.Path)
	}
	if !strings.Contains(h.stdout.String(), "current") {
		t.Errorf("view:\n%s", h.stdout)
	}

	// JSON is the server body as it is.
	hj := newHarness(t, srv, false)
	if code := hj.run("doc", "archive", "AGB", docDecisionName); code != 0 || hj.stdout.String() != archived+"\n" {
		t.Errorf("json: code=%d out=%q", code, hj.stdout.String())
	}
}

func TestDocLinkAndUnlinkSendOneRelationPerTarget(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("POST", stateDocPath+"/relations", canned{body: serverDoc("AGB", docDecisionName, "history", "n")})
	srv.on("DELETE", stateDocPath+"/relations", canned{body: serverDoc("AGB", docDecisionName, "history", "n")})
	h := newHarness(t, srv, false)

	if code := h.run("doc", "link", "AGB", docDecisionName, "--supersedes", "a.md", "--related", "b.md", "--related", "c.md", "--issue", "AGB-7"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var sent []string
	for _, r := range srv.reqs {
		if r.Method != "POST" || r.Path != stateDocPath+"/relations" {
			t.Errorf("request = %s %s", r.Method, r.Path)
		}
		b := decodeBody(t, r)
		sent = append(sent, b["kind"].(string)+" "+b["target"].(string))
	}
	if want := []string{"supersedes a.md", "related b.md", "related c.md", "about AGB-7"}; strings.Join(sent, "|") != strings.Join(want, "|") {
		t.Errorf("relations = %v, want %v", sent, want)
	}

	srv.reqs = nil
	if code := h.run("doc", "unlink", "AGB", docDecisionName, "--issue", "AGB-7"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if r := srv.last(); r.Method != "DELETE" || r.Path != stateDocPath+"/relations" || decodeBody(t, r)["kind"] != "about" || decodeBody(t, r)["target"] != "AGB-7" {
		t.Errorf("unlink request = %s %s %s", r.Method, r.Path, r.Body)
	}
	if h.stdout.String() != serverDoc("AGB", docDecisionName, "history", "n")+"\n" {
		t.Errorf("stdout is the document after the last change: %q", h.stdout.String())
	}
}

func TestDocLinkShowsWarningsAndTheRefusalOfATarget(t *testing.T) {
	warned := strings.Replace(serverDoc("AGB", docDecisionName, "history", "n"), `"updated_by":"claude"}`, `"updated_by":"claude","warnings":[{"field":"related","reason":"unresolved","message":"대상이 아직 없습니다."}]}`, 1)
	srv := newFakeServer(t)
	srv.on("POST", stateDocPath+"/relations", canned{body: warned})
	h := newHarness(t, srv, false)
	if code := h.run("doc", "link", "AGB", docDecisionName, "--related", "later.md"); code != 0 || !strings.Contains(h.stderr.String(), "경고: related: 대상이 아직 없습니다.") {
		t.Errorf("warnings: code=%d stderr=%s", code, h.stderr)
	}

	srv.on("POST", stateDocPath+"/relations", canned{status: 422, body: `{"error":{"code":"validation_failed","message":"x","details":[{"field":"target","reason":"unresolved","message":"문서가 없습니다."}]}}`})
	ht := newHarness(t, srv, true)
	if code := ht.run("doc", "link", "AGB", docDecisionName, "--supersedes", "none.md"); code != 1 || !strings.Contains(ht.stderr.String(), "unresolved") {
		t.Errorf("refused: code=%d stderr=%s", code, ht.stderr)
	}
}

func TestDocLinkUsageErrors(t *testing.T) {
	srv := newFakeServer(t)
	h := newHarness(t, srv, false)
	for label, args := range map[string][]string{
		"no target":             {"doc", "link", "AGB", docDecisionName},
		"no name":               {"doc", "link", "AGB", "--related", "a.md"},
		"a stray argument":      {"doc", "link", "AGB", docDecisionName, "extra", "--related", "a.md"},
		"unlink without target": {"doc", "unlink", "AGB", docDecisionName},
		"archive without name":  {"doc", "archive", "AGB"},
		"unarchive with extra":  {"doc", "unarchive", "AGB", docDecisionName, "x"},
		"an unknown option":     {"doc", "link", "AGB", docDecisionName, "--about", "AGB-1"},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%s: code=%d stderr=%s", label, code, h.stderr)
		}
	}
	if len(srv.reqs) != 0 {
		t.Errorf("usage errors must not reach the server: %v", srv.paths())
	}
}

const docActivityJSON = `{"items":[` +
	`{"id":8,"project":"AGB","document":"` + docDecisionName + `","actor":"claude","session_id":"s","client_host":"pc","at":"2026-11-08T01:00:00.000Z","changes":{"archived":{"from":false,"to":true}}},` +
	`{"id":7,"project":"AGB","document":"` + docDecisionName + `","actor":"owner","session_id":"s","client_host":"pc","at":"2026-11-07T01:00:00.000Z","changes":{"related":{"from":[],"to":["b.md"]},"supersedes":{"from":[],"to":["a.md"]}}}` +
	`],"total":2,"limit":50,"offset":0}`

func TestDocActivity(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", stateDocPath+"/activity", canned{body: docActivityJSON})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0
	if code := h.run("doc", "activity", "AGB", docDecisionName, "--limit", "5"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if r := srv.last(); r.Method != "GET" || r.Path != stateDocPath+"/activity" || r.Query.Get("limit") != "5" {
		t.Errorf("request = %s %s?%s", r.Method, r.Path, r.Query.Encode())
	}
	lines := strings.Split(strings.TrimRight(h.stdout.String(), "\n"), "\n")
	if got := strings.Fields(lines[0]); !equalStrings(got, []string{"AT", "ACTOR", "CHANGES"}) {
		t.Errorf("header = %v", got)
	}
	if !strings.Contains(lines[1], "claude") || !strings.Contains(lines[1], "archived: false → true") ||
		!strings.Contains(lines[2], "related: [] → [\"b.md\"]; supersedes: [] → [\"a.md\"]") {
		t.Errorf("rows:\n%s", h.stdout)
	}
	hj := newHarness(t, srv, false)
	if code := hj.run("doc", "activity", "AGB", docDecisionName); code != 0 || hj.stdout.String() != docActivityJSON+"\n" {
		t.Errorf("json: code=%d out=%q", code, hj.stdout.String())
	}
	srv.on("GET", stateDocPath+"/activity", canned{body: `{"items":[],"total":0,"limit":50,"offset":0}`})
	if code := h.run("doc", "activity", "AGB", docDecisionName); code != 0 || !strings.Contains(h.stdout.String(), "활동 기록이 없습니다.") {
		t.Errorf("empty: code=%d out=%q", code, h.stdout.String())
	}
	if code := h.run("doc", "activity", "AGB"); code != 2 {
		t.Errorf("no name: code=%d", code)
	}
}

func TestDocHelpListsTheStateCommands(t *testing.T) {
	h := newHarness(t, nil, true)
	if code := h.run("doc", "--help"); code != 0 {
		t.Fatalf("code=%d", code)
	}
	for _, name := range []string{"archive", "unarchive", "link", "unlink", "activity"} {
		if !strings.Contains(h.stdout.String(), name) {
			t.Errorf("doc help lacks %s:\n%s", name, h.stdout)
		}
	}
}
