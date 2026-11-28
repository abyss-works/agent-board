package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/wsk0715/agent-board/internal/graph"
	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
	pgstore "github.com/wsk0715/agent-board/internal/store/pg"
)

// registerPGCompatRoutes serves, in PostgreSQL mode, the read and list endpoints and
// the issue write shape that existing clients use. Their responses keep the
// shape of the classic API; concepts the data model folded into others are
// answered from the new place (see compatIssue).
func (h *handler) registerPGCompatRoutes(mux *http.ServeMux) {
	route(mux, "/v1/templates", methods{"GET": h.listTemplates})
	route(mux, "/v1/issues", methods{"GET": h.pgCompatListIssues, "POST": h.pgCompatCreateIssue})
	route(mux, "/v1/issues/{key}/claims", methods{"GET": h.pgCompatIssueClaims})
	route(mux, "/v1/issues/{key}/activity", methods{"GET": h.pgCompatIssueActivity})
	route(mux, "/v1/activity", methods{"GET": h.pgCompatListActivity})
	route(mux, "/v1/projects/{key}/activity", methods{"GET": h.pgCompatProjectActivity})
	route(mux, "/v1/board", methods{"GET": h.pgCompatBoard})
	route(mux, "/v1/queue", methods{"GET": h.pgCompatQueue})
	route(mux, "/v1/labels", methods{"GET": h.pgCompatLabels})
	route(mux, "/v1/projects/{key}/overview", methods{"GET": h.pgCompatOverview})
	route(mux, "/v1/agenda", methods{"GET": h.pgCompatAgenda})
	route(mux, "/v1/digest", methods{"GET": h.pgCompatDigest})
	route(mux, "/v1/projects/{key}/topics", methods{"GET": h.pgCompatListTopics, "POST": h.pgCompatTopicGone})
	route(mux, "/v1/topics/{id}", methods{"PATCH": h.pgCompatTopicGone, "DELETE": h.pgCompatTopicGone})
}

func compatFilter(f store.IssueFilter) pgstore.CompatIssueFilter {
	out := pgstore.CompatIssueFilter{
		Projects: f.Projects, Statuses: f.Statuses, Types: f.Types, Priorities: f.Priorities,
		Labels: f.Labels, Refs: f.Refs, Parent: f.Parent, CreatedBy: f.CreatedBy, UpdatedBy: f.UpdatedBy,
		StatusChangedBy: f.StatusChangedBy, VerificationStates: f.VerificationStates, Dormant: f.Dormant,
		Topics: f.Topics, TopicNone: f.TopicNone, Milestones: f.Milestones, MilestoneNone: f.MilestoneNone,
		Query: f.Query, Limit: f.Limit, Offset: f.Offset,
	}
	for _, s := range f.Sort {
		out.Sort = append(out.Sort, pgstore.CompatSort{Field: s.Field, Desc: s.Desc})
	}
	return out
}

func (h *handler) pgCompatListIssues(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r, "label", "ref")
	f := issueListFilter(q, "open")
	f.Query = q.one("q")
	f.Sort = q.sorts("priority", "created_at", "updated_at", "key", "relevance")
	f.Limit, f.Offset = q.limit(50, 200), q.offset()
	hasQuery := false
	if f.Query != "" {
		m, err := store.BuildMatch(f.Query)
		if err != nil {
			q.add("q", "검색어는 최대 10항입니다.")
		}
		hasQuery = m != ""
	}
	for _, s := range f.Sort {
		if s.Field == "relevance" && !hasQuery {
			q.add("sort", "relevance는 q가 있을 때만 쓸 수 있습니다.")
		}
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	res, err := h.pgStore.CompatListIssues(r.Context(), compatFilter(f))
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(res.Items, res.Total, f.Limit, f.Offset))
}

// compatIssueFields are the fields the classic issue create accepts.
var compatIssueFields = withFields(issueWriteFields, "project")

func (h *handler) pgCompatCreateIssue(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, compatIssueFields)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var project string
	var in pgstore.CreateIssueInput
	var deps []string
	var parent *string
	var topic, milestone *int64
	var details []Detail
	if aerr := firstError(
		decodeField(b, "project", &project),
		decodeField(b, "title", &in.Title),
		decodeField(b, "body", &in.Body),
		decodeField(b, "type", &in.Type),
		decodeField(b, "status", &in.Status),
		decodeField(b, "priority", &in.Priority),
		decodeList(b, "labels", &in.Labels),
		decodeList(b, "refs", &in.Refs),
		decodeList(b, "depends_on", &deps),
		decodeOptional(b, "parent", &parent),
		decodeOptional(b, "topic", &topic),
		decodeOptional(b, "milestone", &milestone),
		decodeOptional(b, "recheck_condition", &in.RecheckCondition),
		decodeOptional(b, "decision_needed", &in.DecisionNeeded),
		decodeField(b, "verification_state", &in.VerificationState),
		decodeOptional(b, "verification_note", &in.VerificationNote),
	); aerr != nil {
		writeError(w, aerr)
		return
	}
	if project == "" {
		writeError(w, validationFailed([]Detail{{Field: "project", Message: "프로젝트 키가 필요합니다."}}))
		return
	}
	in.MilestoneID = milestone
	if topic != nil {
		details = append(details, Detail{Field: "topic", Reason: "topic_removed", Message: "주제는 라벨로 대체되었습니다. labels를 쓰세요."})
	}
	if len(details) > 0 {
		writeError(w, validationFailed(details))
		return
	}
	if parent != nil {
		epicID, err := h.epicOfParent(r, project, "", *parent)
		if err != nil {
			writeError(w, h.pgStoreError(r, err))
			return
		}
		in.EpicID = epicID
	}
	var plan *dependsPlan
	if len(deps) > 0 {
		var err error
		if plan, err = h.planDependsOn(r, "", deps); err != nil {
			writeError(w, h.pgStoreError(r, err))
			return
		}
	}
	res, err := h.pgStore.CreateIssue(r.Context(), TraceFrom(r.Context()), project, in)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	issue := res.Issue
	if plan != nil {
		if err := h.applyDependsOn(r, issue.Key, plan); err != nil {
			writeError(w, h.pgStoreError(r, err))
			return
		}
		if issue, err = h.pgStore.GetIssue(r.Context(), issue.Key); err != nil {
			writeError(w, h.pgStoreError(r, err))
			return
		}
	}
	w.Header().Set("Location", "/v1/issues/"+issue.Key)
	w.Header().Set("ETag", etag(issue.Version))
	writeJSON(w, http.StatusCreated, issue)
}

// epicOfParent maps a classic parent issue key to the epic that groups the children
// of that issue, creating the epic the first time a parent is used. issueKey is the
// issue that gets the parent ("" for an issue not created yet); a parent chain that
// leads back to it is a cycle.
func (h *handler) epicOfParent(r *http.Request, project, issueKey, parentKey string) (*int64, error) {
	ctx := r.Context()
	project = model.NormalizeProjectKey(project)
	var parentID int64
	var parentTitle, parentProject string
	err := h.pgStore.Pool().QueryRow(ctx, `SELECT i.id, i.title, p.key FROM issue i JOIN project p ON p.id = i.project_id WHERE i.key = $1`, parentKey).Scan(&parentID, &parentTitle, &parentProject)
	if err != nil || parentProject != project {
		return nil, &pgstore.RefError{Field: "parent", Key: parentKey}
	}
	cur := parentKey
	for depth := 0; depth < 64; depth++ {
		if cur == issueKey {
			return nil, graph.ErrCycle
		}
		var next *string
		if err := h.pgStore.Pool().QueryRow(ctx, `SELECT e.source_issue_key FROM issue i JOIN epic e ON e.id = i.epic_id WHERE i.key = $1`, cur).Scan(&next); err != nil || next == nil || *next == cur {
			break
		}
		cur = *next
	}
	var id int64
	err = h.pgStore.Pool().QueryRow(ctx, `SELECT e.id FROM epic e JOIN project p ON p.id = e.project_id
		WHERE p.key = $1 AND e.source_issue_key = $2 AND e.status <> 'deleted'`, project, parentKey).Scan(&id)
	if err == nil {
		return &id, nil
	}
	ep, err := h.pgStore.CreateEpic(ctx, TraceFrom(ctx), project, pgstore.CreateEpicInput{Title: parentTitle, SourceIssueID: &parentID, SourceIssueKey: &parentKey})
	if err != nil {
		return nil, err
	}
	return &ep.ID, nil
}

// dependsPlan is a validated change of the dependencies of an issue.
type dependsPlan struct {
	deps []string
	ids  map[string]int64
}

// planDependsOn checks, before anything is written, that every dependency exists and
// that none of them leads back to the issue srcKey ("" for an issue not created yet).
func (h *handler) planDependsOn(r *http.Request, srcKey string, deps []string) (*dependsPlan, error) {
	ctx := r.Context()
	plan := &dependsPlan{deps: deps, ids: map[string]int64{}}
	var srcID int64
	if srcKey != "" {
		if err := h.pgStore.Pool().QueryRow(ctx, `SELECT id FROM issue WHERE key = $1`, srcKey).Scan(&srcID); err != nil {
			return nil, pgstore.ErrNotFound
		}
	}
	for i, d := range deps {
		if d == srcKey && srcKey != "" {
			return nil, graph.ErrCycle
		}
		var tid int64
		if err := h.pgStore.Pool().QueryRow(ctx, `SELECT id FROM issue WHERE key = $1`, d).Scan(&tid); err != nil {
			return nil, &pgstore.RefError{Field: fmt.Sprintf("depends_on[%d]", i), Key: d}
		}
		plan.ids[d] = tid
		if srcKey == "" {
			continue
		}
		var cyclic bool
		if err := h.pgStore.Pool().QueryRow(ctx, `WITH RECURSIVE reach(id) AS (
				SELECT target_id FROM graph_edge
				WHERE kind = 'depends_on' AND status = 'confirmed' AND source_type = 'issue' AND target_type = 'issue' AND source_id = $1
				UNION
				SELECT e.target_id FROM graph_edge e JOIN reach r ON e.source_id = r.id
				WHERE e.kind = 'depends_on' AND e.status = 'confirmed' AND e.source_type = 'issue' AND e.target_type = 'issue'
			) SELECT EXISTS (SELECT 1 FROM reach WHERE id = $2)`, tid, srcID).Scan(&cyclic); err != nil {
			return nil, err
		}
		if cyclic {
			return nil, graph.ErrCycle
		}
	}
	return plan, nil
}

// applyDependsOn makes the confirmed depends_on edges of an issue equal the plan:
// new edges first, so a failure leaves the old ones in place, then the dropped ones.
func (h *handler) applyDependsOn(r *http.Request, key string, plan *dependsPlan) error {
	ctx := r.Context()
	var srcID, projectID int64
	if err := h.pgStore.Pool().QueryRow(ctx, `SELECT id, project_id FROM issue WHERE key = $1`, key).Scan(&srcID, &projectID); err != nil {
		return pgstore.ErrNotFound
	}
	rows, err := h.pgStore.Pool().Query(ctx, `SELECT ge.id, t.key FROM graph_edge ge JOIN issue t ON t.id = ge.target_id
		WHERE ge.kind = 'depends_on' AND ge.status = 'confirmed' AND ge.source_type = 'issue' AND ge.target_type = 'issue' AND ge.source_id = $1`, srcID)
	if err != nil {
		return err
	}
	existing := map[string]int64{}
	for rows.Next() {
		var id int64
		var k string
		if err := rows.Scan(&id, &k); err != nil {
			rows.Close()
			return err
		}
		existing[k] = id
	}
	rows.Close()
	want := map[string]bool{}
	for _, d := range plan.deps {
		want[d] = true
		if _, ok := existing[d]; ok {
			continue
		}
		if _, err := h.graphStore.UpsertExplicitEdge(ctx, pgstore.UpsertEdgeInput{
			ProjectID: projectID,
			Source:    pgstore.NodeRef{Type: model.GraphNodeIssue, ID: srcID},
			Target:    &pgstore.NodeRef{Type: model.GraphNodeIssue, ID: plan.ids[d]},
			Kind:      model.EdgeKindDependsOn,
		}); err != nil {
			return err
		}
	}
	actor := TraceFrom(ctx).Actor
	for k, id := range existing {
		if !want[k] {
			if _, err := h.graphStore.RemoveEdge(ctx, actor, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *handler) pgCompatIssueClaims(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	limit, offset := q.limit(50, 200), q.offset()
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.pgStore.CompatListIssueClaims(r.Context(), r.PathValue("key"), limit, offset)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, limit, offset))
}

func (h *handler) pgCompatIssueActivity(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := pgstore.CompatActivityFilter{Issues: []string{r.PathValue("key")}, Limit: q.limit(50, 200), Offset: q.offset()}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	if _, err := h.pgStore.GetIssue(r.Context(), f.Issues[0]); err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	items, total, err := h.pgStore.CompatListActivity(r.Context(), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}

func (h *handler) pgCompatListActivity(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	projects := q.csv("project")
	for i, p := range projects {
		p = model.NormalizeProjectKey(p)
		projects[i] = p
		if !model.ValidProjectKey(p) {
			q.add("project", "프로젝트 키 형식이 아닙니다.")
		}
	}
	f := pgstore.CompatActivityFilter{
		Projects:     projects,
		Issues:       q.csv("issue"),
		Actors:       q.csv("actor"),
		Sessions:     q.csv("session"),
		ClientHosts:  q.csv("client_host"),
		Kinds:        parseEnumList(q, "kind", model.ParseActivityKind),
		CommentKinds: parseEnumList(q, "comment_kind", model.ParseCommentKind),
		Field:        q.one("field"),
		Limit:        q.limit(50, 200),
		Offset:       q.offset(),
	}
	if t := q.time("since"); t != nil {
		v := t.Time()
		f.Since = &v
	}
	if t := q.time("until"); t != nil {
		v := t.Time()
		f.Until = &v
	}
	if f.Field != "" && !issueFields[f.Field] {
		q.add("field", "이슈 필드 이름이어야 합니다.")
	}
	switch q.one("sort") {
	case "", "-at":
	case "at":
		f.Asc = true
	default:
		q.add("sort", "at 또는 -at이어야 합니다.")
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.pgStore.CompatListActivity(r.Context(), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}

func (h *handler) pgCompatProjectActivity(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	limit, offset := q.limit(50, 200), q.offset()
	var kind model.ResourceKind
	if v := q.one("resource"); v != "" {
		k, ok := model.ParseResourceKind(v)
		if !ok {
			q.add("resource", fmt.Sprintf("%q는 허용되지 않는 값입니다.", v))
		}
		kind = k
	}
	var id int64
	if q.one("id") != "" {
		if kind == "" {
			q.add("id", "resource와 함께 써야 합니다.")
		}
		id = int64(q.intRange("id", 0, 1, 1<<31-1))
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.pgStore.CompatListResourceActivity(r.Context(), r.PathValue("key"), kind, id, limit, offset)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, limit, offset))
}

func (h *handler) pgCompatBoard(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	projects := h.projects(q)
	closedDays, limit := q.intRange("closed_days", 7, 0, 36500), q.limit(100, 200)
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	b, err := h.pgStore.CompatBoard(r.Context(), projects, closedDays, limit)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (h *handler) pgCompatQueue(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := pgstore.CompatIssueFilter{Projects: h.projects(q), Limit: q.limit(50, 200), Offset: q.offset()}
	statusParam := q.one("status")
	if statusParam == "" {
		statusParam = string(model.StatusBacklog)
	}
	statuses, err := model.ExpandIssueStatuses(statusParam)
	if err != nil {
		q.add("status", "상태 또는 open, closed, all이어야 합니다.")
	}
	f.Statuses = statuses
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.pgStore.CompatQueue(r.Context(), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}

func (h *handler) pgCompatLabels(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	projects := h.projects(q)
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, err := h.pgStore.CompatLabels(r.Context(), projects)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) pgCompatOverview(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	limit := q.limit(10, 50)
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	o, err := h.pgStore.CompatOverview(r.Context(), model.NormalizeProjectKey(r.PathValue("key")), limit)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (h *handler) pgCompatAgenda(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	projects := h.projects(q)
	limit := q.limit(10, 50)
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	a, err := h.pgStore.CompatAgenda(r.Context(), projects, limit)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *handler) pgCompatDigest(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	projects := h.projects(q)
	since, until := q.time("since"), q.time("until")
	if since == nil && q.one("since") == "" {
		q.add("since", "필수입니다.")
	}
	limit := q.limit(50, 200)
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	var u *time.Time
	if until != nil {
		v := until.Time()
		u = &v
	}
	d, err := h.pgStore.CompatDigest(r.Context(), projects, since.Time(), u, limit)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// pgCompatListTopics answers the topic list of the classic API. Topics were folded
// into labels, so no project has any.
func (h *handler) pgCompatListTopics(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	q.one("status")
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	if _, err := h.pgStore.GetProject(r.Context(), r.PathValue("key")); err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
}

func (h *handler) pgCompatTopicGone(w http.ResponseWriter, r *http.Request) {
	writeError(w, &apiError{
		Status:  http.StatusGone,
		Code:    "topic_removed",
		Message: "주제는 라벨로 대체되었습니다. 이슈의 labels를 쓰세요.",
	})
}
