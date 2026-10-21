package api

import (
	"net/http"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func (h *handler) registerDocuments(mux *http.ServeMux) {
	route(mux, "/v1/projects/{key}/documents", methods{"GET": h.listDocuments})
	route(mux, "/v1/projects/{key}/documents/{name}", methods{"GET": h.getDocument, "PUT": h.putDocument})
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
	// Nulls are left at their zero value so that validation reports them.
	var in store.DocumentInput
	if _, _, aerr := field(b, "location", &in.Location); aerr != nil {
		writeError(w, aerr)
		return
	}
	if _, _, aerr := field(b, "content", &in.Content); aerr != nil {
		writeError(w, aerr)
		return
	}
	project, name := r.PathValue("key"), r.PathValue("name")
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
		Query:     q.one("q"),
		Projects:  q.csv("project"),
		Kinds:     parseEnumList(q, "kind", model.ParseDocumentKind),
		Locations: parseEnumList(q, "location", model.ParseDocumentLocation),
	}
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
