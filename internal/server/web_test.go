package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/config"
	"github.com/wsk0715/agent-board/internal/model"
)

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, url string) (int, http.Header, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, string(body)
}

func TestServerServesWebFolder(t *testing.T) {
	dir := t.TempDir()
	web := filepath.Join(dir, "web")
	writeFile(t, filepath.Join(web, "index.html"), "<title>agent-board</title>")
	writeFile(t, filepath.Join(web, "assets", "app.js"), "console.log(1)")
	cfg := &config.Config{
		Addr:    "127.0.0.1:0",
		DataDir: filepath.Join(dir, "data"),
		Token:   testToken,
		Actors:  []model.Actor{{ID: "owner", Kind: model.ActorUser}},
		WebDir:  web,
	}
	logs := &bytes.Buffer{}
	srv, err := New(cfg, Options{Version: "t", Logger: slog.New(slog.NewJSONHandler(logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	addr, err := srv.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	}()
	base := "http://" + addr

	for _, path := range []string{"/", "/issues/AGB-1"} {
		if code, _, body := get(t, base+path); code != 200 || body != "<title>agent-board</title>" {
			t.Errorf("GET %s: %d %q", path, code, body)
		}
	}
	code, header, body := get(t, base+"/assets/app.js")
	if code != 200 || body != "console.log(1)" || !strings.Contains(header.Get("Cache-Control"), "immutable") {
		t.Errorf("GET /assets/app.js: %d %q Cache-Control %q", code, body, header.Get("Cache-Control"))
	}
	if code, _, _ := get(t, base+"/assets/none.js"); code != 404 {
		t.Errorf("GET /assets/none.js: status = %d, want 404", code)
	}
	if code, _, _ := get(t, base+"/v1/health"); code != 200 {
		t.Errorf("GET /v1/health: status = %d, want 200", code)
	}
	if code, _, _ := get(t, base+"/v1/projects"); code != 401 {
		t.Errorf("GET /v1/projects without token: status = %d, want 401", code)
	}
	if !strings.Contains(logs.String(), "serving web client") {
		t.Errorf("startup log does not mention the web client: %s", logs.String())
	}
}

func TestServerWithoutWebFolderServesNoPages(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Addr:    "127.0.0.1:0",
		DataDir: filepath.Join(dir, "data"),
		Token:   testToken,
		Actors:  []model.Actor{{ID: "owner", Kind: model.ActorUser}},
	}
	logs := &bytes.Buffer{}
	srv, err := New(cfg, Options{Version: "t", Logger: slog.New(slog.NewJSONHandler(logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	addr, err := srv.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()
	if code, _, _ := get(t, "http://"+addr+"/"); code != 401 {
		t.Errorf("GET / without a web folder: status = %d, want 401 (API only)", code)
	}
	if strings.Contains(logs.String(), "serving web client") {
		t.Errorf("startup log mentions the web client although none is configured: %s", logs.String())
	}
}
