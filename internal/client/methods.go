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
	Topic           string // topic ids and none, comma separated (OR)
	Milestone       string // milestone ids and none, comma separated (OR)
	Acknowledged    string // "true" or "false"; empty sends no filter
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
	set(v, "topic", f.Topic)
	set(v, "milestone", f.Milestone)
	set(v, "acknowledged", f.Acknowledged)
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

// DeleteIssue soft deletes an issue. The reason is sent as the reason query
// parameter, which the server requires. A non-empty version is sent as
// If-Match.
func (c *Client) DeleteIssue(ctx context.Context, key, reason, version string) (*Response, error) {
	v := url.Values{}
	set(v, "reason", reason)
	return c.Request(ctx, http.MethodDelete, withQuery("/v1/issues/"+url.PathEscape(key), v), nil, ifMatch(version))
}

// Templates lists the body templates of the issue types.
func (c *Client) Templates(ctx context.Context) (*Response, error) {
	return c.Request(ctx, http.MethodGet, "/v1/templates", nil, nil)
}

// ListTopics lists the topics of a project. status is active (the server
// default when empty), deleted or all.
func (c *Client) ListTopics(ctx context.Context, project, status string) (*Response, error) {
	v := url.Values{}
	set(v, "status", status)
	return c.Request(ctx, http.MethodGet, withQuery("/v1/projects/"+url.PathEscape(project)+"/topics", v), nil, nil)
}

// CreateTopic adds a topic to a project. A nil parent makes a top-level topic.
func (c *Client) CreateTopic(ctx context.Context, project, name string, parent *int64) (*Response, error) {
	in := map[string]any{"name": name}
	if parent != nil {
		in["parent"] = *parent
	}
	return c.Request(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/topics", in, nil)
}

// PatchTopic applies a JSON Merge Patch to a topic.
func (c *Client) PatchTopic(ctx context.Context, id int64, patch map[string]any) (*Response, error) {
	return c.Request(ctx, http.MethodPatch, "/v1/topics/"+strconv.FormatInt(id, 10), patch, nil)
}

// DeleteTopic soft deletes a topic.
func (c *Client) DeleteTopic(ctx context.Context, id int64) (*Response, error) {
	return c.Request(ctx, http.MethodDelete, "/v1/topics/"+strconv.FormatInt(id, 10), nil, nil)
}

// ListMilestones lists the milestones of a project. status is open (the
// server default when empty), closed, deleted or all, or a comma separated
// list of them (OR).
func (c *Client) ListMilestones(ctx context.Context, project, status string) (*Response, error) {
	v := url.Values{}
	set(v, "status", status)
	return c.Request(ctx, http.MethodGet, withQuery("/v1/projects/"+url.PathEscape(project)+"/milestones", v), nil, nil)
}

// CreateMilestone adds an open milestone to a project. An empty description
// or target date (YYYY-MM-DD) is left out of the request.
func (c *Client) CreateMilestone(ctx context.Context, project, name, description, targetDate string) (*Response, error) {
	in := map[string]any{"name": name}
	if description != "" {
		in["description"] = description
	}
	if targetDate != "" {
		in["target_date"] = targetDate
	}
	return c.Request(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/milestones", in, nil)
}

// GetMilestone fetches one milestone with its progress.
func (c *Client) GetMilestone(ctx context.Context, id int64) (*Response, error) {
	return c.Request(ctx, http.MethodGet, "/v1/milestones/"+strconv.FormatInt(id, 10), nil, nil)
}

// PatchMilestone applies a JSON Merge Patch to a milestone: name,
// description, target_date (null clears it) and status.
func (c *Client) PatchMilestone(ctx context.Context, id int64, patch map[string]any) (*Response, error) {
	return c.Request(ctx, http.MethodPatch, "/v1/milestones/"+strconv.FormatInt(id, 10), patch, nil)
}

// DeleteMilestone soft deletes a milestone.
func (c *Client) DeleteMilestone(ctx context.Context, id int64) (*Response, error) {
	return c.Request(ctx, http.MethodDelete, "/v1/milestones/"+strconv.FormatInt(id, 10), nil, nil)
}

// CreateDeviceAuthorization asks the server to let an agent actor in on a
// device. It is the first step of a login, made without a token, so it sends
// neither the token nor the trace headers. The response holds the device code
// and the user code.
func (c *Client) CreateDeviceAuthorization(ctx context.Context, actor, clientHost string) (*Response, error) {
	return c.send(ctx, http.MethodPost, "/v1/device-authorizations", map[string]any{"actor": actor, "client_host": clientHost}, nil, false)
}

// GetDeviceAuthorization fetches the device authorization with the user code,
// typed in any case and with or without the hyphen. Only a user can.
func (c *Client) GetDeviceAuthorization(ctx context.Context, userCode string) (*Response, error) {
	return c.Request(ctx, http.MethodGet, "/v1/device-authorizations/"+url.PathEscape(userCode), nil, nil)
}

// PatchDeviceAuthorization approves or denies a device authorization: status
// is approved or denied. Only a user can.
func (c *Client) PatchDeviceAuthorization(ctx context.Context, userCode, status string) (*Response, error) {
	return c.Request(ctx, http.MethodPatch, "/v1/device-authorizations/"+url.PathEscape(userCode), map[string]any{"status": status}, nil)
}

// ExchangeToken trades the device code of an approved device authorization
// for an actor token. It is the last step of a login, made without a token, so
// it sends neither the token nor the trace headers. Until the user decides, the
// server answers 400 authorization_pending and the call is repeated.
func (c *Client) ExchangeToken(ctx context.Context, deviceCode string) (*Response, error) {
	return c.send(ctx, http.MethodPost, "/v1/tokens", map[string]any{"device_code": deviceCode}, nil, false)
}

// ListTokens lists actor tokens. status is active (the server default when
// empty), revoked or all. Only a user can.
func (c *Client) ListTokens(ctx context.Context, status string) (*Response, error) {
	v := url.Values{}
	set(v, "status", status)
	return c.Request(ctx, http.MethodGet, withQuery("/v1/tokens", v), nil, nil)
}

// RevokeToken revokes an actor token by id. Only a user can.
func (c *Client) RevokeToken(ctx context.Context, id int64) (*Response, error) {
	return c.Request(ctx, http.MethodDelete, "/v1/tokens/"+strconv.FormatInt(id, 10), nil, nil)
}

// RevokeCurrentToken revokes the actor token the request carries. The shared
// token has none, so the call is refused for it.
func (c *Client) RevokeCurrentToken(ctx context.Context) (*Response, error) {
	return c.Request(ctx, http.MethodDelete, "/v1/tokens/current", nil, nil)
}

// Acknowledge marks an issue as acknowledged by the actor of the request.
func (c *Client) Acknowledge(ctx context.Context, key string) (*Response, error) {
	return c.Request(ctx, http.MethodPost, "/v1/issues/"+url.PathEscape(key)+"/acknowledge", nil, nil)
}

// Unacknowledge clears the acknowledgement of an issue.
func (c *Client) Unacknowledge(ctx context.Context, key string) (*Response, error) {
	return c.Request(ctx, http.MethodDelete, "/v1/issues/"+url.PathEscape(key)+"/acknowledge", nil, nil)
}

// AddComment adds a comment to an issue. A non-empty summary is sent as the
// comment's one-line summary.
func (c *Client) AddComment(ctx context.Context, key, body, summary string) (*Response, error) {
	in := map[string]any{"body": body}
	if summary != "" {
		in["summary"] = summary
	}
	return c.Request(ctx, http.MethodPost, "/v1/issues/"+url.PathEscape(key)+"/comments", in, nil)
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
