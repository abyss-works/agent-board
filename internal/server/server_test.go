package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/config"
	"github.com/wsk0715/agent-board/internal/model"
)

const testToken = "test-token-0123456789abcdef0123456789" // dummy

func TestServerStartsServesAndStops(t *testing.T) {
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
	if _, err := os.Stat(filepath.Join(dir, "data", "agb.db")); err != nil {
		t.Errorf("database file not created in data dir: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Errorf("health = %v", body)
	}

	req, _ := http.NewRequest("GET", "http://"+addr+"/v1/actors/me", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("X-Actor", "owner")
	req.Header.Set("X-Session-Id", "s1")
	req.Header.Set("X-Client-Host", "h1")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Errorf("actors/me = %d", resp2.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if _, err := http.Get("http://" + addr + "/v1/health"); err == nil {
		t.Error("server still accepting connections after Shutdown")
	}
}
