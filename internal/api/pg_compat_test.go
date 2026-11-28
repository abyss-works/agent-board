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
