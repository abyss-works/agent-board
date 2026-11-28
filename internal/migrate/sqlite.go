// Package migrate implements read-only inspection, one-to-one migration, and
// post-migration integrity verification from a SQLite database to PostgreSQL 17.
package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"

	"github.com/wsk0715/agent-board/internal/model"
)

// ExpectedSQLiteSchemaVersion is the SQLite PRAGMA user_version expected by
// the migrator (all 24 tables present).
const ExpectedSQLiteSchemaVersion = 26

// TableAction describes how a SQLite table is handled during migration.
type TableAction string

const (
	ActionMigrate TableAction = "migrate"
	ActionMerge   TableAction = "merge"
	ActionDiscard TableAction = "discard"
)

// TableSpec defines one of the 24 SQLite tables and its PostgreSQL destination.
type TableSpec struct {
	Name   string      `json:"name"`
	Target string      `json:"target"`
	Action TableAction `json:"action"`
}

// SQLiteTables lists all 24 SQLite tables in schema v26 in deterministic order.
var SQLiteTables = []TableSpec{
	{Name: "actor", Target: "actor", Action: ActionMigrate},
	{Name: "project", Target: "project", Action: ActionMigrate},
	{Name: "token", Target: "token", Action: ActionMigrate},
	{Name: "milestone", Target: "milestone", Action: ActionMigrate},
	{Name: "topic", Target: "issue_label", Action: ActionMerge},
	{Name: "issue", Target: "issue+epic", Action: ActionMigrate},
	{Name: "issue_label", Target: "issue_label", Action: ActionMigrate},
	{Name: "issue_dependency", Target: "graph_edge", Action: ActionMerge},
	{Name: "issue_ref", Target: "issue_ref", Action: ActionMigrate},
	{Name: "activity", Target: "activity", Action: ActionMigrate},
	{Name: "document", Target: "record_document", Action: ActionMigrate},
	{Name: "document_relation", Target: "graph_edge", Action: ActionMerge},
	{Name: "document_activity", Target: "document_activity", Action: ActionMigrate},
	{Name: "resource_activity", Target: "resource_activity", Action: ActionMigrate},
	{Name: "message", Target: "message", Action: ActionMigrate},
	{Name: "message_read", Target: "message_read", Action: ActionMigrate},
	{Name: "session", Target: "session", Action: ActionMigrate},
	{Name: "issue_claim", Target: "issue_claim", Action: ActionMigrate},
	{Name: "event", Target: "event", Action: ActionMigrate},
	{Name: "issue_fts", Target: "rag_chunk(backfill)", Action: ActionDiscard},
	{Name: "document_fts", Target: "rag_chunk(backfill)", Action: ActionDiscard},
	{Name: "comment_fts", Target: "rag_chunk(backfill)", Action: ActionDiscard},
	{Name: "embedding", Target: "rag_embedding(backfill)", Action: ActionDiscard},
	{Name: "similar_document", Target: "graph_edge(backfill)", Action: ActionDiscard},
}

// PGIdentityTables lists every PostgreSQL table with an IDENTITY id column
// whose sequence must be synchronized after explicit-ID loading.
var PGIdentityTables = []string{
	"token",
	"project",
	"epic",
	"milestone",
	"issue",
	"spec_document",
	"spec_revision",
	"record_document",
	"activity",
	"document_activity",
	"resource_activity",
	"message",
	"issue_claim",
	"event",
	"ingest_job",
	"normalized_artifact",
	"rag_chunk",
	"embedding_generation",
	"graph_edge",
}

// Bool returns a pointer to v for use in Options.
func Bool(v bool) *bool { return &v }

// openSQLiteReadOnly opens sqlitePath strictly read-only without modifying the
// source file. It verifies that the file exists and enforces query_only=1.
func openSQLiteReadOnly(ctx context.Context, sqlitePath string) (*sql.DB, int, error) {
	if strings.TrimSpace(sqlitePath) == "" {
		return nil, 0, errors.New("source SQLite path is required")
	}
	fi, err := os.Stat(sqlitePath)
	if err != nil {
		return nil, 0, fmt.Errorf("stat source SQLite %q: %w", sqlitePath, err)
	}
	if fi.IsDir() {
		return nil, 0, fmt.Errorf("source SQLite %q is a directory", sqlitePath)
	}
	absPath, err := filepath.Abs(sqlitePath)
	if err != nil {
		return nil, 0, fmt.Errorf("resolve source SQLite path: %w", err)
	}

	uri := (&url.URL{
		Scheme:   "file",
		Path:     filepath.ToSlash(absPath),
		RawQuery: "mode=ro&_query_only=1&_pragma=query_only(1)&_pragma=busy_timeout(5000)",
	}).String()

	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, 0, fmt.Errorf("open SQLite read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, 0, fmt.Errorf("ping SQLite read-only: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA query_only = ON`); err != nil {
		_ = db.Close()
		return nil, 0, fmt.Errorf("enable SQLite query_only: %w", err)
	}

	var userVer int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVer); err != nil {
		_ = db.Close()
		return nil, 0, fmt.Errorf("read SQLite user_version: %w", err)
	}
	if userVer != ExpectedSQLiteSchemaVersion {
		_ = db.Close()
		return nil, userVer, fmt.Errorf("unsupported SQLite schema version %d (expected %d)", userVer, ExpectedSQLiteSchemaVersion)
	}
	return db, userVer, nil
}

func sqliteTableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type IN ('table', 'view') AND name = ?`, table,
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func sqliteTableCount(ctx context.Context, db *sql.DB, table string) (int64, error) {
	exists, err := sqliteTableExists(ctx, db, table)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	var count int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+table+`"`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count sqlite table %s: %w", table, err)
	}
	return count, nil
}

// --- SQLite row structs ---

type sqliteActor struct {
	ID        string
	Kind      string
	CreatedAt time.Time
}

type sqliteProject struct {
	ID         int64
	Key        string
	Name       string
	Status     string
	NextNumber int
	CreatedAt  time.Time
	UpdatedAt  time.Time
	CreatedBy  string
	UpdatedBy  string
}

type sqliteToken struct {
	ID             int64
	ActorID        string
	ClientHost     string
	TokenHash      string
	CreatedAt      time.Time
	CreatedBy      string
	LastUsedAt     *time.Time
	RevokedAt      *time.Time
	RevokedBy      *string
	RevokedSession *string
	RevokedHost    *string
}

type sqliteMilestone struct {
	ID          int64
	ProjectID   int64
	Name        string
	Description string
	TargetDate  *string
	Status      string
	ClosedAt    *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CreatedBy   string
	UpdatedBy   string
}

type sqliteTopic struct {
	ID        int64
	ProjectID int64
	Name      string
	ParentID  *int64
	Status    string
}

type sqliteIssue struct {
	ID                int64
	ProjectID         int64
	Number            int
	Key               string
	Title             string
	Body              string
	Type              string
	Status            string
	Priority          string
	ParentID          *int64
	RecheckCondition  *string
	DecisionNeeded    *string
	TopicID           *int64
	MilestoneID       *int64
	StatusReason      *string
	VerificationState string
	VerificationNote  *string
	VerifiedAt        *time.Time
	VerifiedBy        *string
	Version           int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ClosedAt          *time.Time
	StatusChangedAt   time.Time
	StatusChangedBy   string
	CreatedBy         string
	UpdatedBy         string
}

type sqliteIssueLabel struct {
	IssueID int64
	Label   string
}

type sqliteIssueDependency struct {
	IssueID     int64
	DependsOnID int64
}

type sqliteIssueRef struct {
	IssueID          int64
	Ref              string
	TargetIssueID    *int64
	TargetDocumentID *int64
}

type sqliteActivity struct {
	ID          int64
	IssueID     int64
	Kind        string
	ActorID     string
	SessionID   string
	ClientHost  string
	At          time.Time
	Changes     *string
	Body        *string
	Summary     *string
	CommentKind *string
}

type sqliteDocument struct {
	ID         int64
	ProjectID  int64
	Name       string
	Kind       string
	Date       *string
	Title      string
	Tags       string
	Archived   bool
	Imported   bool
	SourcePath *string
	SHA256     string
	SizeBytes  int
	Content    string
	CreatedAt  time.Time
	CreatedBy  string
	UpdatedAt  time.Time
	UpdatedBy  string
}

type sqliteDocumentRelation struct {
	ID               int64
	DocumentID       int64
	Kind             string
	TargetDocumentID *int64
	TargetIssueID    *int64
	TargetText       *string
	RemovedAt        *time.Time
	RemovedBy        *string
}

type sqliteDocumentActivity struct {
	ID         int64
	DocumentID int64
	ActorID    string
	SessionID  string
	ClientHost string
	At         time.Time
	Changes    string
}

type sqliteResourceActivity struct {
	ID         int64
	ProjectID  int64
	Resource   string
	ResourceID int64
	Kind       string
	ActorID    string
	SessionID  string
	ClientHost string
	At         time.Time
	Changes    string
}

type sqliteMessage struct {
	ID             int64
	ProjectID      int64
	ToLabel        *string
	ToActor        *string
	ToSessionID    *string
	ToRole         *string
	IssueID        *int64
	ReplyToID      *int64
	Title          string
	Body           string
	CreatedAt      time.Time
	CreatedBy      string
	CreatedSession string
	CreatedHost    string
	ExpiresAt      time.Time
	DeletedAt      *time.Time
	DeletedBy      *string
}

type sqliteMessageRead struct {
	MessageID   int64
	Reader      string
	ReadAt      time.Time
	ReadBy      string
	ReadSession string
	ReadHost    string
}

type sqliteSession struct {
	ActorID         string
	SessionID       string
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	LastHost        string
	ParentActorID   *string
	ParentSessionID *string
	Role            *string
	IssueID         *int64
	Location        *string
	State           *string
	StateReason     *string
	StateAt         *time.Time
	UpdatedAt       time.Time
}

type sqliteIssueClaim struct {
	ID         int64
	IssueID    int64
	ActorID    string
	SessionID  string
	ClaimedAt  time.Time
	ReleasedAt *time.Time
	Outcome    *string
	Reason     *string
}

type sqliteEvent struct {
	ID        int64
	At        time.Time
	ProjectID *int64
	Kind      string
	Subject   string
	Actor     string
	SessionID string
	Data      string
}

// --- Time, JSON, Hash, and Normalization Helpers ---

func parseRequiredTimestamp(raw string, field string) (time.Time, error) {
	t, ok := parseFlexibleTimestamp(raw)
	if !ok {
		return time.Time{}, fmt.Errorf("parse timestamp %s=%q", field, raw)
	}
	return t, nil
}

func parseOptionalTimestamp(raw sql.NullString, field string) (*time.Time, error) {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil, nil
	}
	t, ok := parseFlexibleTimestamp(raw.String)
	if !ok {
		return nil, fmt.Errorf("parse timestamp %s=%q", field, raw.String)
	}
	return &t, nil
}

func parseFlexibleTimestamp(raw string) (time.Time, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func nullStringPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

func nullInt64Ptr(ni sql.NullInt64) *int64 {
	if !ni.Valid {
		return nil
	}
	v := ni.Int64
	return &v
}

func normalizeJSONObject(raw string, fallback string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback, nil
	}
	if !json.Valid([]byte(trimmed)) {
		return "", fmt.Errorf("invalid JSON value")
	}
	return trimmed, nil
}

func normalizeJSONArray(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "[]", nil
	}
	var arr []any
	if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
		return "", fmt.Errorf("invalid JSON array: %w", err)
	}
	if arr == nil {
		return "[]", nil
	}
	b, err := json.Marshal(arr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func issueIngestSHA256(title, body string, labels, refs []string) string {
	sortedLabels := append([]string(nil), labels...)
	sort.Strings(sortedLabels)
	sortedRefs := append([]string(nil), refs...)
	sort.Strings(sortedRefs)
	return sha256Hex(title + "\n" + body + "\n" + strings.Join(sortedLabels, ",") + "\n" + strings.Join(sortedRefs, ","))
}

// MapMilestoneStatus converts a legacy SQLite milestone status to the
// PostgreSQL three-stage milestone status.
func MapMilestoneStatus(sqliteStatus string) (string, error) {
	switch sqliteStatus {
	case "open", "planned":
		return "planned", nil
	case "active":
		return "active", nil
	case "closed":
		return "closed", nil
	case "deleted":
		return "deleted", nil
	default:
		return "", fmt.Errorf("unknown milestone status %q", sqliteStatus)
	}
}

// MapIssueStatusToEpicStatus maps a parent issue's status to its promoted epic status.
func MapIssueStatusToEpicStatus(issueStatus string) string {
	switch issueStatus {
	case "deleted":
		return "deleted"
	case "done", "cancelled":
		return "closed"
	default:
		return "open"
	}
}

// BuildTopicPaths builds the slash-joined path for every topic id while
// guarding against parent cycles.
func BuildTopicPaths(topics []sqliteTopic) map[int64]string {
	byID := make(map[int64]sqliteTopic, len(topics))
	for _, t := range topics {
		byID[t.ID] = t
	}
	paths := make(map[int64]string, len(topics))
	for _, t := range topics {
		var segs []string
		seen := map[int64]bool{}
		cur := t
		for {
			if seen[cur.ID] {
				break
			}
			seen[cur.ID] = true
			segs = append(segs, cur.Name)
			if cur.ParentID == nil {
				break
			}
			parent, ok := byID[*cur.ParentID]
			if !ok {
				break
			}
			cur = parent
		}
		for i, j := 0, len(segs)-1; i < j; i, j = i+1, j-1 {
			segs[i], segs[j] = segs[j], segs[i]
		}
		paths[t.ID] = strings.Join(segs, "/")
	}
	return paths
}

// NormalizeTopicLabel maps a SQLite topic into a valid issue_label string
// matching model.ValidLabel (^[a-z0-9][a-z0-9._-]{0,31}$).
func NormalizeTopicLabel(topicID int64, topicPath, topicName string, customMap map[string]string) string {
	if customMap != nil {
		if mapped, ok := customMap[topicPath]; ok && model.ValidLabel(mapped) {
			return mapped
		}
		if mapped, ok := customMap[topicName]; ok && model.ValidLabel(mapped) {
			return mapped
		}
	}

	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(topicPath) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case r == '.' || r == '_':
			if b.Len() > 0 {
				b.WriteRune(r)
				prevDash = false
			}
		default:
			if b.Len() > 0 && !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	label := strings.Trim(b.String(), "-._")
	if len(label) > 32 {
		label = strings.TrimRight(label[:32], "-._")
	}
	if model.ValidLabel(label) {
		return label
	}
	return fmt.Sprintf("topic-%d", topicID)
}

var versionMilestoneRE = regexp.MustCompile(`(?i)^(v?\d+\.\d+(?:\.\d+)?(?:[-+._][a-z0-9.-]+)?|release[-/ _]?\d+(?:\.\d+)*)$`)

// IsVersionMilestoneName reports whether name looks like a release/version milestone.
func IsVersionMilestoneName(name string) bool {
	return versionMilestoneRE.MatchString(strings.TrimSpace(name))
}

// --- Topological sort for self-referencing tables ---

type sessionKey struct {
	actorID   string
	sessionID string
}

func sortSessionsTopologically(sessions []sqliteSession) ([]sqliteSession, error) {
	if len(sessions) == 0 {
		return sessions, nil
	}
	byKey := make(map[sessionKey]sqliteSession, len(sessions))
	keys := make([]sessionKey, 0, len(sessions))
	for _, s := range sessions {
		k := sessionKey{actorID: s.ActorID, sessionID: s.SessionID}
		byKey[k] = s
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].actorID != keys[j].actorID {
			return keys[i].actorID < keys[j].actorID
		}
		return keys[i].sessionID < keys[j].sessionID
	})

	state := make(map[sessionKey]int, len(sessions)) // 0=unvisited, 1=visiting, 2=visited
	ordered := make([]sqliteSession, 0, len(sessions))

	var visit func(k sessionKey) error
	visit = func(k sessionKey) error {
		switch state[k] {
		case 2:
			return nil
		case 1:
			return fmt.Errorf("cycle detected in session parent chain at (%s, %s)", k.actorID, k.sessionID)
		}
		state[k] = 1
		s := byKey[k]
		if s.ParentActorID != nil && s.ParentSessionID != nil {
			pk := sessionKey{actorID: *s.ParentActorID, sessionID: *s.ParentSessionID}
			if _, ok := byKey[pk]; ok {
				if err := visit(pk); err != nil {
					return err
				}
			}
		}
		state[k] = 2
		ordered = append(ordered, s)
		return nil
	}

	for _, k := range keys {
		if err := visit(k); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func sortMessagesTopologically(messages []sqliteMessage) ([]sqliteMessage, error) {
	if len(messages) == 0 {
		return messages, nil
	}
	byID := make(map[int64]sqliteMessage, len(messages))
	ids := make([]int64, 0, len(messages))
	for _, m := range messages {
		byID[m.ID] = m
		ids = append(ids, m.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	state := make(map[int64]int, len(messages))
	ordered := make([]sqliteMessage, 0, len(messages))

	var visit func(id int64) error
	visit = func(id int64) error {
		switch state[id] {
		case 2:
			return nil
		case 1:
			return fmt.Errorf("cycle detected in message reply_to_id chain at id=%d", id)
		}
		state[id] = 1
		m := byID[id]
		if m.ReplyToID != nil {
			if _, ok := byID[*m.ReplyToID]; ok {
				if err := visit(*m.ReplyToID); err != nil {
					return err
				}
			}
		}
		state[id] = 2
		ordered = append(ordered, m)
		return nil
	}

	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

// --- SQLite data readers ---

func readSQLiteActors(ctx context.Context, db *sql.DB) ([]sqliteActor, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, kind, created_at FROM actor ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite actor: %w", err)
	}
	defer rows.Close()
	var out []sqliteActor
	for rows.Next() {
		var a sqliteActor
		var createdRaw string
		if err := rows.Scan(&a.ID, &a.Kind, &createdRaw); err != nil {
			return nil, err
		}
		t, err := parseRequiredTimestamp(createdRaw, "actor.created_at")
		if err != nil {
			return nil, err
		}
		a.CreatedAt = t
		out = append(out, a)
	}
	return out, rows.Err()
}

func readSQLiteProjects(ctx context.Context, db *sql.DB) ([]sqliteProject, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, key, name, status, next_number, created_at, updated_at, created_by, updated_by
		 FROM project ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite project: %w", err)
	}
	defer rows.Close()
	var out []sqliteProject
	for rows.Next() {
		var p sqliteProject
		var createdRaw, updatedRaw string
		if err := rows.Scan(&p.ID, &p.Key, &p.Name, &p.Status, &p.NextNumber, &createdRaw, &updatedRaw, &p.CreatedBy, &p.UpdatedBy); err != nil {
			return nil, err
		}
		var err error
		if p.CreatedAt, err = parseRequiredTimestamp(createdRaw, "project.created_at"); err != nil {
			return nil, err
		}
		if p.UpdatedAt, err = parseRequiredTimestamp(updatedRaw, "project.updated_at"); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func readSQLiteTokens(ctx context.Context, db *sql.DB) ([]sqliteToken, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, actor_id, client_host, token_hash, created_at, created_by,
		        last_used_at, revoked_at, revoked_by, revoked_session, revoked_host
		 FROM token ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite token: %w", err)
	}
	defer rows.Close()
	var out []sqliteToken
	for rows.Next() {
		var t sqliteToken
		var createdRaw string
		var lastUsed, revokedAt, revokedBy, revokedSession, revokedHost sql.NullString
		if err := rows.Scan(&t.ID, &t.ActorID, &t.ClientHost, &t.TokenHash, &createdRaw, &t.CreatedBy,
			&lastUsed, &revokedAt, &revokedBy, &revokedSession, &revokedHost); err != nil {
			return nil, err
		}
		var err error
		if t.CreatedAt, err = parseRequiredTimestamp(createdRaw, "token.created_at"); err != nil {
			return nil, err
		}
		if t.LastUsedAt, err = parseOptionalTimestamp(lastUsed, "token.last_used_at"); err != nil {
			return nil, err
		}
		if t.RevokedAt, err = parseOptionalTimestamp(revokedAt, "token.revoked_at"); err != nil {
			return nil, err
		}
		t.RevokedBy = nullStringPtr(revokedBy)
		t.RevokedSession = nullStringPtr(revokedSession)
		t.RevokedHost = nullStringPtr(revokedHost)
		out = append(out, t)
	}
	return out, rows.Err()
}

func readSQLiteMilestones(ctx context.Context, db *sql.DB) ([]sqliteMilestone, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, name, description, target_date, status, closed_at,
		        created_at, updated_at, created_by, updated_by
		 FROM milestone ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite milestone: %w", err)
	}
	defer rows.Close()
	var out []sqliteMilestone
	for rows.Next() {
		var m sqliteMilestone
		var targetDate, closedAt sql.NullString
		var createdRaw, updatedRaw string
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.Name, &m.Description, &targetDate, &m.Status, &closedAt,
			&createdRaw, &updatedRaw, &m.CreatedBy, &m.UpdatedBy); err != nil {
			return nil, err
		}
		m.TargetDate = nullStringPtr(targetDate)
		var err error
		if m.ClosedAt, err = parseOptionalTimestamp(closedAt, "milestone.closed_at"); err != nil {
			return nil, err
		}
		if m.CreatedAt, err = parseRequiredTimestamp(createdRaw, "milestone.created_at"); err != nil {
			return nil, err
		}
		if m.UpdatedAt, err = parseRequiredTimestamp(updatedRaw, "milestone.updated_at"); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func readSQLiteTopics(ctx context.Context, db *sql.DB) ([]sqliteTopic, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, project_id, name, parent_id, status FROM topic ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite topic: %w", err)
	}
	defer rows.Close()
	var out []sqliteTopic
	for rows.Next() {
		var t sqliteTopic
		var parentID sql.NullInt64
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Name, &parentID, &t.Status); err != nil {
			return nil, err
		}
		t.ParentID = nullInt64Ptr(parentID)
		out = append(out, t)
	}
	return out, rows.Err()
}

func readSQLiteIssues(ctx context.Context, db *sql.DB) ([]sqliteIssue, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, number, key, title, body, type, status, priority,
		        parent_id, recheck_condition, decision_needed, topic_id, milestone_id, status_reason,
		        verification_state, verification_note, verified_at, verified_by,
		        version, created_at, updated_at, closed_at, status_changed_at, status_changed_by,
		        created_by, updated_by
		 FROM issue ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite issue: %w", err)
	}
	defer rows.Close()
	var out []sqliteIssue
	for rows.Next() {
		var i sqliteIssue
		var parentID, topicID, milestoneID sql.NullInt64
		var recheck, decision, statusReason, verNote, verifiedAt, verifiedBy, closedAt sql.NullString
		var createdRaw, updatedRaw, statusChangedRaw string
		if err := rows.Scan(
			&i.ID, &i.ProjectID, &i.Number, &i.Key, &i.Title, &i.Body, &i.Type, &i.Status, &i.Priority,
			&parentID, &recheck, &decision, &topicID, &milestoneID, &statusReason,
			&i.VerificationState, &verNote, &verifiedAt, &verifiedBy,
			&i.Version, &createdRaw, &updatedRaw, &closedAt, &statusChangedRaw, &i.StatusChangedBy,
			&i.CreatedBy, &i.UpdatedBy,
		); err != nil {
			return nil, err
		}
		i.ParentID = nullInt64Ptr(parentID)
		i.TopicID = nullInt64Ptr(topicID)
		i.MilestoneID = nullInt64Ptr(milestoneID)
		i.RecheckCondition = nullStringPtr(recheck)
		i.DecisionNeeded = nullStringPtr(decision)
		i.StatusReason = nullStringPtr(statusReason)
		i.VerificationNote = nullStringPtr(verNote)
		i.VerifiedBy = nullStringPtr(verifiedBy)
		var err error
		if i.VerifiedAt, err = parseOptionalTimestamp(verifiedAt, "issue.verified_at"); err != nil {
			return nil, err
		}
		if i.CreatedAt, err = parseRequiredTimestamp(createdRaw, "issue.created_at"); err != nil {
			return nil, err
		}
		if i.UpdatedAt, err = parseRequiredTimestamp(updatedRaw, "issue.updated_at"); err != nil {
			return nil, err
		}
		if i.ClosedAt, err = parseOptionalTimestamp(closedAt, "issue.closed_at"); err != nil {
			return nil, err
		}
		if i.StatusChangedAt, err = parseRequiredTimestamp(statusChangedRaw, "issue.status_changed_at"); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func readSQLiteIssueLabels(ctx context.Context, db *sql.DB) ([]sqliteIssueLabel, error) {
	rows, err := db.QueryContext(ctx, `SELECT issue_id, label FROM issue_label ORDER BY issue_id, label`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite issue_label: %w", err)
	}
	defer rows.Close()
	var out []sqliteIssueLabel
	for rows.Next() {
		var r sqliteIssueLabel
		if err := rows.Scan(&r.IssueID, &r.Label); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func readSQLiteIssueDependencies(ctx context.Context, db *sql.DB) ([]sqliteIssueDependency, error) {
	rows, err := db.QueryContext(ctx, `SELECT issue_id, depends_on_id FROM issue_dependency ORDER BY issue_id, depends_on_id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite issue_dependency: %w", err)
	}
	defer rows.Close()
	var out []sqliteIssueDependency
	for rows.Next() {
		var r sqliteIssueDependency
		if err := rows.Scan(&r.IssueID, &r.DependsOnID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func readSQLiteIssueRefs(ctx context.Context, db *sql.DB) ([]sqliteIssueRef, error) {
	rows, err := db.QueryContext(ctx, `SELECT issue_id, ref, target_issue_id, target_document_id FROM issue_ref ORDER BY issue_id, ref`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite issue_ref: %w", err)
	}
	defer rows.Close()
	var out []sqliteIssueRef
	for rows.Next() {
		var r sqliteIssueRef
		var targetIssue, targetDoc sql.NullInt64
		if err := rows.Scan(&r.IssueID, &r.Ref, &targetIssue, &targetDoc); err != nil {
			return nil, err
		}
		r.TargetIssueID = nullInt64Ptr(targetIssue)
		r.TargetDocumentID = nullInt64Ptr(targetDoc)
		out = append(out, r)
	}
	return out, rows.Err()
}

func readSQLiteActivities(ctx context.Context, db *sql.DB) ([]sqliteActivity, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, issue_id, kind, actor_id, session_id, client_host, at, changes, body, summary, comment_kind
		 FROM activity ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite activity: %w", err)
	}
	defer rows.Close()
	var out []sqliteActivity
	for rows.Next() {
		var a sqliteActivity
		var atRaw string
		var changes, body, summary, commentKind sql.NullString
		if err := rows.Scan(&a.ID, &a.IssueID, &a.Kind, &a.ActorID, &a.SessionID, &a.ClientHost,
			&atRaw, &changes, &body, &summary, &commentKind); err != nil {
			return nil, err
		}
		var err error
		if a.At, err = parseRequiredTimestamp(atRaw, "activity.at"); err != nil {
			return nil, err
		}
		a.Changes = nullStringPtr(changes)
		a.Body = nullStringPtr(body)
		a.Summary = nullStringPtr(summary)
		a.CommentKind = nullStringPtr(commentKind)
		out = append(out, a)
	}
	return out, rows.Err()
}

func readSQLiteDocuments(ctx context.Context, db *sql.DB) ([]sqliteDocument, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, name, kind, date, title, tags, archived, imported, path,
		        sha256, size, content, created_at, created_by, updated_at, updated_by
		 FROM document ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite document: %w", err)
	}
	defer rows.Close()
	var out []sqliteDocument
	for rows.Next() {
		var d sqliteDocument
		var date, title, path sql.NullString
		var archived, imported int
		var createdRaw, updatedRaw string
		if err := rows.Scan(
			&d.ID, &d.ProjectID, &d.Name, &d.Kind, &date, &title, &d.Tags,
			&archived, &imported, &path, &d.SHA256, &d.SizeBytes, &d.Content,
			&createdRaw, &d.CreatedBy, &updatedRaw, &d.UpdatedBy,
		); err != nil {
			return nil, err
		}
		d.Date = nullStringPtr(date)
		if title.Valid {
			d.Title = title.String
		}
		d.Archived = archived != 0
		d.Imported = imported != 0
		d.SourcePath = nullStringPtr(path)
		var err error
		if d.CreatedAt, err = parseRequiredTimestamp(createdRaw, "document.created_at"); err != nil {
			return nil, err
		}
		if d.UpdatedAt, err = parseRequiredTimestamp(updatedRaw, "document.updated_at"); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func readSQLiteDocumentRelations(ctx context.Context, db *sql.DB) ([]sqliteDocumentRelation, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, document_id, kind, target_document_id, target_issue_id, target_text, removed_at, removed_by
		 FROM document_relation ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite document_relation: %w", err)
	}
	defer rows.Close()
	var out []sqliteDocumentRelation
	for rows.Next() {
		var r sqliteDocumentRelation
		var targetDoc, targetIssue sql.NullInt64
		var targetText, removedAt, removedBy sql.NullString
		if err := rows.Scan(&r.ID, &r.DocumentID, &r.Kind, &targetDoc, &targetIssue, &targetText, &removedAt, &removedBy); err != nil {
			return nil, err
		}
		r.TargetDocumentID = nullInt64Ptr(targetDoc)
		r.TargetIssueID = nullInt64Ptr(targetIssue)
		r.TargetText = nullStringPtr(targetText)
		var err error
		if r.RemovedAt, err = parseOptionalTimestamp(removedAt, "document_relation.removed_at"); err != nil {
			return nil, err
		}
		r.RemovedBy = nullStringPtr(removedBy)
		out = append(out, r)
	}
	return out, rows.Err()
}

func readSQLiteDocumentActivities(ctx context.Context, db *sql.DB) ([]sqliteDocumentActivity, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, document_id, actor_id, session_id, client_host, at, changes
		 FROM document_activity ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite document_activity: %w", err)
	}
	defer rows.Close()
	var out []sqliteDocumentActivity
	for rows.Next() {
		var a sqliteDocumentActivity
		var atRaw string
		if err := rows.Scan(&a.ID, &a.DocumentID, &a.ActorID, &a.SessionID, &a.ClientHost, &atRaw, &a.Changes); err != nil {
			return nil, err
		}
		var err error
		if a.At, err = parseRequiredTimestamp(atRaw, "document_activity.at"); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func readSQLiteResourceActivities(ctx context.Context, db *sql.DB) ([]sqliteResourceActivity, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, resource, resource_id, kind, actor_id, session_id, client_host, at, changes
		 FROM resource_activity ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite resource_activity: %w", err)
	}
	defer rows.Close()
	var out []sqliteResourceActivity
	for rows.Next() {
		var a sqliteResourceActivity
		var atRaw string
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.Resource, &a.ResourceID, &a.Kind,
			&a.ActorID, &a.SessionID, &a.ClientHost, &atRaw, &a.Changes); err != nil {
			return nil, err
		}
		var err error
		if a.At, err = parseRequiredTimestamp(atRaw, "resource_activity.at"); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func readSQLiteMessages(ctx context.Context, db *sql.DB) ([]sqliteMessage, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, to_label, to_actor, to_session_id, to_role, issue_id, reply_to_id,
		        title, body, created_at, created_by, created_session, created_host,
		        expires_at, deleted_at, deleted_by
		 FROM message ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite message: %w", err)
	}
	defer rows.Close()
	var out []sqliteMessage
	for rows.Next() {
		var m sqliteMessage
		var toLabel, toActor, toSession, toRole, deletedAt, deletedBy sql.NullString
		var issueID, replyToID sql.NullInt64
		var createdRaw, expiresRaw string
		if err := rows.Scan(
			&m.ID, &m.ProjectID, &toLabel, &toActor, &toSession, &toRole, &issueID, &replyToID,
			&m.Title, &m.Body, &createdRaw, &m.CreatedBy, &m.CreatedSession, &m.CreatedHost,
			&expiresRaw, &deletedAt, &deletedBy,
		); err != nil {
			return nil, err
		}
		m.ToLabel = nullStringPtr(toLabel)
		m.ToActor = nullStringPtr(toActor)
		m.ToSessionID = nullStringPtr(toSession)
		m.ToRole = nullStringPtr(toRole)
		m.IssueID = nullInt64Ptr(issueID)
		m.ReplyToID = nullInt64Ptr(replyToID)
		m.DeletedBy = nullStringPtr(deletedBy)
		var err error
		if m.CreatedAt, err = parseRequiredTimestamp(createdRaw, "message.created_at"); err != nil {
			return nil, err
		}
		if m.ExpiresAt, err = parseRequiredTimestamp(expiresRaw, "message.expires_at"); err != nil {
			return nil, err
		}
		if m.DeletedAt, err = parseOptionalTimestamp(deletedAt, "message.deleted_at"); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func readSQLiteMessageReads(ctx context.Context, db *sql.DB) ([]sqliteMessageRead, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT message_id, reader, read_at, read_by, read_session, read_host
		 FROM message_read ORDER BY message_id, reader`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite message_read: %w", err)
	}
	defer rows.Close()
	var out []sqliteMessageRead
	for rows.Next() {
		var r sqliteMessageRead
		var readAtRaw string
		if err := rows.Scan(&r.MessageID, &r.Reader, &readAtRaw, &r.ReadBy, &r.ReadSession, &r.ReadHost); err != nil {
			return nil, err
		}
		var err error
		if r.ReadAt, err = parseRequiredTimestamp(readAtRaw, "message_read.read_at"); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func readSQLiteSessions(ctx context.Context, db *sql.DB) ([]sqliteSession, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT actor_id, session_id, first_seen_at, last_seen_at, last_host,
		        parent_actor_id, parent_session_id, role, issue_id, location,
		        state, state_reason, state_at, updated_at
		 FROM session ORDER BY actor_id, session_id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite session: %w", err)
	}
	defer rows.Close()
	var out []sqliteSession
	for rows.Next() {
		var s sqliteSession
		var firstRaw, lastRaw, updatedRaw string
		var parentActor, parentSession, role, location, state, stateReason, stateAt sql.NullString
		var issueID sql.NullInt64
		if err := rows.Scan(
			&s.ActorID, &s.SessionID, &firstRaw, &lastRaw, &s.LastHost,
			&parentActor, &parentSession, &role, &issueID, &location,
			&state, &stateReason, &stateAt, &updatedRaw,
		); err != nil {
			return nil, err
		}
		s.ParentActorID = nullStringPtr(parentActor)
		s.ParentSessionID = nullStringPtr(parentSession)
		s.Role = nullStringPtr(role)
		s.IssueID = nullInt64Ptr(issueID)
		s.Location = nullStringPtr(location)
		s.State = nullStringPtr(state)
		s.StateReason = nullStringPtr(stateReason)
		var err error
		if s.FirstSeenAt, err = parseRequiredTimestamp(firstRaw, "session.first_seen_at"); err != nil {
			return nil, err
		}
		if s.LastSeenAt, err = parseRequiredTimestamp(lastRaw, "session.last_seen_at"); err != nil {
			return nil, err
		}
		if s.StateAt, err = parseOptionalTimestamp(stateAt, "session.state_at"); err != nil {
			return nil, err
		}
		if s.UpdatedAt, err = parseRequiredTimestamp(updatedRaw, "session.updated_at"); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func readSQLiteIssueClaims(ctx context.Context, db *sql.DB) ([]sqliteIssueClaim, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, issue_id, actor_id, session_id, claimed_at, released_at, outcome, reason
		 FROM issue_claim ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite issue_claim: %w", err)
	}
	defer rows.Close()
	var out []sqliteIssueClaim
	for rows.Next() {
		var c sqliteIssueClaim
		var claimedRaw string
		var releasedAt, outcome, reason sql.NullString
		if err := rows.Scan(&c.ID, &c.IssueID, &c.ActorID, &c.SessionID, &claimedRaw, &releasedAt, &outcome, &reason); err != nil {
			return nil, err
		}
		var err error
		if c.ClaimedAt, err = parseRequiredTimestamp(claimedRaw, "issue_claim.claimed_at"); err != nil {
			return nil, err
		}
		if c.ReleasedAt, err = parseOptionalTimestamp(releasedAt, "issue_claim.released_at"); err != nil {
			return nil, err
		}
		c.Outcome = nullStringPtr(outcome)
		c.Reason = nullStringPtr(reason)
		out = append(out, c)
	}
	return out, rows.Err()
}

func readSQLiteEvents(ctx context.Context, db *sql.DB) ([]sqliteEvent, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, at, project_id, kind, subject, actor, session_id, data
		 FROM event ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read sqlite event: %w", err)
	}
	defer rows.Close()
	var out []sqliteEvent
	for rows.Next() {
		var e sqliteEvent
		var atRaw string
		var projID sql.NullInt64
		if err := rows.Scan(&e.ID, &atRaw, &projID, &e.Kind, &e.Subject, &e.Actor, &e.SessionID, &e.Data); err != nil {
			return nil, err
		}
		var err error
		if e.At, err = parseRequiredTimestamp(atRaw, "event.at"); err != nil {
			return nil, err
		}
		e.ProjectID = nullInt64Ptr(projID)
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- Document content validation ---

func checkDocumentContent(content string) []string {
	var reasons []string
	if len(content) > model.MaxDocumentBytes {
		reasons = append(reasons, "exceeds_2mib")
	}
	if !utf8.ValidString(content) {
		reasons = append(reasons, "invalid_utf8")
	}
	if strings.IndexByte(content, 0) >= 0 {
		reasons = append(reasons, "contains_nul_byte")
	}
	return reasons
}

// --- DSN Sanitization ---

var (
	dsnUserinfoRE  = regexp.MustCompile(`(?i)(postgres(?:ql)?://)([^@\s/]+)@`)
	dsnQueryPassRE = regexp.MustCompile(`(?i)([?&]password=)([^&\s'"]+)`)
	dsnKVPassRE    = regexp.MustCompile(`(?i)((?:^|[\s,(])password\s*=\s*)('(?:\\'|[^'])*'|"(?:\\"|[^"])*"|[^\s&,'")]+)`)
)

// RedactDSN strips userinfo and password tokens from any string that may contain
// a PostgreSQL DSN so error messages never leak credentials.
func RedactDSN(msg, rawDSN string) string {
	out := msg
	if rawDSN != "" {
		out = strings.ReplaceAll(out, rawDSN, "<redacted-dsn>")
		// Extract the exact raw userinfo segment between "://" and "@" to cover
		// non-canonical percent-encoding (e.g. unescaped "!" alongside "%40").
		if schemeIdx := strings.Index(rawDSN, "://"); schemeIdx >= 0 {
			rest := rawDSN[schemeIdx+3:]
			if slashIdx := strings.IndexAny(rest, "/?"); slashIdx >= 0 {
				rest = rest[:slashIdx]
			}
			if atIdx := strings.LastIndex(rest, "@"); atIdx >= 0 {
				rawUserinfo := rest[:atIdx]
				if rawUserinfo != "" {
					out = strings.ReplaceAll(out, rawUserinfo, "***")
					if colonIdx := strings.IndexByte(rawUserinfo, ':'); colonIdx >= 0 {
						if rawPW := rawUserinfo[colonIdx+1:]; rawPW != "" {
							out = strings.ReplaceAll(out, rawPW, "***")
						}
					}
				}
			}
		}
		if u, err := url.Parse(rawDSN); err == nil {
			if u.User != nil {
				if userinfo := u.User.String(); userinfo != "" {
					out = strings.ReplaceAll(out, userinfo, "***")
				}
				if pw, hasPW := u.User.Password(); hasPW && pw != "" {
					out = strings.ReplaceAll(out, pw, "***")
					if enc := url.QueryEscape(pw); enc != "" && enc != pw {
						out = strings.ReplaceAll(out, enc, "***")
					}
				}
			}
			for key, vals := range u.Query() {
				if strings.EqualFold(key, "password") {
					for _, pw := range vals {
						if pw == "" {
							continue
						}
						out = strings.ReplaceAll(out, pw, "***")
						if enc := url.QueryEscape(pw); enc != "" && enc != pw {
							out = strings.ReplaceAll(out, enc, "***")
						}
					}
				}
			}
		}
	}
	out = dsnUserinfoRE.ReplaceAllString(out, "${1}***@")
	out = dsnQueryPassRE.ReplaceAllString(out, "${1}***")
	out = dsnKVPassRE.ReplaceAllString(out, "${1}***")
	return out
}

// SanitizeError returns an error with any DSN credentials redacted.
func SanitizeError(err error, rawDSN string) error {
	if err == nil {
		return nil
	}
	return errors.New(RedactDSN(err.Error(), rawDSN))
}
