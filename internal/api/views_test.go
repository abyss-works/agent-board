package api

import (
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func seedViews(t *testing.T, e *env) {
	t.Helper()
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")
	e.createIssue(t, `{"project":"AGB","title":"dep","status":"doing","priority":"P1","labels":["api"]}`)
	e.createIssue(t, `{"project":"AGB","title":"blocked","status":"todo","depends_on":["AGB-1"],"labels":["api","cli"]}`)
	e.createIssue(t, `{"project":"AGB","title":"ready","status":"todo","priority":"P0"}`)
	e.createIssue(t, `{"project":"AGB","title":"decide","type":"decision"}`)
	e.createIssue(t, `{"project":"AGB","title":"done","status":"done","labels":["old"]}`)
	e.createIssue(t, `{"project":"AGB","title":"gone","status":"deleted"}`)
	e.createIssue(t, `{"project":"OTH","title":"other","status":"review"}`)
}

func TestBoardEndpoint(t *testing.T) {
	e := newEnv(t)
	seedViews(t, e)
	rec := e.do(t, "GET", "/v1/board?project=AGB", "", nil)
	if rec.Code != 200 {
		t.Fatalf("board: %d %s", rec.Code, rec.Body.String())
	}
	var b store.Board
	decodeJSON(t, rec, &b)
	if b.Project == nil || *b.Project != "AGB" || len(b.Columns) != 7 {
		t.Fatalf("board = %+v", b)
	}
	if b.Columns[1].Status != model.StatusTodo || b.Columns[1].Total != 2 || b.Columns[1].Issues[0].Key != "AGB-3" {
		t.Errorf("todo column = %+v", b.Columns[1])
	}
	if b.Columns[6].Status != model.StatusDeleted || b.Columns[6].Total != 1 {
		t.Errorf("deleted column must be shown: %+v", b.Columns[6])
	}
	rec = e.do(t, "GET", "/v1/board", "", nil)
	decodeJSON(t, rec, &b)
	if b.Project != nil || b.Columns[3].Total != 1 {
		t.Errorf("all projects board = %+v", b)
	}
	rec = e.do(t, "GET", "/v1/board?project=AGB&closed_days=0&limit=1", "", nil)
	decodeJSON(t, rec, &b)
	if len(b.Columns[1].Issues) != 1 || b.Columns[1].Total != 2 {
		t.Errorf("limit = %+v", b.Columns[1])
	}
	for _, q := range []string{"closed_days=-1", "closed_days=x", "limit=201", "limit=0", "status=todo", "project=bad"} {
		if rec = e.do(t, "GET", "/v1/board?"+q, "", nil); rec.Code != 400 {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
}

func TestQueueEndpoint(t *testing.T) {
	e := newEnv(t)
	seedViews(t, e)
	rec := e.do(t, "GET", "/v1/queue?project=AGB", "", nil)
	if rec.Code != 200 {
		t.Fatalf("queue: %d %s", rec.Code, rec.Body.String())
	}
	var l listEnvelope[model.QueueItem]
	decodeJSON(t, rec, &l)
	if l.Total != 2 || l.Items[0].Key != "AGB-3" || !l.Items[0].Ready || l.Items[1].Key != "AGB-2" || l.Items[1].Ready || l.Items[1].BlockedBy[0] != "AGB-1" {
		t.Errorf("queue = %+v", l)
	}
	rec = e.do(t, "GET", "/v1/queue?project=AGB&limit=1", "", nil)
	decodeJSON(t, rec, &l)
	if len(l.Items) != 1 || l.Items[0].Key != "AGB-3" || l.Limit != 1 {
		t.Errorf("next = %+v", l)
	}
	rec = e.do(t, "GET", "/v1/queue?status=todo,backlog&project=AGB", "", nil)
	decodeJSON(t, rec, &l)
	if l.Total != 3 {
		t.Errorf("with backlog = %d", l.Total)
	}
	rec = e.do(t, "GET", "/v1/queue?status=all", "", nil)
	decodeJSON(t, rec, &l)
	if l.Total != 7 {
		t.Errorf("all = %d", l.Total)
	}
	for _, q := range []string{"status=bogus", "limit=0", "offset=-1", "sort=key", "label=x"} {
		if rec = e.do(t, "GET", "/v1/queue?"+q, "", nil); rec.Code != 400 {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
}

func TestLabelsEndpoint(t *testing.T) {
	e := newEnv(t)
	seedViews(t, e)
	rec := e.do(t, "GET", "/v1/labels", "", nil)
	if rec.Code != 200 {
		t.Fatalf("labels: %d %s", rec.Code, rec.Body.String())
	}
	var l struct {
		Items []store.LabelCount `json:"items"`
	}
	decodeJSON(t, rec, &l)
	if len(l.Items) != 2 || l.Items[0].Label != "api" || l.Items[0].Count != 2 || l.Items[1].Label != "cli" {
		t.Errorf("labels = %+v (closed issue labels excluded)", l.Items)
	}
	rec = e.do(t, "GET", "/v1/labels?project=OTH", "", nil)
	decodeJSON(t, rec, &l)
	if len(l.Items) != 0 || rec.Body.String() == "" || rec.Body.String()[:10] != `{"items":[` {
		t.Errorf("empty labels = %s", rec.Body.String())
	}
	if rec = e.do(t, "GET", "/v1/labels?status=open", "", nil); rec.Code != 400 {
		t.Errorf("unknown param: %d", rec.Code)
	}
}

func TestOverviewEndpoint(t *testing.T) {
	e := newEnv(t)
	seedViews(t, e)
	e.do(t, "POST", "/v1/issues/AGB-1/comments", `{"body":"note"}`, nil)
	rec := e.do(t, "GET", "/v1/projects/AGB/overview", "", nil)
	if rec.Code != 200 {
		t.Fatalf("overview: %d %s", rec.Code, rec.Body.String())
	}
	var o store.Overview
	decodeJSON(t, rec, &o)
	if o.Project.Key != "AGB" || o.Counts.Total != 6 || o.Counts.Open != 4 || o.Counts.ByStatus[model.StatusDeleted] != 1 || o.Counts.ByType[model.TypeDecision] != 1 || o.Counts.ByPriority[model.P0] != 1 {
		t.Errorf("counts = %+v", o.Counts)
	}
	if o.Doing.Total != 1 || o.Doing.Items[0].Key != "AGB-1" || o.Review.Total != 0 || o.Review.Items == nil {
		t.Errorf("doing/review = %+v / %+v", o.Doing, o.Review)
	}
	if o.Blocked.Total != 1 || o.Blocked.Items[0].Key != "AGB-2" || o.Blocked.Items[0].BlockedBy[0] != "AGB-1" {
		t.Errorf("blocked = %+v", o.Blocked)
	}
	if o.Decisions.Total != 1 || o.Decisions.Items[0].Key != "AGB-4" {
		t.Errorf("decisions = %+v", o.Decisions)
	}
	if o.Next.Total != 1 || o.Next.Items[0].Key != "AGB-3" {
		t.Errorf("next = %+v", o.Next)
	}
	if o.RecentActivity.Total != 7 || o.RecentActivity.Items[0].Kind != model.ActivityCommented {
		t.Errorf("recent_activity = total %d first %+v", o.RecentActivity.Total, o.RecentActivity.Items[0])
	}
	rec = e.do(t, "GET", "/v1/projects/AGB/overview?limit=2", "", nil)
	decodeJSON(t, rec, &o)
	if len(o.RecentActivity.Items) != 2 || o.RecentActivity.Total != 7 {
		t.Errorf("limit = %+v", o.RecentActivity)
	}
	if rec = e.do(t, "GET", "/v1/projects/NOPE/overview", "", nil); rec.Code != 404 {
		t.Errorf("missing project: %d", rec.Code)
	}
	for _, q := range []string{"limit=0", "limit=51", "x=1"} {
		if rec = e.do(t, "GET", "/v1/projects/AGB/overview?"+q, "", nil); rec.Code != 400 {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
	// Archived projects still have an overview.
	e.do(t, "PATCH", "/v1/projects/AGB", `{"status":"archived"}`, nil)
	if rec = e.do(t, "GET", "/v1/projects/AGB/overview", "", nil); rec.Code != 200 {
		t.Errorf("archived overview: %d", rec.Code)
	}
}
