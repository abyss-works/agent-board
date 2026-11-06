package api

import (
	"net/http"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func (h *handler) registerSearch(mux *http.ServeMux) {
	route(mux, "/v1/search", methods{"GET": h.search})
}

// search is the unified search over issues, comments and record documents.
func (h *handler) search(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := store.SearchFilter{
		Query:        q.one("q"),
		Projects:     q.csv("project"),
		Types:        parseEnumList(q, "type", model.ParseSearchType),
		CommentKinds: parseEnumList(q, "comment_kind", model.ParseCommentKind),
	}
	for _, p := range f.Projects {
		if !model.ValidProjectKey(p) {
			q.add("project", "프로젝트 키 형식이 아닙니다.")
		}
	}
	f.Limit, f.Offset = q.limit(20, 200), q.offset()
	if match, err := store.BuildMatch(f.Query); err != nil {
		q.add("q", "검색어는 최대 10항입니다.")
	} else if match == "" {
		q.add("q", "검색어가 필요합니다.")
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, total, err := h.store.Search(r.Context(), f)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, envelope(items, total, f.Limit, f.Offset))
}
