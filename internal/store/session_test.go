package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

var trSess = model.Trace{Actor: "claude", SessionID: "claude-abc", ClientHost: "desk"}

func mustSession(t *testing.T, s *Store, actor, id string) *model.Session {
	t.Helper()
	got, err := s.GetSession(t.Context(), actor, id)
	if err != nil {
		t.Fatalf("GetSession(%s/%s): %v", actor, id, err)
	}
	return got
}

func mustTouch(t *testing.T, s *Store, tr model.Trace, at time.Time) {
	t.Helper()
	if err := s.TouchSession(t.Context(), tr, at); err != nil {
		t.Fatalf("TouchSession(%v): %v", tr, err)
	}
}

// fixClock stops the clock of a store at now.
func fixClock(s *Store, now time.Time) { s.SetClock(func() time.Time { return now }) }

func validationField(t *testing.T, err error, field, reason string) {
	t.Helper()
	var verr *model.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want a validation error on %s", err, field)
	}
	for _, f := range verr.Fields {
		if f.Field == field && (reason == "" || f.Reason == reason) {
			return
		}
	}
	t.Errorf("validation fields = %+v, want %s (%s)", verr.Fields, field, reason)
}

func TestTouchSessionCreatesThenRefreshesLastSeenOnly(t *testing.T) {
	s, ctx := seeded(t)
	at := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	fixClock(s, at)
	mustTouch(t, s, trSess, at)
	got := mustSession(t, s, "claude", "claude-abc")
	if got.FirstSeenAt != model.NewTime(at) || got.LastSeenAt != got.FirstSeenAt || got.UpdatedAt != got.FirstSeenAt || got.LastHost != "desk" {
		t.Errorf("created = %+v", got)
	}
	if got.Parent != nil || got.Role != nil || got.Issue != nil || got.Location != nil || got.LastProject != nil || !got.Named || got.Stale {
		t.Errorf("created optional fields = %+v", got)
	}

	later := at.Add(2 * time.Minute)
	tr := trSess
	tr.ClientHost = "laptop"
	mustTouch(t, s, tr, later)
	got = mustSession(t, s, "claude", "claude-abc")
	if got.LastSeenAt != model.NewTime(later) || got.LastHost != "laptop" || got.FirstSeenAt != model.NewTime(at) || got.UpdatedAt != model.NewTime(at) {
		t.Errorf("refreshed = %+v", got)
	}
	if _, err := s.GetSession(ctx, "claude", "none"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing session: %v", err)
	}
}

func TestSessionNamedAndStale(t *testing.T) {
	s, _ := seeded(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	fixClock(s, now)
	mustTouch(t, s, model.Trace{Actor: "claude", SessionID: "cli-20261120", ClientHost: "h"}, now.Add(-31*time.Minute))
	mustTouch(t, s, model.Trace{Actor: "claude", SessionID: "claude-abc", ClientHost: "h"}, now.Add(-29*time.Minute))
	if got := mustSession(t, s, "claude", "cli-20261120"); got.Named || !got.Stale {
		t.Errorf("cli session: named=%v stale=%v", got.Named, got.Stale)
	}
	if got := mustSession(t, s, "claude", "claude-abc"); !got.Named || got.Stale {
		t.Errorf("named session: named=%v stale=%v", got.Named, got.Stale)
	}
}

func TestSessionLastProjectIsTheLatestRecord(t *testing.T) {
	s, ctx := seeded(t)
	t0 := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	fixClock(s, t0)
	mustProject(t, s, ctx, "AAA")
	mustProject(t, s, ctx, "BBB")
	mustTouch(t, s, trSess, t0)
	if got := mustSession(t, s, "claude", "claude-abc"); got.LastProject != nil {
		t.Errorf("last_project without records = %v", *got.LastProject)
	}
	fixClock(s, t0.Add(time.Minute))
	i := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AAA", Title: "a"})
	if _, err := s.AddComment(ctx, trSess, i.Key, "댓글", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := mustSession(t, s, "claude", "claude-abc"); got.LastProject == nil || *got.LastProject != "AAA" {
		t.Errorf("last_project after a comment = %v", got.LastProject)
	}
	fixClock(s, t0.Add(2*time.Minute))
	m := mustMilestone(t, s, "BBB", "m1")
	name := "m2"
	if _, err := s.UpdateMilestone(ctx, trSess, m.ID, MilestonePatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if got := mustSession(t, s, "claude", "claude-abc"); got.LastProject == nil || *got.LastProject != "BBB" {
		t.Errorf("last_project after a milestone write = %v", got.LastProject)
	}
}

func TestUpdateSession(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "APP")
	i := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "APP", Title: "a"})
	at := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	fixClock(s, at)
	mustTouch(t, s, trSess, at)
	mustTouch(t, s, trOwner, at)

	if _, err := s.UpdateSession(ctx, trSess, "claude", "none", SessionPatch{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing session: %v", err)
	}
	// An empty patch changes nothing.
	fixClock(s, at.Add(time.Minute))
	got, err := s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{})
	if err != nil || got.UpdatedAt != model.NewTime(at) {
		t.Errorf("empty patch: %+v %v", got, err)
	}

	_, err = s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{Parent: Optional[model.SessionRef]{Set: true, Value: &model.SessionRef{Actor: "claude", SessionID: "nope"}}})
	validationField(t, err, "parent", "unknown_session")
	_, err = s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{Parent: Optional[model.SessionRef]{Set: true, Value: &model.SessionRef{Actor: "claude", SessionID: "claude-abc"}}})
	validationField(t, err, "parent", "cycle")
	boss := "boss"
	_, err = s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{Role: Optional[string]{Set: true, Value: &boss}})
	validationField(t, err, "role", "")
	missing := "APP-999"
	_, err = s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{Issue: Optional[string]{Set: true, Value: &missing}})
	validationField(t, err, "issue", "unknown_issue")

	role, key := "runner", i.Key
	remote, dirty := "https://user:tok@github.com/a/b.git", 3
	got, err = s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{
		Parent:   Optional[model.SessionRef]{Set: true, Value: &model.SessionRef{Actor: "owner", SessionID: "s-owner"}},
		Role:     Optional[string]{Set: true, Value: &role},
		Issue:    Optional[string]{Set: true, Value: &key},
		Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{RepoRemote: &remote, DirtyCount: &dirty}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Parent == nil || *got.Parent != (model.SessionRef{Actor: "owner", SessionID: "s-owner"}) || deref(got.Role) != "runner" || deref(got.Issue) != i.Key {
		t.Errorf("patched = %+v", got)
	}
	if got.Location == nil || deref(got.Location.RepoRemote) != "https://github.com/a/b.git" || got.Location.DirtyCount == nil || *got.Location.DirtyCount != 3 ||
		got.Location.ReportedAt != model.NewTime(at.Add(time.Minute)) || got.Location.Branch != nil {
		t.Errorf("location = %+v", got.Location)
	}
	if got.UpdatedAt != model.NewTime(at.Add(time.Minute)) || got.Children != nil {
		t.Errorf("updated_at = %s children = %v", got.UpdatedAt, got.Children)
	}

	// The same values again change nothing; null clears.
	fixClock(s, at.Add(2*time.Minute))
	got, err = s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{Role: Optional[string]{Set: true, Value: &role}})
	if err != nil || got.UpdatedAt != model.NewTime(at.Add(time.Minute)) {
		t.Errorf("same role: %s %v", got.UpdatedAt, err)
	}
	got, err = s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{Issue: Optional[string]{Set: true}, Location: Optional[model.SessionLocation]{Set: true}, Parent: Optional[model.SessionRef]{Set: true}})
	if err != nil || got.Issue != nil || got.Location != nil || got.Parent != nil || got.UpdatedAt != model.NewTime(at.Add(2*time.Minute)) {
		t.Errorf("cleared = %+v %v", got, err)
	}
}

func TestUpdateSessionRejectsADescendantAsParent(t *testing.T) {
	s, ctx := seeded(t)
	mustTouch(t, s, trSess, time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC))
	if _, err := s.CreateChildSession(ctx, trSess, ChildSessionInput{SessionID: "claude-abc.b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateChildSession(ctx, model.Trace{Actor: "claude", SessionID: "claude-abc.b", ClientHost: "desk"}, ChildSessionInput{SessionID: "claude-abc.c"}); err != nil {
		t.Fatal(err)
	}
	for _, child := range []string{"claude-abc.b", "claude-abc.c"} {
		_, err := s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{Parent: Optional[model.SessionRef]{Set: true, Value: &model.SessionRef{Actor: "claude", SessionID: child}}})
		validationField(t, err, "parent", "cycle")
	}
}

func TestCreateChildSession(t *testing.T) {
	s, ctx := seeded(t)
	at := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	fixClock(s, at)
	mustTouch(t, s, trSess, at)
	fixClock(s, at.Add(time.Minute))
	child, err := s.CreateChildSession(ctx, trSess, ChildSessionInput{SessionID: "claude-abc.x1y2z3"})
	if err != nil {
		t.Fatal(err)
	}
	now := model.NewTime(at.Add(time.Minute))
	if child.Actor != "claude" || child.SessionID != "claude-abc.x1y2z3" || deref(child.Role) != "subagent" || child.Parent == nil ||
		*child.Parent != (model.SessionRef{Actor: "claude", SessionID: "claude-abc"}) || child.FirstSeenAt != now || child.LastSeenAt != now ||
		child.UpdatedAt != now || child.LastHost != "desk" {
		t.Errorf("child = %+v", child)
	}
	parent := mustSession(t, s, "claude", "claude-abc")
	if deref(parent.Role) != "main" || parent.UpdatedAt != now {
		t.Errorf("parent = %+v", parent)
	}
	if _, err := s.CreateChildSession(ctx, trSess, ChildSessionInput{SessionID: "claude-abc.x1y2z3"}); !errors.Is(err, ErrSessionExists) {
		t.Errorf("same id again: %v", err)
	}
	_, err = s.CreateChildSession(ctx, trSess, ChildSessionInput{SessionID: "bad id"})
	validationField(t, err, "session_id", "")
	boss := "boss"
	_, err = s.CreateChildSession(ctx, trSess, ChildSessionInput{SessionID: "claude-abc.r", Role: &boss})
	validationField(t, err, "role", "")
	missing := "APP-999"
	_, err = s.CreateChildSession(ctx, trSess, ChildSessionInput{SessionID: "claude-abc.r", Issue: &missing})
	validationField(t, err, "issue", "unknown_issue")

	fixClock(s, at.Add(2*time.Minute))
	if _, err := s.CreateChildSession(ctx, trSess, ChildSessionInput{SessionID: "claude-abc.second"}); err != nil {
		t.Fatal(err)
	}
	got := mustSession(t, s, "claude", "claude-abc")
	if got.Children == nil || len(*got.Children) != 2 || (*got.Children)[0].SessionID != "claude-abc.x1y2z3" || (*got.Children)[1].SessionID != "claude-abc.second" {
		t.Errorf("children = %+v", got.Children)
	}
	if (*got.Children)[0].Children != nil {
		t.Error("a child carries children")
	}
	if got.UpdatedAt != now {
		t.Errorf("a parent already main changed: %s", got.UpdatedAt)
	}
}

func TestCreateChildSessionKeepsAParentRole(t *testing.T) {
	s, ctx := seeded(t)
	mustTouch(t, s, trSess, time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC))
	runner := "runner"
	if _, err := s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{Role: Optional[string]{Set: true, Value: &runner}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateChildSession(ctx, trSess, ChildSessionInput{SessionID: "claude-abc.k"}); err != nil {
		t.Fatal(err)
	}
	if got := mustSession(t, s, "claude", "claude-abc"); deref(got.Role) != "runner" {
		t.Errorf("parent role = %v", got.Role)
	}
}

// The parent row is written with the child when the request that names it
// left none.
func TestCreateChildSessionWritesAMissingParent(t *testing.T) {
	s, ctx := seeded(t)
	if _, err := s.CreateChildSession(ctx, trSess, ChildSessionInput{SessionID: "claude-abc.k"}); err != nil {
		t.Fatal(err)
	}
	if got := mustSession(t, s, "claude", "claude-abc"); deref(got.Role) != "main" || got.LastHost != "desk" {
		t.Errorf("parent = %+v", got)
	}
}

func TestListSessions(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "APP")
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	touch := func(actor, id string, ago time.Duration) {
		mustTouch(t, s, model.Trace{Actor: actor, SessionID: id, ClientHost: "h"}, now.Add(-ago))
	}
	touch("claude", "cli-20261120", time.Hour)
	touch("owner", "b-sess", 10*time.Minute)
	touch("claude", "b-sess", 10*time.Minute)
	touch("claude", "a-sess", 5*time.Minute)
	fixClock(s, now)
	if _, err := s.CreateChildSession(ctx, model.Trace{Actor: "claude", SessionID: "a-sess", ClientHost: "h"}, ChildSessionInput{SessionID: "a-sess.kid"}); err != nil {
		t.Fatal(err)
	}
	i := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "APP", Title: "a"})
	if _, err := s.AddComment(ctx, model.Trace{Actor: "claude", SessionID: "b-sess", ClientHost: "h"}, i.Key, "c", nil, nil); err != nil {
		t.Fatal(err)
	}

	ids := func(f SessionFilter) []string {
		t.Helper()
		if f.Limit == 0 {
			f.Limit = 50
		}
		items, total, err := s.ListSessions(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(items))
		for n, it := range items {
			out[n] = it.Actor + "/" + it.SessionID
		}
		if total < len(out) {
			t.Errorf("total %d < %d", total, len(out))
		}
		return out
	}
	eq := func(name string, got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s = %v, want %v", name, got, want)
			return
		}
		for n := range got {
			if got[n] != want[n] {
				t.Errorf("%s = %v, want %v", name, got, want)
				return
			}
		}
	}
	tr, fa := true, false
	eq("all", ids(SessionFilter{}), "claude/a-sess.kid", "claude/a-sess", "claude/b-sess", "owner/b-sess", "claude/cli-20261120")
	eq("active", ids(SessionFilter{Active: &tr}), "claude/a-sess.kid", "claude/a-sess", "claude/b-sess", "owner/b-sess")
	eq("stale", ids(SessionFilter{Active: &fa}), "claude/cli-20261120")
	eq("parent", ids(SessionFilter{Parent: &model.SessionRef{Actor: "claude", SessionID: "a-sess"}}), "claude/a-sess.kid")
	eq("unnamed", ids(SessionFilter{Named: &fa}), "claude/cli-20261120")
	eq("named", ids(SessionFilter{Named: &tr, Actor: "owner"}), "owner/b-sess")
	eq("project", ids(SessionFilter{Project: "APP"}), "claude/b-sess")
	eq("page", ids(SessionFilter{Limit: 2, Offset: 1}), "claude/a-sess", "claude/b-sess")
	if _, _, err := s.ListSessions(ctx, SessionFilter{Project: "NOPE", Limit: 50}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown project: %v", err)
	}
}

func TestFingerprintCoversSessionsButNotLastSeen(t *testing.T) {
	s, ctx := seeded(t)
	at := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	before, err := s.FingerprintNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustTouch(t, s, trSess, at)
	created, _ := s.FingerprintNow(ctx)
	if created == before {
		t.Errorf("creating a session left the fingerprint %q", before)
	}
	mustTouch(t, s, trSess, at.Add(5*time.Minute))
	if touched, _ := s.FingerprintNow(ctx); touched != created {
		t.Errorf("refreshing last_seen_at changed the fingerprint: %q -> %q", created, touched)
	}
	role := "runner"
	if _, err := s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{Role: Optional[string]{Set: true, Value: &role}}); err != nil {
		t.Fatal(err)
	}
	if patched, _ := s.FingerprintNow(ctx); patched == created {
		t.Errorf("patching a session left the fingerprint %q", created)
	}
}

func TestSessionTableKeepsParentPairs(t *testing.T) {
	s, _ := seeded(t)
	_, err := s.db.Exec(`insert into session(actor_id, session_id, first_seen_at, last_seen_at, last_host, parent_actor_id, updated_at)
		values ('claude', 'x', 'a', 'a', 'h', 'claude', 'a')`)
	if err == nil {
		t.Error("a parent actor without a parent session was stored")
	}
	var n sql.NullInt64
	s.db.QueryRow(`select count(*) from session`).Scan(&n)
	if n.Int64 != 0 {
		t.Errorf("sessions = %d", n.Int64)
	}
}

func TestSessionLookupPropagatesContextAndDBErrors(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "APP")
	i := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "APP", Title: "a"})
	at := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	mustTouch(t, s, trSess, at)
	mustTouch(t, s, trOwner, at)

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	verr := &model.ValidationError{}
	if _, err := checkSessionParent(canceled, s.db, model.SessionRef{Actor: "claude", SessionID: "claude-abc"},
		model.SessionRef{Actor: "owner", SessionID: "s-owner"}, verr); !errors.Is(err, context.Canceled) || len(verr.Fields) != 0 {
		t.Errorf("checkSessionParent(canceled) = %v, fields=%+v", err, verr.Fields)
	}
	if _, _, err := sessionIssueID(canceled, s.db, i.Key, verr); !errors.Is(err, context.Canceled) || len(verr.Fields) != 0 {
		t.Errorf("sessionIssueID(canceled) = %v, fields=%+v", err, verr.Fields)
	}

	role := "runner"
	key := i.Key
	for name, patch := range map[string]SessionPatch{
		"parent": {
			Parent: Optional[model.SessionRef]{Set: true, Value: &model.SessionRef{Actor: "owner", SessionID: "s-owner"}},
			Role:   Optional[string]{Set: true, Value: &role},
		},
		"issue": {
			Issue: Optional[string]{Set: true, Value: &key},
			Role:  Optional[string]{Set: true, Value: &role},
		},
	} {
		var ve *model.ValidationError
		if _, err := s.UpdateSession(canceled, trSess, "claude", "claude-abc", patch); !errors.Is(err, context.Canceled) || errors.As(err, &ve) {
			t.Errorf("UpdateSession(%s, canceled) = %v, want context.Canceled", name, err)
		}
	}
	if got := mustSession(t, s, "claude", "claude-abc"); got.Parent != nil || got.Issue != nil || got.Role != nil {
		t.Errorf("session changed after canceled updates: %+v", got)
	}
	if _, err := s.CreateChildSession(canceled, trSess, ChildSessionInput{SessionID: "claude-abc.c", Issue: &key}); !errors.Is(err, context.Canceled) {
		t.Errorf("CreateChildSession(canceled) = %v, want context.Canceled", err)
	}
	if _, err := s.GetSession(ctx, "claude", "claude-abc.c"); !errors.Is(err, ErrNotFound) {
		t.Errorf("child session exists after canceled create: %v", err)
	}
}

func TestUpdateSessionWaitingStateAndClear(t *testing.T) {
	s, ctx := seeded(t)
	t0 := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	fixClock(s, t0)
	mustTouch(t, s, trSess, t0)

	// Setting state_reason when state is NULL fails with requires_state.
	r1 := "배포 승인 대기"
	_, err := s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{
		StateReason: Optional[string]{Set: true, Value: &r1},
	})
	validationField(t, err, "state_reason", "requires_state")

	// Setting state: null with non-null state_reason fails with requires_state.
	_, err = s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{
		State:       Optional[string]{Set: true, Value: nil},
		StateReason: Optional[string]{Set: true, Value: &r1},
	})
	validationField(t, err, "state_reason", "requires_state")

	// Invalid state fails with field state.
	paused := "paused"
	_, err = s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{
		State: Optional[string]{Set: true, Value: &paused},
	})
	validationField(t, err, "state", "")

	// Invalid state_reason values fail with field state_reason.
	stApp := model.SessionStateWaitingApproval
	for _, bad := range []string{"", "   ", strings.Repeat("가", 201), "a\nb", "a\x00b"} {
		b := bad
		_, err = s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{
			State:       Optional[string]{Set: true, Value: &stApp},
			StateReason: Optional[string]{Set: true, Value: &b},
		})
		validationField(t, err, "state_reason", "")
	}

	// Set waiting_approval + reason (with surrounding spaces trimmed).
	t1 := t0.Add(time.Minute)
	fixClock(s, t1)
	fpBefore, err := s.FingerprintNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	padded := "  배포 승인 대기  "
	got, err := s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{
		State:       Optional[string]{Set: true, Value: &stApp},
		StateReason: Optional[string]{Set: true, Value: &padded},
	})
	if err != nil {
		t.Fatal(err)
	}
	if deref(got.State) != stApp || deref(got.StateReason) != "배포 승인 대기" || got.StateAt == nil || *got.StateAt != model.NewTime(t1) || got.UpdatedAt != model.NewTime(t1) {
		t.Errorf("set waiting = %+v", got)
	}
	fpSet, err := s.FingerprintNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fpSet == fpBefore {
		t.Errorf("setting waiting state did not change fingerprint: %q", fpBefore)
	}

	// Updating only state_reason keeps state_at and bumps updated_at.
	t2 := t0.Add(2 * time.Minute)
	fixClock(s, t2)
	r2 := "수정된 사유"
	got, err = s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{
		StateReason: Optional[string]{Set: true, Value: &r2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if deref(got.State) != stApp || deref(got.StateReason) != r2 || got.StateAt == nil || *got.StateAt != model.NewTime(t1) || got.UpdatedAt != model.NewTime(t2) {
		t.Errorf("reason only update = %+v", got)
	}

	// Clearing state_reason alone clears only state_reason and preserves state and state_at.
	t3 := t0.Add(3 * time.Minute)
	fixClock(s, t3)
	got, err = s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{
		StateReason: Optional[string]{Set: true, Value: nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	if deref(got.State) != stApp || got.StateReason != nil || got.StateAt == nil || *got.StateAt != model.NewTime(t1) || got.UpdatedAt != model.NewTime(t3) {
		t.Errorf("clear reason only = %+v", got)
	}

	// Setting a new state without state_reason resets previous state_reason to null and stamps new state_at.
	t4 := t0.Add(4 * time.Minute)
	fixClock(s, t4)
	stIn := model.SessionStateWaitingInput
	if _, err := s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{
		State:       Optional[string]{Set: true, Value: &stIn},
		StateReason: Optional[string]{Set: true, Value: &r1},
	}); err != nil {
		t.Fatal(err)
	}
	t5 := t0.Add(5 * time.Minute)
	fixClock(s, t5)
	stExt := model.SessionStateWaitingExternal
	got, err = s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{
		State: Optional[string]{Set: true, Value: &stExt},
	})
	if err != nil {
		t.Fatal(err)
	}
	if deref(got.State) != stExt || got.StateReason != nil || got.StateAt == nil || *got.StateAt != model.NewTime(t5) || got.UpdatedAt != model.NewTime(t5) {
		t.Errorf("new state without reason = %+v", got)
	}

	// Filter waiting=true / waiting=false.
	mustTouch(t, s, trOwner, t0)
	tr, fa := true, false
	waitingItems, waitingTotal, err := s.ListSessions(ctx, SessionFilter{Waiting: &tr, Limit: 50})
	if err != nil || waitingTotal != 1 || len(waitingItems) != 1 || waitingItems[0].SessionID != "claude-abc" {
		t.Errorf("waiting=true: items=%+v total=%d err=%v", waitingItems, waitingTotal, err)
	}
	notWaitingItems, notWaitingTotal, err := s.ListSessions(ctx, SessionFilter{Waiting: &fa, Limit: 50})
	if err != nil || notWaitingTotal != 1 || len(notWaitingItems) != 1 || notWaitingItems[0].Actor != "owner" {
		t.Errorf("waiting=false: items=%+v total=%d err=%v", notWaitingItems, notWaitingTotal, err)
	}

	// ClearSessionWaiting clears waiting state and bumps updated_at + fingerprint.
	fpBeforeClear, err := s.FingerprintNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t6 := t0.Add(6 * time.Minute)
	fixClock(s, t6)
	at, cleared, err := s.ClearSessionWaiting(ctx, "claude", "claude-abc")
	if err != nil || !cleared || at != model.NewTime(t6) {
		t.Fatalf("ClearSessionWaiting = (%v, %v, %v)", at, cleared, err)
	}
	got = mustSession(t, s, "claude", "claude-abc")
	if got.State != nil || got.StateReason != nil || got.StateAt != nil || got.UpdatedAt != model.NewTime(t6) {
		t.Errorf("after auto clear = %+v", got)
	}
	fpAfterClear, err := s.FingerprintNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fpAfterClear == fpBeforeClear {
		t.Errorf("auto clear did not change fingerprint: %q", fpBeforeClear)
	}

	// Calling ClearSessionWaiting again when already clear is a no-op.
	t7 := t0.Add(7 * time.Minute)
	fixClock(s, t7)
	_, cleared, err = s.ClearSessionWaiting(ctx, "claude", "claude-abc")
	if err != nil || cleared {
		t.Errorf("second ClearSessionWaiting = (%v, %v)", cleared, err)
	}
	if got := mustSession(t, s, "claude", "claude-abc"); got.UpdatedAt != model.NewTime(t6) {
		t.Errorf("no-op clear bumped updated_at: %v", got.UpdatedAt)
	}

	// Explicit state: null clears waiting state.
	if _, err := s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{
		State:       Optional[string]{Set: true, Value: &stIn},
		StateReason: Optional[string]{Set: true, Value: &r1},
	}); err != nil {
		t.Fatal(err)
	}
	t8 := t0.Add(8 * time.Minute)
	fixClock(s, t8)
	got, err = s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{
		State: Optional[string]{Set: true, Value: nil},
	})
	if err != nil || got.State != nil || got.StateReason != nil || got.StateAt != nil || got.UpdatedAt != model.NewTime(t8) {
		t.Errorf("explicit null clear = %+v err=%v", got, err)
	}
}
