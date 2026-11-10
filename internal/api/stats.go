package api

import (
	"net/http"
	"sort"
	"sync"
	"time"
)

// unroutedPattern stands for the requests that no route handled: rejected
// before routing, unknown paths, and the web client.
const unroutedPattern = "(unrouted)"

// latencyBoundsMS are the inclusive upper bounds of the latency buckets in
// milliseconds. A request slower than the last bound falls into one more
// bucket that has no upper bound.
var latencyBoundsMS = [...]int{1, 5, 10, 25, 50, 100, 250, 500, 1000, 5000}

// Bucket counts the requests whose duration is at most LeMS milliseconds and
// above the bound before it. LeMS is nil for the bucket past the last bound.
type Bucket struct {
	LeMS  *int  `json:"le_ms"`
	Count int64 `json:"count"`
}

// RouteStats is the raw tally of one method on one route pattern. An error is
// a response with a 5xx status.
type RouteStats struct {
	Method   string   `json:"method"`
	Route    string   `json:"route"`
	Requests int64    `json:"requests"`
	Errors   int64    `json:"errors"`
	TotalMS  float64  `json:"total_ms"`
	MaxMS    float64  `json:"max_ms"`
	Buckets  []Bucket `json:"buckets"`
}

// otherMethod stands for every method outside the standard set, so a client
// cannot grow the tallies by sending made-up methods.
const otherMethod = "OTHER"

func normalizeMethod(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return m
	}
	return otherMethod
}

type routeKey struct{ method, route string }

type routeTally struct {
	requests, errors int64
	total, max       time.Duration
	buckets          [len(latencyBoundsMS) + 1]int64
}

// statsCollector keeps the tallies in memory. Nothing is persisted, so a
// restart starts from zero.
type statsCollector struct {
	mu     sync.Mutex
	routes map[routeKey]*routeTally
}

func newStatsCollector() *statsCollector {
	return &statsCollector{routes: map[routeKey]*routeTally{}}
}

func (c *statsCollector) record(method, route string, status int, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := routeKey{normalizeMethod(method), route}
	t := c.routes[k]
	if t == nil {
		t = &routeTally{}
		c.routes[k] = t
	}
	t.requests++
	if status >= 500 {
		t.errors++
	}
	t.total += d
	t.max = max(t.max, d)
	i := 0
	for i < len(latencyBoundsMS) && d > time.Duration(latencyBoundsMS[i])*time.Millisecond {
		i++
	}
	t.buckets[i]++
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// snapshot returns the tallies ordered by route and then method.
func (c *statsCollector) snapshot() []RouteStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]RouteStats, 0, len(c.routes))
	for k, t := range c.routes {
		rs := RouteStats{Method: k.method, Route: k.route, Requests: t.requests, Errors: t.errors, TotalMS: ms(t.total), MaxMS: ms(t.max)}
		for i, n := range t.buckets {
			b := Bucket{Count: n}
			if i < len(latencyBoundsMS) {
				le := latencyBoundsMS[i]
				b.LeMS = &le
			}
			rs.Buckets = append(rs.Buckets, b)
		}
		out = append(out, rs)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Route != out[j].Route {
			return out[i].Route < out[j].Route
		}
		return out[i].Method < out[j].Method
	})
	return out
}
