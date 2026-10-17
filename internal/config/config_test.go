package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
