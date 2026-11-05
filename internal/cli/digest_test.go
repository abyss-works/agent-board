package cli

import (
	"strings"
	"testing"
)

func TestDigestSinceForms(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/digest", canned{body: `{"since":"2026-10-17T00:00:00.000Z","until":"2026-10-17T03:00:00.000Z","generated_at":"2026-10-17T03:00:00.000Z","projects":[]}`})
	h := newHarness(t, srv, true)
	cases := map[string]string{
		"2026-10-26T09:00:00+09:00": "2026-10-26T00:00:00Z",
		"2026-10-26":                "2026-10-25T15:00:00Z",
		"3d":                        noon.Add(-72 * 3600 * 1e9).UTC().Format("2006-01-02T15:04:05Z"),
		"12h":                       noon.Add(-12 * 3600 * 1e9).UTC().Format("2006-01-02T15:04:05Z"),
	}
	for in, want := range cases {
		if code := h.run("digest", "--since", in, "-p", "AGB"); code != 0 {
			t.Fatalf("%s: code=%d stderr=%s", in, code, h.stderr)
		}
		if got := srv.last().Query.Get("since"); got != want || srv.last().Query.Get("project") != "AGB" {
			t.Errorf("%s: since=%s want %s", in, got, want)
		}
	}
	if !strings.Contains(h.stdout.String(), "바뀐 것이 없습니다.") {
		t.Errorf("empty output:\n%s", h.stdout)
	}
	for _, bad := range [][]string{{"digest"}, {"digest", "--since", "어제"}} {
		if code := h.run(bad...); code != 2 {
			t.Errorf("%v: code=%d", bad, code)
		}
	}
}

func TestDigestTable(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/digest", canned{body: `{"since":"2026-10-17T00:00:00.000Z","until":"2026-10-17T03:00:00.000Z","generated_at":"2026-10-17T03:00:00.000Z","projects":[{"project":{"key":"AGB","name":"agent-board","status":"active","created_at":"2026-10-17T00:00:00.000Z","updated_at":"2026-10-17T00:00:00.000Z","created_by":"claude","updated_by":"claude"},
"activity":3,"comments":1,
"created":{"total":1,"items":[{"issue":"AGB-9","title":"새 일","status":"backlog","actor":"claude","at":"2026-10-17T01:00:00.000Z"}]},
"status_changes":{"total":2,"items":[{"issue":"AGB-1","title":"옛 일","status":"done","from":"doing","to":"done","actor":"claude","at":"2026-10-17T02:00:00.000Z"}]},
"decisions_asked":{"total":0,"items":[]},
"documents":{"total":1,"items":[{"name":"2026-10-17-1000-decision-x.md","kind":"decision","title":"결정","date":"2026-10-17 10:00","created_by":"claude","created_at":"2026-10-17T02:30:00.000Z"}]}}]}`})
	h := newHarness(t, srv, true)
	if code := h.run("digest", "--since", "1d"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	out := h.stdout.String()
	for _, want := range []string{"### AGB (agent-board): 활동 3, 댓글 1, 새 이슈 1, 상태 변경 2", "AGB-9", "doing → done", "(2개 중 1개)", "2026-10-17-1000-decision-x.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}
