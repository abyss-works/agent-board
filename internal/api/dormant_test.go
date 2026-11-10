package api

import (
	"testing"
	"time"

	"github.com/abyss-works/agent-board/internal/model"
)

func TestDormantFilterAndField(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	e.st.SetClock(func() time.Time { return now.Add(-40 * 24 * time.Hour) })
	e.createProject(t, "AGB", "agent-board")
	e.createIssue(t, `{"project":"AGB","title":"old"}`)
	e.createIssue(t, `{"project":"AGB","title":"old but done","status":"done"}`)
	e.st.SetClock(func() time.Time { return now })
	e.createIssue(t, `{"project":"AGB","title":"new"}`)

	list := func(q string) listEnvelope[model.IssueSummary] {
		t.Helper()
		rec := e.do(t, "GET", "/v1/issues?status=all&sort=key&"+q, "", nil)
		if rec.Code != 200 {
			t.Fatalf("list %s: %d %s", q, rec.Code, rec.Body.String())
		}
		var l listEnvelope[model.IssueSummary]
		decodeJSON(t, rec, &l)
		return l
	}
	l := list("dormant=true")
	if l.Total != 1 || len(l.Items) != 1 || l.Items[0].Key != "AGB-1" || !l.Items[0].Dormant {
		t.Errorf("dormant=true: %+v", l)
	}
	if l := list(""); l.Total != 3 || !l.Items[0].Dormant || l.Items[1].Dormant || l.Items[2].Dormant {
		t.Errorf("no filter: %+v", l)
	}
	for _, q := range []string{"dormant=false", "dormant=yes", "dormant=TRUE", "dormant=1"} {
		if rec := e.do(t, "GET", "/v1/issues?"+q, "", nil); rec.Code != 400 {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
	rec := e.do(t, "GET", "/v1/issues/AGB-1", "", nil)
	var one model.Issue
	decodeJSON(t, rec, &one)
	if rec.Code != 200 || !one.Dormant {
		t.Errorf("single issue: %d dormant=%v", rec.Code, one.Dormant)
	}
}
