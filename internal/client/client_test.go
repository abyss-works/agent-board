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
	"strings"
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

// TestDeviceLoginAgainstRealHandler runs the whole login with the client: the
// agent has no token, the user approves with the shared token, the agent
// exchanges its device code and works with the token it gets.
func TestDeviceLoginAgainstRealHandler(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "agb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	actors := []model.Actor{{ID: "owner", Kind: model.ActorUser}, {ID: "codex", Kind: model.ActorAgent}}
	if err := st.EnsureActors(context.Background(), actors); err != nil {
		t.Fatal(err)
	}
	h := api.New(api.Options{Token: testToken, Actors: actors, Version: "t", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: st})
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx := context.Background()

	user := newClient(srv.URL)
	agent := &Client{BaseURL: srv.URL, Actor: "codex", SessionID: "sess-1", ClientHost: "desk", Version: "t"} // no token yet

	resp, err := agent.CreateDeviceAuthorization(ctx, "codex", "DESKTOP-RPLTFHM")
	if err != nil || resp.Status != 201 {
		t.Fatalf("create device authorization = %+v, %v", resp, err)
	}
	var grant model.DeviceGrant
	if err := resp.Decode(&grant); err != nil || grant.DeviceCode == "" || grant.UserCode == "" || grant.ExpiresIn != 600 || grant.Interval != 5 {
		t.Fatalf("grant = %+v, %v", grant, err)
	}

	var apiErr *Error
	if _, err := agent.ExchangeToken(ctx, grant.DeviceCode); !errors.As(err, &apiErr) || apiErr.Status != 400 || apiErr.Code != "authorization_pending" {
		t.Fatalf("exchange before the decision: %v", err)
	}
	// An agent without a token cannot read its own request.
	if _, err := agent.GetDeviceAuthorization(ctx, grant.UserCode); !errors.As(err, &apiErr) || apiErr.Status != 401 {
		t.Errorf("an agent without a token reads the request: %v", err)
	}

	resp, err = user.GetDeviceAuthorization(ctx, grant.UserCode)
	var pending model.DeviceAuthorization
	if err != nil || resp.Decode(&pending) != nil || pending.Actor != "codex" || pending.ClientHost != "DESKTOP-RPLTFHM" || pending.Status != model.DevicePending {
		t.Fatalf("get device authorization = %+v, %v", pending, err)
	}
	resp, err = user.PatchDeviceAuthorization(ctx, grant.UserCode, "approved")
	var approved model.DeviceAuthorization
	if err != nil || resp.Decode(&approved) != nil || approved.Status != model.DeviceApproved {
		t.Fatalf("approve = %+v, %v", approved, err)
	}

	resp, err = agent.ExchangeToken(ctx, grant.DeviceCode)
	if err != nil || resp.Status != 201 {
		t.Fatalf("exchange = %+v, %v", resp, err)
	}
	var issued model.IssuedToken
	if err := resp.Decode(&issued); err != nil || issued.Actor != "codex" || issued.CreatedBy != "owner" || !strings.HasPrefix(issued.Secret, "agb_") {
		t.Fatalf("issued = %+v, %v", issued, err)
	}

	// With the token the agent is itself.
	agent.Token = issued.Secret
	if me, err := agent.Me(ctx); err != nil || me.ID != "codex" {
		t.Fatalf("me with the actor token = %+v, %v", me, err)
	}
	// The shared token does not serve an agent, and the agent token does not serve the user.
	wrong := newClient(srv.URL)
	wrong.Actor = "codex"
	if _, err := wrong.Me(ctx); !errors.As(err, &apiErr) || apiErr.Status != 403 {
		t.Errorf("shared token as an agent: %v", err)
	}
	if _, err := agent.ListTokens(ctx, ""); !errors.As(err, &apiErr) || apiErr.Status != 403 || len(apiErr.Details) == 0 || apiErr.Details[0].Reason != "user_only" {
		t.Errorf("an agent lists the tokens: %v", err)
	}

	resp, err = user.ListTokens(ctx, "")
	var listed struct {
		Items []model.Token `json:"items"`
	}
	if err != nil || resp.Decode(&listed) != nil || len(listed.Items) != 1 || listed.Items[0].ID != issued.ID || listed.Items[0].LastUsedAt == nil {
		t.Fatalf("list tokens = %+v, %v", listed, err)
	}

	// The agent revokes its own token; from then on it is refused.
	resp, err = agent.RevokeCurrentToken(ctx)
	var revoked model.Token
	if err != nil || resp.Decode(&revoked) != nil || revoked.ID != issued.ID || revoked.RevokedBy == nil || *revoked.RevokedBy != "codex" {
		t.Fatalf("revoke current = %+v, %v", revoked, err)
	}
	if _, err := agent.Me(ctx); !errors.As(err, &apiErr) || apiErr.Status != 401 {
		t.Errorf("revoked token: %v", err)
	}
	resp, err = user.ListTokens(ctx, "revoked")
	if err != nil || resp.Decode(&listed) != nil || len(listed.Items) != 1 {
		t.Fatalf("list revoked tokens = %+v, %v", listed, err)
	}
	// The user can revoke by id; a token that is already revoked stays as it is.
	if resp, err = user.RevokeToken(ctx, issued.ID); err != nil || resp.Status != 200 {
		t.Errorf("revoke by id = %+v, %v", resp, err)
	}
	if _, err := user.RevokeToken(ctx, 9999); !errors.As(err, &apiErr) || apiErr.Status != 404 {
		t.Errorf("revoke a missing token: %v", err)
	}
}
