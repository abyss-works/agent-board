package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/abyss-works/agent-board/internal/api"
	"github.com/abyss-works/agent-board/internal/model"
	"github.com/abyss-works/agent-board/internal/store"
)

func TestDocumentMethodsBuildRequests(t *testing.T) {
	rec := newRecorder(t)
	c := rec.client()
	ctx := context.Background()
	const name = "2026-10-20-1544-decision-priority-model.md"
	cases := []struct {
		label  string
		call   func() (*Response, error)
		method string
		path   string
		query  string
		body   string
	}{
		{"put", func() (*Response, error) {
			return c.PutDocumentWith(ctx, "AGB", name, DocumentPut{Content: "---\nkind: decision\n---\n본문\n"})
		},
			"PUT", "/v1/projects/AGB/documents/" + name, "", `{"content":"---\nkind: decision\n---\n본문\n"}`},
		{"list", func() (*Response, error) { return c.ListDocuments(ctx, "AGB", Page{}) }, "GET", "/v1/projects/AGB/documents", "", ""},
		{"list a page", func() (*Response, error) { return c.ListDocuments(ctx, "AGB", Page{Limit: 500, Offset: 500}) },
			"GET", "/v1/projects/AGB/documents", "limit=500&offset=500", ""},
		{"get", func() (*Response, error) { return c.GetDocument(ctx, "AGB", name) }, "GET", "/v1/projects/AGB/documents/" + name, "", ""},
		{"search", func() (*Response, error) { return c.SearchDocuments(ctx, DocumentFilter{}) }, "GET", "/v1/documents", "", ""},
		{"search with every filter", func() (*Response, error) {
			return c.SearchDocuments(ctx, DocumentFilter{Query: "우선순위 큐", Project: "AGB,XYZ", Kind: "decision,report", State: "current,archived", Imported: "false", Page: Page{Limit: 10, Offset: 20}})
		}, "GET", "/v1/documents", "imported=false&kind=decision%2Creport&limit=10&offset=20&project=AGB%2CXYZ&q=%EC%9A%B0%EC%84%A0%EC%88%9C%EC%9C%84+%ED%81%90&state=current%2Carchived", ""},
		{"unified search with status and state", func() (*Response, error) {
			return c.Search(ctx, SearchFilter{Query: "잠금", Status: "open", State: "all"})
		}, "GET", "/v1/search", "q=%EC%9E%A0%EA%B8%88&state=all&status=open", ""},
		{"graph", func() (*Response, error) { return c.Graph(ctx, "issue:AGB-7", 2, "") }, "GET", "/v1/graph", "depth=2&node=issue%3AAGB-7", ""},
		{"graph with every state", func() (*Response, error) { return c.Graph(ctx, "issue:AGB-7", 0, "all") }, "GET", "/v1/graph", "node=issue%3AAGB-7&state=all", ""},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			rec.header, rec.body = nil, nil
			if _, err := tc.call(); err != nil {
				t.Fatal(err)
			}
			if rec.method != tc.method || rec.path != tc.path || rec.query.Encode() != tc.query {
				t.Errorf("request = %s %s?%s, want %s %s?%s", rec.method, rec.path, rec.query.Encode(), tc.method, tc.path, tc.query)
			}
			// The body is JSON in the shape of the endpoint; the encoder may spell < > & as it likes.
			if tc.body != "" {
				var got, want map[string]string
				if err := json.Unmarshal(rec.body, &got); err != nil {
					t.Fatalf("body %q: %v", rec.body, err)
				}
				json.Unmarshal([]byte(tc.body), &want)
				if len(got) != len(want) || got["content"] != want["content"] {
					t.Errorf("body = %q, want %q", rec.body, tc.body)
				}
			} else if len(rec.body) != 0 {
				t.Errorf("body = %q, want none", rec.body)
			}
		})
	}

	// The content goes out as it is: markup characters are not escaped and a
	// line break is the JSON escape of the byte, nothing else.
	rec.body = nil
	if _, err := c.PutDocumentWith(ctx, "AGB", name, DocumentPut{Content: "<a> & é\r\n"}); err != nil {
		t.Fatal(err)
	}
	htmlEscape := func(code string) string { return `\` + "u" + code }
	if strings.Contains(string(rec.body), htmlEscape("003c")) || strings.Contains(string(rec.body), htmlEscape("0026")) || !strings.Contains(string(rec.body), `"<a> & é\r\n"`) {
		t.Errorf("body = %s", rec.body)
	}
	// A name is one path segment.
	if _, err := c.GetDocument(ctx, "AGB", "a b/c.md"); err != nil || rec.path != "/v1/projects/AGB/documents/a b/c.md" {
		t.Errorf("path = %q, %v", rec.path, err)
	}
}

func TestDocumentsAgainstRealHandler(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "agb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	actors := []model.Actor{{ID: "owner", Kind: model.ActorUser}}
	ctx := context.Background()
	if err := st.EnsureActors(ctx, actors); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.New(api.Options{Token: testToken, Actors: actors, Version: "t", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: st}))
	defer srv.Close()
	c := newClient(srv.URL)
	if _, err := c.CreateProject(ctx, "AGB", "agent-board"); err != nil {
		t.Fatal(err)
	}

	const name = "2026-10-20-1544-decision-priority-model.md"
	content := "---\nkind: decision\ndate: 2026-10-20 15:44\ntitle: 우선순위 큐 모델\ntags: [priority, queue]\n---\n\n등급 <P0> & 서버 계산 순서\r\n"
	resp, err := c.PutDocumentWith(ctx, "AGB", name, DocumentPut{Content: content})
	var d model.Document
	if err != nil || resp.Status != 201 || resp.Decode(&d) != nil || d.Name != name || d.Title == nil || *d.Title != "우선순위 큐 모델" || d.Content != nil {
		t.Fatalf("put = %+v, %v", resp, err)
	}
	// The same content and metadata again changes nothing.
	if resp, err = c.PutDocumentWith(ctx, "AGB", name, DocumentPut{Content: content}); err != nil || resp.Status != 200 {
		t.Fatalf("same again = %+v, %v", resp, err)
	}

	// Other content: the error carries the hash of the stored content.
	_, err = c.PutDocumentWith(ctx, "AGB", name, DocumentPut{Content: content + "x"})
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != 409 || apiErr.Code != "conflict" || len(apiErr.Details) != 1 ||
		apiErr.Details[0].Reason != "immutable" || apiErr.Details[0].SHA256 != d.SHA256 {
		t.Fatalf("other content err = %v (%+v)", err, apiErr)
	}

	resp, err = c.GetDocument(ctx, "AGB", name)
	var full model.Document
	if err != nil || resp.Decode(&full) != nil || full.Content == nil || *full.Content != content || full.State != model.StateCurrent {
		t.Fatalf("get = %+v, %v", full, err)
	}
	resp, err = c.ListDocuments(ctx, "AGB", Page{Limit: 500})
	var list struct {
		Items []model.Document `json:"items"`
		Total int              `json:"total"`
		Limit int              `json:"limit"`
	}
	if err != nil || resp.Decode(&list) != nil || list.Total != 1 || len(list.Items) != 1 || list.Limit != 500 || list.Items[0].SHA256 != d.SHA256 || list.Items[0].Content != nil {
		t.Fatalf("list = %+v, %v", list, err)
	}
	resp, err = c.SearchDocuments(ctx, DocumentFilter{Query: "우선순위", Project: "AGB", Kind: "decision", State: "all"})
	var found struct {
		Items []model.Document `json:"items"`
	}
	if err != nil || resp.Decode(&found) != nil || len(found.Items) != 1 || found.Items[0].Snippet == nil || !strings.Contains(*found.Items[0].Snippet, "[우선순위]") {
		t.Fatalf("search = %+v, %v", found, err)
	}
	if _, err := c.SearchDocuments(ctx, DocumentFilter{Kind: "Plan"}); !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Errorf("unknown kind err = %v", err)
	}
	if _, err := c.GetDocument(ctx, "AGB", "2026-10-21-0900-report-none.md"); !errors.As(err, &apiErr) || apiErr.Status != 404 {
		t.Errorf("missing document err = %v", err)
	}
}

func TestDocumentStateMethodsBuildRequests(t *testing.T) {
	rec := newRecorder(t)
	c := rec.client()
	ctx := context.Background()
	const name = "2026-10-20-1544-decision-priority-model.md"
	text := func(s string) *string { return &s }
	yes := true
	cases := []struct {
		label  string
		call   func() (*Response, error)
		method string
		path   string
		query  string
		body   string
	}{
		{"put with nothing but the content", func() (*Response, error) {
			return c.PutDocumentWith(ctx, "AGB", name, DocumentPut{Content: "본문"})
		}, "PUT", "/v1/projects/AGB/documents/" + name, "", `{"content":"본문"}`},
		{"put with every field", func() (*Response, error) {
			return c.PutDocumentWith(ctx, "AGB", name, DocumentPut{Content: "본문", Kind: text("decision"), Date: text("2026-10-20"), Title: text("제목"), Path: text("a/b.md"),
				Tags: []string{"a"}, Supersedes: []string{"x.md"}, Related: []string{}, Issues: []string{"AGB-1"}, Archived: &yes, Imported: &yes})
		}, "PUT", "/v1/projects/AGB/documents/" + name, "",
			`{"archived":true,"content":"본문","date":"2026-10-20","imported":true,"issues":["AGB-1"],"kind":"decision","path":"a/b.md","related":[],"supersedes":["x.md"],"tags":["a"],"title":"제목"}`},
		{"put of a path name", func() (*Response, error) {
			return c.PutDocumentWith(ctx, "AGB", "notes/a.md", DocumentPut{Content: "x", Imported: &yes})
		}, "PUT", "/v1/projects/AGB/documents/notes/a.md", "", `{"content":"x","imported":true}`},
		{"archive", func() (*Response, error) { return c.ArchiveDocument(ctx, "AGB", name) }, "POST", "/v1/projects/AGB/documents/" + name + "/archive", "", ""},
		{"unarchive", func() (*Response, error) { return c.UnarchiveDocument(ctx, "AGB", name) }, "DELETE", "/v1/projects/AGB/documents/" + name + "/archive", "", ""},
		{"add a relation", func() (*Response, error) { return c.AddDocumentRelation(ctx, "AGB", name, "supersedes", "x.md") },
			"POST", "/v1/projects/AGB/documents/" + name + "/relations", "", `{"kind":"supersedes","target":"x.md"}`},
		{"remove a relation", func() (*Response, error) { return c.RemoveDocumentRelation(ctx, "AGB", name, "about", "AGB-1") },
			"DELETE", "/v1/projects/AGB/documents/" + name + "/relations", "", `{"kind":"about","target":"AGB-1"}`},
		{"activity", func() (*Response, error) { return c.DocumentActivity(ctx, "AGB", name, Page{Limit: 10, Offset: 5}) },
			"GET", "/v1/projects/AGB/documents/" + name + "/activity", "limit=10&offset=5", ""},
		{"search by state", func() (*Response, error) {
			return c.SearchDocuments(ctx, DocumentFilter{State: "all", Imported: "true"})
		}, "GET", "/v1/documents", "imported=true&state=all", ""},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			rec.header, rec.body = nil, nil
			if _, err := tc.call(); err != nil {
				t.Fatal(err)
			}
			if rec.method != tc.method || rec.path != tc.path || rec.query.Encode() != tc.query {
				t.Errorf("request = %s %s?%s, want %s %s?%s", rec.method, rec.path, rec.query.Encode(), tc.method, tc.path, tc.query)
			}
			if tc.body == "" {
				if len(rec.body) != 0 {
					t.Errorf("body = %q, want none", rec.body)
				}
				return
			}
			var got, want map[string]any
			if err := json.Unmarshal(rec.body, &got); err != nil {
				t.Fatalf("body %q: %v", rec.body, err)
			}
			json.Unmarshal([]byte(tc.body), &want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("body = %s, want %s", rec.body, tc.body)
			}
		})
	}
}

func TestDocumentStateAgainstRealHandler(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "agb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	actors := []model.Actor{{ID: "owner", Kind: model.ActorUser}}
	ctx := context.Background()
	if err := st.EnsureActors(ctx, actors); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.New(api.Options{Token: testToken, Actors: actors, Version: "t", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: st}))
	defer srv.Close()
	c := newClient(srv.URL)
	if _, err := c.CreateProject(ctx, "AGB", "agent-board"); err != nil {
		t.Fatal(err)
	}
	const old, next = "2026-10-01-1000-decision-old.md", "2026-10-02-1000-decision-new.md"
	title := "제목"
	var d struct {
		model.Document
		Warnings []model.DocumentWarning `json:"warnings"`
	}
	resp, err := c.PutDocumentWith(ctx, "AGB", old, DocumentPut{Content: "본문", Title: &title})
	if err != nil || resp.Status != 201 || resp.Decode(&d) != nil || d.State != model.StateCurrent || d.Warnings == nil {
		t.Fatalf("put = %+v, %v", resp, err)
	}
	if resp, err = c.PutDocumentWith(ctx, "AGB", next, DocumentPut{Content: "새 본문", Title: &title, Supersedes: []string{old}}); err != nil || resp.Status != 201 {
		t.Fatalf("put with a relation = %+v, %v", resp, err)
	}
	if resp, err = c.GetDocument(ctx, "AGB", old); err != nil || resp.Decode(&d) != nil || d.State != model.StateSuperseded || !reflect.DeepEqual(d.SupersededBy, []string{next}) {
		t.Fatalf("get = %+v, %v", d.Document, err)
	}
	if resp, err = c.ArchiveDocument(ctx, "AGB", next); err != nil || resp.Decode(&d) != nil || d.State != model.StateArchived {
		t.Fatalf("archive = %+v, %v", d.Document, err)
	}
	if resp, err = c.RemoveDocumentRelation(ctx, "AGB", next, "supersedes", old); err != nil || resp.Decode(&d) != nil || len(d.Supersedes) != 0 {
		t.Fatalf("remove = %+v, %v", d.Document, err)
	}
	var apiErr *Error
	if _, err = c.AddDocumentRelation(ctx, "AGB", next, "related", "nothing.md"); !errors.As(err, &apiErr) || apiErr.Status != 422 || apiErr.Details[0].Reason != "unresolved" {
		t.Errorf("unresolved relation err = %v", err)
	}
	resp, err = c.DocumentActivity(ctx, "AGB", next, Page{})
	var list struct {
		Items []model.DocumentActivity `json:"items"`
		Total int                      `json:"total"`
	}
	if err != nil || resp.Decode(&list) != nil || list.Total != 2 || len(list.Items) != 2 || list.Items[0].Changes["supersedes"].To == nil {
		t.Errorf("activity = %+v, %v", list, err)
	}
	if _, err = c.UnarchiveDocument(ctx, "AGB", next); err != nil {
		t.Errorf("unarchive: %v", err)
	}
}
