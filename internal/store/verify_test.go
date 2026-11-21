package store

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

func TestCreateIssueVerificationDefaultsAndValidation(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "APP")

	// Both user and agent issues default to VerificationNone.
	byUser := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "APP", Title: "user issue"})
	byAgent := mustIssue(t, s, ctx, trClaude, IssueInput{Project: "APP", Title: "agent issue"})
	for _, got := range []*model.Issue{byUser, byAgent} {
		if got.VerificationState != model.VerificationNone || got.VerificationNote != nil || got.VerifiedAt != nil || got.VerifiedBy != nil {
			t.Errorf("%s: verification = %q/%v/%v/%v, want none/nil",
				got.Key, got.VerificationState, got.VerificationNote, got.VerifiedAt, got.VerifiedBy)
		}
	}

	// Opt-in pending with trimmed note.
	pending, err := s.CreateIssue(ctx, trClaude, IssueInput{
		Project:           "APP",
		Title:             "needs check",
		Body:              tplBody(model.TypeFeature),
		VerificationState: model.VerificationPending,
		VerificationNote:  str("  실제 화면 확인 필요  "),
	})
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	if pending.VerificationState != model.VerificationPending || pending.VerificationNote == nil || *pending.VerificationNote != "실제 화면 확인 필요" ||
		pending.VerifiedAt != nil || pending.VerifiedBy != nil {
		t.Errorf("pending issue = %+v", pending)
	}

	// Disallow setting verified directly on create.
	if _, err := s.CreateIssue(ctx, trOwner, IssueInput{
		Project:           "APP",
		Title:             "direct verified",
		VerificationState: model.VerificationVerified,
	}); !reflect.DeepEqual(violationFields(err), []string{"verification_state"}) {
		t.Errorf("direct verified create err = %v", err)
	}

	// Disallow note when state is none (default or explicit).
	if _, err := s.CreateIssue(ctx, trOwner, IssueInput{
		Project:          "APP",
		Title:            "note without pending",
		VerificationNote: str("메모"),
	}); !reflect.DeepEqual(violationFields(err), []string{"verification_note"}) {
		t.Errorf("note on default none err = %v", err)
	}
	if _, err := s.CreateIssue(ctx, trOwner, IssueInput{
		Project:           "APP",
		Title:             "blank note",
		VerificationState: model.VerificationPending,
		VerificationNote:  str("   "),
	}); !reflect.DeepEqual(violationFields(err), []string{"verification_note"}) {
		t.Errorf("blank note err = %v", err)
	}
	if _, err := s.CreateIssue(ctx, trOwner, IssueInput{
		Project:           "APP",
		Title:             "too long note",
		VerificationState: model.VerificationPending,
		VerificationNote:  str(strings.Repeat("가", model.MaxVerificationNoteRunes+1)),
	}); !reflect.DeepEqual(violationFields(err), []string{"verification_note"}) {
		t.Errorf("too long note err = %v", err)
	}
}

func TestVerifyAndUnverifyIssue(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "APP")

	var events []model.Event
	s.SetEventHook(func(ev []model.Event) { events = append(events, ev...) })

	noneIssue := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "APP", Title: "no gate"})
	pending := mustIssue(t, s, ctx, trClaude, IssueInput{
		Project:           "APP",
		Title:             "gated work",
		Status:            model.StatusDone,
		VerificationState: model.VerificationPending,
		VerificationNote:  str("화면 레이아웃 확인"),
	})

	// Verify on none fails with validation error.
	if _, _, err := s.VerifyIssue(ctx, trOwner, noneIssue.Key, nil); !reflect.DeepEqual(violationFields(err), []string{"verification_state"}) {
		t.Errorf("VerifyIssue on none err = %v", err)
	}
	if _, _, err := s.UnverifyIssue(ctx, trOwner, noneIssue.Key, nil); !reflect.DeepEqual(violationFields(err), []string{"verification_state"}) {
		t.Errorf("UnverifyIssue on none err = %v", err)
	}

	events = nil
	// Verify pending issue (even when done).
	got, changed, err := s.VerifyIssue(ctx, trOwner, pending.Key, nil)
	if err != nil || !changed {
		t.Fatalf("VerifyIssue: changed=%v err=%v", changed, err)
	}
	if got.VerificationState != model.VerificationVerified || got.VerifiedAt == nil || *got.VerifiedAt != got.UpdatedAt ||
		got.VerifiedBy == nil || *got.VerifiedBy != "owner" || got.VerificationNote == nil || *got.VerificationNote != "화면 레이아웃 확인" ||
		got.Version != pending.Version+1 {
		t.Errorf("verified issue = %+v", got)
	}
	acts, _, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{pending.Key}, Limit: 1})
	if len(acts) != 1 || acts[0].Kind != model.ActivityUpdated ||
		string(acts[0].Changes["verification_state"].From) != `"pending"` ||
		string(acts[0].Changes["verification_state"].To) != `"verified"` {
		t.Errorf("verify activity = %+v", acts)
	}
	if len(events) != 1 || events[0].Kind != model.EventIssueUpdated || events[0].Subject != pending.Key {
		t.Errorf("verify events = %+v", events)
	}

	// Idempotent second verify.
	again, changed, err := s.VerifyIssue(ctx, trOwner, pending.Key, nil)
	if err != nil || changed || !reflect.DeepEqual(again, got) {
		t.Errorf("second VerifyIssue: changed=%v err=%v got=%+v", changed, err, again)
	}

	events = nil
	// Unverify reverts to pending and preserves verification_note.
	reverted, changed, err := s.UnverifyIssue(ctx, trOwner, pending.Key, nil)
	if err != nil || !changed {
		t.Fatalf("UnverifyIssue: changed=%v err=%v", changed, err)
	}
	if reverted.VerificationState != model.VerificationPending || reverted.VerifiedAt != nil || reverted.VerifiedBy != nil ||
		reverted.VerificationNote == nil || *reverted.VerificationNote != "화면 레이아웃 확인" ||
		reverted.Version != got.Version+1 {
		t.Errorf("unverified issue = %+v", reverted)
	}
	acts, _, _ = s.ListActivity(ctx, ActivityFilter{Issues: []string{pending.Key}, Limit: 1})
	if string(acts[0].Changes["verification_state"].From) != `"verified"` ||
		string(acts[0].Changes["verification_state"].To) != `"pending"` {
		t.Errorf("unverify activity = %+v", acts[0])
	}
	if len(events) != 1 || events[0].Kind != model.EventIssueUpdated {
		t.Errorf("unverify events = %+v", events)
	}

	// Idempotent second unverify.
	againPending, changed, err := s.UnverifyIssue(ctx, trOwner, pending.Key, nil)
	if err != nil || changed || !reflect.DeepEqual(againPending, reverted) {
		t.Errorf("second UnverifyIssue: changed=%v err=%v", changed, err)
	}
}

func TestVerifyRules(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "APP")
	i := mustIssue(t, s, ctx, trClaude, IssueInput{Project: "APP", Title: "a", VerificationState: model.VerificationPending})

	stale := i.Version + 5
	if _, _, err := s.VerifyIssue(ctx, trOwner, i.Key, &stale); !errors.Is(err, ErrVersionMismatch) {
		t.Errorf("stale If-Match err = %v", err)
	}
	cur := i.Version
	got, changed, err := s.VerifyIssue(ctx, trOwner, i.Key, &cur)
	if err != nil || !changed || got.Version != cur+1 {
		t.Fatalf("matching If-Match: %+v changed=%v err=%v", got, changed, err)
	}
	if _, _, err := s.VerifyIssue(ctx, trOwner, i.Key, &cur); !errors.Is(err, ErrVersionMismatch) {
		t.Errorf("no-op with stale If-Match err = %v", err)
	}
	if _, _, err := s.VerifyIssue(ctx, trOwner, "APP-99", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing issue err = %v", err)
	}

	archived := model.ProjectArchived
	if _, err := s.UpdateProject(ctx, trOwner, "APP", ProjectPatch{Status: &archived}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UnverifyIssue(ctx, trOwner, i.Key, nil); !errors.Is(err, ErrProjectNotActive) {
		t.Errorf("archived project unverify err = %v", err)
	}
	if _, changed, err := s.VerifyIssue(ctx, trOwner, i.Key, nil); err != nil || changed {
		t.Errorf("no-op in archived project: changed=%v err=%v", changed, err)
	}
}

func TestUpdateIssueVerificationTransitionsAndNoAutoRevert(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "APP")

	i := mustIssue(t, s, ctx, trClaude, IssueInput{Project: "APP", Title: "start", Status: model.StatusReview})
	pending, none, verified := model.VerificationPending, model.VerificationNone, model.VerificationVerified

	// Disallow setting verified directly via patch.
	if _, _, err := s.UpdateIssue(ctx, trOwner, i.Key, IssuePatch{VerificationState: &verified}, nil); !reflect.DeepEqual(violationFields(err), []string{"verification_state"}) {
		t.Errorf("patch none->verified err = %v", err)
	}

	// none -> pending with note.
	got, changed, err := s.UpdateIssue(ctx, trClaude, i.Key, IssuePatch{
		VerificationState: &pending,
		VerificationNote:  Optional[string]{Set: true, Value: str("  모바일 확인  ")},
	}, nil)
	if err != nil || !changed || got.VerificationState != model.VerificationPending || got.VerificationNote == nil || *got.VerificationNote != "모바일 확인" {
		t.Fatalf("none->pending = %+v changed=%v err=%v", got, changed, err)
	}

	// Verify it.
	got, _, err = s.VerifyIssue(ctx, trOwner, i.Key, nil)
	if err != nil || got.VerificationState != model.VerificationVerified {
		t.Fatalf("verify: %+v %v", got, err)
	}
	verifiedAt, verifiedBy := *got.VerifiedAt, *got.VerifiedBy

	// Normal edits (title, body, type, review->doing) MUST NOT auto-revert verified!
	doing, design := model.StatusDoing, model.TypeDesign
	edited, changed, err := s.UpdateIssue(ctx, trClaude, i.Key, IssuePatch{
		Title:  str("renamed"),
		Body:   str(tplBody(model.TypeDesign)),
		Type:   &design,
		Status: &doing,
	}, nil)
	if err != nil || !changed {
		t.Fatalf("edit verified: changed=%v err=%v", changed, err)
	}
	if edited.VerificationState != model.VerificationVerified || edited.VerifiedAt == nil || *edited.VerifiedAt != verifiedAt ||
		edited.VerifiedBy == nil || *edited.VerifiedBy != verifiedBy || edited.VerificationNote == nil || *edited.VerificationNote != "모바일 확인" {
		t.Errorf("normal edit must keep verified state: %+v", edited)
	}

	// Explicit patch verified -> pending clears verified_at/by, keeps note.
	backPending, changed, err := s.UpdateIssue(ctx, trOwner, i.Key, IssuePatch{VerificationState: &pending}, nil)
	if err != nil || !changed || backPending.VerificationState != model.VerificationPending ||
		backPending.VerifiedAt != nil || backPending.VerifiedBy != nil ||
		backPending.VerificationNote == nil || *backPending.VerificationNote != "모바일 확인" {
		t.Errorf("verified->pending = %+v changed=%v err=%v", backPending, changed, err)
	}

	// Patch pending -> none with explicit non-nil note fails validation.
	if _, _, err := s.UpdateIssue(ctx, trOwner, i.Key, IssuePatch{
		VerificationState: &none,
		VerificationNote:  Optional[string]{Set: true, Value: str("남기면 안 됨")},
	}, nil); !reflect.DeepEqual(violationFields(err), []string{"verification_note"}) {
		t.Errorf("pending->none with note err = %v", err)
	}

	// Patch pending -> none clears verification_note, verified_at, verified_by.
	cleared, changed, err := s.UpdateIssue(ctx, trOwner, i.Key, IssuePatch{VerificationState: &none}, nil)
	if err != nil || !changed || cleared.VerificationState != model.VerificationNone ||
		cleared.VerificationNote != nil || cleared.VerifiedAt != nil || cleared.VerifiedBy != nil {
		t.Errorf("pending->none = %+v changed=%v err=%v", cleared, changed, err)
	}
	acts, _, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{i.Key}, Limit: 1})
	fields := make([]string, 0, len(acts[0].Changes))
	for f := range acts[0].Changes {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	if !reflect.DeepEqual(fields, []string{"verification_note", "verification_state"}) {
		t.Errorf("pending->none activity fields = %v", fields)
	}
}

func TestListIssuesVerificationStateFilter(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "APP")
	iNone := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "APP", Title: "none"})
	iPending := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "APP", Title: "pending", VerificationState: model.VerificationPending})
	iVerified := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "APP", Title: "verified", VerificationState: model.VerificationPending})
	if _, _, err := s.VerifyIssue(ctx, trOwner, iVerified.Key, nil); err != nil {
		t.Fatal(err)
	}
	list := func(states ...model.VerificationState) []string {
		items, _, err := s.ListIssues(ctx, IssueFilter{
			Statuses:           model.IssueStatuses,
			VerificationStates: states,
			Sort:               []Sort{{Field: "key"}},
			Limit:              10,
		})
		if err != nil {
			t.Fatal(err)
		}
		return keysOf(items, func(i model.IssueSummary) string { return i.Key })
	}
	if got := list(model.VerificationPending); !reflect.DeepEqual(got, []string{iPending.Key}) {
		t.Errorf("pending = %v", got)
	}
	if got := list(model.VerificationPending, model.VerificationVerified); !reflect.DeepEqual(got, []string{iPending.Key, iVerified.Key}) {
		t.Errorf("pending+verified = %v", got)
	}
	if got := list(model.VerificationNone); !reflect.DeepEqual(got, []string{iNone.Key}) {
		t.Errorf("none = %v", got)
	}
	if got := list(); len(got) != 3 {
		t.Errorf("all = %v", got)
	}
}

func TestOverviewAgendaAndListUnverified(t *testing.T) {
	s, ctx := seeded(t)
	base := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	step := 0
	s.SetClock(func() time.Time {
		step++
		return base.Add(time.Duration(step) * time.Minute)
	})
	mustProject(t, s, ctx, "APP")
	mustProject(t, s, ctx, "OTH")
	ms := mustMilestone(t, s, "APP", "v1")

	mk := func(project, title string, st model.IssueStatus, vs model.VerificationState, milestone *int64) *model.Issue {
		return mustIssue(t, s, ctx, trOwner, IssueInput{
			Project:           project,
			Title:             title,
			Status:            st,
			VerificationState: vs,
			Milestone:         milestone,
		})
	}
	iBacklog := mk("APP", "backlog", model.StatusBacklog, model.VerificationPending, &ms.ID)
	iDone := mk("APP", "done", model.StatusDone, model.VerificationPending, &ms.ID)
	mk("APP", "cancelled", model.StatusCancelled, model.VerificationPending, &ms.ID)
	mk("APP", "deleted", model.StatusDeleted, model.VerificationPending, &ms.ID)
	iReview := mk("APP", "review", model.StatusReview, model.VerificationPending, nil)
	iVerified := mk("APP", "verified", model.StatusDone, model.VerificationPending, &ms.ID)
	if _, _, err := s.VerifyIssue(ctx, trOwner, iVerified.Key, nil); err != nil {
		t.Fatal(err)
	}
	mk("APP", "none", model.StatusDoing, model.VerificationNone, &ms.ID)
	mk("OTH", "other", model.StatusBacklog, model.VerificationPending, nil)

	// Touch iBacklog so it becomes newest updated.
	if _, _, err := s.UpdateIssue(ctx, trOwner, iBacklog.Key, IssuePatch{Title: str("backlog updated")}, nil); err != nil {
		t.Fatal(err)
	}

	o, err := s.Overview(ctx, "APP", 10)
	if err != nil {
		t.Fatal(err)
	}
	// Newest updated first; includes backlog, doing, review, done; excludes cancelled, deleted, verified, none.
	want := []string{iBacklog.Key, iReview.Key, iDone.Key}
	got := keysOf(o.Unverified.Items, func(i model.IssueSummary) string { return i.Key })
	if o.Unverified.Total != 3 || !reflect.DeepEqual(got, want) {
		t.Errorf("overview unverified = %v (total %d), want %v", got, o.Unverified.Total, want)
	}

	o2, _ := s.Overview(ctx, "APP", 2)
	if o2.Unverified.Total != 3 || len(o2.Unverified.Items) != 2 || o2.Unverified.Items[0].Key != iBacklog.Key {
		t.Errorf("overview limit 2 = %+v", o2.Unverified)
	}

	ag, err := s.Agenda(ctx, []string{"APP"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if ag.Projects[0].UserTurn.Unverified.Total != 3 || !reflect.DeepEqual(keysOf(ag.Projects[0].UserTurn.Unverified.Items, func(i model.IssueSummary) string { return i.Key }), want) {
		t.Errorf("agenda unverified = %+v", ag.Projects[0].UserTurn.Unverified)
	}

	// ListUnverifiedIssues for project and scoped to milestone.
	allApp, err := s.ListUnverifiedIssues(ctx, "APP", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := keysOf(allApp, func(i model.IssueSummary) string { return i.Key }); !reflect.DeepEqual(got, want) {
		t.Errorf("ListUnverifiedIssues(APP, nil) = %v, want %v", got, want)
	}
	msOnly, err := s.ListUnverifiedIssues(ctx, "APP", &ms.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := keysOf(msOnly, func(i model.IssueSummary) string { return i.Key }); !reflect.DeepEqual(got, []string{iBacklog.Key, iDone.Key}) {
		t.Errorf("ListUnverifiedIssues(APP, ms) = %v, want [%s %s]", got, iBacklog.Key, iDone.Key)
	}

	// Empty project returns [] not nil.
	mustProject(t, s, ctx, "EMPTY")
	emptyOv, _ := s.Overview(ctx, "EMPTY", 10)
	if emptyOv.Unverified.Total != 0 || emptyOv.Unverified.Items == nil {
		t.Errorf("empty section must be [] not null: %+v", emptyOv.Unverified)
	}
}
