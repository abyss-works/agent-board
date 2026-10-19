package api

import (
	"net/http"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func (h *handler) registerViews(mux *http.ServeMux) {
	route(mux, "/v1/board", methods{"GET": h.board})
	route(mux, "/v1/queue", methods{"GET": h.queue})
	route(mux, "/v1/labels", methods{"GET": h.labels})
	route(mux, "/v1/projects/{key}/overview", methods{"GET": h.overview})
}

func (h *handler) projects(q *query) []string {
	projects := q.csv("project")
	for _, p := range projects {
		if !model.ValidProjectKey(p) {
			q.add("project", "프로젝트 키 형식이 아닙니다.")
		}
	}
	return projects
}

func (h *handler) board(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	p := store.BoardParams{Projects: h.projects(q), ClosedDays: q.intRange("closed_days", 7, 0, 36500), Limit: q.limit(100, 200)}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	b, err := h.store.Board(r.Context(), p)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (h *handler) queue(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := store.QueueFilter{Projects: h.projects(q), Limit: q.limit(50, 200), Offset: q.offset()}
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
	items, total, err := h.store.Queue(r.Context(), f)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}

func (h *handler) labels(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	projects := h.projects(q)
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, err := h.store.Labels(r.Context(), projects)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) overview(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	limit := q.limit(10, 50)
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	o, err := h.store.Overview(r.Context(), r.PathValue("key"), limit)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, o)
}
