package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

// listOf is the list envelope of the API.
type listOf[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func decode[T any](resp *client.Response) (T, error) {
	var v T
	if err := resp.Decode(&v); err != nil {
		return v, fmt.Errorf("응답을 해석하지 못했습니다: %w", err)
	}
	return v, nil
}

// footer notes when a list shows only part of the total.
func footer(w io.Writer, shown, total, offset int) {
	if total > shown || offset > 0 {
		fmt.Fprintf(w, "%d건 중 %d건 표시 (offset %d)\n", total, shown, offset)
	}
}

// addDecisionColumn ends tb with a DECISION column when some row has a
// decision question. Row i of tb is the one for questions[i]. As the last
// column, a long question is cut to the terminal width by the table.
func addDecisionColumn(tb *table, questions []*string) {
	shown := false
	for _, q := range questions {
		shown = shown || q != nil
	}
	if !shown {
		return
	}
	tb.header = append(tb.header, "DECISION")
	for i, q := range questions {
		tb.rows[i] = append(tb.rows[i], deref(q, "-"))
	}
}

// issueTable lists issues. The MILESTONE column appears only when some issue
// has a milestone, the SNIPPET column only for search results and the
// DECISION column only when some issue asks for a decision.
func (a *App) issueTable(items []model.IssueSummary) *table {
	snippet, milestone := false, false
	for _, it := range items {
		if it.Snippet != nil {
			snippet = true
		}
		if it.MilestoneName != nil {
			milestone = true
		}
	}
	tb := &table{header: []string{"KEY", "STATUS", "PRI", "TYPE", "LABELS"}}
	if milestone {
		tb.header = append(tb.header, "MILESTONE")
	}
	tb.header = append(tb.header, "UPDATED", "TITLE")
	if snippet {
		tb.header = append(tb.header, "SNIPPET")
	}
	questions := make([]*string, 0, len(items))
	for _, it := range items {
		row := []string{it.Key, string(it.Status), string(it.Priority), string(it.Type), joinOr(it.Labels, "-")}
		if milestone {
			row = append(row, deref(it.MilestoneName, "-"))
		}
		row = append(row, formatTime(it.UpdatedAt, a.Location), it.Title)
		if snippet {
			row = append(row, deref(it.Snippet, ""))
		}
		tb.add(row...)
		questions = append(questions, it.DecisionNeeded)
	}
	addDecisionColumn(tb, questions)
	return tb
}

func (a *App) renderIssueList(w io.Writer, resp *client.Response) error {
	l, err := decode[listOf[model.IssueSummary]](resp)
	if err != nil {
		return err
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(w, "이슈가 없습니다.")
		return nil
	}
	a.issueTable(l.Items).write(w, a.TermWidth)
	footer(w, len(l.Items), l.Total, l.Offset)
	return nil
}

func (a *App) renderIssue(w io.Writer, resp *client.Response) error {
	i, err := decode[model.Issue](resp)
	if err != nil {
		return err
	}
	pairs := [][2]string{
		{"key", i.Key},
		{"title", i.Title},
		{"project", i.Project},
		{"type", string(i.Type)},
		{"status", string(i.Status)},
		{"status_reason", deref(i.StatusReason, "-")},
		{"acknowledged", a.acknowledgedText(i.AcknowledgedAt, i.AcknowledgedBy)},
		{"priority", string(i.Priority)},
		{"labels", joinOr(i.Labels, "-")},
		{"parent", deref(i.Parent, "-")},
		{"depends_on", joinOr(i.DependsOn, "-")},
		{"refs", joinOr(i.Refs, "-")},
		{"recheck_condition", deref(i.RecheckCondition, "-")},
	}
	if i.DecisionNeeded != nil {
		pairs = append(pairs, [2]string{"결정 필요", *i.DecisionNeeded})
	}
	pairs = append(pairs, [][2]string{
		{"topic", topicText(i.Topic, i.TopicPath)},
		{"milestone", milestoneText(i.Milestone, i.MilestoneName)},
		{"version", strconv.FormatInt(i.Version, 10)},
		{"created", formatTime(i.CreatedAt, a.Location) + " (" + i.CreatedBy + ")"},
		{"updated", formatTime(i.UpdatedAt, a.Location) + " (" + i.UpdatedBy + ")"},
		{"status_changed", formatTime(i.StatusChangedAt, a.Location) + " (" + i.StatusChangedBy + ")"},
		{"closed", formatTimePtr(i.ClosedAt, a.Location)},
	}...)
	fields(w, pairs)
	if strings.TrimSpace(i.Body) != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, strings.TrimRight(i.Body, "\n"))
	}
	return nil
}

// topicText shows the topic path of an issue with its id, or "-".
func topicText(id *int64, path *string) string {
	if id == nil || path == nil {
		return "-"
	}
	return *path + " (" + strconv.FormatInt(*id, 10) + ")"
}

// milestoneText shows the milestone name of an issue with its id, or "-".
func milestoneText(id *int64, name *string) string {
	if id == nil || name == nil {
		return "-"
	}
	return *name + " (" + strconv.FormatInt(*id, 10) + ")"
}

// acknowledgedText says whether the user has seen an issue: "no", or "yes"
// with the time and, when known, who.
func (a *App) acknowledgedText(at *model.Time, by *string) string {
	if at == nil {
		return "no"
	}
	if by == nil {
		return "yes (" + formatTime(*at, a.Location) + ")"
	}
	return "yes (" + formatTime(*at, a.Location) + ", " + *by + ")"
}

// rawString shows a JSON value compactly: strings without quotes, the
// rest as JSON. An absent value and null, such as the milestone of an issue
// that has none, show as "-".
func rawString(r json.RawMessage) string {
	if len(r) == 0 || string(r) == "null" {
		return "-"
	}
	var s string
	if json.Unmarshal(r, &s) == nil {
		return s
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, r); err != nil {
		return string(r)
	}
	return buf.String()
}

// describeActivity summarizes one record in a line.
func describeActivity(act model.Activity) string {
	switch act.Kind {
	case model.ActivityCommented:
		if act.Summary != nil {
			return *act.Summary
		}
		body := deref(act.Body, "")
		if i := strings.IndexByte(body, '\n'); i >= 0 {
			body = body[:i] + " …"
		}
		return body
	case model.ActivityCreated:
		if c, ok := act.Changes["title"]; ok {
			return "title: " + rawString(c.To)
		}
		return "created"
	}
	keys := make([]string, 0, len(act.Changes))
	for k := range act.Changes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		c := act.Changes[k]
		parts = append(parts, k+": "+rawString(c.From)+" → "+rawString(c.To))
	}
	return strings.Join(parts, "; ")
}

func (a *App) activityTable(items []model.Activity) *table {
	tb := &table{header: []string{"AT", "ACTOR", "KIND", "ISSUE", "DETAIL"}}
	for _, it := range items {
		tb.add(formatTime(it.At, a.Location), it.Actor, string(it.Kind), it.Issue, describeActivity(it))
	}
	return tb
}

func (a *App) renderActivityList(w io.Writer, resp *client.Response) error {
	l, err := decode[listOf[model.Activity]](resp)
	if err != nil {
		return err
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(w, "활동 기록이 없습니다.")
		return nil
	}
	a.activityTable(l.Items).write(w, a.TermWidth)
	footer(w, len(l.Items), l.Total, l.Offset)
	return nil
}

func (a *App) renderActivity(w io.Writer, resp *client.Response) error {
	act, err := decode[model.Activity](resp)
	if err != nil {
		return err
	}
	pairs := [][2]string{
		{"id", strconv.FormatInt(act.ID, 10)},
		{"issue", act.Issue},
		{"kind", string(act.Kind)},
		{"actor", act.Actor + " (" + act.SessionID + ", " + act.ClientHost + ")"},
		{"at", formatTime(act.At, a.Location)},
	}
	if act.Summary != nil {
		pairs = append(pairs, [2]string{"summary", *act.Summary})
	}
	fields(w, pairs)
	if act.Body != nil {
		fmt.Fprintln(w)
		fmt.Fprintln(w, strings.TrimRight(*act.Body, "\n"))
	} else if len(act.Changes) > 0 {
		fmt.Fprintln(w, "changes:", describeActivity(act))
	}
	return nil
}

func (a *App) queueTable(items []model.QueueItem) *table {
	tb := &table{header: []string{"KEY", "PRI", "STATUS", "READY", "BLOCKED_BY", "TITLE"}}
	questions := make([]*string, 0, len(items))
	for _, it := range items {
		ready := "yes"
		if !it.Ready {
			ready = "no"
		}
		tb.add(it.Key, string(it.Priority), string(it.Status), ready, joinOr(it.BlockedBy, "-"), it.Title)
		questions = append(questions, it.DecisionNeeded)
	}
	addDecisionColumn(tb, questions)
	return tb
}

func (a *App) renderQueue(w io.Writer, resp *client.Response) error {
	l, err := decode[listOf[model.QueueItem]](resp)
	if err != nil {
		return err
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(w, "큐가 비어 있습니다.")
		return nil
	}
	a.queueTable(l.Items).write(w, a.TermWidth)
	footer(w, len(l.Items), l.Total, l.Offset)
	return nil
}

type boardColumn struct {
	Status model.IssueStatus    `json:"status"`
	Total  int                  `json:"total"`
	Issues []model.IssueSummary `json:"issues"`
}

type board struct {
	Project *string       `json:"project"`
	Columns []boardColumn `json:"columns"`
}

func (a *App) renderBoard(w io.Writer, resp *client.Response) error {
	b, err := decode[board](resp)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "project: %s\n", deref(b.Project, "(전체)"))
	for _, col := range b.Columns {
		fmt.Fprintf(w, "\n== %s (%d) ==\n", col.Status, col.Total)
		if len(col.Issues) == 0 {
			continue
		}
		tb := &table{header: []string{"KEY", "PRI", "TYPE", "LABELS", "TITLE"}}
		questions := make([]*string, 0, len(col.Issues))
		for _, it := range col.Issues {
			tb.add(it.Key, string(it.Priority), string(it.Type), joinOr(it.Labels, "-"), it.Title)
			questions = append(questions, it.DecisionNeeded)
		}
		addDecisionColumn(tb, questions)
		tb.write(w, a.TermWidth)
		if col.Total > len(col.Issues) {
			fmt.Fprintf(w, "… 외 %d건\n", col.Total-len(col.Issues))
		}
	}
	return nil
}

type labelCount struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

func (a *App) renderLabels(w io.Writer, resp *client.Response) error {
	l, err := decode[struct {
		Items []labelCount `json:"items"`
	}](resp)
	if err != nil {
		return err
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(w, "라벨이 없습니다.")
		return nil
	}
	tb := &table{header: []string{"LABEL", "COUNT"}}
	for _, it := range l.Items {
		tb.add(it.Label, strconv.Itoa(it.Count))
	}
	tb.write(w, a.TermWidth)
	return nil
}

func (a *App) renderProjectList(w io.Writer, resp *client.Response) error {
	l, err := decode[listOf[model.Project]](resp)
	if err != nil {
		return err
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(w, "프로젝트가 없습니다.")
		return nil
	}
	tb := &table{header: []string{"KEY", "STATUS", "UPDATED", "NAME"}}
	for _, p := range l.Items {
		tb.add(p.Key, string(p.Status), formatTime(p.UpdatedAt, a.Location), p.Name)
	}
	tb.write(w, a.TermWidth)
	footer(w, len(l.Items), l.Total, l.Offset)
	return nil
}

func (a *App) projectFields(p model.Project) [][2]string {
	return [][2]string{
		{"key", p.Key},
		{"name", p.Name},
		{"status", string(p.Status)},
		{"created", formatTime(p.CreatedAt, a.Location) + " (" + p.CreatedBy + ")"},
		{"updated", formatTime(p.UpdatedAt, a.Location) + " (" + p.UpdatedBy + ")"},
	}
}

func (a *App) renderProject(w io.Writer, resp *client.Response) error {
	p, err := decode[model.Project](resp)
	if err != nil {
		return err
	}
	fields(w, a.projectFields(p))
	return nil
}

type section[T any] struct {
	Total int `json:"total"`
	Items []T `json:"items"`
}

type overview struct {
	Project     model.Project `json:"project"`
	GeneratedAt model.Time    `json:"generated_at"`
	Counts      struct {
		Total      int                       `json:"total"`
		Open       int                       `json:"open"`
		ByStatus   map[model.IssueStatus]int `json:"by_status"`
		ByType     map[model.IssueType]int   `json:"by_type"`
		ByPriority map[model.Priority]int    `json:"by_priority"`
	} `json:"counts"`
	Doing          section[model.IssueSummary] `json:"doing"`
	Review         section[model.IssueSummary] `json:"review"`
	Blocked        section[model.QueueItem]    `json:"blocked"`
	Decisions      section[model.IssueSummary] `json:"decisions"`
	Next           section[model.QueueItem]    `json:"next"`
	Unacknowledged section[model.IssueSummary] `json:"unacknowledged"`
	RecentActivity section[model.Activity]     `json:"recent_activity"`
}

func counts[K ~string](order []K, m map[K]int) string {
	parts := make([]string, 0, len(order))
	for _, k := range order {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

func (a *App) renderOverview(w io.Writer, resp *client.Response) error {
	o, err := decode[overview](resp)
	if err != nil {
		return err
	}
	fields(w, append(a.projectFields(o.Project), [][2]string{
		{"generated_at", formatTime(o.GeneratedAt, a.Location)},
		{"issues", fmt.Sprintf("total %d, open %d", o.Counts.Total, o.Counts.Open)},
		{"by_status", counts(model.IssueStatuses, o.Counts.ByStatus)},
		{"by_type", counts(model.IssueTypes, o.Counts.ByType)},
		{"by_priority", counts(model.Priorities, o.Counts.ByPriority)},
	}...))
	issues := func(name string, s section[model.IssueSummary]) {
		fmt.Fprintf(w, "\n== %s (%d) ==\n", name, s.Total)
		if len(s.Items) > 0 {
			a.issueTable(s.Items).write(w, a.TermWidth)
		}
	}
	queue := func(name string, s section[model.QueueItem]) {
		fmt.Fprintf(w, "\n== %s (%d) ==\n", name, s.Total)
		if len(s.Items) > 0 {
			a.queueTable(s.Items).write(w, a.TermWidth)
		}
	}
	issues("doing", o.Doing)
	issues("review", o.Review)
	queue("blocked", o.Blocked)
	issues("decisions", o.Decisions)
	queue("next", o.Next)
	issues("unacknowledged", o.Unacknowledged)
	fmt.Fprintf(w, "\n== recent_activity (%d) ==\n", o.RecentActivity.Total)
	if len(o.RecentActivity.Items) > 0 {
		a.activityTable(o.RecentActivity.Items).write(w, a.TermWidth)
	}
	return nil
}

func (a *App) renderHealth(w io.Writer, resp *client.Response) error {
	h, err := decode[client.Health](resp)
	if err != nil {
		return err
	}
	fields(w, [][2]string{{"status", h.Status}, {"version", h.Version}, {"api_version", strconv.Itoa(h.APIVersion)}})
	return nil
}

func (a *App) renderActor(w io.Writer, resp *client.Response) error {
	act, err := decode[model.Actor](resp)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%s (%s)\n", act.ID, act.Kind)
	return nil
}

func (a *App) renderActors(w io.Writer, resp *client.Response) error {
	l, err := decode[struct {
		Items []model.Actor `json:"items"`
	}](resp)
	if err != nil {
		return err
	}
	tb := &table{header: []string{"ID", "KIND"}}
	for _, it := range l.Items {
		tb.add(it.ID, string(it.Kind))
	}
	tb.write(w, a.TermWidth)
	return nil
}
