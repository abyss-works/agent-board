package cli

import (
	"regexp"
	"strings"
	"testing"
)

func TestIssueListStaleFlagAndColumn(t *testing.T) {
	plain := plainSummary()
	stale := strings.Replace(strings.Replace(plain, "CLI-1", "CLI-2", 1), `"closed_at":null,`, `"closed_at":null,"stale":true,`, 1)
	srv := newFakeServer(t)
	srv.on("GET", "/v1/issues", canned{body: issueList(plain, stale)})
	h := newHarness(t, srv, true)
	h.app.TermWidth = 0

	if code := h.run("issue", "list", "--stale", "-p", "CLI"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if got := srv.last().Query.Get("stale"); got != "true" || srv.last().Query.Get("project") != "CLI" {
		t.Errorf("--stale sent %q", srv.last().Query.Encode())
	}
	lines := tableLines(h.stdout.String())
	if len(lines) != 3 || !regexp.MustCompile(`^KEY\s+STATUS\s+PRI\s+TYPE\s+LABELS\s+UPDATED\s+STALE\s+TITLE$`).MatchString(lines[0]) {
		t.Fatalf("table:\n%s", h.stdout)
	}
	if !regexp.MustCompile(`\sstale\s`).MatchString(lines[2]) || regexp.MustCompile(`\sstale\s`).MatchString(lines[1]) {
		t.Errorf("only the stale row is marked:\n%s", h.stdout)
	}

	if code := h.run("issue", "list"); code != 0 || srv.last().Query.Has("stale") {
		t.Errorf("no flag must send no filter: code=%d query=%v", code, srv.last().Query)
	}
	if code := h.run("search", "설계", "--stale"); code != 0 || srv.last().Query.Get("stale") != "true" {
		t.Errorf("search shares the filter: code=%d query=%v", code, srv.last().Query)
	}

	// Without a stale row the table keeps its columns.
	srv.on("GET", "/v1/issues", canned{body: issueList(plain)})
	if code := h.run("issue", "list"); code != 0 || strings.Contains(h.stdout.String(), "STALE") {
		t.Errorf("no stale row: code=%d\n%s", code, h.stdout)
	}

	for _, args := range [][]string{{"issue", "list", "--help"}, {"search", "--help"}} {
		if code := h.run(args...); code != 0 || !strings.Contains(h.stdout.String(), "--stale") || !strings.Contains(h.stdout.String(), "30일") {
			t.Errorf("%v: code=%d out=%s", args, code, h.stdout)
		}
	}
}

func TestAgendaShowsStaleCount(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/agenda", canned{body: `{"generated_at":"2026-11-08T03:00:00.000Z","projects":[{"project":{"key":"CLI","name":"cli"},"stale":3}]}`})
	h := newHarness(t, srv, true)
	if code := h.run("agenda"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if !strings.Contains(h.stdout.String(), "== 오래됨: 3 ==") {
		t.Errorf("stale count line missing:\n%s", h.stdout)
	}
	if code := h.run("agenda", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "오래됨") || !strings.Contains(h.stdout.String(), "닫지 않습니다") {
		t.Errorf("agenda help: code=%d\n%s", code, h.stdout)
	}
}
