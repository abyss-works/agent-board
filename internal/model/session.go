package model

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// SessionStaleAfter is how long a session may stay without a call before it
// shows as stopped.
const SessionStaleAfter = 30 * time.Minute

// SessionWaitingHold is how long a waiting session remains within its waiting
// hold window.
const SessionWaitingHold = 24 * time.Hour

// Allowed session waiting states.
const (
	SessionStateWaitingApproval = "waiting_approval"
	SessionStateWaitingInput    = "waiting_input"
	SessionStateWaitingExternal = "waiting_external"
)

// MaxSessionStateReasonRunes is the maximum length of a session state_reason
// after trimming surrounding whitespace.
const MaxSessionStateReasonRunes = 200

// ValidSessionState reports whether s is one of the allowed session waiting
// states.
func ValidSessionState(s string) bool {
	switch s {
	case SessionStateWaitingApproval, SessionStateWaitingInput, SessionStateWaitingExternal:
		return true
	}
	return false
}

// CleanSessionStateReason checks a waiting state reason and returns it with
// surrounding whitespace trimmed. Control characters (0x00..0x1f, 0x7f) are
// rejected before trimming; the trimmed string must be 1..MaxSessionStateReasonRunes
// runes.
func CleanSessionStateReason(raw string) (string, string, bool) {
	for _, r := range raw {
		if r <= 0x1f || r == 0x7f {
			return "", "제어 문자를 넣을 수 없습니다.", false
		}
	}
	trimmed := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(trimmed); n < 1 || n > MaxSessionStateReasonRunes {
		return "", fmt.Sprintf("앞뒤 공백 제거 후 1~%d자여야 합니다.", MaxSessionStateReasonRunes), false
	}
	return trimmed, "", true
}

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
// in the trace headers. LastProject, Stale, and LocationConflicts are computed
// when it is read. Children is set only when one session is read.
type Session struct {
	Actor             string           `json:"actor"`
	SessionID         string           `json:"session_id"`
	Named             bool             `json:"named"`
	FirstSeenAt       Time             `json:"first_seen_at"`
	LastSeenAt        Time             `json:"last_seen_at"`
	LastHost          string           `json:"last_host"`
	LastProject       *string          `json:"last_project"`
	Parent            *SessionRef      `json:"parent"`
	Role              *string          `json:"role"`
	Issue             *string          `json:"issue"`
	Location          *SessionLocation `json:"location"`
	LocationConflicts []SessionRef     `json:"location_conflicts"`
	State             *string          `json:"state"`
	StateReason       *string          `json:"state_reason"`
	StateAt           *Time            `json:"state_at"`
	Stale             bool             `json:"stale"`
	UpdatedAt         Time             `json:"updated_at"`
	Children          *[]Session       `json:"children,omitempty"`
}

// WaitingValid reports whether the session is waiting and its state_at is
// within SessionWaitingHold of now.
func (s Session) WaitingValid(now time.Time) bool {
	return s.State != nil && s.StateAt != nil && now.Sub(s.StateAt.Time()) <= SessionWaitingHold
}

func cleanRemotePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.Trim(p, "/")
	p = strings.TrimSuffix(p, ".git")
	return strings.TrimRight(p, "/")
}

func normalizeLocalPath(raw string) string {
	s := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if s == "" {
		return ""
	}
	if s != "/" {
		s = strings.TrimRight(s, "/")
	}
	return s
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func parseSCPRemote(s string) (host, path string, ok bool) {
	slashIdx := strings.IndexAny(s, "/\\")
	cand := s
	if atIdx := strings.IndexByte(s, '@'); atIdx >= 0 && (slashIdx < 0 || atIdx < slashIdx) {
		cand = s[atIdx+1:]
	}
	slashInCand := strings.IndexAny(cand, "/\\")
	colonIdx := strings.IndexByte(cand, ':')
	if colonIdx <= 0 || (slashInCand >= 0 && colonIdx > slashInCand) {
		return "", "", false
	}
	host = cand[:colonIdx]
	if len(host) == 1 && isASCIILetter(host[0]) {
		return "", "", false
	}
	return host, cand[colonIdx+1:], true
}

// NormalizeRepoRemote normalizes SCP and URL repository remotes to
// "<lowercase host>/<path without trailing .git or />" with userinfo removed.
// Non-URL local paths or strings have slashes normalized and trailing '/'
// removed.
func NormalizeRepoRemote(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u.Hostname() != "" {
			host := strings.ToLower(u.Hostname())
			p := cleanRemotePath(u.Path)
			if p == "" {
				return host
			}
			return host + "/" + p
		}
		return normalizeLocalPath(s)
	}
	if host, p, ok := parseSCPRemote(s); ok {
		host = strings.ToLower(host)
		p = cleanRemotePath(p)
		if p == "" {
			return host
		}
		return host + "/" + p
	}
	return normalizeLocalPath(s)
}

// SessionLocationKey returns the normalized conflict-detection key for a
// session's host and reported location. Detached HEAD or incomplete locations
// return ("", false).
func SessionLocationKey(host string, loc *SessionLocation) (string, bool) {
	if loc == nil || loc.Branch == nil {
		return "", false
	}
	branch := strings.TrimSpace(*loc.Branch)
	if branch == "" || branch == "HEAD" {
		return "", false
	}
	if loc.RepoRemote != nil && strings.TrimSpace(*loc.RepoRemote) != "" {
		return "remote:" + NormalizeRepoRemote(*loc.RepoRemote) + "#" + branch, true
	}
	if loc.Worktree != nil && strings.TrimSpace(*loc.Worktree) != "" {
		wt := normalizeLocalPath(*loc.Worktree)
		if len(wt) >= 2 && wt[1] == ':' && wt[0] >= 'A' && wt[0] <= 'Z' {
			wt = strings.ToLower(wt[:1]) + wt[1:]
		}
		return "local:" + host + ":" + wt + "#" + branch, true
	}
	return "", false
}
