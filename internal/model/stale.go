package model

import "time"

// IssueStaleAfter is how long an open issue may go without activity before it is
// marked stale. Staleness is derived when issues are read; nothing is stored.
const IssueStaleAfter = 30 * 24 * time.Hour

// IsStale reports whether an open issue's last activity is older than
// IssueStaleAfter at now. Closed issues are never stale.
func IsStale(status IssueStatus, lastActivity, now Time) bool {
	if status.Closed() {
		return false
	}
	return now.Time().Sub(lastActivity.Time()) > IssueStaleAfter
}
