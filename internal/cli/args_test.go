package cli

import (
	"flag"
	"slices"
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

func TestLeafFlagsRegistersAllGlobalsExceptSkipped(t *testing.T) {
	allFS := newFlagSet("all")
	(&globals{}).addTo(allFS)
	var all []string
	allFS.VisitAll(func(f *flag.Flag) {
		all = append(all, f.Name)
	})

	cases := []struct {
		name string
		skip []string
	}{
		{"activity", []string{"actor", "client-host"}},
		{"session list", []string{"actor"}},
		{"config set-context", []string{"url", "actor"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &globals{json: true, context: "staging", fields: "id", text: true}
			fs := leafFlags(tc.name, g, tc.skip...)
			for _, name := range all {
				got := fs.Lookup(name)
				if slices.Contains(tc.skip, name) {
					if got != nil {
						t.Errorf("skipped flag %q should not be registered", name)
					}
					continue
				}
				if got == nil {
					t.Errorf("global flag %q is missing", name)
				}
			}
			var registered int
			fs.VisitAll(func(*flag.Flag) { registered++ })
			if want := len(all) - len(tc.skip); registered != want {
				t.Errorf("registered %d flags, want %d", registered, want)
			}
			if got := fs.Lookup("context"); got == nil || got.Value.String() != "staging" {
				t.Errorf("context flag = %v, want staging", got)
			}
			if got := fs.Lookup("fields"); got == nil || got.Value.String() != "id" {
				t.Errorf("fields flag = %v, want id", got)
			}
			if got := fs.Lookup("text"); got == nil || got.Value.String() != "true" {
				t.Errorf("text flag = %v, want true", got)
			}
		})
	}
}
