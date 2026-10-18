package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func (h *handler) registerTopics(mux *http.ServeMux) {
	route(mux, "/v1/projects/{key}/topics", methods{"GET": h.listTopics, "POST": h.createTopic})
	route(mux, "/v1/topics/{id}", methods{"PATCH": h.patchTopic, "DELETE": h.deleteTopic})
}

// topicID reads the {id} path value. An id that is not a positive integer
// names no topic.
func topicID(r *http.Request) (int64, *apiError) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, notFound("대상이 없습니다.")
	}
	return id, nil
}

func (h *handler) listTopics(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	var statuses []model.TopicStatus
	switch q.one("status") {
	case "":
		statuses = []model.TopicStatus{model.TopicActive}
	case "all":
		statuses = model.TopicStatuses
	default:
		statuses = parseEnumList(q, "status", model.ParseTopicStatus)
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, err := h.store.ListTopics(r.Context(), r.PathValue("key"), statuses)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) createTopic(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{"name": true, "parent": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var in store.TopicInput
	if _, _, aerr := field(b, "name", &in.Name); aerr != nil {
		writeError(w, aerr)
		return
	}
	var parent int64
	present, null, aerr := field(b, "parent", &parent)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if present && !null {
		in.Parent = &parent
	}
	tp, err := h.store.CreateTopic(r.Context(), TraceFrom(r.Context()), r.PathValue("key"), in)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/v1/topics/%d", tp.ID))
	writeJSON(w, http.StatusCreated, tp)
}

func (h *handler) patchTopic(w http.ResponseWriter, r *http.Request) {
	id, aerr := topicID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	b, aerr := readBody(w, r, map[string]bool{"name": true, "parent": true, "status": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	var patch store.TopicPatch
	var nulls []Detail
	var name string
	present, null, aerr := field(b, "name", &name)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if present {
		if null {
			nulls = append(nulls, Detail{Field: "name", Message: "null일 수 없습니다."})
		} else {
			patch.Name = &name
		}
	}
	var parent int64
	present, null, aerr = field(b, "parent", &parent)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if present {
		patch.Parent = store.Optional[int64]{Set: true}
		if !null {
			patch.Parent.Value = &parent
		}
	}
	var status model.TopicStatus
	present, null, aerr = field(b, "status", &status)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if present {
		if null {
			nulls = append(nulls, Detail{Field: "status", Message: "null일 수 없습니다."})
		} else {
			patch.Status = &status
		}
	}
	if len(nulls) > 0 {
		writeError(w, validationFailed(nulls))
		return
	}
	h.applyTopicPatch(w, r, id, patch)
}

func (h *handler) deleteTopic(w http.ResponseWriter, r *http.Request) {
	id, aerr := topicID(r)
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	deleted := model.TopicDeleted
	h.applyTopicPatch(w, r, id, store.TopicPatch{Status: &deleted})
}

func (h *handler) applyTopicPatch(w http.ResponseWriter, r *http.Request, id int64, patch store.TopicPatch) {
	tp, err := h.store.UpdateTopic(r.Context(), TraceFrom(r.Context()), id, patch)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, tp)
}
