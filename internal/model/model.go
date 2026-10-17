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
	TypeDecision    IssueType = "decision"
	TypeIdea        IssueType = "idea"
	TypeObservation IssueType = "observation"
)

// IssueTypes lists every issue type in display order.
var IssueTypes = []IssueType{TypeFeature, TypeBug, TypeDesign, TypeDecision, TypeIdea, TypeObservation}

// IssueStatus is the workflow state of an issue. Deletion is a status.
type IssueStatus string

const (
	StatusBacklog   IssueStatus = "backlog"
	StatusTodo      IssueStatus = "todo"
	StatusDoing     IssueStatus = "doing"
	StatusReview    IssueStatus = "review"
	StatusDone      IssueStatus = "done"
	StatusCancelled IssueStatus = "cancelled"
	StatusDeleted   IssueStatus = "deleted"
)

// IssueStatuses lists every issue status in board column order.
var IssueStatuses = []IssueStatus{StatusBacklog, StatusTodo, StatusDoing, StatusReview, StatusDone, StatusCancelled, StatusDeleted}

// OpenStatuses are the statuses an issue can be worked on in.
var OpenStatuses = []IssueStatus{StatusBacklog, StatusTodo, StatusDoing, StatusReview}

// ClosedStatuses are the terminal statuses, including soft deletion.
var ClosedStatuses = []IssueStatus{StatusDone, StatusCancelled, StatusDeleted}

// Closed reports whether the status is one of the closed statuses.
func (s IssueStatus) Closed() bool {
	return s == StatusDone || s == StatusCancelled || s == StatusDeleted
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

// ParsePriority returns the priority named v.
func ParsePriority(v string) (Priority, bool) { return parseEnum(v, Priorities) }

// ParseActorKind returns the actor kind named v.
func ParseActorKind(v string) (ActorKind, bool) {
	return parseEnum(v, []ActorKind{ActorUser, ActorAgent})
}

// ParseActivityKind returns the activity kind named v.
func ParseActivityKind(v string) (ActivityKind, bool) { return parseEnum(v, ActivityKinds) }

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
