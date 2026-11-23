package api

import (
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

func seedViews(t *testing.T, e *env) {
	t.Helper()
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")
	e.createIssue(t, `{"project":"AGB","title":"dep","status":"doing","priority":"P1","labels":["api"]}`)
	e.createIssue(t, `{"project":"AGB","title":"blocked","depends_on":["AGB-1"],"labels":["api","cli"]}`)
	e.createIssue(t, `{"project":"AGB","title":"ready","priority":"P0"}`)
	e.createIssue(t, `{"project":"AGB","title":"decide","decision_needed":"어느 쪽으로 할지"}`)
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
	if b.Project == nil || *b.Project != "AGB" || len(b.Columns) != 6 {
		t.Fatalf("board = %+v", b)
	}
	if b.Columns[0].Status != model.StatusBacklog || b.Columns[0].Total != 3 || b.Columns[0].Issues[0].Key != "AGB-3" {
		t.Errorf("backlog column = %+v", b.Columns[0])
	}
	// Board items carry decision_needed: set on AGB-4, null on AGB-2.
	backlog := b.Columns[0].Issues
	if backlog[1].Key != "AGB-2" || backlog[1].DecisionNeeded != nil || backlog[2].Key != "AGB-4" || backlog[2].DecisionNeeded == nil || *backlog[2].DecisionNeeded != "어느 쪽으로 할지" {
		t.Errorf("backlog column items = %+v", backlog)
	}
	if b.Columns[5].Status != model.StatusDeleted || b.Columns[5].Total != 1 {
		t.Errorf("deleted column must be shown: %+v", b.Columns[5])
	}
	rec = e.do(t, "GET", "/v1/board", "", nil)
	decodeJSON(t, rec, &b)
	if b.Project != nil || b.Columns[2].Total != 1 {
		t.Errorf("all projects board = %+v", b)
	}
	rec = e.do(t, "GET", "/v1/board?project=AGB&closed_days=0&limit=1", "", nil)
	decodeJSON(t, rec, &b)
	if len(b.Columns[0].Issues) != 1 || b.Columns[0].Total != 3 {
		t.Errorf("limit = %+v", b.Columns[0])
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
	if l.Total != 3 || l.Items[0].Key != "AGB-3" || !l.Items[0].Ready || l.Items[1].Key != "AGB-4" || !l.Items[1].Ready ||
		l.Items[2].Key != "AGB-2" || l.Items[2].Ready || l.Items[2].BlockedBy[0] != "AGB-1" {
		t.Errorf("queue = %+v", l)
	}
	if q := l.Items[1].DecisionNeeded; q == nil || *q != "어느 쪽으로 할지" || l.Items[0].DecisionNeeded != nil {
		t.Errorf("queue items carry decision_needed: %v and %v", l.Items[0].DecisionNeeded, q)
	}
	rec = e.do(t, "GET", "/v1/queue?project=AGB&limit=1", "", nil)
	decodeJSON(t, rec, &l)
	if len(l.Items) != 1 || l.Items[0].Key != "AGB-3" || l.Limit != 1 {
		t.Errorf("next = %+v", l)
	}
	rec = e.do(t, "GET", "/v1/queue?status=backlog,doing&project=AGB", "", nil)
	decodeJSON(t, rec, &l)
	if l.Total != 4 {
		t.Errorf("with doing = %d", l.Total)
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
	if o.Project.Key != "AGB" || o.Counts.Total != 6 || o.Counts.Open != 4 || o.Counts.ByStatus[model.StatusDeleted] != 1 || o.Counts.ByType[model.TypeFeature] != 4 || o.Counts.ByPriority[model.P0] != 1 ||
		len(o.Counts.ByStatus) != 6 || len(o.Counts.ByType) != 5 {
		t.Errorf("counts = %+v", o.Counts)
	}
	if o.Doing.Total != 1 || o.Doing.Items[0].Key != "AGB-1" || o.Review.Total != 0 || o.Review.Items == nil {
		t.Errorf("doing/review = %+v / %+v", o.Doing, o.Review)
	}
	if o.Blocked.Total != 1 || o.Blocked.Items[0].Key != "AGB-2" || o.Blocked.Items[0].BlockedBy[0] != "AGB-1" {
		t.Errorf("blocked = %+v", o.Blocked)
	}
	if o.Decisions.Total != 1 || o.Decisions.Items[0].Key != "AGB-4" || o.Decisions.Items[0].DecisionNeeded == nil || *o.Decisions.Items[0].DecisionNeeded != "어느 쪽으로 할지" {
		t.Errorf("decisions = %+v", o.Decisions)
	}
	if !strings.Contains(rec.Body.String(), `"waiting_sessions":[]`) {
		t.Errorf("overview decisions item without waiting sessions must serialize []: %s", rec.Body.String())
	}
	if o.Next.Total != 2 || o.Next.Items[0].Key != "AGB-3" || o.Next.Items[1].Key != "AGB-4" || o.Next.Items[1].DecisionNeeded == nil {
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

func TestAgendaEndpoint(t *testing.T) {
	e := newEnv(t)
	seedViews(t, e)
	rec := e.do(t, "GET", "/v1/agenda?project=AGB", "", nil)
	if rec.Code != 200 {
		t.Fatalf("agenda: %d %s", rec.Code, rec.Body.String())
	}
	var a store.Agenda
	decodeJSON(t, rec, &a)
	if len(a.Projects) != 1 || a.Projects[0].Project.Key != "AGB" {
		t.Fatalf("projects = %+v", a.Projects)
	}
	p := a.Projects[0]
	if p.Agent.Doing.Total != 1 || p.Agent.Next.Total != 2 || p.UserTurn.Decisions.Total != 1 || p.Waiting.Blocked.Total != 1 || p.Waiting.External.Items == nil {
		t.Errorf("agenda = %+v", p)
	}
	if rec = e.do(t, "GET", "/v1/agenda", "", nil); rec.Code != 200 {
		t.Errorf("all projects: %d", rec.Code)
	}
	if rec = e.do(t, "GET", "/v1/agenda?project=NOPE", "", nil); rec.Code != 404 {
		t.Errorf("missing project: %d", rec.Code)
	}
	for _, q := range []string{"limit=0", "limit=51", "x=1", "project=bad"} {
		if rec = e.do(t, "GET", "/v1/agenda?"+q, "", nil); rec.Code != 400 {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
}

func TestAgendaWaitingSessionsEndpoint(t *testing.T) {
	e, clock := newSessionEnv(t)
	owner := map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-1"}
	e.do(t, "POST", "/v1/projects", `{"key":"AAA","name":"A"}`, owner)
	e.do(t, "POST", "/v1/projects", `{"key":"BBB","name":"B"}`, owner)
	e.do(t, "POST", "/v1/issues", `{"project":"AAA","title":"a","body":"b"}`, owner)
	e.do(t, "POST", "/v1/issues", `{"project":"BBB","title":"b","body":"b"}`, owner)

	// s1: last_project is BBB (comment on BBB-1), issue is AAA-1 -> belongs to AAA only.
	s1 := map[string]string{"X-Session-Id": "s1"}
	wantStatus(t, e.do(t, "POST", "/v1/issues/BBB-1/comments", `{"body":"note"}`, s1), 201, "s1 comment")
	clock.add(2 * time.Minute)
	wantStatus(t, e.do(t, "PATCH", "/v1/sessions/claude/s1", `{"issue":"AAA-1","state":"waiting_approval","state_reason":"승인 대기"}`, s1), 200, "s1 wait")

	// s2: no issue, last_project is AAA -> belongs to AAA, earlier state_at.
	s2 := map[string]string{"X-Session-Id": "s2"}
	wantStatus(t, e.do(t, "POST", "/v1/issues/AAA-1/comments", `{"body":"note"}`, s2), 201, "s2 comment")
	clock.add(-time.Minute)
	wantStatus(t, e.do(t, "PATCH", "/v1/sessions/claude/s2", `{"state":"waiting_input","state_reason":"입력 대기"}`, s2), 200, "s2 wait")

	// s3: no issue and no last_project -> excluded from both projects.
	s3 := map[string]string{"X-Session-Id": "s3"}
	wantStatus(t, e.do(t, "GET", "/v1/actors/me", "", s3), 200, "s3 register")
	wantStatus(t, e.do(t, "PATCH", "/v1/sessions/claude/s3", `{"state":"waiting_external"}`, s3), 200, "s3 wait")

	rec := e.do(t, "GET", "/v1/agenda?project=AAA,BBB", "", owner)
	wantStatus(t, rec, 200, "agenda")
	if strings.Contains(rec.Body.String(), `"children"`) {
		t.Errorf("agenda waiting_sessions must not carry children: %s", rec.Body)
	}
	var a store.Agenda
	decodeJSON(t, rec, &a)
	if len(a.Projects) != 2 {
		t.Fatalf("projects = %d", len(a.Projects))
	}
	wsA := a.Projects[0].UserTurn.WaitingSessions
	wsB := a.Projects[1].UserTurn.WaitingSessions
	if wsA.Total != 2 || len(wsA.Items) != 2 || wsA.Items[0].SessionID != "s2" || wsA.Items[1].SessionID != "s1" {
		t.Errorf("AAA waiting_sessions = %+v", wsA)
	}
	if wsB.Total != 0 || wsB.Items == nil || len(wsB.Items) != 0 {
		t.Errorf("BBB waiting_sessions = %+v", wsB)
	}
}

func TestOverviewAndAgendaDecisionWaitingSessionsEndpoint(t *testing.T) {
	e, clock := newSessionEnv(t)
	owner := map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-1"}
	wantStatus(t, e.do(t, "POST", "/v1/projects", `{"key":"AAA","name":"A"}`, owner), 201, "create project")
	// AAA-1: P0 decision issue with no waiting session.
	wantStatus(t, e.do(t, "POST", "/v1/issues", `{"project":"AAA","title":"no wait","body":"b","priority":"P0","decision_needed":"어느 안을 고를까요?"}`, owner), 201, "create AAA-1")
	// AAA-2: P1 decision issue with two waiting sessions (one via session.issue, one via issue_claim) and location conflict.
	wantStatus(t, e.do(t, "POST", "/v1/issues", `{"project":"AAA","title":"with wait","body":"b","priority":"P1","decision_needed":"승인해 주실까요?"}`, owner), 201, "create AAA-2")

	// s1: linked via session.issue, earlier state_at.
	s1 := map[string]string{"X-Actor": "claude", "X-Session-Id": "s1", "X-Client-Host": "desk"}
	wantStatus(t, e.do(t, "GET", "/v1/actors/me", "", s1), 200, "s1 register")
	wantStatus(t, e.do(t, "PATCH", "/v1/sessions/claude/s1",
		`{"issue":"AAA-2","state":"waiting_approval","state_reason":"승인 대기","location":{"repo_remote":"https://example.com/acme/app.git","branch":"feat/a"}}`,
		s1), 200, "s1 wait")

	// s2: linked via open issue_claim (overrides session.issue=AAA-1), later state_at, conflicting location.
	clock.add(2 * time.Minute)
	s2 := map[string]string{"X-Actor": "codex", "X-Session-Id": "s2", "X-Client-Host": "laptop"}
	wantStatus(t, e.do(t, "POST", "/v1/issues/AAA-2/claim", "", s2), 201, "s2 claim AAA-2")
	wantStatus(t, e.do(t, "PATCH", "/v1/sessions/codex/s2",
		`{"issue":"AAA-1","state":"waiting_input","state_reason":"입력 대기","location":{"repo_remote":"git@example.com:acme/app.git","branch":"feat/a"}}`,
		s2), 200, "s2 wait")

	// s3: non-waiting active session on AAA-1 must not appear.
	s3 := map[string]string{"X-Actor": "claude", "X-Session-Id": "s3", "X-Client-Host": "desk"}
	wantStatus(t, e.do(t, "GET", "/v1/actors/me", "", s3), 200, "s3 register")
	wantStatus(t, e.do(t, "PATCH", "/v1/sessions/claude/s3", `{"issue":"AAA-1"}`, s3), 200, "s3 issue")

	// s4: waiting session on AAA-1 that expires (>24h) before overview/agenda read.
	s4 := map[string]string{"X-Actor": "codex", "X-Session-Id": "s4", "X-Client-Host": "desk"}
	wantStatus(t, e.do(t, "GET", "/v1/actors/me", "", s4), 200, "s4 register")
	wantStatus(t, e.do(t, "PATCH", "/v1/sessions/codex/s4", `{"issue":"AAA-1","state":"waiting_input"}`, s4), 200, "s4 wait")
	clock.add(25 * time.Hour)

	// Re-arm s1 and s2 within 24h while keeping s4 expired (>24h).
	wantStatus(t, e.do(t, "PATCH", "/v1/sessions/claude/s1", `{"state":"waiting_approval","state_reason":"승인 대기"}`, s1), 200, "s1 refresh")
	clock.add(2 * time.Minute)
	wantStatus(t, e.do(t, "PATCH", "/v1/sessions/codex/s2", `{"state":"waiting_input","state_reason":"입력 대기"}`, s2), 200, "s2 refresh")

	verifyDecisions := func(name, rawBody string, dec store.Section[model.DecisionItem]) {
		t.Helper()
		if strings.Contains(rawBody, `"waiting_sessions":null`) || strings.Contains(rawBody, `"location_conflicts":null`) {
			t.Errorf("%s contains null array in JSON: %s", name, rawBody)
		}
		if strings.Contains(rawBody, `"children"`) {
			t.Errorf("%s waiting_sessions must not carry children: %s", name, rawBody)
		}
		if dec.Total != 2 || len(dec.Items) != 2 {
			t.Fatalf("%s decisions = %+v", name, dec)
		}
		// AAA-1: empty waiting_sessions []
		if dec.Items[0].Key != "AAA-1" || dec.Items[0].WaitingSessions == nil || len(dec.Items[0].WaitingSessions) != 0 {
			t.Errorf("%s AAA-1 waiting_sessions = %#v, want empty non-nil", name, dec.Items[0].WaitingSessions)
		}
		// AAA-2: [claude/s1, codex/s2] with populated location_conflicts
		ws := dec.Items[1].WaitingSessions
		if dec.Items[1].Key != "AAA-2" || len(ws) != 2 ||
			ws[0].Actor != "claude" || ws[0].SessionID != "s1" ||
			ws[1].Actor != "codex" || ws[1].SessionID != "s2" {
			t.Fatalf("%s AAA-2 waiting_sessions = %+v", name, ws)
		}
		if len(ws[0].LocationConflicts) != 1 || ws[0].LocationConflicts[0] != (model.SessionRef{Actor: "codex", SessionID: "s2"}) {
			t.Errorf("%s s1 location_conflicts = %+v", name, ws[0].LocationConflicts)
		}
		if len(ws[1].LocationConflicts) != 1 || ws[1].LocationConflicts[0] != (model.SessionRef{Actor: "claude", SessionID: "s1"}) {
			t.Errorf("%s s2 location_conflicts = %+v", name, ws[1].LocationConflicts)
		}
	}

	ovRec := e.do(t, "GET", "/v1/projects/AAA/overview", "", owner)
	wantStatus(t, ovRec, 200, "overview")
	var o store.Overview
	decodeJSON(t, ovRec, &o)
	verifyDecisions("overview", ovRec.Body.String(), o.Decisions)

	agRec := e.do(t, "GET", "/v1/agenda?project=AAA", "", owner)
	wantStatus(t, agRec, 200, "agenda")
	var a store.Agenda
	decodeJSON(t, agRec, &a)
	if len(a.Projects) != 1 {
		t.Fatalf("agenda projects = %d", len(a.Projects))
	}
	verifyDecisions("agenda", agRec.Body.String(), a.Projects[0].UserTurn.Decisions)
	// UserTurn.WaitingSessions excludes expired (>24h) s4 and keeps only s1 and s2.
	ws := a.Projects[0].UserTurn.WaitingSessions
	if ws.Total != 2 || len(ws.Items) != 2 || ws.Items[0].SessionID != "s1" || ws.Items[1].SessionID != "s2" {
		t.Errorf("agenda user_turn.waiting_sessions = %+v, want [s1, s2]", ws)
	}
}

func TestDigestEndpoint(t *testing.T) {
	e := newEnv(t)
	seedViews(t, e)
	rec := e.do(t, "GET", "/v1/digest?since=2000-01-01T00:00:00Z&project=AGB", "", nil)
	if rec.Code != 200 {
		t.Fatalf("digest: %d %s", rec.Code, rec.Body.String())
	}
	var d store.Digest
	decodeJSON(t, rec, &d)
	if len(d.Projects) != 1 || d.Projects[0].Project.Key != "AGB" || d.Projects[0].Created.Total == 0 || d.Projects[0].Activity == 0 {
		t.Errorf("digest = %+v", d)
	}
	rec = e.do(t, "GET", "/v1/digest?since=2000-01-01T00:00:00Z&until=2000-01-02T00:00:00Z&project=AGB", "", nil)
	decodeJSON(t, rec, &d)
	if rec.Code != 200 || d.Projects[0].Activity != 0 {
		t.Errorf("empty window: %d %+v", rec.Code, d)
	}
	for _, q := range []string{"", "since=yesterday", "since=2000-01-01T00:00:00Z&limit=201", "since=2000-01-01T00:00:00Z&x=1"} {
		if rec = e.do(t, "GET", "/v1/digest?"+q, "", nil); rec.Code != 400 {
			t.Errorf("%q: %d", q, rec.Code)
		}
	}
	if rec = e.do(t, "GET", "/v1/digest?since=2000-01-01T00:00:00Z&project=NOPE", "", nil); rec.Code != 404 {
		t.Errorf("missing project: %d", rec.Code)
	}
}
