package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The secrets are made up. loginSecret is the actor token the fake server
// issues, deviceCode is the secret the CLI trades for it.
const (
	loginSecret = "agb_issued-secret-0123456789abcdefghijklmnopqrstuvwxy"
	deviceCode  = "dev-code-0123456789abcdefghijklmnopqrstuvwxyz"
	grantJSON   = `{"device_code":"` + deviceCode + `","user_code":"BDFG-HJKL","expires_in":600,"interval":5}`
	issuedJSON  = `{"id":7,"actor":"codex","client_host":"desk","created_at":"2026-10-19T01:21:10.000Z","created_by":"owner","last_used_at":null,"revoked_at":null,"revoked_by":null,"token":"` + loginSecret + `"}`
	pendingJSON = `{"error":{"code":"authorization_pending","message":"아직 승인되지 않았습니다. 승인을 기다려 주세요."}}`
	deniedJSON  = `{"error":{"code":"access_denied","message":"서버가 거부를 알렸습니다."}}`
	expiredJSON = `{"error":{"code":"expired_token","message":"서버가 만료를 알렸습니다."}}`
)

// loginEnv is a harness for the login tests. Its clock moves only when the
// command sleeps, its browser only records the addresses it is asked to open,
// and its fake server answers the token exchange with "pending" until a test
// says otherwise.
type loginEnv struct {
	*harness
	srv    *fakeServer
	now    time.Time
	slept  []time.Duration
	opened []string
	// onSleep runs at the end of every sleep with the number of sleeps so far.
	onSleep func(n int)
}

func newLoginEnv(t *testing.T, tty bool) *loginEnv {
	t.Helper()
	srv := newFakeServer(t)
	srv.on("POST", "/v1/device-authorizations", canned{status: 201, body: grantJSON})
	srv.on("POST", "/v1/tokens", canned{status: 400, body: pendingJSON})
	e := &loginEnv{harness: newHarness(t, srv, tty), srv: srv, now: noon}
	delete(e.env, "AGB_TOKEN")
	e.env["AGB_ACTOR"] = "codex"
	e.app.Now = func() time.Time { return e.now }
	e.app.Sleep = func(ctx context.Context, d time.Duration) error {
		e.slept = append(e.slept, d)
		e.now = e.now.Add(d)
		if e.onSleep != nil {
			e.onSleep(len(e.slept))
		}
		return ctx.Err()
	}
	e.app.OpenBrowser = func(url string) error {
		e.opened = append(e.opened, url)
		return nil
	}
	return e
}

// answerAfter makes the token exchange answer with c once the command has
// slept n times.
func (e *loginEnv) answerAfter(n int, c canned) {
	e.onSleep = func(done int) {
		if done == n {
			e.srv.on("POST", "/v1/tokens", c)
		}
	}
}

// approveAfter lets the user approve while the command sleeps for the nth time.
func (e *loginEnv) approveAfter(n int) {
	e.answerAfter(n, canned{status: 201, body: issuedJSON})
}

// count is the number of requests the fake server got for method and path.
func (e *loginEnv) count(methodAndPath string) int {
	n := 0
	for _, p := range e.srv.paths() {
		if p == methodAndPath {
			n++
		}
	}
	return n
}

func (e *loginEnv) approvalURL() string { return e.srv.srv.URL + "/device?code=BDFG-HJKL" }

// noSecrets fails the test when an output holds a secret of the login.
func (e *loginEnv) noSecrets(t *testing.T) {
	t.Helper()
	for _, secret := range []string{loginSecret, deviceCode} {
		if out := e.stdout.String() + e.stderr.String(); strings.Contains(out, secret) {
			t.Errorf("a secret is in the output:\n%s", out)
		}
	}
}

func TestLoginStoresTheActorToken(t *testing.T) {
	e := newLoginEnv(t, false)
	e.approveAfter(2)
	// The shared token must not reach the two calls that start a login.
	e.env["AGB_TOKEN"] = "shared-token-must-not-be-sent-0123456789"

	if code := e.run("login"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	if want := `{"actor":"codex","client_host":"desk","token_id":7,"created_at":"2026-10-19T01:21:10.000Z"}` + "\n"; e.stdout.String() != want {
		t.Errorf("stdout = %q, want %q", e.stdout.String(), want)
	}
	cfg, err := loadConfig(e.app.ConfigPath)
	if err != nil || !reflect.DeepEqual(cfg, Config{Tokens: map[string]string{"codex": loginSecret}}) {
		t.Errorf("config = %+v %v", cfg, err)
	}
	e.noSecrets(t)

	if want := []string{"POST /v1/device-authorizations", "POST /v1/tokens", "POST /v1/tokens"}; !equalStrings(e.srv.paths(), want) {
		t.Fatalf("requests = %v, want %v", e.srv.paths(), want)
	}
	if got := strings.TrimSpace(e.srv.reqs[0].Body); got != `{"actor":"codex","client_host":"desk"}` {
		t.Errorf("device authorization body = %s", got)
	}
	for i, r := range e.srv.reqs {
		for _, header := range []string{"Authorization", "X-Actor", "X-Session-Id", "X-Client-Host"} {
			if r.Header.Get(header) != "" {
				t.Errorf("request %d sends %s", i, header)
			}
		}
		if i > 0 && strings.TrimSpace(r.Body) != `{"device_code":"`+deviceCode+`"}` {
			t.Errorf("exchange %d body = %s", i, r.Body)
		}
	}
	if want := []time.Duration{5 * time.Second, 5 * time.Second}; !reflect.DeepEqual(e.slept, want) {
		t.Errorf("slept = %v, want the server interval before each attempt: %v", e.slept, want)
	}
	if !equalStrings(e.opened, []string{e.approvalURL()}) {
		t.Errorf("opened = %v", e.opened)
	}
}

func TestLoginTellsTheApprovalAddressOnStderr(t *testing.T) {
	e := newLoginEnv(t, false)
	e.approveAfter(1)
	// A trailing slash of the server address does not double up.
	e.env["AGB_URL"] += "/"
	if code := e.run("login", "--no-browser"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	for _, want := range []string{"승인 주소: " + e.approvalURL() + "\n", "승인 코드: BDFG-HJKL\n", "10분"} {
		if !strings.Contains(e.stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, e.stderr)
		}
	}
	if strings.Contains(e.stdout.String(), "BDFG") || strings.Contains(e.stdout.String(), "/device") {
		t.Errorf("stdout must hold the result only: %q", e.stdout.String())
	}
}

func TestLoginRoundsTheRemainingMinutesUp(t *testing.T) {
	for expiresIn, want := range map[int]string{600: "10분", 601: "11분", 90: "2분", 30: "1분", 60: "1분"} {
		e := newLoginEnv(t, false)
		e.srv.on("POST", "/v1/device-authorizations", canned{status: 201, body: strings.Replace(grantJSON, `"expires_in":600`, `"expires_in":`+strconv.Itoa(expiresIn), 1)})
		e.approveAfter(1)
		if code := e.run("login", "--no-browser"); code != 0 || !strings.Contains(e.stderr.String(), want) {
			t.Errorf("expires_in %d: code=%d, stderr lacks %q:\n%s", expiresIn, code, want, e.stderr)
		}
	}
}

func TestLoginHumanOutputIsOneLine(t *testing.T) {
	e := newLoginEnv(t, true)
	e.approveAfter(1)
	if code := e.run("login"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	out := strings.TrimSuffix(e.stdout.String(), "\n")
	if out == "" || strings.Contains(out, "\n") || strings.Contains(out, "{") || !strings.Contains(out, "codex") || !strings.Contains(out, "7") || !strings.Contains(out, "desk") {
		t.Errorf("stdout = %q", e.stdout.String())
	}
	e.noSecrets(t)
	cfg, _ := loadConfig(e.app.ConfigPath)
	if cfg.Tokens["codex"] != loginSecret {
		t.Errorf("config = %+v", cfg)
	}
}

func TestLoginBrowser(t *testing.T) {
	const failure = "브라우저를 열지 못했습니다. 주소를 직접 여세요.\n"
	t.Run("opens the approval address", func(t *testing.T) {
		e := newLoginEnv(t, false)
		e.approveAfter(1)
		if code := e.run("login"); code != 0 || !equalStrings(e.opened, []string{e.approvalURL()}) {
			t.Errorf("code=%d opened=%v stderr=%s", code, e.opened, e.stderr)
		}
		if strings.Contains(e.stderr.String(), "브라우저") && strings.Contains(e.stderr.String(), "열지 못했습니다") {
			t.Errorf("no failure line expected:\n%s", e.stderr)
		}
	})
	t.Run("--no-browser opens nothing", func(t *testing.T) {
		e := newLoginEnv(t, false)
		e.approveAfter(1)
		if code := e.run("login", "--no-browser"); code != 0 || len(e.opened) != 0 {
			t.Errorf("code=%d opened=%v stderr=%s", code, e.opened, e.stderr)
		}
	})
	t.Run("without an opener nothing is opened", func(t *testing.T) {
		e := newLoginEnv(t, false)
		e.app.OpenBrowser = nil
		e.approveAfter(1)
		if code := e.run("login"); code != 0 {
			t.Errorf("code=%d stderr=%s", code, e.stderr)
		}
	})
	t.Run("a failure is one line and the wait goes on", func(t *testing.T) {
		e := newLoginEnv(t, false)
		e.app.OpenBrowser = func(string) error { return os.ErrNotExist }
		e.approveAfter(2)
		if code := e.run("login"); code != 0 {
			t.Fatalf("code=%d stderr=%s", code, e.stderr)
		}
		if got := strings.Count(e.stderr.String(), failure); got != 1 {
			t.Errorf("failure line count = %d:\n%s", got, e.stderr)
		}
		if strings.Contains(e.stderr.String(), os.ErrNotExist.Error()) {
			t.Errorf("the cause is not shown:\n%s", e.stderr)
		}
		if cfg, _ := loadConfig(e.app.ConfigPath); cfg.Tokens["codex"] != loginSecret {
			t.Errorf("config = %+v", cfg)
		}
	})
}

func TestLoginSendsTheClientHostOfTheTraceHeader(t *testing.T) {
	for name, tc := range map[string]struct {
		prep func(e *loginEnv)
		args []string
		want string
	}{
		"AGB_CLIENT_HOST":  {nil, []string{"login"}, "desk"},
		"host name":        {func(e *loginEnv) { delete(e.env, "AGB_CLIENT_HOST") }, []string{"login"}, "test-host"},
		"--client-host":    {nil, []string{"login", "--client-host", "box1"}, "box1"},
		"before the login": {nil, []string{"--client-host", "box2", "login"}, "box2"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newLoginEnv(t, false)
			e.approveAfter(1)
			if tc.prep != nil {
				tc.prep(e)
			}
			if code := e.run(tc.args...); code != 0 {
				t.Fatalf("code=%d stderr=%s", code, e.stderr)
			}
			if got := strings.TrimSpace(e.srv.reqs[0].Body); got != `{"actor":"codex","client_host":"`+tc.want+`"}` {
				t.Errorf("body = %s", got)
			}
		})
	}
}

func TestLoginActorComesFromTheUsualSettings(t *testing.T) {
	e := newLoginEnv(t, false)
	e.approveAfter(1)
	if code := e.run("--actor", "claude", "login"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	if got := strings.TrimSpace(e.srv.reqs[0].Body); got != `{"actor":"claude","client_host":"desk"}` {
		t.Errorf("body = %s", got)
	}
	// The token goes under the actor the login was made for.
	if cfg, _ := loadConfig(e.app.ConfigPath); cfg.Tokens["claude"] != loginSecret || len(cfg.Tokens) != 1 {
		t.Errorf("config = %+v", cfg)
	}

	// From the config file, when neither the flag nor the environment has it.
	e2 := newLoginEnv(t, false)
	e2.approveAfter(1)
	delete(e2.env, "AGB_ACTOR")
	saveConfig(e2.app.ConfigPath, Config{Actor: "gemini"})
	if code := e2.run("login"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, e2.stderr)
	}
	if cfg, _ := loadConfig(e2.app.ConfigPath); cfg.Tokens["gemini"] != loginSecret || cfg.Actor != "gemini" {
		t.Errorf("config = %+v", cfg)
	}
}

func TestLoginDenied(t *testing.T) {
	e := newLoginEnv(t, false)
	e.answerAfter(2, canned{status: 400, body: deniedJSON})
	if code := e.run("login", "--no-browser"); code != 1 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	if !strings.Contains(e.stderr.String(), "승인이 거부되었습니다.") || strings.Contains(e.stderr.String(), "서버가 거부를 알렸습니다.") {
		t.Errorf("stderr = %s", e.stderr)
	}
	if e.stdout.Len() != 0 {
		t.Errorf("stdout = %q", e.stdout.String())
	}
	if _, err := os.Stat(e.app.ConfigPath); err == nil {
		cfg, _ := loadConfig(e.app.ConfigPath)
		t.Errorf("a refused login saved %+v", cfg)
	}
	if n := e.count("POST /v1/tokens"); n != 2 {
		t.Errorf("exchanges = %d, want the polling to stop at the denial", n)
	}
}

func TestLoginExpired(t *testing.T) {
	const want = "승인 시간이 지났습니다. 다시 실행하세요."
	t.Run("the server says the request is gone", func(t *testing.T) {
		e := newLoginEnv(t, false)
		e.answerAfter(1, canned{status: 400, body: expiredJSON})
		if code := e.run("login"); code != 1 || !strings.Contains(e.stderr.String(), want) || strings.Contains(e.stderr.String(), "서버가 만료를 알렸습니다.") {
			t.Errorf("code=%d stderr=%s", code, e.stderr)
		}
		if e.stdout.Len() != 0 || e.count("POST /v1/tokens") != 1 {
			t.Errorf("stdout=%q exchanges=%d", e.stdout.String(), e.count("POST /v1/tokens"))
		}
	})
	t.Run("expires_in passes while the request stays pending", func(t *testing.T) {
		e := newLoginEnv(t, false)
		e.srv.on("POST", "/v1/device-authorizations", canned{status: 201, body: strings.Replace(grantJSON, `"expires_in":600`, `"expires_in":20`, 1)})
		if code := e.run("login"); code != 1 || !strings.Contains(e.stderr.String(), want) {
			t.Fatalf("code=%d stderr=%s", code, e.stderr)
		}
		// Attempts at 5, 10, 15 and 20 seconds; the one at 20 finds the time up.
		if len(e.slept) != 4 || e.count("POST /v1/tokens") != 4 {
			t.Errorf("slept=%v exchanges=%d", e.slept, e.count("POST /v1/tokens"))
		}
		if e.stdout.Len() != 0 {
			t.Errorf("stdout = %q", e.stdout.String())
		}
		if _, err := os.Stat(e.app.ConfigPath); err == nil {
			t.Error("an expired login wrote the config file")
		}
	})
	t.Run("the default lifetime is the ten minutes of the server", func(t *testing.T) {
		e := newLoginEnv(t, false)
		if code := e.run("login"); code != 1 || !strings.Contains(e.stderr.String(), want) {
			t.Fatalf("code=%d stderr=%s", code, e.stderr)
		}
		if len(e.slept) != 120 {
			t.Errorf("attempts = %d, want 120 (600 s at 5 s)", len(e.slept))
		}
	})
}

func TestLoginFollowsTheServerInterval(t *testing.T) {
	e := newLoginEnv(t, false)
	e.srv.on("POST", "/v1/device-authorizations", canned{status: 201, body: strings.Replace(grantJSON, `"interval":5`, `"interval":2`, 1)})
	e.approveAfter(3)
	if code := e.run("login"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	if want := []time.Duration{2 * time.Second, 2 * time.Second, 2 * time.Second}; !reflect.DeepEqual(e.slept, want) {
		t.Errorf("slept = %v, want %v", e.slept, want)
	}
}

func TestLoginCancelledByInterrupt(t *testing.T) {
	e := newLoginEnv(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.app.Sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}
	if code := e.app.Run(ctx, []string{"login"}); code != 1 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	if !strings.Contains(e.stderr.String(), "canceled") || e.stdout.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q", e.stdout.String(), e.stderr.String())
	}
	if e.count("POST /v1/tokens") != 0 {
		t.Error("an exchange was tried after the interrupt")
	}
	if _, err := os.Stat(e.app.ConfigPath); err == nil {
		t.Error("a cancelled login wrote the config file")
	}
}

func TestLoginStopsOnOtherErrors(t *testing.T) {
	e := newLoginEnv(t, true)
	e.answerAfter(2, canned{status: 500, body: `{"error":{"code":"internal","message":"서버 오류입니다."}}`})
	if code := e.run("login", "--no-browser"); code != 1 || !strings.Contains(e.stderr.String(), "500 internal") {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	if e.count("POST /v1/tokens") != 2 || e.stdout.Len() != 0 {
		t.Errorf("exchanges=%d stdout=%q", e.count("POST /v1/tokens"), e.stdout.String())
	}
}

func TestLoginFailsWhenTheRequestIsRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   []string
	}{
		"not an agent actor": {422, `{"error":{"code":"validation_failed","message":"요청 값이 규칙에 맞지 않습니다.","details":[{"field":"actor","message":"agent 종류의 행위자여야 합니다."}]}}`, []string{"422", "validation_failed", "actor", "agent 종류의 행위자여야 합니다."}},
		"too many pending":   {429, `{"error":{"code":"too_many_requests","message":"승인을 기다리는 요청이 너무 많습니다."}}`, []string{"429", "too_many_requests"}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newLoginEnv(t, true)
			e.srv.on("POST", "/v1/device-authorizations", canned{status: tc.status, body: tc.body})
			if code := e.run("login"); code != 1 {
				t.Fatalf("code=%d stderr=%s", code, e.stderr)
			}
			for _, w := range tc.want {
				if !strings.Contains(e.stderr.String(), w) {
					t.Errorf("stderr lacks %q:\n%s", w, e.stderr)
				}
			}
			if len(e.slept) != 0 || len(e.opened) != 0 || e.count("POST /v1/tokens") != 0 || e.stdout.Len() != 0 {
				t.Errorf("slept=%v opened=%v stdout=%q requests=%v", e.slept, e.opened, e.stdout.String(), e.srv.paths())
			}
		})
	}
}

func TestLoginFailsWhenTheServerIsUnreachable(t *testing.T) {
	e := newLoginEnv(t, true)
	e.env["AGB_URL"] = "http://127.0.0.1:1"
	if code := e.run("login"); code != 1 || e.stderr.Len() == 0 {
		t.Fatalf("code=%d stderr=%q", code, e.stderr.String())
	}
	if len(e.slept) != 0 || len(e.opened) != 0 || e.stdout.Len() != 0 {
		t.Errorf("slept=%v opened=%v stdout=%q", e.slept, e.opened, e.stdout.String())
	}
	if _, err := os.Stat(e.app.ConfigPath); err == nil {
		t.Error("a login that never reached the server wrote the config file")
	}
}

func TestLoginUsageErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		prep func(e *loginEnv)
		args []string
		want string
	}{
		"no actor":           {func(e *loginEnv) { delete(e.env, "AGB_ACTOR") }, []string{"login"}, "AGB_ACTOR"},
		"no server address":  {func(e *loginEnv) { delete(e.env, "AGB_URL") }, []string{"login"}, "AGB_URL"},
		"a bad actor":        {func(e *loginEnv) { e.env["AGB_ACTOR"] = "Codex" }, []string{"login"}, "행위자 ID 형식"},
		"a positional value": {nil, []string{"login", "now"}, "인자가 너무 많습니다"},
		"an unknown flag":    {nil, []string{"login", "--bogus"}, "bogus"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newLoginEnv(t, false)
			if tc.prep != nil {
				tc.prep(e)
			}
			if code := e.run(tc.args...); code != 2 || !strings.Contains(e.stderr.String(), tc.want) {
				t.Errorf("code=%d stderr=%s", code, e.stderr)
			}
			if len(e.srv.reqs) != 0 || len(e.opened) != 0 || e.stdout.Len() != 0 {
				t.Errorf("a usage error must do nothing: requests=%v opened=%v stdout=%q", e.srv.paths(), e.opened, e.stdout.String())
			}
		})
	}
}

func TestLoginKeepsTheRestOfTheConfig(t *testing.T) {
	e := newLoginEnv(t, false)
	e.approveAfter(1)
	saveConfig(e.app.ConfigPath, Config{
		URL: "http://file:1", Token: "shared-file-token", Actor: "owner", SessionID: "file-sess", ClientHost: "file-host",
		Tokens: map[string]string{"claude": "agb_claude-token", "codex": "agb_old-codex-token"},
	})
	if code := e.run("login"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	want := Config{
		URL: "http://file:1", Token: "shared-file-token", Actor: "owner", SessionID: "file-sess", ClientHost: "file-host",
		Tokens: map[string]string{"claude": "agb_claude-token", "codex": loginSecret},
	}
	if cfg, err := loadConfig(e.app.ConfigPath); err != nil || !reflect.DeepEqual(cfg, want) {
		t.Errorf("config = %+v %v, want %+v", cfg, err, want)
	}
}

// Two agents may log in at the same time. The one that finishes last must not
// write back the config it read when it started.
func TestLoginKeepsTokensSavedMeanwhile(t *testing.T) {
	e := newLoginEnv(t, false)
	saveConfig(e.app.ConfigPath, Config{Token: "shared-file-token", Tokens: map[string]string{"gemini": "agb_gemini-token"}})
	e.onSleep = func(n int) {
		switch n {
		case 1:
			saveConfig(e.app.ConfigPath, Config{Token: "shared-file-token", Tokens: map[string]string{"gemini": "agb_gemini-token", "claude": "agb_claude-token"}})
		case 2:
			e.srv.on("POST", "/v1/tokens", canned{status: 201, body: issuedJSON})
		}
	}
	if code := e.run("login"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	want := Config{Token: "shared-file-token", Tokens: map[string]string{"gemini": "agb_gemini-token", "claude": "agb_claude-token", "codex": loginSecret}}
	if cfg, _ := loadConfig(e.app.ConfigPath); !reflect.DeepEqual(cfg, want) {
		t.Errorf("config = %+v, want %+v", cfg, want)
	}
}

func TestLoginWritesTheConfigGivenByTheFlag(t *testing.T) {
	e := newLoginEnv(t, false)
	e.approveAfter(1)
	other := filepath.Join(t.TempDir(), "other.json")
	if code := e.run("--config", other, "login"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	if cfg, _ := loadConfig(other); cfg.Tokens["codex"] != loginSecret {
		t.Errorf("config at --config = %+v", cfg)
	}
	if _, err := os.Stat(e.app.ConfigPath); err == nil {
		t.Error("the default config path was written despite --config")
	}
}

func TestLoginTokenIsUsedByLaterCommands(t *testing.T) {
	e := newLoginEnv(t, false)
	e.approveAfter(1)
	e.srv.on("GET", "/v1/actors/me", canned{body: `{"id":"codex","kind":"agent"}`})
	if code := e.run("login"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, e.stderr)
	}
	if code := e.run("whoami"); code != 0 {
		t.Fatalf("whoami: code=%d stderr=%s", code, e.stderr)
	}
	req := e.srv.last()
	if req.Header.Get("Authorization") != "Bearer "+loginSecret || req.Header.Get("X-Actor") != "codex" {
		t.Errorf("headers = %v", req.Header)
	}
}
