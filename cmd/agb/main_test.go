package main

import (
	"bytes"
	"strings"
	"testing"
)

func run2(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, func(string) string { return "" })
	return code, out.String(), errb.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := run2("version")
	if code != 0 || !strings.Contains(out, "agb ") {
		t.Fatalf("version: code=%d out=%q", code, out)
	}
}

func TestUsage(t *testing.T) {
	if code, _, errb := run2(); code != 2 || !strings.Contains(errb, "serve") {
		t.Errorf("no args: code=%d err=%q", code, errb)
	}
	if code, _, _ := run2("bogus"); code != 2 {
		t.Errorf("unknown command: code=%d", code)
	}
}

func TestServeWithoutConfigFails(t *testing.T) {
	code, _, errb := run2("serve")
	if code != 1 || !strings.Contains(errb, "AGB_TOKEN") {
		t.Errorf("serve without token: code=%d err=%q", code, errb)
	}
}
