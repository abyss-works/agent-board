package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/api"
	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
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
			return c.PutDocument(ctx, "AGB", name, "history", "---\nkind: decision\n---\n본문\n")
		},
			"PUT", "/v1/projects/AGB/documents/" + name, "", `{"content":"---\nkind: decision\n---\n본문\n","location":"history"}`},
		{"put into the archive", func() (*Response, error) { return c.PutDocument(ctx, "AGB", name, "archive", "x") },
			"PUT", "/v1/projects/AGB/documents/" + name, "", `{"content":"x","location":"archive"}`},
		{"list", func() (*Response, error) { return c.ListDocuments(ctx, "AGB", Page{}) }, "GET", "/v1/projects/AGB/documents", "", ""},
		{"list a page", func() (*Response, error) { return c.ListDocuments(ctx, "AGB", Page{Limit: 500, Offset: 500}) },
			"GET", "/v1/projects/AGB/documents", "limit=500&offset=500", ""},
		{"get", func() (*Response, error) { return c.GetDocument(ctx, "AGB", name) }, "GET", "/v1/projects/AGB/documents/" + name, "", ""},
		{"search", func() (*Response, error) { return c.SearchDocuments(ctx, DocumentFilter{}) }, "GET", "/v1/documents", "", ""},
		{"search with every filter", func() (*Response, error) {
			return c.SearchDocuments(ctx, DocumentFilter{Query: "우선순위 큐", Project: "AGB,AGT", Kind: "decision,report", Location: "history", Page: Page{Limit: 10, Offset: 20}})
		}, "GET", "/v1/documents", "kind=decision%2Creport&limit=10&location=history&offset=20&project=AGB%2CAGT&q=%EC%9A%B0%EC%84%A0%EC%88%9C%EC%9C%84+%ED%81%90", ""},
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
				if len(got) != 2 || got["content"] != want["content"] || got["location"] != want["location"] {
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
	if _, err := c.PutDocument(ctx, "AGB", name, "history", "<a> & é\r\n"); err != nil {
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
	resp, err := c.PutDocument(ctx, "AGB", name, "history", content)
	var d model.Document
	if err != nil || resp.Status != 201 || resp.Decode(&d) != nil || d.Name != name || d.Title == nil || *d.Title != "우선순위 큐 모델" || d.Content != nil {
		t.Fatalf("put = %+v, %v", resp, err)
	}
	if resp, err = c.PutDocument(ctx, "AGB", name, "archive", content); err != nil || resp.Status != 200 {
		t.Fatalf("move = %+v, %v", resp, err)
	}

	// Other content: the error carries the hash of the stored content.
	_, err = c.PutDocument(ctx, "AGB", name, "archive", content+"x")
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != 409 || apiErr.Code != "conflict" || len(apiErr.Details) != 1 ||
		apiErr.Details[0].Reason != "immutable" || apiErr.Details[0].SHA256 != d.SHA256 {
		t.Fatalf("other content err = %v (%+v)", err, apiErr)
	}

	resp, err = c.GetDocument(ctx, "AGB", name)
	var full model.Document
	if err != nil || resp.Decode(&full) != nil || full.Content == nil || *full.Content != content || full.Location != model.LocationArchive {
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
	resp, err = c.SearchDocuments(ctx, DocumentFilter{Query: "우선순위", Project: "AGB", Kind: "decision", Location: "archive"})
	var found struct {
		Items []model.Document `json:"items"`
	}
	if err != nil || resp.Decode(&found) != nil || len(found.Items) != 1 || found.Items[0].Snippet == nil || !strings.Contains(*found.Items[0].Snippet, "[우선순위]") {
		t.Fatalf("search = %+v, %v", found, err)
	}
	if _, err := c.SearchDocuments(ctx, DocumentFilter{Kind: "plan"}); !errors.As(err, &apiErr) || apiErr.Status != 400 {
		t.Errorf("unknown kind err = %v", err)
	}
	if _, err := c.GetDocument(ctx, "AGB", "2026-10-21-0900-report-none.md"); !errors.As(err, &apiErr) || apiErr.Status != 404 {
		t.Errorf("missing document err = %v", err)
	}
}
