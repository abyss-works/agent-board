package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDeviceStatuses(t *testing.T) {
	want := []DeviceStatus{DevicePending, DeviceApproved, DeviceDenied}
	if !reflect.DeepEqual(DeviceStatuses, want) {
		t.Fatalf("DeviceStatuses = %v, want %v", DeviceStatuses, want)
	}
	for _, v := range []string{"pending", "approved", "denied"} {
		if got, ok := ParseDeviceStatus(v); !ok || string(got) != v {
			t.Errorf("ParseDeviceStatus(%q) = %q, %v", v, got, ok)
		}
	}
	for _, v := range []string{"", "Approved", "approve", "expired", "all"} {
		if _, ok := ParseDeviceStatus(v); ok {
			t.Errorf("ParseDeviceStatus(%q) must fail", v)
		}
	}
}

func TestParseTokenFilter(t *testing.T) {
	for _, v := range []string{"active", "revoked", "all"} {
		if got, ok := ParseTokenFilter(v); !ok || string(got) != v {
			t.Errorf("ParseTokenFilter(%q) = %q, %v", v, got, ok)
		}
	}
	// The filter is one value; a comma separated list is not part of the contract.
	for _, v := range []string{"", "Active", "active,revoked", "open", "deleted"} {
		if _, ok := ParseTokenFilter(v); ok {
			t.Errorf("ParseTokenFilter(%q) must fail", v)
		}
	}
}

func TestNormalizeUserCode(t *testing.T) {
	// Case and hyphens do not matter; the canonical form is XXXX-XXXX.
	for in, want := range map[string]string{
		"BDFG-HJKL":  "BDFG-HJKL",
		"bdfg-hjkl":  "BDFG-HJKL",
		"BDFGHJKL":   "BDFG-HJKL",
		"bdfghjkl":   "BDFG-HJKL",
		"BdFg-HjKl":  "BDFG-HJKL",
		"BD-FGH-JKL": "BDFG-HJKL",
		"BCDF-GHJK":  "BCDF-GHJK",
		"ZXWV-TSRQ":  "ZXWV-TSRQ",
	} {
		if got, ok := NormalizeUserCode(in); !ok || got != want {
			t.Errorf("NormalizeUserCode(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	// Vowels and digits are not in the alphabet, nor are spaces, other lengths,
	// and letters that only fold to an alphabet letter outside ASCII.
	for _, in := range []string{
		"", "-", "BDFG-HJK", "BDFG-HJKLM", "BDFG-HJKA", "ADFG-HJKL", "BDFG-HJK1", "BDFG HJKL", " BDFG-HJKL", "BDFG-HJKL ",
		"BDFG--HJK", "BDFG-HJKſ", "BDFG-HJK_", "BDFG/HJKL", "한글한글한글한글",
	} {
		if got, ok := NormalizeUserCode(in); ok {
			t.Errorf("NormalizeUserCode(%q) = %q, want a failure", in, got)
		}
	}
}

func TestUserCodeAlphabetHasNoVowelsOrLookalikes(t *testing.T) {
	if len(UserCodeAlphabet) != 20 {
		t.Fatalf("alphabet = %q", UserCodeAlphabet)
	}
	if UserCodeAlphabet != "BCDFGHJKLMNPQRSTVWXZ" {
		t.Errorf("alphabet = %q", UserCodeAlphabet)
	}
	if strings.ContainsAny(UserCodeAlphabet, "AEIOUY01") {
		t.Errorf("alphabet %q holds a vowel or a digit lookalike", UserCodeAlphabet)
	}
}

func TestValidateDeviceRequest(t *testing.T) {
	actors := map[string]Actor{
		"owner":  {ID: "owner", Kind: ActorUser},
		"claude": {ID: "claude", Kind: ActorAgent},
		"codex":  {ID: "codex", Kind: ActorAgent},
	}
	cases := []struct {
		name       string
		actor      string
		clientHost string
		want       []string // field of each violation, in order
	}{
		{"agent and host", "codex", "DESKTOP-RPLTFHM", nil},
		{"host with dots", "claude", "desk-win10.local", nil},
		{"host of 64 characters", "claude", strings.Repeat("h", 62) + ".a", nil},
		{"user actor", "owner", "desk", []string{"actor"}},
		{"undeclared actor", "gemini", "desk", []string{"actor"}},
		{"empty actor", "", "desk", []string{"actor"}},
		{"empty host", "claude", "", []string{"client_host"}},
		{"host with a space", "claude", "my host", []string{"client_host"}},
		{"host with a leading hyphen", "claude", "-desk", []string{"client_host"}},
		{"host of 65 characters", "claude", strings.Repeat("h", 65), []string{"client_host"}},
		{"every violation at once", "owner", "", []string{"actor", "client_host"}},
	}
	for _, c := range cases {
		err := ValidateDeviceRequest(c.actor, c.clientHost, actors)
		var got []string
		if err != nil {
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Errorf("%s: err = %v, want a *ValidationError", c.name, err)
				continue
			}
			for _, f := range ve.Fields {
				got = append(got, f.Field)
			}
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: violations = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTokenJSON(t *testing.T) {
	created, err := ParseTime("2026-10-19T01:21:10.000Z")
	if err != nil {
		t.Fatal(err)
	}
	fresh := Token{ID: 7, Actor: "codex", ClientHost: "DESKTOP-RPLTFHM", CreatedAt: created, CreatedBy: "owner"}
	b, err := json.Marshal(fresh)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":7,"actor":"codex","client_host":"DESKTOP-RPLTFHM","created_at":"2026-10-19T01:21:10.000Z","created_by":"owner",` +
		`"last_used_at":null,"revoked_at":null,"revoked_by":null}`
	if string(b) != want {
		t.Errorf("token JSON =\n%s\nwant\n%s", b, want)
	}

	used, revoked, by := created, created, "owner"
	full := Token{ID: 8, Actor: "claude", ClientHost: "desk", CreatedAt: created, CreatedBy: "owner", LastUsedAt: &used, RevokedAt: &revoked, RevokedBy: &by}
	b, _ = json.Marshal(full)
	want = `{"id":8,"actor":"claude","client_host":"desk","created_at":"2026-10-19T01:21:10.000Z","created_by":"owner",` +
		`"last_used_at":"2026-10-19T01:21:10.000Z","revoked_at":"2026-10-19T01:21:10.000Z","revoked_by":"owner"}`
	if string(b) != want {
		t.Errorf("revoked token JSON =\n%s\nwant\n%s", b, want)
	}

	// The exchange response is the token followed by its secret, which no other view shows.
	b, _ = json.Marshal(IssuedToken{Token: fresh, Secret: "agb_secret"})
	want = `{"id":7,"actor":"codex","client_host":"DESKTOP-RPLTFHM","created_at":"2026-10-19T01:21:10.000Z","created_by":"owner",` +
		`"last_used_at":null,"revoked_at":null,"revoked_by":null,"token":"agb_secret"}`
	if string(b) != want {
		t.Errorf("issued token JSON =\n%s\nwant\n%s", b, want)
	}
	if strings.Contains(string(mustJSON(t, fresh)), `"token"`) {
		t.Error("a token view must not carry the secret")
	}
}

func TestDeviceAuthorizationJSON(t *testing.T) {
	created, _ := ParseTime("2026-10-19T01:20:00.000Z")
	expires, _ := ParseTime("2026-10-19T01:30:00.000Z")
	d := DeviceAuthorization{UserCode: "BDFG-HJKL", Actor: "codex", ClientHost: "DESKTOP-RPLTFHM", Status: DevicePending, CreatedAt: created, ExpiresAt: expires}
	want := `{"user_code":"BDFG-HJKL","actor":"codex","client_host":"DESKTOP-RPLTFHM","status":"pending",` +
		`"created_at":"2026-10-19T01:20:00.000Z","expires_at":"2026-10-19T01:30:00.000Z"}`
	if got := string(mustJSON(t, d)); got != want {
		t.Errorf("device authorization JSON =\n%s\nwant\n%s", got, want)
	}

	g := DeviceGrant{DeviceCode: "dev", UserCode: "BDFG-HJKL", ExpiresIn: DeviceExpiresIn, Interval: DeviceInterval}
	want = `{"device_code":"dev","user_code":"BDFG-HJKL","expires_in":600,"interval":5}`
	if got := string(mustJSON(t, g)); got != want {
		t.Errorf("device grant JSON =\n%s\nwant\n%s", got, want)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
