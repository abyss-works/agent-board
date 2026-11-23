package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

const (
	streamEventsPattern    = "/v1/events/stream"
	defaultSSEPingInterval = 20 * time.Second
)

// eventBroker fans out commit notifications from Store.SetEventHook to active
// SSE subscribers. Subscribers drain the database on each notification so no
// event is ever lost to a full buffer or race window.
type eventBroker struct {
	mu     sync.Mutex
	closed bool
	done   chan struct{}
	subs   map[chan struct{}]struct{}
}

func newEventBroker() *eventBroker {
	return &eventBroker{
		done: make(chan struct{}),
		subs: map[chan struct{}]struct{}{},
	}
}

func (b *eventBroker) Subscribe() (<-chan struct{}, func(), bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, func() {}, false
	}
	ch := make(chan struct{}, 1)
	b.subs[ch] = struct{}{}
	unsub := func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
	return ch, unsub, true
}

func (b *eventBroker) Notify(_ []model.Event) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	b.mu.Unlock()
}

func (b *eventBroker) Done() <-chan struct{} { return b.done }

func (b *eventBroker) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	close(b.done)
	for ch := range b.subs {
		delete(b.subs, ch)
		close(ch)
	}
	b.mu.Unlock()
}

func (h *handler) registerEvents(mux *http.ServeMux) {
	route(mux, "/v1/events", methods{"GET": h.listEvents})
	route(mux, streamEventsPattern, methods{"GET": h.streamEvents})
}

func parseNonNegativeInt64(raw string) (int64, bool) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func (h *handler) listEvents(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	var after int64
	if raw := q.one("after"); raw != "" {
		n, ok := parseNonNegativeInt64(raw)
		if !ok {
			q.add("after", "0 이상의 정수여야 합니다.")
		} else {
			after = n
		}
	}
	project := q.one("project")
	if project != "" && !model.ValidProjectKey(project) {
		q.add("project", "프로젝트 키 형식이 아닙니다.")
	}
	kinds := q.csv("kind")
	limit := q.limit(100, 500)
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	items, nextAfter, err := h.store.ListEvents(r.Context(), store.EventFilter{
		After:   after,
		Project: project,
		Kinds:   kinds,
		Limit:   limit,
	})
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":      items,
		"next_after": nextAfter,
	})
}

func (h *handler) streamEvents(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	afterRaw := q.one("after")
	var afterVal int64
	hasAfter := false
	if afterRaw != "" {
		n, ok := parseNonNegativeInt64(afterRaw)
		if !ok {
			q.add("after", "0 이상의 정수여야 합니다.")
		} else {
			afterVal = n
			hasAfter = true
		}
	}
	project := q.one("project")
	if project != "" && !model.ValidProjectKey(project) {
		q.add("project", "프로젝트 키 형식이 아닙니다.")
	}
	kinds := q.csv("kind")

	lastEventIDRaw := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	var cursor int64
	hasCursor := false
	if lastEventIDRaw != "" {
		n, ok := parseNonNegativeInt64(lastEventIDRaw)
		if !ok {
			q.details = append(q.details, Detail{Field: "Last-Event-ID", Reason: "invalid_header", Message: "0 이상의 정수여야 합니다."})
		} else {
			cursor = n
			hasCursor = true
		}
	} else if hasAfter {
		cursor = afterVal
		hasCursor = true
	}

	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}

	// Subscribe before reading LatestEventID or draining so no event committed
	// during setup can be missed.
	ch, unsub, ok := h.broker.Subscribe()
	if !ok {
		writeError(w, &apiError{Status: http.StatusServiceUnavailable, Code: "shutting_down", Message: "서버가 종료 중입니다."})
		return
	}
	defer unsub()

	if !hasCursor {
		latest, err := h.store.LatestEventID(r.Context())
		if err != nil {
			writeError(w, h.storeError(r, err))
			return
		}
		cursor = latest
	}

	// Fetch the first batch before writing 200 headers so an initial DB read
	// failure returns a JSON error instead of closing an empty stream.
	initialItems, initialNextAfter, err := h.store.ListEvents(r.Context(), store.EventFilter{
		After:   cursor,
		Project: project,
		Kinds:   kinds,
		Limit:   500,
	})
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	_ = rc.Flush()

	h.stats.streamOpened(r.Method, streamEventsPattern)
	defer h.stats.streamClosed(r.Method, streamEventsPattern)

	writeBatch := func(items []model.Event, nextAfter int64) error {
		if len(items) == 0 {
			return nil
		}
		for _, ev := range items {
			if err := writeSSEEvent(w, ev); err != nil {
				return err
			}
			cursor = ev.ID
		}
		if err := rc.Flush(); err != nil {
			return err
		}
		if nextAfter > cursor {
			cursor = nextAfter
		}
		return nil
	}

	drain := func() error {
		for {
			items, nextAfter, err := h.store.ListEvents(r.Context(), store.EventFilter{
				After:   cursor,
				Project: project,
				Kinds:   kinds,
				Limit:   500,
			})
			if err != nil {
				return err
			}
			if len(items) == 0 {
				return nil
			}
			if err := writeBatch(items, nextAfter); err != nil {
				return err
			}
			if len(items) < 500 {
				return nil
			}
		}
	}

	if err := writeBatch(initialItems, initialNextAfter); err != nil {
		return
	}
	if len(initialItems) == 500 {
		if err := drain(); err != nil {
			return
		}
	}

	pingEvery := h.opts.SSEPingInterval
	if pingEvery <= 0 {
		pingEvery = defaultSSEPingInterval
	}
	ticker := time.NewTicker(pingEvery)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.broker.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			if err := drain(); err != nil {
				return
			}
		case <-ticker.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}

func writeSSEEvent(w io.Writer, ev model.Event) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(ev); err != nil {
		return err
	}
	line := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	_, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Kind, line)
	return err
}
