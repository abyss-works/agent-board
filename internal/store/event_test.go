package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

func decodeEventData(t *testing.T, ev model.Event) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(ev.Data, &m); err != nil {
		t.Fatalf("unmarshal event %d (%s) data %q: %v", ev.ID, ev.Kind, string(ev.Data), err)
	}
	return m
}

func stringSliceField(t *testing.T, m map[string]any, key string) []string {
	t.Helper()
	raw, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("field %q is %T, want []any", key, raw)
	}
	out := make([]string, len(arr))
	for i, v := range arr {
		out[i] = v.(string)
	}
	return out
}

func TestEventsAllKindsAndPayloads(t *testing.T) {
	s, ctx := seeded(t)
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })

	var hooked []model.Event
	s.SetEventHook(func(evs []model.Event) {
		// Verify hook runs after writeMu is released by doing a read inside hook.
		if _, err := s.LatestEventID(ctx); err != nil {
			t.Errorf("LatestEventID inside hook: %v", err)
		}
		hooked = append(hooked, evs...)
	})

	latest0, err := s.LatestEventID(ctx)
	if err != nil || latest0 != 0 {
		t.Fatalf("initial LatestEventID = %d, %v", latest0, err)
	}

	// 1. Project create & update
	if _, err := s.CreateProject(ctx, trOwner, "APP", "Example App"); err != nil {
		t.Fatal(err)
	}
	renamed := "Example App 2"
	if _, err := s.UpdateProject(ctx, trOwner, "APP", ProjectPatch{Name: &renamed}); err != nil {
		t.Fatal(err)
	}

	// 2. Topic create & update
	tp, err := s.CreateTopic(ctx, trOwner, "APP", TopicInput{Name: "backend"})
	if err != nil {
		t.Fatal(err)
	}
	tpName := "api"
	if _, err := s.UpdateTopic(ctx, trOwner, tp.ID, TopicPatch{Name: &tpName}); err != nil {
		t.Fatal(err)
	}

	// 3. Milestone create & update
	ms, err := s.CreateMilestone(ctx, trOwner, "APP", MilestoneInput{Name: "v1.0"})
	if err != nil {
		t.Fatal(err)
	}
	msDesc := "first release"
	if _, err := s.UpdateMilestone(ctx, trOwner, ms.ID, MilestonePatch{Description: &msDesc}); err != nil {
		t.Fatal(err)
	}

	// 4. Issue create, update, verify, comment
	iss, err := s.CreateIssue(ctx, trClaude, IssueInput{
		Project:           "APP",
		Title:             "implement events",
		Body:              "## 배경\n배경\n\n## 할 일\n- 구현\n\n## 완료 조건\n- 통과",
		VerificationState: model.VerificationPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	newTitle := "implement event stream"
	if _, changed, err := s.UpdateIssue(ctx, trOwner, iss.Key, IssuePatch{Title: &newTitle}, nil); err != nil || !changed {
		t.Fatalf("UpdateIssue: changed=%v err=%v", changed, err)
	}
	if _, changed, err := s.VerifyIssue(ctx, trOwner, iss.Key, nil); err != nil || !changed {
		t.Fatalf("VerifyIssue: changed=%v err=%v", changed, err)
	}
	cmt, err := s.AddComment(ctx, trClaude, iss.Key, "looks good", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 5. Document create (single + batch), archive, relation, reresolve
	docAName := "2026-11-20-0900-decision-alpha.md"
	docBName := "2026-11-20-0901-decision-beta.md"
	docCName := "legacy/imported-note.md"
	docAText := "---\nkind: decision\ndate: 2026-11-20 09:00\ntitle: Alpha\n---\nalpha body\n"
	docBText := "---\nkind: decision\ndate: 2026-11-20 09:01\ntitle: Beta\n---\nbeta body\n"
	docCText := "---\nkind: decision\ndate: 2026-11-20 08:00\ntitle: Imported\nrelated: [2026-11-20-0902-decision-gamma.md]\n---\nimported\n"
	docGammaName := "2026-11-20-0902-decision-gamma.md"
	docGammaText := "---\nkind: decision\ndate: 2026-11-20 09:02\ntitle: Gamma\n---\ngamma body\n"

	if _, created, err := s.PutDocument(ctx, trOwner, "APP", docAName, DocumentInput{Content: docAText}); err != nil || !created {
		t.Fatalf("PutDocument A: created=%v err=%v", created, err)
	}
	importedTrue := true
	if _, err := s.PutDocumentsBatch(ctx, trOwner, "APP", []BatchDocumentInput{
		{Name: docBName, DocumentInput: DocumentInput{Content: docBText}},
		{Name: docCName, DocumentInput: DocumentInput{Content: docCText, Args: model.DocumentArgs{Imported: &importedTrue}}},
	}); err != nil {
		t.Fatalf("PutDocumentsBatch: %v", err)
	}
	if _, err := s.SetDocumentArchived(ctx, trOwner, "APP", docAName, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeDocumentRelation(ctx, trOwner, "APP", docBName, model.RelAbout, iss.Key, true); err != nil {
		t.Fatal(err)
	}
	// Putting gamma triggers reresolveRelations on docCName -> document.updated!
	if _, created, err := s.PutDocument(ctx, trOwner, "APP", docGammaName, DocumentInput{Content: docGammaText}); err != nil || !created {
		t.Fatalf("PutDocument Gamma: created=%v err=%v", created, err)
	}

	// 6. Message create (with to & without to), read, delete
	msg1, err := s.CreateMessage(ctx, trClaude, "APP", MessageInput{To: "codex", Title: "hello", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	msg2, err := s.CreateMessage(ctx, trClaude, "APP", MessageInput{Title: "broadcast", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AckMessage(ctx, trOwner, msg1.ID, "APP"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteMessage(ctx, trOwner, msg1.ID); err != nil {
		t.Fatal(err)
	}

	// 7. Session created, updated, waiting clear, child session
	if err := s.TouchSession(ctx, trClaude, now); err != nil {
		t.Fatal(err)
	}
	// Existing session touch should NOT emit event.
	now = now.Add(2 * time.Minute)
	if err := s.TouchSession(ctx, trClaude, now); err != nil {
		t.Fatal(err)
	}
	waitState := model.SessionStateWaitingInput
	waitReason := "need answer"
	if _, err := s.UpdateSession(ctx, trOwner, trClaude.Actor, trClaude.SessionID, SessionPatch{
		State:       Optional[string]{Set: true, Value: &waitState},
		StateReason: Optional[string]{Set: true, Value: &waitReason},
	}); err != nil {
		t.Fatal(err)
	}
	if _, cleared, err := s.ClearSessionWaiting(ctx, trClaude.Actor, trClaude.SessionID); err != nil || !cleared {
		t.Fatalf("ClearSessionWaiting: cleared=%v err=%v", cleared, err)
	}
	if _, err := s.CreateChildSession(ctx, trClaude, ChildSessionInput{SessionID: "claude-child"}); err != nil {
		t.Fatal(err)
	}

	// 8. Claim acquired & released (ClaimIssue, ReleaseClaim, expired, CreateIssue doing, UpdateIssue status sync)
	cl1, created, err := s.ClaimIssue(ctx, trClaude, iss.Key)
	if err != nil || !created {
		t.Fatalf("ClaimIssue: created=%v err=%v", created, err)
	}
	if _, err := s.ReleaseClaim(ctx, trClaude, iss.Key, model.ClaimOutcomeDone, nil); err != nil {
		t.Fatal(err)
	}
	// Create issue in doing -> issue.created + session.created (new session) + claim.acquired
	trNew := model.Trace{Actor: "claude", SessionID: "fresh-sess", ClientHost: "devbox"}
	issDoing, err := s.CreateIssue(ctx, trNew, IssueInput{
		Project: "APP",
		Title:   "doing issue",
		Body:    "## 배경\n배경\n\n## 할 일\n- 구현\n\n## 완료 조건\n- 통과",
		Status:  model.StatusDoing,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Expire fresh-sess and let owner claim -> claim.released (expired) + session.created (owner) + claim.acquired
	now = now.Add(model.SessionStaleAfter + time.Minute)
	if _, created, err := s.ClaimIssue(ctx, trOwner, issDoing.Key); err != nil || !created {
		t.Fatalf("ClaimIssue takeover expired: created=%v err=%v", created, err)
	}
	// Owner transitions to done -> claim.released (done) + issue.updated
	doneStatus := model.StatusDone
	if _, changed, err := s.UpdateIssue(ctx, trOwner, issDoing.Key, IssuePatch{Status: &doneStatus}, nil); err != nil || !changed {
		t.Fatalf("UpdateIssue done: changed=%v err=%v", changed, err)
	}

	all, nextAfter, err := s.ListEvents(ctx, EventFilter{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(hooked) {
		t.Fatalf("ListEvents len=%d != hooked len=%d", len(all), len(hooked))
	}
	for i := range all {
		if all[i].ID != hooked[i].ID || all[i].Kind != hooked[i].Kind || all[i].Subject != hooked[i].Subject {
			t.Errorf("event[%d] db=%+v hooked=%+v", i, all[i], hooked[i])
		}
		if i > 0 && all[i].ID <= all[i-1].ID {
			t.Errorf("event IDs not strictly ascending at %d: %d <= %d", i, all[i].ID, all[i-1].ID)
		}
	}
	if nextAfter != all[len(all)-1].ID {
		t.Errorf("nextAfter = %d, want %d", nextAfter, all[len(all)-1].ID)
	}
	latest, err := s.LatestEventID(ctx)
	if err != nil || latest != nextAfter {
		t.Errorf("LatestEventID = %d, err=%v, want %d", latest, err, nextAfter)
	}

	find := func(kind, subject string) []model.Event {
		var out []model.Event
		for _, e := range all {
			if e.Kind == kind && e.Subject == subject {
				out = append(out, e)
			}
		}
		return out
	}

	// Spot-check every event kind and its payload contract.
	if evs := find("project.created", "APP"); len(evs) != 1 {
		t.Errorf("project.created = %d, want 1", len(evs))
	} else {
		d := decodeEventData(t, evs[0])
		if d["activity_id"] == nil || len(stringSliceField(t, d, "fields")) == 0 {
			t.Errorf("project.created data = %v", d)
		}
		if evs[0].Project == nil || *evs[0].Project != "APP" {
			t.Errorf("project.created project = %v", evs[0].Project)
		}
	}
	if evs := find("project.updated", "APP"); len(evs) != 1 {
		t.Errorf("project.updated = %d, want 1", len(evs))
	} else {
		d := decodeEventData(t, evs[0])
		if got := stringSliceField(t, d, "fields"); !reflect.DeepEqual(got, []string{"name"}) {
			t.Errorf("project.updated fields = %v", got)
		}
	}
	if evs := find("topic.created", strconv.FormatInt(tp.ID, 10)); len(evs) != 1 {
		t.Errorf("topic.created = %d, want 1", len(evs))
	}
	if evs := find("topic.updated", strconv.FormatInt(tp.ID, 10)); len(evs) != 1 {
		t.Errorf("topic.updated = %d, want 1", len(evs))
	} else if got := stringSliceField(t, decodeEventData(t, evs[0]), "fields"); !reflect.DeepEqual(got, []string{"name"}) {
		t.Errorf("topic.updated fields = %v", got)
	}
	if evs := find("milestone.created", strconv.FormatInt(ms.ID, 10)); len(evs) != 1 {
		t.Errorf("milestone.created = %d, want 1", len(evs))
	}
	if evs := find("milestone.updated", strconv.FormatInt(ms.ID, 10)); len(evs) != 1 {
		t.Errorf("milestone.updated = %d, want 1", len(evs))
	} else if got := stringSliceField(t, decodeEventData(t, evs[0]), "fields"); !reflect.DeepEqual(got, []string{"description"}) {
		t.Errorf("milestone.updated fields = %v", got)
	}
	if evs := find("issue.created", iss.Key); len(evs) != 1 {
		t.Errorf("issue.created = %d, want 1", len(evs))
	}
	if evs := find("issue.updated", iss.Key); len(evs) != 2 {
		t.Errorf("issue.updated = %d, want 2 (title + verify)", len(evs))
	} else {
		if f := stringSliceField(t, decodeEventData(t, evs[1]), "fields"); !reflect.DeepEqual(f, []string{"verification_state"}) {
			t.Errorf("verify issue.updated fields = %v", f)
		}
	}
	if evs := find("issue.commented", iss.Key); len(evs) != 1 {
		t.Errorf("issue.commented = %d, want 1", len(evs))
	} else {
		d := decodeEventData(t, evs[0])
		if int64(d["activity_id"].(float64)) != cmt.ID {
			t.Errorf("issue.commented activity_id = %v, want %d", d["activity_id"], cmt.ID)
		}
	}
	for _, name := range []string{docAName, docBName, docCName, docGammaName} {
		if evs := find("document.created", name); len(evs) != 1 {
			t.Errorf("document.created(%s) = %d, want 1", name, len(evs))
		}
	}
	for _, name := range []string{docAName, docBName, docCName} {
		if evs := find("document.updated", name); len(evs) != 1 {
			t.Errorf("document.updated(%s) = %d, want 1", name, len(evs))
		} else if decodeEventData(t, evs[0])["activity_id"] == nil {
			t.Errorf("document.updated(%s) missing activity_id", name)
		}
	}
	if evs := find("message.created", strconv.FormatInt(msg1.ID, 10)); len(evs) != 1 {
		t.Errorf("message.created(msg1) = %d, want 1", len(evs))
	} else if d := decodeEventData(t, evs[0]); d["to"] != "codex" {
		t.Errorf("message.created(msg1) data = %v", d)
	}
	if evs := find("message.created", strconv.FormatInt(msg2.ID, 10)); len(evs) != 1 {
		t.Errorf("message.created(msg2) = %d, want 1", len(evs))
	} else if d := decodeEventData(t, evs[0]); d["to"] != nil {
		t.Errorf("message.created(msg2) data = %v, want nil/omitted to", d)
	}
	if evs := find("message.read", strconv.FormatInt(msg1.ID, 10)); len(evs) != 1 {
		t.Errorf("message.read = %d, want 1", len(evs))
	} else if d := decodeEventData(t, evs[0]); d["reader"] != "APP" {
		t.Errorf("message.read data = %v", d)
	}
	if evs := find("message.deleted", strconv.FormatInt(msg1.ID, 10)); len(evs) != 1 {
		t.Errorf("message.deleted = %d, want 1", len(evs))
	}

	// Sessions: created once per new session (never on second TouchSession).
	if evs := find("session.created", trClaude.Actor+"/"+trClaude.SessionID); len(evs) != 1 {
		t.Errorf("session.created(claude) = %d, want 1", len(evs))
	} else if evs[0].Project != nil {
		t.Errorf("session.created project = %v, want nil", evs[0].Project)
	}
	if evs := find("session.created", "claude/claude-child"); len(evs) != 1 {
		t.Errorf("session.created(child) = %d, want 1", len(evs))
	}
	if evs := find("session.updated", trClaude.Actor+"/"+trClaude.SessionID); len(evs) != 3 {
		t.Errorf("session.updated(claude) = %d, want 3 (set waiting + clear waiting + parent role)", len(evs))
	} else {
		// First: UpdateSession called by trOwner -> actor should be owner!
		if evs[0].Actor != trOwner.Actor || evs[0].SessionID != trOwner.SessionID {
			t.Errorf("UpdateSession event trace = %s/%s, want %s/%s", evs[0].Actor, evs[0].SessionID, trOwner.Actor, trOwner.SessionID)
		}
		// Second: ClearSessionWaiting
		if f := stringSliceField(t, decodeEventData(t, evs[1]), "fields"); !reflect.DeepEqual(f, []string{"state", "state_at", "state_reason"}) {
			t.Errorf("ClearSessionWaiting fields = %v", f)
		}
		// Third: CreateChildSession parent role promotion
		if f := stringSliceField(t, decodeEventData(t, evs[2]), "fields"); !reflect.DeepEqual(f, []string{"role"}) {
			t.Errorf("parent role promotion fields = %v", f)
		}
	}

	// Claims
	if evs := find("claim.acquired", iss.Key); len(evs) != 1 {
		t.Errorf("claim.acquired(%s) = %d, want 1", iss.Key, len(evs))
	} else if int64(decodeEventData(t, evs[0])["claim_id"].(float64)) != cl1.ID {
		t.Errorf("claim.acquired data = %s", string(evs[0].Data))
	}
	if evs := find("claim.released", iss.Key); len(evs) != 1 {
		t.Errorf("claim.released(%s) = %d, want 1", iss.Key, len(evs))
	} else if d := decodeEventData(t, evs[0]); d["outcome"] != model.ClaimOutcomeDone {
		t.Errorf("claim.released data = %v", d)
	}
	if evs := find("claim.released", issDoing.Key); len(evs) != 2 {
		t.Errorf("claim.released(%s) = %d, want 2 (expired + done)", issDoing.Key, len(evs))
	} else {
		o0 := decodeEventData(t, evs[0])["outcome"]
		o1 := decodeEventData(t, evs[1])["outcome"]
		if o0 != model.ClaimOutcomeExpired || o1 != model.ClaimOutcomeDone {
			t.Errorf("claim.released outcomes = %v, %v", o0, o1)
		}
	}
}

func TestEventsNoOpDoesNotEmit(t *testing.T) {
	s, ctx := seeded(t)
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })

	mustProject(t, s, ctx, "APP")
	tp := mustTopic(t, s, "APP", "t1", nil)
	ms := mustMilestone(t, s, "APP", "m1")
	iss := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "APP", Title: "no-op test", VerificationState: model.VerificationPending})
	if _, _, err := s.VerifyIssue(ctx, trOwner, iss.Key, nil); err != nil {
		t.Fatal(err)
	}
	docName := "2026-11-20-1000-decision-noop.md"
	docText := "---\nkind: decision\ndate: 2026-11-20 10:00\ntitle: Noop\n---\nbody\n"
	if _, _, err := s.PutDocument(ctx, trOwner, "APP", docName, DocumentInput{Content: docText}); err != nil {
		t.Fatal(err)
	}
	msg, err := s.CreateMessage(ctx, trOwner, "APP", MessageInput{Title: "m", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AckMessage(ctx, trOwner, msg.ID, "APP"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteMessage(ctx, trOwner, msg.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchSession(ctx, trOwner, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ClaimIssue(ctx, trOwner, iss.Key); err != nil {
		t.Fatal(err)
	}

	before, err := s.LatestEventID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hookCalls := 0
	s.SetEventHook(func([]model.Event) { hookCalls++ })

	// Now perform every no-op write.
	sameProj := "APP"
	if _, err := s.UpdateProject(ctx, trOwner, "APP", ProjectPatch{Name: &sameProj}); err != nil {
		t.Fatal(err)
	}
	sameTopic := "t1"
	if _, err := s.UpdateTopic(ctx, trOwner, tp.ID, TopicPatch{Name: &sameTopic}); err != nil {
		t.Fatal(err)
	}
	sameMS := "m1"
	if _, err := s.UpdateMilestone(ctx, trOwner, ms.ID, MilestonePatch{Name: &sameMS}); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := s.UpdateIssue(ctx, trOwner, iss.Key, IssuePatch{}, nil); err != nil || changed {
		t.Fatalf("no-op UpdateIssue: changed=%v err=%v", changed, err)
	}
	if _, changed, err := s.VerifyIssue(ctx, trOwner, iss.Key, nil); err != nil || changed {
		t.Fatalf("no-op VerifyIssue: changed=%v err=%v", changed, err)
	}
	if _, created, err := s.PutDocument(ctx, trOwner, "APP", docName, DocumentInput{Content: docText}); err != nil || created {
		t.Fatalf("no-op PutDocument: created=%v err=%v", created, err)
	}
	if _, err := s.SetDocumentArchived(ctx, trOwner, "APP", docName, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeDocumentRelation(ctx, trOwner, "APP", docName, model.RelAbout, iss.Key, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AckMessage(ctx, trOwner, msg.ID, "APP"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteMessage(ctx, trOwner, msg.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchSession(ctx, trOwner, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSession(ctx, trOwner, trOwner.Actor, trOwner.SessionID, SessionPatch{}); err != nil {
		t.Fatal(err)
	}
	if _, cleared, err := s.ClearSessionWaiting(ctx, trOwner.Actor, trOwner.SessionID); err != nil || cleared {
		t.Fatalf("no-op ClearSessionWaiting: cleared=%v err=%v", cleared, err)
	}
	if _, created, err := s.ClaimIssue(ctx, trOwner, iss.Key); err != nil || created {
		t.Fatalf("no-op ClaimIssue: created=%v err=%v", created, err)
	}

	after, err := s.LatestEventID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after != before || hookCalls != 0 {
		t.Errorf("no-op writes emitted events: before=%d after=%d hookCalls=%d", before, after, hookCalls)
	}
}

func TestEventsRollbackDoesNotPersistOrNotify(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "APP")
	before, err := s.LatestEventID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hookCalls := 0
	s.SetEventHook(func([]model.Event) { hookCalls++ })

	wantErr := errors.New("boom")
	err = s.write(ctx, func(tx *sql.Tx) error {
		if err := s.emitEventTx(ctx, tx, s.stamp(), nil, nil, "session.created", "owner/rollback", "owner", "rollback", nil); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("write err = %v, want %v", err, wantErr)
	}
	if hookCalls != 0 {
		t.Errorf("hookCalls = %d, want 0 after rollback", hookCalls)
	}
	items, _, err := s.ListEvents(ctx, EventFilter{After: before})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("rolled back events persisted: %+v", items)
	}
}

func TestListEventsFiltersAndPagination(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AAA")
	mustProject(t, s, ctx, "BBB")
	mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AAA", Title: "a1"})
	mustIssue(t, s, ctx, trOwner, IssueInput{Project: "BBB", Title: "b1"})
	if _, err := s.CreateMessage(ctx, trOwner, "AAA", MessageInput{Title: "m1", Body: "body"}); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchSession(ctx, trOwner, time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	// Filter by project AAA: excludes BBB and session.* (project=null).
	aaa, nextAAA, err := s.ListEvents(ctx, EventFilter{Project: "AAA"})
	if err != nil {
		t.Fatal(err)
	}
	if len(aaa) != 3 { // project.created, issue.created, message.created
		t.Fatalf("AAA events = %d, want 3", len(aaa))
	}
	for _, ev := range aaa {
		if ev.Project == nil || *ev.Project != "AAA" {
			t.Errorf("event project = %v, want AAA", ev.Project)
		}
	}
	if nextAAA != aaa[len(aaa)-1].ID {
		t.Errorf("nextAAA = %d, want %d", nextAAA, aaa[len(aaa)-1].ID)
	}

	// Non-existent project returns empty slice and next_after == after.
	none, nextNone, err := s.ListEvents(ctx, EventFilter{After: 5, Project: "NOPE"})
	if err != nil {
		t.Fatal(err)
	}
	if none == nil || len(none) != 0 || nextNone != 5 {
		t.Errorf("NOPE project = (%v, %d), want ([], 5)", none, nextNone)
	}

	// Multiple kind prefixes (OR): "issue." and "message.created".
	mixed, _, err := s.ListEvents(ctx, EventFilter{Kinds: []string{"issue.", "message.created"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(mixed) != 3 {
		t.Fatalf("kind filter len = %d, want 3", len(mixed))
	}
	for _, ev := range mixed {
		if ev.Kind != "issue.created" && ev.Kind != "message.created" {
			t.Errorf("unexpected kind %q", ev.Kind)
		}
	}

	// Pagination with limit & after.
	page1, cursor1, err := s.ListEvents(ctx, EventFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || cursor1 != page1[1].ID {
		t.Fatalf("page1 = (%d, %d)", len(page1), cursor1)
	}
	page2, cursor2, err := s.ListEvents(ctx, EventFilter{After: cursor1, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 || page2[0].ID <= cursor1 || cursor2 != page2[1].ID {
		t.Fatalf("page2 = (%+v, %d)", page2, cursor2)
	}
	// After latest returns empty and preserves after.
	latest, _ := s.LatestEventID(ctx)
	tail, tailNext, err := s.ListEvents(ctx, EventFilter{After: latest})
	if err != nil {
		t.Fatal(err)
	}
	if tail == nil || len(tail) != 0 || tailNext != latest {
		t.Errorf("tail = (%v, %d), want ([], %d)", tail, tailNext, latest)
	}
}

func TestFingerprintReflectsEventsAndIgnoresSessionTouch(t *testing.T) {
	s, ctx := seeded(t)
	t0 := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return t0 })

	if err := s.TouchSession(ctx, trClaude, t0); err != nil {
		t.Fatal(err)
	}
	fp1, err := s.FingerprintNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Existing session touch must NOT change fingerprint.
	if err := s.TouchSession(ctx, trClaude, t0.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	fp2, err := s.FingerprintNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fp2 != fp1 {
		t.Errorf("fingerprint changed on existing session touch: %q -> %q", fp1, fp2)
	}

	// Inserting an event directly changes fingerprint.
	if err := s.write(ctx, func(tx *sql.Tx) error {
		return s.emitEventTx(ctx, tx, s.stamp(), nil, nil, "session.updated", "claude/s", "claude", "s", map[string]any{"fields": []string{"role"}})
	}); err != nil {
		t.Fatal(err)
	}
	fp3, err := s.FingerprintNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fp3 == fp2 {
		t.Errorf("fingerprint unchanged after event insert: %q", fp3)
	}
	_ = slices.Contains([]string{fp1, fp2, fp3}, fp3)
}
