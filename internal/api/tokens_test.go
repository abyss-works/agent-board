package api

import (
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

// loginActors declare two users and two agents, so a token can be told apart
// from a token of another agent and an approver from another approver.
var loginActors = []model.Actor{
	{ID: "owner", Kind: model.ActorUser},
	{ID: "boss", Kind: model.ActorUser},
	{ID: "claude", Kind: model.ActorAgent},
	{ID: "codex", Kind: model.ActorAgent},
}

// noCredentials removes the token and the trace headers from a request, as the
// two endpoints that start a login accept.
var noCredentials = map[string]string{"Authorization": "-", "X-Actor": "-", "X-Session-Id": "-", "X-Client-Host": "-"}

func (e *env) anon(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(t, method, target, body, noCredentials)
}

// withToken makes a request carry the secret and name the actor.
func withToken(secret, actor string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + secret, "X-Actor": actor}
}

// startLogin asks for a device authorization as the CLI of the agent would.
func (e *env) startLogin(t *testing.T, actor, host string) model.DeviceGrant {
	t.Helper()
	rec := e.anon(t, "POST", "/v1/device-authorizations", fmt.Sprintf(`{"actor":%q,"client_host":%q}`, actor, host))
	if rec.Code != 201 {
		t.Fatalf("start login: %d %s", rec.Code, rec.Body.String())
	}
	var g model.DeviceGrant
	decodeJSON(t, rec, &g)
	return g
}

// decide answers a device authorization as the user.
func (e *env) decide(t *testing.T, userCode, status, user string) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(t, "PATCH", "/v1/device-authorizations/"+userCode, fmt.Sprintf(`{"status":%q}`, status), map[string]string{"X-Actor": user})
}

func (e *env) exchange(t *testing.T, deviceCode string) *httptest.ResponseRecorder {
	t.Helper()
	return e.anon(t, "POST", "/v1/tokens", fmt.Sprintf(`{"device_code":%q}`, deviceCode))
}

// login runs the whole flow: the agent asks, the user approves, the agent
// exchanges the device code.
func (e *env) login(t *testing.T, actor, host, approver string) model.IssuedToken {
	t.Helper()
	g := e.startLogin(t, actor, host)
	if rec := e.decide(t, g.UserCode, "approved", approver); rec.Code != 200 {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	rec := e.exchange(t, g.DeviceCode)
	if rec.Code != 201 {
		t.Fatalf("exchange: %d %s", rec.Code, rec.Body.String())
	}
	var tok model.IssuedToken
	decodeJSON(t, rec, &tok)
	return tok
}

func (e *env) listTokens(t *testing.T, query string) []model.Token {
	t.Helper()
	rec := e.do(t, "GET", "/v1/tokens"+query, "", map[string]string{"X-Actor": "owner"})
	if rec.Code != 200 {
		t.Fatalf("list tokens %s: %d %s", query, rec.Code, rec.Body.String())
	}
	var l struct {
		Items []model.Token `json:"items"`
	}
	decodeJSON(t, rec, &l)
	return l.Items
}

func ids(items []model.Token) []int64 {
	out := make([]int64, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

// wantErr asserts the status and the error code, and when reason is not empty
// that the first detail has it.
func wantErr(t *testing.T, rec *httptest.ResponseRecorder, status int, code, reason string) errBody {
	t.Helper()
	b := decodeErr(t, rec)
	if rec.Code != status || b.Error.Code != code {
		t.Fatalf("status %d code %q, want %d %q: %s", rec.Code, b.Error.Code, status, code, rec.Body.String())
	}
	if b.Error.Message == "" {
		t.Errorf("empty error message: %s", rec.Body.String())
	}
	if reason != "" && (len(b.Error.Details) == 0 || b.Error.Details[0].Reason != reason) {
		t.Errorf("details = %+v, want the reason %q", b.Error.Details, reason)
	}
	return b
}

func TestSharedTokenIsForUserActorsOnly(t *testing.T) {
	e := newEnvWith(t, loginActors, nil)
	for _, actor := range []string{"owner", "boss"} {
		if rec := e.do(t, "GET", "/v1/actors/me", "", map[string]string{"X-Actor": actor}); rec.Code != 200 {
			t.Errorf("shared token as %s: %d %s", actor, rec.Code, rec.Body.String())
		}
	}
	for _, actor := range []string{"claude", "codex"} {
		rec := e.do(t, "GET", "/v1/actors/me", "", map[string]string{"Authorization": "Bearer " + testToken, "X-Actor": actor})
		b := wantErr(t, rec, 403, "forbidden", "actor_mismatch")
		if b.Error.Details[0].Field != "X-Actor" {
			t.Errorf("%s: details = %+v", actor, b.Error.Details)
		}
		lg := e.lastLog(t)
		if lg["reject"] != "actor_mismatch" || lg["status"] != float64(403) || lg["actor"] != actor {
			t.Errorf("%s: log = %v", actor, lg)
		}
		if _, ok := lg["token_id"]; ok {
			t.Errorf("%s: a shared token has no token_id: %v", actor, lg)
		}
	}
	// The check comes after the trace headers and before the path.
	if rec := e.do(t, "GET", "/v1/nope", "", map[string]string{"Authorization": "Bearer " + testToken, "X-Actor": "claude"}); rec.Code != 403 {
		t.Errorf("unknown path with a mismatching actor: %d, want 403 before 404", rec.Code)
	}
	wantErr(t, e.do(t, "GET", "/v1/actors/me", "", map[string]string{"Authorization": "Bearer " + testToken, "X-Actor": "gemini"}), 400, "bad_request", "unknown_actor")
	wantErr(t, e.do(t, "GET", "/v1/actors/me", "", map[string]string{"Authorization": "Bearer " + testToken, "X-Actor": "claude", "X-Session-Id": "-"}), 400, "bad_request", "missing_header")
}

func TestActorTokenIsBoundToItsActor(t *testing.T) {
	e := newEnvWith(t, loginActors, nil)
	claude := e.login(t, "claude", "desk", "owner")
	codex := e.login(t, "codex", "desk", "owner")

	rec := e.do(t, "GET", "/v1/actors/me", "", withToken(claude.Secret, "claude"))
	var me model.Actor
	decodeJSON(t, rec, &me)
	if rec.Code != 200 || me.ID != "claude" {
		t.Fatalf("claude with its own token: %d %s", rec.Code, rec.Body.String())
	}
	if lg := e.lastLog(t); lg["token_id"] != float64(claude.ID) {
		t.Errorf("log token_id = %v, want %d: %v", lg["token_id"], claude.ID, lg)
	}

	for _, actor := range []string{"owner", "boss", "codex"} {
		rec := e.do(t, "GET", "/v1/actors/me", "", withToken(claude.Secret, actor))
		b := wantErr(t, rec, 403, "forbidden", "actor_mismatch")
		if b.Error.Details[0].Field != "X-Actor" {
			t.Errorf("claude's token as %s: details = %+v", actor, b.Error.Details)
		}
		lg := e.lastLog(t)
		// The token authenticated, so the rejected request still names it.
		if lg["reject"] != "actor_mismatch" || lg["status"] != float64(403) || lg["token_id"] != float64(claude.ID) || lg["actor"] != actor {
			t.Errorf("claude's token as %s: log = %v", actor, lg)
		}
	}
	wantErr(t, e.do(t, "GET", "/v1/actors/me", "", withToken(codex.Secret, "claude")), 403, "forbidden", "actor_mismatch")

	// Order: trace headers, then the actor, then the path.
	wantErr(t, e.do(t, "GET", "/v1/nope", "", withToken(claude.Secret, "codex")), 403, "forbidden", "actor_mismatch")
	missing := withToken(claude.Secret, "codex")
	missing["X-Session-Id"] = "-"
	wantErr(t, e.do(t, "GET", "/v1/actors/me", "", missing), 400, "bad_request", "missing_header")
	wantErr(t, e.do(t, "GET", "/v1/actors/me", "", withToken(claude.Secret, "gemini")), 400, "bad_request", "unknown_actor")
	if rec := e.do(t, "GET", "/v1/nope", "", withToken(claude.Secret, "claude")); rec.Code != 404 {
		t.Errorf("unknown path with the right actor: %d", rec.Code)
	}
}

func TestRevokedOrUnknownActorTokenIsUnauthorized(t *testing.T) {
	e := newEnvWith(t, loginActors, nil)
	claude := e.login(t, "claude", "desk", "owner")
	if rec := e.do(t, "GET", "/v1/actors/me", "", withToken(claude.Secret, "claude")); rec.Code != 200 {
		t.Fatalf("before the revocation: %d", rec.Code)
	}
	if rec := e.do(t, "DELETE", fmt.Sprintf("/v1/tokens/%d", claude.ID), "", map[string]string{"X-Actor": "owner"}); rec.Code != 200 {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body.String())
	}
	for name, secret := range map[string]string{
		"revoked":       claude.Secret,
		"unknown":       model.TokenPrefix + strings.Repeat("A", 43),
		"empty":         "",
		"prefix only":   model.TokenPrefix,
		"shared token+": testToken + "x",
		"case changed":  strings.ToUpper(claude.Secret),
	} {
		rec := e.do(t, "GET", "/v1/actors/me", "", withToken(secret, "claude"))
		wantErr(t, rec, 401, "unauthorized", "")
		lg := e.lastLog(t)
		if lg["reject"] != "unauthorized" {
			t.Errorf("%s: log = %v", name, lg)
		}
		if _, ok := lg["token_id"]; ok {
			t.Errorf("%s: a rejected token has no token_id: %v", name, lg)
		}
	}
}

func TestRequestLogNeverHoldsASecret(t *testing.T) {
	e := newEnvWith(t, loginActors, nil)
	tok := e.login(t, "claude", "desk", "owner")
	e.do(t, "GET", "/v1/actors/me", "", withToken(tok.Secret, "claude"))
	e.do(t, "GET", "/v1/actors/me", "", withToken(tok.Secret, "codex"))
	e.do(t, "GET", "/v1/actors/me", "", withToken(testToken, "owner"))
	e.do(t, "GET", "/v1/actors/me", "", withToken("agb_wrong", "claude"))
	g := e.startLogin(t, "codex", "desk")
	e.exchange(t, g.DeviceCode)
	for _, secret := range []string{tok.Secret, testToken, "agb_wrong", g.DeviceCode} {
		if strings.Contains(e.log.String(), secret) {
			t.Errorf("the request log holds %q", secret)
		}
	}
	if lg := e.lastLog(t); lg["path"] != "/v1/tokens" || lg["status"] != float64(400) || lg["actor"] != "" || lg["session_id"] != "" || lg["client_host"] != "" {
		t.Errorf("log of an unauthenticated request = %v", lg)
	}
	e.do(t, "GET", "/v1/actors/me", "", map[string]string{"X-Actor": "owner"})
	if lg := e.lastLog(t); lg["token_id"] != nil {
		t.Errorf("the shared token has no token_id: %v", lg)
	}
}

func TestActorTokenRecordsItsLastUse(t *testing.T) {
	e := newEnvWith(t, loginActors, nil)
	tok := e.login(t, "claude", "desk", "owner")
	if got := e.listTokens(t, ""); len(got) != 1 || got[0].LastUsedAt != nil {
		t.Fatalf("a token never used: %+v", got)
	}
	if rec := e.do(t, "POST", "/v1/projects", `{"key":"AGB","name":"agent-board"}`, withToken(tok.Secret, "claude")); rec.Code != 201 {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}
	var p model.Project
	decodeJSON(t, e.do(t, "GET", "/v1/projects/AGB", "", withToken(tok.Secret, "claude")), &p)
	if p.CreatedBy != "claude" {
		t.Errorf("the project is attributed to %q, want the actor of the token", p.CreatedBy)
	}
	got := e.listTokens(t, "")
	if len(got) != 1 || got[0].LastUsedAt == nil {
		t.Fatalf("a token that was used: %+v", got)
	}
	first := *got[0].LastUsedAt
	e.do(t, "GET", "/v1/actors/me", "", withToken(tok.Secret, "claude"))
	if again := e.listTokens(t, ""); *again[0].LastUsedAt != first {
		t.Errorf("last_used_at moved within a minute: %v -> %v", first, *again[0].LastUsedAt)
	}
	// Using the shared token touches no actor token.
	e.do(t, "GET", "/v1/actors/me", "", map[string]string{"X-Actor": "owner"})
}

func TestCreateDeviceAuthorization(t *testing.T) {
	clock := newFakeClock()
	e := newEnvWith(t, loginActors, func(o *Options) { o.Now = clock.Now; o.Rand = &countingReader{} })
	rec := e.anon(t, "POST", "/v1/device-authorizations", `{"actor":"codex","client_host":"DESKTOP-RPLTFHM"}`)
	if rec.Code != 201 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var g model.DeviceGrant
	decodeJSON(t, rec, &g)
	device := make([]byte, 32)
	for i := range device {
		device[i] = byte(i)
	}
	letters := ""
	for b := 32; b < 40; b++ {
		letters += string(model.UserCodeAlphabet[b%20])
	}
	if want := base64.RawURLEncoding.EncodeToString(device); g.DeviceCode != want {
		t.Errorf("device_code = %q, want %q (32 bytes, base64url without padding)", g.DeviceCode, want)
	}
	if g.UserCode != model.FormatUserCode(letters) || !userCodeRE.MatchString(g.UserCode) {
		t.Errorf("user_code = %q, want %q", g.UserCode, model.FormatUserCode(letters))
	}
	if g.ExpiresIn != 600 || g.Interval != 5 {
		t.Errorf("expires_in %d, interval %d", g.ExpiresIn, g.Interval)
	}
	if loc := rec.Header().Get("Location"); loc != "/v1/device-authorizations/"+g.UserCode {
		t.Errorf("Location = %q", loc)
	}
	for _, key := range []string{`"device_code"`, `"user_code"`, `"expires_in":600`, `"interval":5`} {
		if !strings.Contains(rec.Body.String(), key) {
			t.Errorf("response lacks %s: %s", key, rec.Body.String())
		}
	}
	lg := e.lastLog(t)
	if lg["method"] != "POST" || lg["path"] != "/v1/device-authorizations" || lg["status"] != float64(201) {
		t.Errorf("the request is logged like any other: %v", lg)
	}
	if _, ok := lg["reject"]; ok {
		t.Errorf("reject on a good request: %v", lg)
	}

	// Credentials and trace headers are not looked at on this path, right or wrong.
	for name, hdr := range map[string]map[string]string{
		"wrong token":         {"Authorization": "Bearer wrong", "X-Actor": "-", "X-Session-Id": "-", "X-Client-Host": "-"},
		"bad trace headers":   {"Authorization": "-", "X-Actor": "NOPE", "X-Session-Id": "a b", "X-Client-Host": "-bad"},
		"shared token, agent": {"Authorization": "Bearer " + testToken, "X-Actor": "claude"},
	} {
		if rec := e.do(t, "POST", "/v1/device-authorizations", `{"actor":"claude","client_host":"desk"}`, hdr); rec.Code != 201 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestCreateDeviceAuthorizationRules(t *testing.T) {
	e := newEnvWith(t, loginActors, nil)
	post := func(body string) *httptest.ResponseRecorder {
		return e.anon(t, "POST", "/v1/device-authorizations", body)
	}
	for name, c := range map[string]struct {
		body   string
		fields []string
	}{
		"user actor":        {`{"actor":"owner","client_host":"desk"}`, []string{"actor"}},
		"undeclared actor":  {`{"actor":"gemini","client_host":"desk"}`, []string{"actor"}},
		"empty actor":       {`{"actor":"","client_host":"desk"}`, []string{"actor"}},
		"bad host":          {`{"actor":"claude","client_host":"my host"}`, []string{"client_host"}},
		"empty host":        {`{"actor":"claude","client_host":""}`, []string{"client_host"}},
		"long host":         {`{"actor":"claude","client_host":"` + strings.Repeat("h", 65) + `"}`, []string{"client_host"}},
		"both wrong":        {`{"actor":"owner","client_host":""}`, []string{"actor", "client_host"}},
		"nothing":           {`{}`, []string{"actor", "client_host"}},
		"null fields":       {`{"actor":null,"client_host":null}`, []string{"actor", "client_host"}},
		"missing host only": {`{"actor":"claude"}`, []string{"client_host"}},
	} {
		b := wantErr(t, post(c.body), 422, "validation_failed", "")
		var got []string
		for _, d := range b.Error.Details {
			got = append(got, d.Field)
		}
		if strings.Join(got, ",") != strings.Join(c.fields, ",") {
			t.Errorf("%s: fields = %v, want %v", name, got, c.fields)
		}
	}
	for _, body := range []string{
		`{"actor":"claude","client_host":"desk","scope":"all"}`, // unknown field
		`{"actor":"claude","client_host":7}`,                    // wrong type
		`{"actor":"claude","client_host":`,                      // syntax error
		`["claude"]`,                                            // not an object
		``,                                                      // empty body
	} {
		wantErr(t, post(body), 400, "bad_request", "")
	}
	b := wantErr(t, post(`{"actor":"claude","client_host":"desk","scope":"all"}`), 400, "bad_request", "unknown_field")
	if b.Error.Details[0].Field != "scope" {
		t.Errorf("details = %+v", b.Error.Details)
	}
	wantErr(t, post(`{"actor":"claude","client_host":"`+strings.Repeat("h", 1<<20)+`"}`), 413, "payload_too_large", "")
}

func TestDeviceAuthorizationsWaitingAreLimited(t *testing.T) {
	clock := newFakeClock()
	e := newEnvWith(t, loginActors, func(o *Options) { o.Now = clock.Now })
	var first model.DeviceGrant
	for i := 0; i < 20; i++ {
		g := e.startLogin(t, "codex", fmt.Sprintf("pc%d", i))
		if i == 0 {
			first = g
		}
	}
	rec := e.anon(t, "POST", "/v1/device-authorizations", `{"actor":"codex","client_host":"one-more"}`)
	wantErr(t, rec, 429, "too_many_requests", "")
	if lg := e.lastLog(t); lg["status"] != float64(429) {
		t.Errorf("log = %v", lg)
	}
	// Deciding a request frees its place; so does the passing of time.
	if rec := e.decide(t, first.UserCode, "denied", "owner"); rec.Code != 200 {
		t.Fatalf("deny: %d %s", rec.Code, rec.Body.String())
	}
	e.startLogin(t, "codex", "after-deny")
	wantErr(t, e.anon(t, "POST", "/v1/device-authorizations", `{"actor":"codex","client_host":"full-again"}`), 429, "too_many_requests", "")
	clock.Advance(600 * time.Second)
	e.startLogin(t, "codex", "after-expiry")
}

func TestGetDeviceAuthorization(t *testing.T) {
	clock := newFakeClock()
	e := newEnvWith(t, loginActors, func(o *Options) { o.Now = clock.Now })
	e.st.SetClock(clock.Now)
	g := e.startLogin(t, "codex", "DESKTOP-RPLTFHM")
	owner := map[string]string{"X-Actor": "owner"}

	rec := e.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, "", owner)
	var d model.DeviceAuthorization
	decodeJSON(t, rec, &d)
	if rec.Code != 200 || d.UserCode != g.UserCode || d.Actor != "codex" || d.ClientHost != "DESKTOP-RPLTFHM" || d.Status != model.DevicePending ||
		d.CreatedAt != model.NewTime(clock.Now()) || d.ExpiresAt != model.NewTime(clock.Now().Add(600*time.Second)) {
		t.Fatalf("device authorization = %d %+v", rec.Code, d)
	}
	if strings.Contains(rec.Body.String(), g.DeviceCode) || strings.Contains(rec.Body.String(), "device_code") {
		t.Errorf("the device code must not be shown to the user: %s", rec.Body.String())
	}
	for name, code := range map[string]string{
		"lower case":       strings.ToLower(g.UserCode),
		"without a hyphen": strings.ReplaceAll(g.UserCode, "-", ""),
		"lower, no hyphen": strings.ToLower(strings.ReplaceAll(g.UserCode, "-", "")),
	} {
		rec := e.do(t, "GET", "/v1/device-authorizations/"+code, "", owner)
		var got model.DeviceAuthorization
		decodeJSON(t, rec, &got)
		if rec.Code != 200 || got.UserCode != g.UserCode {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := e.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, "", map[string]string{"X-Actor": "boss"}); rec.Code != 200 {
		t.Errorf("another user: %d", rec.Code)
	}

	for _, code := range []string{"BCDF-GHJK", "nope", "AEIO-AEIO", "BCDF"} { // unknown, malformed, vowels, too short
		wantErr(t, e.do(t, "GET", "/v1/device-authorizations/"+code, "", owner), 404, "not_found", "")
	}
	wantErr(t, e.do(t, "GET", "/v1/device-authorizations/"+g.UserCode+"?x=1", "", owner), 400, "bad_request", "")

	// Agents may not read it, with a token of their own or with the shared token.
	claude := e.login(t, "claude", "desk", "owner")
	wantErr(t, e.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, "", withToken(claude.Secret, "claude")), 403, "forbidden", "user_only")
	wantErr(t, e.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, "", withToken(testToken, "claude")), 403, "forbidden", "actor_mismatch")
	wantErr(t, e.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, "", map[string]string{"Authorization": "-"}), 401, "unauthorized", "")
	wantErr(t, e.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, "", map[string]string{"X-Actor": "-"}), 400, "bad_request", "missing_header")

	clock.Advance(600 * time.Second)
	wantErr(t, e.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, "", owner), 404, "not_found", "")
}

func TestDecideDeviceAuthorization(t *testing.T) {
	e := newEnvWith(t, loginActors, nil)
	claude := e.login(t, "claude", "desk", "owner")
	g := e.startLogin(t, "codex", "pc")

	decide := func(code, body string, actor string) *httptest.ResponseRecorder {
		return e.do(t, "PATCH", "/v1/device-authorizations/"+code, body, map[string]string{"X-Actor": actor})
	}
	// An agent cannot approve its own request, nor anyone's.
	wantErr(t, e.do(t, "PATCH", "/v1/device-authorizations/"+g.UserCode, `{"status":"approved"}`, withToken(claude.Secret, "claude")), 403, "forbidden", "user_only")
	wantErr(t, e.do(t, "PATCH", "/v1/device-authorizations/"+g.UserCode, `{"status":"approved"}`, withToken(testToken, "codex")), 403, "forbidden", "actor_mismatch")
	var d model.DeviceAuthorization
	decodeJSON(t, e.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, "", map[string]string{"X-Actor": "owner"}), &d)
	if d.Status != model.DevicePending {
		t.Fatalf("a rejected decision changed the request: %+v", d)
	}

	// Invalid decisions change nothing.
	for name, c := range map[string]struct {
		body   string
		status int
		code   string
	}{
		"pending":       {`{"status":"pending"}`, 422, "validation_failed"},
		"unknown value": {`{"status":"maybe"}`, 422, "validation_failed"},
		"empty":         {`{"status":""}`, 422, "validation_failed"},
		"upper case":    {`{"status":"Approved"}`, 422, "validation_failed"},
		"null":          {`{"status":null}`, 422, "validation_failed"},
		"missing":       {`{}`, 422, "validation_failed"},
		"wrong type":    {`{"status":1}`, 400, "bad_request"},
		"unknown field": {`{"status":"approved","actor":"claude"}`, 400, "bad_request"},
		"syntax error":  {`{"status":`, 400, "bad_request"},
	} {
		rec := decide(g.UserCode, c.body, "owner")
		b := wantErr(t, rec, c.status, c.code, "")
		if c.status == 422 && (len(b.Error.Details) != 1 || b.Error.Details[0].Field != "status") {
			t.Errorf("%s: details = %+v", name, b.Error.Details)
		}
	}
	decodeJSON(t, e.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, "", map[string]string{"X-Actor": "owner"}), &d)
	if d.Status != model.DevicePending {
		t.Fatalf("an invalid decision changed the request: %+v", d)
	}

	// Approve, with the code typed in lower case and without the hyphen.
	rec := decide(strings.ToLower(strings.ReplaceAll(g.UserCode, "-", "")), `{"status":"approved"}`, "owner")
	decodeJSON(t, rec, &d)
	if rec.Code != 200 || d.Status != model.DeviceApproved || d.UserCode != g.UserCode || d.Actor != "codex" || d.ClientHost != "pc" {
		t.Fatalf("approve: %d %+v", rec.Code, d)
	}
	// A request that is decided cannot be decided again, whichever way.
	for _, status := range []string{"approved", "denied"} {
		wantErr(t, decide(g.UserCode, fmt.Sprintf(`{"status":%q}`, status), "boss"), 409, "conflict", "not_pending")
	}
	decodeJSON(t, e.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, "", map[string]string{"X-Actor": "owner"}), &d)
	if d.Status != model.DeviceApproved {
		t.Errorf("a conflicting decision changed the request: %+v", d)
	}

	other := e.startLogin(t, "claude", "pc")
	rec = decide(other.UserCode, `{"status":"denied"}`, "boss")
	decodeJSON(t, rec, &d)
	if rec.Code != 200 || d.Status != model.DeviceDenied {
		t.Errorf("deny: %d %s", rec.Code, rec.Body.String())
	}
	wantErr(t, decide("BCDF-GHJK", `{"status":"approved"}`, "owner"), 404, "not_found", "")
	wantErr(t, decide("nope", `{"status":"approved"}`, "owner"), 404, "not_found", "")
}

func TestDeviceAuthorizationRoutes(t *testing.T) {
	e := newEnvWith(t, loginActors, nil)
	owner := map[string]string{"X-Actor": "owner"}
	for method, c := range map[string]struct{ path, allow string }{
		"GET":    {"/v1/device-authorizations", "POST"},
		"PUT":    {"/v1/device-authorizations/BCDF-GHJK", "GET, PATCH"},
		"DELETE": {"/v1/device-authorizations/BCDF-GHJK", "GET, PATCH"},
		"POST":   {"/v1/device-authorizations/BCDF-GHJK", "GET, PATCH"},
	} {
		rec := e.do(t, method, c.path, "", owner)
		wantErr(t, rec, 405, "method_not_allowed", "")
		if rec.Header().Get("Allow") != c.allow {
			t.Errorf("%s %s: Allow = %q, want %q", method, c.path, rec.Header().Get("Allow"), c.allow)
		}
		// Only the creation is open to everyone; any other request needs the token first.
		wantErr(t, e.anon(t, method, c.path, ""), 401, "unauthorized", "")
	}
}

func TestExchangeTokenFlow(t *testing.T) {
	clock := newFakeClock()
	e := newEnvWith(t, loginActors, func(o *Options) { o.Now = clock.Now; o.Rand = &countingReader{} })
	e.st.SetClock(clock.Now)
	g := e.startLogin(t, "codex", "DESKTOP-RPLTFHM")

	// Nothing is issued while the user has not decided.
	wantErr(t, e.exchange(t, g.DeviceCode), 400, "authorization_pending", "")
	wantErr(t, e.exchange(t, g.DeviceCode), 400, "authorization_pending", "")
	if got := e.listTokens(t, "?status=all"); len(got) != 0 {
		t.Fatalf("tokens while pending: %+v", got)
	}

	clock.Advance(90 * time.Second)
	if rec := e.decide(t, g.UserCode, "approved", "boss"); rec.Code != 200 {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	rec := e.exchange(t, g.DeviceCode)
	if rec.Code != 201 {
		t.Fatalf("exchange: %d %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("a token has no page of its own, so no Location: %q", loc)
	}
	var tok model.IssuedToken
	decodeJSON(t, rec, &tok)
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(40 + i) // after the 32 bytes of the device code and the 8 of the user code
	}
	if want := "agb_" + base64.RawURLEncoding.EncodeToString(secret); tok.Secret != want || len(tok.Secret) != 47 {
		t.Errorf("token = %q, want %q (agb_ and 32 bytes in base64url without padding)", tok.Secret, want)
	}
	if tok.ID < 1 || tok.Actor != "codex" || tok.ClientHost != "DESKTOP-RPLTFHM" || tok.CreatedBy != "boss" ||
		tok.CreatedAt != model.NewTime(clock.Now()) || tok.LastUsedAt != nil || tok.RevokedAt != nil || tok.RevokedBy != nil {
		t.Errorf("token = %+v", tok)
	}
	for _, key := range []string{`"last_used_at":null`, `"revoked_at":null`, `"revoked_by":null`, `"token":"agb_`} {
		if !strings.Contains(rec.Body.String(), key) {
			t.Errorf("response lacks %s: %s", key, rec.Body.String())
		}
	}

	// The token works for its actor and for nobody else.
	if rec := e.do(t, "GET", "/v1/actors/me", "", withToken(tok.Secret, "codex")); rec.Code != 200 {
		t.Errorf("the issued token: %d %s", rec.Code, rec.Body.String())
	}
	wantErr(t, e.do(t, "GET", "/v1/actors/me", "", withToken(tok.Secret, "claude")), 403, "forbidden", "actor_mismatch")

	// The request is gone with the exchange.
	wantErr(t, e.exchange(t, g.DeviceCode), 400, "expired_token", "")
	wantErr(t, e.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, "", map[string]string{"X-Actor": "owner"}), 404, "not_found", "")
	if got := e.listTokens(t, "?status=all"); len(got) != 1 {
		t.Errorf("tokens after a second exchange: %+v", got)
	}
}

func TestExchangeDeniedAndExpired(t *testing.T) {
	clock := newFakeClock()
	e := newEnvWith(t, loginActors, func(o *Options) { o.Now = clock.Now })
	e.st.SetClock(clock.Now)

	denied := e.startLogin(t, "codex", "pc")
	if rec := e.decide(t, denied.UserCode, "denied", "owner"); rec.Code != 200 {
		t.Fatalf("deny: %d %s", rec.Code, rec.Body.String())
	}
	wantErr(t, e.exchange(t, denied.DeviceCode), 400, "access_denied", "")
	// The denial is reported once; after that the request is gone.
	wantErr(t, e.exchange(t, denied.DeviceCode), 400, "expired_token", "")

	waiting := e.startLogin(t, "claude", "pc")
	approved := e.startLogin(t, "codex", "pc2")
	if rec := e.decide(t, approved.UserCode, "approved", "owner"); rec.Code != 200 {
		t.Fatalf("approve: %d", rec.Code)
	}
	clock.Advance(600*time.Second - time.Millisecond)
	wantErr(t, e.exchange(t, waiting.DeviceCode), 400, "authorization_pending", "")
	clock.Advance(time.Millisecond)
	wantErr(t, e.exchange(t, waiting.DeviceCode), 400, "expired_token", "")
	// An approval that was not collected in time is lost with the request.
	wantErr(t, e.exchange(t, approved.DeviceCode), 400, "expired_token", "")
	if got := e.listTokens(t, "?status=all"); len(got) != 0 {
		t.Errorf("tokens after a denial and an expiry: %+v", got)
	}
}

func TestExchangeTokenRules(t *testing.T) {
	e := newEnvWith(t, loginActors, nil)
	g := e.startLogin(t, "codex", "pc")
	post := func(body string) *httptest.ResponseRecorder { return e.anon(t, "POST", "/v1/tokens", body) }

	for name, body := range map[string]string{
		"never issued":    `{"device_code":"` + strings.Repeat("A", 43) + `"}`,
		"the user code":   fmt.Sprintf(`{"device_code":%q}`, g.UserCode),
		"a similar code":  fmt.Sprintf(`{"device_code":%q}`, g.DeviceCode+"A"),
		"another case":    fmt.Sprintf(`{"device_code":%q}`, strings.ToLower(g.DeviceCode)+"x"),
		"whitespace only": `{"device_code":" "}`,
	} {
		if rec := post(body); rec.Code != 400 || decodeErr(t, rec).Error.Code != "expired_token" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	for name, body := range map[string]string{"missing": `{}`, "null": `{"device_code":null}`, "empty": `{"device_code":""}`} {
		b := wantErr(t, post(body), 422, "validation_failed", "")
		if len(b.Error.Details) != 1 || b.Error.Details[0].Field != "device_code" {
			t.Errorf("%s: details = %+v", name, b.Error.Details)
		}
	}
	for _, body := range []string{
		fmt.Sprintf(`{"device_code":%q,"actor":"claude"}`, g.DeviceCode), // unknown field
		`{"device_code":5}`, // wrong type
		`{"device_code":`,   // syntax error
		`"abc"`,             // not an object
		``,                  // empty body
	} {
		wantErr(t, post(body), 400, "bad_request", "")
	}
	wantErr(t, post(`{"device_code":"`+strings.Repeat("a", 1<<20)+`"}`), 413, "payload_too_large", "")

	// None of this used up the request, and credentials play no part.
	wantErr(t, e.do(t, "POST", "/v1/tokens", fmt.Sprintf(`{"device_code":%q}`, g.DeviceCode), map[string]string{"Authorization": "Bearer wrong", "X-Actor": "NOPE"}), 400, "authorization_pending", "")
	if rec := e.decide(t, g.UserCode, "approved", "owner"); rec.Code != 200 {
		t.Fatalf("approve: %d", rec.Code)
	}
	rec := e.do(t, "POST", "/v1/tokens", fmt.Sprintf(`{"device_code":%q}`, g.DeviceCode), map[string]string{"Authorization": "Bearer wrong", "X-Actor": "NOPE"})
	if rec.Code != 201 {
		t.Errorf("exchange with unrelated credentials: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLoginRevokesTheOlderTokenOfTheSameDevice(t *testing.T) {
	e := newEnvWith(t, loginActors, nil)
	older := e.login(t, "claude", "desk", "owner")
	otherDevice := e.login(t, "claude", "laptop", "owner")
	otherActor := e.login(t, "codex", "desk", "owner")
	newer := e.login(t, "claude", "desk", "boss")

	wantErr(t, e.do(t, "GET", "/v1/actors/me", "", withToken(older.Secret, "claude")), 401, "unauthorized", "")
	for name, tok := range map[string]model.IssuedToken{"new": newer, "another device": otherDevice} {
		if rec := e.do(t, "GET", "/v1/actors/me", "", withToken(tok.Secret, "claude")); rec.Code != 200 {
			t.Errorf("%s token: %d", name, rec.Code)
		}
	}
	if rec := e.do(t, "GET", "/v1/actors/me", "", withToken(otherActor.Secret, "codex")); rec.Code != 200 {
		t.Errorf("another actor's token: %d", rec.Code)
	}
	revoked := e.listTokens(t, "?status=revoked")
	if len(revoked) != 1 || revoked[0].ID != older.ID || revoked[0].RevokedBy == nil || *revoked[0].RevokedBy != "boss" || revoked[0].RevokedAt == nil {
		t.Errorf("revoked = %+v, want the older token, revoked by the approver of the new one", revoked)
	}
	if active := e.listTokens(t, ""); len(active) != 3 {
		t.Errorf("active tokens = %+v", active)
	}
}

func TestListTokens(t *testing.T) {
	e := newEnvWith(t, loginActors, nil)
	a := e.login(t, "claude", "desk", "owner")
	b := e.login(t, "codex", "desk", "owner")
	c := e.login(t, "claude", "laptop", "owner")
	if rec := e.do(t, "DELETE", fmt.Sprintf("/v1/tokens/%d", b.ID), "", map[string]string{"X-Actor": "owner"}); rec.Code != 200 {
		t.Fatalf("revoke: %d", rec.Code)
	}

	for query, want := range map[string][]int64{
		"":                {c.ID, a.ID},
		"?status=":        {c.ID, a.ID},
		"?status=active":  {c.ID, a.ID},
		"?status=revoked": {b.ID},
		"?status=all":     {c.ID, b.ID, a.ID},
	} {
		got := ids(e.listTokens(t, query))
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%q: %v, want %v", query, got, want)
		}
	}
	rec := e.do(t, "GET", "/v1/tokens?status=all", "", map[string]string{"X-Actor": "boss"})
	if rec.Code != 200 {
		t.Fatalf("another user: %d", rec.Code)
	}
	var raw map[string]any
	decodeJSON(t, rec, &raw)
	if len(raw) != 1 || raw["items"] == nil {
		t.Errorf("the list holds items only: %v", raw)
	}
	for _, secret := range []string{a.Secret, b.Secret, c.Secret} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("the list shows a secret: %s", rec.Body.String())
		}
	}
	if strings.Contains(rec.Body.String(), `"token":`) || strings.Contains(rec.Body.String(), "token_hash") {
		t.Errorf("a listed token has no token field: %s", rec.Body.String())
	}
	if empty := newEnvWith(t, loginActors, nil); !strings.Contains(empty.do(t, "GET", "/v1/tokens", "", map[string]string{"X-Actor": "owner"}).Body.String(), `"items":[]`) {
		t.Error("an empty list must be [] not null")
	}

	for _, query := range []string{"?status=bogus", "?status=active,revoked", "?status=Active", "?status=all&status=active", "?limit=1", "?offset=1", "?sort=id", "?x=1"} {
		wantErr(t, e.do(t, "GET", "/v1/tokens"+query, "", map[string]string{"X-Actor": "owner"}), 400, "bad_request", "invalid_query")
	}

	// Only a user lists tokens, and an agent cannot take the shared token for it.
	wantErr(t, e.do(t, "GET", "/v1/tokens", "", withToken(a.Secret, "claude")), 403, "forbidden", "user_only")
	wantErr(t, e.do(t, "GET", "/v1/tokens", "", withToken(testToken, "claude")), 403, "forbidden", "actor_mismatch")
	wantErr(t, e.do(t, "GET", "/v1/tokens", "", map[string]string{"Authorization": "-"}), 401, "unauthorized", "")
	wantErr(t, e.do(t, "GET", "/v1/tokens", "", map[string]string{"X-Actor": "-"}), 400, "bad_request", "missing_header")
}

func TestRevokeToken(t *testing.T) {
	e := newEnvWith(t, loginActors, nil)
	tok := e.login(t, "codex", "desk", "owner")
	claude := e.login(t, "claude", "desk", "owner")
	path := fmt.Sprintf("/v1/tokens/%d", tok.ID)

	// An agent cannot revoke, not even its own token by id.
	wantErr(t, e.do(t, "DELETE", path, "", withToken(claude.Secret, "claude")), 403, "forbidden", "user_only")
	wantErr(t, e.do(t, "DELETE", fmt.Sprintf("/v1/tokens/%d", claude.ID), "", withToken(claude.Secret, "claude")), 403, "forbidden", "user_only")
	if rec := e.do(t, "GET", "/v1/actors/me", "", withToken(tok.Secret, "codex")); rec.Code != 200 {
		t.Fatalf("a refused revocation revoked the token: %d", rec.Code)
	}

	rec := e.do(t, "DELETE", path, "", map[string]string{"X-Actor": "boss"})
	var got model.Token
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.ID != tok.ID || got.Actor != "codex" || got.RevokedAt == nil || got.RevokedBy == nil || *got.RevokedBy != "boss" || got.LastUsedAt == nil {
		t.Fatalf("revoke = %d %+v", rec.Code, got)
	}
	if strings.Contains(rec.Body.String(), `"token":`) {
		t.Errorf("a revoked token shows no secret: %s", rec.Body.String())
	}
	wantErr(t, e.do(t, "GET", "/v1/actors/me", "", withToken(tok.Secret, "codex")), 401, "unauthorized", "")

	// Revoking again changes nothing and answers the same.
	rec = e.do(t, "DELETE", path, "", map[string]string{"X-Actor": "owner"})
	var again model.Token
	decodeJSON(t, rec, &again)
	if rec.Code != 200 || again.RevokedAt == nil || *again.RevokedAt != *got.RevokedAt || again.RevokedBy == nil || *again.RevokedBy != "boss" {
		t.Errorf("second revoke = %d %+v, want the first revocation back", rec.Code, again)
	}

	for _, id := range []string{"9999", "0", "-1", "abc", "1.5", "99999999999999999999"} {
		wantErr(t, e.do(t, "DELETE", "/v1/tokens/"+id, "", map[string]string{"X-Actor": "owner"}), 404, "not_found", "")
	}
	wantErr(t, e.do(t, "DELETE", path+"?x=1", "", map[string]string{"X-Actor": "owner"}), 400, "bad_request", "")
	wantErr(t, e.do(t, "DELETE", path, "", map[string]string{"Authorization": "-"}), 401, "unauthorized", "")
}

func TestRevokeCurrentToken(t *testing.T) {
	e := newEnvWith(t, loginActors, nil)
	tok := e.login(t, "codex", "desk", "boss")
	other := e.login(t, "claude", "desk", "owner")

	// The shared token has no token of its own to revoke.
	wantErr(t, e.do(t, "DELETE", "/v1/tokens/current", "", map[string]string{"X-Actor": "owner"}), 403, "forbidden", "shared_token")
	wantErr(t, e.do(t, "DELETE", "/v1/tokens/current", "", map[string]string{"Authorization": "-"}), 401, "unauthorized", "")

	rec := e.do(t, "DELETE", "/v1/tokens/current", "", withToken(tok.Secret, "codex"))
	var got model.Token
	decodeJSON(t, rec, &got)
	if rec.Code != 200 || got.ID != tok.ID || got.RevokedAt == nil || got.RevokedBy == nil || *got.RevokedBy != "codex" {
		t.Fatalf("revoke current = %d %+v, want it revoked in the name of its own actor", rec.Code, got)
	}
	if lg := e.lastLog(t); lg["token_id"] != float64(tok.ID) || lg["status"] != float64(200) {
		t.Errorf("log = %v", lg)
	}
	// The token is gone for good, and the other token is untouched.
	wantErr(t, e.do(t, "DELETE", "/v1/tokens/current", "", withToken(tok.Secret, "codex")), 401, "unauthorized", "")
	wantErr(t, e.do(t, "GET", "/v1/actors/me", "", withToken(tok.Secret, "codex")), 401, "unauthorized", "")
	if rec := e.do(t, "GET", "/v1/actors/me", "", withToken(other.Secret, "claude")); rec.Code != 200 {
		t.Errorf("another token: %d", rec.Code)
	}
	// The name of the token is not a way around the actor check.
	wantErr(t, e.do(t, "DELETE", "/v1/tokens/current", "", withToken(other.Secret, "codex")), 403, "forbidden", "actor_mismatch")
	if rec := e.do(t, "GET", "/v1/actors/me", "", withToken(other.Secret, "claude")); rec.Code != 200 {
		t.Errorf("a mismatching revocation revoked the token: %d", rec.Code)
	}
}

func TestTokenRoutes(t *testing.T) {
	e := newEnvWith(t, loginActors, nil)
	tok := e.login(t, "codex", "desk", "owner")
	owner := map[string]string{"X-Actor": "owner"}
	for _, c := range []struct{ method, path, allow string }{
		{"PUT", "/v1/tokens", "GET, POST"},
		{"DELETE", "/v1/tokens", "GET, POST"},
		{"POST", "/v1/tokens/current", "DELETE"},
		{"GET", "/v1/tokens/current", "DELETE"},
		{"GET", fmt.Sprintf("/v1/tokens/%d", tok.ID), "DELETE"},
		{"PATCH", fmt.Sprintf("/v1/tokens/%d", tok.ID), "DELETE"},
		{"POST", fmt.Sprintf("/v1/tokens/%d", tok.ID), "DELETE"},
	} {
		rec := e.do(t, c.method, c.path, "", owner)
		wantErr(t, rec, 405, "method_not_allowed", "")
		if rec.Header().Get("Allow") != c.allow {
			t.Errorf("%s %s: Allow = %q, want %q", c.method, c.path, rec.Header().Get("Allow"), c.allow)
		}
	}
	// current is not an id, and an id is not current.
	wantErr(t, e.do(t, "DELETE", "/v1/tokens/current", "", owner), 403, "forbidden", "shared_token")
	if rec := e.do(t, "DELETE", fmt.Sprintf("/v1/tokens/%d", tok.ID), "", owner); rec.Code != 200 {
		t.Errorf("delete by id: %d", rec.Code)
	}
	// Only the exact POST on the two paths is open; everything near it needs the token.
	for _, c := range []struct{ method, path string }{
		{"POST", "/v1/tokens/"}, {"POST", "/v1/tokens/1"}, {"POST", "/v1/tokens/current"},
		{"GET", "/v1/tokens"}, {"DELETE", "/v1/tokens"},
		{"POST", "/v1/device-authorizations/"}, {"POST", "/v1/device-authorizations/BCDF-GHJK"}, {"GET", "/v1/device-authorizations"},
	} {
		wantErr(t, e.anon(t, c.method, c.path, `{}`), 401, "unauthorized", "")
	}
}

func TestTokenAndDeviceWritesLeaveNoActivity(t *testing.T) {
	e := newEnvWith(t, loginActors, nil)
	tok := e.login(t, "claude", "desk", "owner")
	e.do(t, "DELETE", "/v1/tokens/current", "", withToken(tok.Secret, "claude"))
	e.login(t, "claude", "desk", "owner")
	rec := e.do(t, "GET", "/v1/activity", "", map[string]string{"X-Actor": "owner"})
	var l listEnvelope[model.Activity]
	decodeJSON(t, rec, &l)
	if rec.Code != 200 || l.Total != 0 {
		t.Errorf("activity after logins and revocations: %d %+v", rec.Code, l)
	}
}

func TestLoginEndpointsStayOpenWhenTheWebClientIsServed(t *testing.T) {
	e := newWebEnv(t, testWebFS())
	g := e.startLogin(t, "claude", "desk")
	if rec := e.decide(t, g.UserCode, "approved", "owner"); rec.Code != 200 {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	rec := e.exchange(t, g.DeviceCode)
	var tok model.IssuedToken
	decodeJSON(t, rec, &tok)
	if rec.Code != 201 || tok.Actor != "claude" {
		t.Fatalf("exchange: %d %s", rec.Code, rec.Body.String())
	}
	wantErr(t, e.anon(t, "GET", "/v1/tokens", ""), 401, "unauthorized", "")
	if rec := e.do(t, "GET", "/v1/actors/me", "", withToken(tok.Secret, "claude")); rec.Code != 200 {
		t.Errorf("the issued token: %d", rec.Code)
	}
}
