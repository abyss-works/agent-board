//go:build e2e

package e2e

import (
	"os"
	"testing"
)

// TestPersistence checks that data written before a server restart is still
// served after it. Run it twice: with AGB_E2E_PERSIST_PHASE=write before
// stopping the server, then with AGB_E2E_PERSIST_PHASE=verify after the
// server is started again on the same data directory. AGB_E2E_PERSIST_KEY
// is the project key both runs share and must not exist before the write.
func TestPersistence(t *testing.T) {
	key, phase := os.Getenv(envPersistKey), os.Getenv(envPersistPhase)
	if key == "" || phase == "" {
		t.Skipf("set %s and %s=write|verify", envPersistKey, envPersistPhase)
	}
	c := caller{actor: "hermes", session: "persist-" + key, host: "desk-win10"}
	switch phase {
	case "write":
		p := decodeAs[project](t, c.do(t, "POST", "/v1/projects", map[string]any{"key": key, "name": "재시작 검증"}), 201)
		if p.Key != key {
			t.Fatalf("project = %+v", p)
		}
		iss := createIssue(t, c, map[string]any{"project": key, "title": "재시작 전 이슈", "body": "본문", "labels": strs("persist"), "status": "todo"})
		if iss.Key != key+"-1" {
			t.Fatalf("issue = %s", iss.Key)
		}
		patchIssue(t, c, iss.Key, map[string]any{"priority": "P0"})
		expect(t, c.do(t, "POST", "/v1/issues/"+iss.Key+"/comments", map[string]any{"body": "재시작 전 댓글"}), 201)
		t.Logf("wrote project %s with issue %s", key, iss.Key)
	case "verify":
		p := decodeAs[project](t, c.do(t, "GET", "/v1/projects/"+key, nil), 200)
		if p.Name != "재시작 검증" || p.Status != "active" || p.CreatedBy != "hermes" {
			t.Errorf("project = %+v", p)
		}
		iss := getIssue(t, key+"-1")
		if iss.Title != "재시작 전 이슈" || iss.Body == nil || *iss.Body != "본문" || !equalStrings(iss.Labels, []string{"persist"}) || iss.Status != "todo" || iss.Priority != "P0" || iss.Version != 2 {
			t.Errorf("issue = %+v", iss)
		}
		acts := decodeAs[list[activity]](t, c.do(t, "GET", "/v1/activity?session=persist-"+key+"&sort=at", nil), 200)
		if acts.Total != 3 || len(acts.Items) != 3 {
			t.Fatalf("activity = %+v", acts)
		}
		kinds := []string{acts.Items[0].Kind, acts.Items[1].Kind, acts.Items[2].Kind}
		if !equalStrings(kinds, []string{"created", "updated", "commented"}) {
			t.Errorf("kinds = %v", kinds)
		}
		if acts.Items[2].Body == nil || *acts.Items[2].Body != "재시작 전 댓글" {
			t.Errorf("comment = %+v", acts.Items[2])
		}
		found := decodeAs[list[issue]](t, c.do(t, "GET", "/v1/issues?project="+key+"&q=재시작", nil), 200)
		wantKeys(t, found.Items, key+"-1")
		o := decodeAs[overview](t, c.do(t, "GET", "/v1/projects/"+key+"/overview", nil), 200)
		if o.Counts.Total != 1 || o.Counts.Open != 1 || o.Next.Total != 1 || o.RecentActivity.Total != 3 {
			t.Errorf("overview = %+v", o.Counts)
		}
	default:
		t.Fatalf("%s must be write or verify, got %q", envPersistPhase, phase)
	}
}
