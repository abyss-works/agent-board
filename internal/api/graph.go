package api

import (
	"net/http"

	"github.com/wsk0715/agent-board/internal/model"
)

func (h *handler) registerGraph(mux *http.ServeMux) {
	route(mux, "/v1/graph", methods{"GET": h.graph})
}

// graph returns the decision graph around one issue or record document.
func (h *handler) graph(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	node := q.one("node")
	depth := q.intRange("depth", 1, 1, 2)
	if _, _, _, ok := model.ParseNodeID(node); !ok {
		q.add("node", "issue:<KEY> 또는 doc:<PROJECT>/<문서 이름> 형식이어야 합니다.")
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	g, err := h.store.Graph(r.Context(), node, depth, h.opts.SimilarModel)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, g)
}
