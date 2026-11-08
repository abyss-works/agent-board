package model

import (
	"testing"
	"time"
)

func TestIsStale(t *testing.T) {
	now := NewTime(time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC))
	ago := func(d time.Duration) Time { return NewTime(now.Time().Add(-d)) }
	cases := []struct {
		name   string
		status IssueStatus
		last   Time
		want   bool
	}{
		{"older than 30 days", StatusBacklog, ago(IssueStaleAfter + time.Millisecond), true},
		{"exactly 30 days", StatusBacklog, ago(IssueStaleAfter), false},
		{"recent", StatusDoing, ago(24 * time.Hour), false},
		{"review", StatusReview, ago(IssueStaleAfter + time.Hour), true},
		{"done", StatusDone, ago(IssueStaleAfter + time.Hour), false},
		{"cancelled", StatusCancelled, ago(IssueStaleAfter + time.Hour), false},
		{"deleted", StatusDeleted, ago(IssueStaleAfter + time.Hour), false},
	}
	for _, c := range cases {
		if got := IsStale(c.status, c.last, now); got != c.want {
			t.Errorf("%s: IsStale = %v, want %v", c.name, got, c.want)
		}
	}
}
