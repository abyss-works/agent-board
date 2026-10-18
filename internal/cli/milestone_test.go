package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

const (
	milestoneClosedJSON = `{"id":1,"project":"CLI","name":"v0.1.0","description":"","target_date":"2026-10-18","status":"closed","progress":{"total":4,"done":4},"closed_at":"2026-10-18T09:00:00.000Z","created_at":"2026-10-17T05:30:00.000Z","updated_at":"2026-10-18T09:00:00.000Z","created_by":"owner","updated_by":"owner"}`
	milestoneJSON       = `{"id":2,"project":"CLI","name":"v0.2.0","description":"운영 안정화와 릴리즈 관리","target_date":"2026-11-01","status":"open","progress":{"total":5,"done":2},"closed_at":null,"created_at":"2026-10-18T09:00:00.000Z","updated_at":"2026-10-18T09:00:00.000Z","created_by":"owner","updated_by":"owner"}`
	milestoneNoDateJSON = `{"id":3,"project":"CLI","name":"운영 안정화","description":"","target_date":null,"status":"open","progress":{"total":0,"done":0},"closed_at":null,"created_at":"2026-10-18T09:30:00.000Z","updated_at":"2026-10-18T09:30:00.000Z","created_by":"owner","updated_by":"owner"}`

	milestonesJSON = `{"items":[` + milestoneClosedJSON + `,` + milestoneJSON + `,` + milestoneNoDateJSON + `]}`

	// Two milestones of one name: a deleted one and a live one.
	sameNameMilestonesJSON = `{"items":[` +
		`{"id":7,"project":"AMB","name":"v1","description":"","target_date":null,"status":"deleted","progress":{"total":0,"done":0},"closed_at":null,"created_at":"2026-10-18T09:00:00.000Z","updated_at":"2026-10-18T09:00:00.000Z","created_by":"owner","updated_by":"owner"},` +
		`{"id":9,"project":"AMB","name":"v1","description":"","target_date":null,"status":"open","progress":{"total":0,"done":0},"closed_at":null,"created_at":"2026-10-18T09:10:00.000Z","updated_at":"2026-10-18T09:10:00.000Z","created_by":"owner","updated_by":"owner"}]}`
)

func newMilestoneHarness(t *testing.T, tty bool) (*harness, *fakeServer) {
	t.Helper()
	srv := newFakeServer(t)
	srv.on("GET", "/v1/projects/CLI/milestones", canned{body: milestonesJSON})
	srv.on("GET", "/v1/projects/EMP/milestones", canned{body: `{"items":[]}`})
	srv.on("GET", "/v1/projects/AMB/milestones", canned{body: sameNameMilestonesJSON})
	srv.on("POST", "/v1/projects/CLI/milestones", canned{status: 201, body: milestoneJSON})
	srv.on("POST", "/v1/projects/DUP/milestones", canned{status: 409, body: `{"error":{"code":"conflict","message":"이미 있는 이름입니다.","details":[{"field":"name","reason":"duplicate","message":"같은 이름의 마일스톤이 있습니다."}]}}`})
	srv.on("GET", "/v1/milestones/2", canned{body: milestoneJSON})
	srv.on("PATCH", "/v1/milestones/2", canned{body: milestoneJSON})
	srv.on("PATCH", "/v1/milestones/9", canned{status: 422, body: `{"error":{"code":"validation_failed","message":"요청 값이 규칙에 맞지 않습니다.","details":[{"field":"target_date","message":"YYYY-MM-DD 형식의 날짜여야 합니다."}]}}`})
	srv.on("DELETE", "/v1/milestones/2", canned{body: strings.Replace(milestoneJSON, `"status":"open"`, `"status":"deleted"`, 1)})
	srv.on("POST", "/v1/issues", canned{status: 201, body: issueJSON})
	srv.on("PATCH", "/v1/issues/CLI-1", canned{body: issueJSON})
	srv.on("GET", "/v1/issues", canned{body: listJSON})
	return newHarness(t, srv, tty), srv
}

func TestMilestoneListShowsProgress(t *testing.T) {
	h, srv := newMilestoneHarness(t, true)
	if code := h.run("milestone", "list", "-p", "CLI"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if got := srv.last().Query.Encode(); got != "" {
		t.Errorf("query = %q, want the server default", got)
	}
	lines := strings.Split(strings.TrimRight(h.stdout.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("table:\n%s", h.stdout)
	}
	if got := strings.Fields(lines[0]); !equalStrings(got, []string{"ID", "STATUS", "TARGET", "PROGRESS", "NAME"}) {
		t.Errorf("header = %v", got)
	}
	// The server order is kept: by target date, those without one last.
	for i, want := range [][]string{
		{"1", "closed", "2026-10-18", "4/4", "(100%)", "v0.1.0"},
		{"2", "open", "2026-11-01", "2/5", "(40%)", "v0.2.0"},
		{"3", "open", "-", "0/0", "운영", "안정화"},
	} {
		if got := strings.Fields(lines[i+1]); !equalStrings(got, want) {
			t.Errorf("row %d = %v, want %v", i+1, got, want)
		}
	}

	if code := h.run("milestone", "list", "-p", "CLI", "--status", "all"); code != 0 || srv.last().Query.Get("status") != "all" {
		t.Errorf("--status: code=%d query=%v", code, srv.last().Query)
	}
	if code := h.run("milestone", "list", "--project", "CLI", "--status", "open,closed"); code != 0 || srv.last().Query.Get("status") != "open,closed" {
		t.Errorf("--status list: code=%d query=%v", code, srv.last().Query)
	}
	if code := h.run("milestone", "list", "-p", "EMP"); code != 0 || strings.TrimSpace(h.stdout.String()) != "마일스톤이 없습니다." {
		t.Errorf("empty: code=%d out=%q", code, h.stdout.String())
	}
	// JSON is the server body as it is.
	hj := newHarness(t, srv, false)
	if code := hj.run("milestone", "list", "-p", "CLI"); code != 0 || hj.stdout.String() != milestonesJSON+"\n" {
		t.Errorf("json: code=%d out=%q", code, hj.stdout.String())
	}
	if code := h.run("milestone", "list"); code != 2 || !strings.Contains(h.stderr.String(), "--project") {
		t.Errorf("without a project: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("milestone", "list", "-p", "CLI", "extra"); code != 2 {
		t.Errorf("extra argument: code=%d", code)
	}
}

func TestMilestoneGetShowsFields(t *testing.T) {
	h, srv := newMilestoneHarness(t, true)
	if code := h.run("milestone", "get", "2"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Method != "GET" || req.Path != "/v1/milestones/2" {
		t.Errorf("request = %s %s", req.Method, req.Path)
	}
	out := h.stdout.String()
	for _, want := range []string{"name:", "v0.2.0", "2026-11-01", "2/5 (40%)", "open", "2026-10-18 18:00 (owner)", "운영 안정화와 릴리즈 관리"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
	// The description follows the fields, after a blank line.
	if !strings.Contains(out, "\n\n운영 안정화와 릴리즈 관리\n") {
		t.Errorf("description placement:\n%s", out)
	}
	hj := newHarness(t, srv, false)
	if code := hj.run("milestone", "get", "2"); code != 0 || hj.stdout.String() != milestoneJSON+"\n" {
		t.Errorf("json: code=%d out=%q", code, hj.stdout.String())
	}
	for _, args := range [][]string{{"milestone", "get"}, {"milestone", "get", "v0.2.0"}, {"milestone", "get", "0"}, {"milestone", "get", "-1"}, {"milestone", "get", "2", "3"}} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%v: code=%d", args, code)
		}
	}
}

func TestMilestoneCreateSendsOnlyGivenFields(t *testing.T) {
	h, srv := newMilestoneHarness(t, false)
	if code := h.run("milestone", "create", "-p", "CLI", "v0.2.0"); code != 0 {
		t.Fatalf("name only: code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Method != "POST" || req.Path != "/v1/projects/CLI/milestones" || strings.TrimSpace(req.Body) != `{"name":"v0.2.0"}` {
		t.Errorf("request = %s %s %s", req.Method, req.Path, req.Body)
	}
	if h.stdout.String() != milestoneJSON+"\n" {
		t.Errorf("stdout = %q", h.stdout.String())
	}
	// Options and the name may come in any order.
	if code := h.run("milestone", "create", "v0.2.0", "--description", "운영 안정화와 릴리즈 관리", "--target-date", "2026-11-01", "--project", "CLI"); code != 0 {
		t.Fatalf("all fields: code=%d stderr=%s", code, h.stderr)
	}
	want := `{"description":"운영 안정화와 릴리즈 관리","name":"v0.2.0","target_date":"2026-11-01"}`
	if got := strings.TrimSpace(srv.last().Body); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	srv.reqs = nil
	for _, args := range [][]string{
		{"milestone", "create", "-p", "CLI"},
		{"milestone", "create", "v0.2.0"},
		{"milestone", "create", "-p", "CLI", "a", "b"},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%v: code=%d stderr=%s", args, code, h.stderr)
		}
	}
	if len(srv.reqs) != 0 {
		t.Errorf("usage errors must not reach the server: %v", srv.paths())
	}
	// A server rule violation is an API error with the details shown.
	ht := newHarness(t, srv, true)
	if code := ht.run("milestone", "create", "-p", "DUP", "v0.2.0"); code != 1 {
		t.Fatalf("duplicate: code=%d", code)
	}
	for _, w := range []string{"409", "conflict", "name", "duplicate"} {
		if !strings.Contains(ht.stderr.String(), w) {
			t.Errorf("stderr lacks %q:\n%s", w, ht.stderr)
		}
	}
}

func TestMilestoneUpdateSendsMergePatch(t *testing.T) {
	h, srv := newMilestoneHarness(t, false)
	patchBody := func(args ...string) string {
		t.Helper()
		if code := h.run(append([]string{"milestone", "update", "2"}, args...)...); code != 0 {
			t.Fatalf("%v: code=%d stderr=%s", args, code, h.stderr)
		}
		req := srv.last()
		if req.Method != "PATCH" || req.Path != "/v1/milestones/2" {
			t.Errorf("%v: request = %s %s", args, req.Method, req.Path)
		}
		return strings.TrimSpace(req.Body)
	}
	if got, want := patchBody("--name", "새 이름", "--description", "설명", "--target-date", "2026-12-01", "--status", "closed"),
		`{"description":"설명","name":"새 이름","status":"closed","target_date":"2026-12-01"}`; got != want {
		t.Errorf("every field = %s, want %s", got, want)
	}
	if got := patchBody("--name", "이름만"); got != `{"name":"이름만"}` {
		t.Errorf("name only = %s", got)
	}
	// none clears the date: merge patch null.
	if got := patchBody("--target-date", "none"); got != `{"target_date":null}` {
		t.Errorf("--target-date none = %s", got)
	}
	// An empty description is a value, not an absent option.
	if got := patchBody("--description", ""); got != `{"description":""}` {
		t.Errorf("--description \"\" = %s", got)
	}
	if got := patchBody("--status", "open"); got != `{"status":"open"}` {
		t.Errorf("--status = %s", got)
	}

	srv.reqs = nil
	for _, args := range [][]string{
		{"milestone", "update", "2"},
		{"milestone", "update", "--name", "x"},
		{"milestone", "update", "v0.2.0", "--name", "x"},
		{"milestone", "update", "2", "3", "--name", "x"},
		{"milestone", "update", "2", "--bogus", "x"},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%v: code=%d stderr=%s", args, code, h.stderr)
		}
	}
	if len(srv.reqs) != 0 {
		t.Errorf("usage errors must not reach the server: %v", srv.paths())
	}
	// The server validates the date.
	ht := newHarness(t, srv, true)
	if code := ht.run("milestone", "update", "9", "--target-date", "내일"); code != 1 || !strings.Contains(ht.stderr.String(), "target_date") {
		t.Errorf("invalid date: code=%d stderr=%s", code, ht.stderr)
	}
}

func TestMilestoneCloseReopenDelete(t *testing.T) {
	h, srv := newMilestoneHarness(t, false)
	for _, tc := range []struct {
		args   []string
		method string
		body   string
	}{
		{[]string{"milestone", "close", "2"}, "PATCH", `{"status":"closed"}`},
		{[]string{"milestone", "reopen", "2"}, "PATCH", `{"status":"open"}`},
		{[]string{"milestone", "delete", "2"}, "DELETE", ``},
	} {
		if code := h.run(tc.args...); code != 0 {
			t.Fatalf("%v: code=%d stderr=%s", tc.args, code, h.stderr)
		}
		req := srv.last()
		if req.Method != tc.method || req.Path != "/v1/milestones/2" || strings.TrimSpace(req.Body) != tc.body {
			t.Errorf("%v: request = %s %s %q", tc.args, req.Method, req.Path, req.Body)
		}
	}
	if !strings.Contains(h.stdout.String(), `"deleted"`) {
		t.Errorf("delete output = %s", h.stdout)
	}
	srv.reqs = nil
	for _, args := range [][]string{
		{"milestone", "close"}, {"milestone", "reopen", "v0.2.0"}, {"milestone", "delete"}, {"milestone", "delete", "0"}, {"milestone", "close", "2", "3"},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%v: code=%d", args, code)
		}
	}
	if len(srv.reqs) != 0 {
		t.Errorf("usage errors must not reach the server: %v", srv.paths())
	}
	// On a terminal the changed milestone is shown.
	ht := newHarness(t, srv, true)
	if code := ht.run("milestone", "close", "2"); code != 0 || !strings.Contains(ht.stdout.String(), "v0.2.0") || !strings.Contains(ht.stdout.String(), "status:") {
		t.Errorf("close on a terminal: code=%d out=%s", code, ht.stdout)
	}
}

func TestIssueMilestoneOption(t *testing.T) {
	h, srv := newMilestoneHarness(t, false)
	body := func() map[string]any {
		var m map[string]any
		json.Unmarshal([]byte(srv.last().Body), &m)
		return m
	}

	// create: a name is looked up in all the milestones of the issue's project.
	if code := h.run("issue", "create", "-p", "CLI", "-t", "제목", "--milestone", "v0.2.0"); code != 0 {
		t.Fatalf("name: code=%d stderr=%s", code, h.stderr)
	}
	if !equalStrings(srv.paths(), []string{"GET /v1/projects/CLI/milestones", "POST /v1/issues"}) {
		t.Errorf("requests = %v", srv.paths())
	}
	if got := srv.reqs[0].Query.Get("status"); got != "all" {
		t.Errorf("lookup status = %q, want all", got)
	}
	if body()["milestone"] != float64(2) {
		t.Errorf("create body = %s", srv.last().Body)
	}
	if code := h.run("issue", "create", "-p", "CLI", "-t", "제목", "--milestone", "운영 안정화"); code != 0 || body()["milestone"] != float64(3) {
		t.Errorf("name with a space: code=%d body=%s", code, srv.last().Body)
	}
	// A closed milestone is found too.
	if code := h.run("issue", "create", "-p", "CLI", "-t", "제목", "--milestone", "v0.1.0"); code != 0 || body()["milestone"] != float64(1) {
		t.Errorf("closed milestone: code=%d body=%s", code, srv.last().Body)
	}
	srv.reqs = nil
	if code := h.run("issue", "create", "-p", "CLI", "-t", "제목", "--milestone", "3"); code != 0 || body()["milestone"] != float64(3) || len(srv.reqs) != 1 {
		t.Errorf("by id: code=%d body=%s requests=%v", code, srv.last().Body, srv.paths())
	}
	srv.reqs = nil
	for _, args := range [][]string{
		{"issue", "create", "-p", "CLI", "-t", "제목"},
		{"issue", "create", "-p", "CLI", "-t", "제목", "--milestone", "none"},
	} {
		if code := h.run(args...); code != 0 {
			t.Fatalf("%v: code=%d stderr=%s", args, code, h.stderr)
		}
		if _, has := body()["milestone"]; has || len(srv.reqs) != 1 {
			t.Errorf("%v: milestone sent or looked up: body=%s requests=%v", args, srv.last().Body, srv.paths())
		}
		srv.reqs = nil
	}
	// A name that is not found or is not unique is a usage error and nothing is written.
	for _, tc := range []struct {
		project, name string
		want          []string
	}{
		{"CLI", "없는 마일스톤", []string{"agb milestone list -p CLI --status all"}},
		{"CLI", "v0.2", nil},
		{"CLI", "V0.2.0", nil},
		{"AMB", "v1", []string{"7(deleted)", "9(open)"}},
	} {
		srv.reqs = nil
		if code := h.run("issue", "create", "-p", tc.project, "-t", "제목", "--milestone", tc.name); code != 2 {
			t.Errorf("%s %q: code=%d stderr=%s", tc.project, tc.name, code, h.stderr)
		}
		for _, w := range tc.want {
			if !strings.Contains(h.stderr.String(), w) {
				t.Errorf("%s %q: stderr lacks %q:\n%s", tc.project, tc.name, w, h.stderr)
			}
		}
		for _, r := range srv.reqs {
			if r.Method != "GET" {
				t.Errorf("%s %q: an unresolved milestone must not write: %+v", tc.project, tc.name, r)
			}
		}
	}

	// update: the project comes from the issue key; none clears the milestone.
	srv.reqs = nil
	if code := h.run("issue", "update", "CLI-1", "--milestone", "v0.2.0"); code != 0 || body()["milestone"] != float64(2) {
		t.Fatalf("update: code=%d stderr=%s body=%s", code, h.stderr, srv.last().Body)
	}
	if !equalStrings(srv.paths(), []string{"GET /v1/projects/CLI/milestones", "PATCH /v1/issues/CLI-1"}) {
		t.Errorf("update requests = %v", srv.paths())
	}
	if len(body()) != 1 {
		t.Errorf("only the milestone must be sent: %s", srv.last().Body)
	}
	srv.reqs = nil
	if code := h.run("issue", "update", "CLI-1", "--milestone", "none"); code != 0 {
		t.Fatalf("clear: code=%d stderr=%s", code, h.stderr)
	}
	if v, has := body()["milestone"]; !has || v != nil || len(srv.reqs) != 1 {
		t.Errorf("--milestone none must send milestone:null without a lookup: body=%s requests=%v", srv.last().Body, srv.paths())
	}
	srv.reqs = nil
	if code := h.run("issue", "update", "CLI-1", "--milestone", "3", "--priority", "P0"); code != 0 || body()["milestone"] != float64(3) || body()["priority"] != "P0" || len(srv.reqs) != 1 {
		t.Errorf("by id: code=%d body=%s requests=%v", code, srv.last().Body, srv.paths())
	}
	srv.reqs = nil
	if code := h.run("issue", "update", "CLI-1", "--milestone", "없는 마일스톤"); code != 2 {
		t.Errorf("update unknown name: code=%d", code)
	}
	for _, r := range srv.reqs {
		if r.Method == "PATCH" {
			t.Errorf("an unresolved milestone must not patch the issue: %+v", r)
		}
	}

	// list: IDs and none pass through, names are resolved, flags repeat as OR.
	if code := h.run("issue", "list", "-p", "CLI", "--milestone", "v0.2.0"); code != 0 || srv.last().Query.Get("milestone") != "2" || srv.last().Query.Get("project") != "CLI" {
		t.Errorf("list by name: code=%d query=%v", code, srv.last().Query)
	}
	if code := h.run("issue", "list", "--milestone", "3", "--milestone", "none"); code != 0 || srv.last().Query.Get("milestone") != "3,none" {
		t.Errorf("list by id and none: code=%d query=%v", code, srv.last().Query)
	}
	if code := h.run("issue", "list", "-p", "CLI", "--milestone", "v0.1.0", "--milestone", "none", "--milestone", "5"); code != 0 || srv.last().Query.Get("milestone") != "1,none,5" {
		t.Errorf("list by name, none and id: code=%d query=%v", code, srv.last().Query)
	}
	if code := h.run("issue", "list", "--milestone", "v0.2.0"); code != 2 || !strings.Contains(h.stderr.String(), "-p") {
		t.Errorf("list by name without a project: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("issue", "list", "-p", "CLI,OTH", "--milestone", "v0.2.0"); code != 2 {
		t.Errorf("list by name with several projects: code=%d", code)
	}
	if code := h.run("issue", "list", "-p", "CLI,OTH", "--milestone", "2", "--milestone", "none"); code != 0 || srv.last().Query.Get("milestone") != "2,none" {
		t.Errorf("list by id with several projects: code=%d query=%v", code, srv.last().Query)
	}
	if code := h.run("issue", "list", "-p", "CLI", "--milestone", "없는 마일스톤"); code != 2 {
		t.Errorf("list by unknown name: code=%d", code)
	}
	if code := h.run("search", "설계", "-p", "CLI", "--milestone", "운영 안정화"); code != 0 || srv.last().Query.Get("milestone") != "3" || srv.last().Query.Get("q") != "설계" {
		t.Errorf("search: code=%d query=%v", code, srv.last().Query)
	}
}

func TestProgressTextRoundsDown(t *testing.T) {
	for _, tc := range []struct {
		done, total int
		want        string
	}{
		{0, 0, "0/0"},
		{0, 4, "0/4 (0%)"},
		{1, 3, "1/3 (33%)"},
		{2, 3, "2/3 (66%)"},
		{199, 200, "199/200 (99%)"},
		{5, 5, "5/5 (100%)"},
	} {
		if got := progressText(model.MilestoneProgress{Total: tc.total, Done: tc.done}); got != tc.want {
			t.Errorf("progressText(%d/%d) = %q, want %q", tc.done, tc.total, got, tc.want)
		}
	}
}

func TestIssueViewsShowMilestone(t *testing.T) {
	srv := newFakeServer(t)
	withMilestone := strings.Replace(issueJSON, `"recheck_condition":null,`, `"recheck_condition":null,"milestone":2,"milestone_name":"v0.2.0",`, 1)
	srv.on("GET", "/v1/issues/CLI-1", canned{body: withMilestone})
	srv.on("GET", "/v1/issues/CLI-2", canned{body: issueJSON})
	plain := strings.TrimSuffix(strings.TrimPrefix(listJSON, `{"items":[`), `],"total":1,"limit":50,"offset":0}`)
	assigned := strings.Replace(plain, `"recheck_condition":null,`, `"recheck_condition":null,"milestone":2,"milestone_name":"v0.2.0",`, 1)
	srv.on("GET", "/v1/issues", canned{body: `{"items":[` + assigned + `,` + strings.Replace(plain, `CLI-1`, `CLI-2`, 1) + `],"total":2,"limit":50,"offset":0}`})
	h := newHarness(t, srv, true)

	if code := h.run("issue", "get", "CLI-1"); code != 0 || fieldValue(h.stdout.String(), "milestone") != "v0.2.0 (2)" {
		t.Errorf("with a milestone: code=%d\n%s", code, h.stdout)
	}
	if code := h.run("issue", "get", "CLI-2"); code != 0 || fieldValue(h.stdout.String(), "milestone") != "-" {
		t.Errorf("without a milestone: code=%d\n%s", code, h.stdout)
	}

	// The MILESTONE column is in the list when an issue has a milestone.
	if code := h.run("issue", "list"); code != 0 {
		t.Fatalf("list: code=%d stderr=%s", code, h.stderr)
	}
	lines := strings.Split(strings.TrimRight(h.stdout.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("table:\n%s", h.stdout)
	}
	col := strings.Index(lines[0], "MILESTONE")
	if col < 0 || !strings.HasPrefix(lines[1][col:], "v0.2.0") || !strings.HasPrefix(lines[2][col:], "-") {
		t.Errorf("milestone column (at %d):\n%s", col, h.stdout)
	}
	if !strings.Contains(lines[0], "UPDATED") || !strings.Contains(lines[0], "TITLE") || strings.Index(lines[0], "TITLE") < col {
		t.Errorf("header = %q", lines[0])
	}

	// Without milestones the table keeps its columns.
	srv.on("GET", "/v1/issues", canned{body: listJSON})
	if code := h.run("issue", "list"); code != 0 || strings.Contains(h.stdout.String(), "MILESTONE") {
		t.Errorf("no milestone: code=%d\n%s", code, h.stdout)
	}
}

// fieldValue returns the value of the "name:" line of a fields view, or "" when
// the view has no such line.
func fieldValue(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, name+":"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func TestActivityShowsMilestoneChange(t *testing.T) {
	srv := newFakeServer(t)
	record := func(id int, changes string) string {
		return fmt.Sprintf(`{"id":%d,"issue":"CLI-1","project":"CLI","kind":"updated","actor":"claude","session_id":"s","client_host":"h","at":"2026-10-17T07:10:21.000Z","changes":%s,"summary":null,"body":null}`, id, changes)
	}
	srv.on("GET", "/v1/issues/CLI-1/activity", canned{body: `{"items":[` +
		record(3, `{"milestone":{"from":2,"to":null}}`) + `,` +
		record(2, `{"milestone":{"from":2,"to":3},"status":{"from":"todo","to":"doing"}}`) + `,` +
		record(1, `{"milestone":{"from":null,"to":2}}`) + `],"total":3,"limit":50,"offset":0}`})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0
	if code := h.run("issue", "activity", "CLI-1"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	for _, want := range []string{"milestone: 2 → -", "milestone: 2 → 3; status: todo → doing", "milestone: - → 2"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("activity lacks %q:\n%s", want, h.stdout)
		}
	}
}

func TestMilestoneHelp(t *testing.T) {
	h := newHarness(t, nil, true)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--help"}, "milestone"},
		{[]string{"milestone", "--help"}, "reopen"},
		{[]string{"help", "milestone"}, "close"},
		{[]string{"milestone", "list", "--help"}, "--status"},
		{[]string{"milestone", "get", "--help"}, "<ID>"},
		{[]string{"milestone", "create", "--help"}, "--target-date"},
		{[]string{"milestone", "update", "--help"}, "none"},
		{[]string{"milestone", "close", "--help"}, "closed"},
		{[]string{"milestone", "reopen", "--help"}, "open"},
		{[]string{"milestone", "delete", "--help"}, "deleted"},
		{[]string{"issue", "create", "--help"}, "--milestone"},
		{[]string{"issue", "update", "--help"}, "--milestone"},
		{[]string{"issue", "list", "--help"}, "--milestone"},
		{[]string{"search", "--help"}, "--milestone"},
	} {
		if code := h.run(c.args...); code != 0 || !strings.Contains(h.stdout.String(), c.want) {
			t.Errorf("%v: code=%d out=%s", c.args, code, h.stdout)
		}
	}
	if code := h.run("milestone"); code != 2 {
		t.Errorf("group without sub: code=%d", code)
	}
	if code := h.run("milestone", "bogus"); code != 2 {
		t.Errorf("unknown sub: code=%d", code)
	}
}
