package embed

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

const testKey = "sk-test-key-0123456789" // dummy

func TestClientEmbed(t *testing.T) {
	var got struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		// The data comes back out of order; index says which input each belongs to.
		w.Write([]byte(`{"data":[{"index":1,"embedding":[0,2]},{"index":0,"embedding":[1,0.5]}]}`))
	}))
	defer srv.Close()

	vecs, id, err := NewClient(srv.URL, testKey, "m").Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer "+testKey || got.Model != "m" || !slices.Equal(got.Input, []string{"a", "b"}) {
		t.Errorf("request = auth %q, %+v", auth, got)
	}
	if len(vecs) != 2 || !slices.Equal(vecs[0], []float32{1, 0.5}) || !slices.Equal(vecs[1], []float32{0, 2}) {
		t.Errorf("vectors = %v", vecs)
	}
	if id != "" {
		t.Errorf("response id = %q, want empty when the response has none", id)
	}
}

func TestClientEmbedReadsTheResponseID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"embd-1","data":[{"index":0,"embedding":[1]}]}`))
	}))
	defer srv.Close()
	_, id, err := NewClient(srv.URL, testKey, "m").Embed(context.Background(), []string{"a"})
	if err != nil || id != "embd-1" {
		t.Errorf("response id %q, err %v", id, err)
	}
}

// TestClientTimeoutIsLoggedAsTimeout runs the real client against a server that
// never answers, so the error chain the client returns is what decides the result.
func TestClientTimeoutIsLoggedAsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // the server notices a closed connection only once the body is read
		<-r.Context().Done()
	}))
	defer srv.Close()
	lines := logged(NewClient(srv.URL, testKey, "m"), 50*time.Millisecond, "q")
	if len(lines) != 1 || lines[0]["result"] != "timeout" {
		t.Errorf("lines = %v", lines)
	}
}

func TestClientEmbedErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"status":        {401, `{"error":{"message":"bad key ` + testKey + `"}}`, "401"},
		"plain":         {500, "upstream down", "upstream down"},
		"count":         {200, `{"data":[{"index":0,"embedding":[1]}]}`, "1 vectors for 2 inputs"},
		"index":         {200, `{"data":[{"index":0,"embedding":[1]},{"index":5,"embedding":[1]}]}`, "index"},
		"empty vector":  {200, `{"data":[{"index":0,"embedding":[]},{"index":1,"embedding":[1]}]}`, "empty"},
		"not json":      {200, `<html>`, "decode"},
		"error in 200":  {200, `{"error":{"message":"rate limited"}}`, "rate limited"},
		"empty message": {503, ``, "503"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			w.Write([]byte(tc.body))
		}))
		_, _, err := NewClient(srv.URL, testKey, "m").Embed(context.Background(), []string{"a", "b"})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), testKey) {
			t.Errorf("%s: err = %v, want it to contain %q and not the key", name, err, tc.want)
		}
	}
	// An unreachable server must not leak the key either.
	if _, _, err := NewClient("http://127.0.0.1:1", testKey, "m").Embed(context.Background(), []string{"a"}); err == nil || strings.Contains(err.Error(), testKey) {
		t.Errorf("unreachable: err = %v", err)
	}
}
