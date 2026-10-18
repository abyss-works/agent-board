//go:build e2e

package e2e

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type milestoneProgress struct {
	Total int `json:"total"`
	Done  int `json:"done"`
}

type milestone struct {
	ID          int64             `json:"id"`
	Project     string            `json:"project"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	TargetDate  *string           `json:"target_date"`
	Status      string            `json:"status"`
	Progress    milestoneProgress `json:"progress"`
	ClosedAt    *string           `json:"closed_at"`
	CreatedAt   string            `json:"created_at"`
	UpdatedAt   string            `json:"updated_at"`
	CreatedBy   string            `json:"created_by"`
	UpdatedBy   string            `json:"updated_by"`
}

type milestoneList struct {
	Items []milestone `json:"items"`
}

func createMilestone(t testing.TB, c caller, project string, body map[string]any) milestone {
	t.Helper()
	r := c.do(t, "POST", "/v1/projects/"+project+"/milestones", body)
	m := decodeAs[milestone](t, r, 201)
	if r.header.Get("Location") != fmt.Sprintf("/v1/milestones/%d", m.ID) {
		t.Errorf("Location = %q", r.header.Get("Location"))
	}
	return m
}

func milestoneNames(items []milestone) string {
	var out []string
	for _, it := range items {
		out = append(out, it.Name)
	}
	return strings.Join(out, ",")
}

func TestMilestones(t *testing.T) {
	p := createProject(t, "MS")
	q := createProject(t, "MT")
	v2 := createMilestone(t, owner, p.Key, map[string]any{"name": "v0.2.0", "description": "운영 안정화와 릴리즈 관리", "target_date": "2026-11-01"})
	v3 := createMilestone(t, as("claude"), p.Key, map[string]any{"name": "v0.3.0"})
	v1 := createMilestone(t, owner, p.Key, map[string]any{"name": "v0.1.5", "target_date": "2026-10-20"})
	foreign := createMilestone(t, owner, q.Key, map[string]any{"name": "다른 프로젝트"})
	milestonePath := func(id int64) string { return fmt.Sprintf("/v1/milestones/%d", id) }
	listPath := "/v1/projects/" + p.Key + "/milestones"

	t.Run("shape and list order", func(t *testing.T) {
		if v2.Project != p.Key || v2.Name != "v0.2.0" || v2.Description != "운영 안정화와 릴리즈 관리" || v2.TargetDate == nil || *v2.TargetDate != "2026-11-01" ||
			v2.Status != "open" || v2.ClosedAt != nil || v2.Progress != (milestoneProgress{}) || v2.CreatedBy != "owner" || v2.UpdatedBy != "owner" {
			t.Errorf("v2 = %+v", v2)
		}
		wantTime(t, "created_at", v2.CreatedAt)
		wantTime(t, "updated_at", v2.UpdatedAt)
		if v3.TargetDate != nil || v3.Description != "" || v3.CreatedBy != "claude" {
			t.Errorf("v3 = %+v", v3)
		}
		got := decodeAs[milestoneList](t, owner.do(t, "GET", listPath, nil), 200)
		if milestoneNames(got.Items) != "v0.1.5,v0.2.0,v0.3.0" {
			t.Errorf("list = %s", milestoneNames(got.Items))
		}
		single := decodeAs[milestone](t, owner.do(t, "GET", milestonePath(v2.ID), nil), 200)
		if !reflect.DeepEqual(single, v2) {
			t.Errorf("single = %+v, created = %+v", single, v2)
		}
		wantError(t, owner.do(t, "GET", listPath+"?status=bogus", nil), 400, "bad_request")
		wantError(t, owner.do(t, "GET", "/v1/projects/"+projectKey("NOPE")+"/milestones", nil), 404, "not_found")
		wantError(t, owner.do(t, "GET", milestonePath(99999999), nil), 404, "not_found")
		wantError(t, owner.do(t, "PUT", listPath, map[string]any{}), 405, "method_not_allowed")
	})
	t.Run("rules", func(t *testing.T) {
		post := func(body map[string]any) response { return owner.do(t, "POST", listPath, body) }
		for _, body := range []map[string]any{{"name": ""}, {"name": strings.Repeat("가", 51)}, {}} {
			if e := wantError(t, post(body), 422, "validation_failed"); !e.hasField("name") {
				t.Errorf("%v: %+v", body, e.Error.Details)
			}
		}
		if e := wantError(t, post(map[string]any{"name": "x", "description": strings.Repeat("가", 2001)}), 422, "validation_failed"); !e.hasField("description") {
			t.Errorf("description: %+v", e.Error.Details)
		}
		for _, date := range []string{"", "2026-13-01", "2026-02-30", "26-11-01", "내일"} {
			if e := wantError(t, post(map[string]any{"name": "x", "target_date": date}), 422, "validation_failed"); !e.hasField("target_date") {
				t.Errorf("date %q: %+v", date, e.Error.Details)
			}
		}
		if e := wantError(t, post(map[string]any{"name": "v0.2.0"}), 409, "conflict"); !e.hasReason("duplicate") || !e.hasField("name") {
			t.Errorf("duplicate: %+v", e.Error.Details)
		}
		wantError(t, post(map[string]any{"name": "x", "bogus": 1}), 400, "bad_request")
		wantError(t, post(map[string]any{"name": "x", "status": "closed"}), 400, "bad_request")
		wantError(t, owner.do(t, "POST", "/v1/projects/"+projectKey("NOPE")+"/milestones", map[string]any{"name": "x"}), 404, "not_found")
	})
	t.Run("patch, close, delete and restore", func(t *testing.T) {
		edited := decodeAs[milestone](t, owner.do(t, "PATCH", milestonePath(v3.ID), map[string]any{"name": "v0.3.1", "description": "설명", "target_date": "2026-12-01"}), 200)
		if edited.Name != "v0.3.1" || edited.Description != "설명" || edited.TargetDate == nil || *edited.TargetDate != "2026-12-01" || edited.UpdatedBy != "owner" || edited.CreatedBy != "claude" {
			t.Errorf("edited = %+v", edited)
		}
		cleared := decodeAs[milestone](t, owner.do(t, "PATCH", milestonePath(v3.ID), map[string]any{"target_date": nil}), 200)
		if cleared.TargetDate != nil {
			t.Errorf("cleared = %+v", cleared)
		}
		for _, body := range []map[string]any{{"name": nil}, {"description": nil}, {"status": nil}, {"status": "gone"}, {"target_date": "soon"}} {
			wantError(t, owner.do(t, "PATCH", milestonePath(v3.ID), body), 422, "validation_failed")
		}
		wantError(t, owner.do(t, "PATCH", milestonePath(v3.ID), map[string]any{"progress": map[string]any{"total": 1}}), 400, "bad_request")
		if e := wantError(t, owner.do(t, "PATCH", milestonePath(v3.ID), map[string]any{"name": "v0.2.0"}), 409, "conflict"); !e.hasReason("duplicate") {
			t.Errorf("rename into a taken name: %+v", e.Error.Details)
		}

		closed := decodeAs[milestone](t, owner.do(t, "PATCH", milestonePath(v1.ID), map[string]any{"status": "closed"}), 200)
		if closed.Status != "closed" || closed.ClosedAt == nil || *closed.ClosedAt != closed.UpdatedAt {
			t.Errorf("closed = %+v", closed)
		}
		wantTime(t, "closed_at", *closed.ClosedAt)
		openList := decodeAs[milestoneList](t, owner.do(t, "GET", listPath, nil), 200)
		closedList := decodeAs[milestoneList](t, owner.do(t, "GET", listPath+"?status=closed", nil), 200)
		if milestoneNames(openList.Items) != "v0.2.0,v0.3.1" || milestoneNames(closedList.Items) != "v0.1.5" {
			t.Errorf("open %s, closed %s", milestoneNames(openList.Items), milestoneNames(closedList.Items))
		}
		reopened := decodeAs[milestone](t, owner.do(t, "PATCH", milestonePath(v1.ID), map[string]any{"status": "open"}), 200)
		if reopened.Status != "open" || reopened.ClosedAt != nil {
			t.Errorf("reopened = %+v", reopened)
		}

		gone := decodeAs[milestone](t, owner.do(t, "DELETE", milestonePath(v1.ID), nil), 200)
		if gone.Status != "deleted" {
			t.Errorf("deleted = %+v", gone)
		}
		active := decodeAs[milestoneList](t, owner.do(t, "GET", listPath, nil), 200)
		all := decodeAs[milestoneList](t, owner.do(t, "GET", listPath+"?status=all", nil), 200)
		if milestoneNames(active.Items) != "v0.2.0,v0.3.1" || len(all.Items) != 3 {
			t.Errorf("active %s, all %d", milestoneNames(active.Items), len(all.Items))
		}
		wantError(t, owner.do(t, "DELETE", milestonePath(99999999), nil), 404, "not_found")
		wantError(t, owner.do(t, "POST", milestonePath(v1.ID), map[string]any{}), 405, "method_not_allowed")
		back := decodeAs[milestone](t, owner.do(t, "PATCH", milestonePath(v1.ID), map[string]any{"status": "open"}), 200)
		if back.Status != "open" {
			t.Errorf("restored = %+v", back)
		}
	})
	t.Run("progress follows the issues", func(t *testing.T) {
		todo := newIssue(t, p.Key, "할 일", map[string]any{"status": "todo", "milestone": v2.ID})
		doing := newIssue(t, p.Key, "진행", map[string]any{"status": "doing", "milestone": v2.ID})
		newIssue(t, p.Key, "완료", map[string]any{"status": "done", "milestone": v2.ID})
		dropped := newIssue(t, p.Key, "취소할 것", map[string]any{"milestone": v2.ID})
		progress := func() milestoneProgress {
			return decodeAs[milestone](t, owner.do(t, "GET", milestonePath(v2.ID), nil), 200).Progress
		}
		if got := progress(); got != (milestoneProgress{Total: 4, Done: 1}) {
			t.Errorf("progress = %+v", got)
		}
		patchIssue(t, owner, todo.Key, statusPatch("done"))
		patchIssue(t, owner, dropped.Key, statusPatch("cancelled"))
		if got := progress(); got != (milestoneProgress{Total: 3, Done: 2}) {
			t.Errorf("progress after finishing one and cancelling one = %+v", got)
		}
		deleteIssue(t, owner, doing.Key)
		if got := progress(); got != (milestoneProgress{Total: 2, Done: 2}) {
			t.Errorf("progress after deleting one = %+v", got)
		}
		list := decodeAs[milestoneList](t, owner.do(t, "GET", listPath, nil), 200)
		for _, it := range list.Items {
			if it.ID == v2.ID && it.Progress != (milestoneProgress{Total: 2, Done: 2}) {
				t.Errorf("list progress = %+v", it.Progress)
			}
		}
	})
	t.Run("issues carry a milestone", func(t *testing.T) {
		onV3 := newIssue(t, p.Key, "v0.3.1 이슈", map[string]any{"milestone": v3.ID})
		plain := newIssue(t, p.Key, "마일스톤 없음", nil)
		if onV3.Milestone == nil || *onV3.Milestone != v3.ID || onV3.MilestoneName == nil || *onV3.MilestoneName != "v0.3.1" || plain.Milestone != nil || plain.MilestoneName != nil {
			t.Errorf("milestone fields = %+v / %+v", onV3, plain)
		}
		wantError(t, owner.do(t, "POST", "/v1/issues", map[string]any{"project": p.Key, "title": "x", "milestone": foreign.ID}), 422, "validation_failed")
		wantError(t, owner.do(t, "POST", "/v1/issues", map[string]any{"project": p.Key, "title": "x", "milestone": 99999999}), 422, "validation_failed")
		got := patchIssue(t, owner, plain.Key, map[string]any{"milestone": v1.ID})
		if got.MilestoneName == nil || *got.MilestoneName != "v0.1.5" {
			t.Errorf("patched = %+v", got)
		}
		if e := wantError(t, owner.do(t, "PATCH", "/v1/issues/"+plain.Key, map[string]any{"milestone": foreign.ID}), 422, "validation_failed"); !e.hasField("milestone") {
			t.Errorf("foreign milestone: %+v", e.Error.Details)
		}
		cleared := patchIssue(t, owner, plain.Key, map[string]any{"milestone": nil})
		if cleared.Milestone != nil || cleared.MilestoneName != nil {
			t.Errorf("cleared = %+v", cleared)
		}
		get := func(query string) []string {
			l := decodeAs[struct {
				Items []issue `json:"items"`
			}](t, owner.do(t, "GET", "/v1/issues?status=all&sort=key&project="+p.Key+"&"+query, nil), 200)
			return keys(l.Items)
		}
		if got := get(fmt.Sprintf("milestone=%d", v3.ID)); !equalStrings(got, []string{onV3.Key}) {
			t.Errorf("milestone filter = %v", got)
		}
		if got := get("milestone=none"); !equalStrings(got, []string{plain.Key}) {
			t.Errorf("none filter = %v", got)
		}
		if got := get(fmt.Sprintf("milestone=%d", v2.ID)); len(got) != 4 {
			t.Errorf("v2 filter = %v", got)
		}
		wantError(t, owner.do(t, "GET", "/v1/issues?milestone=abc", nil), 400, "bad_request")

		// A deleted milestone keeps its issues and refuses new ones.
		decodeAs[milestone](t, owner.do(t, "DELETE", milestonePath(v3.ID), nil), 200)
		kept := getIssue(t, onV3.Key)
		if kept.Milestone == nil || *kept.Milestone != v3.ID {
			t.Errorf("issue of a deleted milestone = %+v", kept)
		}
		wantError(t, owner.do(t, "PATCH", "/v1/issues/"+plain.Key, map[string]any{"milestone": v3.ID}), 422, "validation_failed")

		// The activity record lists the milestone change.
		acts := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/activity?field=milestone&kind=updated&project="+p.Key, nil), 200)
		if acts.Total != 2 {
			t.Errorf("activity records with a milestone change = %d, want 2", acts.Total)
		}
		created := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/issues/"+onV3.Key+"/activity", nil), 200)
		if created.Total != 1 || created.Items[0].Changes == nil {
			t.Fatalf("created record = %+v", created)
		}
		if c, ok := (*created.Items[0].Changes)["milestone"]; !ok || string(c.To) != fmt.Sprint(v3.ID) {
			t.Errorf("created record lacks the milestone: %+v", *created.Items[0].Changes)
		}
	})
}
