//go:build e2e

package e2e

import (
	"reflect"
	"testing"
)

func TestChildArgs(t *testing.T) {
	tests := []struct {
		name     string
		in, want []string
	}{
		{"equals form", []string{"-test.v", "-test.count=3", "-test.run=X"}, []string{"-test.v", "-test.count=1", "-test.run=X"}},
		{"double dash", []string{"--test.count=2"}, []string{"-test.count=1"}},
		{"space form", []string{"-test.count", "3", "-test.v"}, []string{"-test.count", "1", "-test.v"}},
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

func TestIsChild(t *testing.T) {
	if !isChild("123", 123) {
		t.Fatal("matching parent pid must be trusted")
	}
	for _, v := range []string{"", "1", "abc", "123 "} {
		if isChild(v, 123) {
			t.Fatalf("isChild(%q, 123) = true", v)
		}
	}
}

func TestRunPasses(t *testing.T) {
	codes := func(cs ...int) (func(int) int, *int) {
		calls := 0
		return func(pass int) int { calls++; return cs[pass-1] }, &calls
	}
	run, calls := codes(0, 3, 4, 0)
	if got := runPasses(4, false, run); got != 3 || *calls != 4 {
		t.Fatalf("no failfast: code=%d calls=%d, want 3 and 4", got, *calls)
	}
	run, calls = codes(0, 3, 4, 0)
	if got := runPasses(4, true, run); got != 3 || *calls != 2 {
		t.Fatalf("failfast: code=%d calls=%d, want 3 and 2", got, *calls)
	}
	run, calls = codes(0, 0)
	if got := runPasses(2, true, run); got != 0 || *calls != 2 {
		t.Fatalf("all pass: code=%d calls=%d, want 0 and 2", got, *calls)
	}
}
