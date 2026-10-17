// Package client is the HTTP client the CLI commands will build on. It
// talks to the server over HTTP only and carries the token and the three
// mandatory trace headers on every request.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// Client holds the connection settings. Zero HTTP uses http.DefaultClient.
type Client struct {
	BaseURL    string
	Token      string
	Actor      string
	SessionID  string
	ClientHost string
	Version    string // reported in User-Agent as agb/<Version>
	HTTP       *http.Client
}

// Detail mirrors the server's error detail.
type Detail struct {
	Field   string `json:"field"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// Error is a non-2xx response decoded from the server's error envelope.
type Error struct {
	Status  int
	Code    string
	Message string
	Details []Detail
}

func (e *Error) Error() string {
	return fmt.Sprintf("agb: %d %s: %s", e.Status, e.Code, e.Message)
}

// Health is the /v1/health response.
type Health struct {
	Status     string `json:"status"`
	Version    string `json:"version"`
	APIVersion int    `json:"api_version"`
}

// Do sends method path with an optional JSON body and decodes the JSON
// response into out when out is non-nil. Non-2xx responses become *Error.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("X-Actor", c.Actor)
	req.Header.Set("X-Session-Id", c.SessionID)
	req.Header.Set("X-Client-Host", c.ClientHost)
	req.Header.Set("User-Agent", "agb/"+c.Version)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &Error{Status: resp.StatusCode}
		var env struct {
			Error struct {
				Code    string   `json:"code"`
				Message string   `json:"message"`
				Details []Detail `json:"details"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &env) == nil && env.Error.Code != "" {
			apiErr.Code, apiErr.Message, apiErr.Details = env.Error.Code, env.Error.Message, env.Error.Details
		} else {
			apiErr.Code, apiErr.Message = "http_error", strings.TrimSpace(string(data))
		}
		return apiErr
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

// Health fetches the server status. It needs no token or trace headers but
// sending them does no harm.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	err := c.Do(ctx, http.MethodGet, "/v1/health", nil, &h)
	return h, err
}

// Me returns the actor the server resolved from the X-Actor header.
func (c *Client) Me(ctx context.Context) (model.Actor, error) {
	var a model.Actor
	err := c.Do(ctx, http.MethodGet, "/v1/actors/me", nil, &a)
	return a, err
}
