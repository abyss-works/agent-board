package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnknownCommandSuggestsNearest(t *testing.T) {
	h := newHarness(t, nil, false)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"overview", "-p", "AGB"}, "agb project overview"},
		{[]string{"isue", "list"}, "agb issue"},
		{[]string{"doc", "serch", "x"}, "agb doc search"},
	}
	for _, c := range cases {
		if code := h.run(c.args...); code != 2 {
			t.Errorf("%v: code=%d", c.args, code)
		}
		if !strings.Contains(h.stderr.String(), "가까운 명령: "+c.want) {
			t.Errorf("%v: stderr=%s", c.args, h.stderr)
		}
	}
	if code := h.run("frobnicate"); code != 2 || strings.Contains(h.stderr.String(), "가까운 명령") {
		t.Errorf("far name: code=%d stderr=%s", code, h.stderr)
	}
}

func TestAliasesRunTheCommand(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues/CLI-1", canned{body: issueJSON, etag: `"3"`})
	srv.on("GET", "/v1/projects/CLI/overview", canned{body: `{}`})
	h := newHarness(t, srv, false)
	if code := h.run("issue", "show", "CLI-1"); code != 0 || srv.last().Path != "/v1/issues/CLI-1" {
		t.Errorf("issue show: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("project", "status", "CLI"); code != 0 || srv.last().Path != "/v1/projects/CLI/overview" {
		t.Errorf("project status: code=%d stderr=%s", code, h.stderr)
	}
}

func TestIssueGetManyKeys(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues/CLI-1", canned{body: issueJSON})
	srv.on("GET", "/v1/issues/CLI-2", canned{body: strings.Replace(issueJSON, `"CLI-1"`, `"CLI-2"`, 1)})
	h := newHarness(t, srv, false)
	if code := h.run("issue", "get", "CLI-1", "CLI-2"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var got []map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil || len(got) != 2 || got[0]["key"] != "CLI-1" || got[1]["key"] != "CLI-2" {
		t.Errorf("stdout=%s err=%v", h.stdout, err)
	}
	h = newHarness(t, srv, true)
	if code := h.run("issue", "get", "CLI-1", "CLI-2"); code != 0 {
		t.Fatalf("table code=%d stderr=%s", code, h.stderr)
	}
	if out := h.stdout.String(); !strings.Contains(out, "CLI-1") || !strings.Contains(out, "CLI-2") {
		t.Errorf("table:\n%s", out)
	}
	if code := h.run("issue", "get", "CLI-1", "CLI-9"); code != 1 {
		t.Errorf("missing key: code=%d", code)
	}
}

func TestFieldsSelectsJSONKeys(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues/CLI-1", canned{body: issueJSON})
	srv.on("GET", "/v1/issues", canned{body: listJSON})
	h := newHarness(t, srv, true)
	if code := h.run("issue", "get", "CLI-1", "--fields", "key,status"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if got := strings.TrimSpace(h.stdout.String()); got != `{"key":"CLI-1","status":"backlog"}` {
		t.Errorf("get: %s", got)
	}
	if code := h.run("--fields", "key,title", "issue", "list"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var list struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &list); err != nil || list.Total != 1 || len(list.Items) != 1 || len(list.Items[0]) != 2 || list.Items[0]["title"] != "첫 이슈" {
		t.Errorf("list: %s err=%v", h.stdout, err)
	}
}

func TestCommentWithStatusChange(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("PATCH", "/v1/issues/CLI-1", canned{body: issueJSON})
	srv.on("POST", "/v1/issues/CLI-1/comments", canned{status: 201, body: `{"id":1,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"s","client_host":"h","at":"2026-10-17T05:46:06.000Z","changes":null,"summary":"끝","body":"결과"}`})
	h := newHarness(t, srv, false)
	if code := h.run("issue", "comment", "CLI-1", "--status", "done", "--reason", "구현 완료", "--summary", "끝", "--body", "결과"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if len(srv.reqs) != 2 || srv.reqs[0].Method != "PATCH" || srv.reqs[1].Method != "POST" {
		t.Fatalf("reqs=%v", srv.reqs)
	}
	var patch map[string]any
	json.Unmarshal([]byte(srv.reqs[0].Body), &patch)
	if patch["status"] != "done" || patch["status_reason"] != "구현 완료" || len(patch) != 2 {
		t.Errorf("patch=%s", srv.reqs[0].Body)
	}
	if code := h.run("issue", "comment", "CLI-1", "--reason", "x", "--body", "y"); code != 2 {
		t.Errorf("reason without status: code=%d", code)
	}
}
