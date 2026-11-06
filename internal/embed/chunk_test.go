package embed

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChunk(t *testing.T) {
	if got := Chunk(" \n\t "); len(got) != 0 {
		t.Errorf("blank text = %q", got)
	}
	if got := Chunk("  짧은 글\n"); len(got) != 1 || got[0] != "짧은 글" {
		t.Errorf("short text = %q", got)
	}
	// 2000 runes of 3 bytes each: the limit counts runes, not bytes.
	got := Chunk(strings.Repeat("가", 2000))
	if len(got) != 3 || utf8.RuneCountInString(got[0]) != 900 || utf8.RuneCountInString(got[1]) != 900 || utf8.RuneCountInString(got[2]) != 200 {
		t.Errorf("long text = %d chunks", len(got))
	}
	if got := Chunk(strings.Repeat("a", 900)); len(got) != 1 {
		t.Errorf("exactly the limit = %d chunks", len(got))
	}
	// A chunk that is only whitespace is dropped and the others are trimmed.
	got = Chunk(strings.Repeat("a", 900) + strings.Repeat(" ", 900) + " b ")
	if len(got) != 2 || got[1] != "b" {
		t.Errorf("whitespace chunks = %q", got)
	}
}
