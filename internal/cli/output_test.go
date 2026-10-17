package cli

import (
	"bytes"
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
