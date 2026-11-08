package api

import (
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func TestProjectActivity(t *testing.T) {
	e := newEnv(t)
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")
	m := e.createMilestone(t, "AGB", `{"name":"v1"}`)
	if rec := e.do(t, "PATCH", milestonePath(m.ID), `{"description":"설명"}`, nil); rec.Code != 200 {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}

	rec := e.do(t, "GET", "/v1/projects/AGB/activity", "", nil)
	var l struct {
		Items []model.ResourceActivity `json:"items"`
		Total int                      `json:"total"`
	}
	decodeJSON(t, rec, &l)
	if rec.Code != 200 || l.Total != 3 || len(l.Items) != 3 {
		t.Fatalf("activity: %d %s", rec.Code, rec.Body.String())
	}
	// Newest first, with the trace of the request.
	u := l.Items[0]
	if u.Resource != model.ResourceMilestone || u.ResourceID != m.ID || u.Kind != model.ResourceUpdated || u.Actor != "claude" || u.Project != "AGB" ||
		u.SessionID == "" || u.ClientHost == "" || string(u.Changes["description"].From) != `""` || string(u.Changes["description"].To) != `"설명"` {
		t.Errorf("update record = %+v", u)
	}
	if l.Items[2].Resource != model.ResourceProject || l.Items[2].Kind != model.ResourceCreated {
		t.Errorf("first record = %+v", l.Items[2])
	}

	// Filters and paging.
	for q, want := range map[string]int{"?resource=milestone": 2, "?resource=project": 1, "?resource=topic": 0, "?resource=milestone&id=999": 0} {
		rec := e.do(t, "GET", "/v1/projects/AGB/activity"+q, "", nil)
		if !strings.Contains(rec.Body.String(), `"total":`+string(rune('0'+want))) {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body.String())
		}
	}
	if rec := e.do(t, "GET", "/v1/projects/AGB/activity?limit=1&offset=1", "", nil); !strings.Contains(rec.Body.String(), `"total":3`) || !strings.Contains(rec.Body.String(), `"limit":1`) {
		t.Errorf("paged: %s", rec.Body.String())
	}
	if rec := e.do(t, "GET", "/v1/projects/OTH/activity", "", nil); !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Errorf("other project: %s", rec.Body.String())
	}

	// Bad input and a missing project.
	for _, q := range []string{"?resource=issue", "?id=3", "?id=x", "?id=0", "?limit=0", "?bogus=1"} {
		if rec := e.do(t, "GET", "/v1/projects/AGB/activity"+q, "", nil); rec.Code != 400 {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body.String())
		}
	}
	if rec := e.do(t, "GET", "/v1/projects/NOPE/activity", "", nil); rec.Code != 404 {
		t.Errorf("missing project: %d", rec.Code)
	}
}
