package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLegacyConfigMigratesToDefaultContextWithBackup(t *testing.T) {
	srv := newFakeServer(t)
	srv.on("GET", "/v1/actors/me", canned{body: `{"id":"claude","kind":"agent"}`})
	h := newHarness(t, nil, false)
	for k := range h.env {
		delete(h.env, k)
	}

	legacy := `{
  "url": "` + srv.srv.URL + `",
  "token": "legacy-shared-secret-0123456789abcdef",
  "tokens": {
    "claude": "agb_legacy-claude-secret",
    "codex": "agb_legacy-codex-secret"
  },
  "actor": "claude",
  "session_id": "sess-legacy",
  "client_host": "desk-legacy",
  "hook_actors": {
    "claude": "claude"
  }
}
`
	if err := os.MkdirAll(filepath.Dir(h.app.ConfigPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.app.ConfigPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	if code := h.run("whoami"); code != 0 {
		t.Fatalf("whoami: code=%d stderr=%s", code, h.stderr)
	}
	req := srv.last()
	if req.Header.Get("Authorization") != "Bearer agb_legacy-claude-secret" ||
		req.Header.Get("X-Actor") != "claude" ||
		req.Header.Get("X-Session-Id") != "sess-legacy" ||
		req.Header.Get("X-Client-Host") != "desk-legacy" {
		t.Errorf("headers = %v", req.Header)
	}

	bakPath := h.app.ConfigPath + ".bak"
	bakRaw, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	if string(bakRaw) != legacy {
		t.Errorf("backup differs:\ngot:  %s\nwant: %s", bakRaw, legacy)
	}

	migratedRaw, err := os.ReadFile(h.app.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var migrated map[string]any
	if err := json.Unmarshal(migratedRaw, &migrated); err != nil {
		t.Fatalf("unmarshal migrated: %v\n%s", err, migratedRaw)
	}
	if migrated["current_context"] != "default" {
		t.Errorf("current_context = %v, want default", migrated["current_context"])
	}
	for _, legacyKey := range []string{"url", "token", "tokens", "actor"} {
		if _, exists := migrated[legacyKey]; exists {
			t.Errorf("top-level %q must not remain in migrated config:\n%s", legacyKey, migratedRaw)
		}
	}
	ctxs, ok := migrated["contexts"].(map[string]any)
	if !ok || ctxs["default"] == nil {
		t.Fatalf("contexts.default missing:\n%s", migratedRaw)
	}

	// If config.json.bak already exists, a subsequent migration must not overwrite it.
	secondDir := t.TempDir()
	secondPath := filepath.Join(secondDir, "config.json")
	if err := os.WriteFile(secondPath+".bak", []byte("original-backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte(`{"url":"http://example.test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(secondPath); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(secondPath + ".bak"); string(got) != "original-backup" {
		t.Errorf("existing .bak was overwritten: %q", got)
	}

	// Missing or empty {} config must not create a .bak file.
	emptyPath := filepath.Join(t.TempDir(), "config.json")
	if _, err := loadConfig(emptyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(emptyPath + ".bak"); !os.IsNotExist(err) {
		t.Errorf("missing config created .bak: %v", err)
	}
	if err := os.WriteFile(emptyPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(emptyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(emptyPath + ".bak"); !os.IsNotExist(err) {
		t.Errorf("empty {} config created .bak: %v", err)
	}
}

func TestContextSelectionPrecedenceAndOverrides(t *testing.T) {
	prod := newFakeServer(t)
	prod.on("GET", "/v1/actors/me", canned{body: `{"id":"owner","kind":"user"}`})
	staging := newFakeServer(t)
	staging.on("GET", "/v1/actors/me", canned{body: `{"id":"claude","kind":"agent"}`})
	override := newFakeServer(t)
	override.on("GET", "/v1/actors/me", canned{body: `{"id":"owner","kind":"user"}`})

	h := newHarness(t, nil, false)
	for k := range h.env {
		delete(h.env, k)
	}

	cfgJSON := `{
  "current_context": "prod",
  "contexts": {
    "prod": {
      "url": "` + prod.srv.URL + `",
      "token": "prod-shared-token-0123456789abcdef0123",
      "actor": "owner"
    },
    "staging": {
      "url": "` + staging.srv.URL + `",
      "tokens": {"claude": "agb_staging-claude-token"},
      "actor": "claude"
    }
  }
}`
	if err := os.MkdirAll(filepath.Dir(h.app.ConfigPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.app.ConfigPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	// (a) Active current_context ("prod")
	if code := h.run("whoami"); code != 0 {
		t.Fatalf("active context: code=%d stderr=%s", code, h.stderr)
	}
	if len(prod.reqs) != 1 || len(staging.reqs) != 0 {
		t.Fatalf("prod=%d staging=%d", len(prod.reqs), len(staging.reqs))
	}
	if prod.last().Header.Get("Authorization") != "Bearer prod-shared-token-0123456789abcdef0123" || prod.last().Header.Get("X-Actor") != "owner" {
		t.Errorf("prod headers = %v", prod.last().Header)
	}

	// (a2) use-context switches active context to staging
	if code := h.run("config", "use-context", "staging"); code != 0 {
		t.Fatalf("use-context: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("whoami"); code != 0 {
		t.Fatalf("after use-context: code=%d stderr=%s", code, h.stderr)
	}
	if len(staging.reqs) != 1 || staging.last().Header.Get("Authorization") != "Bearer agb_staging-claude-token" || staging.last().Header.Get("X-Actor") != "claude" {
		t.Errorf("staging reqs=%d headers=%v", len(staging.reqs), staging.last().Header)
	}

	// (b) AGB_CONTEXT beats current_context ("staging")
	h.env["AGB_CONTEXT"] = "prod"
	if code := h.run("whoami"); code != 0 {
		t.Fatalf("AGB_CONTEXT: code=%d stderr=%s", code, h.stderr)
	}
	if len(prod.reqs) != 2 {
		t.Errorf("prod reqs = %d, want 2", len(prod.reqs))
	}

	// (c) --context beats AGB_CONTEXT
	if code := h.run("--context", "staging", "whoami"); code != 0 {
		t.Fatalf("--context before cmd: code=%d stderr=%s", code, h.stderr)
	}
	if len(staging.reqs) != 2 {
		t.Errorf("staging reqs = %d, want 2", len(staging.reqs))
	}
	if code := h.run("whoami", "--context", "staging"); code != 0 {
		t.Fatalf("--context after cmd: code=%d stderr=%s", code, h.stderr)
	}
	if len(staging.reqs) != 3 {
		t.Errorf("staging reqs = %d, want 3", len(staging.reqs))
	}

	// Individual overrides (--url and AGB_URL) on top of selected context ("prod" via AGB_CONTEXT)
	h.env["AGB_URL"] = override.srv.URL
	if code := h.run("whoami"); code != 0 {
		t.Fatalf("AGB_URL override: code=%d stderr=%s", code, h.stderr)
	}
	if len(override.reqs) != 1 || override.last().Header.Get("Authorization") != "Bearer prod-shared-token-0123456789abcdef0123" {
		t.Errorf("override via AGB_URL: reqs=%d headers=%v", len(override.reqs), override.last().Header)
	}
	if code := h.run("whoami", "--url", prod.srv.URL); code != 0 {
		t.Fatalf("--url override: code=%d stderr=%s", code, h.stderr)
	}
	if len(prod.reqs) != 3 {
		t.Errorf("prod reqs = %d, want 3", len(prod.reqs))
	}
	delete(h.env, "AGB_URL")
	delete(h.env, "AGB_CONTEXT")

	// Unknown explicit context returns exit code 2
	if code := h.run("--context", "missing", "whoami"); code != 2 {
		t.Errorf("unknown --context: code=%d, want 2; stderr=%s", code, h.stderr)
	}
	h.env["AGB_CONTEXT"] = "missing"
	if code := h.run("whoami"); code != 2 {
		t.Errorf("unknown AGB_CONTEXT: code=%d, want 2; stderr=%s", code, h.stderr)
	}
}

func TestConfigContextSubcommands(t *testing.T) {
	h := newHarness(t, nil, true)
	for k := range h.env {
		delete(h.env, k)
	}

	// Validation errors (exit code 2)
	for _, bad := range [][]string{
		{"config", "set-context"},
		{"config", "set-context", "", "--url", "http://127.0.0.1:9000"},
		{"config", "set-context", "bad name", "--url", "http://127.0.0.1:9000"},
		{"config", "set-context", "bad\tname", "--url", "http://127.0.0.1:9000"},
		{"config", "set-context", "prod"}, // missing --url on new non-local context
		{"config", "set-context", "prod", "--url", "not-a-url"},
		{"config", "set-context", "prod", "--url", "http://127.0.0.1:9000", "--actor", "BadActor"},
		{"config", "use-context", "nonexistent"},
		{"config", "delete-context", "nonexistent"},
	} {
		if code := h.run(bad...); code != 2 {
			t.Errorf("%v: code=%d, want 2 (stderr=%s)", bad, code, h.stderr)
		}
	}

	// Create prod with --use, and staging without --use
	if code := h.run("config", "set-context", "prod", "--url", "https://agb.example.com/", "--actor", "owner", "--use"); code != 0 {
		t.Fatalf("set-context prod: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("config", "current-context"); code != 0 || strings.TrimSpace(h.stdout.String()) != "prod" {
		t.Errorf("current-context = %q (code=%d, stderr=%s)", h.stdout.String(), code, h.stderr)
	}

	// Store a token on prod and verify list-contexts never prints token secrets
	const prodSecret = "prod-super-secret-token-value-123456"
	h.app.Stdin = strings.NewReader(prodSecret + "\n")
	if code := h.run("config", "set-token"); code != 0 {
		t.Fatalf("set-token: code=%d stderr=%s", code, h.stderr)
	}

	if code := h.run("config", "set-context", "staging", "--url", "http://127.0.0.1:8421", "--actor", "claude"); code != 0 {
		t.Fatalf("set-context staging: code=%d stderr=%s", code, h.stderr)
	}
	// Update existing context actor without repeating --url
	if code := h.run("config", "set-context", "staging", "--actor", "codex"); code != 0 {
		t.Fatalf("update staging actor: code=%d stderr=%s", code, h.stderr)
	}
	// Create local context without --url (defaults to http://127.0.0.1:8420)
	if code := h.run("config", "set-context", "local"); code != 0 {
		t.Fatalf("set-context local: code=%d stderr=%s", code, h.stderr)
	}

	// list-contexts in table and JSON modes
	if code := h.run("config", "list-contexts"); code != 0 {
		t.Fatalf("list-contexts table: code=%d stderr=%s", code, h.stderr)
	}
	tableOut := h.stdout.String()
	if strings.Contains(tableOut, prodSecret) {
		t.Fatalf("list-contexts table leaked secret:\n%s", tableOut)
	}
	for _, want := range []string{"prod", "https://agb.example.com", "staging", "codex", "local", "http://127.0.0.1:8420"} {
		if !strings.Contains(tableOut, want) {
			t.Errorf("table output missing %q:\n%s", want, tableOut)
		}
	}

	if code := h.run("config", "list-contexts", "--json"); code != 0 {
		t.Fatalf("list-contexts json: code=%d stderr=%s", code, h.stderr)
	}
	if strings.Contains(h.stdout.String(), prodSecret) {
		t.Fatalf("list-contexts json leaked secret:\n%s", h.stdout.String())
	}
	var listed []struct {
		Name        string   `json:"name"`
		URL         string   `json:"url"`
		Actor       string   `json:"actor"`
		Current     bool     `json:"current"`
		TokenSet    bool     `json:"token_set"`
		ActorTokens []string `json:"actor_tokens"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &listed); err != nil || len(listed) != 3 {
		t.Fatalf("list-contexts json = %s (err=%v)", h.stdout.String(), err)
	}

	// use-context local works even when local is deleted first (built-in local creates & activates)
	if code := h.run("config", "delete-context", "local"); code != 0 {
		t.Fatalf("delete-context local: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("config", "use-context", "local"); code != 0 {
		t.Fatalf("use-context local (built-in): code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("config", "current-context"); code != 0 || strings.TrimSpace(h.stdout.String()) != "local" {
		t.Errorf("current-context after local = %q", h.stdout.String())
	}

	// Deleting active context falls back to "default" when present, else ""
	if code := h.run("config", "set-context", "default", "--url", "http://127.0.0.1:8422"); code != 0 {
		t.Fatal(h.stderr.String())
	}
	if code := h.run("config", "delete-context", "local"); code != 0 {
		t.Fatal(h.stderr.String())
	}
	if code := h.run("config", "current-context"); code != 0 || strings.TrimSpace(h.stdout.String()) != "default" {
		t.Errorf("fallback to default failed: %q (code=%d stderr=%s)", h.stdout.String(), code, h.stderr)
	}
	if code := h.run("config", "delete-context", "default"); code != 0 {
		t.Fatal(h.stderr.String())
	}
	cfg, err := loadConfig(h.app.ConfigPath)
	if err != nil || cfg.CurrentContext != "" {
		t.Errorf("current_context after deleting default = %q (err=%v)", cfg.CurrentContext, err)
	}
}

func TestLocalContextConnectionRefusedGuidesServeLocal(t *testing.T) {
	h := newHarness(t, nil, true)
	for k := range h.env {
		delete(h.env, k)
	}
	// Use an unreachable loopback port in local context.
	if code := h.run("config", "set-context", "local", "--url", "http://127.0.0.1:1", "--use"); code != 0 {
		t.Fatalf("set-context local: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("health"); code != 1 {
		t.Fatalf("health on closed port: code=%d, want 1; stderr=%s", code, h.stderr)
	}
	want := "로컬 서버가 실행 중이지 않습니다. agb serve --local 로 먼저 실행하세요."
	if !strings.Contains(h.stderr.String(), want) {
		t.Errorf("stderr lacks local serve hint %q:\n%s", want, h.stderr)
	}

	// Built-in local via --context without prior set-context (unreachable port via client transport error):
	h2 := newHarness(t, nil, true)
	for k := range h2.env {
		delete(h2.env, k)
	}
	h2.app.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, os.ErrClosed
	})}
	if code := h2.run("--context", "local", "health"); code != 1 || !strings.Contains(h2.stderr.String(), want) {
		t.Errorf("built-in --context local: code=%d stderr=%s", code, h2.stderr)
	}

	// Non-local context must NOT print the local server hint.
	if code := h2.run("config", "set-context", "prod", "--url", "http://127.0.0.1:1", "--use"); code != 0 {
		t.Fatal(h2.stderr.String())
	}
	if code := h2.run("health"); code != 1 || strings.Contains(h2.stderr.String(), "agb serve --local") {
		t.Errorf("non-local context printed local hint: code=%d stderr=%s", code, h2.stderr)
	}
}

func TestSetURLAndSetActorTargetSelectedContext(t *testing.T) {
	h := newHarness(t, nil, false)
	for k := range h.env {
		delete(h.env, k)
	}
	if code := h.run("config", "set-context", "alpha", "--url", "http://127.0.0.1:8001", "--use"); code != 0 {
		t.Fatal(h.stderr.String())
	}
	if code := h.run("config", "set-context", "beta", "--url", "http://127.0.0.1:8002"); code != 0 {
		t.Fatal(h.stderr.String())
	}
	if code := h.run("--context", "beta", "config", "set-url", "http://127.0.0.1:8099/"); code != 0 {
		t.Fatalf("set-url: code=%d stderr=%s", code, h.stderr)
	}
	if code := h.run("--context", "beta", "config", "set-actor", "codex"); code != 0 {
		t.Fatalf("set-actor: code=%d stderr=%s", code, h.stderr)
	}
	cfg, err := loadConfig(h.app.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Contexts["alpha"].URL != "http://127.0.0.1:8001" || cfg.Contexts["alpha"].Actor != "" {
		t.Errorf("alpha changed: %+v", cfg.Contexts["alpha"])
	}
	if cfg.Contexts["beta"].URL != "http://127.0.0.1:8099" || cfg.Contexts["beta"].Actor != "codex" {
		t.Errorf("beta = %+v", cfg.Contexts["beta"])
	}
}
