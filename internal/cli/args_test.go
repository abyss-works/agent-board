package cli

import (
	"flag"
	"slices"
	"strconv"
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

	specs := map[string]struct {
		initVal string
		newVal  string
		get     func(*globals) string
	}{
		"json":        {"true", "true", func(g *globals) string { return strconv.FormatBool(g.json) }},
		"table":       {"true", "true", func(g *globals) string { return strconv.FormatBool(g.table) }},
		"text":        {"true", "true", func(g *globals) string { return strconv.FormatBool(g.text) }},
		"context":     {"ctx-init", "ctx-new", func(g *globals) string { return g.context }},
		"url":         {"http://init.example.com", "http://new.example.com", func(g *globals) string { return g.url }},
		"actor":       {"init-actor", "new-actor", func(g *globals) string { return g.actor }},
		"session-id":  {"init-sess", "new-sess", func(g *globals) string { return g.sessionID }},
		"client-host": {"init-host", "new-host", func(g *globals) string { return g.clientHost }},
		"config":      {"init-config.json", "new-config.json", func(g *globals) string { return g.config }},
		"fields":      {"id", "id,actor", func(g *globals) string { return g.fields }},
	}
	if len(all) != len(specs) {
		t.Fatalf("specs cover %d flags, globals registered %d: %v", len(specs), len(all), all)
	}

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
			gInit := &globals{
				json:       true,
				table:      true,
				text:       true,
				context:    "ctx-init",
				url:        "http://init.example.com",
				actor:      "init-actor",
				sessionID:  "init-sess",
				clientHost: "init-host",
				config:     "init-config.json",
				fields:     "id",
			}
			fs := leafFlags(tc.name, gInit, tc.skip...)
			for _, name := range all {
				sp := specs[name]
				got := fs.Lookup(name)
				if slices.Contains(tc.skip, name) {
					if got != nil {
						t.Errorf("skipped flag %q should not be registered", name)
					}
					continue
				}
				if got == nil {
					t.Errorf("global flag %q is missing", name)
					continue
				}
				if got.Value.String() != sp.initVal || got.DefValue != sp.initVal {
					t.Errorf("flag %q initial=(%q, def=%q), want %q", name, got.Value.String(), got.DefValue, sp.initVal)
				}
			}
			var registered int
			fs.VisitAll(func(*flag.Flag) { registered++ })
			if want := len(all) - len(tc.skip); registered != want {
				t.Errorf("registered %d flags, want %d", registered, want)
			}

			gParsed := &globals{}
			fsParse := leafFlags(tc.name, gParsed, tc.skip...)
			var args []string
			for _, name := range all {
				if slices.Contains(tc.skip, name) {
					continue
				}
				args = append(args, "--"+name+"="+specs[name].newVal)
			}
			if err := fsParse.Parse(args); err != nil {
				t.Fatalf("Parse(%v): %v", args, err)
			}
			for _, name := range all {
				sp := specs[name]
				got := sp.get(gParsed)
				if slices.Contains(tc.skip, name) {
					if got != "" && got != "false" {
						t.Errorf("skipped flag %q altered gParsed: got %q", name, got)
					}
					continue
				}
				if got != sp.newVal {
					t.Errorf("parsed %q -> g = %q, want %q", name, got, sp.newVal)
				}
			}
		})
	}
}
