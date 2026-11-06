//go:build e2e

package e2e

import (
	"net/url"
	"strings"
	"testing"
)

type searchHit struct {
	Type      string  `json:"type"`
	Project   string  `json:"project"`
	Key       string  `json:"key"`
	CommentID *int64  `json:"comment_id"`
	Title     *string `json:"title"`
	Kind      *string `json:"kind"`
	At        string  `json:"at"`
	Snippet   string  `json:"snippet"`
}

// TestSearch finds one word of this run in an issue, its comment and a record
// document of two projects, and narrows the result.
func TestSearch(t *testing.T) {
	p := createProject(t, "SA")
	q := createProject(t, "SB")
	word := "e2e" + strings.ToLower(runID) + "find"
	iss := newIssue(t, p.Key, "통합 검색 "+word, nil)
	expect(t, as("claude").do(t, "POST", "/v1/issues/"+iss.Key+"/comments", map[string]any{"body": word + " 원인 확인", "summary": "원인", "comment_kind": "finding"}), 201)
	mustPutDocument(t, owner, q.Key, "2026-11-06-1400-decision-search.md", "history", documentText("decision", "2026-11-06 14:00", "검색 결정", "", word+" 결정"))
	deleted := newIssue(t, p.Key, "지운 "+word, nil)
	deleteIssue(t, owner, deleted.Key)

	all := decodeAs[list[searchHit]](t, owner.do(t, "GET", "/v1/search?q="+url.QueryEscape(word), nil), 200)
	types := map[string]int{}
	for _, h := range all.Items {
		types[h.Type]++
		if h.Snippet == "" || h.At == "" {
			t.Errorf("hit lacks fields: %+v", h)
		}
	}
	if all.Total != 3 || types["issue"] != 1 || types["comment"] != 1 || types["document"] != 1 {
		t.Errorf("all = %d %v", all.Total, types)
	}
	only := decodeAs[list[searchHit]](t, owner.do(t, "GET", "/v1/search?q="+url.QueryEscape(word)+"&type=comment&comment_kind=finding&project="+p.Key, nil), 200)
	if only.Total != 1 || only.Items[0].Key != iss.Key || only.Items[0].CommentID == nil || only.Items[0].Kind == nil || *only.Items[0].Kind != "finding" {
		t.Errorf("finding comments = %+v", only)
	}
	docs := decodeAs[list[searchHit]](t, owner.do(t, "GET", "/v1/search?q="+url.QueryEscape(word)+"&project="+q.Key, nil), 200)
	if docs.Total != 1 || docs.Items[0].Type != "document" || docs.Items[0].At != "2026-11-06 14:00" {
		t.Errorf("documents of %s = %+v", q.Key, docs)
	}
	wantError(t, owner.do(t, "GET", "/v1/search", nil), 400, "bad_request")
}
