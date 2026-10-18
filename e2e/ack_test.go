//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"testing"
)

func TestIssueAcknowledge(t *testing.T) {
	p := createProject(t, "AK")
	byAgent := createIssue(t, as("claude"), map[string]any{"project": p.Key, "title": "agent made"})
	byUser := newIssue(t, p.Key, "user made", nil)

	t.Run("initial state follows the actor kind", func(t *testing.T) {
		if byAgent.AcknowledgedAt != nil || byAgent.AcknowledgedBy != nil {
			t.Errorf("agent made = %v / %v, want unacknowledged", byAgent.AcknowledgedAt, byAgent.AcknowledgedBy)
		}
		if byUser.AcknowledgedAt == nil || *byUser.AcknowledgedAt != byUser.CreatedAt || byUser.AcknowledgedBy == nil || *byUser.AcknowledgedBy != "owner" {
			t.Errorf("user made = %v / %v, want acknowledged at creation", byUser.AcknowledgedAt, byUser.AcknowledgedBy)
		}
	})
	path := "/v1/issues/" + byAgent.Key + "/acknowledge"
	t.Run("acknowledge and unacknowledge", func(t *testing.T) {
		r := owner.do(t, "POST", path, nil)
		got := decodeAs[issue](t, r, 200)
		if got.AcknowledgedAt == nil || got.AcknowledgedBy == nil || *got.AcknowledgedBy != "owner" || got.Version != byAgent.Version+1 {
			t.Fatalf("acknowledged = %+v", got)
		}
		if r.header.Get("ETag") != fmt.Sprintf(`"%d"`, got.Version) {
			t.Errorf("ETag = %q", r.header.Get("ETag"))
		}
		again := decodeAs[issue](t, owner.do(t, "POST", path, nil), 200)
		if again.Version != got.Version {
			t.Errorf("a repeated acknowledge changed the version: %d -> %d", got.Version, again.Version)
		}
		acts := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/issues/"+byAgent.Key+"/activity", nil), 200)
		ch := (*acts.Items[0].Changes)["acknowledged"]
		if acts.Total != 2 || acts.Items[0].Kind != "updated" || string(ch.From) != "false" || string(ch.To) != "true" {
			t.Errorf("activity = total %d %+v", acts.Total, acts.Items[0])
		}
		cleared := decodeAs[issue](t, owner.do(t, "DELETE", path, nil), 200)
		if cleared.AcknowledgedAt != nil || cleared.AcknowledgedBy != nil || cleared.Version != got.Version+1 {
			t.Errorf("unacknowledged = %+v", cleared)
		}
	})
	t.Run("If-Match", func(t *testing.T) {
		cur := getIssue(t, byAgent.Key)
		wantError(t, owner.do(t, "POST", path, nil, http.Header{"If-Match": {fmt.Sprintf(`"%d"`, cur.Version-1)}}), 412, "precondition_failed")
		decodeAs[issue](t, owner.do(t, "POST", path, nil, http.Header{"If-Match": {fmt.Sprintf(`"%d"`, cur.Version)}}), 200)
	})
	t.Run("the mark does not touch the workflow", func(t *testing.T) {
		got := patchIssue(t, as("claude"), byAgent.Key, map[string]any{"status": "doing"})
		if got.AcknowledgedAt == nil {
			t.Errorf("a later change must keep the mark: %+v", got)
		}
		unmarked := createIssue(t, as("claude"), map[string]any{"project": p.Key, "title": "still unacknowledged"})
		moved := patchIssue(t, as("claude"), unmarked.Key, map[string]any{"status": "todo"})
		if moved.Status != "todo" || moved.AcknowledgedAt != nil {
			t.Errorf("status change of an unacknowledged issue = %+v", moved)
		}
	})
	t.Run("list filter and overview section", func(t *testing.T) {
		q := "/v1/issues?status=all&sort=key&project=" + p.Key
		yes := decodeAs[list[issue]](t, owner.do(t, "GET", q+"&acknowledged=true", nil), 200)
		no := decodeAs[list[issue]](t, owner.do(t, "GET", q+"&acknowledged=false", nil), 200)
		if yes.Total != 2 || no.Total != 1 {
			t.Errorf("acknowledged true/false = %d/%d, want 2/1", yes.Total, no.Total)
		}
		wantError(t, owner.do(t, "GET", q+"&acknowledged=maybe", nil), 400, "bad_request")
		o := decodeAs[overview](t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/overview", nil), 200)
		if o.Unacknowledged.Total != 1 || len(o.Unacknowledged.Items) != 1 || o.Unacknowledged.Items[0].AcknowledgedAt != nil {
			t.Errorf("unacknowledged section = %+v", o.Unacknowledged)
		}
	})
	t.Run("unknown issue is 404", func(t *testing.T) {
		wantError(t, owner.do(t, "POST", "/v1/issues/"+p.Key+"-999/acknowledge", nil), 404, "not_found")
	})
}
