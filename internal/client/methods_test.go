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
	if resp.Status != 200 || string(resp.Body) != rec.reply || resp.Header.Get("ETag") != `"7"` {
		t.Errorf("resp = %d %q etag %q", resp.Status, resp.Body, resp.Header.Get("ETag"))
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
		Labels: []string{"api", "cli"}, Parent: "none", Milestone: "2,none", CreatedBy: "claude",
		Query: "설계 api", Sort: "-updated_at", Limit: 20, Offset: 40,
	}
	q := f.Values()
	want := url.Values{
		"project": {"AGB,WEB"}, "status": {"todo,doing"}, "type": {"bug"}, "priority": {"P0,P1"},
		"label": {"api", "cli"}, "parent": {"none"}, "milestone": {"2,none"}, "created_by": {"claude"},
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
		{"delete issue", func() (*Response, error) { return c.DeleteIssue(ctx, "CLI-1", "잘못 만든 이슈", "") }, "DELETE", "/v1/issues/CLI-1", url.Values{"reason": {"잘못 만든 이슈"}}.Encode(), ""},
		{"delete issue without a reason", func() (*Response, error) { return c.DeleteIssue(ctx, "CLI-1", "", `"3"`) }, "DELETE", "/v1/issues/CLI-1", "", ""},
		{"templates", func() (*Response, error) { return c.Templates(ctx) }, "GET", "/v1/templates", "", ""},
		{"list topics", func() (*Response, error) { return c.ListTopics(ctx, "CLI", "all") }, "GET", "/v1/projects/CLI/topics", "status=all", ""},
		{"list active topics", func() (*Response, error) { return c.ListTopics(ctx, "CLI", "") }, "GET", "/v1/projects/CLI/topics", "", ""},
		{"create top-level topic", func() (*Response, error) { return c.CreateTopic(ctx, "CLI", "이슈 흐름", nil) }, "POST", "/v1/projects/CLI/topics", "", `{"name":"이슈 흐름"}`},
		{"create child topic", func() (*Response, error) {
			parent := int64(3)
			return c.CreateTopic(ctx, "CLI", "트리아지", &parent)
		}, "POST", "/v1/projects/CLI/topics", "", `{"name":"트리아지","parent":3}`},
		{"patch topic", func() (*Response, error) {
			return c.PatchTopic(ctx, 7, map[string]any{"parent": nil})
		}, "PATCH", "/v1/topics/7", "", `{"parent":null}`},
		{"delete topic", func() (*Response, error) { return c.DeleteTopic(ctx, 7) }, "DELETE", "/v1/topics/7", "", ""},
		{"list by topic", func() (*Response, error) {
			return c.ListIssues(ctx, IssueFilter{Topic: "3,none"})
		}, "GET", "/v1/issues", "topic=3%2Cnone", ""},
		{"list milestones", func() (*Response, error) { return c.ListMilestones(ctx, "CLI", "all") }, "GET", "/v1/projects/CLI/milestones", "status=all", ""},
		{"list open milestones", func() (*Response, error) { return c.ListMilestones(ctx, "CLI", "") }, "GET", "/v1/projects/CLI/milestones", "", ""},
		{"create milestone", func() (*Response, error) { return c.CreateMilestone(ctx, "CLI", "v0.2.0", "", "") }, "POST", "/v1/projects/CLI/milestones", "", `{"name":"v0.2.0"}`},
		{"create milestone with every field", func() (*Response, error) {
			return c.CreateMilestone(ctx, "CLI", "v0.2.0", "운영 안정화와 릴리즈 관리", "2026-11-01")
		}, "POST", "/v1/projects/CLI/milestones", "", `{"description":"운영 안정화와 릴리즈 관리","name":"v0.2.0","target_date":"2026-11-01"}`},
		{"get milestone", func() (*Response, error) { return c.GetMilestone(ctx, 7) }, "GET", "/v1/milestones/7", "", ""},
		{"patch milestone", func() (*Response, error) {
			return c.PatchMilestone(ctx, 7, map[string]any{"target_date": nil, "status": "closed"})
		}, "PATCH", "/v1/milestones/7", "", `{"status":"closed","target_date":null}`},
		{"delete milestone", func() (*Response, error) { return c.DeleteMilestone(ctx, 7) }, "DELETE", "/v1/milestones/7", "", ""},
		{"list by milestone", func() (*Response, error) {
			return c.ListIssues(ctx, IssueFilter{Milestone: "3,none"})
		}, "GET", "/v1/issues", "milestone=3%2Cnone", ""},
		{"acknowledge", func() (*Response, error) { return c.Acknowledge(ctx, "CLI-1") }, "POST", "/v1/issues/CLI-1/acknowledge", "", ""},
		{"unacknowledge", func() (*Response, error) { return c.Unacknowledge(ctx, "CLI-1") }, "DELETE", "/v1/issues/CLI-1/acknowledge", "", ""},
		{"list unacknowledged", func() (*Response, error) {
			return c.ListIssues(ctx, IssueFilter{Project: "CLI", Acknowledged: "false"})
		}, "GET", "/v1/issues", "acknowledged=false&project=CLI", ""},
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
		{"create device authorization", func() (*Response, error) {
			return c.CreateDeviceAuthorization(ctx, "codex", "DESKTOP-RPLTFHM")
		}, "POST", "/v1/device-authorizations", "", `{"actor":"codex","client_host":"DESKTOP-RPLTFHM"}`},
		{"get device authorization", func() (*Response, error) { return c.GetDeviceAuthorization(ctx, "BDFG-HJKL") }, "GET", "/v1/device-authorizations/BDFG-HJKL", "", ""},
		{"approve device authorization", func() (*Response, error) {
			return c.PatchDeviceAuthorization(ctx, "BDFG-HJKL", "approved")
		}, "PATCH", "/v1/device-authorizations/BDFG-HJKL", "", `{"status":"approved"}`},
		{"deny device authorization", func() (*Response, error) {
			return c.PatchDeviceAuthorization(ctx, "bdfghjkl", "denied")
		}, "PATCH", "/v1/device-authorizations/bdfghjkl", "", `{"status":"denied"}`},
		{"exchange token", func() (*Response, error) { return c.ExchangeToken(ctx, "Yq3-device_code") }, "POST", "/v1/tokens", "", `{"device_code":"Yq3-device_code"}`},
		{"list tokens", func() (*Response, error) { return c.ListTokens(ctx, "") }, "GET", "/v1/tokens", "", ""},
		{"list revoked tokens", func() (*Response, error) { return c.ListTokens(ctx, "revoked") }, "GET", "/v1/tokens", "status=revoked", ""},
		{"revoke token", func() (*Response, error) { return c.RevokeToken(ctx, 7) }, "DELETE", "/v1/tokens/7", "", ""},
		{"revoke current token", func() (*Response, error) { return c.RevokeCurrentToken(ctx) }, "DELETE", "/v1/tokens/current", "", ""},
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
			if tc.name == "delete issue without a reason" && rec.header.Get("If-Match") != `"3"` {
				t.Errorf("If-Match = %q", rec.header.Get("If-Match"))
			}
		})
	}
}

// The two calls that start a login are made before the client has a token, so
// they carry neither the token nor the trace headers. Every other call carries
// all of them.
func TestLoginCallsSendNoCredentials(t *testing.T) {
	rec := newRecorder(t)
	c := rec.client()
	ctx := context.Background()
	credentials := []string{"Authorization", "X-Actor", "X-Session-Id", "X-Client-Host"}
	for name, call := range map[string]func() (*Response, error){
		"create device authorization": func() (*Response, error) { return c.CreateDeviceAuthorization(ctx, "codex", "desk") },
		"exchange token":              func() (*Response, error) { return c.ExchangeToken(ctx, "code") },
	} {
		rec.header = nil
		if _, err := call(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, h := range credentials {
			if v := rec.header.Get(h); v != "" {
				t.Errorf("%s sent %s: %q", name, h, v)
			}
		}
		if rec.header.Get("Content-Type") != "application/json; charset=utf-8" || rec.header.Get("User-Agent") != "agb/t" || rec.header.Get("Accept") != "application/json" {
			t.Errorf("%s: headers = %v", name, rec.header)
		}
	}
	for name, call := range map[string]func() (*Response, error){
		"get device authorization":   func() (*Response, error) { return c.GetDeviceAuthorization(ctx, "BDFG-HJKL") },
		"patch device authorization": func() (*Response, error) { return c.PatchDeviceAuthorization(ctx, "BDFG-HJKL", "approved") },
		"list tokens":                func() (*Response, error) { return c.ListTokens(ctx, "all") },
		"revoke token":               func() (*Response, error) { return c.RevokeToken(ctx, 7) },
		"revoke current token":       func() (*Response, error) { return c.RevokeCurrentToken(ctx) },
	} {
		rec.header = nil
		if _, err := call(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, h := range credentials {
			if rec.header.Get(h) == "" {
				t.Errorf("%s sent no %s", name, h)
			}
		}
		if rec.header.Get("Authorization") != "Bearer "+testToken {
			t.Errorf("%s: Authorization = %q", name, rec.header.Get("Authorization"))
		}
	}
}

func TestLoginCallsKeepTheServerError(t *testing.T) {
	rec := newRecorder(t)
	rec.status = 400
	rec.reply = `{"error":{"code":"authorization_pending","message":"아직 승인되지 않았습니다."}}`
	_, err := rec.client().ExchangeToken(context.Background(), "code")
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != 400 || apiErr.Code != "authorization_pending" || string(apiErr.Body) != rec.reply {
		t.Errorf("err = %v (%+v)", err, apiErr)
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
