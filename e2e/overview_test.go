//go:build e2e

package e2e

import (
	"reflect"
	"testing"
)

func TestOverview(t *testing.T) {
	p := createProject(t, "OV")
	other := createProject(t, "OV2")
	// Ten issues; the comments say which overview sections they land in.
	i1 := newIssue(t, p.Key, "I1 todo P1 feature", map[string]any{"status": "todo", "priority": "P1"}) // next
	tick()
	i2 := newIssue(t, p.Key, "I2 todo P0 bug after I1", map[string]any{"status": "todo", "priority": "P0", "type": "bug", "depends_on": strs(i1.Key)}) // blocked
	tick()
	i3 := newIssue(t, p.Key, "I3 doing P2 design", map[string]any{"status": "doing", "type": "design"}) // doing
	tick()
	i4 := newIssue(t, p.Key, "I4 review P1 decision", map[string]any{"status": "review", "priority": "P1", "type": "decision"}) // review, decisions
	tick()
	i5 := newIssue(t, p.Key, "I5 backlog P3 decision", map[string]any{"priority": "P3", "type": "decision"}) // decisions
	tick()
	i6 := newIssue(t, p.Key, "I6 done P2 feature", map[string]any{"status": "done"})
	tick()
	i7 := newIssue(t, p.Key, "I7 cancelled P1 idea", map[string]any{"status": "cancelled", "priority": "P1", "type": "idea"})
	tick()
	i8 := newIssue(t, p.Key, "I8 deleted P2 feature", nil)
	tick()
	i9 := newIssue(t, p.Key, "I9 todo P2 observation after I6", map[string]any{"status": "todo", "type": "observation", "recheck_condition": "I6 뒤 재검토", "depends_on": strs(i6.Key)}) // next (I6 is closed)
	tick()
	i10 := newIssue(t, p.Key, "I10 backlog P0 bug after I2", map[string]any{"priority": "P0", "type": "bug", "depends_on": strs(i2.Key)}) // blocked
	tick()
	deleteIssue(t, owner, i8.Key)
	tick()
	comment := decodeAs[activity](t, as("claude").do(t, "POST", "/v1/issues/"+i3.Key+"/comments", map[string]any{"body": "진행 중"}), 201)
	// Another project's issue depending on ours must not leak in.
	newIssue(t, other.Key, "other todo", map[string]any{"status": "todo", "depends_on": strs(i1.Key)})
	_ = i7

	check := func(t *testing.T, o overview, status string) {
		t.Helper()
		if o.Project.Key != p.Key || o.Project.Name != "e2e "+p.Key || o.Project.Status != status {
			t.Errorf("project = %+v", o.Project)
		}
		wantTime(t, "generated_at", o.GeneratedAt)
		if o.Counts.Total != 10 || o.Counts.Open != 7 {
			t.Errorf("counts total %d open %d", o.Counts.Total, o.Counts.Open)
		}
		wantStatus := map[string]int{"backlog": 2, "todo": 3, "doing": 1, "review": 1, "done": 1, "cancelled": 1, "deleted": 1}
		if !reflect.DeepEqual(o.Counts.ByStatus, wantStatus) {
			t.Errorf("by_status = %v, want %v", o.Counts.ByStatus, wantStatus)
		}
		wantType := map[string]int{"feature": 1, "bug": 2, "design": 1, "decision": 2, "idea": 0, "observation": 1}
		if !reflect.DeepEqual(o.Counts.ByType, wantType) {
			t.Errorf("by_type = %v, want %v", o.Counts.ByType, wantType)
		}
		wantPriority := map[string]int{"P0": 2, "P1": 2, "P2": 2, "P3": 1}
		if !reflect.DeepEqual(o.Counts.ByPriority, wantPriority) {
			t.Errorf("by_priority = %v, want %v", o.Counts.ByPriority, wantPriority)
		}
		if o.Doing.Total != 1 {
			t.Errorf("doing total = %d", o.Doing.Total)
		}
		wantKeys(t, o.Doing.Items, i3.Key)
		if o.Review.Total != 1 {
			t.Errorf("review total = %d", o.Review.Total)
		}
		wantKeys(t, o.Review.Items, i4.Key)
		if o.Blocked.Total != 2 {
			t.Errorf("blocked total = %d", o.Blocked.Total)
		}
		wantKeys(t, o.Blocked.Items, i2.Key, i10.Key)
		if len(o.Blocked.Items) == 2 {
			b2, b10 := o.Blocked.Items[0], o.Blocked.Items[1]
			if b2.Ready == nil || *b2.Ready || !equalStrings(b2.BlockedBy, []string{i1.Key}) {
				t.Errorf("blocked I2 = ready %v blocked_by %v", b2.Ready, b2.BlockedBy)
			}
			if b10.Ready == nil || *b10.Ready || !equalStrings(b10.BlockedBy, []string{i2.Key}) {
				t.Errorf("blocked I10 = ready %v blocked_by %v", b10.Ready, b10.BlockedBy)
			}
		}
		if o.Decisions.Total != 2 {
			t.Errorf("decisions total = %d", o.Decisions.Total)
		}
		wantKeys(t, o.Decisions.Items, i4.Key, i5.Key)
		if o.Next.Total != 2 {
			t.Errorf("next total = %d", o.Next.Total)
		}
		wantKeys(t, o.Next.Items, i1.Key, i9.Key)
		for _, it := range o.Next.Items {
			if it.Ready == nil || !*it.Ready || it.BlockedBy == nil || len(it.BlockedBy) != 0 {
				t.Errorf("next %s = ready %v blocked_by %v", it.Key, it.Ready, it.BlockedBy)
			}
		}
		for _, sec := range [][]issue{o.Doing.Items, o.Review.Items, o.Blocked.Items, o.Decisions.Items, o.Next.Items} {
			for _, it := range sec {
				if it.Body != nil {
					t.Errorf("%s carries a body; sections hold summaries", it.Key)
				}
				if it.Project != p.Key {
					t.Errorf("%s belongs to %s", it.Key, it.Project)
				}
			}
		}
		if o.RecentActivity.Total != 12 {
			t.Errorf("recent_activity total = %d, want 10 creates + 1 delete + 1 comment", o.RecentActivity.Total)
		}
		for i, a := range o.RecentActivity.Items {
			if a.Project != p.Key {
				t.Errorf("recent_activity[%d] belongs to %s", i, a.Project)
			}
			if i > 0 && o.RecentActivity.Items[i-1].At < a.At {
				t.Errorf("recent_activity not newest first at %d", i)
			}
		}
	}

	t.Run("default limit 10", func(t *testing.T) {
		o := decodeAs[overview](t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/overview", nil), 200)
		check(t, o, "active")
		if len(o.RecentActivity.Items) != 10 {
			t.Errorf("recent_activity items = %d", len(o.RecentActivity.Items))
		}
		if len(o.RecentActivity.Items) > 1 {
			first, second := o.RecentActivity.Items[0], o.RecentActivity.Items[1]
			if first.ID != comment.ID || first.Kind != "commented" || first.Actor != "claude" || first.SessionID != sessionID || first.ClientHost != defaultHost {
				t.Errorf("recent_activity[0] = %+v", first)
			}
			if second.Kind != "updated" || second.Issue != i8.Key {
				t.Errorf("recent_activity[1] = %+v", second)
			}
		}
	})
	t.Run("limit caps every section but not the totals", func(t *testing.T) {
		o := decodeAs[overview](t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/overview?limit=1", nil), 200)
		if o.Blocked.Total != 2 || len(o.Blocked.Items) != 1 || o.Blocked.Items[0].Key != i2.Key {
			t.Errorf("blocked = total %d items %v", o.Blocked.Total, keys(o.Blocked.Items))
		}
		if o.Decisions.Total != 2 || len(o.Decisions.Items) != 1 || o.Decisions.Items[0].Key != i4.Key {
			t.Errorf("decisions = total %d items %v", o.Decisions.Total, keys(o.Decisions.Items))
		}
		if o.Next.Total != 2 || len(o.Next.Items) != 1 || o.Next.Items[0].Key != i1.Key {
			t.Errorf("next = total %d items %v", o.Next.Total, keys(o.Next.Items))
		}
		if o.RecentActivity.Total != 12 || len(o.RecentActivity.Items) != 1 {
			t.Errorf("recent_activity = total %d items %d", o.RecentActivity.Total, len(o.RecentActivity.Items))
		}
		o = decodeAs[overview](t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/overview?limit=50", nil), 200)
		if len(o.RecentActivity.Items) != 12 {
			t.Errorf("limit=50 recent_activity items = %d", len(o.RecentActivity.Items))
		}
	})
	t.Run("limit outside 1..50 and unknown parameters are 400", func(t *testing.T) {
		for _, q := range []string{"limit=0", "limit=51", "limit=x", "offset=1", "project=X"} {
			wantError(t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/overview?"+q, nil), 400, "bad_request")
		}
	})
	t.Run("archived and deleted projects are computed the same way", func(t *testing.T) {
		decodeAs[project](t, owner.do(t, "PATCH", "/v1/projects/"+p.Key, map[string]any{"status": "archived"}), 200)
		check(t, decodeAs[overview](t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/overview", nil), 200), "archived")
		decodeAs[project](t, owner.do(t, "DELETE", "/v1/projects/"+p.Key, nil), 200)
		check(t, decodeAs[overview](t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/overview", nil), 200), "deleted")
	})
	t.Run("sections follow the queue when a dependency closes", func(t *testing.T) {
		decodeAs[project](t, owner.do(t, "PATCH", "/v1/projects/"+p.Key, map[string]any{"status": "active"}), 200)
		patchIssue(t, owner, i1.Key, map[string]any{"status": "done"})
		o := decodeAs[overview](t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/overview", nil), 200)
		wantKeys(t, o.Next.Items, i2.Key, i9.Key)
		wantKeys(t, o.Blocked.Items, i10.Key)
		if o.Counts.Open != 6 || o.Counts.ByStatus["done"] != 2 || o.Counts.ByType["feature"] != 0 || o.Counts.ByPriority["P1"] != 1 {
			t.Errorf("counts after closing I1 = %+v", o.Counts)
		}
		if o.RecentActivity.Total != 13 || o.RecentActivity.Items[0].Issue != i1.Key {
			t.Errorf("recent_activity = total %d first %+v", o.RecentActivity.Total, o.RecentActivity.Items[0])
		}
	})
	t.Run("empty project", func(t *testing.T) {
		e := createProject(t, "OV3")
		o := decodeAs[overview](t, owner.do(t, "GET", "/v1/projects/"+e.Key+"/overview", nil), 200)
		if o.Counts.Total != 0 || o.Counts.Open != 0 || len(o.Counts.ByStatus) != 7 || len(o.Counts.ByType) != 6 || len(o.Counts.ByPriority) != 4 {
			t.Errorf("empty counts = %+v", o.Counts)
		}
		for name, sec := range map[string]int{"doing": o.Doing.Total, "review": o.Review.Total, "blocked": o.Blocked.Total, "decisions": o.Decisions.Total, "next": o.Next.Total, "recent_activity": o.RecentActivity.Total} {
			if sec != 0 {
				t.Errorf("%s total = %d", name, sec)
			}
		}
		if o.Doing.Items == nil || o.Blocked.Items == nil || o.Next.Items == nil || o.RecentActivity.Items == nil {
			t.Errorf("empty sections must be [] not null")
		}
	})
}
