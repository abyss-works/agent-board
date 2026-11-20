package api

import (
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

func setupClaimProject(t *testing.T, e *env) {
	t.Helper()
	rec := e.do(t, "POST", "/v1/projects", `{"key":"AGB","name":"agent-board"}`, map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-s"})
	if rec.Code != 201 {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body)
	}
}

func createClaimIssue(t *testing.T, e *env, body string, headers map[string]string) model.Issue {
	t.Helper()
	rec := e.do(t, "POST", "/v1/issues", body, headers)
	if rec.Code != 201 {
		t.Fatalf("create issue: %d %s", rec.Code, rec.Body)
	}
	var out model.Issue
	decodeJSON(t, rec, &out)
	return out
}

func TestClaimEndpointLifecycleAndWaitingExpiration(t *testing.T) {
	e, clock := newSessionEnv(t)
	setupClaimProject(t, e)
	issue := createClaimIssue(t, e, `{"project":"AGB","title":"점유 대상"}`, map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-s"})

	hdrA := map[string]string{"X-Actor": "claude", "X-Session-Id": "sess-a"}
	hdrB := map[string]string{"X-Actor": "codex", "X-Session-Id": "sess-b"}

	// Missing issue returns 404.
	if rec := e.do(t, "POST", "/v1/issues/AGB-999/claim", "", hdrA); rec.Code != 404 {
		t.Fatalf("missing issue claim: %d %s", rec.Code, rec.Body)
	}

	// First claim returns 201 with active=true.
	rec := e.do(t, "POST", "/v1/issues/"+issue.Key+"/claim", "", hdrA)
	if rec.Code != 201 {
		t.Fatalf("first claim: %d %s", rec.Code, rec.Body)
	}
	var c1 model.IssueClaim
	decodeJSON(t, rec, &c1)
	if !c1.Active || c1.Actor != "claude" || c1.SessionID != "sess-a" || c1.Issue != issue.Key || c1.ReleasedAt != nil || c1.Outcome != nil {
		t.Fatalf("c1 = %+v", c1)
	}

	// Same session re-claim returns 200 idempotently.
	clock.add(5 * time.Minute)
	rec = e.do(t, "POST", "/v1/issues/"+issue.Key+"/claim", "", hdrA)
	if rec.Code != 200 {
		t.Fatalf("idempotent claim: %d %s", rec.Code, rec.Body)
	}
	var c1Again model.IssueClaim
	decodeJSON(t, rec, &c1Again)
	if c1Again.ID != c1.ID || !c1Again.Active {
		t.Errorf("re-claim = %+v, want id %d", c1Again, c1.ID)
	}

	// Another session gets 409 claimed with holder detail.
	rec = e.do(t, "POST", "/v1/issues/"+issue.Key+"/claim", "", hdrB)
	if rec.Code != 409 {
		t.Fatalf("other session claim: %d %s", rec.Code, rec.Body)
	}
	eb := decodeErr(t, rec)
	if eb.Error.Code != "claimed" || len(eb.Error.Details) != 1 || eb.Error.Details[0].Field != "claim" ||
		eb.Error.Details[0].Reason != "claimed" || eb.Error.Details[0].Message != "claude/sess-a 세션이 점유 중입니다." {
		t.Errorf("409 body = %+v", eb)
	}

	// After 31 minutes without activity, another session expires the old claim and gets 201; issue status stays backlog.
	clock.add(31 * time.Minute)
	rec = e.do(t, "POST", "/v1/issues/"+issue.Key+"/claim", "", hdrB)
	if rec.Code != 201 {
		t.Fatalf("claim after 31m: %d %s", rec.Code, rec.Body)
	}
	var c2 model.IssueClaim
	decodeJSON(t, rec, &c2)
	if !c2.Active || c2.Actor != "codex" || c2.SessionID != "sess-b" || c2.ID == c1.ID {
		t.Fatalf("c2 = %+v", c2)
	}
	rec = e.do(t, "GET", "/v1/issues/"+issue.Key, "", hdrB)
	var afterExp model.Issue
	decodeJSON(t, rec, &afterExp)
	if afterExp.Status != model.StatusBacklog {
		t.Errorf("issue status changed after expiration: %s", afterExp.Status)
	}

	// Put codex/sess-b into waiting_input state; claim stays valid at 23h and expires at 25h.
	rec = e.do(t, "PATCH", "/v1/sessions/codex/sess-b", `{"state":"waiting_input","reason":"확인 대기"}`, hdrB)
	if rec.Code != 400 {
		// state_reason is the field name on sessions PATCH
		t.Fatalf("expected unknown field on reason, got %d", rec.Code)
	}
	rec = e.do(t, "PATCH", "/v1/sessions/codex/sess-b", `{"state":"waiting_input","state_reason":"확인 대기"}`, hdrB)
	if rec.Code != 200 {
		t.Fatalf("set waiting_input: %d %s", rec.Code, rec.Body)
	}
	clock.add(23 * time.Hour)
	rec = e.do(t, "POST", "/v1/issues/"+issue.Key+"/claim", "", hdrA)
	if rec.Code != 409 {
		t.Fatalf("waiting claim at 23h: %d %s", rec.Code, rec.Body)
	}
	clock.add(2 * time.Hour)
	rec = e.do(t, "POST", "/v1/issues/"+issue.Key+"/claim", "", hdrA)
	if rec.Code != 201 {
		t.Fatalf("waiting claim at 25h: %d %s", rec.Code, rec.Body)
	}
	var c3 model.IssueClaim
	decodeJSON(t, rec, &c3)

	// GET /v1/issues/{key}/claims lists claims newest first with expired outcomes.
	rec = e.do(t, "GET", "/v1/issues/"+issue.Key+"/claims?limit=2&offset=0", "", hdrA)
	if rec.Code != 200 {
		t.Fatalf("list claims: %d %s", rec.Code, rec.Body)
	}
	var page struct {
		Items  []model.IssueClaim `json:"items"`
		Total  int                `json:"total"`
		Limit  int                `json:"limit"`
		Offset int                `json:"offset"`
	}
	decodeJSON(t, rec, &page)
	if page.Total != 3 || page.Limit != 2 || page.Offset != 0 || len(page.Items) != 2 {
		t.Fatalf("page = %+v", page)
	}
	if page.Items[0].ID != c3.ID || !page.Items[0].Active || page.Items[1].ID != c2.ID || page.Items[1].Active || *page.Items[1].Outcome != model.ClaimOutcomeExpired {
		t.Errorf("page items = %+v", page.Items)
	}
	if rec := e.do(t, "GET", "/v1/issues/AGB-999/claims", "", hdrA); rec.Code != 404 {
		t.Errorf("missing issue claims: %d", rec.Code)
	}
}

func TestReleaseClaimEndpointPermissionsAndValidation(t *testing.T) {
	e, _ := newSessionEnv(t)
	setupClaimProject(t, e)
	issue := createClaimIssue(t, e, `{"project":"AGB","title":"해제 대상"}`, map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-s"})

	hdrA := map[string]string{"X-Actor": "claude", "X-Session-Id": "sess-a"}
	hdrB := map[string]string{"X-Actor": "codex", "X-Session-Id": "sess-b"}
	hdrOwner := map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-s"}

	// No open claim returns 404.
	if rec := e.do(t, "DELETE", "/v1/issues/"+issue.Key+"/claim", "", hdrA); rec.Code != 404 {
		t.Fatalf("unclaimed delete: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, "POST", "/v1/issues/"+issue.Key+"/claim", "", hdrA); rec.Code != 201 {
		t.Fatalf("claim: %d %s", rec.Code, rec.Body)
	}

	// Unknown field returns 400.
	if rec := e.do(t, "DELETE", "/v1/issues/"+issue.Key+"/claim", `{"extra":"x"}`, hdrA); rec.Code != 400 {
		t.Errorf("unknown field: %d %s", rec.Code, rec.Body)
	}
	// Invalid outcome returns 422 on field outcome.
	for _, body := range []string{`{"outcome":"expired"}`, `{"outcome":""}`, `{"outcome":null}`} {
		rec := e.do(t, "DELETE", "/v1/issues/"+issue.Key+"/claim", body, hdrA)
		if rec.Code != 422 {
			t.Errorf("bad outcome %s: %d %s", body, rec.Code, rec.Body)
			continue
		}
		wantDetail(t, decodeErr(t, rec), "outcome", "invalid")
	}
	// Invalid reason returns 422 on field reason.
	for _, body := range []string{`{"reason":""}`, `{"reason":"   "}`, `{"reason":"a\nb"}`, `{"reason":"` + strings.Repeat("가", 201) + `"}`} {
		rec := e.do(t, "DELETE", "/v1/issues/"+issue.Key+"/claim", body, hdrA)
		if rec.Code != 422 {
			t.Errorf("bad reason %s: %d %s", body, rec.Code, rec.Body)
			continue
		}
		wantDetail(t, decodeErr(t, rec), "reason", "invalid")
	}

	// Other agent receives 403 forbidden.
	rec := e.do(t, "DELETE", "/v1/issues/"+issue.Key+"/claim", `{"outcome":"abandoned"}`, hdrB)
	if rec.Code != 403 || decodeErr(t, rec).Error.Code != "forbidden" {
		t.Fatalf("other agent delete: %d %s", rec.Code, rec.Body)
	}

	// Holder releases with done and reason.
	rec = e.do(t, "DELETE", "/v1/issues/"+issue.Key+"/claim", `{"outcome":"done","reason":"작업 마침"}`, hdrA)
	if rec.Code != 200 {
		t.Fatalf("holder delete done: %d %s", rec.Code, rec.Body)
	}
	var rel model.IssueClaim
	decodeJSON(t, rec, &rel)
	if rel.Active || rel.Outcome == nil || *rel.Outcome != model.ClaimOutcomeDone || rel.Reason == nil || *rel.Reason != "작업 마침" || rel.ReleasedAt == nil {
		t.Errorf("released = %+v", rel)
	}

	// Re-claim and release by holder with empty body defaults to abandoned.
	if rec := e.do(t, "POST", "/v1/issues/"+issue.Key+"/claim", "", hdrA); rec.Code != 201 {
		t.Fatalf("re-claim: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, "DELETE", "/v1/issues/"+issue.Key+"/claim", "", hdrA)
	if rec.Code != 200 {
		t.Fatalf("empty body delete: %d %s", rec.Code, rec.Body)
	}
	decodeJSON(t, rec, &rel)
	if rel.Active || rel.Outcome == nil || *rel.Outcome != model.ClaimOutcomeAbandoned {
		t.Errorf("default abandoned = %+v", rel)
	}

	// Re-claim by agent, then user actor releases it.
	if rec := e.do(t, "POST", "/v1/issues/"+issue.Key+"/claim", "", hdrA); rec.Code != 201 {
		t.Fatalf("re-claim 2: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(t, "DELETE", "/v1/issues/"+issue.Key+"/claim", `{"outcome":"abandoned","reason":"사용자 해제"}`, hdrOwner)
	if rec.Code != 200 {
		t.Fatalf("user delete: %d %s", rec.Code, rec.Body)
	}
}

func TestIssueStatusPatchAndCreateClaimsIntegration(t *testing.T) {
	e, clock := newSessionEnv(t)
	setupClaimProject(t, e)
	hdrA := map[string]string{"X-Actor": "claude", "X-Session-Id": "sess-a"}
	hdrB := map[string]string{"X-Actor": "codex", "X-Session-Id": "sess-b"}
	hdrOwner := map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-s"}

	// POST /v1/issues with status=doing creates a claim automatically.
	born := createClaimIssue(t, e, `{"project":"AGB","title":"생성 시 진행","status":"doing"}`, hdrOwner)
	rec := e.do(t, "GET", "/v1/issues/"+born.Key+"/claims", "", hdrOwner)
	var list struct {
		Items []model.IssueClaim `json:"items"`
		Total int                `json:"total"`
	}
	decodeJSON(t, rec, &list)
	if list.Total != 1 || !list.Items[0].Active || list.Items[0].Actor != "owner" {
		t.Fatalf("born doing claims = %+v", list)
	}

	// PATCH backlog->doing creates a claim.
	issue := createClaimIssue(t, e, `{"project":"AGB","title":"상태 연동"}`, hdrOwner)
	if rec := e.do(t, "PATCH", "/v1/issues/"+issue.Key, `{"status":"doing"}`, hdrA); rec.Code != 200 {
		t.Fatalf("backlog->doing: %d %s", rec.Code, rec.Body)
	}

	// Another agent cannot move doing->done or doing->backlog while valid claim exists.
	for _, body := range []string{`{"status":"done"}`, `{"status":"backlog"}`} {
		rec := e.do(t, "PATCH", "/v1/issues/"+issue.Key, body, hdrB)
		if rec.Code != 409 || decodeErr(t, rec).Error.Code != "claimed" {
			t.Errorf("other agent %s: %d %s", body, rec.Code, rec.Body)
		}
	}
	// Issue stays doing.
	var cur model.Issue
	decodeJSON(t, e.do(t, "GET", "/v1/issues/"+issue.Key, "", hdrA), &cur)
	if cur.Status != model.StatusDoing {
		t.Fatalf("status changed despite 409: %s", cur.Status)
	}

	// User taking over releases existing claim as taken_over with status_reason.
	if rec := e.do(t, "PATCH", "/v1/issues/"+issue.Key, `{"status":"review","status_reason":"사용자가 검토로 전환"}`, hdrOwner); rec.Code != 200 {
		t.Fatalf("user takeover: %d %s", rec.Code, rec.Body)
	}
	decodeJSON(t, e.do(t, "GET", "/v1/issues/"+issue.Key+"/claims", "", hdrOwner), &list)
	if list.Total != 1 || list.Items[0].Active || *list.Items[0].Outcome != model.ClaimOutcomeTakenOver || *list.Items[0].Reason != "사용자가 검토로 전환" {
		t.Errorf("taken over claim = %+v", list.Items[0])
	}

	// Holder moving doing->done releases claim as done; doing->cancelled releases as abandoned.
	if rec := e.do(t, "PATCH", "/v1/issues/"+issue.Key, `{"status":"doing"}`, hdrA); rec.Code != 200 {
		t.Fatalf("review->doing: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "PATCH", "/v1/issues/"+issue.Key, `{"status":"done"}`, hdrA); rec.Code != 200 {
		t.Fatalf("doing->done: %d %s", rec.Code, rec.Body)
	}
	decodeJSON(t, e.do(t, "GET", "/v1/issues/"+issue.Key+"/claims", "", hdrA), &list)
	if list.Items[0].Active || *list.Items[0].Outcome != model.ClaimOutcomeDone {
		t.Errorf("doing->done claim = %+v", list.Items[0])
	}

	// After 31 minutes, another agent moving to doing expires stale claim and creates a new one.
	if rec := e.do(t, "PATCH", "/v1/issues/"+issue.Key, `{"status":"backlog"}`, hdrA); rec.Code != 200 {
		t.Fatalf("done->backlog: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "POST", "/v1/issues/"+issue.Key+"/claim", "", hdrA); rec.Code != 201 {
		t.Fatalf("claim backlog: %d %s", rec.Code, rec.Body)
	}
	clock.add(31 * time.Minute)
	if rec := e.do(t, "PATCH", "/v1/issues/"+issue.Key, `{"status":"doing"}`, hdrB); rec.Code != 200 {
		t.Fatalf("expired claim -> other agent doing: %d %s", rec.Code, rec.Body)
	}
	decodeJSON(t, e.do(t, "GET", "/v1/issues/"+issue.Key+"/claims", "", hdrB), &list)
	if !list.Items[0].Active || list.Items[0].Actor != "codex" || *list.Items[1].Outcome != model.ClaimOutcomeExpired {
		t.Errorf("after expired takeover = %+v", list.Items[:2])
	}

	// DELETE /v1/issues/{key}?reason=... by holder releases claim as abandoned.
	if rec := e.do(t, "DELETE", "/v1/issues/"+issue.Key+"?reason=중복", "", hdrB); rec.Code != 200 {
		t.Fatalf("delete issue: %d %s", rec.Code, rec.Body)
	}
	decodeJSON(t, e.do(t, "GET", "/v1/issues/"+issue.Key+"/claims", "", hdrB), &list)
	if list.Items[0].Active || *list.Items[0].Outcome != model.ClaimOutcomeAbandoned {
		t.Errorf("after delete claim = %+v", list.Items[0])
	}
}

func TestOverviewAgendaHolderAndSessionIssueHTTP(t *testing.T) {
	e, clock := newSessionEnv(t)
	setupClaimProject(t, e)
	hdrA := map[string]string{"X-Actor": "claude", "X-Session-Id": "sess-a"}
	hdrOwner := map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-s"}

	claimed := createClaimIssue(t, e, `{"project":"AGB","title":"점유 진행"}`, hdrOwner)
	unclaimed := createClaimIssue(t, e, `{"project":"AGB","title":"대체 진행"}`, hdrOwner)
	fallbackIssue := createClaimIssue(t, e, `{"project":"AGB","title":"세션 기본 이슈"}`, hdrOwner)

	if rec := e.do(t, "PATCH", "/v1/issues/"+claimed.Key, `{"status":"doing"}`, hdrA); rec.Code != 200 {
		t.Fatalf("claimed doing: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "PATCH", "/v1/sessions/claude/sess-a", `{"issue":"`+fallbackIssue.Key+`","state":"waiting_input"}`, hdrA); rec.Code != 200 {
		t.Fatalf("set session waiting: %d %s", rec.Code, rec.Body)
	}

	// Make unclaimed doing then release its claim so overview uses activity fallback.
	if rec := e.do(t, "PATCH", "/v1/issues/"+unclaimed.Key, `{"status":"doing"}`, hdrOwner); rec.Code != 200 {
		t.Fatalf("unclaimed doing: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "DELETE", "/v1/issues/"+unclaimed.Key+"/claim", "", hdrOwner); rec.Code != 200 {
		t.Fatalf("unclaim: %d %s", rec.Code, rec.Body)
	}

	// Session GET reflects claimed issue while waiting (within 24h).
	clock.add(10 * time.Hour)
	var sess model.Session
	decodeJSON(t, e.do(t, "GET", "/v1/sessions/claude/sess-a", "", hdrOwner), &sess)
	if sess.Issue == nil || *sess.Issue != claimed.Key {
		t.Errorf("session issue while claimed & waiting = %v, want %s", sess.Issue, claimed.Key)
	}

	// Overview doing section reports source="claim" (not stale while waiting) and source="activity" (stale at 10h > 2h).
	var ov struct {
		Doing struct {
			Items []model.DoingItem `json:"items"`
		} `json:"doing"`
	}
	decodeJSON(t, e.do(t, "GET", "/v1/projects/AGB/overview", "", hdrOwner), &ov)
	byKey := map[string]model.Holder{}
	for _, it := range ov.Doing.Items {
		byKey[it.Key] = it.Holder
	}
	if h := byKey[claimed.Key]; h.Source != model.HolderSourceClaim || h.Stale || h.Actor != "claude" {
		t.Errorf("overview claimed holder = %+v", h)
	}
	if h := byKey[unclaimed.Key]; h.Source != model.HolderSourceActivity || !h.Stale || h.Actor != "owner" {
		t.Errorf("overview activity fallback holder = %+v", h)
	}

	// After 25h total, session issue falls back to fallbackIssue.
	clock.add(15 * time.Hour)
	decodeJSON(t, e.do(t, "GET", "/v1/sessions/claude/sess-a", "", hdrOwner), &sess)
	if sess.Issue == nil || *sess.Issue != fallbackIssue.Key {
		t.Errorf("session issue after claim expiry = %v, want %s", sess.Issue, fallbackIssue.Key)
	}
}

func TestPatchDoingToDoingClaimsHTTP(t *testing.T) {
	e, clock := newSessionEnv(t)
	setupClaimProject(t, e)
	hdrA := map[string]string{"X-Actor": "claude", "X-Session-Id": "sess-a"}
	hdrB := map[string]string{"X-Actor": "codex", "X-Session-Id": "sess-b"}
	hdrOwner := map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-s"}

	issue := createClaimIssue(t, e, `{"project":"AGB","title":"진행 중 인계","status":"doing"}`, hdrOwner)
	// Switch claim to claude/sess-a by unclaiming owner then claiming with claude.
	if rec := e.do(t, "DELETE", "/v1/issues/"+issue.Key+"/claim", "", hdrOwner); rec.Code != 200 {
		t.Fatalf("unclaim owner: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "PATCH", "/v1/issues/"+issue.Key, `{"status":"doing"}`, hdrA); rec.Code != 200 {
		t.Fatalf("unclaimed doing->doing: %d %s", rec.Code, rec.Body)
	}
	var got model.Issue
	decodeJSON(t, e.do(t, "GET", "/v1/issues/"+issue.Key, "", hdrA), &got)
	if got.Version != issue.Version {
		t.Errorf("unclaimed doing->doing bumped version %d -> %d", issue.Version, got.Version)
	}
	var list struct {
		Items []model.IssueClaim `json:"items"`
		Total int                `json:"total"`
	}
	decodeJSON(t, e.do(t, "GET", "/v1/issues/"+issue.Key+"/claims", "", hdrA), &list)
	if list.Total != 2 || !list.Items[0].Active || list.Items[0].Actor != "claude" {
		t.Fatalf("after unclaimed doing->doing: %+v", list)
	}
	activeID := list.Items[0].ID

	// Same session valid doing->doing is 200 no-op.
	if rec := e.do(t, "PATCH", "/v1/issues/"+issue.Key, `{"status":"doing"}`, hdrA); rec.Code != 200 {
		t.Fatalf("same session doing->doing: %d %s", rec.Code, rec.Body)
	}
	decodeJSON(t, e.do(t, "GET", "/v1/issues/"+issue.Key+"/claims", "", hdrA), &list)
	if list.Total != 2 || list.Items[0].ID != activeID || !list.Items[0].Active {
		t.Errorf("same session doing->doing mutated claim: %+v", list)
	}

	// Another agent while valid gets 409 claimed.
	rec := e.do(t, "PATCH", "/v1/issues/"+issue.Key, `{"status":"doing"}`, hdrB)
	if rec.Code != 409 || decodeErr(t, rec).Error.Code != "claimed" {
		t.Fatalf("other agent valid doing->doing: %d %s", rec.Code, rec.Body)
	}

	// After 31 minutes, another agent expires the stale claim and acquires a new one without bumping version.
	clock.add(31 * time.Minute)
	rec = e.do(t, "PATCH", "/v1/issues/"+issue.Key, `{"status":"doing"}`, hdrB)
	if rec.Code != 200 {
		t.Fatalf("other agent expired doing->doing: %d %s", rec.Code, rec.Body)
	}
	decodeJSON(t, rec, &got)
	if got.Status != model.StatusDoing || got.Version != issue.Version {
		t.Errorf("expired doing->doing response: status=%s version=%d (want %d)", got.Status, got.Version, issue.Version)
	}
	decodeJSON(t, e.do(t, "GET", "/v1/issues/"+issue.Key+"/claims", "", hdrB), &list)
	if list.Total != 3 || !list.Items[0].Active || list.Items[0].Actor != "codex" || *list.Items[1].Outcome != model.ClaimOutcomeExpired {
		t.Errorf("claims after expired doing->doing = %+v", list)
	}

	// User taking over a valid doing claim marks it taken_over and acquires a new claim.
	rec = e.do(t, "PATCH", "/v1/issues/"+issue.Key, `{"status":"doing"}`, hdrOwner)
	if rec.Code != 200 {
		t.Fatalf("user doing->doing: %d %s", rec.Code, rec.Body)
	}
	decodeJSON(t, e.do(t, "GET", "/v1/issues/"+issue.Key+"/claims", "", hdrOwner), &list)
	if list.Total != 4 || !list.Items[0].Active || list.Items[0].Actor != "owner" || *list.Items[1].Outcome != model.ClaimOutcomeTakenOver {
		t.Errorf("claims after user doing->doing = %+v", list)
	}
}

func TestClaimEndpointsRejectInactiveProjects(t *testing.T) {
	e, _ := newSessionEnv(t)
	hdrA := map[string]string{"X-Actor": "claude", "X-Session-Id": "sess-a"}
	hdrOwner := map[string]string{"X-Actor": "owner", "X-Session-Id": "owner-s"}

	for _, tc := range []struct {
		key    string
		status string
	}{
		{"ARC", "archived"},
		{"DEL", "deleted"},
	} {
		if rec := e.do(t, "POST", "/v1/projects", `{"key":"`+tc.key+`","name":"`+tc.status+`"}`, hdrOwner); rec.Code != 201 {
			t.Fatalf("create %s: %d %s", tc.key, rec.Code, rec.Body)
		}
		unclaimed := createClaimIssue(t, e, `{"project":"`+tc.key+`","title":"미점유"}`, hdrOwner)
		claimed := createClaimIssue(t, e, `{"project":"`+tc.key+`","title":"점유됨"}`, hdrOwner)
		if rec := e.do(t, "POST", "/v1/issues/"+claimed.Key+"/claim", "", hdrA); rec.Code != 201 {
			t.Fatalf("claim before %s: %d %s", tc.status, rec.Code, rec.Body)
		}
		if rec := e.do(t, "PATCH", "/v1/projects/"+tc.key, `{"status":"`+tc.status+`"}`, hdrOwner); rec.Code != 200 {
			t.Fatalf("set project %s: %d %s", tc.status, rec.Code, rec.Body)
		}

		rec := e.do(t, "POST", "/v1/issues/"+unclaimed.Key+"/claim", "", hdrA)
		if rec.Code != 409 {
			t.Errorf("%s POST claim: %d %s", tc.status, rec.Code, rec.Body)
		} else {
			wantDetail(t, decodeErr(t, rec), "", "project_not_active")
		}

		rec = e.do(t, "DELETE", "/v1/issues/"+claimed.Key+"/claim", "", hdrA)
		if rec.Code != 409 {
			t.Errorf("%s DELETE claim: %d %s", tc.status, rec.Code, rec.Body)
		} else {
			wantDetail(t, decodeErr(t, rec), "", "project_not_active")
		}
	}
}
