package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func (h *handler) registerMilestones(mux *http.ServeMux) {
	route(mux, "/v1/projects/{key}/milestones", methods{"GET": h.listMilestones, "POST": h.createMilestone})
	route(mux, "/v1/milestones/{id}", methods{"GET": h.getMilestone, "PATCH": h.patchMilestone, "DELETE": h.deleteMilestone})
}

// milestoneID reads the {id} path value. An id that is not a positive integer
// names no milestone.
func milestoneID(r *http.Request) (int64, *apiError) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, notFound("대상이 없습니다.")
	}
	return id, nil
}

func (h *handler) listMilestones(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	param := q.one("status")
	if param == "" {
		param = string(model.MilestoneOpen)
	}
	statuses, err := model.ExpandMilestoneStatuses(param)
	if err != nil {
		q.add("status", "open, closed, deleted, all 중 하나이거나 쉼표로 이은 목록이어야 합니다.")
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, err := h.store.ListMilestones(r.Context(), r.PathValue("key"), statuses)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) createMilestone(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{"name": true, "description": true, "target_date": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	// Nulls on name and description are left at their zero value so that
	// validation reports an empty name.
	var in store.MilestoneInput
	if _, _, aerr := field(b, "name", &in.Name); aerr != nil {
		writeError(w, aerr)
		return
	}
	if _, _, aerr := field(b, "description", &in.Description); aerr != nil {
		writeError(w, aerr)
		return
	}
	var date string
	present, null, aerr := field(b, "target_date", &date)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if present && !null {
		in.TargetDate = &date
	}
	m, err := h.store.CreateMilestone(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), in)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/v1/milestones/%d", m.ID))
	writeJSON(w, http.StatusCreated, m)
}

func (h *handler) getMilestone(w http.ResponseWriter, r *http.Request) {
	id, aerr := milestoneID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	m, err := h.store.GetMilestone(r.Context(), id)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *handler) patchMilestone(w http.ResponseWriter, r *http.Request) {
	id, aerr := milestoneID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	b, aerr := readBody(w, r, map[string]bool{"name": true, "description": true, "target_date": true, "status": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var patch store.MilestonePatch
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
	var name, description, date string
	var status model.MilestoneStatus
	steps := []func() *apiError{
		func() *apiError {
			p, n, e := field(b, "name", &name)
			nonNull("name", p, n, func() { patch.Name = &name })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "description", &description)
			nonNull("description", p, n, func() { patch.Description = &description })
			return e
		},
		func() *apiError {
			p, n, e := field(b, "target_date", &date)
			if p {
				patch.TargetDate = store.Optional[string]{Set: true}
				if !n {
					patch.TargetDate.Value = &date
				}
			}
			return e
		},
		func() *apiError {
			p, n, e := field(b, "status", &status)
			nonNull("status", p, n, func() { patch.Status = &status })
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
	h.applyMilestonePatch(w, r, id, patch)
}

func (h *handler) deleteMilestone(w http.ResponseWriter, r *http.Request) {
	id, aerr := milestoneID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	deleted := model.MilestoneDeleted
	h.applyMilestonePatch(w, r, id, store.MilestonePatch{Status: &deleted})
}

func (h *handler) applyMilestonePatch(w http.ResponseWriter, r *http.Request, id int64, patch store.MilestonePatch) {
	m, err := h.store.UpdateMilestone(r.Context(), TraceFrom(r.Context()), id, patch)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, m)
}
