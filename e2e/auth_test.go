//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

type health struct {
	Status     string `json:"status"`
	Version    string `json:"version"`
	APIVersion int    `json:"api_version"`
}

func TestHealth(t *testing.T) {
	t.Run("open without token and headers", func(t *testing.T) {
		h := decodeAs[health](t, send(t, "GET", "/v1/health", nil, nil), 200)
		if h.Status != "ok" || h.APIVersion != 1 || h.Version == "" {
			t.Errorf("health = %+v", h)
		}
	})
	t.Run("ignores a wrong token", func(t *testing.T) {
		hdr := http.Header{"Authorization": {"Bearer wrong"}}
		expect(t, send(t, "GET", "/v1/health", nil, hdr), 200)
	})
	t.Run("405 with Allow", func(t *testing.T) {
		r := send(t, "POST", "/v1/health", nil, nil)
		wantError(t, r, 405, "method_not_allowed")
		if !strings.Contains(r.header.Get("Allow"), "GET") {
			t.Errorf("Allow = %q", r.header.Get("Allow"))
		}
	})
}

func TestAuth(t *testing.T) {
	good := owner.headers(false)
	cases := []struct {
		name string
		mod  func(h http.Header)
	}{
		{"missing Authorization", func(h http.Header) { h.Del("Authorization") }},
		{"wrong token", func(h http.Header) { h.Set("Authorization", "Bearer "+strings.Repeat("x", 40)) }},
		{"not Bearer", func(h http.Header) { h.Set("Authorization", "Basic "+strings.Repeat("x", 40)) }},
		{"bare token", func(h http.Header) { h.Set("Authorization", token) }},
		{"token with prefix", func(h http.Header) { h.Set("Authorization", "Bearer "+token+"x") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := good.Clone()
			c.mod(h)
			wantError(t, send(t, "GET", "/v1/actors/me", nil, h), 401, "unauthorized")
		})
	}
	t.Run("auth is checked before trace headers", func(t *testing.T) {
		h := http.Header{"Authorization": {"Bearer wrong"}}
		wantError(t, send(t, "GET", "/v1/actors/me", nil, h), 401, "unauthorized")
	})
	t.Run("auth is checked before the path", func(t *testing.T) {
		h := http.Header{"Authorization": {"Bearer wrong"}}
		wantError(t, send(t, "GET", "/v1/no-such-path", nil, h), 401, "unauthorized")
	})
}

func TestTraceHeaders(t *testing.T) {
	type tc struct {
		name   string
		mod    func(h http.Header)
		field  string
		reason string
	}
	long := strings.Repeat("a", 129)
	cases := []tc{
		{"missing X-Actor", func(h http.Header) { h.Del("X-Actor") }, "X-Actor", ""},
		{"missing X-Session-Id", func(h http.Header) { h.Del("X-Session-Id") }, "X-Session-Id", ""},
		{"missing X-Client-Host", func(h http.Header) { h.Del("X-Client-Host") }, "X-Client-Host", ""},
		{"duplicate X-Actor", func(h http.Header) { h["X-Actor"] = []string{"owner", "owner"} }, "X-Actor", ""},
		{"duplicate X-Session-Id", func(h http.Header) { h["X-Session-Id"] = []string{"a", "b"} }, "X-Session-Id", ""},
		{"duplicate X-Client-Host", func(h http.Header) { h["X-Client-Host"] = []string{"a", "b"} }, "X-Client-Host", ""},
		{"actor uppercase", func(h http.Header) { h.Set("X-Actor", "Owner") }, "X-Actor", ""},
		{"actor with dash", func(h http.Header) { h.Set("X-Actor", "my-agent") }, "X-Actor", ""},
		{"actor too long", func(h http.Header) { h.Set("X-Actor", strings.Repeat("a", 33)) }, "X-Actor", ""},
		{"unknown actor", func(h http.Header) { h.Set("X-Actor", "nobody") }, "X-Actor", "unknown_actor"},
		{"session with space", func(h http.Header) { h.Set("X-Session-Id", "has space") }, "X-Session-Id", ""},
		{"session starts with dot", func(h http.Header) { h.Set("X-Session-Id", ".abc") }, "X-Session-Id", ""},
		{"session too long", func(h http.Header) { h.Set("X-Session-Id", long) }, "X-Session-Id", ""},
		{"host leading hyphen", func(h http.Header) { h.Set("X-Client-Host", "-bad") }, "X-Client-Host", ""},
		{"host trailing hyphen", func(h http.Header) { h.Set("X-Client-Host", "bad-") }, "X-Client-Host", ""},
		{"host empty label", func(h http.Header) { h.Set("X-Client-Host", "a..b") }, "X-Client-Host", ""},
		{"host underscore", func(h http.Header) { h.Set("X-Client-Host", "a_b") }, "X-Client-Host", ""},
		{"host too long", func(h http.Header) { h.Set("X-Client-Host", strings.Repeat("a", 65)) }, "X-Client-Host", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := owner.headers(false)
			c.mod(h)
			e := wantError(t, send(t, "GET", "/v1/actors/me", nil, h), 400, "bad_request")
			if !e.hasField(c.field) {
				t.Errorf("details should name %s: %+v", c.field, e.Error.Details)
			}
			if c.reason != "" && !e.hasReason(c.reason) {
				t.Errorf("details should carry reason %s: %+v", c.reason, e.Error.Details)
			}
		})
	}
	t.Run("valid variants accepted", func(t *testing.T) {
		for _, c := range []caller{
			{"owner", "A", "a"},
			{"claude", "abc.def_ghi-123", "desk-win10.local"},
			{"hermes", strings.Repeat("s", 128), strings.Repeat("h", 62) + ".a"}, // 64 characters
		} {
			expect(t, c.do(t, "GET", "/v1/actors/me", nil), 200)
		}
	})
	t.Run("bad headers on an unknown path give 400 not 404", func(t *testing.T) {
		h := owner.headers(false)
		h.Del("X-Actor")
		wantError(t, send(t, "GET", "/v1/no-such-path", nil, h), 400, "bad_request")
	})
	t.Run("good headers on an unknown path give 404", func(t *testing.T) {
		wantError(t, owner.do(t, "GET", "/v1/no-such-path", nil), 404, "not_found")
		wantError(t, owner.do(t, "GET", "/v2/projects", nil), 404, "not_found")
	})
	t.Run("405 carries Allow", func(t *testing.T) {
		r := owner.do(t, "PUT", "/v1/projects", nil)
		wantError(t, r, 405, "method_not_allowed")
		allow := r.header.Get("Allow")
		if !strings.Contains(allow, "GET") || !strings.Contains(allow, "POST") {
			t.Errorf("Allow = %q", allow)
		}
		r = owner.do(t, "DELETE", "/v1/board", nil)
		wantError(t, r, 405, "method_not_allowed")
		if r.header.Get("Allow") != "GET" {
			t.Errorf("Allow = %q", r.header.Get("Allow"))
		}
	})
}

func TestActors(t *testing.T) {
	t.Run("list declares the configured actors", func(t *testing.T) {
		got := decodeAs[struct {
			Items []actor `json:"items"`
		}](t, owner.do(t, "GET", "/v1/actors", nil), 200)
		want := map[string]string{"owner": "user", "claude": "agent", "codex": "agent", "gemini": "agent", "opencode": "agent", "hermes": "agent"}
		seen := map[string]string{}
		for _, a := range got.Items {
			seen[a.ID] = a.Kind
		}
		for id, kind := range want {
			if seen[id] != kind {
				t.Errorf("actor %s kind = %q, want %q (items %+v)", id, seen[id], kind, got.Items)
			}
		}
	})
	t.Run("me follows X-Actor", func(t *testing.T) {
		for id, kind := range map[string]string{"owner": "user", "claude": "agent", "gemini": "agent"} {
			a := decodeAs[actor](t, as(id).do(t, "GET", "/v1/actors/me", nil), 200)
			if a.ID != id || a.Kind != kind {
				t.Errorf("me as %s = %+v", id, a)
			}
		}
	})
}
