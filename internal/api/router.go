// Package api is the HTTP layer: routing, header checks, request logging,
// JSON decoding and validation, and mapping store results to responses.
package api

import (
	crand "crypto/rand"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/abyss-works/agent-board/internal/model"
	"github.com/abyss-works/agent-board/internal/store"
)

// APIVersion is the number in the /v1 path prefix.
const APIVersion = 1

// Options configures the handler.
type Options struct {
	Token   string
	Actors  []model.Actor
	Version string
	// Release is the release the binary was built for, such as v0.8.1. Empty
	// when unknown; health then omits it.
	Release string
	Logger  *slog.Logger
	Store   *store.Store
	// Web is the built web client. When set, every path outside /v1 is served
	// from it without authentication. When nil those paths belong to the API.
	Web fs.FS
	// Now and Rand are the clock and the source of randomness behind device
	// authorizations and token secrets. Nil means time.Now and crypto/rand;
	// tests replace them to fix times and codes.
	Now  func() time.Time
	Rand io.Reader
	// Semantic finds sources by meaning for the unified search. When nil the
	// search matches keywords only.
	Semantic Semantic
	// SimilarModel is the embedding model whose similar-document pairs the
	// graph shows. Empty when search by meaning is off: no similar edges.
	SimilarModel string
}

type handler struct {
	opts    Options
	actors  map[string]model.Actor
	store   *store.Store
	devices *deviceStore
	stats   *statsCollector
	started time.Time
}

// New builds the complete HTTP handler.
func New(o Options) http.Handler {
	h := &handler{opts: o, actors: map[string]model.Actor{}, store: o.Store, stats: newStatsCollector()}
	for _, a := range o.Actors {
		h.actors[a.ID] = a
	}
	if h.opts.Logger == nil {
		h.opts.Logger = slog.Default()
	}
	if h.opts.Now == nil {
		h.opts.Now = time.Now
	}
	if h.opts.Rand == nil {
		h.opts.Rand = crand.Reader
	}
	h.devices = newDeviceStore(h.opts.Now, h.opts.Rand)
	h.started = h.opts.Now()

	// Routes behind authentication and trace headers.
	protected := http.NewServeMux()
	route(protected, "/v1/actors", methods{"GET": h.listActors})
	route(protected, "/v1/actors/me", methods{"GET": h.me})
	route(protected, "/v1/stats", methods{"GET": h.serverStats})
	h.registerProjects(protected)
	h.registerIssues(protected)
	h.registerTopics(protected)
	h.registerMilestones(protected)
	h.registerMessages(protected)
	h.registerDocuments(protected)
	h.registerSearch(protected)
	h.registerGraph(protected)
	h.registerTemplates(protected)
	h.registerViews(protected)
	h.registerDevices(protected)
	h.registerTokens(protected)
	protected.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, notFound("경로가 없습니다."))
	})

	root := http.NewServeMux()
	route(root, "/v1/health", methods{"GET": h.health})
	authenticated := h.auth(h.listActorsUnnamed(trace(h.actors, h.bind(protected))))
	guarded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if startsLogin(r) {
			protected.ServeHTTP(w, r)
			return
		}
		authenticated.ServeHTTP(w, r)
	})
	if o.Web == nil {
		root.Handle("/", guarded)
	} else {
		// Only /v1 stays behind authentication; a browser asking for a page
		// sends neither the token nor the trace headers.
		root.Handle("/v1", guarded)
		root.Handle("/v1/", guarded)
		root.Handle("/", webHandler(o.Web))
	}
	return logging(h.opts.Logger, h.stats, root)
}

// startsLogin reports whether the request is one of the two that start a
// login: asking for a device authorization and exchanging its device code.
// They come from a client that has no token yet, so they carry neither a token
// nor the trace headers. The other methods and paths next to them are
// authenticated like the rest.
func startsLogin(r *http.Request) bool {
	return r.Method == http.MethodPost && (r.URL.Path == "/v1/device-authorizations" || r.URL.Path == "/v1/tokens")
}

type methods map[string]http.HandlerFunc

// route registers one path with a method table so unknown methods get a
// JSON 405 with an Allow header. Path parameters stay available through
// r.PathValue.
func route(mux *http.ServeMux, pattern string, m methods) {
	mux.HandleFunc(pattern, m.serve())
}

// serve is the handler of a method table.
func (m methods) serve() http.HandlerFunc {
	allowed := make([]string, 0, len(m))
	for k := range m {
		allowed = append(allowed, k)
	}
	sort.Strings(allowed)
	allow := strings.Join(allowed, ", ")
	return func(w http.ResponseWriter, r *http.Request) {
		setPattern(r, r.Pattern)
		if fn, ok := m[r.Method]; ok {
			fn(w, r)
			return
		}
		w.Header().Set("Allow", allow)
		writeError(w, &apiError{Status: 405, Code: "method_not_allowed", Message: "이 경로는 " + allow + " 메서드만 지원합니다."})
	}
}

func (h *handler) health(w http.ResponseWriter, _ *http.Request) {
	body := map[string]any{"status": "ok", "version": h.opts.Version, "api_version": APIVersion}
	if h.opts.Release != "" {
		body["release"] = h.opts.Release
	}
	writeJSON(w, 200, body)
}

func (h *handler) serverStats(w http.ResponseWriter, r *http.Request) {
	size, err := h.store.FileSize()
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, 200, map[string]any{
		"started_at": model.NewTime(h.started),
		"db_bytes":   size,
		"routes":     h.stats.snapshot(),
	})
}

func (h *handler) listActors(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"items": h.opts.Actors})
}

func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, h.actors[TraceFrom(r.Context()).Actor])
}
