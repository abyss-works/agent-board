package api

import (
	"sync"
	"testing"
	"time"
)

func findRoute(t *testing.T, routes []RouteStats, method, route string) RouteStats {
	t.Helper()
	for _, r := range routes {
		if r.Method == method && r.Route == route {
			return r
		}
	}
	t.Fatalf("route %s %s not found in %+v", method, route, routes)
	return RouteStats{}
}

func bucketCounts(r RouteStats) []int64 {
	out := make([]int64, len(r.Buckets))
	for i, b := range r.Buckets {
		out[i] = b.Count
	}
	return out
}

func TestStatsCollectorAggregates(t *testing.T) {
	c := newStatsCollector()
	c.record("GET", "/v1/issues/{key}", 200, 3*time.Millisecond)
	c.record("GET", "/v1/issues/{key}", 404, 20*time.Millisecond)
	c.record("GET", "/v1/issues/{key}", 500, 1500*time.Millisecond)
	c.record("POST", "/v1/issues/{key}", 201, time.Millisecond)

	got := findRoute(t, c.snapshot(), "GET", "/v1/issues/{key}")
	if got.Requests != 3 || got.Errors != 1 {
		t.Errorf("requests=%d errors=%d, want 3 and 1 (only 5xx is an error)", got.Requests, got.Errors)
	}
	if got.TotalMS != 1523 || got.MaxMS != 1500 {
		t.Errorf("total=%v max=%v, want 1523 and 1500", got.TotalMS, got.MaxMS)
	}
	if post := findRoute(t, c.snapshot(), "POST", "/v1/issues/{key}"); post.Requests != 1 {
		t.Errorf("methods must be kept apart: %+v", post)
	}
}

func TestStatsBucketBounds(t *testing.T) {
	c := newStatsCollector()
	// A duration equal to a bound belongs to that bound's bucket.
	for _, d := range []time.Duration{0, time.Millisecond, time.Millisecond + 1, 5 * time.Second, time.Hour} {
		c.record("GET", "/x", 200, d)
	}
	r := findRoute(t, c.snapshot(), "GET", "/x")
	if len(r.Buckets) != len(latencyBoundsMS)+1 {
		t.Fatalf("buckets = %d, want %d", len(r.Buckets), len(latencyBoundsMS)+1)
	}
	if r.Buckets[len(r.Buckets)-1].LeMS != nil {
		t.Error("last bucket must have no upper bound")
	}
	counts := bucketCounts(r)
	if counts[0] != 2 || counts[1] != 1 || counts[len(counts)-2] != 1 || counts[len(counts)-1] != 1 {
		t.Errorf("counts = %v", counts)
	}
}

func TestStatsSnapshotIsSortedAndEmptyAtStart(t *testing.T) {
	c := newStatsCollector()
	if got := c.snapshot(); got == nil || len(got) != 0 {
		t.Errorf("empty snapshot = %#v, want empty non-nil slice", got)
	}
	c.record("POST", "/b", 200, 0)
	c.record("GET", "/b", 200, 0)
	c.record("GET", "/a", 200, 0)
	got := c.snapshot()
	order := []string{got[0].Route + got[0].Method, got[1].Route + got[1].Method, got[2].Route + got[2].Method}
	if order[0] != "/aGET" || order[1] != "/bGET" || order[2] != "/bPOST" {
		t.Errorf("order = %v", order)
	}
}

func TestStatsCollectorConcurrent(t *testing.T) {
	c := newStatsCollector()
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			for range 100 {
				c.record("GET", "/x", 200, time.Millisecond)
				c.snapshot()
			}
		})
	}
	wg.Wait()
	if n := findRoute(t, c.snapshot(), "GET", "/x").Requests; n != 5000 {
		t.Errorf("requests = %d, want 5000", n)
	}
}

type statsResponse struct {
	StartedAt string       `json:"started_at"`
	DBBytes   int64        `json:"db_bytes"`
	Routes    []RouteStats `json:"routes"`
}

func TestStatsEndpoint(t *testing.T) {
	e := newEnvWith(t, testActors, func(o *Options) {
		o.Now = func() time.Time { return time.Date(2026, 11, 9, 12, 30, 0, 0, time.FixedZone("KST", 9*3600)) }
	})
	e.do(t, "GET", "/v1/actors", "", nil)
	e.do(t, "GET", "/v1/issues/AGB-1", "", nil)
	e.do(t, "GET", "/v1/issues/AGB-2", "", nil)
	e.do(t, "GET", "/v1/no-such-path", "", nil)
	e.do(t, "GET", "/v1/actors", "", map[string]string{"Authorization": "Bearer wrong"})

	rec := e.do(t, "GET", "/v1/stats", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got statsResponse
	decodeJSON(t, rec, &got)
	if got.StartedAt != "2026-11-09T03:30:00.000Z" {
		t.Errorf("started_at = %q, want UTC", got.StartedAt)
	}
	if got.DBBytes <= 0 {
		t.Errorf("db_bytes = %d", got.DBBytes)
	}
	if n := findRoute(t, got.Routes, "GET", "/v1/actors").Requests; n != 1 {
		t.Errorf("/v1/actors requests = %d, want 1 (the rejected one is not routed)", n)
	}
	// Two different keys are one route pattern.
	if n := findRoute(t, got.Routes, "GET", "/v1/issues/{key}").Requests; n != 2 {
		t.Errorf("/v1/issues/{key} requests = %d, want 2", n)
	}
	// Requests no route handled are counted together, not by their path.
	if n := findRoute(t, got.Routes, "GET", unroutedPattern).Requests; n < 2 {
		t.Errorf("unrouted requests = %d, want at least 2", n)
	}
	for _, r := range got.Routes {
		if r.Route == "/v1/no-such-path" || r.Route == "/v1/issues/AGB-1" {
			t.Errorf("raw path used as route: %q", r.Route)
		}
	}
}

func TestStatsCountsActorListWithoutActor(t *testing.T) {
	e := newEnv(t)
	e.do(t, "GET", "/v1/actors", "", map[string]string{"Authorization": "Bearer " + testToken, "X-Actor": "-"})
	var got statsResponse
	decodeJSON(t, e.do(t, "GET", "/v1/stats", "", nil), &got)
	if n := findRoute(t, got.Routes, "GET", "/v1/actors").Requests; n != 1 {
		t.Errorf("/v1/actors requests = %d, want 1", n)
	}
	for _, r := range got.Routes {
		if r.Route == "/" {
			t.Errorf("request counted under %q", r.Route)
		}
	}
}

func TestStatsEndpointAccess(t *testing.T) {
	e := newEnv(t)
	if rec := e.do(t, "GET", "/v1/stats", "", map[string]string{"Authorization": "Bearer wrong"}); rec.Code != 401 {
		t.Errorf("wrong token status = %d, want 401", rec.Code)
	}
	if rec := e.do(t, "GET", "/v1/stats", "", map[string]string{"X-Actor": "-"}); rec.Code != 400 {
		t.Errorf("missing actor status = %d, want 400", rec.Code)
	}
	if rec := e.do(t, "POST", "/v1/stats", "", nil); rec.Code != 405 {
		t.Errorf("POST status = %d, want 405", rec.Code)
	}
}

func TestStatsGroupsUnknownMethods(t *testing.T) {
	c := newStatsCollector()
	for _, m := range []string{"X0001", "X0002", "BREW", "get"} {
		c.record(m, "/v1/health", 200, 0)
	}
	c.record("GET", "/v1/health", 200, 0)
	c.record("TRACE", "/v1/health", 200, 0)
	got := c.snapshot()
	if len(got) != 3 {
		t.Fatalf("entries = %+v, want GET, TRACE and OTHER only", got)
	}
	if n := findRoute(t, got, otherMethod, "/v1/health").Requests; n != 4 {
		t.Errorf("OTHER requests = %d, want 4", n)
	}
	if n := findRoute(t, got, "GET", "/v1/health").Requests; n != 1 {
		t.Errorf("GET requests = %d, want 1", n)
	}
}
