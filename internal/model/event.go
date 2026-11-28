package model

import "encoding/json"

// Event kind constants emitted for domain writes.
const (
	EventIssueCreated     = "issue.created"
	EventIssueUpdated     = "issue.updated"
	EventIssueCommented   = "issue.commented"
	EventProjectCreated   = "project.created"
	EventProjectUpdated   = "project.updated"
	EventEpicCreated      = "epic.created"
	EventEpicUpdated      = "epic.updated"
	EventTopicCreated     = "topic.created"
	EventTopicUpdated     = "topic.updated"
	EventMilestoneCreated = "milestone.created"
	EventMilestoneUpdated = "milestone.updated"
	EventDocumentCreated  = "document.created"
	EventDocumentUpdated  = "document.updated"
	EventSpecCreated      = "spec.created"
	EventSpecUpdated      = "spec.updated"
	EventRecordCreated    = "record.created"
	EventRecordUpdated    = "record.updated"
	EventMessageCreated   = "message.created"
	EventMessageDeleted   = "message.deleted"
	EventMessageRead      = "message.read"
	EventSessionCreated   = "session.created"
	EventSessionUpdated   = "session.updated"
	EventClaimAcquired    = "claim.acquired"
	EventClaimReleased    = "claim.released"
)

// Event is one append-only notification record in the unified event stream.
// Project is nil for cross-project events such as session events.
type Event struct {
	ID        int64           `json:"id"`
	At        Time            `json:"at"`
	Project   *string         `json:"project"`
	Kind      string          `json:"kind"`
	Subject   string          `json:"subject"`
	Actor     string          `json:"actor"`
	SessionID string          `json:"session_id"`
	Data      json.RawMessage `json:"data"`
}
