package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Page is the limit/offset pair of list endpoints. Zero means "server
// default" and is not sent.
type Page struct {
	Limit  int
	Offset int
}

func (p Page) addTo(v url.Values) {
	if p.Limit > 0 {
		v.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Offset > 0 {
		v.Set("offset", strconv.Itoa(p.Offset))
	}
}

// set adds name=value unless value is empty.
func set(v url.Values, name, value string) {
	if value != "" {
		v.Set(name, value)
	}
}

func withQuery(path string, v url.Values) string {
	if len(v) == 0 {
		return path
	}
	return path + "?" + v.Encode()
}

// ProjectFilter selects projects. Comma separated values are OR.
type ProjectFilter struct {
	Status string
	Sort   string
	Page
}

// Values encodes the filter as query parameters.
func (f ProjectFilter) Values() url.Values {
	v := url.Values{}
	set(v, "status", f.Status)
	set(v, "sort", f.Sort)
	f.Page.addTo(v)
	return v
}

// IssueFilter selects issues. Comma separated values are OR; Labels are
// repeated and therefore AND.
type IssueFilter struct {
	Project         string
	Status          string
	Type            string
	Priority        string
	Labels          []string
	Parent          string
	CreatedBy       string
	UpdatedBy       string
	StatusChangedBy string
	Query           string
	Sort            string
	Limit           int
	Offset          int
}

// Values encodes the filter as query parameters.
func (f IssueFilter) Values() url.Values {
	v := url.Values{}
	set(v, "project", f.Project)
	set(v, "status", f.Status)
	set(v, "type", f.Type)
	set(v, "priority", f.Priority)
	for _, l := range f.Labels {
		v.Add("label", l)
	}
	set(v, "parent", f.Parent)
	set(v, "created_by", f.CreatedBy)
	set(v, "updated_by", f.UpdatedBy)
	set(v, "status_changed_by", f.StatusChangedBy)
	set(v, "q", f.Query)
	set(v, "sort", f.Sort)
	Page{Limit: f.Limit, Offset: f.Offset}.addTo(v)
	return v
}

// ActivityFilter selects activity records. Comma separated values are OR.
type ActivityFilter struct {
	Project    string
	Issue      string
	Actor      string
	Session    string
	ClientHost string
	Kind       string
	Field      string
	Since      string
	Until      string
	Sort       string
	Page
}

// Values encodes the filter as query parameters.
func (f ActivityFilter) Values() url.Values {
	v := url.Values{}
	set(v, "project", f.Project)
	set(v, "issue", f.Issue)
	set(v, "actor", f.Actor)
	set(v, "session", f.Session)
	set(v, "client_host", f.ClientHost)
	set(v, "kind", f.Kind)
	set(v, "field", f.Field)
	set(v, "since", f.Since)
	set(v, "until", f.Until)
	set(v, "sort", f.Sort)
	f.Page.addTo(v)
	return v
}

// BoardParams selects the board. ClosedDays below zero means "server
// default"; zero is sent and means no window.
type BoardParams struct {
	Project    string
	ClosedDays int
	Limit      int
}

// Values encodes the parameters as query parameters.
func (p BoardParams) Values() url.Values {
	v := url.Values{}
	set(v, "project", p.Project)
	if p.ClosedDays >= 0 {
		v.Set("closed_days", strconv.Itoa(p.ClosedDays))
	}
	Page{Limit: p.Limit}.addTo(v)
	return v
}

// QueueParams selects the priority queue.
type QueueParams struct {
	Project string
	Status  string
	Page
}

// Values encodes the parameters as query parameters.
func (p QueueParams) Values() url.Values {
	v := url.Values{}
	set(v, "project", p.Project)
	set(v, "status", p.Status)
	p.Page.addTo(v)
	return v
}

func ifMatch(version string) http.Header {
	if version == "" {
		return nil
	}
	return http.Header{"If-Match": {version}}
}

// Actors lists the declared actors.
func (c *Client) Actors(ctx context.Context) (*Response, error) {
	return c.Request(ctx, http.MethodGet, "/v1/actors", nil, nil)
}

// ListProjects lists projects.
func (c *Client) ListProjects(ctx context.Context, f ProjectFilter) (*Response, error) {
	return c.Request(ctx, http.MethodGet, withQuery("/v1/projects", f.Values()), nil, nil)
}

// CreateProject creates a project.
func (c *Client) CreateProject(ctx context.Context, key, name string) (*Response, error) {
	return c.Request(ctx, http.MethodPost, "/v1/projects", map[string]any{"key": key, "name": name}, nil)
}

// GetProject fetches one project.
func (c *Client) GetProject(ctx context.Context, key string) (*Response, error) {
	return c.Request(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(key), nil, nil)
}

// PatchProject applies a JSON Merge Patch to a project.
func (c *Client) PatchProject(ctx context.Context, key string, patch map[string]any) (*Response, error) {
	return c.Request(ctx, http.MethodPatch, "/v1/projects/"+url.PathEscape(key), patch, nil)
}

// DeleteProject soft deletes a project.
func (c *Client) DeleteProject(ctx context.Context, key string) (*Response, error) {
	return c.Request(ctx, http.MethodDelete, "/v1/projects/"+url.PathEscape(key), nil, nil)
}

// Overview fetches the project status view. limit 0 means the server
// default.
func (c *Client) Overview(ctx context.Context, key string, limit int) (*Response, error) {
	v := url.Values{}
	Page{Limit: limit}.addTo(v)
	return c.Request(ctx, http.MethodGet, withQuery("/v1/projects/"+url.PathEscape(key)+"/overview", v), nil, nil)
}

// ListIssues lists issues.
func (c *Client) ListIssues(ctx context.Context, f IssueFilter) (*Response, error) {
	return c.Request(ctx, http.MethodGet, withQuery("/v1/issues", f.Values()), nil, nil)
}

// CreateIssue creates an issue from the given fields.
func (c *Client) CreateIssue(ctx context.Context, in map[string]any) (*Response, error) {
	return c.Request(ctx, http.MethodPost, "/v1/issues", in, nil)
}

// GetIssue fetches one issue with its body.
func (c *Client) GetIssue(ctx context.Context, key string) (*Response, error) {
	return c.Request(ctx, http.MethodGet, "/v1/issues/"+url.PathEscape(key), nil, nil)
}

// PatchIssue applies a JSON Merge Patch to an issue. A non-empty version
// is sent as If-Match.
func (c *Client) PatchIssue(ctx context.Context, key string, patch map[string]any, version string) (*Response, error) {
	return c.Request(ctx, http.MethodPatch, "/v1/issues/"+url.PathEscape(key), patch, ifMatch(version))
}

// DeleteIssue soft deletes an issue. A non-empty version is sent as
// If-Match.
func (c *Client) DeleteIssue(ctx context.Context, key, version string) (*Response, error) {
	return c.Request(ctx, http.MethodDelete, "/v1/issues/"+url.PathEscape(key), nil, ifMatch(version))
}

// AddComment adds a comment to an issue.
func (c *Client) AddComment(ctx context.Context, key, body string) (*Response, error) {
	return c.Request(ctx, http.MethodPost, "/v1/issues/"+url.PathEscape(key)+"/comments", map[string]any{"body": body}, nil)
}

// IssueActivity lists one issue's activity, newest first.
func (c *Client) IssueActivity(ctx context.Context, key string, p Page) (*Response, error) {
	v := url.Values{}
	p.addTo(v)
	return c.Request(ctx, http.MethodGet, withQuery("/v1/issues/"+url.PathEscape(key)+"/activity", v), nil, nil)
}

// ListActivity lists activity records across issues.
func (c *Client) ListActivity(ctx context.Context, f ActivityFilter) (*Response, error) {
	return c.Request(ctx, http.MethodGet, withQuery("/v1/activity", f.Values()), nil, nil)
}

// Board fetches the status board.
func (c *Client) Board(ctx context.Context, p BoardParams) (*Response, error) {
	return c.Request(ctx, http.MethodGet, withQuery("/v1/board", p.Values()), nil, nil)
}

// Queue fetches the priority queue.
func (c *Client) Queue(ctx context.Context, p QueueParams) (*Response, error) {
	return c.Request(ctx, http.MethodGet, withQuery("/v1/queue", p.Values()), nil, nil)
}

// Labels lists labels in use. project may be empty or comma separated.
func (c *Client) Labels(ctx context.Context, project string) (*Response, error) {
	v := url.Values{}
	set(v, "project", project)
	return c.Request(ctx, http.MethodGet, withQuery("/v1/labels", v), nil, nil)
}
