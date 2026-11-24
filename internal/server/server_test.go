package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/config"
	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
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

func TestStartFailureClosesStore(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	dataDir := filepath.Join(t.TempDir(), "data")
	cfg := &config.Config{
		Addr:    busy.Addr().String(),
		DataDir: dataDir,
		Token:   testToken,
		Actors:  []model.Actor{{ID: "owner", Kind: model.ActorUser}},
	}
	srv, err := New(cfg, Options{Version: "t", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Start(); err == nil {
		t.Fatal("Start succeeded on an address already in use")
	}
	if _, _, err := srv.store.ListProjects(context.Background(), store.ProjectFilter{}); err == nil {
		t.Fatal("store is still open after a failed Start")
	}
	if err := os.Remove(filepath.Join(dataDir, "agb.db")); err != nil {
		t.Fatalf("database file is still held after a failed Start: %v", err)
	}
}

func TestShutdownClosesOpenEventStreams(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Addr:    "127.0.0.1:0",
		DataDir: filepath.Join(dir, "data"),
		Token:   testToken,
		Actors:  []model.Actor{{ID: "owner", Kind: model.ActorUser}},
	}
	srv, err := New(cfg, Options{Version: "t", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	addr, err := srv.Start()
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("GET", "http://"+addr+"/v1/events/stream", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("X-Actor", "owner")
	req.Header.Set("X-Session-Id", "s-stream")
	req.Header.Set("X-Client-Host", "h1")
	resp, err := (&http.Client{Timeout: 0}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}

	readDone := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(resp.Body)
		readDone <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown with open stream failed: %v", err)
	}
	select {
	case err := <-readDone:
		if err != nil {
			t.Errorf("stream body read after shutdown = %v, want clean EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("open event stream did not close on Shutdown")
	}
}

func TestServerPersistsDynamicallyRegisteredActorAcrossRestart(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	newCfg := func() *config.Config {
		return &config.Config{
			Addr:    "127.0.0.1:0",
			DataDir: dataDir,
			Token:   testToken,
			Actors:  []model.Actor{{ID: "owner", Kind: model.ActorUser}},
		}
	}

	srv1, err := New(newCfg(), Options{Version: "t", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	addr1, err := srv1.Start()
	if err != nil {
		t.Fatal(err)
	}
	base1 := "http://" + addr1

	// Start device authorization for undeclared agent "devin".
	resp, err := http.Post(base1+"/v1/device-authorizations", "application/json", bytes.NewBufferString(`{"actor":"devin","client_host":"desk"}`))
	if err != nil {
		t.Fatal(err)
	}
	var grant model.DeviceGrant
	if err := json.NewDecoder(resp.Body).Decode(&grant); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("create device auth = %d", resp.StatusCode)
	}

	// Approve as owner.
	req, _ := http.NewRequest("PATCH", base1+"/v1/device-authorizations/"+grant.UserCode, bytes.NewBufferString(`{"status":"approved"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("X-Actor", "owner")
	req.Header.Set("X-Session-Id", "s1")
	req.Header.Set("X-Client-Host", "h1")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("approve = %d", resp.StatusCode)
	}

	// Exchange token.
	resp, err = http.Post(base1+"/v1/tokens", "application/json", bytes.NewBufferString(`{"device_code":"`+grant.DeviceCode+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var issued model.IssuedToken
	if err := json.NewDecoder(resp.Body).Decode(&issued); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("exchange = %d", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv1.Shutdown(ctx); err != nil {
		t.Fatalf("srv1.Shutdown: %v", err)
	}

	// Restart server with the same cfg.Actors (only "owner") and same DataDir.
	srv2, err := New(newCfg(), Options{Version: "t", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	addr2, err := srv2.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		_ = srv2.Shutdown(ctx2)
	}()
	base2 := "http://" + addr2

	// devin remains valid after restart!
	req2, _ := http.NewRequest("GET", base2+"/v1/actors/me", nil)
	req2.Header.Set("Authorization", "Bearer "+issued.Secret)
	req2.Header.Set("X-Actor", "devin")
	req2.Header.Set("X-Session-Id", "s2")
	req2.Header.Set("X-Client-Host", "desk")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	var me model.Actor
	if err := json.NewDecoder(resp2.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 200 || me.ID != "devin" || me.Kind != model.ActorAgent {
		t.Fatalf("after restart actors/me = %d %+v", resp2.StatusCode, me)
	}
}
