// Package client is the HTTP client the CLI commands build on. It talks to
// the server over HTTP only and carries the token and the three mandatory
// trace headers on every request.
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
// Body is the response body as the server sent it.
type Error struct {
	Status  int
	Code    string
	Message string
	Details []Detail
	Body    []byte
}

func (e *Error) Error() string {
	return fmt.Sprintf("agb: %d %s: %s", e.Status, e.Code, e.Message)
}

// Response is a 2xx response with its body kept verbatim.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Decode unmarshals the body into out.
func (r *Response) Decode(out any) error { return json.Unmarshal(r.Body, out) }

// Health is the /v1/health response.
type Health struct {
	Status     string `json:"status"`
	Version    string `json:"version"`
	APIVersion int    `json:"api_version"`
}

// Request sends method path with an optional JSON body and extra headers
// and returns the raw response. Non-2xx responses become *Error.
func (c *Client) Request(ctx context.Context, method, path string, body any, extra http.Header) (*Response, error) {
	return c.send(ctx, method, path, body, extra, true)
}

// send is Request, with the token and the trace headers only when
// authenticated is true. The two calls that start a login are made before the
// client has a token and send none of them.
func (c *Client) send(ctx context.Context, method, path string, body any, extra http.Header, authenticated bool) (*Response, error) {
	var rd io.Reader
	if body != nil {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(body); err != nil {
			return nil, err
		}
		rd = &buf
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("X-Actor", c.Actor)
		req.Header.Set("X-Session-Id", c.SessionID)
		req.Header.Set("X-Client-Host", c.ClientHost)
	}
	req.Header.Set("User-Agent", "agb/"+c.Version)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	for k, vs := range extra {
		req.Header[k] = append([]string(nil), vs...)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &Error{Status: resp.StatusCode, Body: data}
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
		return nil, apiErr
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: data}, nil
}

// Do sends method path with an optional JSON body and decodes the JSON
// response into out when out is non-nil. Non-2xx responses become *Error.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	resp, err := c.Request(ctx, method, path, body, nil)
	if err != nil {
		return err
	}
	if out == nil || len(resp.Body) == 0 {
		return nil
	}
	return resp.Decode(out)
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
