package cli

import (
	"strings"
	"testing"
)

const (
	verifiedIssueJSON = `{"key":"CLI-1","project":"CLI","number":1,"title":"첫 이슈","body":"","type":"feature","status":"doing","priority":"P1","labels":[],"parent":null,"depends_on":[],"refs":[],"recheck_condition":null,"status_reason":null,"verification_state":"verified","verification_note":"화면 레이아웃 확인","verified_at":"2026-10-17T08:00:00.000Z","verified_by":"owner","version":4,"created_at":"2026-10-17T05:46:06.000Z","updated_at":"2026-10-17T08:00:00.000Z","closed_at":null,"status_changed_at":"2026-10-17T07:10:21.000Z","status_changed_by":"claude","created_by":"claude","updated_by":"owner"}`
	pendingIssueJSON  = `{"key":"CLI-2","project":"CLI","number":2,"title":"둘째 이슈","body":"","type":"bug","status":"done","priority":"P2","labels":[],"parent":null,"depends_on":[],"refs":[],"recheck_condition":null,"status_reason":null,"verification_state":"pending","verification_note":"모바일 확인","verified_at":null,"verified_by":null,"version":1,"created_at":"2026-10-17T05:46:06.000Z","updated_at":"2026-10-17T05:46:06.000Z","closed_at":null,"status_changed_at":"2026-10-17T05:46:06.000Z","status_changed_by":"claude","created_by":"claude","updated_by":"claude"}`
	noneIssueJSON     = `{"key":"CLI-3","project":"CLI","number":3,"title":"셋째 이슈","body":"","type":"feature","status":"backlog","priority":"P2","labels":[],"parent":null,"depends_on":[],"refs":[],"recheck_condition":null,"status_reason":null,"verification_state":"none","verification_note":null,"verified_at":null,"verified_by":null,"version":1,"created_at":"2026-10-17T05:46:06.000Z","updated_at":"2026-10-17T05:46:06.000Z","closed_at":null,"status_changed_at":"2026-10-17T05:46:06.000Z","status_changed_by":"claude","created_by":"claude","updated_by":"claude"}`
)

func TestIssueVerifyPostsEveryKey(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("POST", "/v1/issues/CLI-1/verify", canned{body: verifiedIssueJSON, etag: `"4"`})
	srv.on("POST", "/v1/issues/CLI-3/verify", canned{body: strings.Replace(verifiedIssueJSON, "CLI-1", "CLI-3", 1), etag: `"2"`})
	h := newHarness(t, srv, false)

	if code := h.run("issue", "verify", "CLI-1", "--if-match", "3"); code != 0 {
		t.Fatalf("one key: code=%d stderr=%s", code, h.stderr)
	}
	if h.stdout.String() != verifiedIssueJSON+"\n" {
		t.Errorf("single key stdout: %q", h.stdout.String())
	}
	if got := srv.last().Header.Get("If-Match"); got != `"3"` {
		t.Errorf("If-Match = %q, want \"3\"", got)
	}

	if code := h.run("issue", "verify", "CLI-1", "CLI-3"); code != 0 {
		t.Fatalf("two keys: code=%d stderr=%s", code, h.stderr)
	}
	lines := strings.Split(strings.TrimSpace(h.stdout.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"CLI-1"`) || !strings.Contains(lines[1], `"CLI-3"`) {
		t.Errorf("several keys output:\n%s", h.stdout.String())
	}
	if got := srv.reqs[len(srv.reqs)-2:]; got[0].Method != "POST" || got[0].Path != "/v1/issues/CLI-1/verify" || got[1].Path != "/v1/issues/CLI-3/verify" {
		t.Errorf("requests = %+v", got)
	}

	// The first failure stops the run and keeps what was already done.
	before := len(srv.reqs)
	if code := h.run("issue", "verify", "CLI-1", "CLI-9", "CLI-3"); code != 1 {
		t.Errorf("missing key: code=%d", code)
	}
	if len(srv.reqs)-before != 2 || !strings.Contains(h.stdout.String(), `"CLI-1"`) || !strings.Contains(h.stderr.String(), "not_found") {
		t.Errorf("stop at first failure: %d requests\nstdout=%s\nstderr=%s", len(srv.reqs)-before, h.stdout, h.stderr)
	}

	if code := h.run("issue", "verify"); code != 2 {
		t.Errorf("no key: code=%d", code)
	}
}

func TestIssueUnverifyDeletesEveryKey(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("DELETE", "/v1/issues/CLI-2/verify", canned{body: pendingIssueJSON, etag: `"2"`})
	srv.on("DELETE", "/v1/issues/CLI-3/verify", canned{body: strings.Replace(pendingIssueJSON, "CLI-2", "CLI-3", 1), etag: `"2"`})
	h := newHarness(t, srv, false)

	if code := h.run("issue", "unverify", "CLI-2", "--if-match", "1"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Method != "DELETE" || req.Path != "/v1/issues/CLI-2/verify" || req.Header.Get("If-Match") != `"1"` || h.stdout.String() != pendingIssueJSON+"\n" {
		t.Errorf("request = %+v stdout = %q", req, h.stdout.String())
	}
	if code := h.run("issue", "unverify", "CLI-2", "CLI-3"); code != 0 {
		t.Fatalf("two keys: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("issue", "unverify"); code != 2 {
		t.Errorf("no key: code=%d", code)
	}
}

func TestIssueCreateAndUpdateVerificationFlags(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("POST", "/v1/issues", canned{status: 201, body: pendingIssueJSON})
	srv.on("PATCH", "/v1/issues/CLI-2", canned{body: pendingIssueJSON})
	h := newHarness(t, srv, false)

	// --verify-note without --verification auto-sets verification_state=pending.
	if code := h.run("issue", "create", "-p", "CLI", "-t", "화면 확인", "--verify-note", "모바일 확인"); code != 0 {
		t.Fatalf("create with verify-note: code=%d stderr=%s", code, h.stderr)
	}
	body := srv.last().Body
	if !strings.Contains(body, `"verification_state":"pending"`) || !strings.Contains(body, `"verification_note":"모바일 확인"`) {
		t.Errorf("create body = %s", body)
	}

	// Explicit --verification none with --verify-note none sends verification_state=none without note.
	if code := h.run("issue", "create", "-p", "CLI", "-t", "내부 정리", "--verification", "none", "--verify-note", "none"); code != 0 {
		t.Fatalf("create with none: code=%d stderr=%s", code, h.stderr)
	}
	body = srv.last().Body
	if !strings.Contains(body, `"verification_state":"none"`) || strings.Contains(body, `"verification_note"`) {
		t.Errorf("create none body = %s", body)
	}

	// Empty --verify-note is a usage error.
	if code := h.run("issue", "create", "-p", "CLI", "-t", "빈 메모", "--verify-note", ""); code != 2 {
		t.Errorf("create empty verify-note: code=%d", code)
	}

	// Update --verification and --verify-note.
	if code := h.run("issue", "update", "CLI-2", "--verification", "pending", "--verify-note", "다크 모드 확인"); code != 0 {
		t.Fatalf("update verification: code=%d stderr=%s", code, h.stderr)
	}
	body = srv.last().Body
	if !strings.Contains(body, `"verification_state":"pending"`) || !strings.Contains(body, `"verification_note":"다크 모드 확인"`) {
		t.Errorf("update body = %s", body)
	}

	// Update --verify-note none clears it with null.
	if code := h.run("issue", "update", "CLI-2", "--verify-note", "none"); code != 0 {
		t.Fatalf("clear verify-note: code=%d stderr=%s", code, h.stderr)
	}
	if got := strings.TrimSpace(srv.last().Body); got != `{"verification_note":null}` {
		t.Errorf("clear verify-note body = %s", got)
	}
	if code := h.run("issue", "update", "CLI-2", "--verify-note", ""); code != 2 {
		t.Errorf("update empty verify-note: code=%d", code)
	}
}

func TestIssueListVerificationFilterAndView(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues", canned{body: listJSON})
	srv.on("GET", "/v1/issues/CLI-1", canned{body: verifiedIssueJSON})
	srv.on("GET", "/v1/issues/CLI-1/activity", canned{body: `{"items":[],"total":0,"limit":50,"offset":0}`})
	srv.on("GET", "/v1/issues/CLI-2", canned{body: pendingIssueJSON})
	srv.on("GET", "/v1/issues/CLI-2/activity", canned{body: `{"items":[],"total":0,"limit":50,"offset":0}`})
	srv.on("GET", "/v1/issues/CLI-3", canned{body: noneIssueJSON})
	srv.on("GET", "/v1/issues/CLI-3/activity", canned{body: `{"items":[],"total":0,"limit":50,"offset":0}`})
	h := newHarness(t, srv, true)

	for _, v := range []string{"none", "pending", "verified", "pending,verified"} {
		if code := h.run("issue", "list", "--verification", v, "-p", "CLI"); code != 0 {
			t.Fatalf("--verification %s: code=%d stderr=%s", v, code, h.stderr)
		}
		if got := srv.last().Query.Get("verification"); got != v || srv.last().Query.Get("project") != "CLI" {
			t.Errorf("--verification %s sent %q", v, srv.last().Query.Encode())
		}
	}
	if code := h.run("issue", "list"); code != 0 || srv.last().Query.Has("verification") {
		t.Errorf("no filter must not send verification: %v", srv.last().Query)
	}
	if code := h.run("search", "설계", "--verification", "pending"); code != 0 || srv.last().Query.Get("verification") != "pending" {
		t.Errorf("search shares verification filter: code=%d query=%v", code, srv.last().Query)
	}

	if code := h.run("issue", "get", "CLI-1"); code != 0 || !strings.Contains(h.stdout.String(), "verified (2026-10-17 17:00, owner)") || !strings.Contains(h.stdout.String(), "화면 레이아웃 확인") {
		t.Errorf("verified view: code=%d\n%s", code, h.stdout)
	}
	if code := h.run("issue", "get", "CLI-2"); code != 0 || !strings.Contains(h.stdout.String(), "pending") || !strings.Contains(h.stdout.String(), "모바일 확인") {
		t.Errorf("pending view: code=%d\n%s", code, h.stdout)
	}
	if code := h.run("issue", "get", "CLI-3"); code != 0 || !strings.Contains(h.stdout.String(), "verification:") || !strings.Contains(h.stdout.String(), "none") {
		t.Errorf("none view: code=%d\n%s", code, h.stdout)
	}
	for _, args := range [][]string{{"issue", "list", "--help"}, {"search", "--help"}} {
		if code := h.run(args...); code != 0 || !strings.Contains(h.stdout.String(), "--verification") {
			t.Errorf("%v: code=%d out=%s", args, code, h.stdout)
		}
	}
	if code := h.run("issue", "verify", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "<KEY>") {
		t.Errorf("verify help: code=%d out=%s", code, h.stdout)
	}
	if code := h.run("issue", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "unverify") {
		t.Errorf("issue help: code=%d out=%s", code, h.stdout)
	}
}

func TestOverviewTableListsUnverifiedIssues(t *testing.T) {
	project := `{"key":"CLI","name":"cli 검증","status":"active","created_at":"2026-10-17T05:30:00.000Z","updated_at":"2026-10-17T05:30:00.000Z","created_by":"claude","updated_by":"claude"}`
	empty := `{"total":0,"items":[]}`
	body := `{"project":` + project + `,"generated_at":"2026-10-17T09:00:00.000Z","counts":{"total":1,"open":1,"by_status":{},"by_type":{},"by_priority":{}},` +
		`"doing":` + empty + `,"review":` + empty + `,"blocked":` + empty + `,"decisions":` + empty + `,"next":` + empty +
		`,"unverified":{"total":1,"items":[` + pendingIssueJSON + `]},"recent_activity":` + empty + `}`
	srv := newFakeServer(t)
	srv.on("GET", "/v1/projects/CLI/overview", canned{body: body})
	h := newHarness(t, srv, true)
	if code := h.run("project", "overview", "CLI"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "== unverified (1) ==") || !strings.Contains(out, "둘째 이슈") {
		t.Errorf("overview table:\n%s", out)
	}
	if strings.Index(out, "== next (0) ==") > strings.Index(out, "== unverified (1) ==") || strings.Index(out, "== unverified (1) ==") > strings.Index(out, "== recent_activity (0) ==") {
		t.Errorf("unverified sits between next and recent_activity:\n%s", out)
	}
}

func TestReleaseCheck(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/projects/CLI/milestones", canned{body: `{"items":[{"id":7,"project":"CLI","name":"v1.0","description":"","target_date":null,"status":"open","closed_at":null,"created_at":"2026-10-17T05:30:00.000Z","updated_at":"2026-10-17T05:30:00.000Z","created_by":"owner","updated_by":"owner"}]}`})

	// Zero unverified issues -> exit 0.
	srv.on("GET", "/v1/issues", canned{body: `{"items":[],"total":0,"limit":200,"offset":0}`})
	h := newHarness(t, srv, true)
	if code := h.run("release", "check", "-p", "CLI", "--milestone", "v1.0"); code != 0 {
		t.Fatalf("clean release check: code=%d stdout=%s stderr=%s", code, h.stdout, h.stderr)
	}
	q := srv.last().Query
	if q.Get("project") != "CLI" || q.Get("milestone") != "7" || q.Get("verification") != "pending" || q.Get("status") != "backlog,doing,review,done" {
		t.Errorf("release check query = %s", q.Encode())
	}

	// Pending unverified issue -> exit 1, prints blocking issue.
	srv.on("GET", "/v1/issues", canned{body: `{"items":[` + pendingIssueJSON + `],"total":1,"limit":200,"offset":0}`})
	if code := h.run("release", "check", "-p", "CLI"); code != 1 {
		t.Fatalf("blocked release check: code=%d stdout=%s stderr=%s", code, h.stdout, h.stderr)
	}
	if !strings.Contains(h.stdout.String(), "CLI-2") {
		t.Errorf("blocked release check table lacks CLI-2:\n%s", h.stdout)
	}

	// Partial list (Total > len(Items)) prints truncation hint while preserving exit 1.
	srv.on("GET", "/v1/issues", canned{body: `{"items":[` + pendingIssueJSON + `],"total":250,"limit":1,"offset":0}`})
	if code := h.run("release", "check", "-p", "CLI", "--limit", "1"); code != 1 {
		t.Fatalf("partial release check: code=%d stdout=%s stderr=%s", code, h.stdout, h.stderr)
	}
	if !strings.Contains(h.stdout.String(), "250건 중 1건 표시") || !strings.Contains(h.stdout.String(), "250건이 남아") {
		t.Errorf("partial release check lacks truncation hint:\n%s", h.stdout)
	}

	// JSON / --fields / --text also returns exit 1 when total > 0.
	hJSON := newHarness(t, srv, false)
	if code := hJSON.run("release", "check", "-p", "CLI", "--fields", "key,verification_note", "--text"); code != 1 {
		t.Fatalf("release check --text: code=%d stdout=%s stderr=%s", code, hJSON.stdout, hJSON.stderr)
	}
	if got := strings.TrimSpace(hJSON.stdout.String()); got != "CLI-2\t모바일 확인" {
		t.Errorf("release check --text = %q", got)
	}

	if !strings.Contains(Usage, "\n  release ") {
		t.Error("agb --help does not list release")
	}
}
