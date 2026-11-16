package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const testToken = "test-token-0123456789abcdef0123456789" // 38 chars, dummy

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"AGB_TOKEN":  testToken,
		"AGB_ACTORS": "owner:user,claude:agent",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:8420" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.DataDir != "./data" {
		t.Errorf("DataDir = %q", cfg.DataDir)
	}
	if cfg.Token != testToken {
		t.Errorf("Token not kept")
	}
	if len(cfg.Actors) != 2 || cfg.Actors[0].ID != "owner" || cfg.Actors[0].Kind != "user" || cfg.Actors[1].ID != "claude" || cfg.Actors[1].Kind != "agent" {
		t.Errorf("Actors = %+v", cfg.Actors)
	}
	if cfg.WebDir != "" {
		t.Errorf("WebDir = %q, want empty (web client not served)", cfg.WebDir)
	}
	if cfg.Backup.Enabled() {
		t.Error("backup must be disabled without a bucket")
	}
	if cfg.Backup.Interval != time.Hour || cfg.Backup.Retention != 672*time.Hour || cfg.Backup.Prefix != "agb" {
		t.Errorf("Backup defaults = %+v", cfg.Backup)
	}
}

func TestLoadOverrides(t *testing.T) {
	webDir := t.TempDir()
	cfg, err := Load(env(map[string]string{
		"AGB_TOKEN":                    testToken,
		"AGB_ACTORS":                   "owner:user",
		"AGB_ADDR":                     "100.64.0.1:9000",
		"AGB_DATA_DIR":                 "/data",
		"AGB_WEB_DIR":                  webDir,
		"AGB_BACKUP_BUCKET":            "agb-backup",
		"AGB_BACKUP_ENDPOINT":          "https://example.r2.cloudflarestorage.com",
		"AGB_BACKUP_ACCESS_KEY_ID":     "dummy-id",
		"AGB_BACKUP_SECRET_ACCESS_KEY": "dummy-secret",
		"AGB_BACKUP_PREFIX":            "p",
		"AGB_BACKUP_INTERVAL":          "30m",
		"AGB_BACKUP_RETENTION":         "48h",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "100.64.0.1:9000" || cfg.DataDir != "/data" || cfg.WebDir != webDir {
		t.Errorf("overrides = %+v", cfg)
	}
	if !cfg.Backup.Enabled() || cfg.Backup.Interval != 30*time.Minute || cfg.Backup.Retention != 48*time.Hour || cfg.Backup.Prefix != "p" {
		t.Errorf("Backup = %+v", cfg.Backup)
	}
}

func TestLoadRejects(t *testing.T) {
	base := map[string]string{"AGB_TOKEN": testToken, "AGB_ACTORS": "owner:user"}
	cases := []struct {
		name string
		set  map[string]string
		want string
	}{
		{"missing token", map[string]string{"AGB_TOKEN": ""}, "AGB_TOKEN"},
		{"short token", map[string]string{"AGB_TOKEN": "short"}, "32"},
		{"missing actors", map[string]string{"AGB_ACTORS": ""}, "AGB_ACTORS"},
		{"bad actor format", map[string]string{"AGB_ACTORS": "owner"}, "AGB_ACTORS"},
		{"bad actor id", map[string]string{"AGB_ACTORS": "Owner:user"}, "Owner"},
		{"bad actor kind", map[string]string{"AGB_ACTORS": "owner:bot"}, "bot"},
		{"duplicate actor", map[string]string{"AGB_ACTORS": "owner:user,owner:agent"}, "owner"},
		{"bad interval", map[string]string{"AGB_BACKUP_INTERVAL": "soon"}, "AGB_BACKUP_INTERVAL"},
		{"zero interval", map[string]string{"AGB_BACKUP_INTERVAL": "0s"}, "AGB_BACKUP_INTERVAL"},
		{"bad retention", map[string]string{"AGB_BACKUP_RETENTION": "-1h"}, "AGB_BACKUP_RETENTION"},
		{"bucket without endpoint", map[string]string{"AGB_BACKUP_BUCKET": "b"}, "AGB_BACKUP_ENDPOINT"},
	}
	for _, c := range cases {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range c.set {
			m[k] = v
		}
		_, err := Load(env(m))
		if err == nil {
			t.Errorf("%s: expected error", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.want)
		}
		if strings.Contains(err.Error(), testToken) {
			t.Errorf("%s: error leaks the token", c.name)
		}
	}
}

func TestLoadRejectsUnusableWebDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "index.html")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"missing folder": filepath.Join(t.TempDir(), "none"),
		"regular file":   file,
	}
	for name, dir := range cases {
		_, err := Load(env(map[string]string{"AGB_TOKEN": testToken, "AGB_ACTORS": "owner:user", "AGB_WEB_DIR": dir}))
		if err == nil || !strings.Contains(err.Error(), "AGB_WEB_DIR") {
			t.Errorf("%s: err = %v, want an AGB_WEB_DIR error", name, err)
		}
	}
}

func TestLoadEmbedding(t *testing.T) {
	base := map[string]string{"AGB_TOKEN": testToken, "AGB_ACTORS": "owner:user"}
	with := func(kv ...string) func(string) string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return env(m)
	}

	cfg, err := Load(with())
	if err != nil {
		t.Fatalf("without a key: %v", err)
	}
	if cfg.Embedding.Enabled() || cfg.Embedding.Interval != time.Minute {
		t.Errorf("without a key: %+v", cfg.Embedding)
	}

	cfg, err = Load(with("AGB_EMBEDDING_API_KEY", "k", "AGB_EMBEDDING_URL", "https://example.test/v1/embeddings", "AGB_EMBEDDING_MODEL", "m",
		"AGB_EMBEDDING_QUERY_PREFIX", `Instruct: x\nQuery: `, "AGB_EMBEDDING_INTERVAL", "5m"))
	if err != nil {
		t.Fatal(err)
	}
	e := cfg.Embedding
	if e.Provider != nil {
		t.Errorf("Provider = %s, want nil when unset", e.Provider)
	}
	if !e.Enabled() || e.APIKey != "k" || e.URL != "https://example.test/v1/embeddings" || e.Model != "m" || e.QueryPrefix != "Instruct: x\nQuery: " || e.Interval != 5*time.Minute {
		t.Errorf("Embedding = %+v", e)
	}

	for name, tc := range map[string]struct {
		value string
		want  string // the Provider kept, compared after compacting
	}{
		"object":                  {`{"order":["x"]}`, `{"order":["x"]}`},
		"empty object":            {`{}`, `{}`},
		"object with outer space": {`  {"order":["a"]}  `, `{"order":["a"]}`},
	} {
		cfg, err := Load(with("AGB_EMBEDDING_PROVIDER", tc.value))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var got bytes.Buffer
		if err := json.Compact(&got, cfg.Embedding.Provider); err != nil || got.String() != tc.want {
			t.Errorf("%s: Provider = %s (%v), want %s", name, cfg.Embedding.Provider, err, tc.want)
		}
	}

	for name, tc := range map[string]struct {
		kv   []string
		want string
	}{
		"url":                    {[]string{"AGB_EMBEDDING_API_KEY", "secret-key", "AGB_EMBEDDING_MODEL", "m"}, "AGB_EMBEDDING_URL"},
		"model":                  {[]string{"AGB_EMBEDDING_API_KEY", "secret-key", "AGB_EMBEDDING_URL", "https://example.test"}, "AGB_EMBEDDING_MODEL"},
		"interval":               {[]string{"AGB_EMBEDDING_INTERVAL", "soon"}, "AGB_EMBEDDING_INTERVAL"},
		"provider broken":        {[]string{"AGB_EMBEDDING_PROVIDER", `{"order":`}, "AGB_EMBEDDING_PROVIDER"},
		"provider array":         {[]string{"AGB_EMBEDDING_PROVIDER", `["x"]`}, "AGB_EMBEDDING_PROVIDER"},
		"provider string":        {[]string{"AGB_EMBEDDING_PROVIDER", `"x"`}, "AGB_EMBEDDING_PROVIDER"},
		"provider number":        {[]string{"AGB_EMBEDDING_PROVIDER", `1`}, "AGB_EMBEDDING_PROVIDER"},
		"provider null":          {[]string{"AGB_EMBEDDING_PROVIDER", `null`}, "AGB_EMBEDDING_PROVIDER"},
		"provider blank":         {[]string{"AGB_EMBEDDING_PROVIDER", "   "}, "AGB_EMBEDDING_PROVIDER"},
		"provider trailing text": {[]string{"AGB_EMBEDDING_PROVIDER", `{"order":["a"]} trailing`}, "AGB_EMBEDDING_PROVIDER"},
		"provider 10KB broken":   {[]string{"AGB_EMBEDDING_PROVIDER", "{" + strings.Repeat("a", 10<<10)}, "AGB_EMBEDDING_PROVIDER"},
	} {
		_, err := Load(with(tc.kv...))
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret-key") {
			t.Errorf("%s: err = %v, want it to name %s and not the key", name, err, tc.want)
		}
	}
}

func TestLoadTruncatesLongInvalidValues(t *testing.T) {
	base := map[string]string{"AGB_TOKEN": testToken, "AGB_ACTORS": "owner:user"}
	with := func(k, v string) func(string) string {
		m := map[string]string{}
		for bk, bv := range base {
			m[bk] = bv
		}
		m[k] = v
		return env(m)
	}

	// Short values are echoed whole, without a truncation marker.
	short := "not-json-" + strings.Repeat("x", 41) // 50 runes
	_, err := Load(with("AGB_EMBEDDING_PROVIDER", short))
	if err == nil {
		t.Fatal("expected error for short invalid provider")
	}
	if !strings.Contains(err.Error(), short) || strings.Contains(err.Error(), "...(truncated)") {
		t.Errorf("50-rune value: err = %q, want %q verbatim without truncation", err.Error(), short)
	}

	// At 120 runes, still verbatim without truncation.
	exact := "{" + strings.Repeat("a", 119)
	_, err = Load(with("AGB_EMBEDDING_PROVIDER", exact))
	if err == nil {
		t.Fatal("expected error for 120-rune invalid provider")
	}
	if !strings.Contains(err.Error(), exact) || strings.Contains(err.Error(), "...(truncated)") {
		t.Errorf("120-rune value: err = %q, want %q verbatim without truncation", err.Error(), exact)
	}

	// 201-rune ASCII value: first 120 runes + "...(truncated)", under 300 bytes.
	long200 := "{" + strings.Repeat("a", 200)
	_, err = Load(with("AGB_EMBEDDING_PROVIDER", long200))
	if err == nil {
		t.Fatal("expected error for 201-rune invalid provider")
	}
	msg := err.Error()
	wantPrefix := "{" + strings.Repeat("a", 119)
	if !strings.Contains(msg, wantPrefix+"...(truncated)") || strings.Contains(msg, strings.Repeat("a", 120)) || len(msg) >= 300 {
		t.Errorf("201-rune value: len=%d err = %q", len(msg), msg)
	}

	// 10KB broken value and multibyte Hangul across the 120-rune boundary stay valid UTF-8 and bounded.
	for name, kv := range map[string][2]string{
		"provider 10KB":    {"AGB_EMBEDDING_PROVIDER", "{" + strings.Repeat("a", 10<<10)},
		"provider hangul":  {"AGB_EMBEDDING_PROVIDER", "{" + strings.Repeat("가", 200)},
		"backup interval":  {"AGB_BACKUP_INTERVAL", strings.Repeat("나", 200)},
		"backup retention": {"AGB_BACKUP_RETENTION", strings.Repeat("다", 200)},
		"embed interval":   {"AGB_EMBEDDING_INTERVAL", strings.Repeat("라", 200)},
		"web dir":          {"AGB_WEB_DIR", filepath.Join(t.TempDir(), strings.Repeat("마", 200))},
		"actors entry":     {"AGB_ACTORS", strings.Repeat("a", 200)},
		"actors id":        {"AGB_ACTORS", strings.Repeat("A", 200) + ":user"},
		"actors kind":      {"AGB_ACTORS", "owner:" + strings.Repeat("b", 200)},
	} {
		_, err := Load(with(kv[0], kv[1]))
		if err == nil {
			t.Errorf("%s: expected error", name)
			continue
		}
		got := err.Error()
		if !utf8.ValidString(got) {
			t.Errorf("%s: error is not valid UTF-8: %q", name, got)
		}
		if !strings.Contains(got, "...(truncated)") {
			t.Errorf("%s: error missing ...(truncated): %q", name, got)
		}
		if len(got) > 600 {
			t.Errorf("%s: error too long (%d bytes): %q", name, len(got), got[:200])
		}
	}
}
