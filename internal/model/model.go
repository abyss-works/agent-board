// Package model defines the domain objects, enumerations and field rules of
// the agent-board API. It has no dependency on storage or HTTP.
package model

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// IssueType is the kind of an issue.
type IssueType string

const (
	TypeFeature     IssueType = "feature"
	TypeBug         IssueType = "bug"
	TypeDesign      IssueType = "design"
	TypeIdea        IssueType = "idea"
	TypeObservation IssueType = "observation"
)

// IssueTypes lists every issue type in display order.
var IssueTypes = []IssueType{TypeFeature, TypeBug, TypeDesign, TypeIdea, TypeObservation}

// IssueStatus is the workflow state of an issue. Deletion is a status.
type IssueStatus string

const (
	StatusBacklog   IssueStatus = "backlog"
	StatusDoing     IssueStatus = "doing"
	StatusReview    IssueStatus = "review"
	StatusDone      IssueStatus = "done"
	StatusCancelled IssueStatus = "cancelled"
	StatusDeleted   IssueStatus = "deleted"
)

// IssueStatuses lists every issue status in board column order.
var IssueStatuses = []IssueStatus{StatusBacklog, StatusDoing, StatusReview, StatusDone, StatusCancelled, StatusDeleted}

// OpenStatuses are the statuses an issue can be worked on in.
var OpenStatuses = []IssueStatus{StatusBacklog, StatusDoing, StatusReview}

// ClosedStatuses are the terminal statuses, including soft deletion.
var ClosedStatuses = []IssueStatus{StatusDone, StatusCancelled, StatusDeleted}

// Closed reports whether the status is one of the closed statuses.
func (s IssueStatus) Closed() bool {
	return s == StatusDone || s == StatusCancelled || s == StatusDeleted
}

// ReasonRequired reports whether changing an issue to this status must come
// with a status reason.
func (s IssueStatus) ReasonRequired() bool {
	return s == StatusCancelled || s == StatusDeleted
}

var statusLabels = map[IssueStatus]string{
	StatusBacklog: "백로그", StatusDoing: "진행", StatusReview: "검토",
	StatusDone: "완료", StatusCancelled: "취소", StatusDeleted: "삭제",
}

// Label is the Korean name of the status.
func (s IssueStatus) Label() string {
	if l, ok := statusLabels[s]; ok {
		return l
	}
	return string(s)
}

// ProjectStatus is the lifecycle state of a project. Deletion is a status.
type ProjectStatus string

const (
	ProjectActive   ProjectStatus = "active"
	ProjectArchived ProjectStatus = "archived"
	ProjectDeleted  ProjectStatus = "deleted"
)

// ProjectStatuses lists every project status.
var ProjectStatuses = []ProjectStatus{ProjectActive, ProjectArchived, ProjectDeleted}

// TopicStatus is the lifecycle state of a topic. Deletion is a status.
type TopicStatus string

const (
	TopicActive  TopicStatus = "active"
	TopicDeleted TopicStatus = "deleted"
)

// TopicStatuses lists every topic status.
var TopicStatuses = []TopicStatus{TopicActive, TopicDeleted}

// MilestoneStatus is the lifecycle state of a milestone. Deletion is a status.
type MilestoneStatus string

const (
	MilestoneOpen    MilestoneStatus = "open"
	MilestoneClosed  MilestoneStatus = "closed"
	MilestoneDeleted MilestoneStatus = "deleted"
)

// MilestoneStatuses lists every milestone status.
var MilestoneStatuses = []MilestoneStatus{MilestoneOpen, MilestoneClosed, MilestoneDeleted}

// Priority orders issues in the queue. P0 is the most urgent.
type Priority string

const (
	P0 Priority = "P0"
	P1 Priority = "P1"
	P2 Priority = "P2"
	P3 Priority = "P3"
)

// Priorities lists every priority from most to least urgent.
var Priorities = []Priority{P0, P1, P2, P3}

// DefaultPriority is the priority of an issue created without one. Ideas and
// observations are not work to start; otherwise what the user asked for comes
// before what an agent found.
func DefaultPriority(t IssueType, kind ActorKind) Priority {
	switch {
	case t == TypeIdea || t == TypeObservation:
		return P3
	case kind == ActorUser:
		return P1
	}
	return P2
}

// ActorKind distinguishes the user from agent profiles.
type ActorKind string

const (
	ActorUser  ActorKind = "user"
	ActorAgent ActorKind = "agent"
)

// ActivityKind is the kind of an activity record.
type ActivityKind string

const (
	ActivityCreated   ActivityKind = "created"
	ActivityUpdated   ActivityKind = "updated"
	ActivityCommented ActivityKind = "commented"
)

// ActivityKinds lists every activity kind.
var ActivityKinds = []ActivityKind{ActivityCreated, ActivityUpdated, ActivityCommented}

// DeviceStatus is the state of a device authorization: a request to give an
// agent actor a token on one device. It waits for the user to decide.
type DeviceStatus string

const (
	DevicePending  DeviceStatus = "pending"
	DeviceApproved DeviceStatus = "approved"
	DeviceDenied   DeviceStatus = "denied"
)

// DeviceStatuses lists every device authorization status.
var DeviceStatuses = []DeviceStatus{DevicePending, DeviceApproved, DeviceDenied}

// TokenFilter selects actor tokens by whether they are revoked.
type TokenFilter string

const (
	TokensActive  TokenFilter = "active"
	TokensRevoked TokenFilter = "revoked"
	TokensAll     TokenFilter = "all"
)

// TokenFilters lists every token filter.
var TokenFilters = []TokenFilter{TokensActive, TokensRevoked, TokensAll}

// Device authorization and actor token settings.
const (
	// DeviceExpiresIn is how long a device authorization lives, in seconds.
	DeviceExpiresIn = 600
	// DeviceInterval is the seconds a client waits between exchange attempts.
	DeviceInterval = 5
	// MaxPendingDevices is the most device authorizations that may wait for a
	// decision at once.
	MaxPendingDevices = 20
	// UserCodeAlphabet holds the characters of a user code: consonants only, so
	// a code never spells a word.
	UserCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"
	// UserCodeLength is the number of characters in a user code.
	UserCodeLength = 8
	// TokenPrefix starts every actor token.
	TokenPrefix = "agb_"
)

// RefKinds are the allowed prefixes of a reference ("kind:value").
var RefKinds = []string{"repo", "file", "url", "pr", "issue", "doc"}

func parseEnum[T ~string](v string, all []T) (T, bool) {
	for _, a := range all {
		if string(a) == v {
			return a, true
		}
	}
	var zero T
	return zero, false
}

// ParseIssueType returns the issue type named v.
func ParseIssueType(v string) (IssueType, bool) { return parseEnum(v, IssueTypes) }

// ParseIssueStatus returns the issue status named v. Aliases are not statuses.
func ParseIssueStatus(v string) (IssueStatus, bool) { return parseEnum(v, IssueStatuses) }

// ParseProjectStatus returns the project status named v.
func ParseProjectStatus(v string) (ProjectStatus, bool) { return parseEnum(v, ProjectStatuses) }

// ParseTopicStatus returns the topic status named v.
func ParseTopicStatus(v string) (TopicStatus, bool) { return parseEnum(v, TopicStatuses) }

// ParseMilestoneStatus returns the milestone status named v. The alias "all"
// is not a status.
func ParseMilestoneStatus(v string) (MilestoneStatus, bool) {
	return parseEnum(v, MilestoneStatuses)
}

// ParsePriority returns the priority named v.
func ParsePriority(v string) (Priority, bool) { return parseEnum(v, Priorities) }

// ParseActorKind returns the actor kind named v.
func ParseActorKind(v string) (ActorKind, bool) {
	return parseEnum(v, []ActorKind{ActorUser, ActorAgent})
}

// ParseActivityKind returns the activity kind named v.
func ParseActivityKind(v string) (ActivityKind, bool) { return parseEnum(v, ActivityKinds) }

// ParseDeviceStatus returns the device authorization status named v.
func ParseDeviceStatus(v string) (DeviceStatus, bool) { return parseEnum(v, DeviceStatuses) }

// ParseTokenFilter returns the token filter named v.
func ParseTokenFilter(v string) (TokenFilter, bool) { return parseEnum(v, TokenFilters) }

// ExpandIssueStatuses turns a comma separated status filter into statuses.
// "open", "closed" and "all" are aliases. Duplicates are removed.
func ExpandIssueStatuses(filter string) ([]IssueStatus, error) {
	var out []IssueStatus
	seen := map[IssueStatus]bool{}
	add := func(list ...IssueStatus) {
		for _, s := range list {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	for _, part := range strings.Split(filter, ",") {
		switch part {
		case "open":
			add(OpenStatuses...)
		case "closed":
			add(ClosedStatuses...)
		case "all":
			add(IssueStatuses...)
		default:
			s, ok := ParseIssueStatus(part)
			if !ok {
				return nil, fmt.Errorf("unknown status %q", part)
			}
			add(s)
		}
	}
	return out, nil
}

// ExpandMilestoneStatuses turns a comma separated status filter into
// statuses. "all" is an alias for every status. Duplicates are removed.
func ExpandMilestoneStatuses(filter string) ([]MilestoneStatus, error) {
	var out []MilestoneStatus
	seen := map[MilestoneStatus]bool{}
	add := func(list ...MilestoneStatus) {
		for _, s := range list {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	for _, part := range strings.Split(filter, ",") {
		if part == "all" {
			add(MilestoneStatuses...)
			continue
		}
		s, ok := ParseMilestoneStatus(part)
		if !ok {
			return nil, fmt.Errorf("unknown status %q", part)
		}
		add(s)
	}
	return out, nil
}

var (
	projectKeyRE = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)
	actorIDRE    = regexp.MustCompile(`^[a-z0-9]{1,32}$`)
	labelRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
	sessionIDRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	hostLabelRE  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	issueKeyRE   = regexp.MustCompile(`^([A-Z][A-Z0-9]{1,9})-([1-9][0-9]*)$`)
)

// ValidProjectKey reports whether v is a well formed project key.
func ValidProjectKey(v string) bool { return projectKeyRE.MatchString(v) }

// ValidActorID reports whether v is a well formed actor ID.
func ValidActorID(v string) bool { return actorIDRE.MatchString(v) }

// ValidLabel reports whether v is a well formed label.
func ValidLabel(v string) bool { return labelRE.MatchString(v) }

// ValidSessionID reports whether v is a well formed X-Session-Id value.
func ValidSessionID(v string) bool { return sessionIDRE.MatchString(v) }

// ValidClientHost reports whether v is a well formed X-Client-Host value:
// 1 to 64 characters of dot separated hostname labels.
func ValidClientHost(v string) bool {
	if len(v) == 0 || len(v) > 64 {
		return false
	}
	for _, label := range strings.Split(v, ".") {
		if !hostLabelRE.MatchString(label) {
			return false
		}
	}
	return true
}

// FormatUserCode writes the UserCodeLength characters of a user code as
// XXXX-XXXX.
func FormatUserCode(chars string) string {
	half := UserCodeLength / 2
	return chars[:half] + "-" + chars[half:]
}

// NormalizeUserCode returns the canonical form (XXXX-XXXX) of a user code
// typed in any case and with or without hyphens. ok is false when v is not a
// user code.
func NormalizeUserCode(v string) (code string, ok bool) {
	var chars [UserCodeLength]byte
	n := 0
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == '-' {
			continue
		}
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if n == UserCodeLength || strings.IndexByte(UserCodeAlphabet, c) < 0 {
			return "", false
		}
		chars[n] = c
		n++
	}
	if n != UserCodeLength {
		return "", false
	}
	return FormatUserCode(string(chars[:])), true
}

// ParseRef splits a "kind:value" reference and checks both parts.
func ParseRef(v string) (kind, value string, ok bool) {
	kind, value, found := strings.Cut(v, ":")
	if !found || value == "" || len(value) > 500 {
		return "", "", false
	}
	if _, ok := parseEnum(kind, RefKinds); !ok {
		return "", "", false
	}
	return kind, value, true
}

// IssueKey formats the key of issue number n in a project.
func IssueKey(project string, n int64) string {
	return project + "-" + strconv.FormatInt(n, 10)
}

// ParseIssueKey splits an issue key into project key and number.
func ParseIssueKey(v string) (project string, n int64, ok bool) {
	m := issueKeyRE.FindStringSubmatch(v)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return m[1], n, true
}
