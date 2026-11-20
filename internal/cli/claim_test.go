package cli

import (
	"strings"
	"testing"
)

const (
	activeClaimJSON   = `{"id":7,"issue":"CLI-1","actor":"claude","session_id":"sess-1","claimed_at":"2026-11-20T01:00:00.000Z","released_at":null,"outcome":null,"reason":null,"active":true}`
	releasedClaimJSON = `{"id":7,"issue":"CLI-1","actor":"claude","session_id":"sess-1","claimed_at":"2026-11-20T01:00:00.000Z","released_at":"2026-11-20T02:00:00.000Z","outcome":"done","reason":"작업 마침","active":false}`
	claimListJSON     = `{"items":[` + activeClaimJSON + `,{"id":6,"issue":"CLI-1","actor":"codex","session_id":"sess-0","claimed_at":"2026-11-19T01:00:00.000Z","released_at":"2026-11-19T02:00:00.000Z","outcome":"expired","reason":null,"active":false}],"total":2,"limit":50,"offset":0}`
	claimed409JSON    = `{"error":{"code":"claimed","message":"다른 세션이 점유 중인 이슈입니다.","details":[{"field":"claim","reason":"claimed","message":"codex/sess-0 세션이 점유 중입니다."}]}}`
)

func TestIssueClaimCommands(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("POST", "/v1/issues/CLI-1/claim", canned{status: 201, body: activeClaimJSON})
	srv.on("DELETE", "/v1/issues/CLI-1/claim", canned{body: releasedClaimJSON})
	srv.on("GET", "/v1/issues/CLI-1/claims", canned{body: claimListJSON})

	// Help lists claim, unclaim, and claims.
	h := newHarness(t, srv, true)
	if code := h.run("issue", "--help"); code != 0 {
		t.Fatalf("issue --help: code=%d stderr=%s", code, h.stderr)
	}
	for _, sub := range []string{"claim", "unclaim", "claims"} {
		if !strings.Contains(h.stdout.String(), sub) {
			t.Errorf("issue --help lacks %q:\n%s", sub, h.stdout)
		}
	}

	// issue claim table output.
	if code := h.run("issue", "claim", "CLI-1"); code != 0 {
		t.Fatalf("claim table: code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Method != "POST" || req.Path != "/v1/issues/CLI-1/claim" {
		t.Errorf("claim req = %s %s", req.Method, req.Path)
	}
	for _, want := range []string{"key:", "CLI-1", "actor:", "claude", "session_id:", "sess-1", "claimed_at:", "2026-11-20 10:00", "active:", "true"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("claim table lacks %q:\n%s", want, h.stdout)
		}
	}

	// issue claim JSON and --fields --text (before and after command).
	if code := h.run("issue", "claim", "CLI-1", "--json"); code != 0 || strings.TrimSpace(h.stdout.String()) != activeClaimJSON {
		t.Errorf("claim --json: code=%d out=%s", code, h.stdout)
	}
	if code := h.run("issue", "claim", "CLI-1", "--fields", "actor,session_id,active", "--text"); code != 0 || h.stdout.String() != "claude\tsess-1\ttrue\n" {
		t.Errorf("claim --fields after: code=%d out=%q stderr=%s", code, h.stdout, h.stderr)
	}
	if code := h.run("--fields", "issue,active", "--text", "issue", "claim", "CLI-1"); code != 0 || h.stdout.String() != "CLI-1\ttrue\n" {
		t.Errorf("claim --fields before: code=%d out=%q stderr=%s", code, h.stdout, h.stderr)
	}

	// issue unclaim without flags sends empty body; with flags sends JSON body.
	if code := h.run("issue", "unclaim", "CLI-1", "--json"); code != 0 {
		t.Fatalf("unclaim empty: code=%d stderr=%s", code, h.stderr)
	}
	if req := srv.last(); req.Method != "DELETE" || req.Path != "/v1/issues/CLI-1/claim" || req.Body != "" {
		t.Errorf("unclaim empty req = %s %s %q", req.Method, req.Path, req.Body)
	}
	if code := h.run("issue", "unclaim", "CLI-1", "--outcome", "done", "--reason", "작업 마침", "--table"); code != 0 {
		t.Fatalf("unclaim with flags: code=%d stderr=%s", code, h.stderr)
	}
	if body := strings.TrimSpace(srv.last().Body); body != `{"outcome":"done","reason":"작업 마침"}` {
		t.Errorf("unclaim body = %s", body)
	}
	for _, want := range []string{"key:", "CLI-1", "active:", "false", "outcome:", "done", "reason:", "작업 마침"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("unclaim table lacks %q:\n%s", want, h.stdout)
		}
	}

	// issue claims table, pagination, JSON, and --fields --text.
	if code := h.run("issue", "claims", "CLI-1", "--limit", "10", "--offset", "1", "--table"); code != 0 {
		t.Fatalf("claims table: code=%d stderr=%s", code, h.stderr)
	}
	if q := srv.last().Query; q.Get("limit") != "10" || q.Get("offset") != "1" {
		t.Errorf("claims query = %v", q)
	}
	for _, want := range []string{"ID", "ACTOR", "SESSION", "CLAIMED_AT", "RELEASED_AT", "OUTCOME", "REASON", "claude", "sess-1", "codex", "sess-0", "expired"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("claims table lacks %q:\n%s", want, h.stdout)
		}
	}
	if code := h.run("issue", "claims", "CLI-1", "--fields", "id,actor,active", "--text"); code != 0 || h.stdout.String() != "7\tclaude\ttrue\n6\tcodex\tfalse\n" {
		t.Errorf("claims --fields --text: code=%d out=%q stderr=%s", code, h.stdout, h.stderr)
	}

	// 409 claimed returns exit code 1 with error details on stderr.
	srv.on("POST", "/v1/issues/CLI-2/claim", canned{status: 409, body: claimed409JSON})
	if code := h.run("issue", "claim", "CLI-2"); code != 1 {
		t.Errorf("409 claim: code=%d, want 1", code)
	}
	if !strings.Contains(h.stderr.String(), "claimed") || !strings.Contains(h.stderr.String(), "codex/sess-0") {
		t.Errorf("409 stderr = %q", h.stderr)
	}

	// Usage errors return exit code 2 without hitting server.
	n := len(srv.reqs)
	for _, args := range [][]string{
		{"issue", "claim"},
		{"issue", "claim", "CLI-1", "CLI-2"},
		{"issue", "unclaim"},
		{"issue", "claims"},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%v: code=%d, want 2", args, code)
		}
	}
	if len(srv.reqs) != n {
		t.Errorf("usage errors reached server: %d extra requests", len(srv.reqs)-n)
	}
}
