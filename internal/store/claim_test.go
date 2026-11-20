package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

func TestClaimIssueLifecycleAndExpiration(t *testing.T) {
	s, ctx := seeded(t)
	if err := s.EnsureActors(ctx, []model.Actor{
		{ID: "owner", Kind: model.ActorUser},
		{ID: "claude", Kind: model.ActorAgent},
		{ID: "codex", Kind: model.ActorAgent},
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	clockAt(s, &now)
	mustProject(t, s, ctx, "AGB")
	issue := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "claim target"})

	trA := model.Trace{Actor: "claude", SessionID: "sess-a", ClientHost: "desk"}
	trB := model.Trace{Actor: "codex", SessionID: "sess-b", ClientHost: "laptop"}

	if _, _, err := s.ClaimIssue(ctx, trA, "AGB-999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing issue: err=%v, want ErrNotFound", err)
	}

	// First claim creates a claim and guarantees the session row exists.
	c1, created, err := s.ClaimIssue(ctx, trA, issue.Key)
	if err != nil || !created || !c1.Active || c1.Actor != "claude" || c1.SessionID != "sess-a" || c1.Issue != issue.Key || c1.ReleasedAt != nil || c1.Outcome != nil {
		t.Fatalf("first claim = %+v created=%v err=%v", c1, created, err)
	}
	if sess := mustSession(t, s, "claude", "sess-a"); sess.LastHost != "desk" {
		t.Errorf("session not created on claim: %+v", sess)
	}

	// Re-claiming from the same active session is idempotent.
	now = now.Add(5 * time.Minute)
	c1Again, created, err := s.ClaimIssue(ctx, trA, issue.Key)
	if err != nil || created || c1Again.ID != c1.ID || !c1Again.Active {
		t.Fatalf("same session re-claim = %+v created=%v err=%v", c1Again, created, err)
	}

	// Another session is rejected with ErrIssueClaimed while valid.
	var cerr *ErrIssueClaimed
	if _, _, err := s.ClaimIssue(ctx, trB, issue.Key); !errors.As(err, &cerr) || cerr.Actor != "claude" || cerr.SessionID != "sess-a" {
		t.Fatalf("other session while active: err=%v", err)
	}

	// After 31 minutes without session activity, the claim expires on takeover and issue status does not change.
	now = now.Add(26 * time.Minute)
	c2, created, err := s.ClaimIssue(ctx, trB, issue.Key)
	if err != nil || !created || !c2.Active || c2.Actor != "codex" || c2.SessionID != "sess-b" || c2.ID == c1.ID {
		t.Fatalf("claim after 31m = %+v created=%v err=%v", c2, created, err)
	}
	gotIssue, err := s.GetIssue(ctx, issue.Key)
	if err != nil || gotIssue.Status != model.StatusBacklog {
		t.Errorf("issue status changed on expiration: %+v err=%v", gotIssue, err)
	}

	// Waiting state extends claim validity up to 24h.
	stWait := model.SessionStateWaitingInput
	if _, err := s.UpdateSession(ctx, trB.Actor, trB.SessionID, SessionPatch{
		State: Optional[string]{Set: true, Value: &stWait},
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(23 * time.Hour)
	cerr = nil
	if _, _, err := s.ClaimIssue(ctx, trA, issue.Key); !errors.As(err, &cerr) || cerr.Actor != "codex" || cerr.SessionID != "sess-b" {
		t.Fatalf("waiting claim at 23h should reject: err=%v", err)
	}
	// After 25h total from state_at, the waiting hold has expired.
	now = now.Add(2 * time.Hour)
	c3, created, err := s.ClaimIssue(ctx, trA, issue.Key)
	if err != nil || !created || !c3.Active || c3.Actor != "claude" {
		t.Fatalf("waiting claim at 25h should expire and allow new claim: %+v created=%v err=%v", c3, created, err)
	}

	// History lists newest first with expired outcomes on replaced claims.
	claims, total, err := s.ListIssueClaims(ctx, issue.Key, 50, 0)
	if err != nil || total != 3 || len(claims) != 3 {
		t.Fatalf("ListIssueClaims = %+v total=%d err=%v", claims, total, err)
	}
	if claims[0].ID != c3.ID || !claims[0].Active || claims[0].Outcome != nil {
		t.Errorf("claims[0] = %+v", claims[0])
	}
	if claims[1].ID != c2.ID || claims[1].Active || deref(claims[1].Outcome) != model.ClaimOutcomeExpired {
		t.Errorf("claims[1] = %+v", claims[1])
	}
	if claims[2].ID != c1.ID || claims[2].Active || deref(claims[2].Outcome) != model.ClaimOutcomeExpired {
		t.Errorf("claims[2] = %+v", claims[2])
	}
}

func TestReleaseClaimPermissionsAndValidation(t *testing.T) {
	s, ctx := seeded(t)
	if err := s.EnsureActors(ctx, []model.Actor{
		{ID: "owner", Kind: model.ActorUser},
		{ID: "claude", Kind: model.ActorAgent},
		{ID: "codex", Kind: model.ActorAgent},
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	clockAt(s, &now)
	mustProject(t, s, ctx, "AGB")
	issue := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "release target"})

	trA := model.Trace{Actor: "claude", SessionID: "sess-a", ClientHost: "desk"}
	trB := model.Trace{Actor: "codex", SessionID: "sess-b", ClientHost: "laptop"}

	if _, err := s.ReleaseClaim(ctx, trA, "AGB-999", "", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing issue: err=%v", err)
	}
	if _, err := s.ReleaseClaim(ctx, trA, issue.Key, "", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("no open claim: err=%v", err)
	}

	if _, _, err := s.ClaimIssue(ctx, trA, issue.Key); err != nil {
		t.Fatal(err)
	}

	// Validation errors.
	_, err := s.ReleaseClaim(ctx, trA, issue.Key, model.ClaimOutcomeExpired, nil)
	validationField(t, err, "outcome", "invalid")
	for _, bad := range []string{"", "   ", strings.Repeat("가", 201), "line\nbreak", "ctl\x00char"} {
		r := bad
		_, err = s.ReleaseClaim(ctx, trA, issue.Key, model.ClaimOutcomeDone, &r)
		validationField(t, err, "reason", "invalid")
	}

	// Another agent cannot release.
	if _, err := s.ReleaseClaim(ctx, trB, issue.Key, model.ClaimOutcomeAbandoned, nil); !errors.Is(err, ErrClaimForbidden) {
		t.Errorf("other agent release: err=%v, want ErrClaimForbidden", err)
	}

	// Holder releases with done and reason.
	reason := "  작업 완료  "
	rel, err := s.ReleaseClaim(ctx, trA, issue.Key, model.ClaimOutcomeDone, &reason)
	if err != nil || rel.Active || deref(rel.Outcome) != model.ClaimOutcomeDone || deref(rel.Reason) != "작업 완료" || rel.ReleasedAt == nil {
		t.Fatalf("holder release = %+v err=%v", rel, err)
	}

	// Re-claim and release by user actor with default outcome (abandoned).
	if _, _, err := s.ClaimIssue(ctx, trA, issue.Key); err != nil {
		t.Fatal(err)
	}
	relUser, err := s.ReleaseClaim(ctx, trOwner, issue.Key, "", nil)
	if err != nil || relUser.Active || deref(relUser.Outcome) != model.ClaimOutcomeAbandoned || relUser.Reason != nil {
		t.Fatalf("user release = %+v err=%v", relUser, err)
	}
}

func TestIssueStatusTransitionClaims(t *testing.T) {
	s, ctx := seeded(t)
	if err := s.EnsureActors(ctx, []model.Actor{
		{ID: "owner", Kind: model.ActorUser},
		{ID: "claude", Kind: model.ActorAgent},
		{ID: "codex", Kind: model.ActorAgent},
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	clockAt(s, &now)
	mustProject(t, s, ctx, "AGB")

	trA := model.Trace{Actor: "claude", SessionID: "sess-a", ClientHost: "desk"}
	trB := model.Trace{Actor: "codex", SessionID: "sess-b", ClientHost: "laptop"}

	// Creating with status=doing automatically creates an active claim.
	born := mustIssue(t, s, ctx, trA, IssueInput{Title: "born doing", Body: tplBody(model.TypeFeature), Status: model.StatusDoing})
	claims, total, err := s.ListIssueClaims(ctx, born.Key, 10, 0)
	if err != nil || total != 1 || !claims[0].Active || claims[0].Actor != "claude" || claims[0].SessionID != "sess-a" {
		t.Fatalf("born doing claims = %+v total=%d err=%v", claims, total, err)
	}

	// Another agent cannot move a validly claimed issue; status stays doing.
	done := model.StatusDone
	var cerr *ErrIssueClaimed
	if _, _, err := s.UpdateIssue(ctx, trB, born.Key, IssuePatch{Status: &done}, nil); !errors.As(err, &cerr) || cerr.Actor != "claude" {
		t.Fatalf("other agent move doing->done: err=%v", err)
	}
	if cur, _ := s.GetIssue(ctx, born.Key); cur.Status != model.StatusDoing {
		t.Errorf("status mutated despite 409: %s", cur.Status)
	}

	// Holder moving doing->review releases claim with outcome done.
	review := model.StatusReview
	if _, _, err := s.UpdateIssue(ctx, trA, born.Key, IssuePatch{Status: &review}, nil); err != nil {
		t.Fatal(err)
	}
	claims, _, _ = s.ListIssueClaims(ctx, born.Key, 10, 0)
	if claims[0].Active || deref(claims[0].Outcome) != model.ClaimOutcomeDone {
		t.Errorf("doing->review claim = %+v", claims[0])
	}

	// Moving backlog->doing creates a claim; holder moving doing->backlog releases with abandoned.
	issue := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "transitions"})
	doing := model.StatusDoing
	backlog := model.StatusBacklog
	if _, _, err := s.UpdateIssue(ctx, trA, issue.Key, IssuePatch{Status: &doing}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpdateIssue(ctx, trA, issue.Key, IssuePatch{Status: &backlog}, nil); err != nil {
		t.Fatal(err)
	}
	claims, _, _ = s.ListIssueClaims(ctx, issue.Key, 10, 0)
	if len(claims) != 1 || claims[0].Active || deref(claims[0].Outcome) != model.ClaimOutcomeAbandoned {
		t.Errorf("doing->backlog claim = %+v", claims)
	}

	// User taking over a validly claimed issue releases it with taken_over + status_reason,
	// and creates a new claim if the new status is doing.
	if _, _, err := s.ClaimIssue(ctx, trA, issue.Key); err != nil {
		t.Fatal(err)
	}
	reason := "사용자가 직접 진행"
	if _, _, err := s.UpdateIssue(ctx, trOwner, issue.Key, IssuePatch{Status: &doing, StatusReason: &reason}, nil); err != nil {
		t.Fatal(err)
	}
	claims, _, _ = s.ListIssueClaims(ctx, issue.Key, 10, 0)
	if len(claims) != 3 || !claims[0].Active || claims[0].Actor != "owner" {
		t.Fatalf("after user takeover to doing, latest claim = %+v", claims[0])
	}
	if claims[1].Active || deref(claims[1].Outcome) != model.ClaimOutcomeTakenOver || deref(claims[1].Reason) != reason {
		t.Errorf("taken over claim = %+v", claims[1])
	}

	// After 31m, another agent moving the issue to doing expires the stale claim and acquires a new one.
	now = now.Add(31 * time.Minute)
	if _, _, err := s.UpdateIssue(ctx, trOwner, issue.Key, IssuePatch{Status: &backlog}, nil); err != nil {
		t.Fatal(err)
	}
	// Re-claim by trA, then let it expire after 31m while issue is doing.
	if _, _, err := s.UpdateIssue(ctx, trA, issue.Key, IssuePatch{Status: &doing}, nil); err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * time.Minute)
	if _, _, err := s.UpdateIssue(ctx, trB, issue.Key, IssuePatch{Status: &done}, nil); err != nil {
		t.Fatalf("other agent after expiration: %v", err)
	}
	claims, _, _ = s.ListIssueClaims(ctx, issue.Key, 10, 0)
	if claims[0].Active || deref(claims[0].Outcome) != model.ClaimOutcomeExpired {
		t.Errorf("expired claim after other agent move = %+v", claims[0])
	}

	// Same session moving doing->cancelled and doing->deleted releases with abandoned.
	for _, st := range []model.IssueStatus{model.StatusCancelled, model.StatusDeleted} {
		it := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "to " + string(st)})
		if _, _, err := s.UpdateIssue(ctx, trA, it.Key, IssuePatch{Status: &doing}, nil); err != nil {
			t.Fatal(err)
		}
		target := st
		rsn := "정리"
		if _, _, err := s.UpdateIssue(ctx, trA, it.Key, IssuePatch{Status: &target, StatusReason: &rsn}, nil); err != nil {
			t.Fatal(err)
		}
		cs, _, _ := s.ListIssueClaims(ctx, it.Key, 10, 0)
		if len(cs) != 1 || cs[0].Active || deref(cs[0].Outcome) != model.ClaimOutcomeAbandoned {
			t.Errorf("doing->%s claim = %+v", st, cs)
		}
	}

	// Same session claiming while backlog then moving to doing keeps a valid claim,
	// or expires and replaces an invalid claim.
	pre := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "pre-claimed"})
	cValid, _, err := s.ClaimIssue(ctx, trA, pre.Key)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpdateIssue(ctx, trA, pre.Key, IssuePatch{Status: &doing}, nil); err != nil {
		t.Fatal(err)
	}
	cs, _, _ := s.ListIssueClaims(ctx, pre.Key, 10, 0)
	if len(cs) != 1 || cs[0].ID != cValid.ID || !cs[0].Active {
		t.Errorf("valid pre-claim moving to doing = %+v", cs)
	}

	preExp := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "pre-claimed expired"})
	cOld, _, err := s.ClaimIssue(ctx, trA, preExp.Key)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * time.Minute)
	if _, _, err := s.UpdateIssue(ctx, trA, preExp.Key, IssuePatch{Status: &doing}, nil); err != nil {
		t.Fatal(err)
	}
	cs, _, _ = s.ListIssueClaims(ctx, preExp.Key, 10, 0)
	if len(cs) != 2 || !cs[0].Active || cs[0].ID == cOld.ID || cs[1].ID != cOld.ID || deref(cs[1].Outcome) != model.ClaimOutcomeExpired {
		t.Errorf("expired pre-claim moving to doing = %+v", cs)
	}
}

func TestSessionIssueReflectsValidClaim(t *testing.T) {
	s, ctx := seeded(t)
	now := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	clockAt(s, &now)
	mustProject(t, s, ctx, "AAA")
	mustProject(t, s, ctx, "BBB")
	i1 := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AAA", Title: "fallback issue"})
	i2 := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "BBB", Title: "claimed first"})
	i3 := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "BBB", Title: "claimed second"})

	tr := model.Trace{Actor: "claude", SessionID: "sess-claim", ClientHost: "desk"}
	mustTouch(t, s, tr, now)
	if _, err := s.UpdateSession(ctx, tr.Actor, tr.SessionID, SessionPatch{
		Issue: Optional[string]{Set: true, Value: &i1.Key},
	}); err != nil {
		t.Fatal(err)
	}
	if got := mustSession(t, s, tr.Actor, tr.SessionID); deref(got.Issue) != i1.Key {
		t.Fatalf("before claim: issue=%v, want %s", got.Issue, i1.Key)
	}

	// Valid claim overrides session.issue_id.
	now = now.Add(time.Minute)
	if _, _, err := s.ClaimIssue(ctx, tr, i2.Key); err != nil {
		t.Fatal(err)
	}
	if got := mustSession(t, s, tr.Actor, tr.SessionID); deref(got.Issue) != i2.Key {
		t.Errorf("after first claim: issue=%v, want %s", got.Issue, i2.Key)
	}
	// ListSessions also shows claimed issue.
	listed, _, err := s.ListSessions(ctx, SessionFilter{Actor: "claude", Limit: 10})
	if err != nil || len(listed) != 1 || deref(listed[0].Issue) != i2.Key {
		t.Errorf("ListSessions = %+v err=%v", listed, err)
	}

	// Second claim takes precedence as the newest open claim.
	now = now.Add(time.Minute)
	if _, _, err := s.ClaimIssue(ctx, tr, i3.Key); err != nil {
		t.Fatal(err)
	}
	if got := mustSession(t, s, tr.Actor, tr.SessionID); deref(got.Issue) != i3.Key {
		t.Errorf("after second claim: issue=%v, want %s", got.Issue, i3.Key)
	}

	// Waiting session in Agenda shows the claimed issue and attributes to the claimed issue's project (BBB).
	stWait := model.SessionStateWaitingInput
	if _, err := s.UpdateSession(ctx, tr.Actor, tr.SessionID, SessionPatch{
		State: Optional[string]{Set: true, Value: &stWait},
	}); err != nil {
		t.Fatal(err)
	}
	ag, err := s.Agenda(ctx, []string{"AAA", "BBB"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if ag.Projects[0].UserTurn.WaitingSessions.Total != 0 || ag.Projects[1].UserTurn.WaitingSessions.Total != 1 ||
		deref(ag.Projects[1].UserTurn.WaitingSessions.Items[0].Issue) != i3.Key {
		t.Errorf("agenda waiting_sessions = AAA:%+v BBB:%+v", ag.Projects[0].UserTurn.WaitingSessions, ag.Projects[1].UserTurn.WaitingSessions)
	}

	// Releasing i3 exposes i2 again.
	if _, err := s.ReleaseClaim(ctx, tr, i3.Key, model.ClaimOutcomeDone, nil); err != nil {
		t.Fatal(err)
	}
	if got := mustSession(t, s, tr.Actor, tr.SessionID); deref(got.Issue) != i2.Key {
		t.Errorf("after releasing i3: issue=%v, want %s", got.Issue, i2.Key)
	}

	// Waiting holds the claim valid at 23h, then expires at 25h and falls back to session.issue_id (i1).
	now = now.Add(23 * time.Hour)
	if got := mustSession(t, s, tr.Actor, tr.SessionID); deref(got.Issue) != i2.Key {
		t.Errorf("waiting at 23h: issue=%v, want %s", got.Issue, i2.Key)
	}
	now = now.Add(2 * time.Hour)
	if got := mustSession(t, s, tr.Actor, tr.SessionID); deref(got.Issue) != i1.Key {
		t.Errorf("expired at 25h: issue=%v, want fallback %s", got.Issue, i1.Key)
	}
}

func TestFingerprintCoversIssueClaims(t *testing.T) {
	s, ctx := seeded(t)
	now := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	clockAt(s, &now)
	mustProject(t, s, ctx, "AGB")
	issue := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "fp claim"})
	tr := model.Trace{Actor: "claude", SessionID: "sess-fp", ClientHost: "desk"}
	mustTouch(t, s, tr, now)

	fp0, err := s.FingerprintNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ClaimIssue(ctx, tr, issue.Key); err != nil {
		t.Fatal(err)
	}
	fp1, err := s.FingerprintNow(ctx)
	if err != nil || fp1 == fp0 {
		t.Fatalf("creating claim did not change fingerprint: %q -> %q (%v)", fp0, fp1, err)
	}
	if _, err := s.ReleaseClaim(ctx, tr, issue.Key, model.ClaimOutcomeDone, nil); err != nil {
		t.Fatal(err)
	}
	fp2, err := s.FingerprintNow(ctx)
	if err != nil || fp2 == fp1 {
		t.Fatalf("releasing claim did not change fingerprint: %q -> %q (%v)", fp1, fp2, err)
	}
}

func TestUpdateDoingToDoingSyncsClaimWithoutBumpingVersion(t *testing.T) {
	s, ctx := seeded(t)
	if err := s.EnsureActors(ctx, []model.Actor{
		{ID: "owner", Kind: model.ActorUser},
		{ID: "claude", Kind: model.ActorAgent},
		{ID: "codex", Kind: model.ActorAgent},
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	clockAt(s, &now)
	mustProject(t, s, ctx, "AGB")

	trA := model.Trace{Actor: "claude", SessionID: "sess-a", ClientHost: "desk"}
	trB := model.Trace{Actor: "codex", SessionID: "sess-b", ClientHost: "laptop"}
	doing := model.StatusDoing

	issue := mustIssue(t, s, ctx, trA, IssueInput{Title: "doing target", Body: tplBody(model.TypeFeature), Status: model.StatusDoing})
	startVer := issue.Version
	actsBefore, _, err := s.ListActivity(ctx, ActivityFilter{Issues: []string{issue.Key}, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}

	// Same session with valid claim: no-op, keeps claim ID, version, and activity count.
	claimsBefore, _, _ := s.ListIssueClaims(ctx, issue.Key, 10, 0)
	got, changed, err := s.UpdateIssue(ctx, trA, issue.Key, IssuePatch{Status: &doing}, nil)
	if err != nil || changed || got.Version != startVer {
		t.Fatalf("same session doing->doing: got=%+v changed=%v err=%v", got, changed, err)
	}
	claimsAfter, _, _ := s.ListIssueClaims(ctx, issue.Key, 10, 0)
	if len(claimsAfter) != 1 || claimsAfter[0].ID != claimsBefore[0].ID || !claimsAfter[0].Active {
		t.Errorf("same session claim changed: %+v", claimsAfter)
	}

	// Another agent while claim is valid: rejected with ErrIssueClaimed.
	var cerr *ErrIssueClaimed
	if _, _, err := s.UpdateIssue(ctx, trB, issue.Key, IssuePatch{Status: &doing}, nil); !errors.As(err, &cerr) || cerr.Actor != "claude" {
		t.Fatalf("other agent doing->doing while active: err=%v", err)
	}

	// Another agent combining doing->doing with a field edit while claim is valid: rejected without applying edit.
	newTitle := "should not apply"
	cerr = nil
	if _, _, err := s.UpdateIssue(ctx, trB, issue.Key, IssuePatch{Status: &doing, Title: &newTitle}, nil); !errors.As(err, &cerr) {
		t.Fatalf("other agent doing->doing with title edit: err=%v", err)
	}
	if cur, _ := s.GetIssue(ctx, issue.Key); cur.Title != "doing target" || cur.Version != startVer {
		t.Errorf("issue mutated on claimed error: %+v", cur)
	}

	// After 31 minutes, another agent takes over the expired claim without bumping version or activity.
	now = now.Add(31 * time.Minute)
	got, changed, err = s.UpdateIssue(ctx, trB, issue.Key, IssuePatch{Status: &doing}, nil)
	if err != nil || changed || got.Status != model.StatusDoing || got.Version != startVer {
		t.Fatalf("expired doing->doing takeover: got=%+v changed=%v err=%v", got, changed, err)
	}
	claims, _, _ := s.ListIssueClaims(ctx, issue.Key, 10, 0)
	if len(claims) != 2 || !claims[0].Active || claims[0].Actor != "codex" || claims[1].Active || deref(claims[1].Outcome) != model.ClaimOutcomeExpired {
		t.Errorf("claims after expired takeover = %+v", claims)
	}
	actsAfter, _, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{issue.Key}, Limit: 50})
	if len(actsAfter) != len(actsBefore) {
		t.Errorf("activity count = %d, want %d", len(actsAfter), len(actsBefore))
	}

	// User taking over a valid doing claim marks it taken_over and creates a new claim without bumping version.
	got, changed, err = s.UpdateIssue(ctx, trOwner, issue.Key, IssuePatch{Status: &doing}, nil)
	if err != nil || changed || got.Version != startVer {
		t.Fatalf("user doing->doing takeover: got=%+v changed=%v err=%v", got, changed, err)
	}
	claims, _, _ = s.ListIssueClaims(ctx, issue.Key, 10, 0)
	if len(claims) != 3 || !claims[0].Active || claims[0].Actor != "owner" || deref(claims[1].Outcome) != model.ClaimOutcomeTakenOver {
		t.Errorf("claims after user takeover = %+v", claims)
	}

	// Unclaimed doing issue acquires a new claim on doing->doing without bumping version.
	if _, err := s.ReleaseClaim(ctx, trOwner, issue.Key, model.ClaimOutcomeAbandoned, nil); err != nil {
		t.Fatal(err)
	}
	got, changed, err = s.UpdateIssue(ctx, trA, issue.Key, IssuePatch{Status: &doing}, nil)
	if err != nil || changed || got.Version != startVer {
		t.Fatalf("unclaimed doing->doing: got=%+v changed=%v err=%v", got, changed, err)
	}
	claims, _, _ = s.ListIssueClaims(ctx, issue.Key, 10, 0)
	if len(claims) != 4 || !claims[0].Active || claims[0].Actor != "claude" {
		t.Errorf("claims after unclaimed doing->doing = %+v", claims)
	}
}

func TestClaimAndReleaseRejectInactiveProject(t *testing.T) {
	s, ctx := seeded(t)
	now := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	clockAt(s, &now)
	trA := model.Trace{Actor: "claude", SessionID: "sess-a", ClientHost: "desk"}

	for _, tc := range []struct {
		key    string
		status model.ProjectStatus
	}{
		{"ARC", model.ProjectArchived},
		{"DEL", model.ProjectDeleted},
	} {
		mustProject(t, s, ctx, tc.key)
		unclaimed := mustIssue(t, s, ctx, trOwner, IssueInput{Project: tc.key, Title: "unclaimed"})
		claimed := mustIssue(t, s, ctx, trOwner, IssueInput{Project: tc.key, Title: "claimed"})
		if _, _, err := s.ClaimIssue(ctx, trA, claimed.Key); err != nil {
			t.Fatal(err)
		}
		st := tc.status
		if _, err := s.UpdateProject(ctx, trOwner, tc.key, ProjectPatch{Status: &st}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.ClaimIssue(ctx, trA, unclaimed.Key); !errors.Is(err, ErrProjectNotActive) {
			t.Errorf("%s ClaimIssue: err=%v, want ErrProjectNotActive", tc.status, err)
		}
		if _, err := s.ReleaseClaim(ctx, trA, claimed.Key, model.ClaimOutcomeAbandoned, nil); !errors.Is(err, ErrProjectNotActive) {
			t.Errorf("%s ReleaseClaim: err=%v, want ErrProjectNotActive", tc.status, err)
		}
	}
}
