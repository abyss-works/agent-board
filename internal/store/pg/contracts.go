// Package pg implements the PostgreSQL 17 domain store and defines the shared
// persistence, graph, ingest, model adapter, and context engine contracts.
package pg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/wsk0715/agent-board/internal/model"
)

// IngestNotifyChannel is the PostgreSQL LISTEN/NOTIFY channel signaled when a
// domain write transaction enqueues a search projection ingest job.
const IngestNotifyChannel = "agb_ingest_job"

// Sentinel errors returned by the PostgreSQL store and graph layer.
var (
	ErrNotFound               = errors.New("not found")
	ErrDuplicateKey           = errors.New("duplicate key")
	ErrDuplicateName          = errors.New("duplicate name")
	ErrDuplicateMilestoneName = errors.New("duplicate milestone name")
	ErrProjectNotActive       = errors.New("project not active")
	ErrGlobalProjectProtected = errors.New("global scope project cannot be deleted")
	ErrVersionMismatch        = errors.New("version mismatch")
	ErrMilestoneScopeLocked   = errors.New("active milestone requires P0 or bug issue unless forced with reason")
	ErrEdgeAlreadyDecided     = errors.New("edge already decided")
	ErrClaimForbidden         = errors.New("claim forbidden")
	ErrSessionExists          = errors.New("session already exists")
	ErrUnverifiedIssues       = errors.New("unverified issues remain")
)

// Optional distinguishes an omitted patch field from an explicit null or value.
type Optional[T any] struct {
	Set   bool
	Value *T
}

// RefError reports a referenced key that does not exist.
type RefError struct {
	Field string
	Key   string
}

func (e *RefError) Error() string { return fmt.Sprintf("%s: %s not found", e.Field, e.Key) }

// ImmutableError reports an attempt to overwrite an immutable record document
// with different content.
type ImmutableError struct {
	Name   string
	Field  string
	SHA256 string
}

func (e *ImmutableError) Error() string {
	return fmt.Sprintf("record %s already exists with sha256 %s", e.Name, e.SHA256)
}

// ErrIssueClaimed reports that an issue already has an active claim held by
// another session.
type ErrIssueClaimed struct {
	Actor     string
	SessionID string
}

func (e *ErrIssueClaimed) Error() string {
	return fmt.Sprintf("issue claimed by %s/%s", e.Actor, e.SessionID)
}

// EdgeAlreadyDecidedError wraps ErrEdgeAlreadyDecided with the edge ID and
// current status.
type EdgeAlreadyDecidedError struct {
	EdgeID int64
	Status model.EdgeStatus
}

func (e *EdgeAlreadyDecidedError) Error() string {
	return fmt.Sprintf("edge %d is already %s", e.EdgeID, e.Status)
}

func (e *EdgeAlreadyDecidedError) Unwrap() error { return ErrEdgeAlreadyDecided }

// UnverifiedIssuesError reports that closing a milestone is blocked by pending
// verification issues.
type UnverifiedIssuesError struct {
	Issues []model.IssueSummary
}

func (e *UnverifiedIssuesError) Error() string {
	keys := make([]string, len(e.Issues))
	for i, it := range e.Issues {
		keys[i] = it.Key
	}
	return fmt.Sprintf("unverified issues remain: %s", strings.Join(keys, ", "))
}

func (e *UnverifiedIssuesError) Unwrap() error { return ErrUnverifiedIssues }

// Trace identifies the actor, session, and host of a domain write.
type Trace = model.Trace

// CreateProjectInput carries fields for creating a project.
type CreateProjectInput struct {
	Key  string
	Name string
}

// ProjectPatch is a partial update for a project.
type ProjectPatch struct {
	Name   *string
	Status *model.ProjectStatus
}

// CreateEpicInput carries fields for creating an epic.
type CreateEpicInput struct {
	Key            string
	Title          string
	Description    string
	SourceIssueID  *int64
	SourceIssueKey *string
}

// EpicFilter narrows ListEpics results.
type EpicFilter struct {
	Statuses []model.EpicStatus
	Limit    int
	Offset   int
}

// EpicPatch is a partial update for an epic.
type EpicPatch struct {
	Title       *string
	Description *string
	Status      *model.EpicStatus
}

// CreateMilestoneInput carries fields for creating a three-stage milestone.
type CreateMilestoneInput struct {
	Name        string
	Description string
	Status      model.MilestoneStatus // defaults to planned when empty
}

// MilestonePatch is a partial update for a milestone.
type MilestonePatch struct {
	Name        *string
	Description *string
	Status      *model.MilestoneStatus
}

// CreateIssueInput carries fields for creating an issue.
type CreateIssueInput struct {
	Title             string
	Body              string
	Type              model.IssueType
	Status            model.IssueStatus
	Priority          model.Priority
	EpicID            *int64
	MilestoneID       *int64
	Labels            []string
	Refs              []string
	RecheckCondition  *string
	DecisionNeeded    *string
	VerificationState model.VerificationState
	VerificationNote  *string
	Force             bool
	Reason            string
}

// IssuePatch is a partial update for an issue.
type IssuePatch struct {
	Title             *string
	Body              *string
	Type              *model.IssueType
	Status            *model.IssueStatus
	Priority          *model.Priority
	EpicID            Optional[int64]
	MilestoneID       Optional[int64]
	Labels            *[]string
	Refs              *[]string
	RecheckCondition  Optional[string]
	DecisionNeeded    Optional[string]
	StatusReason      *string
	VerificationState *model.VerificationState
	VerificationNote  Optional[string]
	Force             bool
	Reason            string
}

// IssueFilter narrows ListIssues results.
type IssueFilter struct {
	Statuses           []model.IssueStatus
	Types              []model.IssueType
	Priorities         []model.Priority
	EpicID             *int64
	MilestoneID        *int64
	Labels             []string
	VerificationStates []model.VerificationState
	Limit              int
	Offset             int
}

// IssueDetail returns an issue together with its labels and references.
type IssueDetail struct {
	model.Issue
	ID int64 `json:"id"`
}

// IssueWriteResult wraps an issue mutation with the queued ingest job ID.
type IssueWriteResult struct {
	Issue       IssueDetail `json:"issue"`
	IngestJobID int64       `json:"ingest_job_id"`
}

// AddCommentInput carries fields for appending a comment to an issue.
type AddCommentInput struct {
	Body        string
	Summary     *string
	CommentKind *model.CommentKind
}

// ActivityWriteResult wraps an activity creation with its queued ingest job ID.
type ActivityWriteResult struct {
	Activity    model.Activity `json:"activity"`
	IngestJobID int64          `json:"ingest_job_id"`
}

// UpsertSpecInput carries fields for creating or updating a mutable spec.
type UpsertSpecInput struct {
	Kind          string
	Title         string
	Tags          []string
	Content       string
	ChangeSummary string
}

// SpecWriteResult wraps a spec upsert with creation status and ingest metadata.
type SpecWriteResult struct {
	Spec         model.SpecDocument `json:"spec"`
	Created      bool               `json:"-"`
	IngestJobID  int64              `json:"ingest_job_id"`
	IngestStatus model.IngestStatus `json:"ingest_status"`
}

// SpecFilter narrows ListSpecs results.
type SpecFilter struct {
	Kinds           []string
	IncludeArchived bool
	Limit           int
	Offset          int
}

// CreateRecordInput carries fields for creating an immutable record document.
type CreateRecordInput struct {
	Name       string
	Kind       string
	RecordedAt *model.Time
	Title      string
	Tags       []string
	Archived   bool
	Imported   bool
	SourcePath *string
	Content    string
}

// SupersedesCandidate summarizes a candidate supersedes edge returned by wait mode.
type SupersedesCandidate struct {
	EdgeID     int64    `json:"edge_id"`
	TargetType string   `json:"target_type"`
	TargetID   int64    `json:"target_id"`
	TargetName string   `json:"target_name"`
	Confidence *float64 `json:"confidence,omitempty"`
	Evidence   *string  `json:"evidence,omitempty"`
}

// RecordWriteResult wraps a record creation with idempotency and ingest metadata.
type RecordWriteResult struct {
	Record               model.RecordDocument  `json:"record"`
	Created              bool                  `json:"-"`
	IngestJobID          int64                 `json:"ingest_job_id"`
	IngestStatus         model.IngestStatus    `json:"ingest_status"`
	SupersedesCandidates []SupersedesCandidate `json:"supersedes_candidates"`
}

// RecordFilter narrows ListRecords results.
type RecordFilter struct {
	Kinds           []string
	IncludeArchived bool
	Limit           int
	Offset          int
}

// CreateMessageInput carries fields for creating a project message.
type CreateMessageInput struct {
	To        string
	ToSession *model.SessionRef
	ToRole    string
	Issue     string
	ReplyTo   *int64
	Title     string
	Body      string
	TTL       string
}

// MessageFilter narrows ListMessages results.
type MessageFilter struct {
	To        string
	ToSession *model.SessionRef
	ToRole    string
	Issue     string
	ReplyTo   *int64
	Viewer    *model.SessionRef
	UnreadBy  string
	All       bool
	Limit     int
	Offset    int
}

// SessionPatch is a partial update for a session.
type SessionPatch struct {
	Parent      Optional[model.SessionRef]
	Role        Optional[string]
	Issue       Optional[string]
	Location    Optional[model.SessionLocation]
	State       Optional[string]
	StateReason Optional[string]
}

// ChildSessionInput carries fields for registering a child session.
type ChildSessionInput struct {
	SessionID string
	Role      *string
	Issue     *string
}

// SessionFilter narrows ListSessions results.
type SessionFilter struct {
	Project string
	Actor   string
	Parent  *model.SessionRef
	Active  *bool
	Named   *bool
	Waiting *bool
	Limit   int
	Offset  int
}

// EventFilter narrows ListEvents results.
type EventFilter struct {
	After   int64
	Project string
	Kinds   []string
	Limit   int
}

// CreateTokenInput carries fields for creating an actor token.
type CreateTokenInput struct {
	Actor           string
	ClientHost      string
	Secret          string
	CreatedBy       string
	ApprovedSession string
	ApprovedHost    string
}

// DomainStore is the transactional persistence contract for domain entities.
type DomainStore interface {
	// Project & Global Scope
	CreateProject(ctx context.Context, tr Trace, in CreateProjectInput) (model.Project, error)
	GetProject(ctx context.Context, key string) (model.Project, error)
	ListProjects(ctx context.Context, statuses []model.ProjectStatus) ([]model.Project, error)
	UpdateProject(ctx context.Context, tr Trace, key string, patch ProjectPatch) (model.Project, error)
	DeleteProject(ctx context.Context, tr Trace, key string) (model.Project, error)

	// Epic
	CreateEpic(ctx context.Context, tr Trace, projectKey string, in CreateEpicInput) (model.Epic, error)
	GetEpic(ctx context.Context, key string) (model.Epic, error)
	ListEpics(ctx context.Context, projectKey string, filter EpicFilter) ([]model.Epic, int, error)
	UpdateEpic(ctx context.Context, tr Trace, key string, patch EpicPatch) (model.Epic, error)
	DeleteEpic(ctx context.Context, tr Trace, key string) (model.Epic, error)

	// Milestone (three-stage: planned, active, closed + deleted)
	CreateMilestone(ctx context.Context, tr Trace, projectKey string, in CreateMilestoneInput) (model.Milestone, error)
	GetMilestone(ctx context.Context, id int64) (model.Milestone, error)
	ListMilestones(ctx context.Context, projectKey string, statuses []model.MilestoneStatus) ([]model.Milestone, error)
	UpdateMilestone(ctx context.Context, tr Trace, id int64, patch MilestonePatch) (model.Milestone, error)
	DeleteMilestone(ctx context.Context, tr Trace, id int64) (model.Milestone, error)

	// Issue, Label, Ref, Comment/Activity
	CreateIssue(ctx context.Context, tr Trace, projectKey string, in CreateIssueInput) (IssueWriteResult, error)
	GetIssue(ctx context.Context, key string) (IssueDetail, error)
	ListIssues(ctx context.Context, projectKey string, filter IssueFilter) ([]IssueDetail, int, error)
	UpdateIssue(ctx context.Context, tr Trace, key string, expectedVersion *int64, patch IssuePatch) (IssueWriteResult, error)
	DeleteIssue(ctx context.Context, tr Trace, key string, expectedVersion *int64, reason string) (IssueDetail, error)
	VerifyIssue(ctx context.Context, tr Trace, key string, expectedVersion *int64) (IssueDetail, error)
	UnverifyIssue(ctx context.Context, tr Trace, key string, expectedVersion *int64) (IssueDetail, error)
	AddComment(ctx context.Context, tr Trace, issueKey string, in AddCommentInput) (ActivityWriteResult, error)

	// Spec Document (mutable upsert + append-only spec_revision)
	UpsertSpec(ctx context.Context, tr Trace, projectKey, slug string, in UpsertSpecInput) (SpecWriteResult, error)
	GetSpec(ctx context.Context, projectKey, slug string) (model.SpecDocument, error)
	ListSpecs(ctx context.Context, projectKey string, filter SpecFilter) ([]model.SpecSummary, int, error)
	ListSpecRevisions(ctx context.Context, projectKey, slug string) ([]model.SpecRevision, error)
	SetSpecArchived(ctx context.Context, tr Trace, projectKey, slug string, archived bool) (model.SpecDocument, error)

	// Record Document (immutable content/sha256, archive transition only)
	CreateRecord(ctx context.Context, tr Trace, projectKey string, in CreateRecordInput) (RecordWriteResult, error)
	GetRecord(ctx context.Context, projectKey, name string) (model.RecordDocument, error)
	ListRecords(ctx context.Context, projectKey string, filter RecordFilter) ([]model.RecordSummary, int, error)
	SetRecordArchived(ctx context.Context, tr Trace, projectKey, name string, archived bool) (model.RecordDocument, error)

	// Session, IssueClaim, Message, Event, Actor, Token
	TouchSession(ctx context.Context, tr Trace, at time.Time) error
	GetSession(ctx context.Context, actor, sessionID string) (model.Session, error)
	ListSessions(ctx context.Context, filter SessionFilter) ([]model.Session, int, error)
	UpdateSession(ctx context.Context, tr Trace, actor, sessionID string, patch SessionPatch) (model.Session, error)
	CreateChildSession(ctx context.Context, tr Trace, in ChildSessionInput) (model.Session, error)
	ClaimIssue(ctx context.Context, tr Trace, issueKey string, reason string) (model.IssueClaim, bool, error)
	ReleaseIssueClaim(ctx context.Context, tr Trace, issueKey string, outcome string, reason *string) (model.IssueClaim, error)
	CreateMessage(ctx context.Context, tr Trace, projectKey string, in CreateMessageInput) (model.Message, error)
	GetMessage(ctx context.Context, messageID int64) (model.Message, error)
	ListMessages(ctx context.Context, projectKey string, filter MessageFilter) ([]model.Message, int, error)
	MarkMessageRead(ctx context.Context, tr Trace, messageID int64, reader string) (model.MessageRead, error)
	DeleteMessage(ctx context.Context, tr Trace, messageID int64) (model.Message, error)
	ListEvents(ctx context.Context, filter EventFilter) ([]model.Event, int64, error)
	EnsureAgentActor(ctx context.Context, actorID string) (bool, error)
	ListActors(ctx context.Context) ([]model.Actor, error)
	CreateToken(ctx context.Context, tr Trace, in CreateTokenInput) (model.Token, error)
	GetTokenByHash(ctx context.Context, tokenHash string) (model.Token, error)
	RevokeToken(ctx context.Context, tr Trace, tokenID int64) (model.Token, error)
}

// --- GraphStore shared contract ---

// NodeRef uniquely identifies a node in the domain graph.
type NodeRef struct {
	Type model.GraphNodeType `json:"type"`
	ID   int64               `json:"id"`
}

// UpsertEdgeInput carries fields for inserting or updating a graph edge.
type UpsertEdgeInput struct {
	ProjectID            int64
	Source               NodeRef
	Target               *NodeRef
	UnresolvedTargetText *string
	Kind                 model.EdgeKind
	Confidence           *float64
	Evidence             *string
}

// CandidateFilter narrows candidate graph edge queries.
type CandidateFilter struct {
	Project string
	Kind    model.EdgeKind
	Limit   int
}

// EdgeCandidate enriches a candidate Edge with display metadata.
type EdgeCandidate struct {
	model.Edge
	SourceKeyOrName string `json:"source_key_or_name,omitempty"`
	TargetKeyOrName string `json:"target_key_or_name,omitempty"`
}

// ExpandOptions configures 1-hop neighborhood expansion.
type ExpandOptions struct {
	IncludeContent bool
	MaxPerSeed     int
}

// NeighborSummary describes a 1-hop neighbor connected to a seed node.
type NeighborSummary struct {
	EdgeID    int64               `json:"edge_id"`
	Direction string              `json:"direction"`
	Kind      model.EdgeKind      `json:"kind"`
	Origin    model.EdgeOrigin    `json:"origin"`
	Status    model.EdgeStatus    `json:"status"`
	NodeType  model.GraphNodeType `json:"node_type"`
	NodeID    int64               `json:"node_id"`
	KeyOrName string              `json:"key_or_name"`
	Title     string              `json:"title"`
	Summary   string              `json:"summary"`
	Content   *string             `json:"content"`
}

// ReadyFilter narrows deterministic ready-queue queries.
type ReadyFilter struct {
	EpicKey        string
	MilestoneID    *int64
	Label          string
	IncludeClaimed bool
	Limit          int
}

// ReadyIssue is an executable issue returned by the ready queue.
type ReadyIssue struct {
	ID                    int64             `json:"id"`
	Key                   string            `json:"key"`
	Title                 string            `json:"title"`
	Type                  model.IssueType   `json:"type"`
	Status                model.IssueStatus `json:"status"`
	Priority              model.Priority    `json:"priority"`
	EpicKey               *string           `json:"epic_key"`
	MilestoneID           *int64            `json:"milestone_id"`
	ClaimedBy             *string           `json:"claimed_by"`
	UnblockedDependencies int               `json:"unblocked_dependencies"`
}

// GraphStore manages explicit and inferred graph edges and traversals.
type GraphStore interface {
	UpsertExplicitEdge(ctx context.Context, in UpsertEdgeInput) (model.Edge, error)
	UpsertInferredEdge(ctx context.Context, in UpsertEdgeInput) (model.Edge, error)
	ListCandidates(ctx context.Context, filter CandidateFilter) ([]EdgeCandidate, error)
	ConfirmEdge(ctx context.Context, actorID string, edgeID int64) (model.Edge, error)
	RejectEdge(ctx context.Context, actorID string, edgeID int64) (model.Edge, error)
	Expand1Hop(ctx context.Context, seeds []NodeRef, opts ExpandOptions) (map[NodeRef][]NeighborSummary, error)
	ListReadyIssues(ctx context.Context, projectKey string, filter ReadyFilter) ([]ReadyIssue, error)
}

// --- IngestQueue shared contract ---

// EnqueueInput describes an ingest job to enqueue inside a write transaction.
type EnqueueInput struct {
	SourceType   model.IngestSourceType
	SourceID     int64
	SourceSHA256 string
	Stage        model.IngestStage
}

// JobResult reports the outcome of waiting for an ingest job.
type JobResult struct {
	JobID     int64
	Status    model.IngestStatus
	TimedOut  bool
	LastError string
}

// IngestQueue coordinates asynchronous search projection jobs.
type IngestQueue interface {
	EnqueueTx(ctx context.Context, tx pgx.Tx, job EnqueueInput) (int64, error)
	ClaimNext(ctx context.Context, workerID string) (*model.IngestJob, error)
	MarkDone(ctx context.Context, jobID int64) error
	MarkRetryOrDeadLetter(ctx context.Context, jobID int64, cause error, backoff time.Duration) (deadLettered bool, err error)
	WaitForJob(ctx context.Context, jobID int64, timeout time.Duration) (JobResult, error)
}

// --- External model adapter contracts ---

// Usage records token and latency metrics for external model calls.
type Usage struct {
	PromptTokens int
	TotalTokens  int
	Latency      time.Duration
}

// NormalizeInput carries raw source content for normalization.
type NormalizeInput struct {
	SourceType model.IngestSourceType
	ProjectKey string
	KeyOrName  string
	Kind       string
	Title      string
	Content    string
}

// NormalizeOutput holds normalized document metadata and text.
type NormalizeOutput struct {
	NormalizedTitle string
	Summary         string
	Metadata        map[string]any
	NormalizedBody  string
}

// Normalizer normalizes heterogeneous source payloads into structured text.
type Normalizer interface {
	PipelineVersion() string
	Normalize(ctx context.Context, in NormalizeInput) (NormalizeOutput, error)
}

// Embedder generates dense vector embeddings for documents and queries.
type Embedder interface {
	Provider() string
	Model() string
	Dimensions() int
	EmbedDocuments(ctx context.Context, texts []string) ([][]float32, Usage, error)
	EmbedQuery(ctx context.Context, query string) ([]float32, Usage, error)
}

// RerankDocument is a candidate passed to a cross-encoder reranker.
type RerankDocument struct {
	ID   string
	Text string
}

// RerankResult is one scored candidate returned by a reranker.
type RerankResult struct {
	Index int
	ID    string
	Score float64
}

// Reranker re-scores candidate chunks for a query.
type Reranker interface {
	Provider() string
	Model() string
	Rerank(ctx context.Context, query string, docs []RerankDocument, topK int) ([]RerankResult, Usage, error)
}

// --- ContextEngine shared contract ---

// ContextQueryRequest configures a single-call context retrieval query.
type ContextQueryRequest struct {
	Query            string   `json:"query"`
	Project          string   `json:"project,omitempty"`
	IncludeGlobal    *bool    `json:"include_global,omitempty"`
	SourceTypes      []string `json:"source_types,omitempty"`
	MaxResults       int      `json:"max_results,omitempty"`
	ConfidenceCutoff *float64 `json:"confidence_cutoff,omitempty"`
	ExpandHops       bool     `json:"expand_hops,omitempty"`
	IncludeArchived  bool     `json:"include_archived,omitempty"`
}

// ContextTiming records latency breakdown in milliseconds.
type ContextTiming struct {
	Embed  float64 `json:"embed"`
	DB     float64 `json:"db"`
	Rerank float64 `json:"rerank"`
	Total  float64 `json:"total"`
}

// ContextItem is one ranked result returned by ContextEngine.Query.
type ContextItem struct {
	NodeType     model.GraphNodeType `json:"node_type"`
	NodeID       int64               `json:"node_id"`
	Project      string              `json:"project"`
	KeyOrName    string              `json:"key_or_name"`
	Kind         string              `json:"kind"`
	Title        string              `json:"title"`
	Score        float64             `json:"score"`
	Summary      string              `json:"summary"`
	Content      string              `json:"content"`
	SupersededBy *NeighborSummary    `json:"superseded_by"`
	Neighbors    []NeighborSummary   `json:"neighbors"`
}

// ContextQueryResponse is the response envelope for ContextEngine.Query.
type ContextQueryResponse struct {
	Query           string        `json:"query"`
	CutoffApplied   bool          `json:"cutoff_applied"`
	CutoffThreshold float64       `json:"cutoff_threshold"`
	ScoreGap12      float64       `json:"score_gap_1_2"`
	FallbackMode    string        `json:"fallback_mode"`
	TimingMS        ContextTiming `json:"timing_ms"`
	Items           []ContextItem `json:"items"`
}

// ContextEngine executes hybrid retrieval, reranking, cutoff, and graph assembly.
type ContextEngine interface {
	Query(ctx context.Context, req ContextQueryRequest) (ContextQueryResponse, error)
}
