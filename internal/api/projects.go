package api

import (
	"net/http"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func (h *handler) registerProjects(mux *http.ServeMux) {
	route(mux, "/v1/projects", methods{"GET": h.listProjects, "POST": h.createProject})
	route(mux, "/v1/projects/{key}", methods{"GET": h.getProject, "PATCH": h.patchProject, "DELETE": h.deleteProject})
}

func (h *handler) createProject(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{"key": true, "name": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var key, name string
	for _, f := range []struct {
		name string
		dst  *string
	}{{"key", &key}, {"name", &name}} {
		if _, _, aerr := field(b, f.name, f.dst); aerr != nil {
			writeError(w, aerr)
			return
		}
	}
	p, err := h.store.CreateProject(r.Context(), TraceFrom(r.Context()), key, name)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	w.Header().Set("Location", "/v1/projects/"+p.Key)
	writeJSON(w, http.StatusCreated, p)
}

func (h *handler) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := h.store.GetProject(r.Context(), r.PathValue("key"))
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *handler) listProjects(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := store.ProjectFilter{Sort: q.sorts("key", "created_at", "updated_at"), Limit: q.limit(50, 200), Offset: q.offset()}
	statusParam := q.one("status")
	switch statusParam {
	case "":
		f.Statuses = []model.ProjectStatus{model.ProjectActive}
	case "all":
		f.Statuses = model.ProjectStatuses
	default:
		f.Statuses = parseEnumList(q, "status", model.ParseProjectStatus)
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.store.ListProjects(r.Context(), f)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}

func (h *handler) patchProject(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{"name": true, "status": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var patch store.ProjectPatch
	var name string
	present, null, aerr := field(b, "name", &name)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if present {
		if null {
			writeError(w, validationFailed([]Detail{{Field: "name", Message: "null일 수 없습니다."}}))
			return
		}
		patch.Name = &name
	}
	var status model.ProjectStatus
	present, null, aerr = field(b, "status", &status)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if present {
		if null {
			writeError(w, validationFailed([]Detail{{Field: "status", Message: "null일 수 없습니다."}}))
			return
		}
		patch.Status = &status
	}
	h.applyProjectPatch(w, r, patch)
}

func (h *handler) deleteProject(w http.ResponseWriter, r *http.Request) {
	deleted := model.ProjectDeleted
	h.applyProjectPatch(w, r, store.ProjectPatch{Status: &deleted})
}

func (h *handler) applyProjectPatch(w http.ResponseWriter, r *http.Request, patch store.ProjectPatch) {
	p, err := h.store.UpdateProject(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), patch)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, p)
}
