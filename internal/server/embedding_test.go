package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/config"
	"github.com/wsk0715/agent-board/internal/model"
)

// TestEmbeddingProviderReachesEveryEmbeddingRequest runs a server whose
// embeddings endpoint is a local one and reads the bodies it receives: both the
// indexing request and the query request carry the configured provider object.
func TestEmbeddingProviderReachesEveryEmbeddingRequest(t *testing.T) {
	const provider = `{"order":["x"]}`
	const prefix = "Query: "
	type embedRequest struct {
		Input    []string        `json:"input"`
		Provider json.RawMessage `json:"provider"`
	}
	var mu sync.Mutex
	var indexing, query []string // provider of each request, by kind
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		mu.Lock()
		if len(req.Input) == 1 && strings.HasPrefix(req.Input[0], prefix) {
			query = append(query, string(req.Provider))
		} else {
			indexing = append(indexing, string(req.Provider))
		}
		mu.Unlock()
		data := make([]map[string]any, len(req.Input))
		for i := range req.Input {
			data[i] = map[string]any{"index": i, "embedding": []float32{1, 0}}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer api.Close()
	count := func(s *[]string) int {
		mu.Lock()
		defer mu.Unlock()
		return len(*s)
	}

	cfg := &config.Config{
		Addr:    "127.0.0.1:0",
		DataDir: filepath.Join(t.TempDir(), "data"),
		Token:   testToken,
		Actors:  []model.Actor{{ID: "owner", Kind: model.ActorUser}},
		Embedding: config.Embedding{
			APIKey: "dummy", URL: api.URL, Model: "m", QueryPrefix: prefix,
			Interval: 20 * time.Millisecond, Provider: json.RawMessage(provider),
		},
	}
	srv, err := New(cfg, Options{Version: "t", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
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
	// Each request uses its own connection, so none is left open and unused
	// for Shutdown to wait on.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	do := func(method, path, body string) {
		t.Helper()
		req, _ := http.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("X-Actor", "owner")
		req.Header.Set("X-Session-Id", "s1")
		req.Header.Set("X-Client-Host", "h1")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			t.Fatalf("%s %s = %d: %s", method, path, resp.StatusCode, raw)
		}
	}

	do("POST", "/v1/projects", `{"key":"P1","name":"p"}`)
	do("POST", "/v1/issues", `{"project":"P1","title":"first issue"}`)
	deadline := time.Now().Add(3 * time.Second)
	for count(&indexing) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	do("GET", "/v1/search?q=issue", "")

	mu.Lock()
	defer mu.Unlock()
	if len(indexing) == 0 || len(query) == 0 {
		t.Fatalf("embedding requests: %d indexing, %d query, want at least one of each", len(indexing), len(query))
	}
	for _, got := range append(indexing, query...) {
		if got != provider {
			t.Errorf("provider in an embedding request = %q, want %s (indexing %q, query %q)", got, provider, indexing, query)
			break
		}
	}
}
