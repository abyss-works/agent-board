package model

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Global scope constants for cross-project specs and records.
const (
	GlobalProjectID  int64 = 0
	GlobalProjectKey       = "GLOBAL"
)

// ScopeKind distinguishes regular projects from the single global scope.
type ScopeKind string

const (
	ScopeProject ScopeKind = "project"
	ScopeGlobal  ScopeKind = "global"
)

// ScopeKinds lists every valid project scope kind.
var ScopeKinds = []ScopeKind{ScopeProject, ScopeGlobal}

// ParseScopeKind returns the ScopeKind named v.
func ParseScopeKind(v string) (ScopeKind, bool) { return parseEnum(v, ScopeKinds) }

// Document payload validation sentinel errors.
var (
	ErrPayloadTooLarge = errors.New("content exceeds 2 MiB limit")
	ErrInvalidUTF8     = errors.New("content is not valid UTF-8")
	ErrBinaryPayload   = errors.New("content contains NUL byte")
)

// ValidateUTF8TextBytes checks that content is at most MaxDocumentBytes, valid
// UTF-8, and free of NUL (0x00) bytes.
func ValidateUTF8TextBytes(content []byte) error {
	if len(content) > MaxDocumentBytes {
		return ErrPayloadTooLarge
	}
	if !utf8.Valid(content) {
		return ErrInvalidUTF8
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return ErrBinaryPayload
	}
	return nil
}

// ValidateUTF8TextString checks that content is at most MaxDocumentBytes,
// valid UTF-8, and free of NUL (0x00) bytes.
func ValidateUTF8TextString(content string) error {
	if len(content) > MaxDocumentBytes {
		return ErrPayloadTooLarge
	}
	if !utf8.ValidString(content) {
		return ErrInvalidUTF8
	}
	if strings.IndexByte(content, 0) >= 0 {
		return ErrBinaryPayload
	}
	return nil
}

// ParseMilestoneStageStatus returns a three-stage milestone status
// (planned, active, closed, deleted).
func ParseMilestoneStageStatus(v string) (MilestoneStatus, bool) {
	return parseEnum(v, MilestoneStageStatuses)
}

// ExpandMilestoneStageStatuses expands a comma-separated status filter for
// three-stage milestones. "all" expands to every stage status.
func ExpandMilestoneStageStatuses(filter string) ([]MilestoneStatus, error) {
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
			add(MilestoneStageStatuses...)
			continue
		}
		s, ok := ParseMilestoneStageStatus(part)
		if !ok {
			return nil, fmt.Errorf("unknown milestone status %q", part)
		}
		add(s)
	}
	return out, nil
}

// EpicStatus is the lifecycle state of an epic.
type EpicStatus string

const (
	EpicOpen    EpicStatus = "open"
	EpicClosed  EpicStatus = "closed"
	EpicDeleted EpicStatus = "deleted"
)

// EpicStatuses lists every valid epic status.
var EpicStatuses = []EpicStatus{EpicOpen, EpicClosed, EpicDeleted}

// ParseEpicStatus returns the epic status named v.
func ParseEpicStatus(v string) (EpicStatus, bool) { return parseEnum(v, EpicStatuses) }

// Epic groups related issues under a project-level initiative.
type Epic struct {
	ID             int64      `json:"id"`
	Project        string     `json:"project"`
	Key            string     `json:"key"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	Status         EpicStatus `json:"status"`
	SourceIssueID  *int64     `json:"source_issue_id,omitempty"`
	SourceIssueKey *string    `json:"source_issue_key,omitempty"`
	ClosedAt       *Time      `json:"closed_at"`
	CreatedAt      Time       `json:"created_at"`
	UpdatedAt      Time       `json:"updated_at"`
	CreatedBy      string     `json:"created_by"`
	UpdatedBy      string     `json:"updated_by"`
}

// Limits on spec slugs and document names.
const (
	MaxSpecSlugLen   = 128
	MaxRecordNameLen = 255
)

var specSlugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// ValidSpecSlug reports whether s is a valid mutable spec slug.
func ValidSpecSlug(s string) bool {
	return len(s) > 0 && len(s) <= MaxSpecSlugLen && specSlugRE.MatchString(s)
}

// SpecDocument is a mutable specification document whose latest body is held
// in its current revision.
type SpecDocument struct {
	ID                int64    `json:"id"`
	Project           string   `json:"project"`
	Slug              string   `json:"slug"`
	Kind              string   `json:"kind"`
	Title             string   `json:"title"`
	Tags              []string `json:"tags"`
	CurrentRevisionID *int64   `json:"current_revision_id,omitempty"`
	CurrentRevisionNo int      `json:"current_revision_no"`
	SHA256            string   `json:"sha256"`
	SizeBytes         int      `json:"size_bytes"`
	Content           *string  `json:"content,omitempty"`
	Archived          bool     `json:"archived"`
	CreatedAt         Time     `json:"created_at"`
	CreatedBy         string   `json:"created_by"`
	UpdatedAt         Time     `json:"updated_at"`
	UpdatedBy         string   `json:"updated_by"`
}

// SpecSummary is a mutable specification without its full content body.
type SpecSummary struct {
	ID                int64    `json:"id"`
	Project           string   `json:"project"`
	Slug              string   `json:"slug"`
	Kind              string   `json:"kind"`
	Title             string   `json:"title"`
	Tags              []string `json:"tags"`
	CurrentRevisionNo int      `json:"current_revision_no"`
	SHA256            string   `json:"sha256"`
	SizeBytes         int      `json:"size_bytes"`
	Archived          bool     `json:"archived"`
	CreatedAt         Time     `json:"created_at"`
	CreatedBy         string   `json:"created_by"`
	UpdatedAt         Time     `json:"updated_at"`
	UpdatedBy         string   `json:"updated_by"`
}

// SpecRevision is one append-only revision of a SpecDocument.
type SpecRevision struct {
	ID             int64  `json:"id"`
	SpecDocumentID int64  `json:"spec_document_id"`
	RevisionNo     int    `json:"revision_no"`
	Title          string `json:"title"`
	Content        string `json:"content"`
	SHA256         string `json:"sha256"`
	SizeBytes      int    `json:"size_bytes"`
	ChangeSummary  string `json:"change_summary"`
	CreatedAt      Time   `json:"created_at"`
	CreatedBy      string `json:"created_by"`
	SessionID      string `json:"session_id"`
	ClientHost     string `json:"client_host"`
}

// RecordDocument is an immutable record document with content and sha256 locked
// after creation; only the archived flag may transition.
type RecordDocument struct {
	ID         int64    `json:"id"`
	Project    string   `json:"project"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	RecordedAt *Time    `json:"recorded_at"`
	Title      string   `json:"title"`
	Tags       []string `json:"tags"`
	Archived   bool     `json:"archived"`
	Imported   bool     `json:"imported"`
	SourcePath *string  `json:"source_path"`
	SHA256     string   `json:"sha256"`
	SizeBytes  int      `json:"size_bytes"`
	Content    *string  `json:"content,omitempty"`
	CreatedAt  Time     `json:"created_at"`
	CreatedBy  string   `json:"created_by"`
	UpdatedAt  Time     `json:"updated_at"`
	UpdatedBy  string   `json:"updated_by"`
}

// RecordSummary is an immutable record without its full content body.
type RecordSummary struct {
	ID         int64    `json:"id"`
	Project    string   `json:"project"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	RecordedAt *Time    `json:"recorded_at"`
	Title      string   `json:"title"`
	Tags       []string `json:"tags"`
	Archived   bool     `json:"archived"`
	Imported   bool     `json:"imported"`
	SourcePath *string  `json:"source_path"`
	SHA256     string   `json:"sha256"`
	SizeBytes  int      `json:"size_bytes"`
	CreatedAt  Time     `json:"created_at"`
	CreatedBy  string   `json:"created_by"`
	UpdatedAt  Time     `json:"updated_at"`
	UpdatedBy  string   `json:"updated_by"`
}

// IngestSourceType names the domain source tracked by an ingest job.
type IngestSourceType string

const (
	IngestSourceIssue    IngestSourceType = "issue"
	IngestSourceActivity IngestSourceType = "activity"
	IngestSourceSpec     IngestSourceType = "spec"
	IngestSourceRecord   IngestSourceType = "record"
)

// IngestStage names the stage of an ingest job.
type IngestStage string

const (
	IngestStageNormalize IngestStage = "normalize"
	IngestStageChunk     IngestStage = "chunk"
	IngestStageIndex     IngestStage = "index"
	IngestStageAll       IngestStage = "all"
)

// IngestStatus is the execution state of an ingest job.
type IngestStatus string

const (
	IngestStatusPending    IngestStatus = "pending"
	IngestStatusRunning    IngestStatus = "running"
	IngestStatusDone       IngestStatus = "done"
	IngestStatusDeadLetter IngestStatus = "dead_letter"
)

// IngestJob represents one queued projection job in ingest_job.
type IngestJob struct {
	ID            int64            `json:"id"`
	SourceType    IngestSourceType `json:"source_type"`
	SourceID      int64            `json:"source_id"`
	SourceSHA256  string           `json:"source_sha256"`
	Stage         IngestStage      `json:"stage"`
	Status        IngestStatus     `json:"status"`
	Attempts      int              `json:"attempts"`
	MaxAttempts   int              `json:"max_attempts"`
	NextAttemptAt Time             `json:"next_attempt_at"`
	LastError     *string          `json:"last_error,omitempty"`
	CreatedAt     Time             `json:"created_at"`
	StartedAt     *Time            `json:"started_at,omitempty"`
	FinishedAt    *Time            `json:"finished_at,omitempty"`
}

// GraphNodeType identifies the domain node type in graph_edge.
type GraphNodeType string

const (
	GraphNodeIssue  GraphNodeType = "issue"
	GraphNodeSpec   GraphNodeType = "spec"
	GraphNodeRecord GraphNodeType = "record"
	GraphNodeEpic   GraphNodeType = "epic"
)

// GraphNodeTypes lists every valid graph node type.
var GraphNodeTypes = []GraphNodeType{GraphNodeIssue, GraphNodeSpec, GraphNodeRecord, GraphNodeEpic}

// ParseGraphNodeType returns the GraphNodeType named v.
func ParseGraphNodeType(v string) (GraphNodeType, bool) { return parseEnum(v, GraphNodeTypes) }

// EdgeKind names the semantic relationship of a graph edge.
type EdgeKind string

const (
	EdgeKindSupersedes EdgeKind = "supersedes"
	EdgeKindAbout      EdgeKind = "about"
	EdgeKindDependsOn  EdgeKind = "depends_on"
	EdgeKindRelated    EdgeKind = "related"
)

// EdgeKinds lists every valid graph edge kind.
var EdgeKinds = []EdgeKind{EdgeKindSupersedes, EdgeKindAbout, EdgeKindDependsOn, EdgeKindRelated}

// ParseEdgeKind returns the EdgeKind named v.
func ParseEdgeKind(v string) (EdgeKind, bool) { return parseEnum(v, EdgeKinds) }

// EdgeOrigin records whether a graph edge was explicitly authored or inferred.
type EdgeOrigin string

const (
	EdgeOriginExplicit EdgeOrigin = "explicit"
	EdgeOriginInferred EdgeOrigin = "inferred"
)

// EdgeOrigins lists every valid graph edge origin.
var EdgeOrigins = []EdgeOrigin{EdgeOriginExplicit, EdgeOriginInferred}

// ParseEdgeOrigin returns the EdgeOrigin named v.
func ParseEdgeOrigin(v string) (EdgeOrigin, bool) { return parseEnum(v, EdgeOrigins) }

// EdgeStatus is the review lifecycle state of a graph edge.
type EdgeStatus string

const (
	EdgeStatusConfirmed EdgeStatus = "confirmed"
	EdgeStatusCandidate EdgeStatus = "candidate"
	EdgeStatusRejected  EdgeStatus = "rejected"
	EdgeStatusRemoved   EdgeStatus = "removed"
)

// EdgeStatuses lists every valid graph edge status.
var EdgeStatuses = []EdgeStatus{EdgeStatusConfirmed, EdgeStatusCandidate, EdgeStatusRejected, EdgeStatusRemoved}

// ParseEdgeStatus returns the EdgeStatus named v.
func ParseEdgeStatus(v string) (EdgeStatus, bool) { return parseEnum(v, EdgeStatuses) }

// Edge represents a persisted directed relationship between domain nodes.
type Edge struct {
	ID                   int64          `json:"id"`
	ProjectID            int64          `json:"project_id,omitempty"`
	Project              string         `json:"project,omitempty"`
	SourceType           GraphNodeType  `json:"source_type"`
	SourceID             int64          `json:"source_id"`
	TargetType           *GraphNodeType `json:"target_type,omitempty"`
	TargetID             *int64         `json:"target_id,omitempty"`
	UnresolvedTargetText *string        `json:"unresolved_target_text,omitempty"`
	Kind                 EdgeKind       `json:"kind"`
	Origin               EdgeOrigin     `json:"origin"`
	Status               EdgeStatus     `json:"status"`
	Confidence           *float64       `json:"confidence,omitempty"`
	Evidence             *string        `json:"evidence,omitempty"`
	DecidedBy            *string        `json:"decided_by,omitempty"`
	DecidedAt            *Time          `json:"decided_at,omitempty"`
	CreatedAt            Time           `json:"created_at"`
}
