//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
)

type topic struct {
	ID        int64  `json:"id"`
	Project   string `json:"project"`
	Name      string `json:"name"`
	Parent    *int64 `json:"parent"`
	Path      string `json:"path"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	CreatedBy string `json:"created_by"`
	UpdatedBy string `json:"updated_by"`
}

type topicList struct {
	Items []topic `json:"items"`
}

func createTopic(t testing.TB, c caller, project string, body map[string]any) topic {
	t.Helper()
	r := c.do(t, "POST", "/v1/projects/"+project+"/topics", body)
	tp := decodeAs[topic](t, r, 201)
	if r.header.Get("Location") != fmt.Sprintf("/v1/topics/%d", tp.ID) {
		t.Errorf("Location = %q", r.header.Get("Location"))
	}
	return tp
}

func topicPaths(items []topic) string {
	var out []string
	for _, it := range items {
		out = append(out, it.Path)
	}
	return strings.Join(out, ",")
}

func TestTopics(t *testing.T) {
	p := createProject(t, "TP")
	q := createProject(t, "TQ")
	flow := createTopic(t, owner, p.Key, map[string]any{"name": "이슈 흐름"})
	triage := createTopic(t, as("claude"), p.Key, map[string]any{"name": "트리아지", "parent": flow.ID})
	infra := createTopic(t, owner, p.Key, map[string]any{"name": "인프라"})
	foreign := createTopic(t, owner, q.Key, map[string]any{"name": "다른 프로젝트"})
	topicPath := func(id int64) string { return fmt.Sprintf("/v1/topics/%d", id) }

	t.Run("shape and list order", func(t *testing.T) {
		if flow.Project != p.Key || flow.Parent != nil || flow.Path != "이슈 흐름" || flow.Status != "active" || flow.CreatedBy != "owner" {
			t.Errorf("flow = %+v", flow)
		}
		wantTime(t, "created_at", flow.CreatedAt)
		if triage.Parent == nil || *triage.Parent != flow.ID || triage.Path != "이슈 흐름/트리아지" || triage.CreatedBy != "claude" {
			t.Errorf("triage = %+v", triage)
		}
		got := decodeAs[topicList](t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/topics", nil), 200)
		if topicPaths(got.Items) != "이슈 흐름,이슈 흐름/트리아지,인프라" {
			t.Errorf("list = %s", topicPaths(got.Items))
		}
		wantError(t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/topics?status=bogus", nil), 400, "bad_request")
		wantError(t, owner.do(t, "GET", "/v1/projects/"+projectKey("NOPE")+"/topics", nil), 404, "not_found")
	})
	t.Run("rules", func(t *testing.T) {
		post := func(body map[string]any) response {
			return owner.do(t, "POST", "/v1/projects/"+p.Key+"/topics", body)
		}
		for _, body := range []map[string]any{{"name": ""}, {"name": "a/b"}, {"name": strings.Repeat("가", 51)}, {}} {
			if e := wantError(t, post(body), 422, "validation_failed"); !e.hasField("name") {
				t.Errorf("%v: %+v", body, e.Error.Details)
			}
		}
		for _, parent := range []int64{triage.ID, foreign.ID, 99999999} {
			if e := wantError(t, post(map[string]any{"name": "x", "parent": parent}), 422, "validation_failed"); !e.hasField("parent") {
				t.Errorf("parent %d: %+v", parent, e.Error.Details)
			}
		}
		if e := wantError(t, post(map[string]any{"name": "트리아지", "parent": flow.ID}), 409, "conflict"); !e.hasReason("duplicate") {
			t.Errorf("duplicate: %+v", e.Error.Details)
		}
		wantError(t, post(map[string]any{"name": "x", "bogus": 1}), 400, "bad_request")
	})
	t.Run("patch, delete and restore", func(t *testing.T) {
		moved := decodeAs[topic](t, owner.do(t, "PATCH", topicPath(triage.ID), map[string]any{"parent": infra.ID, "name": "분류"}), 200)
		if moved.Path != "인프라/분류" || moved.UpdatedBy != "owner" {
			t.Errorf("moved = %+v", moved)
		}
		if e := wantError(t, owner.do(t, "DELETE", topicPath(infra.ID), nil), 409, "conflict"); !e.hasReason("topic_has_children") {
			t.Errorf("delete with a child: %+v", e.Error.Details)
		}
		gone := decodeAs[topic](t, owner.do(t, "DELETE", topicPath(triage.ID), nil), 200)
		if gone.Status != "deleted" {
			t.Errorf("deleted = %+v", gone)
		}
		decodeAs[topic](t, owner.do(t, "DELETE", topicPath(infra.ID), nil), 200)
		active := decodeAs[topicList](t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/topics", nil), 200)
		all := decodeAs[topicList](t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/topics?status=all", nil), 200)
		if topicPaths(active.Items) != "이슈 흐름" || len(all.Items) != 3 {
			t.Errorf("active %s, all %d", topicPaths(active.Items), len(all.Items))
		}
		wantError(t, owner.do(t, "PATCH", topicPath(99999999), map[string]any{"name": "x"}), 404, "not_found")
		wantError(t, owner.do(t, "GET", topicPath(flow.ID), nil), 405, "method_not_allowed")
		back := decodeAs[topic](t, owner.do(t, "PATCH", topicPath(infra.ID), map[string]any{"status": "active"}), 200)
		backChild := decodeAs[topic](t, owner.do(t, "PATCH", topicPath(triage.ID), map[string]any{"status": "active"}), 200)
		if back.Status != "active" || backChild.Status != "active" {
			t.Errorf("restored = %+v %+v", back, backChild)
		}
	})
	t.Run("issues carry a topic", func(t *testing.T) {
		onChild := newIssue(t, p.Key, "분류된 이슈", map[string]any{"topic": triage.ID})
		onTop := newIssue(t, p.Key, "대주제 이슈", map[string]any{"topic": infra.ID})
		plain := newIssue(t, p.Key, "주제 없음", nil)
		if onChild.TopicPath == nil || *onChild.TopicPath != "인프라/분류" || onChild.Topic == nil || *onChild.Topic != triage.ID || plain.Topic != nil {
			t.Errorf("topic fields = %+v / %+v", onChild, plain)
		}
		wantError(t, owner.do(t, "POST", "/v1/issues", map[string]any{"project": p.Key, "title": "x", "topic": foreign.ID}), 422, "validation_failed")
		got := patchIssue(t, owner, plain.Key, map[string]any{"topic": flow.ID})
		if got.TopicPath == nil || *got.TopicPath != "이슈 흐름" {
			t.Errorf("patched = %+v", got)
		}
		cleared := patchIssue(t, owner, plain.Key, map[string]any{"topic": nil})
		if cleared.Topic != nil || cleared.TopicPath != nil {
			t.Errorf("cleared = %+v", cleared)
		}
		get := func(query string) []string {
			l := decodeAs[struct {
				Items []issue `json:"items"`
			}](t, owner.do(t, "GET", "/v1/issues?status=all&sort=key&project="+p.Key+"&"+query, nil), 200)
			return keys(l.Items)
		}
		if got := get(fmt.Sprintf("topic=%d", infra.ID)); !equalStrings(got, []string{onChild.Key, onTop.Key}) {
			t.Errorf("top-level topic filter = %v", got)
		}
		if got := get(fmt.Sprintf("topic=%d", triage.ID)); !equalStrings(got, []string{onChild.Key}) {
			t.Errorf("child topic filter = %v", got)
		}
		if got := get("topic=none"); !equalStrings(got, []string{plain.Key}) {
			t.Errorf("none filter = %v", got)
		}
		wantError(t, owner.do(t, "GET", "/v1/issues?topic=abc", nil), 400, "bad_request")
	})
}
