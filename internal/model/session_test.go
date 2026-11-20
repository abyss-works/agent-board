package model

import (
	"strings"
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

func TestValidSessionState(t *testing.T) {
	for _, s := range []string{SessionStateWaitingApproval, SessionStateWaitingInput, SessionStateWaitingExternal} {
		if !ValidSessionState(s) {
			t.Errorf("ValidSessionState(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "paused", "waiting", "WAITING_INPUT"} {
		if ValidSessionState(s) {
			t.Errorf("ValidSessionState(%q) = true, want false", s)
		}
	}
}

func TestCleanSessionStateReason(t *testing.T) {
	got, _, ok := CleanSessionStateReason("  배포 승인 대기  ")
	if !ok || got != "배포 승인 대기" {
		t.Errorf("trimmed = (%q, %v)", got, ok)
	}
	if _, _, ok := CleanSessionStateReason(strings.Repeat("가", MaxSessionStateReasonRunes)); !ok {
		t.Error("200 runes rejected")
	}
	for _, bad := range []string{
		"",
		"   ",
		strings.Repeat("가", MaxSessionStateReasonRunes+1),
		"줄바꿈\n포함",
		"끝줄바꿈\n",
		"널\x00포함",
		"탭\t포함",
		"제어\x1f문자",
		"삭제\x7f문자",
	} {
		if _, _, ok := CleanSessionStateReason(bad); ok {
			t.Errorf("CleanSessionStateReason(%q) accepted", bad)
		}
	}
}

func TestSessionWaitingValid(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	st := SessionStateWaitingInput
	at23 := NewTime(now.Add(-23 * time.Hour))
	at24 := NewTime(now.Add(-24 * time.Hour))
	at25 := NewTime(now.Add(-25 * time.Hour))

	cases := []struct {
		name string
		s    Session
		want bool
	}{
		{"23h ago", Session{State: &st, StateAt: &at23}, true},
		{"24h exact", Session{State: &st, StateAt: &at24}, true},
		{"25h ago", Session{State: &st, StateAt: &at25}, false},
		{"nil state", Session{State: nil, StateAt: &at23}, false},
		{"nil state_at", Session{State: &st, StateAt: nil}, false},
	}
	for _, c := range cases {
		if got := c.s.WaitingValid(now); got != c.want {
			t.Errorf("%s: WaitingValid = %v, want %v", c.name, got, c.want)
		}
	}
}
