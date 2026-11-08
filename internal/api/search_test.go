package api

import (
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

type searchList struct {
	Items []model.SearchHit `json:"items"`
	Total int               `json:"total"`
	Limit int               `json:"limit"`
	// Semantic is the state of the search by meaning: ok, disabled or failed.
	Semantic string `json:"semantic"`
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
		"unknown param":  "/v1/search?q=x&sort=date",
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

func TestSearchStatusAndStateFilters(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	backlog := e.createIssue(t, `{"project":"AGB","title":"잠금 대기"}`)
	done := e.createIssue(t, `{"project":"AGB","title":"잠금 끝"}`)
	doneLater := e.createIssue(t, `{"project":"AGB","title":"잠금 취소"}`)
	gone := e.createIssue(t, `{"project":"AGB","title":"잠금 삭제"}`)
	for key, body := range map[string]string{done.Key: `{"status":"doing"}`, doneLater.Key: `{"status":"doing"}`} {
		if rec := e.do(t, "PATCH", "/v1/issues/"+key, body, nil); rec.Code != 200 {
			t.Fatalf("patch %s: %d %s", key, rec.Code, rec.Body.String())
		}
	}
	for key, body := range map[string]string{done.Key: `{"status":"done","status_reason":"끝냈다"}`, doneLater.Key: `{"status":"cancelled","status_reason":"취소했다"}`} {
		if rec := e.do(t, "PATCH", "/v1/issues/"+key, body, nil); rec.Code != 200 {
			t.Fatalf("patch %s: %d %s", key, rec.Code, rec.Body.String())
		}
	}
	if rec := e.do(t, "PATCH", "/v1/issues/"+gone.Key, `{"status":"deleted","status_reason":"지웠다"}`, nil); rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "POST", "/v1/issues/"+backlog.Key+"/comments", `{"body":"잠금 댓글"}`, nil); rec.Code != 201 {
		t.Fatalf("comment: %d %s", rec.Code, rec.Body.String())
	}
	e.mustPutDocument(t, "AGB", stOld, "history", apiDocText("decision", "잠금 옛"))
	e.putFields(t, "AGB", stNew, map[string]any{"content": apiDocText("decision", "잠금 새"), "supersedes": []string{stOld}}, nil)
	e.putFields(t, "AGB", stRep, map[string]any{"content": apiDocText("report", "잠금 보관"), "archived": true}, nil)

	keys := func(target string) string {
		t.Helper()
		rec := e.do(t, "GET", "/v1/search?q=잠금&limit=50"+target, "", nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", target, rec.Code, rec.Body.String())
		}
		var l searchList
		decodeJSON(t, rec, &l)
		var out []string
		for _, h := range l.Items {
			k := string(h.Type)[:1] + ":" + h.Key
			if h.Type == model.SearchDocument {
				k = "d:" + h.Key[:10]
			}
			out = append(out, k)
		}
		sort.Strings(out)
		return strings.Join(out, " ")
	}
	b, d, c := backlog.Key, done.Key, doneLater.Key
	cases := map[string]string{
		// Default: every status but deleted, and the current documents.
		"":                          "c:" + b + " d:2026-10-02 i:" + b + " i:" + d + " i:" + c,
		"&status=all":               "c:" + b + " d:2026-10-02 i:" + b + " i:" + d + " i:" + c,
		"&status=open":              "c:" + b + " d:2026-10-02 i:" + b,
		"&status=closed":            "d:2026-10-02 i:" + d + " i:" + c,
		"&status=done":              "d:2026-10-02 i:" + d,
		"&status=backlog,done":      "c:" + b + " d:2026-10-02 i:" + b + " i:" + d,
		"&state=all":                "c:" + b + " d:2026-10-01 d:2026-10-02 d:2026-10-03 i:" + b + " i:" + d + " i:" + c,
		"&state=superseded":         "c:" + b + " d:2026-10-01 i:" + b + " i:" + d + " i:" + c,
		"&state=archived,current":   "c:" + b + " d:2026-10-02 d:2026-10-03 i:" + b + " i:" + d + " i:" + c,
		"&status=open&state=all":    "c:" + b + " d:2026-10-01 d:2026-10-02 d:2026-10-03 i:" + b,
		"&type=document&state=all":  "d:2026-10-01 d:2026-10-02 d:2026-10-03",
		"&type=comment&status=done": "",
	}
	for target, want := range cases {
		if got := keys(target); got != want {
			t.Errorf("%q: %s\nwant %s", target, got, want)
		}
	}
	for _, target := range []string{"&status=deleted", "&status=open,deleted", "&status=wip", "&state=old", "&status=done&status=open"} {
		rec := e.do(t, "GET", "/v1/search?q=잠금"+target, "", nil)
		if rec.Code != 400 {
			t.Errorf("%s: %d %s", target, rec.Code, rec.Body.String())
		}
	}

	// The fields of a hit.
	rec := e.do(t, "GET", "/v1/search?q="+url.QueryEscape("잠금")+"&state=all&limit=50", "", nil)
	var l searchList
	decodeJSON(t, rec, &l)
	for _, h := range l.Items {
		switch {
		case h.Type == model.SearchDocument && h.Key == stOld:
			if h.Status != nil || *h.State != "superseded" || h.Supersedes == nil || len(h.Supersedes) != 0 || !reflect.DeepEqual(h.SupersededBy, []string{stNew}) {
				t.Errorf("superseded hit = %+v", h)
			}
		case h.Type == model.SearchDocument && h.Key == stNew:
			if *h.State != "current" || !reflect.DeepEqual(h.Supersedes, []string{stOld}) || h.SupersededBy == nil {
				t.Errorf("current hit = %+v", h)
			}
		case h.Type == model.SearchComment:
			if h.Status == nil || *h.Status != "backlog" || h.State != nil || h.Supersedes != nil {
				t.Errorf("comment hit = %+v", h)
			}
		case h.Type == model.SearchIssue:
			if h.Status == nil || h.State != nil || h.SupersededBy != nil {
				t.Errorf("issue hit = %+v", h)
			}
		}
	}
	// The JSON order of the spec, with nulls where a field does not apply.
	for _, want := range []string{`"kind":"decision","status":null,"state":"superseded","supersedes":[],"superseded_by":["` + stNew + `"],"at":`, `"status":"backlog","state":null,"supersedes":null,"superseded_by":null,"at":`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("JSON lacks %s: %s", want, rec.Body.String())
		}
	}
}
