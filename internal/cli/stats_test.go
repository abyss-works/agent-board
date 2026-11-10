package cli

import (
	"strings"
	"testing"
)

const statsJSON = `{"started_at":"2026-11-09T03:30:00.000Z","db_bytes":2097152,"routes":[` +
	`{"method":"GET","route":"/v1/issues/{key}","requests":4,"errors":1,"total_ms":100.5,"max_ms":60,"buckets":[{"le_ms":1,"count":0},{"le_ms":5,"count":1},{"le_ms":null,"count":3}]},` +
	`{"method":"POST","route":"/v1/issues","requests":1,"errors":0,"total_ms":8,"max_ms":8,"buckets":[{"le_ms":1,"count":0},{"le_ms":5,"count":0},{"le_ms":null,"count":1}]}]}`

func statsServer(t *testing.T) *fakeServer {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/stats", canned{body: statsJSON})
	return srv
}

func TestStatsPipeIsJSON(t *testing.T) {
	srv := statsServer(t)
	h := newHarness(t, srv, false)
	got := textRun(t, h, 0, "stats")
	if !strings.Contains(got, `"db_bytes":2097152`) || strings.Contains(got, "REQUESTS") {
		t.Errorf("out=%q", got)
	}
	if req := srv.last(); req.Path != "/v1/stats" || req.Method != "GET" {
		t.Errorf("request = %s %s", req.Method, req.Path)
	}
}

func TestStatsTable(t *testing.T) {
	h := newHarness(t, statsServer(t), true)
	got := textRun(t, h, 0, "stats")
	for _, w := range []string{"2026-11-09T03:30:00.000Z", "2097152", "METHOD", "ROUTE", "REQUESTS", "ERRORS", "AVG_MS", "MAX_MS", "/v1/issues/{key}", "25.1", "60"} {
		if !strings.Contains(got, w) {
			t.Errorf("table lacks %q:\n%s", w, got)
		}
	}
}

func TestStatsFieldsText(t *testing.T) {
	h := newHarness(t, statsServer(t), false)
	got := textRun(t, h, 0, "stats", "--fields", "routes.method,routes.route,routes.requests", "--text")
	if got != "GET\t/v1/issues/{key}\t4\nPOST\t/v1/issues\t1\n" {
		t.Errorf("out=%q", got)
	}
}

func TestStatsRejectsArguments(t *testing.T) {
	h := newHarness(t, statsServer(t), false)
	if code := h.run("stats", "extra"); code != 2 {
		t.Errorf("code=%d, want 2", code)
	}
}
