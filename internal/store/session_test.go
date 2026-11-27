package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
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
	mustProject(t, s, ctx, "CCC")
	mustProject(t, s, ctx, "DDD")
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
	fixClock(s, t0.Add(3*time.Minute))
	docName := "2026-10-17-0003-report-test.md"
	docTitle := "문서 활동"
	if _, _, err := s.PutDocument(ctx, trOwner, "CCC", docName, DocumentInput{
		Content: "본문\n",
		Args:    model.DocumentArgs{Title: &docTitle},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetDocumentArchived(ctx, trSess, "CCC", docName, true); err != nil {
		t.Fatal(err)
	}
	if got := mustSession(t, s, "claude", "claude-abc"); got.LastProject == nil || *got.LastProject != "CCC" {
		t.Errorf("last_project after document_activity = %v", got.LastProject)
	}
	fixClock(s, t0.Add(4*time.Minute))
	issD := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "DDD", Title: "d"})
	if _, _, err := s.ClaimIssue(ctx, trSess, issD.Key); err != nil {
		t.Fatal(err)
	}
	if got := mustSession(t, s, "claude", "claude-abc"); got.LastProject == nil || *got.LastProject != "DDD" {
		t.Errorf("last_project after issue_claim = %v", got.LastProject)
	}
}

func TestSessionLastProjectAndListFilterWithOnlyClaim(t *testing.T) {
	s, ctx := seeded(t)
	t0 := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	fixClock(s, t0)
	mustProject(t, s, ctx, "APP")
	iss := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "APP", Title: "점유 전용"})

	trClaimOnly := model.Trace{Actor: "claude", SessionID: "claim-only", ClientHost: "desk"}
	mustTouch(t, s, trClaimOnly, t0)
	if _, _, err := s.ClaimIssue(ctx, trClaimOnly, iss.Key); err != nil {
		t.Fatal(err)
	}
	got := mustSession(t, s, "claude", "claim-only")
	if got.LastProject == nil || *got.LastProject != "APP" {
		t.Fatalf("last_project = %v, want APP", got.LastProject)
	}
	items, total, err := s.ListSessions(ctx, SessionFilter{Project: "APP", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].SessionID != "claim-only" {
		t.Errorf("ListSessions(project=APP) = %+v (total=%d), want claim-only", items, total)
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
	remote, dirty := "https://user:tok@example.com/a/b.git", 3
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
	if got.Location == nil || deref(got.Location.RepoRemote) != "https://example.com/a/b.git" || got.Location.DirtyCount == nil || *got.Location.DirtyCount != 3 ||
		got.Location.ReportedAt != model.NewTime(at.Add(time.Minute)) || got.Location.Branch != nil {
		t.Errorf("location = %+v", got.Location)
	}
	// Strip userinfo and trim spaces for SCP remotes (including IPv6) and padded URLs.
	for _, tc := range []struct {
		raw, want string
	}{
		{"  https://user:tok@example.com/a/b.git  ", "https://example.com/a/b.git"},
		{"  user:tok@example.com:acme/app.git  ", "example.com:acme/app.git"},
		{"  git@[2001:db8::1]:acme/app.git  ", "[2001:db8::1]:acme/app.git"},
	} {
		r := tc.raw
		res, err := s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{
			Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{RepoRemote: &r}},
		})
		if err != nil {
			t.Fatalf("UpdateSession(%q): %v", tc.raw, err)
		}
		if res.Location == nil || deref(res.Location.RepoRemote) != tc.want {
			t.Errorf("UpdateSession(%q) stored %v, want %q", tc.raw, deref(res.Location.RepoRemote), tc.want)
		}
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

func TestCreateChildSessionRejectsSelfIDWhenParentMissing(t *testing.T) {
	s, ctx := seeded(t)
	if _, err := s.CreateChildSession(ctx, trSess, ChildSessionInput{SessionID: trSess.SessionID}); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("self id with missing parent = %v, want ErrSessionExists", err)
	}
	var count int
	if err := s.db.QueryRow(`select count(*) from session`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("session count = %d, want 0 after rejected self-id child create", count)
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
		if f.Limit == 50 && total != len(out) {
			t.Errorf("total %d != len(out) %d for %+v", total, len(out), f)
		} else if total < len(out) {
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

	// Deterministic in-transaction DB error paths (after BeginTx succeeds).
	rawDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(s.path)+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	// Corrupt owner/s-owner's issue_id type so getSessionRow inside checkSessionParent
	// (and CreateChildSession's getSessionRow) fails inside the write transaction.
	if _, err := rawDB.Exec(`update session set issue_id = 'bad-int' where actor_id = 'owner' and session_id = 's-owner'`); err != nil {
		t.Fatal(err)
	}
	var ve *model.ValidationError
	if _, err := s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{
		Parent: Optional[model.SessionRef]{Set: true, Value: &model.SessionRef{Actor: "owner", SessionID: "s-owner"}},
		Role:   Optional[string]{Set: true, Value: &role},
	}); err == nil || errors.As(err, &ve) {
		t.Errorf("UpdateSession(parent db scan error) = %v, want non-ValidationError DB error", err)
	}
	if got := mustSession(t, s, "claude", "claude-abc"); got.Parent != nil || got.Role != nil {
		t.Errorf("session changed after in-tx parent error: %+v", got)
	}
	if _, err := s.CreateChildSession(ctx, model.Trace{Actor: "owner", SessionID: "other-owner", ClientHost: "desk"}, ChildSessionInput{SessionID: "s-owner"}); err == nil || errors.Is(err, ErrSessionExists) {
		t.Errorf("CreateChildSession(existing row scan error) = %v, want non-ErrSessionExists DB error", err)
	}

	// Drop issue table so sessionIssueID inside s.write fails with a DB error.
	if _, err := rawDB.Exec(`drop table issue`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSession(ctx, trSess, "claude", "claude-abc", SessionPatch{
		Issue: Optional[string]{Set: true, Value: &key},
		Role:  Optional[string]{Set: true, Value: &role},
	}); err == nil || errors.As(err, &ve) {
		t.Errorf("UpdateSession(issue table missing) = %v, want non-ValidationError DB error", err)
	}
	if _, err := s.CreateChildSession(ctx, trSess, ChildSessionInput{SessionID: "claude-abc.c", Issue: &key}); err == nil || errors.As(err, &ve) {
		t.Errorf("CreateChildSession(issue table missing) = %v, want non-ValidationError DB error", err)
	}
	var childCount int
	if err := s.db.QueryRow(`select count(*) from session where actor_id = 'claude' and session_id = 'claude-abc.c'`).Scan(&childCount); err != nil {
		t.Fatal(err)
	}
	if childCount != 0 {
		t.Errorf("child session count = %d, want 0 after in-tx issue error", childCount)
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

func TestSessionLocationConflicts(t *testing.T) {
	s, ctx := seeded(t)
	if err := s.EnsureActors(ctx, []model.Actor{{ID: "owner", Kind: model.ActorUser}, {ID: "claude", Kind: model.ActorAgent}, {ID: "codex", Kind: model.ActorAgent}}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	fixClock(s, now)

	ptr := func(v string) *string { return &v }
	sameRefs := func(got, want []model.SessionRef) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	// Active sessions on same remote+branch (HTTPS vs SCP) conflict with each other.
	tr1 := model.Trace{Actor: "claude", SessionID: "s1", ClientHost: "desk"}
	tr2 := model.Trace{Actor: "codex", SessionID: "s2", ClientHost: "laptop"}
	trDiffBranch := model.Trace{Actor: "claude", SessionID: "s-diff", ClientHost: "desk"}
	trDetached := model.Trace{Actor: "claude", SessionID: "s-head", ClientHost: "desk"}
	trStale := model.Trace{Actor: "owner", SessionID: "s-stale", ClientHost: "desk"}
	trWait := model.Trace{Actor: "owner", SessionID: "s-wait", ClientHost: "desk"}

	for _, tr := range []model.Trace{tr1, tr2, trDiffBranch, trDetached} {
		mustTouch(t, s, tr, now.Add(-5*time.Minute))
	}
	mustTouch(t, s, trStale, now.Add(-40*time.Minute))
	mustTouch(t, s, trWait, now.Add(-2*time.Hour))

	// s-wait sets waiting_input at -2h (within 24h WaitingValid hold).
	fixClock(s, now.Add(-2*time.Hour))
	stWait := model.SessionStateWaitingInput
	if _, err := s.UpdateSession(ctx, trWait, trWait.Actor, trWait.SessionID, SessionPatch{
		State:    Optional[string]{Set: true, Value: &stWait},
		Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{RepoRemote: ptr("ssh://git@example.com:22/acme/app.git"), Branch: ptr("feat/x")}},
	}); err != nil {
		t.Fatal(err)
	}

	fixClock(s, now)
	if _, err := s.UpdateSession(ctx, trStale, trStale.Actor, trStale.SessionID, SessionPatch{
		Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{RepoRemote: ptr("https://example.com/acme/app.git"), Branch: ptr("feat/x")}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSession(ctx, trDiffBranch, trDiffBranch.Actor, trDiffBranch.SessionID, SessionPatch{
		Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{RepoRemote: ptr("https://example.com/acme/app.git"), Branch: ptr("feat/y")}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSession(ctx, trDetached, trDetached.Actor, trDetached.SessionID, SessionPatch{
		Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{RepoRemote: ptr("https://example.com/acme/app.git"), Branch: ptr("HEAD")}},
	}); err != nil {
		t.Fatal(err)
	}

	u1, err := s.UpdateSession(ctx, tr1, tr1.Actor, tr1.SessionID, SessionPatch{
		Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{RepoRemote: ptr("https://user:tok@example.com/acme/app.git"), Branch: ptr("feat/x")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want1 := []model.SessionRef{{Actor: "owner", SessionID: "s-wait"}}
	if !sameRefs(u1.LocationConflicts, want1) {
		t.Errorf("u1 conflicts = %+v, want %+v", u1.LocationConflicts, want1)
	}

	u2, err := s.UpdateSession(ctx, tr2, tr2.Actor, tr2.SessionID, SessionPatch{
		Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{RepoRemote: ptr("git@example.com:acme/app.git"), Branch: ptr("feat/x")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want2 := []model.SessionRef{{Actor: "claude", SessionID: "s1"}, {Actor: "owner", SessionID: "s-wait"}}
	if !sameRefs(u2.LocationConflicts, want2) {
		t.Errorf("u2 conflicts = %+v, want %+v", u2.LocationConflicts, want2)
	}

	// Child session on same remote+branch gets conflicts and parent sees child too.
	child, err := s.CreateChildSession(ctx, tr1, ChildSessionInput{SessionID: "s1.kid"})
	if err != nil {
		t.Fatal(err)
	}
	if child.LocationConflicts == nil || len(child.LocationConflicts) != 0 {
		t.Errorf("new child without location conflicts = %#v, want empty non-nil", child.LocationConflicts)
	}
	if _, err := s.UpdateSession(ctx, tr1, tr1.Actor, "s1.kid", SessionPatch{
		Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{RepoRemote: ptr("http://example.com/acme/app/"), Branch: ptr("feat/x")}},
	}); err != nil {
		t.Fatal(err)
	}

	parent := mustSession(t, s, "claude", "s1")
	wantParent := []model.SessionRef{
		{Actor: "claude", SessionID: "s1.kid"},
		{Actor: "codex", SessionID: "s2"},
		{Actor: "owner", SessionID: "s-wait"},
	}
	if !sameRefs(parent.LocationConflicts, wantParent) {
		t.Errorf("parent conflicts = %+v, want %+v", parent.LocationConflicts, wantParent)
	}
	if parent.Children == nil || len(*parent.Children) != 1 {
		t.Fatalf("parent children = %+v", parent.Children)
	}
	wantChild := []model.SessionRef{
		{Actor: "claude", SessionID: "s1"},
		{Actor: "codex", SessionID: "s2"},
		{Actor: "owner", SessionID: "s-wait"},
	}
	if !sameRefs((*parent.Children)[0].LocationConflicts, wantChild) {
		t.Errorf("child conflicts = %+v, want %+v", (*parent.Children)[0].LocationConflicts, wantChild)
	}

	// Non-conflicting sessions have non-nil empty slice; stale non-waiting has empty slice.
	diffSess := mustSession(t, s, trDiffBranch.Actor, trDiffBranch.SessionID)
	if diffSess.LocationConflicts == nil || len(diffSess.LocationConflicts) != 0 {
		t.Errorf("diff branch conflicts = %#v, want empty non-nil", diffSess.LocationConflicts)
	}
	staleSess := mustSession(t, s, trStale.Actor, trStale.SessionID)
	if staleSess.LocationConflicts == nil || len(staleSess.LocationConflicts) != 0 {
		t.Errorf("stale non-waiting conflicts = %#v, want empty non-nil", staleSess.LocationConflicts)
	}

	// ListSessions filtered by actor=claude still finds conflicts with codex/owner across DB.
	listed, _, err := s.ListSessions(ctx, SessionFilter{Actor: "claude", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	foundS1 := false
	for _, it := range listed {
		if it.SessionID == "s1" {
			foundS1 = true
			if !sameRefs(it.LocationConflicts, wantParent) {
				t.Errorf("filtered list s1 conflicts = %+v, want %+v", it.LocationConflicts, wantParent)
			}
		}
	}
	if !foundS1 {
		t.Fatal("s1 not returned in filtered ListSessions")
	}

	// Local fallback: no remote, same host + worktree (normalized) + branch conflicts; different host does not.
	trLocal1 := model.Trace{Actor: "claude", SessionID: "loc1", ClientHost: "desk"}
	trLocal2 := model.Trace{Actor: "codex", SessionID: "loc2", ClientHost: "desk"}
	trLocalOtherHost := model.Trace{Actor: "owner", SessionID: "loc3", ClientHost: "laptop"}
	for _, tr := range []model.Trace{trLocal1, trLocal2, trLocalOtherHost} {
		mustTouch(t, s, tr, now.Add(-2*time.Minute))
	}
	if _, err := s.UpdateSession(ctx, trLocal1, trLocal1.Actor, trLocal1.SessionID, SessionPatch{
		Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{Worktree: ptr(`C:\Work\LocalRepo`), Branch: ptr("dev")}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSession(ctx, trLocalOtherHost, trLocalOtherHost.Actor, trLocalOtherHost.SessionID, SessionPatch{
		Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{Worktree: ptr("c:/Work/LocalRepo"), Branch: ptr("dev")}},
	}); err != nil {
		t.Fatal(err)
	}
	loc2, err := s.UpdateSession(ctx, trLocal2, trLocal2.Actor, trLocal2.SessionID, SessionPatch{
		Location: Optional[model.SessionLocation]{Set: true, Value: &model.SessionLocation{Worktree: ptr("c:/Work/LocalRepo/"), Branch: ptr("dev")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sameRefs(loc2.LocationConflicts, []model.SessionRef{{Actor: "claude", SessionID: "loc1"}}) {
		t.Errorf("local fallback conflicts = %+v", loc2.LocationConflicts)
	}
	loc3 := mustSession(t, s, trLocalOtherHost.Actor, trLocalOtherHost.SessionID)
	if loc3.LocationConflicts == nil || len(loc3.LocationConflicts) != 0 {
		t.Errorf("other host local conflicts = %#v, want empty", loc3.LocationConflicts)
	}

	// Clearing location to null excludes the session from others' conflicts and clears its own.
	clearedLoc2, err := s.UpdateSession(ctx, trLocal2, trLocal2.Actor, trLocal2.SessionID, SessionPatch{
		Location: Optional[model.SessionLocation]{Set: true, Value: nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	if clearedLoc2.Location != nil || clearedLoc2.LocationConflicts == nil || len(clearedLoc2.LocationConflicts) != 0 {
		t.Errorf("cleared location session = %+v", clearedLoc2)
	}
	loc1After := mustSession(t, s, trLocal1.Actor, trLocal1.SessionID)
	if len(loc1After.LocationConflicts) != 0 {
		t.Errorf("loc1 still sees cleared loc2: %+v", loc1After.LocationConflicts)
	}

	// Advancing clock past 24h (>SessionWaitingHold) expires s-wait's waiting hold so it drops out of conflicts.
	later := now.Add(23 * time.Hour)
	fixClock(s, later)
	mustTouch(t, s, tr1, later)
	mustTouch(t, s, model.Trace{Actor: "claude", SessionID: "s1.kid", ClientHost: "desk"}, later)
	mustTouch(t, s, tr2, later)
	u1AfterExp := mustSession(t, s, tr1.Actor, tr1.SessionID)
	wantAfterExp := []model.SessionRef{{Actor: "claude", SessionID: "s1.kid"}, {Actor: "codex", SessionID: "s2"}}
	if !sameRefs(u1AfterExp.LocationConflicts, wantAfterExp) {
		t.Errorf("u1 conflicts after s-wait >24h = %+v, want %+v", u1AfterExp.LocationConflicts, wantAfterExp)
	}
	waitExpired := mustSession(t, s, trWait.Actor, trWait.SessionID)
	if len(waitExpired.LocationConflicts) != 0 {
		t.Errorf("expired waiting session conflicts = %+v, want empty", waitExpired.LocationConflicts)
	}

	// scanSession initializes LocationConflicts to non-nil empty slice, and
	// attachLocationConflicts skips DB query when no input session needs lookup.
	staleCutoff, waitCutoff := sessionCutoffs(model.NewTime(later))
	scanned, err := scanSession(s.db.QueryRowContext(ctx, sessionSelect+` where s.actor_id = ? and s.session_id = ?`, staleCutoff, waitCutoff, tr1.Actor, tr1.SessionID), model.NewTime(later))
	if err != nil {
		t.Fatal(err)
	}
	if scanned.LocationConflicts == nil || len(scanned.LocationConflicts) != 0 {
		t.Errorf("scanSession LocationConflicts = %#v, want non-nil empty slice", scanned.LocationConflicts)
	}
	if _, err := s.db.Exec(`drop table session`); err != nil {
		t.Fatal(err)
	}
	if err := s.attachLocationConflicts(ctx, model.NewTime(later), staleSess, clearedLoc2, waitExpired); err != nil {
		t.Errorf("attachLocationConflicts should early-return without querying DB, got: %v", err)
	}
	if err := s.attachLocationConflicts(ctx, model.NewTime(later), u1AfterExp); err == nil {
		t.Errorf("expected DB error when active session with location triggers lookup on dropped table")
	}
}
