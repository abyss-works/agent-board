package cli

import (
	"flag"
	"strings"
	"testing"
)

func TestParseArgsInterleavesFlagsAndPositionals(t *testing.T) {
	fs := newFlagSet("x")
	var labels stringList
	var n int
	fs.Var(&labels, "label", "")
	fs.IntVar(&n, "limit", 0, "")
	pos, err := parseArgs(fs, []string{"AGB-1", "--label", "a", "second", "--limit=3", "--label", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pos, ",") != "AGB-1,second" || strings.Join(labels, ",") != "a,b" || n != 3 {
		t.Errorf("pos=%v labels=%v n=%d", pos, labels, n)
	}
}

func TestParseArgsStopsAtDoubleDash(t *testing.T) {
	fs := newFlagSet("x")
	var n int
	fs.IntVar(&n, "limit", 0, "")
	pos, err := parseArgs(fs, []string{"--limit", "1", "--", "--limit", "x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pos, ",") != "--limit,x" || n != 1 {
		t.Errorf("pos=%v n=%d", pos, n)
	}
}

func TestParseArgsReportsUnknownFlagAndHelp(t *testing.T) {
	fs := newFlagSet("x")
	if _, err := parseArgs(fs, []string{"--bogus"}); err == nil {
		t.Error("unknown flag accepted")
	}
	fs = newFlagSet("x")
	if _, err := parseArgs(fs, []string{"pos", "--help"}); err != flag.ErrHelp {
		t.Errorf("help err = %v", err)
	}
}

func TestStringListIsRepeatable(t *testing.T) {
	var l stringList
	l.Set("a")
	l.Set("b,c")
	if len(l) != 2 || l[1] != "b,c" || l.String() != "a, b,c" {
		t.Errorf("list = %#v", l)
	}
}
