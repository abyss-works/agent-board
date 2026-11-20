package model

import (
	"testing"
	"time"
)

func TestSessionNamed(t *testing.T) {
	for id, want := range map[string]bool{
		"cli-20261120":  false,
		"claude-abc":    true,
		"cli-2026112":   true,
		"cli-202611200": true,
		"xcli-20261120": true,
	} {
		if got := SessionNamed(id); got != want {
			t.Errorf("SessionNamed(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestSessionStale(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		ago  time.Duration
		want bool
	}{{31 * time.Minute, true}, {29 * time.Minute, false}, {30 * time.Minute, false}, {0, false}} {
		if got := SessionStale(NewTime(now.Add(-c.ago)), NewTime(now)); got != c.want {
			t.Errorf("stale after %s = %v, want %v", c.ago, got, c.want)
		}
	}
}

func TestParseSessionRole(t *testing.T) {
	for _, r := range []string{"main", "subagent", "runner"} {
		if _, ok := ParseSessionRole(r); !ok {
			t.Errorf("ParseSessionRole(%q) rejected", r)
		}
	}
	if _, ok := ParseSessionRole("boss"); ok {
		t.Error("ParseSessionRole(boss) accepted")
	}
}
