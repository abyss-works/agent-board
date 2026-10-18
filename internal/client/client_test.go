package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/wsk0715/agent-board/internal/api"
	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

const testToken = "test-token-0123456789abcdef0123456789" // dummy

func newClient(url string) *Client {
	return &Client{BaseURL: url, Token: testToken, Actor: "owner", SessionID: "sess-1", ClientHost: "desk", Version: "t"}
}

func TestClientSendsRequiredHeaders(t *testing.T) {
	var got http.Header
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		w.Write([]byte(`{"key":"AGB"}`))
	}))
	defer srv.Close()
	c := newClient(srv.URL)
	var out struct {
		Key string `json:"key"`
	}
	if err := c.Do(context.Background(), "POST", "/v1/projects", map[string]string{"key": "AGB"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Key != "AGB" {
		t.Errorf("out = %+v", out)
	}
	for k, want := range map[string]string{
		"Authorization": "Bearer " + testToken, "X-Actor": "owner", "X-Session-Id": "sess-1", "X-Client-Host": "desk",
		"Content-Type": "application/json; charset=utf-8", "User-Agent": "agb/t",
	} {
		if got.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, got.Get(k), want)
		}
	}
	if string(gotBody) != `{"key":"AGB"}`+"\n" && string(gotBody) != `{"key":"AGB"}` {
		t.Errorf("body = %q", gotBody)
	}
}

func TestClientMapsAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(422)
		w.Write([]byte(`{"error":{"code":"validation_failed","message":"bad","details":[{"field":"title","message":"m"}]}}`))
	}))
	defer srv.Close()
	err := newClient(srv.URL).Do(context.Background(), "POST", "/v1/issues", map[string]string{}, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	if apiErr.Status != 422 || apiErr.Code != "validation_failed" || apiErr.Message != "bad" || len(apiErr.Details) != 1 || apiErr.Details[0].Field != "title" {
		t.Errorf("apiErr = %+v", apiErr)
	}
	if apiErr.Error() == "" {
		t.Error("empty error string")
	}
}

func TestClientAgainstRealHandler(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "agb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	actors := []model.Actor{{ID: "owner", Kind: model.ActorUser}}
	if err := st.EnsureActors(context.Background(), actors); err != nil {
		t.Fatal(err)
	}
	h := api.New(api.Options{Token: testToken, Actors: actors, Version: "t", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: st})
	srv := httptest.NewServer(h)
	defer srv.Close()

	c := newClient(srv.URL)
	health, err := c.Health(context.Background())
	if err != nil || health.Status != "ok" || health.APIVersion != 1 {
		t.Fatalf("health = %+v %v", health, err)
	}
	me, err := c.Me(context.Background())
	if err != nil || me.ID != "owner" {
		t.Fatalf("me = %+v %v", me, err)
	}
	var p model.Project
	if err := c.Do(context.Background(), "POST", "/v1/projects", map[string]string{"key": "AGB", "name": "n"}, &p); err != nil || p.Key != "AGB" {
		t.Fatalf("create project = %+v %v", p, err)
	}
	var raw json.RawMessage
	if err := c.Do(context.Background(), "GET", "/v1/projects/AGB/overview?limit=5", nil, &raw); err != nil || len(raw) == 0 {
		t.Fatalf("overview = %v", err)
	}
	// A client with a wrong session id format is rejected by the server.
	bad := newClient(srv.URL)
	bad.SessionID = "has space"
	var apiErr *Error
	if err := bad.Do(context.Background(), "GET", "/v1/actors/me", nil, nil); !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Errorf("bad session id err = %v", err)
	}
}
