package api

import (
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

type searchList struct {
	Items []model.SearchHit `json:"items"`
	Total int               `json:"total"`
	Limit int               `json:"limit"`
}

func TestSearch(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.mustPutDocument(t, "AGB", apiDocPriority, "history", apiDocText("decision", "잠금 기록", "title: 잠금 결정"))
	issue := e.createIssue(t, `{"project":"AGB","title":"잠금 조사"}`)
	rec := e.do(t, "POST", "/v1/issues/"+issue.Key+"/comments", `{"body":"잠금 원인은 긴 트랜잭션이다","summary":"잠금 원인","comment_kind":"finding"}`, nil)
	if rec.Code != 201 {
		t.Fatalf("comment: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, "GET", "/v1/search?q=잠금", "", nil)
	var l searchList
	decodeJSON(t, rec, &l)
	if rec.Code != 200 || l.Total != 3 || l.Limit != 20 {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	seen := map[model.SearchType]bool{}
	for _, h := range l.Items {
		seen[h.Type] = true
	}
	if !seen[model.SearchIssue] || !seen[model.SearchComment] || !seen[model.SearchDocument] {
		t.Errorf("types = %v", seen)
	}
	rec = e.do(t, "GET", "/v1/search?q=잠금&type=comment&comment_kind=finding&project=AGB", "", nil)
	decodeJSON(t, rec, &l)
	if rec.Code != 200 || l.Total != 1 || l.Items[0].Key != issue.Key || l.Items[0].CommentID == nil {
		t.Errorf("finding comments: %d %s", rec.Code, rec.Body.String())
	}
	for label, target := range map[string]string{
		"no query":       "/v1/search",
		"blank query":    "/v1/search?q=%20",
		"unknown type":   "/v1/search?q=x&type=wiki",
		"unknown kind":   "/v1/search?q=x&comment_kind=plan",
		"bad project":    "/v1/search?q=x&project=agb",
		"unknown param":  "/v1/search?q=x&status=open",
		"limit over 200": "/v1/search?q=x&limit=201",
		"eleven terms":   "/v1/search?q=a+b+c+d+e+f+g+h+i+j+k",
	} {
		if rec := e.do(t, "GET", target, "", nil); rec.Code != 400 {
			t.Errorf("%s: %d %s", label, rec.Code, rec.Body.String())
		}
	}
	if rec := e.do(t, "POST", "/v1/search", `{}`, nil); rec.Code != 405 || rec.Header().Get("Allow") != "GET" {
		t.Errorf("POST: %d Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
}
