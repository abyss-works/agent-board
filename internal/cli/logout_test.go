package cli

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

const (
	revokedJSON = `{"id":7,"actor":"codex","client_host":"desk","created_at":"2026-10-19T01:21:10.000Z","created_by":"owner","last_used_at":"2026-10-19T02:00:00.000Z","revoked_at":"2026-10-19T03:00:00.000Z","revoked_by":"codex"}`
	unauthJSON  = `{"error":{"code":"unauthorized","message":"유효한 Bearer 토큰이 필요합니다."}}`
)

// newLogoutEnv is a login harness whose config holds the tokens of two actors
// and a shared token. The environment holds a shared token too, which a logout
// must not use.
func newLogoutEnv(t *testing.T, tty bool) *loginEnv {
	t.Helper()
	e := newLoginEnv(t, tty)
	e.srv.on("DELETE", "/v1/tokens/current", canned{body: revokedJSON})
	e.env["AGB_TOKEN"] = "env-shared-token-must-not-be-used"
	saveConfig(e.app.ConfigPath, Config{
		Token:  "shared-file-token",
		Tokens: map[string]string{"codex": "agb_codex-token", "claude": "agb_claude-token"},
	})
	return e
}

func TestLogoutRevokesTheTokenAndForgetsIt(t *testing.T) {
	e := newLogoutEnv(t, false)
	if code := e.run("logout"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	if want := []string{"DELETE /v1/tokens/current"}; !equalStrings(e.srv.paths(), want) {
		t.Fatalf("requests = %v, want %v", e.srv.paths(), want)
	}
	// The token of the actor goes with the trace headers of the actor.
	h := e.srv.last().Header
	if h.Get("Authorization") != "Bearer agb_codex-token" || h.Get("X-Actor") != "codex" || h.Get("X-Session-Id") != "sess-1" || h.Get("X-Client-Host") != "desk" {
		t.Errorf("headers = %v", h)
	}
	if want := `{"actor":"codex","revoked":true}` + "\n"; e.stdout.String() != want {
		t.Errorf("stdout = %q, want %q", e.stdout.String(), want)
	}
	// Only the token of this actor goes.
	want := Config{Token: "shared-file-token", Tokens: map[string]string{"claude": "agb_claude-token"}}
	if cfg, err := loadConfig(e.app.ConfigPath); err != nil || !reflect.DeepEqual(cfg, want) {
		t.Errorf("config = %+v %v, want %+v", cfg, err, want)
	}
	if strings.Contains(e.stdout.String()+e.stderr.String(), "agb_codex-token") {
		t.Errorf("the token is in the output:\n%s%s", e.stdout, e.stderr)
	}
}

func TestLogoutOfTheLastTokenLeavesNoTokensKey(t *testing.T) {
	e := newLogoutEnv(t, false)
	saveConfig(e.app.ConfigPath, Config{URL: "http://file:1", Tokens: map[string]string{"codex": "agb_codex-token"}})
	if code := e.run("logout"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	raw, _ := os.ReadFile(e.app.ConfigPath)
	if strings.Contains(string(raw), "tokens") || !strings.Contains(string(raw), "http://file:1") {
		t.Errorf("config file:\n%s", raw)
	}
}

func TestLogoutHumanOutput(t *testing.T) {
	e := newLogoutEnv(t, true)
	if code := e.run("logout"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	out := strings.TrimSuffix(e.stdout.String(), "\n")
	if strings.Contains(out, "\n") || strings.Contains(out, "{") || !strings.Contains(out, "codex") || !strings.Contains(out, "폐기했습니다") {
		t.Errorf("stdout = %q", e.stdout.String())
	}
}

func TestLogoutWithoutALocalToken(t *testing.T) {
	for name, prep := range map[string]func(e *loginEnv){
		"no config file":        func(e *loginEnv) { os.Remove(e.app.ConfigPath) },
		"only the shared token": func(e *loginEnv) { saveConfig(e.app.ConfigPath, Config{Token: "shared-file-token"}) },
		"only the token of another actor": func(e *loginEnv) {
			saveConfig(e.app.ConfigPath, Config{Tokens: map[string]string{"claude": "agb_claude-token"}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newLogoutEnv(t, false)
			prep(e)
			before, beforeErr := os.ReadFile(e.app.ConfigPath)
			if code := e.run("logout"); code != 2 {
				t.Fatalf("code=%d stderr=%s", code, e.stderr)
			}
			if !strings.Contains(e.stderr.String(), "agb login --actor codex") {
				t.Errorf("stderr = %s", e.stderr)
			}
			if len(e.srv.reqs) != 0 || e.stdout.Len() != 0 {
				t.Errorf("a logout without a token must do nothing: requests=%v stdout=%q", e.srv.paths(), e.stdout.String())
			}
			if after, afterErr := os.ReadFile(e.app.ConfigPath); string(after) != string(before) || (afterErr == nil) != (beforeErr == nil) {
				t.Errorf("the config file changed:\n%s\n--\n%s", before, after)
			}
		})
	}
}

func TestLogoutOfATokenTheServerDoesNotKnow(t *testing.T) {
	e := newLogoutEnv(t, false)
	e.srv.on("DELETE", "/v1/tokens/current", canned{status: 401, body: unauthJSON})
	if code := e.run("logout"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	if want := `{"actor":"codex","revoked":false}` + "\n"; e.stdout.String() != want {
		t.Errorf("stdout = %q, want %q", e.stdout.String(), want)
	}
	want := Config{Token: "shared-file-token", Tokens: map[string]string{"claude": "agb_claude-token"}}
	if cfg, _ := loadConfig(e.app.ConfigPath); !reflect.DeepEqual(cfg, want) {
		t.Errorf("config = %+v, want %+v", cfg, want)
	}

	th := newLogoutEnv(t, true)
	th.srv.on("DELETE", "/v1/tokens/current", canned{status: 401, body: unauthJSON})
	if code := th.run("logout"); code != 0 || !strings.Contains(th.stdout.String(), "이미 폐기한 토큰") {
		t.Errorf("human: code=%d stdout=%q stderr=%q", code, th.stdout.String(), th.stderr.String())
	}
}

func TestLogoutKeepsTheTokenWhenTheServerCannotBeAsked(t *testing.T) {
	for name, tc := range map[string]struct {
		prep func(e *loginEnv)
		want string
	}{
		"server error": {func(e *loginEnv) {
			e.srv.on("DELETE", "/v1/tokens/current", canned{status: 500, body: `{"error":{"code":"internal","message":"서버 오류입니다."}}`})
		}, "500 internal"},
		"forbidden": {func(e *loginEnv) {
			e.srv.on("DELETE", "/v1/tokens/current", canned{status: 403, body: `{"error":{"code":"forbidden","message":"토큰과 X-Actor가 맞지 않습니다.","details":[{"field":"X-Actor","reason":"actor_mismatch","message":"m"}]}}`})
		}, "403 forbidden"},
		"server down": {func(e *loginEnv) { e.env["AGB_URL"] = "http://127.0.0.1:1" }, "agb:"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newLogoutEnv(t, true)
			tc.prep(e)
			before, _ := os.ReadFile(e.app.ConfigPath)
			if code := e.run("logout"); code != 1 || !strings.Contains(e.stderr.String(), tc.want) {
				t.Fatalf("code=%d stderr=%s", code, e.stderr)
			}
			if e.stdout.Len() != 0 {
				t.Errorf("stdout = %q", e.stdout.String())
			}
			if after, _ := os.ReadFile(e.app.ConfigPath); string(after) != string(before) {
				t.Errorf("the config file changed:\n%s\n--\n%s", before, after)
			}
		})
	}
}

func TestLogoutUsageErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		prep func(e *loginEnv)
		args []string
		want string
	}{
		"no actor":           {func(e *loginEnv) { delete(e.env, "AGB_ACTOR") }, []string{"logout"}, "AGB_ACTOR"},
		"no server address":  {func(e *loginEnv) { delete(e.env, "AGB_URL") }, []string{"logout"}, "AGB_URL"},
		"a positional value": {nil, []string{"logout", "now"}, "인자가 너무 많습니다"},
		"an unknown flag":    {nil, []string{"logout", "--bogus"}, "bogus"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newLogoutEnv(t, false)
			if tc.prep != nil {
				tc.prep(e)
			}
			if code := e.run(tc.args...); code != 2 || !strings.Contains(e.stderr.String(), tc.want) {
				t.Errorf("code=%d stderr=%s", code, e.stderr)
			}
			if len(e.srv.reqs) != 0 {
				t.Errorf("a usage error must not reach the server: %v", e.srv.paths())
			}
			if cfg, _ := loadConfig(e.app.ConfigPath); len(cfg.Tokens) != 2 {
				t.Errorf("config = %+v", cfg)
			}
		})
	}
}

func TestLogoutActorFollowsTheUsualSettings(t *testing.T) {
	e := newLogoutEnv(t, false)
	if code := e.run("--actor", "claude", "logout"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	h := e.srv.last().Header
	if h.Get("Authorization") != "Bearer agb_claude-token" || h.Get("X-Actor") != "claude" {
		t.Errorf("headers = %v", h)
	}
	if cfg, _ := loadConfig(e.app.ConfigPath); !reflect.DeepEqual(cfg.Tokens, map[string]string{"codex": "agb_codex-token"}) {
		t.Errorf("config = %+v", cfg)
	}
}
