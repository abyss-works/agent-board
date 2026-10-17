package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

var noon = time.Date(2026, 10, 17, 12, 0, 0, 0, time.FixedZone("KST", 9*3600))

func TestResolveSettingsPrecedence(t *testing.T) {
	file := Config{URL: "http://file:1", Token: "file-token", Actor: "owner", SessionID: "file-sess", ClientHost: "file-host"}
	e := env(map[string]string{"AGB_URL": "http://env:2", "AGB_TOKEN": "env-token", "AGB_ACTOR": "codex", "AGB_SESSION_ID": "env-sess", "AGB_CLIENT_HOST": "env-host"})
	g := &globals{url: "http://flag:3", actor: "claude", sessionID: "flag-sess", clientHost: "flag-host"}

	s, err := resolveSettings(g, e, file, noon, "desk")
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "http://flag:3" || s.Actor != "claude" || s.SessionID != "flag-sess" || s.ClientHost != "flag-host" || s.Token != "env-token" {
		t.Errorf("flags should win: %+v", s)
	}
	s, err = resolveSettings(&globals{}, e, file, noon, "desk")
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "http://env:2" || s.Actor != "codex" || s.SessionID != "env-sess" || s.ClientHost != "env-host" || s.Token != "env-token" {
		t.Errorf("env should beat file: %+v", s)
	}
	s, err = resolveSettings(&globals{}, env(nil), file, noon, "desk")
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "http://file:1" || s.Actor != "owner" || s.SessionID != "file-sess" || s.ClientHost != "file-host" || s.Token != "file-token" {
		t.Errorf("file is the fallback: %+v", s)
	}
}

func TestResolveSettingsDefaults(t *testing.T) {
	file := Config{URL: "http://file:1", Token: "t", Actor: "owner"}
	s, err := resolveSettings(&globals{}, env(nil), file, noon, "DESKTOP_AB C.home.")
	if err != nil {
		t.Fatal(err)
	}
	if s.SessionID != "cli-20261017" {
		t.Errorf("session id = %q, want cli-<local date>", s.SessionID)
	}
	if s.ClientHost != "DESKTOP-AB-C.home" {
		t.Errorf("client host = %q", s.ClientHost)
	}
	if strings.TrimRight(s.URL, "/") != s.URL {
		t.Errorf("url keeps no trailing slash: %q", s.URL)
	}
}

func TestResolveSettingsMissingValues(t *testing.T) {
	cases := []struct {
		name string
		file Config
		want string
	}{
		{"url", Config{Token: "t", Actor: "owner"}, "AGB_URL"},
		{"token", Config{URL: "http://x", Actor: "owner"}, "AGB_TOKEN"},
		{"actor", Config{URL: "http://x", Token: "t"}, "AGB_ACTOR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := resolveSettings(&globals{}, env(nil), tc.file, noon, "desk")
			if err == nil {
				err = s.requireAuth()
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "agb config set") {
				t.Errorf("err = %v, want mention of %s and the config command", err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), "t\"") {
				t.Errorf("error must not echo values: %v", err)
			}
		})
	}
}

func TestResolveSettingsRejectsBadValues(t *testing.T) {
	file := Config{URL: "http://x", Token: "t", Actor: "owner"}
	for name, g := range map[string]*globals{
		"actor":       {actor: "Claude"},
		"session":     {sessionID: "has space"},
		"client host": {clientHost: "-bad-"},
	} {
		if _, err := resolveSettings(g, env(nil), file, noon, "desk"); err == nil {
			t.Errorf("%s: bad value accepted", name)
		}
	}
}

func TestSanitizeHost(t *testing.T) {
	cases := map[string]string{
		"desk":                         "desk",
		"DESKTOP-ABC":                  "DESKTOP-ABC",
		"my host.local":                "my-host.local",
		"_under_score_":                "under-score",
		"...":                          "unknown-host",
		"":                             "unknown-host",
		strings.Repeat("a", 70):        strings.Repeat("a", 63),
		strings.Repeat("a", 63) + ".b": strings.Repeat("a", 63),
	}
	for in, want := range cases {
		if got := sanitizeHost(in); got != want {
			t.Errorf("sanitizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConfigFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "agb", "config.json")
	cfg, err := loadConfig(path)
	if err != nil || cfg != (Config{}) {
		t.Fatalf("missing file must load as empty: %+v %v", cfg, err)
	}
	want := Config{URL: "http://127.0.0.1:8421", Token: "secret-token-value", Actor: "claude"}
	if err := saveConfig(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig(path)
	if err != nil || got != want {
		t.Fatalf("round trip = %+v %v", got, err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"session_id"`) == strings.Contains(string(raw), `"client_host"`) && strings.Contains(string(raw), `"session_id"`) {
		t.Errorf("empty fields should be omitted: %s", raw)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(path)
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %o, want 600", fi.Mode().Perm())
		}
		di, _ := os.Stat(filepath.Dir(path))
		if di.Mode().Perm() != 0o700 {
			t.Errorf("dir mode = %o, want 700", di.Mode().Perm())
		}
	}
}

func TestConfigFileRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte("{not json"), 0o600)
	if _, err := loadConfig(path); err == nil {
		t.Error("garbage accepted")
	}
}

func TestDefaultSessionIDUsesLocalDate(t *testing.T) {
	// 2026-10-17 01:00 KST is 2026-10-16 16:00 UTC; the local date wins.
	early := time.Date(2026, 10, 17, 1, 0, 0, 0, time.FixedZone("KST", 9*3600))
	if got := defaultSessionID(early); got != "cli-20261017" {
		t.Errorf("session id = %q", got)
	}
}
