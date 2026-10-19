//go:build e2e

package e2e

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	userCodeRE   = regexp.MustCompile(`^[BCDFGHJKLMNPQRSTVWXZ]{4}-[BCDFGHJKLMNPQRSTVWXZ]{4}$`)
	deviceCodeRE = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	secretRE     = regexp.MustCompile(`^agb_[A-Za-z0-9_-]{43}$`)
)

// loginHost builds a client host that is unique to the run and the test, so a
// login in one test never revokes the token of another.
func loginHost(suffix string) string { return "e2e-" + strings.ToLower(runID) + "-" + suffix }

// withSecret makes a call carry the given token instead of the one the
// harness would pick for its actor.
func withSecret(secret string) http.Header {
	return http.Header{"Authorization": {"Bearer " + secret}}
}

func anonymousBytes(t testing.TB, method, path string, raw []byte) response {
	t.Helper()
	r, err := anonymousRaw(method, path, raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func startLogin(t testing.TB, actorID, host string) deviceGrant {
	t.Helper()
	r := anonymous(t, "POST", "/v1/device-authorizations", map[string]any{"actor": actorID, "client_host": host})
	return decodeAs[deviceGrant](t, r, 201)
}

func exchange(t testing.TB, deviceCode string) response {
	t.Helper()
	return anonymous(t, "POST", "/v1/tokens", map[string]any{"device_code": deviceCode})
}

// decide answers a device authorization as owner.
func decide(t testing.TB, userCode, status string) response {
	t.Helper()
	return owner.do(t, "PATCH", "/v1/device-authorizations/"+userCode, map[string]any{"status": status})
}

func login(t testing.TB, actorID, host string) tokenInfo {
	t.Helper()
	tok, err := deviceLogin(actorID, host)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func listTokens(t testing.TB, query string) []tokenInfo {
	t.Helper()
	return decodeAs[tokenList](t, owner.do(t, "GET", "/v1/tokens"+query, nil), 200).Items
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func ptr[T any](v T) *T { return &v }

func findToken(items []tokenInfo, id int64) *tokenInfo {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

func TestDeviceLogin(t *testing.T) {
	host := loginHost("flow")
	r := anonymous(t, "POST", "/v1/device-authorizations", map[string]any{"actor": "codex", "client_host": host})
	g := decodeAs[deviceGrant](t, r, 201)

	t.Run("the request", func(t *testing.T) {
		if !deviceCodeRE.MatchString(g.DeviceCode) || !userCodeRE.MatchString(g.UserCode) || g.ExpiresIn != 600 || g.Interval != 5 {
			t.Errorf("grant = %+v, want a 43 character device code, a user code like BDFG-HJKL, 600 seconds and an interval of 5", g)
		}
		if loc := r.header.Get("Location"); loc != "/v1/device-authorizations/"+g.UserCode {
			t.Errorf("Location = %q", loc)
		}
	})
	t.Run("nothing is issued while the user waits", func(t *testing.T) {
		for range 2 {
			wantError(t, exchange(t, g.DeviceCode), 400, "authorization_pending")
		}
		for _, it := range listTokens(t, "?status=all") {
			if it.ClientHost == host {
				t.Errorf("a token exists before the approval: %+v", it)
			}
		}
	})
	t.Run("the user sees the request", func(t *testing.T) {
		d := decodeAs[deviceAuth](t, owner.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, nil), 200)
		if d.UserCode != g.UserCode || d.Actor != "codex" || d.ClientHost != host || d.Status != "pending" {
			t.Errorf("device authorization = %+v", d)
		}
		wantTime(t, "created_at", d.CreatedAt)
		wantTime(t, "expires_at", d.ExpiresAt)
		if d.ExpiresAt <= d.CreatedAt {
			t.Errorf("expires_at %s is not after created_at %s", d.ExpiresAt, d.CreatedAt)
		}
		// The code may be typed in any case and without the hyphen.
		for _, code := range []string{strings.ToLower(g.UserCode), strings.ReplaceAll(g.UserCode, "-", ""), strings.ToLower(strings.ReplaceAll(g.UserCode, "-", ""))} {
			if got := decodeAs[deviceAuth](t, owner.do(t, "GET", "/v1/device-authorizations/"+code, nil), 200); got.UserCode != g.UserCode {
				t.Errorf("%s: user_code = %q", code, got.UserCode)
			}
		}
		if strings.Contains(string(owner.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, nil).body), g.DeviceCode) {
			t.Error("the user is shown the device code")
		}
	})
	t.Run("the user approves", func(t *testing.T) {
		d := decodeAs[deviceAuth](t, decide(t, g.UserCode, "approved"), 200)
		if d.Status != "approved" || d.UserCode != g.UserCode {
			t.Errorf("approved = %+v", d)
		}
		if got := decodeAs[deviceAuth](t, owner.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, nil), 200); got.Status != "approved" {
			t.Errorf("status after approving = %q", got.Status)
		}
		wantError(t, decide(t, g.UserCode, "denied"), 409, "conflict")
		wantError(t, decide(t, g.UserCode, "approved"), 409, "conflict")
	})
	var tok tokenInfo
	t.Run("the agent receives its token", func(t *testing.T) {
		r := exchange(t, g.DeviceCode)
		tok = decodeAs[tokenInfo](t, r, 201)
		if tok.ID < 1 || tok.Actor != "codex" || tok.ClientHost != host || tok.CreatedBy != "owner" || !secretRE.MatchString(tok.Token) {
			t.Errorf("token = %+v", tok)
		}
		if tok.LastUsedAt != nil || tok.RevokedAt != nil || tok.RevokedBy != nil {
			t.Errorf("a new token is unused and not revoked: %+v", tok)
		}
		wantTime(t, "created_at", tok.CreatedAt)
		if loc := r.header.Get("Location"); loc != "" {
			t.Errorf("a token has no page of its own, so no Location: %q", loc)
		}
	})
	t.Run("the token works as its actor", func(t *testing.T) {
		codex := as("codex")
		a := decodeAs[actor](t, codex.do(t, "GET", "/v1/actors/me", nil, withSecret(tok.Token)), 200)
		if a.ID != "codex" || a.Kind != "agent" {
			t.Errorf("me = %+v", a)
		}
		p := decodeAs[project](t, codex.do(t, "POST", "/v1/projects", map[string]any{"key": projectKey("TK"), "name": "토큰으로 만든 프로젝트"}, withSecret(tok.Token)), 201)
		if p.CreatedBy != "codex" {
			t.Errorf("created_by = %q, want the actor of the token", p.CreatedBy)
		}
	})
	t.Run("the list shows the use and never the secret", func(t *testing.T) {
		r := owner.do(t, "GET", "/v1/tokens", nil)
		listed := findToken(decodeAs[tokenList](t, r, 200).Items, tok.ID)
		if listed == nil || listed.LastUsedAt == nil || listed.RevokedAt != nil || listed.Actor != "codex" {
			t.Fatalf("listed token = %+v", listed)
		}
		wantTime(t, "last_used_at", *listed.LastUsedAt)
		if strings.Contains(string(r.body), tok.Token) || strings.Contains(string(r.body), `"token"`) {
			t.Errorf("the list shows a secret: %s", r.body)
		}
	})
	t.Run("the exchange happens once", func(t *testing.T) {
		wantError(t, exchange(t, g.DeviceCode), 400, "expired_token")
		wantError(t, owner.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, nil), 404, "not_found")
		count := 0
		for _, it := range listTokens(t, "?status=all") {
			if it.Actor == "codex" && it.ClientHost == host {
				count++
			}
		}
		if count != 1 {
			t.Errorf("tokens of codex on %s = %d, want 1", host, count)
		}
	})
}

func TestDeviceLoginDenied(t *testing.T) {
	host := loginHost("denied")
	g := startLogin(t, "claude", host)
	d := decodeAs[deviceAuth](t, decide(t, g.UserCode, "denied"), 200)
	if d.Status != "denied" {
		t.Fatalf("denied = %+v", d)
	}
	wantError(t, decide(t, g.UserCode, "approved"), 409, "conflict")
	// The denial is reported once; then the request is gone.
	wantError(t, exchange(t, g.DeviceCode), 400, "access_denied")
	wantError(t, exchange(t, g.DeviceCode), 400, "expired_token")
	wantError(t, owner.do(t, "GET", "/v1/device-authorizations/"+g.UserCode, nil), 404, "not_found")
	wantError(t, decide(t, g.UserCode, "approved"), 404, "not_found")
	for _, it := range listTokens(t, "?status=all") {
		if it.ClientHost == host {
			t.Errorf("a denied request produced a token: %+v", it)
		}
	}
}

func TestDecisionValues(t *testing.T) {
	g := startLogin(t, "gemini", loginHost("values"))
	path := "/v1/device-authorizations/" + g.UserCode
	for _, status := range []string{"pending", "maybe", "", "Approved"} {
		e := wantError(t, owner.do(t, "PATCH", path, map[string]any{"status": status}), 422, "validation_failed")
		if !e.hasField("status") {
			t.Errorf("%q: details = %+v", status, e.Error.Details)
		}
	}
	wantError(t, owner.do(t, "PATCH", path, map[string]any{}), 422, "validation_failed")
	wantError(t, owner.do(t, "PATCH", path, map[string]any{"status": nil}), 422, "validation_failed")
	wantError(t, owner.do(t, "PATCH", path, map[string]any{"status": 1}), 400, "bad_request")
	wantError(t, owner.do(t, "PATCH", path, map[string]any{"status": "approved", "actor": "owner"}), 400, "bad_request")
	if d := decodeAs[deviceAuth](t, owner.do(t, "GET", path, nil), 200); d.Status != "pending" {
		t.Errorf("a rejected decision changed the request: %+v", d)
	}
	for _, code := range []string{"BCDF-GHJK", "nope", "AEIO-AEIO"} {
		wantError(t, owner.do(t, "GET", "/v1/device-authorizations/"+code, nil), 404, "not_found")
		wantError(t, decide(t, code, "approved"), 404, "not_found")
	}
	decodeAs[deviceAuth](t, decide(t, g.UserCode, "denied"), 200)
	wantError(t, exchange(t, g.DeviceCode), 400, "access_denied")
}

// An expiry after 600 seconds cannot be waited for here; the unit tests move
// the clock. What the exchange does for a code the server does not know is the
// same answer.
func TestExchangeOfAnUnknownDeviceCode(t *testing.T) {
	wantError(t, exchange(t, strings.Repeat("A", 43)), 400, "expired_token")
	wantError(t, exchange(t, "short"), 400, "expired_token")
	g := startLogin(t, "gemini", loginHost("unknown"))
	wantError(t, exchange(t, g.UserCode), 400, "expired_token") // the user code is not the device code
	decodeAs[deviceAuth](t, decide(t, g.UserCode, "denied"), 200)
	wantError(t, exchange(t, g.DeviceCode), 400, "access_denied")
}

func TestActorTokenBinding(t *testing.T) {
	claudeToken := actorTokens["claude"]
	if claudeToken == "" {
		t.Fatal("the harness holds no token for claude")
	}
	t.Run("an agent works with its own token", func(t *testing.T) {
		a := decodeAs[actor](t, as("claude").do(t, "GET", "/v1/actors/me", nil), 200)
		if a.ID != "claude" {
			t.Errorf("me = %+v", a)
		}
	})
	t.Run("a token does not speak for another actor", func(t *testing.T) {
		for _, who := range []string{"codex", "gemini", "owner"} {
			e := wantError(t, as(who).do(t, "GET", "/v1/actors/me", nil, withSecret(claudeToken)), 403, "forbidden")
			if !e.hasReason("actor_mismatch") {
				t.Errorf("%s: details = %+v", who, e.Error.Details)
			}
		}
	})
	t.Run("the shared token is for users only", func(t *testing.T) {
		for _, who := range []string{"claude", "codex", "hermes"} {
			e := wantError(t, as(who).do(t, "GET", "/v1/actors/me", nil, withSecret(token)), 403, "forbidden")
			if !e.hasReason("actor_mismatch") {
				t.Errorf("%s: details = %+v", who, e.Error.Details)
			}
		}
		expect(t, owner.do(t, "GET", "/v1/actors/me", nil), 200)
	})
	t.Run("the order of the checks", func(t *testing.T) {
		// The actor check comes after the trace headers and before the path.
		wantError(t, as("codex").do(t, "GET", "/v1/no-such-path", nil, withSecret(claudeToken)), 403, "forbidden")
		h := as("codex").headers(false)
		h.Set("Authorization", "Bearer "+claudeToken)
		h.Del("X-Session-Id")
		wantError(t, send(t, "GET", "/v1/actors/me", nil, h), 400, "bad_request")
		wantError(t, as("nobody").do(t, "GET", "/v1/actors/me", nil, withSecret(claudeToken)), 400, "bad_request")
		wantError(t, as("claude").do(t, "GET", "/v1/no-such-path", nil), 404, "not_found")
	})
	t.Run("an unknown token is unauthorized", func(t *testing.T) {
		for _, secret := range []string{"agb_" + strings.Repeat("A", 43), "agb_", claudeToken + "x", strings.ToUpper(claudeToken)} {
			wantError(t, as("claude").do(t, "GET", "/v1/actors/me", nil, withSecret(secret)), 401, "unauthorized")
		}
	})
}

func TestUserOnlyRoutes(t *testing.T) {
	agent := as("claude")
	g := startLogin(t, "gemini", loginHost("useronly"))
	path := "/v1/device-authorizations/" + g.UserCode
	mustBeUserOnly := func(name string, r response) {
		t.Helper()
		if e := wantError(t, r, 403, "forbidden"); !e.hasReason("user_only") {
			t.Errorf("%s: details = %+v", name, e.Error.Details)
		}
	}
	mustBeUserOnly("list tokens", agent.do(t, "GET", "/v1/tokens", nil))
	mustBeUserOnly("revoke a token by id", agent.do(t, "DELETE", "/v1/tokens/1", nil))
	mustBeUserOnly("read a device authorization", agent.do(t, "GET", path, nil))
	mustBeUserOnly("approve a device authorization", agent.do(t, "PATCH", path, map[string]any{"status": "approved"}))
	mustBeUserOnly("deny a device authorization", agent.do(t, "PATCH", path, map[string]any{"status": "denied"}))
	// The refused approval changed nothing.
	if d := decodeAs[deviceAuth](t, owner.do(t, "GET", path, nil), 200); d.Status != "pending" {
		t.Errorf("an agent decided its own request: %+v", d)
	}

	decodeAs[deviceAuth](t, decide(t, g.UserCode, "denied"), 200)
	wantError(t, exchange(t, g.DeviceCode), 400, "access_denied")
}

func TestRevocation(t *testing.T) {
	host := loginHost("revoke")
	a := login(t, "codex", host)
	b := login(t, "claude", host)
	use := func(who, secret string) response {
		t.Helper()
		return as(who).do(t, "GET", "/v1/actors/me", nil, withSecret(secret))
	}
	expect(t, use("codex", a.Token), 200)
	expect(t, use("claude", b.Token), 200)

	var revoked tokenInfo
	t.Run("by id", func(t *testing.T) {
		revoked = decodeAs[tokenInfo](t, owner.do(t, "DELETE", "/v1/tokens/"+itoa(a.ID), nil), 200)
		if revoked.ID != a.ID || revoked.Actor != "codex" || revoked.RevokedAt == nil || revoked.RevokedBy == nil || *revoked.RevokedBy != "owner" || revoked.Token != "" {
			t.Fatalf("revoked = %+v", revoked)
		}
		wantTime(t, "revoked_at", *revoked.RevokedAt)
		e := wantError(t, use("codex", a.Token), 401, "unauthorized")
		if len(e.Error.Details) != 0 {
			t.Errorf("details = %+v", e.Error.Details)
		}
		expect(t, use("claude", b.Token), 200) // another token is not touched
	})
	t.Run("again, which changes nothing", func(t *testing.T) {
		again := decodeAs[tokenInfo](t, owner.do(t, "DELETE", "/v1/tokens/"+itoa(a.ID), nil), 200)
		if again.RevokedAt == nil || *again.RevokedAt != *revoked.RevokedAt || again.RevokedBy == nil || *again.RevokedBy != "owner" {
			t.Errorf("again = %+v, first = %+v", again, revoked)
		}
	})
	t.Run("a token that does not exist", func(t *testing.T) {
		for _, id := range []string{"2000000000", "0", "-1", "abc"} {
			wantError(t, owner.do(t, "DELETE", "/v1/tokens/"+id, nil), 404, "not_found")
		}
	})
	t.Run("the shared token has no token of its own", func(t *testing.T) {
		e := wantError(t, owner.do(t, "DELETE", "/v1/tokens/current", nil), 403, "forbidden")
		if !e.hasReason("shared_token") {
			t.Errorf("details = %+v", e.Error.Details)
		}
	})
	var self tokenInfo
	t.Run("a token revokes itself", func(t *testing.T) {
		self = decodeAs[tokenInfo](t, as("claude").do(t, "DELETE", "/v1/tokens/current", nil, withSecret(b.Token)), 200)
		if self.ID != b.ID || self.RevokedAt == nil || self.RevokedBy == nil || *self.RevokedBy != "claude" {
			t.Errorf("self revoked = %+v", self)
		}
		wantError(t, as("claude").do(t, "DELETE", "/v1/tokens/current", nil, withSecret(b.Token)), 401, "unauthorized")
		wantError(t, use("claude", b.Token), 401, "unauthorized")
	})
	t.Run("the harness token is untouched", func(t *testing.T) {
		expect(t, as("claude").do(t, "GET", "/v1/actors/me", nil), 200)
		expect(t, as("codex").do(t, "GET", "/v1/actors/me", nil), 200)
	})
	t.Run("lists", func(t *testing.T) {
		active := listTokens(t, "")
		if findToken(active, a.ID) != nil || findToken(active, b.ID) != nil {
			t.Errorf("revoked tokens are listed as active")
		}
		for q, revokedWanted := range map[string]*bool{"?status=active": ptr(false), "?status=revoked": ptr(true), "?status=all": nil} {
			items := listTokens(t, q)
			for _, it := range items {
				if revokedWanted != nil && (it.RevokedAt != nil) != *revokedWanted {
					t.Errorf("%s lists %+v", q, it)
				}
			}
			for i := 1; i < len(items); i++ {
				if items[i-1].ID <= items[i].ID {
					t.Errorf("%s is not newest first: %d then %d", q, items[i-1].ID, items[i].ID)
				}
			}
		}
		revokedList := listTokens(t, "?status=revoked")
		if findToken(revokedList, a.ID) == nil || findToken(revokedList, b.ID) == nil {
			t.Errorf("revoked list lacks the revoked tokens: %+v", revokedList)
		}
		all := listTokens(t, "?status=all")
		if findToken(all, a.ID) == nil || findToken(all, b.ID) == nil {
			t.Errorf("the full list lacks the revoked tokens")
		}
		for _, q := range []string{"?status=bogus", "?status=active,revoked", "?limit=1", "?x=1"} {
			wantError(t, owner.do(t, "GET", "/v1/tokens"+q, nil), 400, "bad_request")
		}
	})
}

func TestLoginOnTheSameDeviceRevokesTheOlderToken(t *testing.T) {
	host, other := loginHost("relogin"), loginHost("elsewhere")
	first := login(t, "codex", host)
	elsewhere := login(t, "codex", other)
	otherActor := login(t, "claude", host)
	use := func(who, secret string) response {
		t.Helper()
		return as(who).do(t, "GET", "/v1/actors/me", nil, withSecret(secret))
	}
	expect(t, use("codex", first.Token), 200)

	second := login(t, "codex", host)
	wantError(t, use("codex", first.Token), 401, "unauthorized")
	expect(t, use("codex", second.Token), 200)
	expect(t, use("codex", elsewhere.Token), 200) // another device
	expect(t, use("claude", otherActor.Token), 200)

	revoked := findToken(listTokens(t, "?status=revoked"), first.ID)
	if revoked == nil || revoked.RevokedAt == nil || revoked.RevokedBy == nil || *revoked.RevokedBy != "owner" {
		t.Errorf("the older token = %+v, want it revoked in the name of the approver", revoked)
	}
	if active := findToken(listTokens(t, ""), second.ID); active == nil || active.RevokedAt != nil {
		t.Errorf("the new token = %+v", active)
	}
}

func TestPendingRequestsAreLimited(t *testing.T) {
	host := loginHost("limit")
	var codes []deviceGrant
	limited := false
	for i := 0; i < 40 && !limited; i++ {
		r := anonymous(t, "POST", "/v1/device-authorizations", map[string]any{"actor": "gemini", "client_host": host})
		if r.status == 201 {
			codes = append(codes, decode[deviceGrant](t, r))
			continue
		}
		wantError(t, r, 429, "too_many_requests")
		limited = true
	}
	// Leave the server as it was: a denied request no longer waits.
	for _, g := range codes {
		expect(t, decide(t, g.UserCode, "denied"), 200)
		wantError(t, exchange(t, g.DeviceCode), 400, "access_denied")
	}
	if !limited {
		t.Fatalf("no 429 after %d waiting requests, want one by the 21st", len(codes))
	}
	if len(codes) > 20 {
		t.Errorf("%d requests were accepted, the limit is 20 waiting at once", len(codes))
	}
	// A place is free again.
	g := startLogin(t, "gemini", host)
	expect(t, decide(t, g.UserCode, "denied"), 200)
	wantError(t, exchange(t, g.DeviceCode), 400, "access_denied")
}

func TestLoginRequestRules(t *testing.T) {
	t.Run("device authorization", func(t *testing.T) {
		post := func(body any) response { return anonymous(t, "POST", "/v1/device-authorizations", body) }
		host := loginHost("rules")
		e := wantError(t, post(map[string]any{"actor": "claude", "client_host": host, "scope": "all"}), 400, "bad_request")
		if !e.hasField("scope") || !e.hasReason("unknown_field") {
			t.Errorf("unknown field: %+v", e.Error.Details)
		}
		wantError(t, anonymousBytes(t, "POST", "/v1/device-authorizations", []byte(`{"actor":"claude","client_host":`)), 400, "bad_request")
		wantError(t, anonymousBytes(t, "POST", "/v1/device-authorizations", []byte(`["claude"]`)), 400, "bad_request")
		wantError(t, post(map[string]any{"actor": "claude", "client_host": 7}), 400, "bad_request")
		wantError(t, anonymousBytes(t, "POST", "/v1/device-authorizations", []byte(`{"actor":"claude","client_host":"`+strings.Repeat("h", 1<<20)+`"}`)), 413, "payload_too_large")

		for name, c := range map[string]struct {
			body   map[string]any
			fields []string
		}{
			"a user":         {map[string]any{"actor": "owner", "client_host": host}, []string{"actor"}},
			"not declared":   {map[string]any{"actor": "nobody", "client_host": host}, []string{"actor"}},
			"a bad host":     {map[string]any{"actor": "claude", "client_host": "my host"}, []string{"client_host"}},
			"both are wrong": {map[string]any{"actor": "owner", "client_host": ""}, []string{"actor", "client_host"}},
			"nothing":        {map[string]any{}, []string{"actor", "client_host"}},
		} {
			e := wantError(t, post(c.body), 422, "validation_failed")
			if got := e.fields(); strings.Join(got, ",") != strings.Join(c.fields, ",") {
				t.Errorf("%s: fields = %v, want %v", name, got, c.fields)
			}
		}

		// Credentials and trace headers play no part, whatever they are.
		h := http.Header{"Content-Type": {jsonType}, "Authorization": {"Bearer wrong"}, "X-Actor": {"NOPE"}, "X-Session-Id": {"a b"}}
		body := `{"actor":"gemini","client_host":"` + host + `"}`
		r := send(t, "POST", "/v1/device-authorizations", []byte(body), h)
		g := decodeAs[deviceGrant](t, r, 201)
		decodeAs[deviceAuth](t, decide(t, g.UserCode, "denied"), 200)
		wantError(t, exchange(t, g.DeviceCode), 400, "access_denied")
	})
	t.Run("token exchange", func(t *testing.T) {
		post := func(body any) response { return anonymous(t, "POST", "/v1/tokens", body) }
		e := wantError(t, post(map[string]any{"device_code": "x", "actor": "claude"}), 400, "bad_request")
		if !e.hasField("actor") || !e.hasReason("unknown_field") {
			t.Errorf("unknown field: %+v", e.Error.Details)
		}
		wantError(t, anonymousBytes(t, "POST", "/v1/tokens", []byte(`{"device_code":`)), 400, "bad_request")
		wantError(t, anonymousBytes(t, "POST", "/v1/tokens", []byte(`"abc"`)), 400, "bad_request")
		wantError(t, post(map[string]any{"device_code": 5}), 400, "bad_request")
		wantError(t, anonymousBytes(t, "POST", "/v1/tokens", []byte(`{"device_code":"`+strings.Repeat("a", 1<<20)+`"}`)), 413, "payload_too_large")
		for _, body := range []map[string]any{{}, {"device_code": nil}, {"device_code": ""}} {
			if e := wantError(t, post(body), 422, "validation_failed"); !e.hasField("device_code") {
				t.Errorf("%v: %+v", body, e.Error.Details)
			}
		}
		// A wrong token or wrong headers do not get in the way of the exchange.
		h := http.Header{"Content-Type": {jsonType}, "Authorization": {"Bearer wrong"}, "X-Actor": {"NOPE"}}
		wantError(t, send(t, "POST", "/v1/tokens", []byte(`{"device_code":"unknown"}`), h), 400, "expired_token")
	})
	t.Run("the paths next to them are not open", func(t *testing.T) {
		for _, c := range []struct{ method, path string }{
			{"GET", "/v1/device-authorizations"},
			{"GET", "/v1/device-authorizations/BCDF-GHJK"},
			{"PATCH", "/v1/device-authorizations/BCDF-GHJK"},
			{"GET", "/v1/tokens"},
			{"DELETE", "/v1/tokens/1"},
			{"DELETE", "/v1/tokens/current"},
			{"POST", "/v1/tokens/current"},
		} {
			wantError(t, anonymous(t, c.method, c.path, nil), 401, "unauthorized")
		}
		r := owner.do(t, "GET", "/v1/device-authorizations", nil)
		wantError(t, r, 405, "method_not_allowed")
		if r.header.Get("Allow") != "POST" {
			t.Errorf("Allow = %q", r.header.Get("Allow"))
		}
		r = owner.do(t, "PUT", "/v1/tokens", nil)
		wantError(t, r, 405, "method_not_allowed")
		if allow := r.header.Get("Allow"); !strings.Contains(allow, "GET") || !strings.Contains(allow, "POST") {
			t.Errorf("Allow = %q", allow)
		}
		r = owner.do(t, "GET", "/v1/tokens/current", nil)
		wantError(t, r, 405, "method_not_allowed")
		if r.header.Get("Allow") != "DELETE" {
			t.Errorf("Allow = %q", r.header.Get("Allow"))
		}
	})
}

func TestLoginLeavesNoActivity(t *testing.T) {
	before := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/activity?limit=1", nil), 200).Total
	tok := login(t, "hermes", loginHost("activity"))
	expect(t, as("hermes").do(t, "DELETE", "/v1/tokens/current", nil, withSecret(tok.Token)), 200)
	after := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/activity?limit=1", nil), 200).Total
	if after != before {
		t.Errorf("activity records went from %d to %d, a login and a revocation write none", before, after)
	}
}
