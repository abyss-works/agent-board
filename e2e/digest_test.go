//go:build e2e

package e2e

import (
	"net/url"
	"testing"
)

func TestDigest(t *testing.T) {
	p := createProject(t, "DG")
	tick()
	i := newIssue(t, p.Key, "요약할 일", nil)
	tick()
	patchIssue(t, owner, i.Key, map[string]any{"status": "done"})

	type event struct {
		Issue string  `json:"issue"`
		From  *string `json:"from"`
		To    *string `json:"to"`
	}
	type digest struct {
		Since    string `json:"since"`
		Until    string `json:"until"`
		Projects []struct {
			Project  project        `json:"project"`
			Activity int            `json:"activity"`
			Created  section[event] `json:"created"`
			Status   section[event] `json:"status_changes"`
		} `json:"projects"`
	}
	path := "/v1/digest?project=" + p.Key + "&since=" + url.QueryEscape(p.CreatedAt)
	d := decodeAs[digest](t, owner.do(t, "GET", path, nil), 200)
	wantTime(t, "until", d.Until)
	if len(d.Projects) != 1 || d.Projects[0].Project.Key != p.Key {
		t.Fatalf("projects = %+v", d.Projects)
	}
	got := d.Projects[0]
	if got.Activity != 2 || got.Created.Total != 1 || got.Created.Items[0].Issue != i.Key {
		t.Errorf("created = %+v (activity %d)", got.Created, got.Activity)
	}
	if got.Status.Total != 1 || *got.Status.Items[0].From != "backlog" || *got.Status.Items[0].To != "done" {
		t.Errorf("status = %+v", got.Status)
	}
	wantError(t, owner.do(t, "GET", "/v1/digest?project="+p.Key, nil), 400, "bad_request")
}
