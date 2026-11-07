package api

import (
	"fmt"
	"net/http"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func (h *handler) registerDocuments(mux *http.ServeMux) {
	route(mux, "/v1/projects/{key}/documents", methods{"GET": h.listDocuments})
	route(mux, "/v1/projects/{key}/documents/{name...}", methods{"GET": h.getDocument, "PUT": h.putDocument})
	route(mux, "/v1/documents", methods{"GET": h.searchDocuments})
}

// putDocument stores a record document: 201 for a new one, 200 for the same
// content again, which also moves the document when the location differs.
// Other content under the same name is a 409.
func (h *handler) putDocument(w http.ResponseWriter, r *http.Request) {
	b, aerr := readBody(w, r, map[string]bool{"location": true, "content": true})
	if aerr != nil {
		writeError(w, aerr)
		return
	}
	in := store.DocumentInput{LocationOnly: true}
	var location string
	if _, _, aerr := field(b, "location", &location); aerr != nil {
		writeError(w, aerr)
		return
	}
	if _, _, aerr := field(b, "content", &in.Content); aerr != nil {
		writeError(w, aerr)
		return
	}
	project, name := r.PathValue("key"), r.PathValue("name")
	if location != "" {
		loc, ok := model.ParseDocumentLocation(location)
		if !ok {
			writeError(w, validationFailed([]Detail{{Field: "location", Message: "history, archive, legacy 중 하나여야 합니다."}}))
			return
		}
		in.Args.ApplyLocation(name, loc)
	}
	d, created, err := h.store.PutDocument(r.Context(), TraceFrom(r.Context()), project, name, in)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	status := http.StatusOK
	if created {
		w.Header().Set("Location", "/v1/projects/"+project+"/documents/"+name)
		status = http.StatusCreated
	}
	writeJSON(w, status, d)
}

func (h *handler) getDocument(w http.ResponseWriter, r *http.Request) {
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	d, err := h.store.GetDocument(r.Context(), r.PathValue("key"), r.PathValue("name"))
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *handler) listDocuments(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	limit, offset := q.limit(50, 500), q.offset()
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.store.ListDocuments(r.Context(), r.PathValue("key"), limit, offset)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, limit, offset))
}

func (h *handler) searchDocuments(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := store.DocumentFilter{
		Query:    q.one("q"),
		Projects: q.csv("project"),
		Kinds:    parseEnumList(q, "kind", model.ParseDocumentKind),
		States:   parseStates(q),
		Imported: parseBool(q, "imported"),
	}
	rejectLocation(q)
	for _, p := range f.Projects {
		if !model.ValidProjectKey(p) {
			q.add("project", "프로젝트 키 형식이 아닙니다.")
		}
	}
	f.Limit, f.Offset = q.limit(50, 200), q.offset()
	if _, err := store.BuildMatch(f.Query); err != nil {
		q.add("q", "검색어는 최대 10항입니다.")
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.store.SearchDocuments(r.Context(), f)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}

// parseStates reads the state filter of a document search: current when none is
// given, and no filter, which is every state, for all.
func parseStates(q *query) []model.DocumentState {
	values := q.csv("state")
	if len(values) == 0 {
		return []model.DocumentState{model.StateCurrent}
	}
	var out []model.DocumentState
	for _, v := range values {
		if v == "all" {
			return nil
		}
		state, ok := model.ParseDocumentState(v)
		if !ok {
			q.add("state", fmt.Sprintf("%q는 허용되지 않는 값입니다. current, superseded, archived, all 중 하나여야 합니다.", v))
			continue
		}
		out = append(out, state)
	}
	return out
}

// parseBool reads an optional true or false parameter.
func parseBool(q *query, name string) *bool {
	v := q.one(name)
	switch v {
	case "":
		return nil
	case "true", "false":
		b := v == "true"
		return &b
	}
	q.add(name, "true 또는 false여야 합니다.")
	return nil
}

// rejectLocation refuses the location filter, which the state and imported
// filters replaced.
func rejectLocation(q *query) {
	if _, ok := q.values["location"]; ok {
		q.seen["location"] = true
		q.add("location", "location은 없어졌습니다. 상태는 state, 가져온 문서는 imported를 쓰세요.")
	}
}
