package migrate

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/wsk0715/agent-board/internal/model"
)

// PlanOptions configures optional dry-run inspection behavior.
type PlanOptions struct {
	TopicLabelMap map[string]string
}

// TableCount holds the row count and migration disposition for one SQLite table.
type TableCount struct {
	Name     string      `json:"name"`
	Target   string      `json:"target"`
	Action   TableAction `json:"action"`
	RowCount int64       `json:"row_count"`
}

// EpicPromotionCandidate describes one parent issue to be promoted to an epic
// while preserving its original issue row 1:1.
type EpicPromotionCandidate struct {
	SourceIssueID    int64    `json:"source_issue_id"`
	SourceIssueKey   string   `json:"source_issue_key"`
	ProjectID        int64    `json:"project_id"`
	ProjectKey       string   `json:"project_key"`
	Title            string   `json:"title"`
	IssueStatus      string   `json:"issue_status"`
	TargetEpicKey    string   `json:"target_epic_key"`
	TargetEpicStatus string   `json:"target_epic_status"`
	ChildCount       int      `json:"child_count"`
	ChildIssueIDs    []int64  `json:"child_issue_ids"`
	ChildIssueKeys   []string `json:"child_issue_keys"`
}

// TopicLabelMapping describes how one SQLite topic maps to a normalized issue_label.
type TopicLabelMapping struct {
	TopicID             int64  `json:"topic_id"`
	ProjectID           int64  `json:"project_id"`
	ProjectKey          string `json:"project_key"`
	TopicName           string `json:"topic_name"`
	TopicPath           string `json:"topic_path"`
	TopicStatus         string `json:"topic_status"`
	NormalizedLabel     string `json:"normalized_label"`
	IssueCount          int    `json:"issue_count"`
	DuplicateLabelCount int    `json:"duplicate_label_count"`
}

// MilestoneCandidate flags a milestone that merits operator review before cutover.
type MilestoneCandidate struct {
	MilestoneID int64  `json:"milestone_id"`
	ProjectKey  string `json:"project_key"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	TotalIssues int    `json:"total_issues"`
	DoneIssues  int    `json:"done_issues"`
	OpenIssues  int    `json:"open_issues"`
	Reason      string `json:"reason"`
}

// DuplicateMilestoneName reports duplicate live milestone names in one project.
type DuplicateMilestoneName struct {
	ProjectKey   string  `json:"project_key"`
	Name         string  `json:"name"`
	MilestoneIDs []int64 `json:"milestone_ids"`
}

// MilestonePlanSummary summarizes milestone status conversions and review candidates.
type MilestonePlanSummary struct {
	Total                   int                      `json:"total"`
	StatusConversions       map[string]int           `json:"status_conversions"`
	CompletedOpenCandidates []MilestoneCandidate     `json:"completed_open_candidates"`
	NonVersionCandidates    []MilestoneCandidate     `json:"non_version_candidates"`
	DuplicateLiveNames      []DuplicateMilestoneName `json:"duplicate_live_names"`
}

// DocumentCheckIssue describes a validation or metadata warning on a SQLite document.
type DocumentCheckIssue struct {
	DocumentID int64  `json:"document_id"`
	ProjectKey string `json:"project_key"`
	Name       string `json:"name"`
	SizeBytes  int    `json:"size_bytes"`
	Reason     string `json:"reason"`
	Blocking   bool   `json:"blocking"`
}

// DuplicateOpenClaim reports multiple active (released_at IS NULL) claims on one issue.
type DuplicateOpenClaim struct {
	IssueID  int64   `json:"issue_id"`
	IssueKey string  `json:"issue_key"`
	ClaimIDs []int64 `json:"claim_ids"`
}

// PlanReport is the complete dry-run analysis produced by Plan without connecting to PostgreSQL.
type PlanReport struct {
	SourcePath                  string                   `json:"source_path"`
	SchemaVersion               int                      `json:"schema_version"`
	Tables                      []TableCount             `json:"tables"`
	EpicPromotions              []EpicPromotionCandidate `json:"epic_promotions"`
	TotalPromotedEpics          int                      `json:"total_promoted_epics"`
	TotalAssignedChildIssues    int                      `json:"total_assigned_child_issues"`
	TopicMappings               []TopicLabelMapping      `json:"topic_mappings"`
	MilestoneSummary            MilestonePlanSummary     `json:"milestone_summary"`
	DocumentIssues              []DocumentCheckIssue     `json:"document_issues"`
	UnresolvedDocumentRelations int                      `json:"unresolved_document_relations"`
	RemovedDocumentRelations    int                      `json:"removed_document_relations"`
	DuplicateOpenClaims         []DuplicateOpenClaim     `json:"duplicate_open_claims"`
	TopicResourceActivityCount  int                      `json:"topic_resource_activity_count"`
	MissingActors               []string                 `json:"missing_actors"`
	ProjectIDConflicts          []string                 `json:"project_id_conflicts"`
	BlockingIssues              []string                 `json:"blocking_issues"`
}

// Plan inspects sqlitePath strictly read-only without touching PostgreSQL and
// returns a full migration dry-run report.
func Plan(ctx context.Context, sqlitePath string) (*PlanReport, error) {
	return PlanWithOptions(ctx, sqlitePath, PlanOptions{})
}

// PlanWithOptions inspects sqlitePath read-only using the given options.
func PlanWithOptions(ctx context.Context, sqlitePath string, opts PlanOptions) (*PlanReport, error) {
	db, userVer, err := openSQLiteReadOnly(ctx, sqlitePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	report := &PlanReport{
		SourcePath:          sqlitePath,
		SchemaVersion:       userVer,
		Tables:              make([]TableCount, 0, len(SQLiteTables)),
		EpicPromotions:      []EpicPromotionCandidate{},
		TopicMappings:       []TopicLabelMapping{},
		DocumentIssues:      []DocumentCheckIssue{},
		DuplicateOpenClaims: []DuplicateOpenClaim{},
		MissingActors:       []string{},
		ProjectIDConflicts:  []string{},
		BlockingIssues:      []string{},
		MilestoneSummary: MilestonePlanSummary{
			StatusConversions:       map[string]int{},
			CompletedOpenCandidates: []MilestoneCandidate{},
			NonVersionCandidates:    []MilestoneCandidate{},
			DuplicateLiveNames:      []DuplicateMilestoneName{},
		},
	}

	for _, spec := range SQLiteTables {
		cnt, err := sqliteTableCount(ctx, db, spec.Name)
		if err != nil {
			return nil, err
		}
		report.Tables = append(report.Tables, TableCount{
			Name:     spec.Name,
			Target:   spec.Target,
			Action:   spec.Action,
			RowCount: cnt,
		})
	}

	actors, err := readSQLiteActors(ctx, db)
	if err != nil {
		return nil, err
	}
	actorSet := make(map[string]bool, len(actors))
	for _, a := range actors {
		actorSet[a.ID] = true
	}

	projects, err := readSQLiteProjects(ctx, db)
	if err != nil {
		return nil, err
	}
	projectByID := make(map[int64]sqliteProject, len(projects))
	for _, p := range projects {
		projectByID[p.ID] = p
		if p.ID == model.GlobalProjectID || strings.EqualFold(p.Key, model.GlobalProjectKey) {
			msg := fmt.Sprintf("project id=%d key=%q conflicts with reserved GLOBAL scope (id=0)", p.ID, p.Key)
			report.ProjectIDConflicts = append(report.ProjectIDConflicts, msg)
			report.BlockingIssues = append(report.BlockingIssues, msg)
		}
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
	documents, err := readSQLiteDocuments(ctx, db)
	if err != nil {
		return nil, err
	}
	docRels, err := readSQLiteDocumentRelations(ctx, db)
	if err != nil {
		return nil, err
	}
	resActs, err := readSQLiteResourceActivities(ctx, db)
	if err != nil {
		return nil, err
	}
	claims, err := readSQLiteIssueClaims(ctx, db)
	if err != nil {
		return nil, err
	}

	// 1. Epic promotions (parent issues with >= 1 child).
	issueByID := make(map[int64]sqliteIssue, len(issues))
	childrenByParent := map[int64][]sqliteIssue{}
	for _, iss := range issues {
		issueByID[iss.ID] = iss
		if iss.ParentID != nil {
			childrenByParent[*iss.ParentID] = append(childrenByParent[*iss.ParentID], iss)
		}
	}

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

	epicSeqByProject := map[int64]int{}
	for _, pid := range parentIDs {
		parent, ok := issueByID[pid]
		if !ok {
			msg := fmt.Sprintf("issue parent_id=%d references missing issue", pid)
			report.BlockingIssues = append(report.BlockingIssues, msg)
			continue
		}
		children := childrenByParent[pid]
		sort.Slice(children, func(i, j int) bool { return children[i].ID < children[j].ID })
		childIDs := make([]int64, len(children))
		childKeys := make([]string, len(children))
		for idx, ch := range children {
			childIDs[idx] = ch.ID
			childKeys[idx] = ch.Key
		}
		projKey := projectByID[parent.ProjectID].Key
		epicSeqByProject[parent.ProjectID]++
		epicKey := fmt.Sprintf("%s-EPIC-%d", projKey, epicSeqByProject[parent.ProjectID])
		report.EpicPromotions = append(report.EpicPromotions, EpicPromotionCandidate{
			SourceIssueID:    parent.ID,
			SourceIssueKey:   parent.Key,
			ProjectID:        parent.ProjectID,
			ProjectKey:       projKey,
			Title:            parent.Title,
			IssueStatus:      parent.Status,
			TargetEpicKey:    epicKey,
			TargetEpicStatus: MapIssueStatusToEpicStatus(parent.Status),
			ChildCount:       len(children),
			ChildIssueIDs:    childIDs,
			ChildIssueKeys:   childKeys,
		})
		report.TotalAssignedChildIssues += len(children)
	}
	report.TotalPromotedEpics = len(report.EpicPromotions)

	// 2. Topic -> normalized issue_label mappings (deduplicated).
	topicPaths := BuildTopicPaths(topics)
	existingLabelsByIssue := make(map[int64]map[string]bool)
	for _, il := range issueLabels {
		if existingLabelsByIssue[il.IssueID] == nil {
			existingLabelsByIssue[il.IssueID] = map[string]bool{}
		}
		existingLabelsByIssue[il.IssueID][il.Label] = true
	}
	issuesByTopic := map[int64][]int64{}
	for _, iss := range issues {
		if iss.TopicID != nil {
			issuesByTopic[*iss.TopicID] = append(issuesByTopic[*iss.TopicID], iss.ID)
		}
	}
	for _, t := range topics {
		path := topicPaths[t.ID]
		label := NormalizeTopicLabel(t.ID, path, t.Name, opts.TopicLabelMap)
		issIDs := issuesByTopic[t.ID]
		dupCount := 0
		for _, iid := range issIDs {
			if existingLabelsByIssue[iid][label] {
				dupCount++
			}
		}
		report.TopicMappings = append(report.TopicMappings, TopicLabelMapping{
			TopicID:             t.ID,
			ProjectID:           t.ProjectID,
			ProjectKey:          projectByID[t.ProjectID].Key,
			TopicName:           t.Name,
			TopicPath:           path,
			TopicStatus:         t.Status,
			NormalizedLabel:     label,
			IssueCount:          len(issIDs),
			DuplicateLabelCount: dupCount,
		})
	}

	// 3. Milestone status conversion and review candidates.
	type msStats struct {
		total int
		done  int
		open  int
	}
	statsByMS := map[int64]*msStats{}
	for _, iss := range issues {
		if iss.MilestoneID == nil || iss.Status == "deleted" {
			continue
		}
		st := statsByMS[*iss.MilestoneID]
		if st == nil {
			st = &msStats{}
			statsByMS[*iss.MilestoneID] = st
		}
		st.total++
		switch iss.Status {
		case "done":
			st.done++
		case "backlog", "doing", "review":
			st.open++
		}
	}

	type liveMSKey struct {
		projectID int64
		name      string
	}
	liveMSByName := map[liveMSKey][]int64{}
	report.MilestoneSummary.Total = len(milestones)
	for _, m := range milestones {
		targetStatus, err := MapMilestoneStatus(m.Status)
		if err != nil {
			report.BlockingIssues = append(report.BlockingIssues, fmt.Sprintf("milestone id=%d: %v", m.ID, err))
			continue
		}
		convKey := m.Status + "->" + targetStatus
		report.MilestoneSummary.StatusConversions[convKey]++
		if targetStatus != "deleted" {
			k := liveMSKey{projectID: m.ProjectID, name: m.Name}
			liveMSByName[k] = append(liveMSByName[k], m.ID)
		}

		st := statsByMS[m.ID]
		var total, done, open int
		if st != nil {
			total, done, open = st.total, st.done, st.open
		}
		projKey := projectByID[m.ProjectID].Key

		if m.Status == "open" && total > 0 && open == 0 && done > 0 {
			report.MilestoneSummary.CompletedOpenCandidates = append(report.MilestoneSummary.CompletedOpenCandidates, MilestoneCandidate{
				MilestoneID: m.ID,
				ProjectKey:  projKey,
				Name:        m.Name,
				Status:      m.Status,
				TotalIssues: total,
				DoneIssues:  done,
				OpenIssues:  open,
				Reason:      "100%_completed_while_open",
			})
		}
		if m.Status != "deleted" && !IsVersionMilestoneName(m.Name) {
			report.MilestoneSummary.NonVersionCandidates = append(report.MilestoneSummary.NonVersionCandidates, MilestoneCandidate{
				MilestoneID: m.ID,
				ProjectKey:  projKey,
				Name:        m.Name,
				Status:      m.Status,
				TotalIssues: total,
				DoneIssues:  done,
				OpenIssues:  open,
				Reason:      "non_version_name",
			})
		}
	}
	for k, ids := range liveMSByName {
		if len(ids) > 1 {
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			report.MilestoneSummary.DuplicateLiveNames = append(report.MilestoneSummary.DuplicateLiveNames, DuplicateMilestoneName{
				ProjectKey:   projectByID[k.projectID].Key,
				Name:         k.name,
				MilestoneIDs: ids,
			})
			report.BlockingIssues = append(report.BlockingIssues,
				fmt.Sprintf("duplicate live milestone name %q in project %s (ids=%v)", k.name, projectByID[k.projectID].Key, ids))
		}
	}
	sort.Slice(report.MilestoneSummary.DuplicateLiveNames, func(i, j int) bool {
		if report.MilestoneSummary.DuplicateLiveNames[i].ProjectKey != report.MilestoneSummary.DuplicateLiveNames[j].ProjectKey {
			return report.MilestoneSummary.DuplicateLiveNames[i].ProjectKey < report.MilestoneSummary.DuplicateLiveNames[j].ProjectKey
		}
		return report.MilestoneSummary.DuplicateLiveNames[i].Name < report.MilestoneSummary.DuplicateLiveNames[j].Name
	})

	// 4. Document content checks (> 2 MiB, invalid UTF-8, NUL byte, sha256 mismatch, unparseable date).
	for _, d := range documents {
		projKey := projectByID[d.ProjectID].Key
		for _, reason := range checkDocumentContent(d.Content) {
			report.DocumentIssues = append(report.DocumentIssues, DocumentCheckIssue{
				DocumentID: d.ID,
				ProjectKey: projKey,
				Name:       d.Name,
				SizeBytes:  len(d.Content),
				Reason:     reason,
				Blocking:   true,
			})
			report.BlockingIssues = append(report.BlockingIssues,
				fmt.Sprintf("document id=%d (%s/%s): %s (size=%d)", d.ID, projKey, d.Name, reason, len(d.Content)))
		}
		if len(d.SHA256) != 64 || d.SHA256 != sha256Hex(d.Content) {
			report.DocumentIssues = append(report.DocumentIssues, DocumentCheckIssue{
				DocumentID: d.ID,
				ProjectKey: projKey,
				Name:       d.Name,
				SizeBytes:  len(d.Content),
				Reason:     "sha256_mismatch",
				Blocking:   false,
			})
		}
		if d.Date != nil && strings.TrimSpace(*d.Date) != "" {
			if _, ok := parseFlexibleTimestamp(*d.Date); !ok {
				report.DocumentIssues = append(report.DocumentIssues, DocumentCheckIssue{
					DocumentID: d.ID,
					ProjectKey: projKey,
					Name:       d.Name,
					SizeBytes:  len(d.Content),
					Reason:     "unparseable_date",
					Blocking:   false,
				})
			}
		}
	}

	// 5. Document relations: unresolved target_text & removed counts.
	for _, r := range docRels {
		if r.TargetText != nil {
			report.UnresolvedDocumentRelations++
		}
		if r.RemovedAt != nil {
			report.RemovedDocumentRelations++
		}
	}

	// 6. Resource activity topic rows count.
	for _, ra := range resActs {
		if ra.Resource == "topic" {
			report.TopicResourceActivityCount++
		}
	}

	// 7. Issue claims: duplicate active claims & missing actor references.
	openClaimsByIssue := map[int64][]int64{}
	missingActorSet := map[string]bool{}
	for _, c := range claims {
		if !actorSet[c.ActorID] {
			missingActorSet[c.ActorID] = true
		}
		if c.ReleasedAt == nil {
			openClaimsByIssue[c.IssueID] = append(openClaimsByIssue[c.IssueID], c.ID)
		}
	}
	for iid, cids := range openClaimsByIssue {
		if len(cids) > 1 {
			sort.Slice(cids, func(i, j int) bool { return cids[i] < cids[j] })
			report.DuplicateOpenClaims = append(report.DuplicateOpenClaims, DuplicateOpenClaim{
				IssueID:  iid,
				IssueKey: issueByID[iid].Key,
				ClaimIDs: cids,
			})
		}
	}
	sort.Slice(report.DuplicateOpenClaims, func(i, j int) bool {
		return report.DuplicateOpenClaims[i].IssueID < report.DuplicateOpenClaims[j].IssueID
	})
	for aid := range missingActorSet {
		report.MissingActors = append(report.MissingActors, aid)
	}
	sort.Strings(report.MissingActors)

	return report, nil
}

// FormatTable writes a human-readable summary of the plan report.
func (r *PlanReport) FormatTable(w io.Writer) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Migration Plan (Dry Run)\n")
	fmt.Fprintf(&buf, "Source: %s (SQLite schema v%d)\n\n", r.SourcePath, r.SchemaVersion)

	fmt.Fprintf(&buf, "== 24 SQLite Tables ==\n")
	tw := tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "TABLE\tTARGET\tACTION\tROWS\n")
	for _, t := range r.Tables {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", t.Name, t.Target, t.Action, t.RowCount)
	}
	_ = tw.Flush()

	fmt.Fprintf(&buf, "\n== Epic Promotions (1:1 Issue Preservation) ==\n")
	fmt.Fprintf(&buf, "Promoted Epics: %d | Assigned Child Issues: %d\n", r.TotalPromotedEpics, r.TotalAssignedChildIssues)
	if len(r.EpicPromotions) > 0 {
		tw = tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)
		fmt.Fprintf(tw, "SOURCE_ID\tSOURCE_KEY\tEPIC_KEY\tSTATUS\tCHILDREN\tTITLE\n")
		for _, ep := range r.EpicPromotions {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s->%s\t%d\t%s\n",
				ep.SourceIssueID, ep.SourceIssueKey, ep.TargetEpicKey, ep.IssueStatus, ep.TargetEpicStatus, ep.ChildCount, ep.Title)
		}
		_ = tw.Flush()
	}

	fmt.Fprintf(&buf, "\n== Topic -> Issue Label Mapping ==\n")
	if len(r.TopicMappings) == 0 {
		fmt.Fprintf(&buf, "(none)\n")
	} else {
		tw = tabwriter.NewWriter(&buf, 0, 2, 2, ' ', 0)
		fmt.Fprintf(tw, "TOPIC_ID\tPROJECT\tTOPIC_PATH\tLABEL\tISSUES\tDEDUP\n")
		for _, tm := range r.TopicMappings {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%d\n",
				tm.TopicID, tm.ProjectKey, tm.TopicPath, tm.NormalizedLabel, tm.IssueCount, tm.DuplicateLabelCount)
		}
		_ = tw.Flush()
	}

	fmt.Fprintf(&buf, "\n== Milestones ==\n")
	convKeys := make([]string, 0, len(r.MilestoneSummary.StatusConversions))
	for k := range r.MilestoneSummary.StatusConversions {
		convKeys = append(convKeys, k)
	}
	sort.Strings(convKeys)
	if len(convKeys) == 0 {
		fmt.Fprintf(&buf, "Total: 0\n")
	} else {
		parts := make([]string, 0, len(convKeys))
		for _, k := range convKeys {
			parts = append(parts, fmt.Sprintf("%s=%d", k, r.MilestoneSummary.StatusConversions[k]))
		}
		fmt.Fprintf(&buf, "Total: %d (%s)\n", r.MilestoneSummary.Total, strings.Join(parts, ", "))
	}
	fmt.Fprintf(&buf, "100%% Completed Open Candidates: %d\n", len(r.MilestoneSummary.CompletedOpenCandidates))
	for _, c := range r.MilestoneSummary.CompletedOpenCandidates {
		fmt.Fprintf(&buf, "  - [%s] %s (id=%d, done=%d/%d)\n", c.ProjectKey, c.Name, c.MilestoneID, c.DoneIssues, c.TotalIssues)
	}
	fmt.Fprintf(&buf, "Non-Version Milestone Candidates: %d\n", len(r.MilestoneSummary.NonVersionCandidates))
	for _, c := range r.MilestoneSummary.NonVersionCandidates {
		fmt.Fprintf(&buf, "  - [%s] %s (id=%d, status=%s, issues=%d)\n", c.ProjectKey, c.Name, c.MilestoneID, c.Status, c.TotalIssues)
	}

	fmt.Fprintf(&buf, "\n== Pre-Flight Checks ==\n")
	fmt.Fprintf(&buf, "Document Issues (>2MiB / non-UTF8 / NUL / hash / date): %d\n", len(r.DocumentIssues))
	for _, di := range r.DocumentIssues {
		fmt.Fprintf(&buf, "  - doc id=%d (%s/%s): %s (blocking=%v)\n", di.DocumentID, di.ProjectKey, di.Name, di.Reason, di.Blocking)
	}
	fmt.Fprintf(&buf, "Unresolved Document Relations (target_text): %d (removed: %d)\n", r.UnresolvedDocumentRelations, r.RemovedDocumentRelations)
	fmt.Fprintf(&buf, "Duplicate Open Issue Claims: %d\n", len(r.DuplicateOpenClaims))
	for _, dc := range r.DuplicateOpenClaims {
		fmt.Fprintf(&buf, "  - issue %s (id=%d): claim_ids=%v\n", dc.IssueKey, dc.IssueID, dc.ClaimIDs)
	}
	fmt.Fprintf(&buf, "Topic Resource Activity Rows (skipped): %d\n", r.TopicResourceActivityCount)
	fmt.Fprintf(&buf, "Blocking Issues: %d\n", len(r.BlockingIssues))
	for _, b := range r.BlockingIssues {
		fmt.Fprintf(&buf, "  - BLOCKING: %s\n", b)
	}

	_, err := io.WriteString(w, buf.String())
	return err
}

// String renders the plan report as a formatted table string.
func (r *PlanReport) String() string {
	var buf bytes.Buffer
	_ = r.FormatTable(&buf)
	return buf.String()
}
