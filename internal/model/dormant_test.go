package model

import (
	"testing"
	"time"
)

func TestIsDormant(t *testing.T) {
	now := NewTime(time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC))
	ago := func(d time.Duration) Time { return NewTime(now.Time().Add(-d)) }
	cases := []struct {
		name   string
		status IssueStatus
		last   Time
		want   bool
	}{
		{"older than 30 days", StatusBacklog, ago(IssueDormantAfter + time.Millisecond), true},
		{"exactly 30 days", StatusBacklog, ago(IssueDormantAfter), false},
		{"recent", StatusDoing, ago(24 * time.Hour), false},
		{"review", StatusReview, ago(IssueDormantAfter + time.Hour), true},
		{"done", StatusDone, ago(IssueDormantAfter + time.Hour), false},
		{"cancelled", StatusCancelled, ago(IssueDormantAfter + time.Hour), false},
		{"deleted", StatusDeleted, ago(IssueDormantAfter + time.Hour), false},
	}
	for _, c := range cases {
		if got := IsDormant(c.status, c.last, now); got != c.want {
			t.Errorf("%s: IsDormant = %v, want %v", c.name, got, c.want)
		}
	}
}
