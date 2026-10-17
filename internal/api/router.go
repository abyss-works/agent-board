// Package api is the HTTP layer: routing, header checks, request logging,
// JSON decoding and validation, and mapping store results to responses.
package api

import (
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

// APIVersion is the number in the /v1 path prefix.
const APIVersion = 1

// Options configures the handler.
type Options struct {
	Token   string
	Actors  []model.Actor
	Version string
	Logger  *slog.Logger
	Store   *store.Store
}

type handler struct {
	opts   Options
	actors map[string]model.Actor
	store  *store.Store
}

// New builds the complete HTTP handler.
func New(o Options) http.Handler {
	h := &handler{opts: o, actors: map[string]model.Actor{}, store: o.Store}
	for _, a := range o.Actors {
		h.actors[a.ID] = a
	}
	if h.opts.Logger == nil {
		h.opts.Logger = slog.Default()
	}

	// Routes behind authentication and trace headers.
	protected := http.NewServeMux()
	route(protected, "/v1/actors", methods{"GET": h.listActors})
	route(protected, "/v1/actors/me", methods{"GET": h.me})
	h.registerProjects(protected)
	h.registerIssues(protected)
	h.registerViews(protected)
	protected.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, notFound("경로가 없습니다."))
	})

	root := http.NewServeMux()
	route(root, "/v1/health", methods{"GET": h.health})
	root.Handle("/", auth(o.Token, trace(h.actors, protected)))
	return logging(h.opts.Logger, root)
}

type methods map[string]http.HandlerFunc

// route registers one path with a method table so unknown methods get a
// JSON 405 with an Allow header. Path parameters stay available through
// r.PathValue.
func route(mux *http.ServeMux, pattern string, m methods) {
	allowed := make([]string, 0, len(m))
	for k := range m {
		allowed = append(allowed, k)
	}
	sort.Strings(allowed)
	allow := strings.Join(allowed, ", ")
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if fn, ok := m[r.Method]; ok {
			fn(w, r)
			return
		}
		w.Header().Set("Allow", allow)
		writeError(w, &apiError{Status: 405, Code: "method_not_allowed", Message: "이 경로는 " + allow + " 메서드만 지원합니다."})
	})
}

func (h *handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "version": h.opts.Version, "api_version": APIVersion})
}

func (h *handler) listActors(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"items": h.opts.Actors})
}

func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, h.actors[TraceFrom(r.Context()).Actor])
}
