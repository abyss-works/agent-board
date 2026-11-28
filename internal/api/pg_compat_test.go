package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPGClassicViewsAndIssueShape runs the classic list and view endpoints of
// PostgreSQL mode through the handler.
func TestPGClassicViewsAndIssueShape(t *testing.T) {
	h := newPGTestHandler(t)
	post := func(path string, body any, want int) []byte {
		t.Helper()
		b, _ := json.Marshal(body)
		rec := pgDo(h, "POST", path, b, "")
		if rec.Code != want {
			t.Fatalf("POST %s = %d %s, want %d", path, rec.Code, rec.Body.String(), want)
		}
		return rec.Body.Bytes()
	}
	get := func(path string, want int) []byte {
		t.Helper()
		rec := pgDo(h, "GET", path, nil, "")
		if rec.Code != want {
			t.Fatalf("GET %s = %d %s, want %d", path, rec.Code, rec.Body.String(), want)
		}
		return rec.Body.Bytes()
	}
	post("/v1/projects", map[string]any{"key": "CMP", "name": "호환"}, 201)

	var base, next struct {
		Key       string   `json:"key"`
		Parent    *string  `json:"parent"`
		DependsOn []string `json:"depends_on"`
		EpicKey   *string  `json:"epic_key"`
	}
	if err := json.Unmarshal(post("/v1/issues", map[string]any{"project": "CMP", "title": "선행 작업", "type": "bug", "priority": "P1", "labels": []string{"api"}}, 201), &base); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(post("/v1/issues", map[string]any{"project": "CMP", "title": "후행 작업", "type": "bug", "parent": base.Key, "depends_on": []string{base.Key}}, 201), &next); err != nil {
		t.Fatal(err)
	}
	if next.Parent == nil || *next.Parent != base.Key || len(next.DependsOn) != 1 || next.EpicKey == nil {
		t.Fatalf("classic fields not answered: %+v", next)
	}
	// a parent chain that leads back, a dependency that leads back, and a topic are refused
	for _, c := range []struct {
		path string
		body map[string]any
		want int
	}{
		{"/v1/issues/" + base.Key, map[string]any{"parent": next.Key}, 409},
		{"/v1/issues/" + base.Key, map[string]any{"depends_on": []string{next.Key}}, 409},
		{"/v1/issues/" + base.Key, map[string]any{"topic": 1}, 422},
	} {
		b, _ := json.Marshal(c.body)
		if rec := pgDo(h, "PATCH", c.path, b, ""); rec.Code != c.want {
			t.Errorf("PATCH %s %v = %d %s, want %d", c.path, c.body, rec.Code, rec.Body.String(), c.want)
		}
	}

	var list struct {
		Items []struct {
			Key string `json:"key"`
		} `json:"items"`
		Total int `json:"total"`
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(get("/v1/issues?project=CMP&q=%EC%84%A0%ED%96%89", 200), &list); err != nil || list.Total != 1 || list.Items[0].Key != base.Key || list.Limit != 50 {
		t.Fatalf("issue search = %+v %v", list, err)
	}
	if err := json.Unmarshal(get("/v1/queue?project=CMP&status=open", 200), &list); err != nil || list.Total != 2 {
		t.Fatalf("queue = %+v %v", list, err)
	}
	var q struct {
		Items []struct {
			Key       string   `json:"key"`
			Ready     bool     `json:"ready"`
			BlockedBy []string `json:"blocked_by"`
		} `json:"items"`
	}
	if err := json.Unmarshal(get("/v1/queue?project=CMP&status=open", 200), &q); err != nil {
		t.Fatal(err)
	}
	for _, it := range q.Items {
		if it.Key == next.Key && (it.Ready || len(it.BlockedBy) != 1) {
			t.Errorf("blocked issue reads as %+v", it)
		}
		if it.Key == base.Key && !it.Ready {
			t.Errorf("free issue reads as %+v", it)
		}
	}
	for _, p := range []string{
		"/v1/board?project=CMP", "/v1/labels?project=CMP", "/v1/projects/CMP/overview", "/v1/agenda?project=CMP",
		"/v1/digest?since=2020-01-01T00:00:00.000Z&project=CMP", "/v1/activity?project=CMP", "/v1/projects/CMP/activity",
		"/v1/issues/" + base.Key + "/claims", "/v1/issues/" + base.Key + "/activity", "/v1/templates", "/v1/projects/CMP/topics",
	} {
		get(p, 200)
	}
	var act struct {
		Items []struct {
			Kind string `json:"kind"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(get("/v1/issues/"+base.Key+"/activity", 200), &act); err != nil || act.Total < 1 || act.Items[0].Kind == "created" && act.Total > 1 {
		t.Fatalf("activity must list newest first: %+v %v", act, err)
	}
	var ov struct {
		Counts struct {
			Total int `json:"total"`
			Open  int `json:"open"`
		} `json:"counts"`
		Next struct {
			Total int `json:"total"`
		} `json:"next"`
		Blocked struct {
			Total int `json:"total"`
		} `json:"blocked"`
	}
	if err := json.Unmarshal(get("/v1/projects/CMP/overview", 200), &ov); err != nil || ov.Counts.Total != 2 || ov.Next.Total != 1 || ov.Blocked.Total != 1 {
		t.Fatalf("overview = %+v %v", ov, err)
	}
	if rec := pgDo(h, "POST", "/v1/projects/CMP/topics", []byte(`{"name":"x"}`), ""); rec.Code != 410 {
		t.Errorf("topic write = %d, want 410", rec.Code)
	}
	get("/v1/projects?limit=1&offset=0&sort=-created_at", 200)
	if !strings.Contains(string(get("/v1/projects?limit=1", 200)), `"limit":1`) {
		t.Error("project list must carry its paging envelope")
	}
}

// TestPGClassicDocumentsSearchGraph runs the record document, search, and graph
// endpoints of PostgreSQL mode through the handler.
func TestPGClassicDocumentsSearchGraph(t *testing.T) {
	h := newPGTestHandler(t)
	do := func(method, path string, body any, want int) []byte {
		t.Helper()
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		rec := pgDo(h, method, path, b, "")
		if rec.Code != want {
			t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body.String(), want)
		}
		return rec.Body.Bytes()
	}
	do("POST", "/v1/projects", map[string]any{"key": "DOC", "name": "문서"}, 201)
	do("POST", "/v1/issues", map[string]any{"project": "DOC", "title": "근거 이슈"}, 201)
	text := func(kind, title, body string) string {
		return "---\nkind: " + kind + "\ndate: 2026-11-06 14:00\ntitle: " + title + "\n---\n# " + title + "\n" + body + "\n"
	}
	oldName, newName := "2026-11-01-1000-decision-old.md", "2026-11-02-1000-decision-new.md"
	do("PUT", "/v1/projects/DOC/documents/"+oldName, map[string]any{"content": text("decision", "옛 결정", "캐시는 오분 만료")}, 201)
	do("PUT", "/v1/projects/DOC/documents/"+oldName, map[string]any{"content": text("decision", "옛 결정", "캐시는 오분 만료")}, 200)
	do("PUT", "/v1/projects/DOC/documents/"+oldName, map[string]any{"content": text("decision", "옛 결정", "다른 내용")}, 409)
	var created struct {
		State      string   `json:"state"`
		Supersedes []string `json:"supersedes"`
		Issues     []string `json:"issues"`
		Warnings   []any    `json:"warnings"`
	}
	if err := json.Unmarshal(do("PUT", "/v1/projects/DOC/documents/"+newName, map[string]any{
		"content": text("decision", "새 결정", "캐시는 삼십분 만료"), "supersedes": []string{oldName}, "issues": []string{"DOC-1"}}, 201), &created); err != nil {
		t.Fatal(err)
	}
	if created.State != "current" || len(created.Supersedes) != 1 || len(created.Issues) != 1 || created.Warnings == nil {
		t.Fatalf("created = %+v", created)
	}
	do("PUT", "/v1/projects/DOC/documents/"+newName, map[string]any{"content": text("decision", "새 결정", "캐시는 삼십분 만료")}, 409) // metadata differs
	do("POST", "/v1/projects/DOC/documents/"+oldName+"/relations", map[string]any{"kind": "supersedes", "target": newName}, 409)
	do("POST", "/v1/projects/DOC/documents/nothing.md/archive", nil, 404)

	var old struct {
		State        string   `json:"state"`
		SupersededBy []string `json:"superseded_by"`
	}
	if err := json.Unmarshal(do("GET", "/v1/projects/DOC/documents/"+oldName, nil, 200), &old); err != nil || old.State != "superseded" || len(old.SupersededBy) != 1 {
		t.Fatalf("old = %+v %v", old, err)
	}
	do("POST", "/v1/projects/DOC/documents/"+newName+"/archive", nil, 200)
	var acts struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(do("GET", "/v1/projects/DOC/documents/"+newName+"/activity", nil, 200), &acts); err != nil || acts.Total != 1 {
		t.Fatalf("document activity = %+v %v", acts, err)
	}
	do("DELETE", "/v1/projects/DOC/documents/"+newName+"/archive", nil, 200)

	var docs struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(do("GET", "/v1/documents?project=DOC&q=%EC%82%BC%EC%8B%AD%EB%B6%84", nil, 200), &docs); err != nil || docs.Total != 1 || docs.Items[0].Name != newName {
		t.Fatalf("document search = %+v %v", docs, err)
	}
	if err := json.Unmarshal(do("GET", "/v1/projects/DOC/documents", nil, 200), &docs); err != nil || docs.Total != 2 {
		t.Fatalf("document list = %+v %v", docs, err)
	}
	var hits struct {
		Items []struct {
			Type string `json:"type"`
		} `json:"items"`
		Semantic string `json:"semantic"`
	}
	if err := json.Unmarshal(do("GET", "/v1/search?q=%EA%B7%BC%EA%B1%B0&project=DOC", nil, 200), &hits); err != nil || len(hits.Items) != 1 || hits.Items[0].Type != "issue" || hits.Semantic != "disabled" {
		t.Fatalf("search = %+v %v", hits, err)
	}
	var g struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
		Edges []struct {
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(do("GET", "/v1/graph?depth=2&state=all&node=issue:DOC-1", nil, 200), &g); err != nil || len(g.Nodes) != 3 || len(g.Edges) != 2 {
		t.Fatalf("graph = %+v %v", g, err)
	}
	do("GET", "/v1/graph?node=issue:DOC-99", nil, 404)
	// an issue reference has to resolve
	do("POST", "/v1/issues", map[string]any{"project": "DOC", "title": "참조", "refs": []string{"doc:nothing.md"}}, 422)
	do("POST", "/v1/issues", map[string]any{"project": "DOC", "title": "참조", "refs": []string{"doc:" + newName}}, 201)
}
