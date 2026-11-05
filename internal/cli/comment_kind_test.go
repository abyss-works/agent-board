package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCommentKind(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("POST", "/v1/issues/CLI-1/comments", canned{status: 201, body: `{"id":1,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"s","client_host":"h","at":"2026-10-17T05:46:06.000Z","changes":null,"summary":"캐시 범위","comment_kind":"decision","body":"Redis는 표시용 캐시로만 쓴다."}`})
	srv.on("GET", "/v1/activity", canned{body: `{"items":[{"id":1,"issue":"CLI-1","project":"CLI","kind":"commented","actor":"claude","session_id":"s","client_host":"h","at":"2026-10-17T05:46:06.000Z","changes":null,"summary":"캐시 범위","comment_kind":"decision","body":"x"}],"total":1,"limit":50,"offset":0}`})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0
	if code := h.run("issue", "comment", "CLI-1", "--kind", "decision", "--summary", "캐시 범위", "--body", "Redis는 표시용 캐시로만 쓴다."); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var in map[string]string
	json.Unmarshal([]byte(srv.last().Body), &in)
	if in["comment_kind"] != "decision" || len(in) != 3 {
		t.Errorf("request body = %v", in)
	}
	if !strings.Contains(h.stdout.String(), "comment_kind") || !strings.Contains(h.stdout.String(), "decision") {
		t.Errorf("render:\n%s", h.stdout)
	}
	if code := h.run("activity", "--comment-kind", "decision,lesson"); code != 0 || srv.last().Query.Get("comment_kind") != "decision,lesson" {
		t.Errorf("activity filter: code=%d query=%v", code, srv.last().Query)
	}
	if !strings.Contains(h.stdout.String(), "[decision] 캐시 범위") {
		t.Errorf("activity table:\n%s", h.stdout)
	}
	srv.reqs = nil
	if code := h.run("issue", "comment", "CLI-1", "--kind", "progress", "--body", "x"); code != 2 || len(srv.reqs) != 0 {
		t.Errorf("bad kind: code=%d reqs=%d", code, len(srv.reqs))
	}
}
