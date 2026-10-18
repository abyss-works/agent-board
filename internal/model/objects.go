package model

import "encoding/json"

// Actor is a declared user or agent profile.
type Actor struct {
	ID   string    `json:"id"`
	Kind ActorKind `json:"kind"`
}

// Trace carries the three mandatory request headers into every write.
type Trace struct {
	Actor      string
	SessionID  string
	ClientHost string
}

// Project is a project resource.
type Project struct {
	Key       string        `json:"key"`
	Name      string        `json:"name"`
	Status    ProjectStatus `json:"status"`
	CreatedAt Time          `json:"created_at"`
	UpdatedAt Time          `json:"updated_at"`
	CreatedBy string        `json:"created_by"`
	UpdatedBy string        `json:"updated_by"`
}

// Topic is one entry of a project's two-level classification list. A topic
// without a parent is a top-level topic; its path is its name, and a topic
// under it has the path "top-level/topic".
type Topic struct {
	ID        int64       `json:"id"`
	Project   string      `json:"project"`
	Name      string      `json:"name"`
	Parent    *int64      `json:"parent"`
	Path      string      `json:"path"`
	Status    TopicStatus `json:"status"`
	CreatedAt Time        `json:"created_at"`
	UpdatedAt Time        `json:"updated_at"`
	CreatedBy string      `json:"created_by"`
	UpdatedBy string      `json:"updated_by"`
}

// MilestoneProgress counts the issues of a milestone: Total leaves out
// cancelled and deleted issues, Done is the part of Total that is done.
type MilestoneProgress struct {
	Total int `json:"total"`
	Done  int `json:"done"`
}

// Milestone groups the issues of a project into a release or a goal. The
// target date is a calendar date written YYYY-MM-DD. ClosedAt is set while the
// milestone is closed. Progress is computed whenever a milestone is read.
type Milestone struct {
	ID          int64             `json:"id"`
	Project     string            `json:"project"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	TargetDate  *string           `json:"target_date"`
	Status      MilestoneStatus   `json:"status"`
	Progress    MilestoneProgress `json:"progress"`
	ClosedAt    *Time             `json:"closed_at"`
	CreatedAt   Time              `json:"created_at"`
	UpdatedAt   Time              `json:"updated_at"`
	CreatedBy   string            `json:"created_by"`
	UpdatedBy   string            `json:"updated_by"`
}

// Issue is a full issue resource including the body.
type Issue struct {
	Key              string      `json:"key"`
	Project          string      `json:"project"`
	Number           int64       `json:"number"`
	Title            string      `json:"title"`
	Body             string      `json:"body"`
	Type             IssueType   `json:"type"`
	Status           IssueStatus `json:"status"`
	Priority         Priority    `json:"priority"`
	Labels           []string    `json:"labels"`
	Parent           *string     `json:"parent"`
	DependsOn        []string    `json:"depends_on"`
	Refs             []string    `json:"refs"`
	RecheckCondition *string     `json:"recheck_condition"`
	Topic            *int64      `json:"topic"`
	TopicPath        *string     `json:"topic_path"`
	Milestone        *int64      `json:"milestone"`
	MilestoneName    *string     `json:"milestone_name"`
	StatusReason     *string     `json:"status_reason"`
	AcknowledgedAt   *Time       `json:"acknowledged_at"`
	AcknowledgedBy   *string     `json:"acknowledged_by"`
	Version          int64       `json:"version"`
	CreatedAt        Time        `json:"created_at"`
	UpdatedAt        Time        `json:"updated_at"`
	ClosedAt         *Time       `json:"closed_at"`
	StatusChangedAt  Time        `json:"status_changed_at"`
	StatusChangedBy  string      `json:"status_changed_by"`
	CreatedBy        string      `json:"created_by"`
	UpdatedBy        string      `json:"updated_by"`
}

// IssueSummary is an issue without its body, as returned by list views.
// Snippet is set only by full text search.
type IssueSummary struct {
	Key              string      `json:"key"`
	Project          string      `json:"project"`
	Number           int64       `json:"number"`
	Title            string      `json:"title"`
	Type             IssueType   `json:"type"`
	Status           IssueStatus `json:"status"`
	Priority         Priority    `json:"priority"`
	Labels           []string    `json:"labels"`
	Parent           *string     `json:"parent"`
	DependsOn        []string    `json:"depends_on"`
	Refs             []string    `json:"refs"`
	RecheckCondition *string     `json:"recheck_condition"`
	Topic            *int64      `json:"topic"`
	TopicPath        *string     `json:"topic_path"`
	Milestone        *int64      `json:"milestone"`
	MilestoneName    *string     `json:"milestone_name"`
	StatusReason     *string     `json:"status_reason"`
	AcknowledgedAt   *Time       `json:"acknowledged_at"`
	AcknowledgedBy   *string     `json:"acknowledged_by"`
	Version          int64       `json:"version"`
	CreatedAt        Time        `json:"created_at"`
	UpdatedAt        Time        `json:"updated_at"`
	ClosedAt         *Time       `json:"closed_at"`
	StatusChangedAt  Time        `json:"status_changed_at"`
	StatusChangedBy  string      `json:"status_changed_by"`
	CreatedBy        string      `json:"created_by"`
	UpdatedBy        string      `json:"updated_by"`
	Snippet          *string     `json:"snippet,omitempty"`
}

// Summary strips the body from an issue.
func (i *Issue) Summary() IssueSummary {
	return IssueSummary{
		Key: i.Key, Project: i.Project, Number: i.Number, Title: i.Title,
		Type: i.Type, Status: i.Status, Priority: i.Priority,
		Labels: i.Labels, Parent: i.Parent, DependsOn: i.DependsOn, Refs: i.Refs,
		RecheckCondition: i.RecheckCondition, Topic: i.Topic, TopicPath: i.TopicPath,
		Milestone: i.Milestone, MilestoneName: i.MilestoneName, StatusReason: i.StatusReason,
		AcknowledgedAt: i.AcknowledgedAt, AcknowledgedBy: i.AcknowledgedBy, Version: i.Version,
		CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt, ClosedAt: i.ClosedAt,
		StatusChangedAt: i.StatusChangedAt, StatusChangedBy: i.StatusChangedBy,
		CreatedBy: i.CreatedBy, UpdatedBy: i.UpdatedBy,
	}
}

// QueueItem is an issue summary with its readiness in the priority queue.
type QueueItem struct {
	IssueSummary
	Ready     bool     `json:"ready"`
	BlockedBy []string `json:"blocked_by"`
}

// Change is one field change in an activity record.
type Change struct {
	From json.RawMessage `json:"from,omitempty"`
	To   json.RawMessage `json:"to"`
}

// Activity is one append-only record of a write to an issue.
type Activity struct {
	ID         int64             `json:"id"`
	Issue      string            `json:"issue"`
	Project    string            `json:"project"`
	Kind       ActivityKind      `json:"kind"`
	Actor      string            `json:"actor"`
	SessionID  string            `json:"session_id"`
	ClientHost string            `json:"client_host"`
	At         Time              `json:"at"`
	Changes    map[string]Change `json:"changes"`
	Summary    *string           `json:"summary"`
	Body       *string           `json:"body"`
}
