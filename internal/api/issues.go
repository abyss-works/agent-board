package api

import (
	"net/http"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func (h *handler) registerIssues(mux *http.ServeMux) {
	route(mux, "/v1/issues", methods{"GET": h.listIssues, "POST": h.createIssue})
	route(mux, "/v1/issues/{key}", methods{"GET": h.getIssue, "PATCH": h.patchIssue, "DELETE": h.deleteIssue})
	route(mux, "/v1/issues/{key}/comments", methods{"POST": h.createComment})
	route(mux, "/v1/issues/{key}/activity", methods{"GET": h.issueActivity})
	route(mux, "/v1/activity", methods{"GET": h.listActivity})
}

var issueWriteFields = map[string]bool{
	"title": true, "body": true, "type": true, "status": true, "priority": true,
	"labels": true, "parent": true, "depends_on": true, "refs": true, "recheck_condition": true,
}

// issueFields are the activity change fields accepted by the field filter.
var issueFields = issueWriteFields

func (h *handler) createIssue(w http.ResponseWriter, r *http.Request) {
	allowed := map[string]bool{"project": true}
	for k := range issueWriteFields {
		allowed[k] = true
	}
	b, aerr := readBody(w, r, allowed)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var in store.IssueInput
	var parent, recheck string
	// Nulls on non-nullable fields are left at their zero value so that
	// validation reports them (an empty title, for example).
	steps := []func() *apiError{
		func() *apiError { _, _, e := field(b, "project", &in.Project); return e },
		func() *apiError { _, _, e := field(b, "title", &in.Title); return e },
		func() *apiError { _, _, e := field(b, "body", &in.Body); return e },
		func() *apiError { _, _, e := field(b, "type", &in.Type); return e },
		func() *apiError { _, _, e := field(b, "status", &in.Status); return e },
		func() *apiError { _, _, e := field(b, "priority", &in.Priority); return e },
		func() *apiError { _, _, e := field(b, "labels", &in.Labels); return e },
		func() *apiError { _, _, e := field(b, "depends_on", &in.DependsOn); return e },
		func() *apiError { _, _, e := field(b, "refs", &in.Refs); return e },
		func() *apiError {
			present, null, e := field(b, "parent", &parent)
			if present && !null {
				in.Parent = &parent
			}
			return e
		},
		func() *apiError {
			present, null, e := field(b, "recheck_condition", &recheck)
			if present && !null {
				in.RecheckCondition = &recheck
			}
			return e
		},
	}
	for _, step := range steps {
		if aerr := step(); aerr != nil {
			writeError(w, aerr)
			return
		}
	}
	if in.Project == "" {
		writeError(w, validationFailed([]Detail{{Field: "project", Message: "프로젝트 키가 필요합니다."}}))
		return
	}
	issue, err := h.store.CreateIssue(r.Context(), TraceFrom(r.Context()), in)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	w.Header().Set("Location", "/v1/issues/"+issue.Key)
	w.Header().Set("ETag", etag(issue.Version))
	writeJSON(w, http.StatusCreated, issue)
}

func (h *handler) getIssue(w http.ResponseWriter, r *http.Request) {
	issue, err := h.store.GetIssue(r.Context(), r.PathValue("key"))
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	w.Header().Set("ETag", etag(issue.Version))
	writeJSON(w, http.StatusOK, issue)
}

func (h *handler) patchIssue(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, issueWriteFields)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var patch store.IssuePatch
	var nulls []Detail
	nonNull := func(name string, present, null bool, set func()) {
		if !present {
			return
		}
		if null {
			nulls = append(nulls, Detail{Field: name, Message: "null일 수 없습니다."})
			return
		}
		set()
	}
	var title, bodyText string
	var typ model.IssueType
	var status model.IssueStatus
	var prio model.Priority
	var labels, deps, refs []string
	var parent, recheck string
	steps := []func() *apiError{
		func() *apiError {
			p, n, e := field(b, "title", &title)
			nonNull("title", p, n, func() { patch.Title = &title })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "body", &bodyText)
			nonNull("body", p, n, func() { patch.Body = &bodyText })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "type", &typ)
			nonNull("type", p, n, func() { patch.Type = &typ })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "status", &status)
			nonNull("status", p, n, func() { patch.Status = &status })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "priority", &prio)
			nonNull("priority", p, n, func() { patch.Priority = &prio })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "labels", &labels)
			nonNull("labels", p, n, func() { patch.Labels = &labels })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "depends_on", &deps)
			nonNull("depends_on", p, n, func() { patch.DependsOn = &deps })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "refs", &refs)
			nonNull("refs", p, n, func() { patch.Refs = &refs })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "parent", &parent)
			if p {
				patch.Parent = store.Optional[string]{Set: true}
				if !n {
					patch.Parent.Value = &parent
				}
			}
			return e
		},
		func() *apiError {
			p, n, e := field(b, "recheck_condition", &recheck)
			if p {
				patch.RecheckCondition = store.Optional[string]{Set: true}
				if !n {
					patch.RecheckCondition.Value = &recheck
				}
			}
			return e
		},
	}
	for _, step := range steps {
		if aerr := step(); aerr != nil {
			writeError(w, aerr)
			return
		}
	}
	if len(nulls) > 0 {
		writeError(w, validationFailed(nulls))
		return
	}
	h.applyIssuePatch(w, r, patch)
}

func (h *handler) deleteIssue(w http.ResponseWriter, r *http.Request) {
	deleted := model.StatusDeleted
	h.applyIssuePatch(w, r, store.IssuePatch{Status: &deleted})
}

func (h *handler) applyIssuePatch(w http.ResponseWriter, r *http.Request, patch store.IssuePatch) {
	version, aerr := ifMatch(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	issue, _, err := h.store.UpdateIssue(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), patch, version)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	w.Header().Set("ETag", etag(issue.Version))
	writeJSON(w, http.StatusOK, issue)
}

// issueListFilter parses the shared issue filters of /v1/issues and the
// views. statusDefault is used when the status parameter is absent.
func issueListFilter(q *query, statusDefault string) store.IssueFilter {
	f := store.IssueFilter{
		Projects:        q.csv("project"),
		Types:           parseEnumList(q, "type", model.ParseIssueType),
		Priorities:      parseEnumList(q, "priority", model.ParsePriority),
		CreatedBy:       q.csv("created_by"),
		UpdatedBy:       q.csv("updated_by"),
		StatusChangedBy: q.csv("status_changed_by"),
	}
	for _, p := range f.Projects {
		if !model.ValidProjectKey(p) {
			q.add("project", "프로젝트 키 형식이 아닙니다.")
		}
	}
	for _, l := range q.repeated("label") {
		if !model.ValidLabel(l) {
			q.add("label", "라벨 형식이 아닙니다.")
		}
		f.Labels = append(f.Labels, l)
	}
	statusParam := q.one("status")
	if statusParam == "" {
		statusParam = statusDefault
	}
	statuses, err := model.ExpandIssueStatuses(statusParam)
	if err != nil {
		q.add("status", "상태 또는 open, closed, all이어야 합니다.")
	}
	f.Statuses = statuses
	if p := q.one("parent"); p != "" {
		if _, _, ok := model.ParseIssueKey(p); !ok && p != "none" {
			q.add("parent", "이슈 키 또는 none이어야 합니다.")
		}
		f.Parent = &p
	}
	return f
}

func (h *handler) listIssues(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r, "label")
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
	items, total, err := h.store.ListIssues(r.Context(), f)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}

func (h *handler) createComment(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{"body": true, "summary": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var text, summaryText string
	if _, _, aerr := field(b, "body", &text); aerr != nil {
		writeError(w, aerr)
		return
	}
	var summary *string
	present, null, aerr := field(b, "summary", &summaryText)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if present && !null {
		summary = &summaryText
	}
	a, err := h.store.AddComment(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), text, summary)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (h *handler) issueActivity(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := store.ActivityFilter{Issues: []string{r.PathValue("key")}, Limit: q.limit(50, 200), Offset: q.offset()}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	if _, err := h.store.GetIssue(r.Context(), f.Issues[0]); err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	items, total, err := h.store.ListActivity(r.Context(), f)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}

func (h *handler) listActivity(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := store.ActivityFilter{
		Projects:    q.csv("project"),
		Issues:      q.csv("issue"),
		Actors:      q.csv("actor"),
		Sessions:    q.csv("session"),
		ClientHosts: q.csv("client_host"),
		Kinds:       parseEnumList(q, "kind", model.ParseActivityKind),
		Field:       q.one("field"),
		Since:       q.time("since"),
		Until:       q.time("until"),
		Limit:       q.limit(50, 200),
		Offset:      q.offset(),
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
	items, total, err := h.store.ListActivity(r.Context(), f)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}
