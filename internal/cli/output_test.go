package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

func TestOutputModeSelection(t *testing.T) {
	tty := &App{StdoutIsTerminal: true}
	pipe := &App{StdoutIsTerminal: false}
	cases := []struct {
		name string
		app  *App
		g    globals
		json bool
	}{
		{"tty defaults to table", tty, globals{}, false},
		{"pipe defaults to json", pipe, globals{}, true},
		{"--json forces json on tty", tty, globals{json: true}, true},
		{"--table forces table on pipe", pipe, globals{table: true}, false},
	}
	for _, tc := range cases {
		if got := tc.app.jsonOutput(&tc.g); got != tc.json {
			t.Errorf("%s: json=%v", tc.name, got)
		}
	}
}

func TestWriteJSONPassesBodyThroughWithNewline(t *testing.T) {
	var buf bytes.Buffer
	writeJSON(&buf, []byte(`{"items":[],"total":0}`))
	if buf.String() != `{"items":[],"total":0}`+"\n" {
		t.Errorf("out = %q", buf.String())
	}
	buf.Reset()
	writeJSON(&buf, []byte("{}\n"))
	if buf.String() != "{}\n" {
		t.Errorf("newline doubled: %q", buf.String())
	}
}

func TestDescribeActivityShowsSummaryOfComments(t *testing.T) {
	text := func(s string) *string { return &s }
	cases := []struct {
		name string
		act  model.Activity
		want string
	}{
		{"summary replaces the body", model.Activity{Kind: model.ActivityCommented, Summary: text("서버 랭킹 함수 구현"), Body: text("상세 본문\n둘째 줄")}, "서버 랭킹 함수 구현"},
		{"no summary shows the first body line", model.Activity{Kind: model.ActivityCommented, Body: text("상세 본문\n둘째 줄")}, "상세 본문 …"},
		{"one line body without summary", model.Activity{Kind: model.ActivityCommented, Body: text("한 줄 본문")}, "한 줄 본문"},
	}
	for _, tc := range cases {
		if got := describeActivity(tc.act); got != tc.want {
			t.Errorf("%s: describeActivity = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDescribeActivityShowsFieldChanges(t *testing.T) {
	change := func(from, to string) model.Change {
		c := model.Change{To: json.RawMessage(to)}
		if from != "" {
			c.From = json.RawMessage(from)
		}
		return c
	}
	cases := []struct {
		name    string
		changes map[string]model.Change
		want    string
	}{
		{"milestone assigned", map[string]model.Change{"milestone": change("null", "2")}, "milestone: - → 2"},
		{"milestone moved", map[string]model.Change{"milestone": change("2", "3")}, "milestone: 2 → 3"},
		{"milestone cleared", map[string]model.Change{"milestone": change("3", "null")}, "milestone: 3 → -"},
		{"strings are shown without quotes", map[string]model.Change{"status": change(`"todo"`, `"doing"`)}, "status: todo → doing"},
		{"fields are sorted by name", map[string]model.Change{"status": change(`"todo"`, `"doing"`), "milestone": change("null", "2")}, "milestone: - → 2; status: todo → doing"},
		{"a missing previous value", map[string]model.Change{"milestone": change("", "2")}, "milestone: - → 2"},
	}
	for _, tc := range cases {
		act := model.Activity{Kind: model.ActivityUpdated, Changes: tc.changes}
		if got := describeActivity(act); got != tc.want {
			t.Errorf("%s: describeActivity = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDisplayWidthCountsWideRunes(t *testing.T) {
	if displayWidth("abc") != 3 || displayWidth("한글") != 4 || displayWidth("a한") != 3 {
		t.Errorf("widths = %d %d %d", displayWidth("abc"), displayWidth("한글"), displayWidth("a한"))
	}
}

func TestTruncateRespectsWideRunes(t *testing.T) {
	if got := truncate("한국어 제목입니다", 7); displayWidth(got) > 7 || !strings.HasSuffix(got, "…") {
		t.Errorf("truncate = %q (width %d)", got, displayWidth(got))
	}
	if got := truncate("short", 10); got != "short" {
		t.Errorf("short text changed: %q", got)
	}
	if got := truncate("abcdef", 3); got != "ab…" {
		t.Errorf("truncate = %q", got)
	}
}

func TestTableAlignsAndFitsWidth(t *testing.T) {
	tb := &table{header: []string{"KEY", "TITLE"}}
	tb.add("CLI-1", "짧은 제목")
	tb.add("CLI-10", "아주 긴 제목이라서 폭을 넘어가는 경우입니다")
	var buf bytes.Buffer
	tb.write(&buf, 30)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	if !strings.HasPrefix(lines[0], "KEY     TITLE") || !strings.HasPrefix(lines[1], "CLI-1   짧은 제목") || !strings.HasPrefix(lines[2], "CLI-10  아주") {
		t.Errorf("alignment:\n%s", buf.String())
	}
	for _, l := range lines {
		if displayWidth(l) > 30 {
			t.Errorf("line wider than 30: %q (%d)", l, displayWidth(l))
		}
	}
	buf.Reset()
	tb.write(&buf, 0)
	if !strings.Contains(buf.String(), "넘어가는 경우입니다") {
		t.Errorf("width 0 must not truncate:\n%s", buf.String())
	}
}

func TestFormatTimeUsesLocation(t *testing.T) {
	ts, _ := model.ParseTime("2026-10-17T05:46:06.123Z")
	kst := time.FixedZone("KST", 9*3600)
	if got := formatTime(ts, kst); got != "2026-10-17 14:46" {
		t.Errorf("formatTime = %q", got)
	}
}
