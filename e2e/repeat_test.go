package e2e

import (
	"reflect"
	"strings"
	"testing"
)

// This file has no e2e build tag: its tests need no server, so go test ./...
// runs them. The e2e harness uses the two functions below.

// runPasses calls run for passes 1..n and returns the first nonzero code, or
// 0. It stops after a pass that asks for it, or, with failfast, after the
// first failing pass.
func runPasses(n int, failfast bool, run func(pass int) (code int, stop bool)) int {
	code := 0
	for pass := 1; pass <= n; pass++ {
		c, stop := run(pass)
		if c != 0 && code == 0 {
			code = c
		}
		if stop || (c != 0 && failfast) {
			break
		}
	}
	return code
}

// childArgs returns args with -test.count set to 1, leaving args unchanged.
// go test always writes the flag as -test.count=N; the "-test.count N" form
// is handled too.
func childArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		a = strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
		switch {
		case strings.HasPrefix(a, "test.count="):
			out[i] = "-test.count=1"
		case a == "test.count" && i+1 < len(out):
			out[i+1] = "1"
		}
	}
	return out
}

func TestChildArgs(t *testing.T) {
	tests := []struct {
		name     string
		in, want []string
	}{
		{"equals form", []string{"-test.v", "-test.count=3", "-test.run=X"}, []string{"-test.v", "-test.count=1", "-test.run=X"}},
		{"double dash", []string{"--test.count=2"}, []string{"-test.count=1"}},
		{"space form", []string{"-test.count", "3", "-test.v"}, []string{"-test.count", "1", "-test.v"}},
		{"double dash space form", []string{"--test.count", "3"}, []string{"--test.count", "1"}},
		{"no count", []string{"-test.v"}, []string{"-test.v"}},
		{"empty", nil, nil},
		{"trailing flag without value", []string{"-test.count"}, []string{"-test.count"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := append([]string(nil), tc.in...)
			got := childArgs(in)
			if !reflect.DeepEqual(got, tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
				t.Fatalf("childArgs(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !reflect.DeepEqual(in, tc.in) {
				t.Fatalf("childArgs changed its input: %q", in)
			}
		})
	}
}

func TestRunPasses(t *testing.T) {
	type res struct {
		code int
		stop bool
	}
	tests := []struct {
		name      string
		n         int
		failfast  bool
		results   []res
		wantCode  int
		wantCalls int
	}{
		{"runs all and keeps the first failure", 4, false, []res{{0, false}, {3, false}, {4, false}, {0, false}}, 3, 4},
		{"failfast stops after the first failure", 4, true, []res{{0, false}, {3, false}, {4, false}, {0, false}}, 3, 2},
		{"failfast without failure runs all", 2, true, []res{{0, false}, {0, false}}, 0, 2},
		{"stop ends the run without failfast", 4, false, []res{{0, false}, {1, true}, {0, false}, {0, false}}, 1, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			got := runPasses(tc.n, tc.failfast, func(pass int) (int, bool) {
				calls++
				r := tc.results[pass-1]
				return r.code, r.stop
			})
			if got != tc.wantCode || calls != tc.wantCalls {
				t.Fatalf("code=%d calls=%d, want %d and %d", got, calls, tc.wantCode, tc.wantCalls)
			}
		})
	}
}
