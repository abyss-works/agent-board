package api

import (
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

const (
	testIndex = `<!doctype html><title>agent-board</title><div id="root"></div>`
	testAsset = `console.log("app")`
	// outsideSecret is written next to the served folder, never inside it.
	outsideSecret = "OUTSIDE-SECRET"

	cacheNone      = "no-cache"
	cacheForAssets = "public, max-age=31536000, immutable"

	frameOptions   = "DENY"
	frameAncestors = "frame-ancestors 'none'"
)

func testWebFS() fs.FS {
	return fstest.MapFS{
		"index.html":   {Data: []byte(testIndex)},
		"assets/x.js":  {Data: []byte(testAsset)},
		"assets/x.css": {Data: []byte("body{}")},
		"robots.txt":   {Data: []byte("User-agent: *")},
	}
}

// newWebEnv is newEnv with web mounted.
func newWebEnv(t *testing.T, web fs.FS) *env {
	t.Helper()
	e := newEnv(t)
	logger := slog.New(slog.NewJSONHandler(e.log, nil))
	e.h = New(Options{Token: testToken, Actors: testActors, Version: "test", Logger: logger, Store: e.st, Web: web})
	return e
}

// plain sends a request with neither Authorization nor trace headers.
func (e *env) plain(method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func writeTestFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWebServesIndexForClientRoutes(t *testing.T) {
	e := newWebEnv(t, testWebFS())
	// Directories are never listed; they are paths without a file like any other route.
	for _, target := range []string{"/", "/index.html", "/issues/AGB-1", "/projects/AGB/board", "/assets", "/assets/"} {
		rec := e.plain("GET", target)
		if rec.Code != 200 || rec.Body.String() != testIndex {
			t.Errorf("GET %s: status %d body %q", target, rec.Code, rec.Body.String())
			continue
		}
		if cc := rec.Header().Get("Cache-Control"); cc != cacheNone {
			t.Errorf("GET %s: Cache-Control = %q, want %q", target, cc, cacheNone)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s: Content-Type = %q", target, ct)
		}
	}
}

func TestWebServesAssetsWithLongCache(t *testing.T) {
	e := newWebEnv(t, testWebFS())
	cases := []struct{ target, body, contentType string }{
		{"/assets/x.js", testAsset, "javascript"},
		{"/assets/x.css", "body{}", "text/css"},
	}
	for _, c := range cases {
		rec := e.plain("GET", c.target)
		if rec.Code != 200 || rec.Body.String() != c.body {
			t.Errorf("GET %s: status %d body %q", c.target, rec.Code, rec.Body.String())
			continue
		}
		if cc := rec.Header().Get("Cache-Control"); cc != cacheForAssets {
			t.Errorf("GET %s: Cache-Control = %q, want %q", c.target, cc, cacheForAssets)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, c.contentType) {
			t.Errorf("GET %s: Content-Type = %q, want %q", c.target, ct, c.contentType)
		}
	}
}

func TestWebRevalidatesFilesOutsideAssets(t *testing.T) {
	e := newWebEnv(t, testWebFS())
	rec := e.plain("GET", "/robots.txt")
	if rec.Code != 200 || rec.Body.String() != "User-agent: *" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != cacheNone {
		t.Errorf("Cache-Control = %q, want %q", cc, cacheNone)
	}
}

func TestWebMissingFileWithExtensionIs404(t *testing.T) {
	e := newWebEnv(t, testWebFS())
	for _, target := range []string{"/assets/none.js", "/assets/none/x.js", "/missing.png", "/favicon.ico", "/.env"} {
		rec := e.plain("GET", target)
		if rec.Code != 404 {
			t.Errorf("GET %s: status = %d, want 404", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "agent-board") {
			t.Errorf("GET %s: answered with the index page", target)
		}
	}
}

func TestWebAllowsOnlyReadMethods(t *testing.T) {
	e := newWebEnv(t, testWebFS())
	for _, target := range []string{"/", "/issues/AGB-1", "/assets/x.js"} {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			rec := e.plain(method, target)
			if rec.Code != 405 || rec.Header().Get("Allow") != "GET, HEAD" {
				t.Errorf("%s %s: status %d Allow %q", method, target, rec.Code, rec.Header().Get("Allow"))
			}
		}
	}
	rec := e.plain("HEAD", "/assets/x.js")
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Cache-Control") != cacheForAssets {
		t.Errorf("HEAD: status %d body %q Cache-Control %q", rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
}

func TestWebForbidsFraming(t *testing.T) {
	e := newWebEnv(t, testWebFS())
	cases := []struct {
		method, target string
		status         int
	}{
		{"GET", "/assets/x.js", 200}, // an existing file
		{"GET", "/device", 200},      // a client route, answered with index.html
		{"GET", "/missing.png", 404}, // a missing file
		{"POST", "/device", 405},     // a method that is not allowed
	}
	for _, c := range cases {
		rec := e.plain(c.method, c.target)
		if rec.Code != c.status {
			t.Errorf("%s %s: status = %d, want %d", c.method, c.target, rec.Code, c.status)
		}
		if got := rec.Header().Get("X-Frame-Options"); got != frameOptions {
			t.Errorf("%s %s: X-Frame-Options = %q, want %q", c.method, c.target, got, frameOptions)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != frameAncestors {
			t.Errorf("%s %s: Content-Security-Policy = %q, want %q", c.method, c.target, got, frameAncestors)
		}
	}
}

func TestWebKeepsTheAPIBehindAuth(t *testing.T) {
	e := newWebEnv(t, testWebFS())

	rec := e.plain("GET", "/v1/health")
	if rec.Code != 200 {
		t.Fatalf("health: status %d: %s", rec.Code, rec.Body.String())
	}
	var health map[string]any
	decodeJSON(t, rec, &health)
	if health["status"] != "ok" {
		t.Errorf("health = %v", health)
	}

	rec = e.plain("GET", "/v1/projects")
	if rec.Code != 401 || decodeErr(t, rec).Error.Code != "unauthorized" {
		t.Errorf("projects without token: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(t, "GET", "/v1/projects", "", map[string]string{"X-Actor": "-"})
	if rec.Code != 400 || decodeErr(t, rec).Error.Code != "bad_request" {
		t.Errorf("projects without X-Actor: %d %s", rec.Code, rec.Body.String())
	}
	if rec = e.do(t, "GET", "/v1/actors", "", nil); rec.Code != 200 {
		t.Errorf("actors: %d %s", rec.Code, rec.Body.String())
	}

	// Unknown paths under /v1 stay JSON errors and never fall back to the web client.
	for _, target := range []string{"/v1", "/v1/", "/v1/nope", "/v1/nope/deeper"} {
		rec = e.do(t, "GET", target, "", nil)
		if rec.Code != 404 || decodeErr(t, rec).Error.Code != "not_found" {
			t.Errorf("GET %s: %d %s", target, rec.Code, rec.Body.String())
		}
		if rec = e.plain("GET", target); rec.Code != 401 {
			t.Errorf("GET %s without token: status = %d, want 401", target, rec.Code)
		}
	}
	if rec = e.do(t, "POST", "/v1/actors", "", nil); rec.Code != 405 {
		t.Errorf("POST /v1/actors: status = %d, want 405", rec.Code)
	}
}

func TestWebCannotLeaveTheFolder(t *testing.T) {
	root := t.TempDir()
	web := filepath.Join(root, "web")
	writeTestFile(t, filepath.Join(web, "index.html"), testIndex)
	writeTestFile(t, filepath.Join(web, "assets", "x.js"), testAsset)
	writeTestFile(t, filepath.Join(root, "outside.txt"), outsideSecret)

	e := newWebEnv(t, os.DirFS(web))
	handlers := map[string]http.Handler{"router": e.h, "web handler": webHandler(os.DirFS(web))}
	// The encoded forms reach the web handler as ".." elements in URL.Path,
	// bypassing the router's own path cleaning.
	targets := []string{
		"/../outside.txt",
		"/assets/../../outside.txt",
		"/%2e%2e/outside.txt",
		"/..%2foutside.txt",
		"/assets/..%2f..%2foutside.txt",
		"/..%5coutside.txt",
	}
	for name, h := range handlers {
		for _, target := range targets {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
			if strings.Contains(rec.Body.String(), outsideSecret) {
				t.Errorf("%s: GET %s returned a file from outside the folder", name, target)
			}
			if rec.Code == 200 {
				t.Errorf("%s: GET %s: status = 200, want a redirect or 404", name, target)
			}
		}
	}
}

func TestWithoutWebFolderPathsOutsideV1StayInTheAPI(t *testing.T) {
	e := newEnv(t)
	for _, target := range []string{"/", "/index.html", "/issues/AGB-1", "/assets/x.js"} {
		if rec := e.plain("GET", target); rec.Code != 401 {
			t.Errorf("GET %s without token: status = %d, want 401", target, rec.Code)
		}
		rec := e.do(t, "GET", target, "", nil)
		if rec.Code != 404 || decodeErr(t, rec).Error.Code != "not_found" {
			t.Errorf("GET %s: %d %s", target, rec.Code, rec.Body.String())
		}
	}
}
