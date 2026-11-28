package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// ContextQuery runs the single-call context retrieval. req is the JSON body of
// POST /v1/context/query.
func (c *Client) ContextQuery(ctx context.Context, req map[string]any) (*Response, error) {
	return c.Request(ctx, http.MethodPost, "/v1/context/query", req, nil)
}

// Ready lists the issues of a project that can be picked up now. The empty and
// zero filter values are left to the server's defaults.
func (c *Client) Ready(ctx context.Context, project, epic, label string, milestoneID int64, includeClaimed bool, limit, offset int) (*Response, error) {
	v := url.Values{}
	set(v, "epic", epic)
	set(v, "label", label)
	if milestoneID > 0 {
		v.Set("milestone_id", strconv.FormatInt(milestoneID, 10))
	}
	if includeClaimed {
		v.Set("include_claimed", "true")
	}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		v.Set("offset", strconv.Itoa(offset))
	}
	return c.Request(ctx, http.MethodGet, withQuery("/v1/projects/"+url.PathEscape(project)+"/ready", v), nil, nil)
}

// EdgeCandidates lists the proposed graph edges that wait for a decision.
func (c *Client) EdgeCandidates(ctx context.Context, project, kind string, limit, offset int) (*Response, error) {
	v := url.Values{}
	set(v, "project", project)
	set(v, "kind", kind)
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		v.Set("offset", strconv.Itoa(offset))
	}
	return c.Request(ctx, http.MethodGet, withQuery("/v1/edges/candidates", v), nil, nil)
}

// DecideEdge confirms or rejects a candidate edge. action is confirm or reject.
func (c *Client) DecideEdge(ctx context.Context, id int64, action string) (*Response, error) {
	return c.Request(ctx, http.MethodPost, "/v1/edges/"+strconv.FormatInt(id, 10)+"/"+action, nil, nil)
}

// RemoveEdge marks an edge removed.
func (c *Client) RemoveEdge(ctx context.Context, id int64) (*Response, error) {
	return c.Request(ctx, http.MethodDelete, "/v1/edges/"+strconv.FormatInt(id, 10), nil, nil)
}

func waitQuery(wait bool) url.Values {
	v := url.Values{}
	if wait {
		v.Set("wait", "true")
	}
	return v
}

// UpsertSpec creates or revises a mutable spec document. With wait the call
// returns after the server has indexed the revision (or timed out waiting).
func (c *Client) UpsertSpec(ctx context.Context, project, slug string, body map[string]any, wait bool) (*Response, error) {
	p := "/v1/projects/" + url.PathEscape(project) + "/specs/" + url.PathEscape(slug)
	return c.Request(ctx, http.MethodPut, withQuery(p, waitQuery(wait)), body, nil)
}

// GetSpec fetches a spec document.
func (c *Client) GetSpec(ctx context.Context, project, slug string) (*Response, error) {
	return c.Request(ctx, http.MethodGet, "/v1/projects/"+url.PathEscape(project)+"/specs/"+url.PathEscape(slug), nil, nil)
}

// CreateRecord appends an immutable record document.
func (c *Client) CreateRecord(ctx context.Context, project string, body map[string]any, wait bool) (*Response, error) {
	p := "/v1/projects/" + url.PathEscape(project) + "/records"
	return c.Request(ctx, http.MethodPost, withQuery(p, waitQuery(wait)), body, nil)
}

// ListEpics lists the epics of a project; status is open, closed, deleted, a
// comma list of them, or all.
func (c *Client) ListEpics(ctx context.Context, project, status string) (*Response, error) {
	v := url.Values{}
	set(v, "status", status)
	return c.Request(ctx, http.MethodGet, withQuery("/v1/projects/"+url.PathEscape(project)+"/epics", v), nil, nil)
}

// GetEpic fetches one epic by key.
func (c *Client) GetEpic(ctx context.Context, key string) (*Response, error) {
	return c.Request(ctx, http.MethodGet, "/v1/epics/"+url.PathEscape(key), nil, nil)
}

// CreateEpic adds an epic to a project.
func (c *Client) CreateEpic(ctx context.Context, project string, body map[string]any) (*Response, error) {
	return c.Request(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/epics", body, nil)
}

// PatchEpic changes the title, description, or status of an epic.
func (c *Client) PatchEpic(ctx context.Context, key string, patch map[string]any) (*Response, error) {
	return c.Request(ctx, http.MethodPatch, "/v1/epics/"+url.PathEscape(key), patch, nil)
}
