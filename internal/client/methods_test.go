package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// recorder is an httptest server that remembers the last request and
// answers with a fixed status, headers and body.
type recorder struct {
	srv    *httptest.Server
	method string
	path   string
	query  url.Values
	header http.Header
	body   []byte
	status int
	reply  string
	etag   string
}

func newRecorder(t *testing.T) *recorder {
	t.Helper()
	r := &recorder{status: 200, reply: `{"ok":true}`}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.method, r.path, r.query, r.header = req.Method, req.URL.Path, req.URL.Query(), req.Header.Clone()
		r.body, _ = io.ReadAll(req.Body)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.etag != "" {
			w.Header().Set("ETag", r.etag)
		}
		w.WriteHeader(r.status)
		w.Write([]byte(r.reply))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recorder) client() *Client { return newClient(r.srv.URL) }

func TestRequestReturnsRawBodyAndHeaders(t *testing.T) {
	rec := newRecorder(t)
	rec.reply = `{"key":"AGB-1","version":7}`
	rec.etag = `"7"`
	resp, err := rec.client().Request(context.Background(), http.MethodGet, "/v1/issues/AGB-1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 || string(resp.Body) != rec.reply || resp.ETag() != `"7"` {
		t.Errorf("resp = %d %q etag %q", resp.Status, resp.Body, resp.ETag())
	}
	var out struct {
		Key string `json:"key"`
	}
	if err := resp.Decode(&out); err != nil || out.Key != "AGB-1" {
		t.Errorf("decode = %+v %v", out, err)
	}
	if rec.header.Get("Authorization") != "Bearer "+testToken || rec.header.Get("X-Actor") != "owner" {
		t.Errorf("headers = %v", rec.header)
	}
}

func TestRequestSendsExtraHeaders(t *testing.T) {
	rec := newRecorder(t)
	_, err := rec.client().Request(context.Background(), http.MethodPatch, "/v1/issues/AGB-1", map[string]any{"title": "x"}, http.Header{"If-Match": {`"3"`}})
	if err != nil {
		t.Fatal(err)
	}
	if rec.header.Get("If-Match") != `"3"` || rec.method != http.MethodPatch {
		t.Errorf("If-Match = %q method %s", rec.header.Get("If-Match"), rec.method)
	}
	if strings.TrimSpace(string(rec.body)) != `{"title":"x"}` {
		t.Errorf("body = %q", rec.body)
	}
}

func TestRequestErrorKeepsServerBody(t *testing.T) {
	rec := newRecorder(t)
	rec.status = 412
	rec.reply = `{"error":{"code":"precondition_failed","message":"버전이 다릅니다."}}`
	_, err := rec.client().Request(context.Background(), http.MethodPatch, "/v1/issues/AGB-1", map[string]any{}, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	if apiErr.Status != 412 || apiErr.Code != "precondition_failed" || string(apiErr.Body) != rec.reply {
		t.Errorf("apiErr = %+v body %q", apiErr, apiErr.Body)
	}
}

func TestIssueFilterQuery(t *testing.T) {
	f := IssueFilter{
		Project: "AGB,WEB", Status: "todo,doing", Type: "bug", Priority: "P0,P1",
		Labels: []string{"api", "cli"}, Parent: "none", CreatedBy: "claude",
		Query: "설계 api", Sort: "-updated_at", Limit: 20, Offset: 40,
	}
	q := f.Values()
	want := url.Values{
		"project": {"AGB,WEB"}, "status": {"todo,doing"}, "type": {"bug"}, "priority": {"P0,P1"},
		"label": {"api", "cli"}, "parent": {"none"}, "created_by": {"claude"},
		"q": {"설계 api"}, "sort": {"-updated_at"}, "limit": {"20"}, "offset": {"40"},
	}
	if q.Encode() != want.Encode() {
		t.Errorf("values = %v, want %v", q, want)
	}
	if (IssueFilter{}).Values().Encode() != "" {
		t.Errorf("empty filter must produce no parameters: %q", (IssueFilter{}).Values().Encode())
	}
}

func TestTypedMethodsBuildPaths(t *testing.T) {
	rec := newRecorder(t)
	c := rec.client()
	ctx := context.Background()
	cases := []struct {
		name   string
		call   func() (*Response, error)
		method string
		path   string
		query  string
		body   string
	}{
		{"actors", func() (*Response, error) { return c.Actors(ctx) }, "GET", "/v1/actors", "", ""},
		{"list projects", func() (*Response, error) {
			return c.ListProjects(ctx, ProjectFilter{Status: "all", Sort: "key", Page: Page{Limit: 5}})
		}, "GET", "/v1/projects", "limit=5&sort=key&status=all", ""},
		{"create project", func() (*Response, error) { return c.CreateProject(ctx, "CLI", "cli 검증") }, "POST", "/v1/projects", "", `{"key":"CLI","name":"cli 검증"}`},
		{"get project", func() (*Response, error) { return c.GetProject(ctx, "CLI") }, "GET", "/v1/projects/CLI", "", ""},
		{"patch project", func() (*Response, error) {
			return c.PatchProject(ctx, "CLI", map[string]any{"status": "archived"})
		}, "PATCH", "/v1/projects/CLI", "", `{"status":"archived"}`},
		{"delete project", func() (*Response, error) { return c.DeleteProject(ctx, "CLI") }, "DELETE", "/v1/projects/CLI", "", ""},
		{"overview", func() (*Response, error) { return c.Overview(ctx, "CLI", 3) }, "GET", "/v1/projects/CLI/overview", "limit=3", ""},
		{"list issues", func() (*Response, error) {
			return c.ListIssues(ctx, IssueFilter{Project: "CLI", Labels: []string{"a", "b"}})
		}, "GET", "/v1/issues", "label=a&label=b&project=CLI", ""},
		{"create issue", func() (*Response, error) {
			return c.CreateIssue(ctx, map[string]any{"project": "CLI", "title": "t"})
		}, "POST", "/v1/issues", "", `{"project":"CLI","title":"t"}`},
		{"get issue", func() (*Response, error) { return c.GetIssue(ctx, "CLI-1") }, "GET", "/v1/issues/CLI-1", "", ""},
		{"patch issue", func() (*Response, error) {
			return c.PatchIssue(ctx, "CLI-1", map[string]any{"status": "doing"}, `"2"`)
		}, "PATCH", "/v1/issues/CLI-1", "", `{"status":"doing"}`},
		{"delete issue", func() (*Response, error) { return c.DeleteIssue(ctx, "CLI-1", "") }, "DELETE", "/v1/issues/CLI-1", "", ""},
		{"comment", func() (*Response, error) { return c.AddComment(ctx, "CLI-1", "댓글", "") }, "POST", "/v1/issues/CLI-1/comments", "", `{"body":"댓글"}`},
		{"comment with summary", func() (*Response, error) { return c.AddComment(ctx, "CLI-1", "댓글", "서버 랭킹 함수 구현") }, "POST", "/v1/issues/CLI-1/comments", "", `{"body":"댓글","summary":"서버 랭킹 함수 구현"}`},
		{"issue activity", func() (*Response, error) { return c.IssueActivity(ctx, "CLI-1", Page{Limit: 10, Offset: 2}) }, "GET", "/v1/issues/CLI-1/activity", "limit=10&offset=2", ""},
		{"activity", func() (*Response, error) {
			return c.ListActivity(ctx, ActivityFilter{Project: "CLI", Kind: "updated", Field: "status", Since: "2026-10-17T00:00:00Z", Sort: "at"})
		}, "GET", "/v1/activity", "field=status&kind=updated&project=CLI&since=2026-10-17T00%3A00%3A00Z&sort=at", ""},
		{"board", func() (*Response, error) { return c.Board(ctx, BoardParams{Project: "CLI", ClosedDays: 0, Limit: 5}) }, "GET", "/v1/board", "closed_days=0&limit=5&project=CLI", ""},
		{"board default", func() (*Response, error) { return c.Board(ctx, BoardParams{ClosedDays: -1}) }, "GET", "/v1/board", "", ""},
		{"queue", func() (*Response, error) {
			return c.Queue(ctx, QueueParams{Project: "CLI", Status: "todo,backlog", Page: Page{Limit: 1}})
		}, "GET", "/v1/queue", "limit=1&project=CLI&status=todo%2Cbacklog", ""},
		{"labels", func() (*Response, error) { return c.Labels(ctx, "CLI") }, "GET", "/v1/labels", "project=CLI", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec.header, rec.body = nil, nil
			if _, err := tc.call(); err != nil {
				t.Fatal(err)
			}
			if rec.method != tc.method || rec.path != tc.path || rec.query.Encode() != tc.query {
				t.Errorf("request = %s %s?%s, want %s %s?%s", rec.method, rec.path, rec.query.Encode(), tc.method, tc.path, tc.query)
			}
			if got := strings.TrimSpace(string(rec.body)); got != tc.body {
				t.Errorf("body = %q, want %q", got, tc.body)
			}
			if tc.name == "patch issue" && rec.header.Get("If-Match") != `"2"` {
				t.Errorf("If-Match = %q", rec.header.Get("If-Match"))
			}
			if tc.name == "delete issue" && rec.header.Get("If-Match") != "" {
				t.Errorf("If-Match sent without a version: %q", rec.header.Get("If-Match"))
			}
		})
	}
}

func TestCreateIssueBodyKeepsUnicode(t *testing.T) {
	rec := newRecorder(t)
	_, err := rec.client().CreateIssue(context.Background(), map[string]any{"project": "CLI", "title": "한국어 <제목> & 기호"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.body, &got); err != nil || got["title"] != "한국어 <제목> & 기호" {
		t.Errorf("body = %s (%v)", rec.body, err)
	}
	if !strings.Contains(string(rec.body), "<") || strings.Contains(string(rec.body), "\\u003c") {
		t.Errorf("HTML escaping must be off: %s", rec.body)
	}
}
