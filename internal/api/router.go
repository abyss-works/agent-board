// Package api is the HTTP layer: routing, header checks, request logging,
// JSON decoding and validation, and mapping store results to responses.
package api

import (
	"context"
	crand "crypto/rand"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wsk0715/agent-board/internal/graph"
	"github.com/wsk0715/agent-board/internal/ingest"
	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/search"
	"github.com/wsk0715/agent-board/internal/store"
	pgstore "github.com/wsk0715/agent-board/internal/store/pg"
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
	Release       string
	Logger        *slog.Logger
	Store         *store.Store
	PGStore       *pgstore.Store
	GraphStore    *graph.Store
	IngestQueue   *ingest.Queue
	ContextEngine pgstore.ContextEngine
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
	// SSEPingInterval is how often an open event stream sends a keepalive
	// comment. Zero uses the default (20 seconds).
	SSEPingInterval time.Duration
	// MaxSSEConnections caps concurrent SSE event streams across the server.
	// Zero uses the default (64).
	MaxSSEConnections int
	// MaxSSEConnectionsPerActor caps concurrent SSE event streams per actor.
	// Zero uses the default (16).
	MaxSSEConnectionsPerActor int
}

type handler struct {
	opts          Options
	actorsMu      sync.RWMutex
	actors        map[string]model.Actor
	actorOrder    []model.Actor
	store         *store.Store
	pgStore       *pgstore.Store
	graphStore    *graph.Store
	ingestQueue   *ingest.Queue
	contextEngine pgstore.ContextEngine
	devices       *deviceStore
	stats         *statsCollector
	broker        *eventBroker
	started       time.Time

	// seen holds, per actor and session ID, when this process last wrote the
	// call of that session.
	seenMu sync.Mutex
	seen   map[string]time.Time
}

type closeableHandler struct {
	http.Handler
	closeFn func()
}

func (c *closeableHandler) Close() error {
	c.closeFn()
	return nil
}

// New builds the complete HTTP handler. The returned handler also implements
// io.Closer to close open SSE streams before server shutdown.
func New(o Options) http.Handler {
	h := &handler{
		opts:          o,
		actors:        map[string]model.Actor{},
		store:         o.Store,
		pgStore:       o.PGStore,
		graphStore:    o.GraphStore,
		ingestQueue:   o.IngestQueue,
		contextEngine: o.ContextEngine,
		stats:         newStatsCollector(),
		broker:        newEventBroker(o.MaxSSEConnections, o.MaxSSEConnectionsPerActor),
		seen:          map[string]time.Time{},
	}
	if h.opts.Logger == nil {
		h.opts.Logger = slog.Default()
	}
	if h.pgStore != nil {
		h.pgStore.SetEventHook(h.broker.Notify)
		if h.graphStore == nil {
			h.graphStore = graph.NewStore(h.pgStore.Pool())
		}
		if h.contextEngine == nil {
			h.contextEngine = search.NewEngine(search.EngineConfig{
				Pool:       h.pgStore.Pool(),
				GraphStore: h.graphStore,
				Logger:     h.opts.Logger,
			})
		}
	} else if h.store != nil {
		h.store.SetEventHook(h.broker.Notify)
	}
	for _, a := range o.Actors {
		if _, exists := h.actors[a.ID]; !exists {
			h.actorOrder = append(h.actorOrder, a)
		}
		h.actors[a.ID] = a
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
	if h.pgStore != nil {
		h.registerPGRoutes(protected)
	} else {
		h.registerProjects(protected)
		h.registerIssues(protected)
		h.registerTopics(protected)
		h.registerMilestones(protected)
		h.registerMessages(protected)
		h.registerSessions(protected)
		h.registerEvents(protected)
		h.registerDocuments(protected)
		h.registerSearch(protected)
		h.registerGraph(protected)
		h.registerTemplates(protected)
		h.registerViews(protected)
		h.registerDevices(protected)
		h.registerTokens(protected)
	}
	protected.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, notFound("경로가 없습니다."))
	})

	root := http.NewServeMux()
	route(root, "/v1/health", methods{"GET": h.health})
	// A request that passed authentication and the trace headers records the
	// call of its session before the route handles it.
	authenticated := h.auth(h.listActorsUnnamed(h.trace(h.bind(h.touchSession(protected)))))
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
	return &closeableHandler{
		Handler: logging(h.opts.Logger, h.stats, root),
		closeFn: h.broker.Close,
	}
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
	var (
		size int64
		err  error
	)
	if h.pgStore != nil {
		size, err = h.pgStore.DBSize(r.Context())
		if err != nil {
			writeError(w, h.pgStoreError(r, err))
			return
		}
	} else {
		size, err = h.store.FileSize()
		if err != nil {
			writeError(w, h.storeError(r, err))
			return
		}
	}
	writeJSON(w, 200, map[string]any{
		"started_at": model.NewTime(h.started),
		"db_bytes":   size,
		"routes":     h.stats.snapshot(),
		"streams":    h.stats.streamSnapshot(),
	})
}

func (h *handler) actor(id string) (model.Actor, bool) {
	h.actorsMu.RLock()
	defer h.actorsMu.RUnlock()
	a, ok := h.actors[id]
	return a, ok
}

func (h *handler) actorList() []model.Actor {
	h.actorsMu.RLock()
	defer h.actorsMu.RUnlock()
	out := make([]model.Actor, len(h.actorOrder))
	copy(out, h.actorOrder)
	return out
}

func (h *handler) actorMap() map[string]model.Actor {
	h.actorsMu.RLock()
	defer h.actorsMu.RUnlock()
	out := make(map[string]model.Actor, len(h.actors))
	for k, v := range h.actors {
		out[k] = v
	}
	return out
}

func (h *handler) registerAgentActor(ctx context.Context, id string) (bool, error) {
	if h.pgStore != nil {
		created, err := h.pgStore.EnsureAgentActor(ctx, id)
		if err != nil {
			return false, err
		}
		h.actorsMu.Lock()
		if _, exists := h.actors[id]; !exists {
			a := model.Actor{ID: id, Kind: model.ActorAgent}
			h.actors[id] = a
			h.actorOrder = append(h.actorOrder, a)
		}
		h.actorsMu.Unlock()
		return created, nil
	}
	if h.store != nil {
		created, err := h.store.EnsureAgentActor(ctx, id)
		if err != nil {
			return false, err
		}
		h.actorsMu.Lock()
		if _, exists := h.actors[id]; !exists {
			a := model.Actor{ID: id, Kind: model.ActorAgent}
			h.actors[id] = a
			h.actorOrder = append(h.actorOrder, a)
		}
		h.actorsMu.Unlock()
		return created, nil
	}
	h.actorsMu.Lock()
	defer h.actorsMu.Unlock()
	if a, exists := h.actors[id]; exists {
		if a.Kind == model.ActorUser {
			return false, &model.ValidationError{Fields: []model.FieldError{
				{Field: "actor", Message: "user 종류 행위자는 기기 승인 로그인을 쓸 수 없습니다."},
			}}
		}
		return false, nil
	}
	a := model.Actor{ID: id, Kind: model.ActorAgent}
	h.actors[id] = a
	h.actorOrder = append(h.actorOrder, a)
	return true, nil
}

func (h *handler) listActors(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"items": h.actorList()})
}

func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	a, _ := h.actor(TraceFrom(r.Context()).Actor)
	writeJSON(w, 200, a)
}
