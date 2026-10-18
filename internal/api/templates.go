package api

import (
	"net/http"

	"github.com/wsk0715/agent-board/internal/model"
)

func (h *handler) registerTemplates(mux *http.ServeMux) {
	route(mux, "/v1/templates", methods{"GET": h.listTemplates})
}

// listTemplates returns the body template of every issue type, in the order
// of the types. They are the templates an agent's issue bodies are checked
// against.
func (h *handler) listTemplates(w http.ResponseWriter, r *http.Request) {
	if aerr := newQuery(r).finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": model.Templates})
}
