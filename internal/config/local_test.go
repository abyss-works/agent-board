package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultLocalDataDir(t *testing.T) {
	override := filepath.Join(t.TempDir(), "custom-local")
	got, err := DefaultLocalDataDir(env(map[string]string{"AGB_LOCAL_DATA_DIR": override}))
	if err != nil || got != override {
		t.Fatalf("AGB_LOCAL_DATA_DIR override: got=%q err=%v", got, err)
	}

	switch runtime.GOOS {
	case "windows":
		appData := filepath.Join(t.TempDir(), "AppData", "Local")
		got, err = DefaultLocalDataDir(env(map[string]string{"LOCALAPPDATA": appData}))
		if err != nil || got != filepath.Join(appData, "agb") {
			t.Errorf("windows LOCALAPPDATA: got=%q err=%v", got, err)
		}
		if _, err := DefaultLocalDataDir(env(nil)); err == nil {
			t.Error("windows without LOCALAPPDATA must fail without UserConfigDir fallback")
		}
	case "darwin":
		home := t.TempDir()
		got, err = DefaultLocalDataDir(env(map[string]string{"HOME": home}))
		if err != nil || got != filepath.Join(home, "Library", "Application Support", "agb") {
			t.Errorf("darwin: got=%q err=%v", got, err)
		}
	default:
		xdg := filepath.Join(t.TempDir(), "xdg")
		got, err = DefaultLocalDataDir(env(map[string]string{"XDG_DATA_HOME": xdg}))
		if err != nil || got != filepath.Join(xdg, "agb") {
			t.Errorf("linux XDG_DATA_HOME: got=%q err=%v", got, err)
		}
		home := t.TempDir()
		got, err = DefaultLocalDataDir(env(map[string]string{"HOME": home, "XDG_DATA_HOME": "relative/path"}))
		if err != nil || got != filepath.Join(home, ".local", "share", "agb") {
			t.Errorf("linux relative XDG fallback: got=%q err=%v", got, err)
		}
	}
}

func TestLoadLocalRejectsNonLoopbackAddresses(t *testing.T) {
	badAddrs := []string{
		"0.0.0.0:8420",
		":8420",
		"192.168.1.10:8420",
		"localhost:8420",
		"[::1]:8420",
		"[::]:8420",
		"127.0.0.2:8420",
		"127.0.0.1",
		"not-an-addr",
	}
	for _, addr := range badAddrs {
		dir := filepath.Join(t.TempDir(), "data")
		_, err := LoadLocal(env(nil), LocalOptions{Addr: addr, DataDir: dir})
		if err == nil {
			t.Errorf("LoadLocal accepted non-127.0.0.1 address %q", addr)
		}
	}

	// Also reject AGB_ADDR when it is not 127.0.0.1:<port>
	for _, addr := range []string{"0.0.0.0:8420", ":8420", "localhost:8420"} {
		dir := filepath.Join(t.TempDir(), "data")
		_, err := LoadLocal(env(map[string]string{"AGB_ADDR": addr}), LocalOptions{DataDir: dir})
		if err == nil {
			t.Errorf("LoadLocal accepted AGB_ADDR=%q", addr)
		}
	}
}

func TestLoadLocalSeedsAuthIsolatesEnvAndRejectsCorruptFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "local-data")
	// Remote env vars in the shell must NOT leak into --local server config.
	remoteEnv := env(map[string]string{
		"AGB_TOKEN":    "remote-prod-token-must-not-be-used-0123456789",
		"AGB_ACTORS":   "admin:user",
		"AGB_DATA_DIR": "/should/not/be/used",
	})
	cfg, err := LoadLocal(remoteEnv, LocalOptions{DataDir: dir})
	if err != nil {
		t.Fatalf("LoadLocal: %v", err)
	}
	if cfg.Addr != "127.0.0.1:8420" {
		t.Errorf("Addr = %q, want 127.0.0.1:8420", cfg.Addr)
	}
	if cfg.DataDir != dir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, dir)
	}
	if cfg.Token == "remote-prod-token-must-not-be-used-0123456789" || len(cfg.Token) != 64 {
		t.Errorf("Token len=%d, must be 64-char random hex isolated from AGB_TOKEN", len(cfg.Token))
	}
	if len(cfg.SeedTokens) == 0 || cfg.SeedTokens["claude"] == "" || cfg.SeedTokens["codex"] == "" || cfg.SeedTokens["gemini"] == "" {
		t.Errorf("SeedTokens missing default agent tokens: keys=%d", len(cfg.SeedTokens))
	}

	authPath := LocalAuthPath(dir)
	rawFirst, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("local-auth.json not created: %v", err)
	}

	// Second LoadLocal reuses the exact same token and agent tokens.
	cfg2, err := LoadLocal(env(nil), LocalOptions{DataDir: dir, Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("second LoadLocal: %v", err)
	}
	if cfg2.Token != cfg.Token || cfg2.SeedTokens["claude"] != cfg.SeedTokens["claude"] {
		t.Errorf("tokens changed across restart")
	}
	rawSecond, _ := os.ReadFile(authPath)
	if string(rawFirst) != string(rawSecond) {
		t.Errorf("local-auth.json rewritten on second load")
	}

	// Corrupted local-auth.json must fail without overwriting the file!
	corruptDir := filepath.Join(t.TempDir(), "corrupt")
	if err := os.MkdirAll(corruptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	corruptContent := "{corrupt-json-do-not-overwrite"
	corruptPath := LocalAuthPath(corruptDir)
	if err := os.WriteFile(corruptPath, []byte(corruptContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLocal(env(nil), LocalOptions{DataDir: corruptDir}); err == nil {
		t.Fatal("corrupt local-auth.json was accepted")
	}
	afterCorrupt, _ := os.ReadFile(corruptPath)
	if string(afterCorrupt) != corruptContent {
		t.Errorf("corrupt local-auth.json was overwritten: %q", string(afterCorrupt))
	}

	// Regression check: remote Load still requires AGB_TOKEN and AGB_ACTORS
	if _, err := Load(env(map[string]string{"AGB_LOCAL_DATA_DIR": dir})); err == nil || !strings.Contains(err.Error(), "AGB_TOKEN") {
		t.Errorf("remote Load regression: err=%v", err)
	}
}
