package model

import "time"

// IssueDormantAfter is how long an open issue may go without activity before it is
// marked dormant. Dormantness is derived when issues are read; nothing is stored.
const IssueDormantAfter = 30 * 24 * time.Hour

// IsDormant reports whether an open issue's last activity is older than
// IssueDormantAfter at now. Closed issues are never dormant.
func IsDormant(status IssueStatus, lastActivity, now Time) bool {
	if status.Closed() {
		return false
	}
	return now.Time().Sub(lastActivity.Time()) > IssueDormantAfter
}
