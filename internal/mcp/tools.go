package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wsk0715/agent-board/internal/client"
)

var errBadArgument = errors.New("invalid argument")

type tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Run         func(ctx context.Context, args map[string]any) ([]byte, error)
}

func obj(required []string, props map[string]any) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func prop(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

func strArg(a map[string]any, name string) (string, error) {
	v, ok := a[name]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%w: %s must be a string", errBadArgument, name)
	}
	return s, nil
}

func reqStr(a map[string]any, name string) (string, error) {
	s, err := strArg(a, name)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("%w: %s is required", errBadArgument, name)
	}
	return s, nil
}

func intArg(a map[string]any, name string) (int64, error) {
	v, ok := a[name]
	if !ok || v == nil {
		return 0, nil
	}
	f, ok := v.(float64)
	if !ok || f != float64(int64(f)) {
		return 0, fmt.Errorf("%w: %s must be an integer", errBadArgument, name)
	}
	return int64(f), nil
}

func boolArg(a map[string]any, name string) (bool, error) {
	v, ok := a[name]
	if !ok || v == nil {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%w: %s must be a boolean", errBadArgument, name)
	}
	return b, nil
}

// passthrough copies the named keys of a into a request body unchanged, so the
// server's own validation reports any bad value.
func passthrough(a map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := a[k]; ok && v != nil {
			out[k] = v
		}
	}
	return out
}

func body(r *client.Response, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	return r.Body, nil
}

func buildTools(c *client.Client) []tool {
	return []tool{
		{
			Name: "agb_context",
			Description: "Answer a question from the project record in ONE call: hybrid keyword + semantic search, rerank, and a context packet " +
				"with the top items' content and their confirmed graph neighbours (both directions). When the top result is clearly ahead " +
				"(score gap >= cutoff) only that one is returned. Prefer this over reading documents one by one.",
			Schema: obj([]string{"query"}, map[string]any{
				"query":              prop("string", "Natural-language question or keywords"),
				"project":            prop("string", "Project key; omit to search all projects"),
				"include_global":     prop("boolean", "Include global project documents (default true)"),
				"source_types":       map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"issue", "activity", "spec", "record"}}},
				"max_results":        prop("integer", "1-10, default 3"),
				"confidence_cutoff":  prop("number", "0-1 score-gap threshold for the single-result cutoff"),
				"expand_hops":        prop("integer", "0-2; 1 or more also returns neighbour content"),
				"include_archived":   prop("boolean", "Search archived documents too"),
				"include_superseded": prop("boolean", "Search superseded documents and report their latest replacement"),
			}),
			Run: func(ctx context.Context, a map[string]any) ([]byte, error) {
				if _, err := reqStr(a, "query"); err != nil {
					return nil, err
				}
				return body(c.ContextQuery(ctx, passthrough(a, "query", "project", "include_global", "source_types", "max_results",
					"confidence_cutoff", "expand_hops", "include_archived", "include_superseded")))
			},
		},
		{
			Name:        "agb_ready",
			Description: "List the issues of a project that can be started now: every dependency is closed and nobody has claimed it. Ordered by priority.",
			Schema: obj([]string{"project"}, map[string]any{
				"project":         prop("string", "Project key"),
				"epic":            prop("string", "Epic key filter"),
				"label":           prop("string", "Label filter"),
				"milestone_id":    prop("integer", "Milestone id filter"),
				"include_claimed": prop("boolean", "Include issues claimed by another session"),
				"limit":           prop("integer", "1-100, default 20"),
			}),
			Run: func(ctx context.Context, a map[string]any) ([]byte, error) {
				project, err := reqStr(a, "project")
				if err != nil {
					return nil, err
				}
				epic, err := strArg(a, "epic")
				if err != nil {
					return nil, err
				}
				label, err := strArg(a, "label")
				if err != nil {
					return nil, err
				}
				ms, err := intArg(a, "milestone_id")
				if err != nil {
					return nil, err
				}
				inc, err := boolArg(a, "include_claimed")
				if err != nil {
					return nil, err
				}
				limit, err := intArg(a, "limit")
				if err != nil {
					return nil, err
				}
				return body(c.Ready(ctx, project, epic, label, ms, inc, int(limit), 0))
			},
		},
		{
			Name:        "agb_edge_candidates",
			Description: "List proposed graph edges (supersedes, about, depends_on, related) that wait for a confirm or reject decision.",
			Schema: obj(nil, map[string]any{
				"project": prop("string", "Project key filter"),
				"kind":    prop("string", "supersedes, about, depends_on or related"),
				"limit":   prop("integer", "1-200, default 50"),
			}),
			Run: func(ctx context.Context, a map[string]any) ([]byte, error) {
				project, err := strArg(a, "project")
				if err != nil {
					return nil, err
				}
				kind, err := strArg(a, "kind")
				if err != nil {
					return nil, err
				}
				limit, err := intArg(a, "limit")
				if err != nil {
					return nil, err
				}
				return body(c.EdgeCandidates(ctx, project, kind, int(limit), 0))
			},
		},
		{
			Name:        "agb_edge_decide",
			Description: "Confirm or reject a candidate edge. Only confirmed edges appear as neighbours and supersedes links in agb_context.",
			Schema: obj([]string{"id", "action"}, map[string]any{
				"id":     prop("integer", "Edge id from agb_edge_candidates"),
				"action": map[string]any{"type": "string", "enum": []string{"confirm", "reject"}},
			}),
			Run: func(ctx context.Context, a map[string]any) ([]byte, error) {
				id, err := intArg(a, "id")
				if err != nil {
					return nil, err
				}
				if id < 1 {
					return nil, fmt.Errorf("%w: id must be a positive integer", errBadArgument)
				}
				action, err := reqStr(a, "action")
				if err != nil {
					return nil, err
				}
				if action != "confirm" && action != "reject" {
					return nil, fmt.Errorf("%w: action must be confirm or reject", errBadArgument)
				}
				return body(c.DecideEdge(ctx, id, action))
			},
		},
		{
			Name: "agb_spec_put",
			Description: "Create or revise a mutable spec document (a new revision is kept). With wait=true the call returns after indexing, " +
				"with ingest_status and any supersedes candidates.",
			Schema: obj([]string{"project", "slug", "content"}, map[string]any{
				"project":        prop("string", "Project key"),
				"slug":           prop("string", "Spec slug"),
				"content":        prop("string", "UTF-8 markdown body, at most 2 MiB"),
				"title":          prop("string", "Title"),
				"kind":           prop("string", "Kind"),
				"tags":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"change_summary": prop("string", "Revision summary"),
				"wait":           prop("boolean", "Wait for indexing (up to 15 s)"),
			}),
			Run: func(ctx context.Context, a map[string]any) ([]byte, error) {
				project, err := reqStr(a, "project")
				if err != nil {
					return nil, err
				}
				slug, err := reqStr(a, "slug")
				if err != nil {
					return nil, err
				}
				if _, ok := a["content"].(string); !ok {
					return nil, fmt.Errorf("%w: content is required", errBadArgument)
				}
				wait, err := boolArg(a, "wait")
				if err != nil {
					return nil, err
				}
				return body(c.UpsertSpec(ctx, project, slug, passthrough(a, "content", "title", "kind", "tags", "change_summary"), wait))
			},
		},
		{
			Name:        "agb_record_add",
			Description: "Append an immutable record document (decision, finding, incident, ...). With wait=true the call returns after indexing.",
			Schema: obj([]string{"project", "name", "kind", "content"}, map[string]any{
				"project": prop("string", "Project key"),
				"name":    prop("string", "Unique record name"),
				"kind":    prop("string", "Kind"),
				"title":   prop("string", "Title"),
				"tags":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"content": prop("string", "UTF-8 markdown body, at most 2 MiB"),
				"wait":    prop("boolean", "Wait for indexing (up to 15 s)"),
			}),
			Run: func(ctx context.Context, a map[string]any) ([]byte, error) {
				project, err := reqStr(a, "project")
				if err != nil {
					return nil, err
				}
				if _, err := reqStr(a, "name"); err != nil {
					return nil, err
				}
				if _, err := reqStr(a, "kind"); err != nil {
					return nil, err
				}
				if _, ok := a["content"].(string); !ok {
					return nil, fmt.Errorf("%w: content is required", errBadArgument)
				}
				wait, err := boolArg(a, "wait")
				if err != nil {
					return nil, err
				}
				return body(c.CreateRecord(ctx, project, passthrough(a, "name", "kind", "title", "tags", "content"), wait))
			},
		},
	}
}
