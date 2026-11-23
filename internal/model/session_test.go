package model

import (
	"strings"
	"testing"
	"time"
)

func TestSessionNamed(t *testing.T) {
	for id, want := range map[string]bool{
		"cli-20261120":  false,
		"claude-abc":    true,
		"cli-2026112":   true,
		"cli-202611200": true,
		"xcli-20261120": true,
	} {
		if got := SessionNamed(id); got != want {
			t.Errorf("SessionNamed(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestSessionStale(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		ago  time.Duration
		want bool
	}{{31 * time.Minute, true}, {29 * time.Minute, false}, {30 * time.Minute, false}, {0, false}} {
		if got := SessionStale(NewTime(now.Add(-c.ago)), NewTime(now)); got != c.want {
			t.Errorf("stale after %s = %v, want %v", c.ago, got, c.want)
		}
	}
}

func TestParseSessionRole(t *testing.T) {
	for _, r := range []string{"main", "subagent", "runner"} {
		if _, ok := ParseSessionRole(r); !ok {
			t.Errorf("ParseSessionRole(%q) rejected", r)
		}
	}
	if _, ok := ParseSessionRole("boss"); ok {
		t.Error("ParseSessionRole(boss) accepted")
	}
}

func TestValidSessionState(t *testing.T) {
	for _, s := range []string{SessionStateWaitingApproval, SessionStateWaitingInput, SessionStateWaitingExternal} {
		if !ValidSessionState(s) {
			t.Errorf("ValidSessionState(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "paused", "waiting", "WAITING_INPUT"} {
		if ValidSessionState(s) {
			t.Errorf("ValidSessionState(%q) = true, want false", s)
		}
	}
}

func TestCleanSessionStateReason(t *testing.T) {
	got, _, ok := CleanSessionStateReason("  배포 승인 대기  ")
	if !ok || got != "배포 승인 대기" {
		t.Errorf("trimmed = (%q, %v)", got, ok)
	}
	if _, _, ok := CleanSessionStateReason(strings.Repeat("가", MaxSessionStateReasonRunes)); !ok {
		t.Error("200 runes rejected")
	}
	for _, bad := range []string{
		"",
		"   ",
		strings.Repeat("가", MaxSessionStateReasonRunes+1),
		"줄바꿈\n포함",
		"끝줄바꿈\n",
		"널\x00포함",
		"탭\t포함",
		"제어\x1f문자",
		"삭제\x7f문자",
		"씨원\u0085문자",
		"시퀀스\u009b문자",
	} {
		if _, _, ok := CleanSessionStateReason(bad); ok {
			t.Errorf("CleanSessionStateReason(%q) accepted", bad)
		}
	}
}

func TestSessionWaitingValid(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	st := SessionStateWaitingInput
	at23 := NewTime(now.Add(-23 * time.Hour))
	at24 := NewTime(now.Add(-24 * time.Hour))
	at25 := NewTime(now.Add(-25 * time.Hour))

	cases := []struct {
		name string
		s    Session
		want bool
	}{
		{"23h ago", Session{State: &st, StateAt: &at23}, true},
		{"24h exact", Session{State: &st, StateAt: &at24}, true},
		{"25h ago", Session{State: &st, StateAt: &at25}, false},
		{"nil state", Session{State: nil, StateAt: &at23}, false},
		{"nil state_at", Session{State: &st, StateAt: nil}, false},
	}
	for _, c := range cases {
		if got := c.s.WaitingValid(now); got != c.want {
			t.Errorf("%s: WaitingValid = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNormalizeRepoRemote(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"https://github.com/acme/app.git", "github.com/acme/app"},
		{"  https://user:secret@GitHub.COM/acme/app.git/  ", "github.com/acme/app"},
		{"http://Example.COM/acme/app/", "example.com/acme/app"},
		{"git@github.com:acme/app.git", "github.com/acme/app"},
		{"user:tok@GitLab.Example.com:/team/sub/repo.git/", "gitlab.example.com/team/sub/repo"},
		{"ssh://git@github.com:22/acme/app.git", "github.com/acme/app"},
		{"ssh://user:pass@Example.COM:2222/acme/app/", "example.com/acme/app"},
		{"git@[2001:db8::1]:acme/app.git", "2001:db8::1/acme/app"},
		{"[2001:DB8::1]:/acme/app/", "2001:db8::1/acme/app"},
		{"ssh://git@[2001:db8::1]:22/acme/app.git", "2001:db8::1/acme/app"},
		{"user:p@ss@Example.COM:acme/app.git", "example.com/acme/app"},
		{"user:tok@Example.COM/acme/app.git", "example.com/acme/app"},
		{"user:tok@[2001:db8::1]/acme/app.git", "2001:db8::1/acme/app"},
		{"https://user:p@ss@Example.COM/acme/app.git", "example.com/acme/app"},
		{"https://user:secret@example.com/%ZZ", "https://example.com/%ZZ"},
		{"https://user:secret@example.com:badport/repo.git", "https://example.com:badport/repo.git"},
		{"https://user:secret@[::1/repo.git", "https://[::1/repo.git"},
		{"/srv/repos/app/", "/srv/repos/app"},
		{`C:\work\repos\app\`, "c:/work/repos/app"},
		{"  /var/git/app.git/  ", "/var/git/app.git"},
		{"my-local-repo/", "my-local-repo"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := NormalizeRepoRemote(c.raw); got != c.want {
			t.Errorf("NormalizeRepoRemote(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestStripRemoteUserinfo(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"  https://user:tok@example.com/acme/app.git  ", "https://example.com/acme/app.git"},
		{"https://example.com/acme/app.git ", "https://example.com/acme/app.git"},
		{"  user:tok@example.com:acme/app.git  ", "example.com:acme/app.git"},
		{"git@example.com:acme/app.git", "example.com:acme/app.git"},
		{"  git@[2001:db8::1]:acme/app.git  ", "[2001:db8::1]:acme/app.git"},
		{"ssh://git@[2001:db8::1]:22/acme/app.git", "ssh://[2001:db8::1]:22/acme/app.git"},
		{"https://user:p@ss@example.com/acme/app.git", "https://example.com/acme/app.git"},
		{"https://user:secret@example.com/%ZZ", "https://example.com/%ZZ"},
		{"https://user:secret@example.com:badport/repo.git", "https://example.com:badport/repo.git"},
		{"https://user:secret@[::1/repo.git", "https://[::1/repo.git"},
		{"https://user:secret@example.com/repo\x00.git", "https://example.com/repo\x00.git"},
		{"https://example.com/foo@bar/%ZZ", "https://example.com/foo@bar/%ZZ"},
		{"user:p@ss@example.com:acme/app.git", "example.com:acme/app.git"},
		{"user:p@ss@[2001:db8::1]:acme/app.git", "[2001:db8::1]:acme/app.git"},
		{"user:tok@example.com/acme/app.git", "example.com/acme/app.git"},
		{"user:tok@[2001:db8::1]/acme/app.git", "[2001:db8::1]/acme/app.git"},
		{"/srv/repos/@org/app", "/srv/repos/@org/app"},
		{"example.com/foo@bar", "example.com/foo@bar"},
		{"user@C:\\work\\repos\\app", "user@C:\\work\\repos\\app"},
		{`  C:\work\repos\app  `, `C:\work\repos\app`},
		{"  /srv/repos/app  ", "/srv/repos/app"},
	}
	for _, c := range cases {
		if got := StripRemoteUserinfo(c.raw); got != c.want {
			t.Errorf("StripRemoteUserinfo(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestSessionLocationKey(t *testing.T) {
	str := func(v string) *string { return &v }
	cases := []struct {
		name    string
		host    string
		loc     *SessionLocation
		wantKey string
		wantOK  bool
	}{
		{"nil location", "desk", nil, "", false},
		{"nil branch", "desk", &SessionLocation{RepoRemote: str("https://example.com/acme/app.git")}, "", false},
		{"empty branch", "desk", &SessionLocation{RepoRemote: str("https://example.com/acme/app.git"), Branch: str("   ")}, "", false},
		{"detached HEAD", "desk", &SessionLocation{RepoRemote: str("https://example.com/acme/app.git"), Branch: str("HEAD")}, "", false},
		{"detached HEAD trimmed", "desk", &SessionLocation{RepoRemote: str("https://example.com/acme/app.git"), Branch: str(" HEAD ")}, "", false},
		{"https remote", "desk", &SessionLocation{RepoRemote: str("https://example.com/acme/app.git"), Branch: str("feat/x"), Worktree: str("/work/app")}, "remote:example.com/acme/app#feat/x", true},
		{"scp remote", "laptop", &SessionLocation{RepoRemote: str("git@example.com:acme/app.git"), Branch: str("feat/x"), Worktree: str(`C:\Users\dev\app`)}, "remote:example.com/acme/app#feat/x", true},
		{"ipv6 scp remote", "laptop", &SessionLocation{RepoRemote: str("git@[2001:db8::1]:acme/app.git"), Branch: str("feat/x")}, "remote:2001:db8::1/acme/app#feat/x", true},
		{"ssh remote", "box", &SessionLocation{RepoRemote: str("ssh://git@example.com:22/acme/app.git"), Branch: str("feat/x")}, "remote:example.com/acme/app#feat/x", true},
		{"local fallback windows", "desk", &SessionLocation{Worktree: str(`C:\Work\App\`), Branch: str("feat/x")}, "local:desk:c:/Work/App#feat/x", true},
		{"local fallback blank remote", "desk", &SessionLocation{RepoRemote: str("  "), Worktree: str("c:/Work/App/"), Branch: str("feat/x")}, "local:desk:c:/Work/App#feat/x", true},
		{"local fallback unix", "laptop", &SessionLocation{Worktree: str("/home/dev/app/"), Branch: str("main")}, "local:laptop:/home/dev/app#main", true},
		{"no remote and no worktree", "desk", &SessionLocation{Branch: str("main"), Worktree: str("  ")}, "", false},
	}
	for _, c := range cases {
		gotKey, gotOK := SessionLocationKey(c.host, c.loc)
		if gotKey != c.wantKey || gotOK != c.wantOK {
			t.Errorf("%s: SessionLocationKey = (%q, %v), want (%q, %v)", c.name, gotKey, gotOK, c.wantKey, c.wantOK)
		}
	}
}
