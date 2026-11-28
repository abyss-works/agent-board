package migrate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store/pg"
)

// ErrTargetNotEmpty is returned when the target PostgreSQL database already
// contains domain rows beyond the seeded GLOBAL scope.
var ErrTargetNotEmpty = errors.New("target PostgreSQL database is not empty")

// Options configures a SQLite to PostgreSQL migration run.
type Options struct {
	// SourcePath is the path to the source SQLite database file (opened read-only).
	SourcePath string
	// TargetDSN is the PostgreSQL connection string used when Pool is nil.
	TargetDSN string
	// Pool is an optional pre-opened PostgreSQL pool (schema migrations must already be applied).
	Pool *pgxpool.Pool
	// EnqueueBackfill controls whether ingest_job rows are enqueued for migrated
	// issues, comment activities, and record documents. Defaults to true when nil.
	EnqueueBackfill *bool
	// TopicLabelMap optionally overrides normalized label names for specific topic
	// paths or names.
	TopicLabelMap map[string]string
}

func (o Options) shouldEnqueueBackfill() bool {
	if o.EnqueueBackfill == nil {
		return true
	}
	return *o.EnqueueBackfill
}

// RunResult summarizes a completed SQLite to PostgreSQL migration.
type RunResult struct {
	SourcePath                   string           `json:"source_path"`
	CopiedTables                 map[string]int64 `json:"copied_tables"`
	SkippedDerivedTables         map[string]int64 `json:"skipped_derived_tables"`
	PromotedEpics                int              `json:"promoted_epics"`
	AssignedChildIssues          int              `json:"assigned_child_issues"`
	MergedTopicLabels            int              `json:"merged_topic_labels"`
	SkippedTopicResourceActivity int              `json:"skipped_topic_resource_activity"`
	ResolvedDuplicateClaims      int              `json:"resolved_duplicate_claims"`
	EnqueuedBackfillJobs         int              `json:"enqueued_backfill_jobs"`
	SyncedSequences              []string         `json:"synced_sequences"`
}

// Run performs an atomic, one-to-one lossless migration from SQLite into PostgreSQL 17.
// The source SQLite file is opened strictly read-only and is never modified.
func Run(ctx context.Context, opts Options) (*RunResult, error) {
	if strings.TrimSpace(opts.SourcePath) == "" {
		return nil, errors.New("source SQLite path is required")
	}

	pool := opts.Pool
	var ownedStore *pg.Store
	if pool == nil {
		if strings.TrimSpace(opts.TargetDSN) == "" {
			return nil, errors.New("target PostgreSQL DSN is required")
		}
		st, err := pg.Open(ctx, opts.TargetDSN, 8)
		if err != nil {
			return nil, SanitizeError(fmt.Errorf("open target PostgreSQL: %w", err), opts.TargetDSN)
		}
		ownedStore = st
		pool = st.Pool()
		defer func() { _ = ownedStore.Close() }()
	}

	db, _, err := openSQLiteReadOnly(ctx, opts.SourcePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	// Read all source data from SQLite before opening the PG write transaction.
	actors, err := readSQLiteActors(ctx, db)
	if err != nil {
		return nil, err
	}
	projects, err := readSQLiteProjects(ctx, db)
	if err != nil {
		return nil, err
	}
	tokens, err := readSQLiteTokens(ctx, db)
	if err != nil {
		return nil, err
	}
	milestones, err := readSQLiteMilestones(ctx, db)
	if err != nil {
		return nil, err
	}
	topics, err := readSQLiteTopics(ctx, db)
	if err != nil {
		return nil, err
	}
	issues, err := readSQLiteIssues(ctx, db)
	if err != nil {
		return nil, err
	}
	issueLabels, err := readSQLiteIssueLabels(ctx, db)
	if err != nil {
		return nil, err
	}
	issueDeps, err := readSQLiteIssueDependencies(ctx, db)
	if err != nil {
		return nil, err
	}
	issueRefs, err := readSQLiteIssueRefs(ctx, db)
	if err != nil {
		return nil, err
	}
	activities, err := readSQLiteActivities(ctx, db)
	if err != nil {
		return nil, err
	}
	documents, err := readSQLiteDocuments(ctx, db)
	if err != nil {
		return nil, err
	}
	docRels, err := readSQLiteDocumentRelations(ctx, db)
	if err != nil {
		return nil, err
	}
	docActs, err := readSQLiteDocumentActivities(ctx, db)
	if err != nil {
		return nil, err
	}
	resActs, err := readSQLiteResourceActivities(ctx, db)
	if err != nil {
		return nil, err
	}
	messages, err := readSQLiteMessages(ctx, db)
	if err != nil {
		return nil, err
	}
	msgReads, err := readSQLiteMessageReads(ctx, db)
	if err != nil {
		return nil, err
	}
	sessions, err := readSQLiteSessions(ctx, db)
	if err != nil {
		return nil, err
	}
	claims, err := readSQLiteIssueClaims(ctx, db)
	if err != nil {
		return nil, err
	}
	events, err := readSQLiteEvents(ctx, db)
	if err != nil {
		return nil, err
	}

	derivedCounts := map[string]int64{}
	for _, spec := range SQLiteTables {
		if spec.Action == ActionDiscard {
			cnt, err := sqliteTableCount(ctx, db, spec.Name)
			if err != nil {
				return nil, err
			}
			derivedCounts[spec.Name] = cnt
		}
	}

	// Pre-flight validation on blocking invariants.
	projectByID := make(map[int64]sqliteProject, len(projects))
	for _, p := range projects {
		if p.ID == model.GlobalProjectID || strings.EqualFold(p.Key, model.GlobalProjectKey) {
			return nil, fmt.Errorf("SQLite project id=%d key=%q conflicts with reserved GLOBAL scope (id=0)", p.ID, p.Key)
		}
		projectByID[p.ID] = p
	}
	for _, d := range documents {
		if reasons := checkDocumentContent(d.Content); len(reasons) > 0 {
			return nil, fmt.Errorf("document id=%d (%s) cannot be migrated: %s", d.ID, d.Name, strings.Join(reasons, ", "))
		}
	}

	sortedSessions, err := sortSessionsTopologically(sessions)
	if err != nil {
		return nil, err
	}
	sortedMessages, err := sortMessagesTopologically(messages)
	if err != nil {
		return nil, err
	}

	// Build actor lookup and ensure any actor referenced by issue_claim exists.
	actorSet := make(map[string]bool, len(actors)+1)
	for _, a := range actors {
		actorSet[a.ID] = true
	}
	for _, c := range claims {
		if !actorSet[c.ActorID] {
			actorSet[c.ActorID] = true
			actors = append(actors, sqliteActor{
				ID:        c.ActorID,
				Kind:      string(model.ActorAgent),
				CreatedAt: c.ClaimedAt,
			})
		}
	}

	// Defensively resolve duplicate active claims (keep highest claim ID open).
	resolvedDupClaims := 0
	openClaimIdxByIssue := map[int64][]int{}
	for idx, c := range claims {
		if c.ReleasedAt == nil {
			openClaimIdxByIssue[c.IssueID] = append(openClaimIdxByIssue[c.IssueID], idx)
		}
	}
	for _, idxs := range openClaimIdxByIssue {
		if len(idxs) <= 1 {
			continue
		}
		sort.Slice(idxs, func(i, j int) bool { return claims[idxs[i]].ID < claims[idxs[j]].ID })
		latest := claims[idxs[len(idxs)-1]]
		outcome := "taken_over"
		reason := "resolved duplicate open claim during migration"
		for _, oldIdx := range idxs[:len(idxs)-1] {
			ts := latest.ClaimedAt
			claims[oldIdx].ReleasedAt = &ts
			claims[oldIdx].Outcome = &outcome
			claims[oldIdx].Reason = &reason
			resolvedDupClaims++
		}
	}

	// Begin atomic PostgreSQL transaction.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, SanitizeError(fmt.Errorf("begin PostgreSQL transaction: %w", err), opts.TargetDSN)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize migration runs against the same target database.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('agb_sqlite_migration'))`); err != nil {
		return nil, fmt.Errorf("acquire migration advisory lock: %w", err)
	}

	// Ensure target database has no pre-existing migrated data.
	if err := ensureTargetEmptyTx(ctx, tx); err != nil {
		return nil, err
	}

	// Ensure system actor and GLOBAL project (id=0) seed records remain intact.
	if _, err := tx.Exec(ctx,
		`INSERT INTO actor (id, kind) VALUES ('system', 'agent') ON CONFLICT (id) DO NOTHING`); err != nil {
		return nil, fmt.Errorf("ensure system actor: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO project (id, key, name, scope_kind, status, next_number, created_by, updated_by)
		 VALUES (0, 'GLOBAL', 'Global Scope', 'global', 'active', 1, 'system', 'system')
		 ON CONFLICT (id) DO NOTHING`); err != nil {
		return nil, fmt.Errorf("ensure GLOBAL project seed: %w", err)
	}

	copied := map[string]int64{}

	// 1. actor
	for _, a := range actors {
		if _, err := tx.Exec(ctx,
			`INSERT INTO actor (id, kind, created_at)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (id) DO NOTHING`,
			a.ID, a.Kind, a.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("insert actor %q: %w", a.ID, err)
		}
	}
	copied["actor"] = int64(len(actors))

	// 2. project (id >= 1, scope_kind = 'project')
	for _, p := range projects {
		if _, err := tx.Exec(ctx,
			`INSERT INTO project (id, key, name, scope_kind, status, next_number, created_at, updated_at, created_by, updated_by)
			 VALUES ($1, $2, $3, 'project', $4, $5, $6, $7, $8, $9)`,
			p.ID, p.Key, p.Name, p.Status, p.NextNumber, p.CreatedAt, p.UpdatedAt, p.CreatedBy, p.UpdatedBy,
		); err != nil {
			return nil, fmt.Errorf("insert project id=%d key=%q: %w", p.ID, p.Key, err)
		}
	}
	copied["project"] = int64(len(projects))

	// 3. token
	for _, t := range tokens {
		if _, err := tx.Exec(ctx,
			`INSERT INTO token (id, actor_id, client_host, token_hash, created_at, created_by,
			                    last_used_at, revoked_at, revoked_by, revoked_session, revoked_host)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			t.ID, t.ActorID, t.ClientHost, t.TokenHash, t.CreatedAt, t.CreatedBy,
			t.LastUsedAt, t.RevokedAt, t.RevokedBy, t.RevokedSession, t.RevokedHost,
		); err != nil {
			return nil, fmt.Errorf("insert token id=%d: %w", t.ID, err)
		}
	}
	copied["token"] = int64(len(tokens))

	// 4. milestone (open -> planned, target_date dropped)
	for _, m := range milestones {
		status, err := MapMilestoneStatus(m.Status)
		if err != nil {
			return nil, fmt.Errorf("milestone id=%d: %w", m.ID, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO milestone (id, project_id, name, description, status, closed_at,
			                        created_at, updated_at, created_by, updated_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			m.ID, m.ProjectID, m.Name, m.Description, status, m.ClosedAt,
			m.CreatedAt, m.UpdatedAt, m.CreatedBy, m.UpdatedBy,
		); err != nil {
			return nil, fmt.Errorf("insert milestone id=%d: %w", m.ID, err)
		}
	}
	copied["milestone"] = int64(len(milestones))

	// 5. issue (1:1 preservation of ALL rows, initially epic_id = NULL)
	issueByID := make(map[int64]sqliteIssue, len(issues))
	childrenByParent := map[int64][]sqliteIssue{}
	for _, i := range issues {
		issueByID[i.ID] = i
		if i.ParentID != nil {
			childrenByParent[*i.ParentID] = append(childrenByParent[*i.ParentID], i)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue (
			     id, project_id, number, key, epic_id, milestone_id, title, body, type, status, priority,
			     recheck_condition, decision_needed, status_reason,
			     verification_state, verification_note, verified_at, verified_by,
			     version, created_at, updated_at, closed_at, status_changed_at, status_changed_by,
			     created_by, updated_by
			 ) VALUES (
			     $1, $2, $3, $4, NULL, $5, $6, $7, $8, $9, $10,
			     $11, $12, $13,
			     $14, $15, $16, $17,
			     $18, $19, $20, $21, $22, $23,
			     $24, $25
			 )`,
			i.ID, i.ProjectID, i.Number, i.Key, i.MilestoneID, i.Title, i.Body, i.Type, i.Status, i.Priority,
			i.RecheckCondition, i.DecisionNeeded, i.StatusReason,
			i.VerificationState, i.VerificationNote, i.VerifiedAt, i.VerifiedBy,
			i.Version, i.CreatedAt, i.UpdatedAt, i.ClosedAt, i.StatusChangedAt, i.StatusChangedBy,
			i.CreatedBy, i.UpdatedBy,
		); err != nil {
			return nil, fmt.Errorf("insert issue id=%d key=%q: %w", i.ID, i.Key, err)
		}
	}
	copied["issue"] = int64(len(issues))

	// 6. epic promotion from parent issues + child issue epic_id assignment
	parentIDs := make([]int64, 0, len(childrenByParent))
	for pid := range childrenByParent {
		parentIDs = append(parentIDs, pid)
	}
	sort.Slice(parentIDs, func(i, j int) bool {
		a, b := issueByID[parentIDs[i]], issueByID[parentIDs[j]]
		if a.ProjectID != b.ProjectID {
			return a.ProjectID < b.ProjectID
		}
		if a.Number != b.Number {
			return a.Number < b.Number
		}
		return a.ID < b.ID
	})

	epicIDBySourceIssue := make(map[int64]int64, len(parentIDs))
	epicSeqByProject := map[int64]int{}
	for _, pid := range parentIDs {
		parent, ok := issueByID[pid]
		if !ok {
			return nil, fmt.Errorf("parent issue id=%d not found", pid)
		}
		projKey := projectByID[parent.ProjectID].Key
		epicSeqByProject[parent.ProjectID]++
		epicKey := fmt.Sprintf("%s-EPIC-%d", projKey, epicSeqByProject[parent.ProjectID])
		epicStatus := MapIssueStatusToEpicStatus(parent.Status)

		var epicID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO epic (project_id, key, title, description, status, source_issue_id, source_issue_key,
			                   closed_at, created_at, updated_at, created_by, updated_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			 RETURNING id`,
			parent.ProjectID, epicKey, parent.Title, parent.Body, epicStatus,
			parent.ID, parent.Key, parent.ClosedAt, parent.CreatedAt, parent.UpdatedAt, parent.CreatedBy, parent.UpdatedBy,
		).Scan(&epicID); err != nil {
			return nil, fmt.Errorf("insert promoted epic for issue %q: %w", parent.Key, err)
		}
		epicIDBySourceIssue[parent.ID] = epicID
	}
	copied["epic"] = int64(len(epicIDBySourceIssue))

	assignedChildren := 0
	for _, i := range issues {
		if i.ParentID == nil {
			continue
		}
		epicID, ok := epicIDBySourceIssue[*i.ParentID]
		if !ok {
			return nil, fmt.Errorf("child issue %q references unpromoted parent id=%d", i.Key, *i.ParentID)
		}
		if _, err := tx.Exec(ctx, `UPDATE issue SET epic_id = $1 WHERE id = $2`, epicID, i.ID); err != nil {
			return nil, fmt.Errorf("assign epic_id=%d to issue %q: %w", epicID, i.Key, err)
		}
		assignedChildren++
	}

	// 7. issue_label + topic merge (deduplicated)
	topicPaths := BuildTopicPaths(topics)
	topicLabelByID := make(map[int64]string, len(topics))
	for _, t := range topics {
		topicLabelByID[t.ID] = NormalizeTopicLabel(t.ID, topicPaths[t.ID], t.Name, opts.TopicLabelMap)
	}

	labelsByIssue := make(map[int64]map[string]bool)
	addIssueLabel := func(issueID int64, label string) error {
		if labelsByIssue[issueID] == nil {
			labelsByIssue[issueID] = map[string]bool{}
		}
		if labelsByIssue[issueID][label] {
			return nil
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_label (issue_id, label) VALUES ($1, $2) ON CONFLICT (issue_id, label) DO NOTHING`,
			issueID, label,
		); err != nil {
			return fmt.Errorf("insert issue_label (%d, %q): %w", issueID, label, err)
		}
		labelsByIssue[issueID][label] = true
		return nil
	}

	for _, il := range issueLabels {
		if err := addIssueLabel(il.IssueID, il.Label); err != nil {
			return nil, err
		}
	}
	mergedTopicLabels := 0
	for _, i := range issues {
		if i.TopicID == nil {
			continue
		}
		label, ok := topicLabelByID[*i.TopicID]
		if !ok {
			return nil, fmt.Errorf("issue %q references missing topic id=%d", i.Key, *i.TopicID)
		}
		existed := labelsByIssue[i.ID] != nil && labelsByIssue[i.ID][label]
		if err := addIssueLabel(i.ID, label); err != nil {
			return nil, err
		}
		if !existed {
			mergedTopicLabels++
		}
	}
	totalLabels := int64(0)
	for _, set := range labelsByIssue {
		totalLabels += int64(len(set))
	}
	copied["issue_label"] = totalLabels

	// 8. record_document (before issue_ref so target_record_id FK is satisfied)
	docByID := make(map[int64]sqliteDocument, len(documents))
	for _, d := range documents {
		docByID[d.ID] = d
		var recordedAt *time.Time
		if d.Date != nil && strings.TrimSpace(*d.Date) != "" {
			if t, ok := parseFlexibleTimestamp(*d.Date); ok {
				recordedAt = &t
			}
		}
		tagsJSON, err := normalizeJSONArray(d.Tags)
		if err != nil {
			return nil, fmt.Errorf("document id=%d tags: %w", d.ID, err)
		}
		hash := d.SHA256
		if len(hash) != 64 {
			hash = sha256Hex(d.Content)
		}
		sizeBytes := len(d.Content)
		if _, err := tx.Exec(ctx,
			`INSERT INTO record_document (
			     id, project_id, name, kind, recorded_at, title, tags, archived, imported, source_path,
			     sha256, size_bytes, content, created_at, created_by, updated_at, updated_by
			 ) VALUES (
			     $1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10,
			     $11, $12, $13, $14, $15, $16, $17
			 )`,
			d.ID, d.ProjectID, d.Name, d.Kind, recordedAt, d.Title, tagsJSON, d.Archived, d.Imported, d.SourcePath,
			hash, sizeBytes, d.Content, d.CreatedAt, d.CreatedBy, d.UpdatedAt, d.UpdatedBy,
		); err != nil {
			return nil, fmt.Errorf("insert record_document id=%d name=%q: %w", d.ID, d.Name, err)
		}
	}
	copied["record_document"] = int64(len(documents))

	// 9. issue_ref (target_document_id -> target_record_id)
	refsByIssue := make(map[int64][]string)
	for _, r := range issueRefs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_ref (issue_id, ref, target_issue_id, target_spec_id, target_record_id, target_epic_id)
			 VALUES ($1, $2, $3, NULL, $4, NULL)`,
			r.IssueID, r.Ref, r.TargetIssueID, r.TargetDocumentID,
		); err != nil {
			return nil, fmt.Errorf("insert issue_ref (%d, %q): %w", r.IssueID, r.Ref, err)
		}
		refsByIssue[r.IssueID] = append(refsByIssue[r.IssueID], r.Ref)
	}
	copied["issue_ref"] = int64(len(issueRefs))

	// 10. graph_edge from issue_dependency + document_relation
	var graphEdgeCount int64
	for _, dep := range issueDeps {
		if dep.IssueID == dep.DependsOnID {
			continue
		}
		iss, ok := issueByID[dep.IssueID]
		if !ok {
			return nil, fmt.Errorf("issue_dependency references missing source issue id=%d", dep.IssueID)
		}
		if _, ok := issueByID[dep.DependsOnID]; !ok {
			return nil, fmt.Errorf("issue_dependency references missing target issue id=%d", dep.DependsOnID)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO graph_edge (
			     project_id, source_type, source_id, target_type, target_id, unresolved_target_text,
			     kind, origin, status, created_at
			 ) VALUES ($1, 'issue', $2, 'issue', $3, NULL, 'depends_on', 'explicit', 'confirmed', $4)`,
			iss.ProjectID, dep.IssueID, dep.DependsOnID, iss.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("insert graph_edge depends_on (%d -> %d): %w", dep.IssueID, dep.DependsOnID, err)
		}
		graphEdgeCount++
	}
	for _, rel := range docRels {
		doc, ok := docByID[rel.DocumentID]
		if !ok {
			return nil, fmt.Errorf("document_relation id=%d references missing source document id=%d", rel.ID, rel.DocumentID)
		}
		var targetType *string
		var targetID *int64
		var unresolved *string
		switch {
		case rel.TargetDocumentID != nil:
			if _, ok := docByID[*rel.TargetDocumentID]; !ok {
				return nil, fmt.Errorf("document_relation id=%d references missing target document id=%d", rel.ID, *rel.TargetDocumentID)
			}
			tt := "record"
			targetType = &tt
			targetID = rel.TargetDocumentID
		case rel.TargetIssueID != nil:
			if _, ok := issueByID[*rel.TargetIssueID]; !ok {
				return nil, fmt.Errorf("document_relation id=%d references missing target issue id=%d", rel.ID, *rel.TargetIssueID)
			}
			tt := "issue"
			targetType = &tt
			targetID = rel.TargetIssueID
		case rel.TargetText != nil:
			unresolved = rel.TargetText
		default:
			return nil, fmt.Errorf("document_relation id=%d has no target", rel.ID)
		}
		status := "confirmed"
		var decidedBy *string
		var decidedAt *time.Time
		if rel.RemovedAt != nil {
			status = "removed"
			decidedBy = rel.RemovedBy
			decidedAt = rel.RemovedAt
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO graph_edge (
			     project_id, source_type, source_id, target_type, target_id, unresolved_target_text,
			     kind, origin, status, decided_by, decided_at, created_at
			 ) VALUES ($1, 'record', $2, $3, $4, $5, $6, 'explicit', $7, $8, $9, $10)`,
			doc.ProjectID, rel.DocumentID, targetType, targetID, unresolved,
			rel.Kind, status, decidedBy, decidedAt, doc.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("insert graph_edge from document_relation id=%d: %w", rel.ID, err)
		}
		graphEdgeCount++
	}
	copied["graph_edge"] = graphEdgeCount

	// 11. activity
	for _, a := range activities {
		var changesArg *string
		if a.Changes != nil && strings.TrimSpace(*a.Changes) != "" {
			norm, err := normalizeJSONObject(*a.Changes, "{}")
			if err != nil {
				return nil, fmt.Errorf("activity id=%d changes: %w", a.ID, err)
			}
			changesArg = &norm
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO activity (id, issue_id, kind, actor_id, session_id, client_host, at, changes, body, summary, comment_kind)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11)`,
			a.ID, a.IssueID, a.Kind, a.ActorID, a.SessionID, a.ClientHost, a.At, changesArg, a.Body, a.Summary, a.CommentKind,
		); err != nil {
			return nil, fmt.Errorf("insert activity id=%d: %w", a.ID, err)
		}
	}
	copied["activity"] = int64(len(activities))

	// 12. document_activity (target_type = 'record')
	for _, da := range docActs {
		changesJSON, err := normalizeJSONObject(da.Changes, "{}")
		if err != nil {
			return nil, fmt.Errorf("document_activity id=%d changes: %w", da.ID, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO document_activity (id, target_type, target_id, actor_id, session_id, client_host, at, changes)
			 VALUES ($1, 'record', $2, $3, $4, $5, $6, $7::jsonb)`,
			da.ID, da.DocumentID, da.ActorID, da.SessionID, da.ClientHost, da.At, changesJSON,
		); err != nil {
			return nil, fmt.Errorf("insert document_activity id=%d: %w", da.ID, err)
		}
	}
	copied["document_activity"] = int64(len(docActs))

	// 13. resource_activity (project, milestone only; skip topic)
	var copiedResActs int64
	skippedTopicResActs := 0
	for _, ra := range resActs {
		if ra.Resource == "topic" {
			skippedTopicResActs++
			continue
		}
		changesJSON, err := normalizeJSONObject(ra.Changes, "{}")
		if err != nil {
			return nil, fmt.Errorf("resource_activity id=%d changes: %w", ra.ID, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO resource_activity (id, project_id, resource, resource_id, kind, actor_id, session_id, client_host, at, changes)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)`,
			ra.ID, ra.ProjectID, ra.Resource, ra.ResourceID, ra.Kind,
			ra.ActorID, ra.SessionID, ra.ClientHost, ra.At, changesJSON,
		); err != nil {
			return nil, fmt.Errorf("insert resource_activity id=%d: %w", ra.ID, err)
		}
		copiedResActs++
	}
	copied["resource_activity"] = copiedResActs

	// 14. message (topologically sorted by reply_to_id)
	for _, m := range sortedMessages {
		if _, err := tx.Exec(ctx,
			`INSERT INTO message (
			     id, project_id, to_label, to_actor, to_session_id, to_role, issue_id, reply_to_id,
			     title, body, created_at, created_by, created_session, created_host,
			     expires_at, deleted_at, deleted_by
			 ) VALUES (
			     $1, $2, $3, $4, $5, $6, $7, $8,
			     $9, $10, $11, $12, $13, $14,
			     $15, $16, $17
			 )`,
			m.ID, m.ProjectID, m.ToLabel, m.ToActor, m.ToSessionID, m.ToRole, m.IssueID, m.ReplyToID,
			m.Title, m.Body, m.CreatedAt, m.CreatedBy, m.CreatedSession, m.CreatedHost,
			m.ExpiresAt, m.DeletedAt, m.DeletedBy,
		); err != nil {
			return nil, fmt.Errorf("insert message id=%d: %w", m.ID, err)
		}
	}
	copied["message"] = int64(len(sortedMessages))

	// 15. message_read
	for _, mr := range msgReads {
		if _, err := tx.Exec(ctx,
			`INSERT INTO message_read (message_id, reader, read_at, read_by, read_session, read_host)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			mr.MessageID, mr.Reader, mr.ReadAt, mr.ReadBy, mr.ReadSession, mr.ReadHost,
		); err != nil {
			return nil, fmt.Errorf("insert message_read (%d, %q): %w", mr.MessageID, mr.Reader, err)
		}
	}
	copied["message_read"] = int64(len(msgReads))

	// 16. session (topologically sorted by parent_actor_id, parent_session_id)
	for _, s := range sortedSessions {
		var locArg *string
		if s.Location != nil && strings.TrimSpace(*s.Location) != "" {
			norm, err := normalizeJSONObject(*s.Location, "{}")
			if err != nil {
				return nil, fmt.Errorf("session (%s, %s) location: %w", s.ActorID, s.SessionID, err)
			}
			locArg = &norm
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO session (
			     actor_id, session_id, first_seen_at, last_seen_at, last_host,
			     parent_actor_id, parent_session_id, role, issue_id, location,
			     state, state_reason, state_at, updated_at
			 ) VALUES (
			     $1, $2, $3, $4, $5,
			     $6, $7, $8, $9, $10::jsonb,
			     $11, $12, $13, $14
			 )`,
			s.ActorID, s.SessionID, s.FirstSeenAt, s.LastSeenAt, s.LastHost,
			s.ParentActorID, s.ParentSessionID, s.Role, s.IssueID, locArg,
			s.State, s.StateReason, s.StateAt, s.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("insert session (%s, %s): %w", s.ActorID, s.SessionID, err)
		}
	}
	copied["session"] = int64(len(sortedSessions))

	// 17. issue_claim
	for _, c := range claims {
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_claim (id, issue_id, actor_id, session_id, claimed_at, released_at, outcome, reason)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			c.ID, c.IssueID, c.ActorID, c.SessionID, c.ClaimedAt, c.ReleasedAt, c.Outcome, c.Reason,
		); err != nil {
			return nil, fmt.Errorf("insert issue_claim id=%d: %w", c.ID, err)
		}
	}
	copied["issue_claim"] = int64(len(claims))

	// 18. event
	for _, e := range events {
		dataJSON, err := normalizeJSONObject(e.Data, "{}")
		if err != nil {
			return nil, fmt.Errorf("event id=%d data: %w", e.ID, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO event (id, at, project_id, kind, subject, actor, session_id, data)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)`,
			e.ID, e.At, e.ProjectID, e.Kind, e.Subject, e.Actor, e.SessionID, dataJSON,
		); err != nil {
			return nil, fmt.Errorf("insert event id=%d: %w", e.ID, err)
		}
	}
	copied["event"] = int64(len(events))

	// 19. Enqueue backfill ingest_job rows if enabled (default: true).
	enqueuedJobs := 0
	if opts.shouldEnqueueBackfill() {
		for _, i := range issues {
			labelSlice := make([]string, 0, len(labelsByIssue[i.ID]))
			for lbl := range labelsByIssue[i.ID] {
				labelSlice = append(labelSlice, lbl)
			}
			hash := issueIngestSHA256(i.Title, i.Body, labelSlice, refsByIssue[i.ID])
			if _, err := tx.Exec(ctx,
				`INSERT INTO ingest_job (source_type, source_id, source_sha256, stage, status)
				 VALUES ('issue', $1, $2, 'all', 'pending')`,
				i.ID, hash,
			); err != nil {
				return nil, fmt.Errorf("enqueue ingest_job for issue id=%d: %w", i.ID, err)
			}
			enqueuedJobs++
		}
		for _, a := range activities {
			if a.Kind != "commented" || a.Body == nil || *a.Body == "" {
				continue
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO ingest_job (source_type, source_id, source_sha256, stage, status)
				 VALUES ('activity', $1, $2, 'all', 'pending')`,
				a.ID, sha256Hex(*a.Body),
			); err != nil {
				return nil, fmt.Errorf("enqueue ingest_job for activity id=%d: %w", a.ID, err)
			}
			enqueuedJobs++
		}
		for _, d := range documents {
			hash := d.SHA256
			if len(hash) != 64 {
				hash = sha256Hex(d.Content)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO ingest_job (source_type, source_id, source_sha256, stage, status)
				 VALUES ('record', $1, $2, 'all', 'pending')`,
				d.ID, hash,
			); err != nil {
				return nil, fmt.Errorf("enqueue ingest_job for record_document id=%d: %w", d.ID, err)
			}
			enqueuedJobs++
		}
	}
	copied["ingest_job"] = int64(enqueuedJobs)

	// 20. Synchronize all IDENTITY sequences so subsequent inserts never collide.
	syncedSeqs, err := syncIdentitySequencesTx(ctx, tx)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, SanitizeError(fmt.Errorf("commit migration transaction: %w", err), opts.TargetDSN)
	}

	// Best-effort ANALYZE so PostgreSQL planner statistics reflect bulk load.
	_, _ = pool.Exec(ctx, `ANALYZE`)

	return &RunResult{
		SourcePath:                   opts.SourcePath,
		CopiedTables:                 copied,
		SkippedDerivedTables:         derivedCounts,
		PromotedEpics:                len(epicIDBySourceIssue),
		AssignedChildIssues:          assignedChildren,
		MergedTopicLabels:            mergedTopicLabels,
		SkippedTopicResourceActivity: skippedTopicResActs,
		ResolvedDuplicateClaims:      resolvedDupClaims,
		EnqueuedBackfillJobs:         enqueuedJobs,
		SyncedSequences:              syncedSeqs,
	}, nil
}

func ensureTargetEmptyTx(ctx context.Context, tx pgx.Tx) error {
	checks := []struct {
		name  string
		query string
	}{
		{"actor(non-system)", `SELECT COUNT(*) FROM actor WHERE id <> 'system'`},
		{"token", `SELECT COUNT(*) FROM token`},
		{"project(non-global)", `SELECT COUNT(*) FROM project WHERE id <> 0`},
		{"epic", `SELECT COUNT(*) FROM epic`},
		{"milestone", `SELECT COUNT(*) FROM milestone`},
		{"issue", `SELECT COUNT(*) FROM issue`},
		{"spec_document", `SELECT COUNT(*) FROM spec_document`},
		{"spec_revision", `SELECT COUNT(*) FROM spec_revision`},
		{"record_document", `SELECT COUNT(*) FROM record_document`},
		{"activity", `SELECT COUNT(*) FROM activity`},
		{"document_activity", `SELECT COUNT(*) FROM document_activity`},
		{"resource_activity", `SELECT COUNT(*) FROM resource_activity`},
		{"message", `SELECT COUNT(*) FROM message`},
		{"session", `SELECT COUNT(*) FROM session`},
		{"issue_claim", `SELECT COUNT(*) FROM issue_claim`},
		{"event", `SELECT COUNT(*) FROM event`},
		{"ingest_job", `SELECT COUNT(*) FROM ingest_job`},
		{"normalized_artifact", `SELECT COUNT(*) FROM normalized_artifact`},
		{"rag_chunk", `SELECT COUNT(*) FROM rag_chunk`},
		{"embedding_generation", `SELECT COUNT(*) FROM embedding_generation`},
		{"graph_edge", `SELECT COUNT(*) FROM graph_edge`},
	}
	for _, c := range checks {
		var n int64
		if err := tx.QueryRow(ctx, c.query).Scan(&n); err != nil {
			return fmt.Errorf("check target table %s: %w", c.name, err)
		}
		if n > 0 {
			return fmt.Errorf("%w: table %s already contains %d rows", ErrTargetNotEmpty, c.name, n)
		}
	}
	return nil
}

func syncIdentitySequencesTx(ctx context.Context, tx pgx.Tx) ([]string, error) {
	synced := make([]string, 0, len(PGIdentityTables))
	for _, tbl := range PGIdentityTables {
		var seqName *string
		if err := tx.QueryRow(ctx, `SELECT pg_get_serial_sequence($1, 'id')`, tbl).Scan(&seqName); err != nil {
			return nil, fmt.Errorf("resolve identity sequence for %s: %w", tbl, err)
		}
		if seqName == nil || *seqName == "" {
			return nil, fmt.Errorf("identity sequence not found for table %s", tbl)
		}
		var maxID int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(id), 0) FROM `+pgx.Identifier{tbl}.Sanitize()).Scan(&maxID); err != nil {
			return nil, fmt.Errorf("read max id for %s: %w", tbl, err)
		}
		if maxID >= 1 {
			if _, err := tx.Exec(ctx, `SELECT setval($1, $2, true)`, *seqName, maxID); err != nil {
				return nil, fmt.Errorf("setval(%s, %d, true): %w", *seqName, maxID, err)
			}
		} else {
			if _, err := tx.Exec(ctx, `SELECT setval($1, 1, false)`, *seqName); err != nil {
				return nil, fmt.Errorf("setval(%s, 1, false): %w", *seqName, err)
			}
		}
		synced = append(synced, *seqName)
	}
	return synced, nil
}

// FormatTable writes a human-readable summary of a migration run.
func (r *RunResult) FormatTable(w io.Writer) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Migration Completed\n")
	fmt.Fprintf(&buf, "Source: %s\n\n", r.SourcePath)

	fmt.Fprintf(&buf, "== Copied PostgreSQL Tables ==\n")
	names := make([]string, 0, len(r.CopiedTables))
	for k := range r.CopiedTables {
		names = append(names, k)
	}
	sort.Strings(names)
	tw := tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "TABLE\tROWS\n")
	for _, n := range names {
		fmt.Fprintf(tw, "%s\t%d\n", n, r.CopiedTables[n])
	}
	_ = tw.Flush()

	fmt.Fprintf(&buf, "\n== Skipped Derived SQLite Tables ==\n")
	dnames := make([]string, 0, len(r.SkippedDerivedTables))
	for k := range r.SkippedDerivedTables {
		dnames = append(dnames, k)
	}
	sort.Strings(dnames)
	tw = tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "TABLE\tSOURCE_ROWS\n")
	for _, n := range dnames {
		fmt.Fprintf(tw, "%s\t%d\n", n, r.SkippedDerivedTables[n])
	}
	_ = tw.Flush()

	fmt.Fprintf(&buf, "\n== Summary ==\n")
	fmt.Fprintf(&buf, "Promoted Epics: %d | Assigned Child Issues: %d\n", r.PromotedEpics, r.AssignedChildIssues)
	fmt.Fprintf(&buf, "Merged Topic Labels: %d | Skipped Topic Resource Activity: %d\n", r.MergedTopicLabels, r.SkippedTopicResourceActivity)
	fmt.Fprintf(&buf, "Resolved Duplicate Claims: %d | Enqueued Backfill Jobs: %d\n", r.ResolvedDuplicateClaims, r.EnqueuedBackfillJobs)
	fmt.Fprintf(&buf, "Synced Identity Sequences: %d\n", len(r.SyncedSequences))

	_, err := io.WriteString(w, buf.String())
	return err
}
