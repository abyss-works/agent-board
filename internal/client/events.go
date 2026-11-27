package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// WatchParams configures a subscription to GET /v1/events/stream.
type WatchParams struct {
	Project     string
	Kinds       []string
	After       *int64
	LastEventID string
}

// EventStream opens GET /v1/events/stream with the given filter and cursor.
func (c *Client) EventStream(ctx context.Context, p WatchParams) (*http.Response, error) {
	q := url.Values{}
	if p.Project != "" {
		q.Set("project", p.Project)
	}
	if len(p.Kinds) > 0 {
		q.Set("kind", strings.Join(p.Kinds, ","))
	}
	if p.After != nil {
		q.Set("after", strconv.FormatInt(*p.After, 10))
	}
	path := "/v1/events/stream"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var extra http.Header
	if p.LastEventID != "" {
		extra = http.Header{"Last-Event-ID": []string{p.LastEventID}}
	}
	return c.Stream(ctx, path, extra)
}
