package store

import (
	"context"
	"database/sql"
	"errors"
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

	if _, err := s.UpdateSession(ctx, "claude", "none", SessionPatch{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing session: %v", err)
	}
	// An empty patch changes nothing.
	fixClock(s, at.Add(time.Minute))
	got, err := s.UpdateSession(ctx, "claude", "claude-abc", SessionPatch{})
	if err != nil || got.UpdatedAt != model.NewTime(at) {
		t.Errorf("empty patch: %+v %v", got, err)
	}

	_, err = s.UpdateSession(ctx, "claude", "claude-abc", SessionPatch{Parent: Optional[model.SessionRef]{Set: true, Value: &model.SessionRef{Actor: "claude", SessionID: "nope"}}})
	validationField(t, err, "parent", "unknown_session")
	_, err = s.UpdateSession(ctx, "claude", "claude-abc", SessionPatch{Parent: Optional[model.SessionRef]{Set: true, Value: &model.SessionRef{Actor: "claude", SessionID: "claude-abc"}}})
	validationField(t, err, "parent", "cycle")
	boss := "boss"
	_, err = s.UpdateSession(ctx, "claude", "claude-abc", SessionPatch{Role: Optional[string]{Set: true, Value: &boss}})
	validationField(t, err, "role", "")
	missing := "APP-999"
	_, err = s.UpdateSession(ctx, "claude", "claude-abc", SessionPatch{Issue: Optional[string]{Set: true, Value: &missing}})
	validationField(t, err, "issue", "unknown_issue")

	role, key := "runner", i.Key
	remote, dirty := "https://user:tok@github.com/a/b.git", 3
	got, err = s.UpdateSession(ctx, "claude", "claude-abc", SessionPatch{
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
	got, err = s.UpdateSession(ctx, "claude", "claude-abc", SessionPatch{Role: Optional[string]{Set: true, Value: &role}})
	if err != nil || got.UpdatedAt != model.NewTime(at.Add(time.Minute)) {
		t.Errorf("same role: %s %v", got.UpdatedAt, err)
	}
	got, err = s.UpdateSession(ctx, "claude", "claude-abc", SessionPatch{Issue: Optional[string]{Set: true}, Location: Optional[model.SessionLocation]{Set: true}, Parent: Optional[model.SessionRef]{Set: true}})
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
		_, err := s.UpdateSession(ctx, "claude", "claude-abc", SessionPatch{Parent: Optional[model.SessionRef]{Set: true, Value: &model.SessionRef{Actor: "claude", SessionID: child}}})
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
	if _, err := s.UpdateSession(ctx, "claude", "claude-abc", SessionPatch{Role: Optional[string]{Set: true, Value: &runner}}); err != nil {
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
	if _, err := s.UpdateSession(ctx, "claude", "claude-abc", SessionPatch{Role: Optional[string]{Set: true, Value: &role}}); err != nil {
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
		if _, err := s.UpdateSession(canceled, "claude", "claude-abc", patch); !errors.Is(err, context.Canceled) || errors.As(err, &ve) {
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
