package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/cli"
	"github.com/wsk0715/agent-board/internal/config"
	"github.com/wsk0715/agent-board/internal/server"
)

func TestServeHelpAndInvalidLocalFlags(t *testing.T) {
	code, out, errb := run2("serve", "--help")
	if code != 0 || !strings.Contains(out, "--local") || !strings.Contains(out, "--data-dir") || errb != "" {
		t.Errorf("serve --help: code=%d out=%q err=%q", code, out, errb)
	}

	// Non-127.0.0.1 bind addresses must fail with configuration error before starting.
	for _, addr := range []string{"0.0.0.0:8420", ":8420", "192.168.1.10:8420", "localhost:8420"} {
		var stdout, stderr bytes.Buffer
		dir := filepath.Join(t.TempDir(), "local")
		code := run([]string{"serve", "--local", "--data-dir", dir, "--addr", addr}, &stdout, &stderr, func(string) string { return "" })
		if code == 0 {
			t.Errorf("serve --local --addr %s succeeded, want failure", addr)
		}
	}
}

func TestLocalServerEndToEndWithCLIAndPersistence(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "agb-local-data")
	cliCfgPath := filepath.Join(t.TempDir(), "cli", "config.json")

	// Shell has remote AGB_TOKEN set; both local server and local CLI context must ignore it and use local-auth.json!
	envMap := map[string]string{
		"AGB_LOCAL_DATA_DIR": dataDir,
		"AGB_TOKEN":          "remote-shell-token-that-would-401-locally-0123456789",
	}
	getenv := func(k string) string { return envMap[k] }

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	startLocal := func(t *testing.T) (*server.Server, string, *config.Config) {
		t.Helper()
		cfg, err := config.LoadLocal(getenv, config.LocalOptions{Addr: "127.0.0.1:0", DataDir: dataDir})
		if err != nil {
			t.Fatalf("LoadLocal: %v", err)
		}
		srv, err := server.New(cfg, server.Options{Version: "test", Logger: logger})
		if err != nil {
			t.Fatalf("server.New: %v", err)
		}
		bound, err := srv.Start()
		if err != nil {
			t.Fatalf("srv.Start: %v", err)
		}
		return srv, "http://" + bound, cfg
	}

	srv1, baseURL1, cfg1 := startLocal(t)
	shutdown1 := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv1.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown 1: %v", err)
		}
	}

	// Ensure token secrets are never printed in server logs.
	if strings.Contains(logBuf.String(), cfg1.Token) || strings.Contains(logBuf.String(), cfg1.SeedTokens["claude"]) {
		shutdown1()
		t.Fatalf("server logs leaked local token")
	}

	runCLI := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		app := cli.New("test", getenv)
		app.ConfigPath = cliCfgPath
		app.Stdout = &out
		app.Stderr = &errb
		app.StdoutIsTerminal = false
		code := app.Run(context.Background(), args)
		return code, out.String(), errb.String()
	}

	// Point the local context to the ephemeral 127.0.0.1 port (no token or actor saved in config.json!).
	if code, _, errb := runCLI("config", "set-context", "local", "--url", baseURL1, "--use"); code != 0 {
		shutdown1()
		t.Fatalf("set-context local: code=%d stderr=%s", code, errb)
	}

	// 1) Default user actor ("owner") creates a project without --actor or login.
	if code, _, errb := runCLI("project", "create", "LOC", "로컬 프로젝트"); code != 0 {
		shutdown1()
		t.Fatalf("project create as owner: code=%d stderr=%s", code, errb)
	}

	// 2) Agent actor (--actor claude) creates an issue without browser login!
	issueBody := "## 배경\n로컬 실행 검증.\n\n## 할 일\n- 로컬 이슈 생성\n\n## 완료 조건\n- 조회 응답 확인\n"
	bodyFile := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyFile, []byte(issueBody), 0o600); err != nil {
		shutdown1()
		t.Fatal(err)
	}
	code, createOut, errb := runCLI("--actor", "claude", "issue", "create", "-p", "LOC", "-t", "사용자는 로컬 서버에 이슈를 등록할 수 있다", "--type", "feature", "--body-file", bodyFile)
	if code != 0 {
		shutdown1()
		t.Fatalf("issue create as claude: code=%d stderr=%s", code, errb)
	}
	var created struct {
		Key       string `json:"key"`
		CreatedBy string `json:"created_by"`
	}
	if err := json.Unmarshal([]byte(createOut), &created); err != nil || created.Key != "LOC-1" || created.CreatedBy != "claude" {
		shutdown1()
		t.Fatalf("created = %+v (raw=%s, err=%v)", created, createOut, err)
	}

	// 3) Restart local server on same dataDir and verify token + DB data persist.
	shutdown1()

	srv2, baseURL2, cfg2 := startLocal(t)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv2.Shutdown(ctx)
	}()

	if cfg2.Token != cfg1.Token || cfg2.SeedTokens["claude"] != cfg1.SeedTokens["claude"] {
		t.Errorf("local-auth.json tokens changed after restart")
	}

	if code, _, errb := runCLI("config", "set-context", "local", "--url", baseURL2); code != 0 {
		t.Fatalf("update local url: code=%d stderr=%s", code, errb)
	}

	// Both default human actor and --actor claude can query LOC-1 after restart.
	for _, extra := range [][]string{nil, {"--actor", "claude"}, {"--actor", "codex"}} {
		args := append(extra, "issue", "get", "LOC-1")
		code, getOut, errb := runCLI(args...)
		if code != 0 || !strings.Contains(getOut, `"LOC-1"`) {
			t.Errorf("issue get (%v): code=%d out=%s err=%s", extra, code, getOut, errb)
		}
	}
}
