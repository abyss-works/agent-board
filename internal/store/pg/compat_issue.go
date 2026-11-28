package pg

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

// CompatSort is one term of a requested ordering.
type CompatSort struct {
	Field string
	Desc  bool
}

// CompatIssueFilter selects, searches and orders issues across projects with the
// vocabulary of the classic issue list.
type CompatIssueFilter struct {
	Projects           []string
	Statuses           []model.IssueStatus
	Types              []model.IssueType
	Priorities         []model.Priority
	Labels             []string // AND
	Refs               []string // AND, exact "kind:value"
	Parent             *string  // "none" selects issues without a parent
	CreatedBy          []string
	UpdatedBy          []string
	StatusChangedBy    []string
	VerificationStates []model.VerificationState
	Dormant            *bool
	Topics             []int64 // topics were folded into labels; no issue carries one
	TopicNone          bool
	Milestones         []int64
	MilestoneNone      bool
	Query              string
	Sort               []CompatSort
	Limit              int
	Offset             int

	ClosedSince    *time.Time // board: only issues closed at or after this time
	DecisionNeeded bool       // overview: only issues that ask for a decision
	// Ready narrows to issues whose dependencies are all closed (true) or that
	// still wait on an open dependency (false); nil leaves it open.
	Ready *bool
	// ExcludeLabels leaves out issues that carry any of these labels.
	ExcludeLabels []string
}

// lastActivityExpr is the time of an issue's latest activity: its updated_at, or
// a later comment, which does not touch updated_at.
const lastActivityExpr = `GREATEST(i.updated_at, COALESCE((SELECT max(a.at) FROM activity a WHERE a.issue_id = i.id), i.updated_at))`

// openDepsExpr counts the dependencies of the issue that are still open.
const openDepsExpr = `(SELECT count(*) FROM graph_edge d JOIN issue di ON di.id = d.target_id
	WHERE d.kind = 'depends_on' AND d.status = 'confirmed' AND d.source_type = 'issue' AND d.target_type = 'issue'
	  AND d.source_id = i.id AND di.status NOT IN ('done', 'cancelled', 'deleted'))`

// blocksExpr counts the open issues that wait on the issue.
const blocksExpr = `(SELECT count(*) FROM graph_edge d JOIN issue bi ON bi.id = d.source_id
	WHERE d.kind = 'depends_on' AND d.status = 'confirmed' AND d.source_type = 'issue' AND d.target_type = 'issue'
	  AND d.target_id = i.id AND bi.status NOT IN ('done', 'cancelled', 'deleted'))`

var compatIssueSortCols = map[string]string{
	"priority":   "i.priority",
	"created_at": "i.created_at",
	"updated_at": "i.updated_at",
	"key":        "p.key, i.number",
}

// CompatIssueResult is a page of issue summaries with the open-dependency count
// each carries.
type CompatIssueResult struct {
	Items    []model.IssueSummary
	Total    int
	OpenDeps []int
}

func (s *Store) compatWhere(f CompatIssueFilter, args *[]any) []string {
	var where []string
	add := func(v any) string {
		*args = append(*args, v)
		return fmt.Sprintf("$%d", len(*args))
	}
	in := func(col string, vals []string) {
		if len(vals) > 0 {
			where = append(where, col+" = ANY("+add(vals)+")")
		}
	}
	in("p.key", f.Projects)
	statuses := make([]string, len(f.Statuses))
	for i, v := range f.Statuses {
		statuses[i] = string(v)
	}
	in("i.status", statuses)
	types := make([]string, len(f.Types))
	for i, v := range f.Types {
		types[i] = string(v)
	}
	in("i.type", types)
	prios := make([]string, len(f.Priorities))
	for i, v := range f.Priorities {
		prios[i] = string(v)
	}
	in("i.priority", prios)
	in("i.created_by", f.CreatedBy)
	in("i.updated_by", f.UpdatedBy)
	in("i.status_changed_by", f.StatusChangedBy)
	vstates := make([]string, len(f.VerificationStates))
	for i, v := range f.VerificationStates {
		vstates[i] = string(v)
	}
	in("i.verification_state", vstates)
	for _, l := range f.Labels {
		where = append(where, "EXISTS (SELECT 1 FROM issue_label l WHERE l.issue_id = i.id AND l.label = "+add(l)+")")
	}
	if len(f.ExcludeLabels) > 0 {
		where = append(where, "NOT EXISTS (SELECT 1 FROM issue_label l WHERE l.issue_id = i.id AND l.label = ANY("+add(f.ExcludeLabels)+"))")
	}
	for _, r := range f.Refs {
		where = append(where, "EXISTS (SELECT 1 FROM issue_ref r WHERE r.issue_id = i.id AND r.ref = "+add(r)+")")
	}
	if f.Parent != nil {
		if *f.Parent == "none" {
			where = append(where, "(e.source_issue_key IS NULL OR e.source_issue_key = i.key)")
		} else {
			where = append(where, "(e.source_issue_key = "+add(*f.Parent)+" AND i.key <> e.source_issue_key)")
		}
	}
	// Topics were folded into labels, so no issue belongs to one.
	if len(f.Topics) > 0 && !f.TopicNone {
		where = append(where, "FALSE")
	}
	switch {
	case len(f.Milestones) > 0 && f.MilestoneNone:
		where = append(where, "(i.milestone_id IS NULL OR i.milestone_id = ANY("+add(f.Milestones)+"))")
	case len(f.Milestones) > 0:
		where = append(where, "i.milestone_id = ANY("+add(f.Milestones)+")")
	case f.MilestoneNone:
		where = append(where, "i.milestone_id IS NULL")
	}
	if f.Dormant != nil && *f.Dormant {
		where = append(where, "i.status IN ('backlog','doing','review') AND "+lastActivityExpr+" < "+add(s.now().Add(-model.IssueDormantAfter)))
	}
	if f.ClosedSince != nil {
		where = append(where, "i.closed_at >= "+add(*f.ClosedSince))
	}
	if f.DecisionNeeded {
		where = append(where, "i.decision_needed IS NOT NULL")
	}
	if f.Ready != nil {
		if *f.Ready {
			where = append(where, openDepsExpr+" = 0")
		} else {
			where = append(where, openDepsExpr+" > 0")
		}
	}
	for _, t := range searchTerms(f.Query) {
		p := add("%" + escapeLike(t) + "%")
		where = append(where, "(i.title ILIKE "+p+" OR i.body ILIKE "+p+" OR i.key ILIKE "+p+")")
	}
	return where
}

// searchTerms splits a classic search query: a quoted query is one phrase, otherwise
// every word is a term and all of them must match.
func searchTerms(q string) []string {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	if len(q) >= 2 && strings.HasPrefix(q, `"`) && strings.HasSuffix(q, `"`) {
		if inner := strings.TrimSpace(q[1 : len(q)-1]); inner != "" {
			return []string{inner}
		}
		return nil
	}
	var terms []string
	for _, t := range strings.Fields(q) {
		// Quote characters are search syntax, not text.
		if t = strings.ReplaceAll(t, `"`, ""); t != "" {
			terms = append(terms, t)
		}
	}
	return terms
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// CompatListIssues returns a page of issue summaries and the total count.
func (s *Store) CompatListIssues(ctx context.Context, f CompatIssueFilter) (CompatIssueResult, error) {
	var args []any
	where := s.compatWhere(f, &args)
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var res CompatIssueResult
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*)`+issueFrom+cond, args...).Scan(&res.Total); err != nil {
		return res, err
	}
	order := compatOrder(f)
	q := `SELECT ` + issueCols + `, ` + lastActivityExpr + `, ` + openDepsExpr + issueFrom + cond + order
	if f.Limit > 0 {
		args = append(args, f.Limit)
		q += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	if f.Offset > 0 {
		args = append(args, f.Offset)
		q += fmt.Sprintf(" OFFSET $%d", len(args))
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return res, err
	}
	var details []IssueDetail
	var last []time.Time
	var deps []int
	for rows.Next() {
		var la time.Time
		var od int
		r, err := scanIssueRowExtra(rows, &la, &od)
		if err != nil {
			rows.Close()
			return res, err
		}
		details = append(details, r.detail)
		last = append(last, la)
		deps = append(deps, od)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	ptrs := make([]*IssueDetail, len(details))
	for i := range details {
		ptrs[i] = &details[i]
	}
	if err := hydrateIssueCollections(ctx, s.pool, ptrs); err != nil {
		return res, err
	}
	now := model.NewTime(s.now())
	res.Items = make([]model.IssueSummary, len(details))
	res.OpenDeps = deps
	terms := searchTerms(f.Query)
	for i := range details {
		details[i].Dormant = model.IsDormant(details[i].Status, model.NewTime(last[i]), now)
		res.Items[i] = details[i].Issue.Summary()
		if len(terms) > 0 {
			sn := Snippet(details[i].Title+" "+details[i].Body, terms)
			res.Items[i].Snippet = &sn
		}
	}
	return res, nil
}

func compatOrder(f CompatIssueFilter) string {
	var terms []string
	for _, so := range f.Sort {
		if so.Field == "relevance" {
			continue
		}
		col, ok := compatIssueSortCols[so.Field]
		if !ok {
			continue
		}
		if so.Desc {
			parts := strings.Split(col, ", ")
			for i := range parts {
				parts[i] += " DESC"
			}
			col = strings.Join(parts, ", ")
		}
		terms = append(terms, col)
	}
	if len(terms) == 0 {
		terms = append(terms, "i.priority", "i.created_at")
	}
	tie := "i.id"
	if len(f.Sort) > 0 && f.Sort[0].Desc {
		tie += " DESC"
	}
	return " ORDER BY " + strings.Join(append(terms, tie), ", ")
}

// Snippet cuts a short window of text around the first matching term and marks the
// matches with square brackets.
func Snippet(text string, terms []string) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	lower := make([]string, len(words))
	for i, w := range words {
		lower[i] = strings.ToLower(w)
	}
	first := -1
	for i, w := range lower {
		for _, t := range terms {
			if strings.Contains(w, strings.ToLower(t)) {
				first = i
				break
			}
		}
		if first >= 0 {
			break
		}
	}
	start, end := 0, len(words)
	if first >= 0 {
		start = first - 6
		if start < 0 {
			start = 0
		}
		end = start + 16
		if end > len(words) {
			end = len(words)
		}
	} else if end > 16 {
		end = 16
	}
	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		w := words[i]
		for _, t := range terms {
			if strings.Contains(lower[i], strings.ToLower(t)) {
				w = "[" + w + "]"
				break
			}
		}
		out = append(out, w)
	}
	res := strings.Join(out, " ")
	if start > 0 {
		res = "..." + res
	}
	if end < len(words) {
		res += "..."
	}
	return res
}
