package model

import (
	"regexp"
	"time"
)

// SessionStaleAfter is how long a session may stay without a call before it
// shows as stopped.
const SessionStaleAfter = 30 * time.Minute

// SessionRole is what a session does among the sessions it belongs with.
type SessionRole string

const (
	RoleMain     SessionRole = "main"
	RoleSubagent SessionRole = "subagent"
	RoleRunner   SessionRole = "runner"
)

// SessionRoles lists every session role.
var SessionRoles = []SessionRole{RoleMain, RoleSubagent, RoleRunner}

// ParseSessionRole returns the session role named v.
func ParseSessionRole(v string) (SessionRole, bool) { return parseEnum(v, SessionRoles) }

// Location field limits, in characters.
const (
	MaxLocationRemoteRunes   = 500
	MaxLocationBranchRunes   = 255
	MaxLocationWorktreeRunes = 500
	MaxLocationHeadRunes     = 64
)

var cliSessionRE = regexp.MustCompile(`^cli-[0-9]{8}$`)

// SessionNamed reports whether a session ID was named by its caller rather
// than being the default the CLI makes from the date (cli-YYYYMMDD).
func SessionNamed(id string) bool { return !cliSessionRE.MatchString(id) }

// SessionStale reports whether a session last seen at lastSeen shows as
// stopped at now.
func SessionStale(lastSeen, now Time) bool {
	return now.Time().Sub(lastSeen.Time()) > SessionStaleAfter
}

// SessionRef names a session by its actor and session ID.
type SessionRef struct {
	Actor     string `json:"actor"`
	SessionID string `json:"session_id"`
}

// SessionLocation is where a session works, as it last reported. Keys it did
// not report are null.
type SessionLocation struct {
	RepoRemote *string `json:"repo_remote"`
	Branch     *string `json:"branch"`
	Worktree   *string `json:"worktree"`
	Head       *string `json:"head"`
	DirtyCount *int    `json:"dirty_count"`
	ReportedAt Time    `json:"reported_at"`
}

// Session is an agent session: an actor and a session ID the server has seen
// in the trace headers. LastProject and Stale are computed when it is read.
// Children is set only when one session is read.
type Session struct {
	Actor       string           `json:"actor"`
	SessionID   string           `json:"session_id"`
	Named       bool             `json:"named"`
	FirstSeenAt Time             `json:"first_seen_at"`
	LastSeenAt  Time             `json:"last_seen_at"`
	LastHost    string           `json:"last_host"`
	LastProject *string          `json:"last_project"`
	Parent      *SessionRef      `json:"parent"`
	Role        *string          `json:"role"`
	Issue       *string          `json:"issue"`
	Location    *SessionLocation `json:"location"`
	Stale       bool             `json:"stale"`
	UpdatedAt   Time             `json:"updated_at"`
	Children    *[]Session       `json:"children,omitempty"`
}
