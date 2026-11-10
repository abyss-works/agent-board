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
