package migrate

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/model"
)

// VerifyOptions configures optional verification overrides.
type VerifyOptions struct {
	TopicLabelMap map[string]string
}

// TableVerifyRow holds expected vs actual counts for one table or mapping check.
type TableVerifyRow struct {
	Name     string `json:"name"`
	Expected int64  `json:"expected"`
	Actual   int64  `json:"actual"`
	Passed   bool   `json:"passed"`
}

// SequenceCheck verifies that a PostgreSQL IDENTITY sequence will allocate an ID
// strictly greater than the table's current MAX(id).
type SequenceCheck struct {
	Table     string `json:"table"`
	Sequence  string `json:"sequence"`
	MaxID     int64  `json:"max_id"`
	LastValue int64  `json:"last_value"`
	IsCalled  bool   `json:"is_called"`
	NextValue int64  `json:"next_value"`
	Passed    bool   `json:"passed"`
}

// VerifyReport holds the full post-migration integrity check results.
type VerifyReport struct {
	OK                     bool             `json:"ok"`
	GlobalSeedOK           bool             `json:"global_seed_ok"`
	TableCounts            []TableVerifyRow `json:"table_counts"`
	DocumentHashChecked    int              `json:"document_hash_checked"`
	DocumentHashMismatches []string         `json:"document_hash_mismatches"`
	EpicFKChecked          int              `json:"epic_fk_checked"`
	EpicFKViolations       []string         `json:"epic_fk_violations"`
	SequenceChecks         []SequenceCheck  `json:"sequence_checks"`
	Failures               []string         `json:"failures"`
}

// Verify compares the read-only SQLite source database against the target
// PostgreSQL database across counts, epic/FK integrity, document sha256 hashes,
// GLOBAL scope preservation, and IDENTITY sequence alignment.
func Verify(ctx context.Context, sqlitePath string, pgPool *pgxpool.Pool) (*VerifyReport, error) {
	return VerifyWithOptions(ctx, sqlitePath, pgPool, VerifyOptions{})
}

// VerifyWithOptions runs Verify with optional custom topic label overrides.
func VerifyWithOptions(ctx context.Context, sqlitePath string, pgPool *pgxpool.Pool, opts VerifyOptions) (*VerifyReport, error) {
	if pgPool == nil {
		return nil, fmt.Errorf("PostgreSQL pool is required")
	}
	db, _, err := openSQLiteReadOnly(ctx, sqlitePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	rep := &VerifyReport{
		OK:                     true,
		TableCounts:            []TableVerifyRow{},
		DocumentHashMismatches: []string{},
		EpicFKViolations:       []string{},
		SequenceChecks:         []SequenceCheck{},
		Failures:               []string{},
	}
	fail := func(msg string) {
		rep.OK = false
		rep.Failures = append(rep.Failures, msg)
	}
	addCount := func(name string, expected, actual int64) {
		passed := expected == actual
		rep.TableCounts = append(rep.TableCounts, TableVerifyRow{
			Name:     name,
			Expected: expected,
			Actual:   actual,
			Passed:   passed,
		})
		if !passed {
			fail(fmt.Sprintf("count mismatch for %s: expected=%d actual=%d", name, expected, actual))
		}
	}

	// 1. Verify GLOBAL project seed (id=0, key='GLOBAL', scope_kind='global').
	var globalCount int64
	if err := pgPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM project WHERE id = $1 AND key = $2 AND scope_kind = 'global'`,
		model.GlobalProjectID, model.GlobalProjectKey,
	).Scan(&globalCount); err != nil {
		return nil, fmt.Errorf("verify GLOBAL project seed: %w", err)
	}
	rep.GlobalSeedOK = globalCount == 1
	if !rep.GlobalSeedOK {
		fail(fmt.Sprintf("GLOBAL project seed (id=0, key='GLOBAL', scope_kind='global') missing or invalid (count=%d)", globalCount))
	}

	// Read SQLite source data for deep comparisons.
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

	pgCount := func(query string) (int64, error) {
		var n int64
		err := pgPool.QueryRow(ctx, query).Scan(&n)
		return n, err
	}

	// 2. Table count comparisons.
	// actor: verify every SQLite actor exists with matching kind.
	pgActors := map[string]string{}
	aRows, err := pgPool.Query(ctx, `SELECT id, kind FROM actor`)
	if err != nil {
		return nil, fmt.Errorf("query pg actor: %w", err)
	}
	for aRows.Next() {
		var id, kind string
		if err := aRows.Scan(&id, &kind); err != nil {
			aRows.Close()
			return nil, err
		}
		pgActors[id] = kind
	}
	aRows.Close()
	if err := aRows.Err(); err != nil {
		return nil, err
	}
	matchedActors := int64(0)
	for _, a := range actors {
		if k, ok := pgActors[a.ID]; ok && k == a.Kind {
			matchedActors++
		} else {
			fail(fmt.Sprintf("actor %q (kind=%s) missing or mismatched in PostgreSQL (got %q)", a.ID, a.Kind, k))
		}
	}
	addCount("actor(sqlite_matched)", int64(len(actors)), matchedActors)

	pgProjects, err := pgCount(`SELECT COUNT(*) FROM project WHERE scope_kind = 'project'`)
	if err != nil {
		return nil, err
	}
	addCount("project(scope_project)", int64(len(projects)), pgProjects)

	pgTokens, err := pgCount(`SELECT COUNT(*) FROM token`)
	if err != nil {
		return nil, err
	}
	addCount("token", int64(len(tokens)), pgTokens)

	pgMilestones, err := pgCount(`SELECT COUNT(*) FROM milestone`)
	if err != nil {
		return nil, err
	}
	addCount("milestone", int64(len(milestones)), pgMilestones)

	pgIssues, err := pgCount(`SELECT COUNT(*) FROM issue`)
	if err != nil {
		return nil, err
	}
	addCount("issue", int64(len(issues)), pgIssues)

	// Epic count & child epic_id count.
	parentSet := map[int64]bool{}
	expectedChildren := int64(0)
	for _, i := range issues {
		if i.ParentID != nil {
			parentSet[*i.ParentID] = true
			expectedChildren++
		}
	}
	pgEpics, err := pgCount(`SELECT COUNT(*) FROM epic`)
	if err != nil {
		return nil, err
	}
	addCount("epic", int64(len(parentSet)), pgEpics)

	pgAssignedChildren, err := pgCount(`SELECT COUNT(*) FROM issue WHERE epic_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	addCount("issue(epic_id_assigned)", expectedChildren, pgAssignedChildren)

	// issue_label expected deduplicated union (SQLite issue_label + topic mapping).
	topicPaths := BuildTopicPaths(topics)
	topicLabelByID := make(map[int64]string, len(topics))
	for _, t := range topics {
		topicLabelByID[t.ID] = NormalizeTopicLabel(t.ID, topicPaths[t.ID], t.Name, opts.TopicLabelMap)
	}
	type labelPair struct {
		issueID int64
		label   string
	}
	expectedLabels := map[labelPair]bool{}
	for _, il := range issueLabels {
		expectedLabels[labelPair{il.IssueID, il.Label}] = true
	}
	for _, i := range issues {
		if i.TopicID != nil {
			if lbl, ok := topicLabelByID[*i.TopicID]; ok {
				expectedLabels[labelPair{i.ID, lbl}] = true
			}
		}
	}
	pgLabels, err := pgCount(`SELECT COUNT(*) FROM issue_label`)
	if err != nil {
		return nil, err
	}
	addCount("issue_label", int64(len(expectedLabels)), pgLabels)

	pgRefs, err := pgCount(`SELECT COUNT(*) FROM issue_ref`)
	if err != nil {
		return nil, err
	}
	addCount("issue_ref", int64(len(issueRefs)), pgRefs)

	pgRecords, err := pgCount(`SELECT COUNT(*) FROM record_document`)
	if err != nil {
		return nil, err
	}
	addCount("record_document", int64(len(documents)), pgRecords)

	pgActs, err := pgCount(`SELECT COUNT(*) FROM activity`)
	if err != nil {
		return nil, err
	}
	addCount("activity", int64(len(activities)), pgActs)

	pgDocActs, err := pgCount(`SELECT COUNT(*) FROM document_activity WHERE target_type = 'record'`)
	if err != nil {
		return nil, err
	}
	addCount("document_activity", int64(len(docActs)), pgDocActs)

	expectedResActs := int64(0)
	for _, ra := range resActs {
		if ra.Resource == "project" || ra.Resource == "milestone" {
			expectedResActs++
		}
	}
	pgResActs, err := pgCount(`SELECT COUNT(*) FROM resource_activity`)
	if err != nil {
		return nil, err
	}
	addCount("resource_activity", expectedResActs, pgResActs)

	pgMsgs, err := pgCount(`SELECT COUNT(*) FROM message`)
	if err != nil {
		return nil, err
	}
	addCount("message", int64(len(messages)), pgMsgs)

	pgMsgReads, err := pgCount(`SELECT COUNT(*) FROM message_read`)
	if err != nil {
		return nil, err
	}
	addCount("message_read", int64(len(msgReads)), pgMsgReads)

	pgSessions, err := pgCount(`SELECT COUNT(*) FROM session`)
	if err != nil {
		return nil, err
	}
	addCount("session", int64(len(sessions)), pgSessions)

	pgClaims, err := pgCount(`SELECT COUNT(*) FROM issue_claim`)
	if err != nil {
		return nil, err
	}
	addCount("issue_claim", int64(len(claims)), pgClaims)

	pgEvents, err := pgCount(`SELECT COUNT(*) FROM event`)
	if err != nil {
		return nil, err
	}
	addCount("event", int64(len(events)), pgEvents)

	// graph_edge breakdown: depends_on + record relations (confirmed/removed/unresolved).
	expectedDeps := int64(0)
	for _, d := range issueDeps {
		if d.IssueID != d.DependsOnID {
			expectedDeps++
		}
	}
	pgDepEdges, err := pgCount(`SELECT COUNT(*) FROM graph_edge WHERE source_type = 'issue' AND target_type = 'issue' AND kind = 'depends_on' AND origin = 'explicit'`)
	if err != nil {
		return nil, err
	}
	addCount("graph_edge(depends_on)", expectedDeps, pgDepEdges)

	pgDocEdges, err := pgCount(`SELECT COUNT(*) FROM graph_edge WHERE source_type = 'record' AND origin = 'explicit'`)
	if err != nil {
		return nil, err
	}
	addCount("graph_edge(document_relation)", int64(len(docRels)), pgDocEdges)

	expectedUnresolved := int64(0)
	expectedRemoved := int64(0)
	for _, r := range docRels {
		if r.TargetText != nil {
			expectedUnresolved++
		}
		if r.RemovedAt != nil {
			expectedRemoved++
		}
	}
	pgUnresolved, err := pgCount(`SELECT COUNT(*) FROM graph_edge WHERE source_type = 'record' AND unresolved_target_text IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	addCount("graph_edge(unresolved_target_text)", expectedUnresolved, pgUnresolved)

	pgRemoved, err := pgCount(`SELECT COUNT(*) FROM graph_edge WHERE source_type = 'record' AND status = 'removed'`)
	if err != nil {
		return nil, err
	}
	addCount("graph_edge(removed)", expectedRemoved, pgRemoved)

	// 3. 100% record_document sha256 + content hash check.
	pgDocHashes := make(map[int64][2]string, len(documents))
	dRows, err := pgPool.Query(ctx, `SELECT id, sha256, content FROM record_document`)
	if err != nil {
		return nil, fmt.Errorf("query pg record_document hashes: %w", err)
	}
	for dRows.Next() {
		var id int64
		var storedHash, content string
		if err := dRows.Scan(&id, &storedHash, &content); err != nil {
			dRows.Close()
			return nil, err
		}
		pgDocHashes[id] = [2]string{storedHash, sha256Hex(content)}
	}
	dRows.Close()
	if err := dRows.Err(); err != nil {
		return nil, err
	}

	for _, d := range documents {
		rep.DocumentHashChecked++
		got, ok := pgDocHashes[d.ID]
		if !ok {
			msg := fmt.Sprintf("record_document id=%d (%s) missing in PostgreSQL", d.ID, d.Name)
			rep.DocumentHashMismatches = append(rep.DocumentHashMismatches, msg)
			fail(msg)
			continue
		}
		wantHash := d.SHA256
		if len(wantHash) != 64 {
			wantHash = sha256Hex(d.Content)
		}
		wantContentHash := sha256Hex(d.Content)
		if got[0] != wantHash || got[1] != wantContentHash {
			msg := fmt.Sprintf("record_document id=%d (%s) hash mismatch: sqlite=(stored:%s content:%s) pg=(stored:%s content:%s)",
				d.ID, d.Name, wantHash, wantContentHash, got[0], got[1])
			rep.DocumentHashMismatches = append(rep.DocumentHashMismatches, msg)
			fail(msg)
		}
	}

	// 4. Verify promoted epic.source_issue_id, exact child->epic mapping, and dependent FKs.
	issueByID := make(map[int64]sqliteIssue, len(issues))
	for _, i := range issues {
		issueByID[i.ID] = i
	}
	epicBySourceIssue := map[int64]int64{}
	eRows, err := pgPool.Query(ctx, `
		SELECT e.id, e.project_id, e.source_issue_id, e.source_issue_key, i.key, i.project_id
		FROM epic e
		LEFT JOIN issue i ON i.id = e.source_issue_id`)
	if err != nil {
		return nil, fmt.Errorf("query pg epic source issues: %w", err)
	}
	for eRows.Next() {
		var epicID, epicProjID int64
		var srcID, issProjID *int64
		var srcKey, issKey *string
		if err := eRows.Scan(&epicID, &epicProjID, &srcID, &srcKey, &issKey, &issProjID); err != nil {
			eRows.Close()
			return nil, err
		}
		rep.EpicFKChecked++
		if srcID == nil || issKey == nil || issProjID == nil {
			msg := fmt.Sprintf("epic id=%d missing valid source_issue_id FK", epicID)
			rep.EpicFKViolations = append(rep.EpicFKViolations, msg)
			fail(msg)
			continue
		}
		if !parentSet[*srcID] {
			msg := fmt.Sprintf("epic id=%d source_issue_id=%d was not a parent issue in SQLite", epicID, *srcID)
			rep.EpicFKViolations = append(rep.EpicFKViolations, msg)
			fail(msg)
		}
		if srcKey == nil || *srcKey != *issKey || epicProjID != *issProjID {
			msg := fmt.Sprintf("epic id=%d source metadata mismatch (source_key=%v issue_key=%v)", epicID, srcKey, issKey)
			rep.EpicFKViolations = append(rep.EpicFKViolations, msg)
			fail(msg)
		}
		epicBySourceIssue[*srcID] = epicID
	}
	eRows.Close()
	if err := eRows.Err(); err != nil {
		return nil, err
	}

	// Verify every child issue points to its parent's promoted epic.
	pgChildEpics := map[int64]*int64{}
	cRows, err := pgPool.Query(ctx, `SELECT id, epic_id FROM issue`)
	if err != nil {
		return nil, fmt.Errorf("query pg issue epic_id: %w", err)
	}
	for cRows.Next() {
		var id int64
		var eid *int64
		if err := cRows.Scan(&id, &eid); err != nil {
			cRows.Close()
			return nil, err
		}
		pgChildEpics[id] = eid
	}
	cRows.Close()
	if err := cRows.Err(); err != nil {
		return nil, err
	}
	for _, i := range issues {
		gotEpicID, exists := pgChildEpics[i.ID]
		if !exists {
			msg := fmt.Sprintf("issue id=%d (%s) missing in PostgreSQL", i.ID, i.Key)
			rep.EpicFKViolations = append(rep.EpicFKViolations, msg)
			fail(msg)
			continue
		}
		if i.ParentID == nil {
			if gotEpicID != nil {
				msg := fmt.Sprintf("non-child issue id=%d (%s) unexpectedly has epic_id=%d", i.ID, i.Key, *gotEpicID)
				rep.EpicFKViolations = append(rep.EpicFKViolations, msg)
				fail(msg)
			}
			continue
		}
		wantEpicID, ok := epicBySourceIssue[*i.ParentID]
		if !ok || gotEpicID == nil || *gotEpicID != wantEpicID {
			msg := fmt.Sprintf("child issue id=%d (%s) epic_id mismatch: want=%d got=%v", i.ID, i.Key, wantEpicID, gotEpicID)
			rep.EpicFKViolations = append(rep.EpicFKViolations, msg)
			fail(msg)
		}
	}

	// Verify dependent FK counts on promoted source issues match SQLite 1:1.
	if len(parentSet) > 0 {
		promotedIDs := make([]int64, 0, len(parentSet))
		for pid := range parentSet {
			promotedIDs = append(promotedIDs, pid)
		}
		sort.Slice(promotedIDs, func(i, j int) bool { return promotedIDs[i] < promotedIDs[j] })

		var sqlitePromotedAct, sqlitePromotedLabel, sqlitePromotedRef, sqlitePromotedMsg, sqlitePromotedClaim, sqlitePromotedEdge int64
		for _, a := range activities {
			if parentSet[a.IssueID] {
				sqlitePromotedAct++
			}
		}
		for lp := range expectedLabels {
			if parentSet[lp.issueID] {
				sqlitePromotedLabel++
			}
		}
		for _, r := range issueRefs {
			if parentSet[r.IssueID] {
				sqlitePromotedRef++
			}
		}
		for _, m := range messages {
			if m.IssueID != nil && parentSet[*m.IssueID] {
				sqlitePromotedMsg++
			}
		}
		for _, c := range claims {
			if parentSet[c.IssueID] {
				sqlitePromotedClaim++
			}
		}
		for _, dep := range issueDeps {
			if dep.IssueID != dep.DependsOnID && (parentSet[dep.IssueID] || parentSet[dep.DependsOnID]) {
				sqlitePromotedEdge++
			}
		}
		for _, rel := range docRels {
			if rel.TargetIssueID != nil && parentSet[*rel.TargetIssueID] {
				sqlitePromotedEdge++
			}
		}

		depChecks := []struct {
			name  string
			want  int64
			query string
		}{
			{"promoted_issue.activity", sqlitePromotedAct, `SELECT COUNT(*) FROM activity WHERE issue_id = ANY($1)`},
			{"promoted_issue.issue_label", sqlitePromotedLabel, `SELECT COUNT(*) FROM issue_label WHERE issue_id = ANY($1)`},
			{"promoted_issue.issue_ref", sqlitePromotedRef, `SELECT COUNT(*) FROM issue_ref WHERE issue_id = ANY($1)`},
			{"promoted_issue.message", sqlitePromotedMsg, `SELECT COUNT(*) FROM message WHERE issue_id = ANY($1)`},
			{"promoted_issue.issue_claim", sqlitePromotedClaim, `SELECT COUNT(*) FROM issue_claim WHERE issue_id = ANY($1)`},
			{"promoted_issue.graph_edge", sqlitePromotedEdge, `SELECT COUNT(*) FROM graph_edge WHERE (source_type = 'issue' AND source_id = ANY($1)) OR (target_type = 'issue' AND target_id = ANY($1))`},
		}
		for _, dc := range depChecks {
			var got int64
			if err := pgPool.QueryRow(ctx, dc.query, promotedIDs).Scan(&got); err != nil {
				return nil, fmt.Errorf("verify %s: %w", dc.name, err)
			}
			if got != dc.want {
				msg := fmt.Sprintf("dependent FK count mismatch for %s: expected=%d actual=%d", dc.name, dc.want, got)
				rep.EpicFKViolations = append(rep.EpicFKViolations, msg)
				fail(msg)
			}
		}
	}

	// 5. Verify all IDENTITY sequences are >= MAX(id) (next allocated value > MAX(id)).
	for _, tbl := range PGIdentityTables {
		var seqName *string
		if err := pgPool.QueryRow(ctx, `SELECT pg_get_serial_sequence($1, 'id')`, tbl).Scan(&seqName); err != nil {
			return nil, fmt.Errorf("resolve sequence for %s: %w", tbl, err)
		}
		if seqName == nil || *seqName == "" {
			fail(fmt.Sprintf("sequence not found for table %s", tbl))
			continue
		}
		var maxID int64
		if err := pgPool.QueryRow(ctx, `SELECT COALESCE(MAX(id), 0) FROM `+pgx.Identifier{tbl}.Sanitize()).Scan(&maxID); err != nil {
			return nil, fmt.Errorf("query max id for %s: %w", tbl, err)
		}
		var seqSchema, seqRel string
		if err := pgPool.QueryRow(ctx, `
			SELECT n.nspname, c.relname
			FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.oid = to_regclass($1)`,
			*seqName,
		).Scan(&seqSchema, &seqRel); err != nil {
			return nil, fmt.Errorf("resolve sequence identifier %s: %w", *seqName, err)
		}
		var lastVal int64
		var isCalled bool
		seqIdent := pgx.Identifier{seqSchema, seqRel}.Sanitize()
		if err := pgPool.QueryRow(ctx, `SELECT last_value, is_called FROM `+seqIdent).Scan(&lastVal, &isCalled); err != nil {
			return nil, fmt.Errorf("query sequence %s: %w", *seqName, err)
		}
		nextVal := lastVal
		if isCalled {
			nextVal = lastVal + 1
		}
		passed := nextVal > maxID && lastVal >= maxID
		rep.SequenceChecks = append(rep.SequenceChecks, SequenceCheck{
			Table:     tbl,
			Sequence:  *seqName,
			MaxID:     maxID,
			LastValue: lastVal,
			IsCalled:  isCalled,
			NextValue: nextVal,
			Passed:    passed,
		})
		if !passed {
			fail(fmt.Sprintf("sequence %s for table %s out of sync: max_id=%d last_value=%d is_called=%v next_value=%d",
				*seqName, tbl, maxID, lastVal, isCalled, nextVal))
		}
	}

	return rep, nil
}

// FormatTable writes a human-readable integrity verification summary.
func (r *VerifyReport) FormatTable(w io.Writer) error {
	var buf bytes.Buffer
	status := "PASS"
	if !r.OK {
		status = "FAIL"
	}
	fmt.Fprintf(&buf, "Migration Integrity Verification: %s\n\n", status)
	fmt.Fprintf(&buf, "GLOBAL Scope Seed (id=0): %v\n", r.GlobalSeedOK)
	fmt.Fprintf(&buf, "Document SHA-256 Checked: %d (mismatches: %d)\n", r.DocumentHashChecked, len(r.DocumentHashMismatches))
	fmt.Fprintf(&buf, "Epic FK Checked: %d (violations: %d)\n\n", r.EpicFKChecked, len(r.EpicFKViolations))

	fmt.Fprintf(&buf, "== Table & Mapping Counts ==\n")
	tw := tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "CHECK\tEXPECTED\tACTUAL\tSTATUS\n")
	for _, row := range r.TableCounts {
		st := "PASS"
		if !row.Passed {
			st = "FAIL"
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\n", row.Name, row.Expected, row.Actual, st)
	}
	_ = tw.Flush()

	fmt.Fprintf(&buf, "\n== Identity Sequences ==\n")
	tw = tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "TABLE\tMAX_ID\tLAST_VALUE\tNEXT_VALUE\tSTATUS\n")
	for _, sc := range r.SequenceChecks {
		st := "PASS"
		if !sc.Passed {
			st = "FAIL"
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\n", sc.Table, sc.MaxID, sc.LastValue, sc.NextValue, st)
	}
	_ = tw.Flush()

	if len(r.Failures) > 0 {
		fmt.Fprintf(&buf, "\n== Failures (%d) ==\n", len(r.Failures))
		for _, f := range r.Failures {
			fmt.Fprintf(&buf, "  - %s\n", f)
		}
	}

	_, err := io.WriteString(w, buf.String())
	return err
}
