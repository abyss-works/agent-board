// Command agb is the agent-board binary. "agb serve" runs the API server;
// every other command is a client that talks to the server over HTTP only.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wsk0715/agent-board/internal/backup"
	"github.com/wsk0715/agent-board/internal/cli"
	"github.com/wsk0715/agent-board/internal/config"
	"github.com/wsk0715/agent-board/internal/server"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// release is the release the binary belongs to (vX.Y.Z), set at build time
// with -ldflags "-X main.release=...". Empty for builds not made from a release.
var release = ""

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, cli.Usage)
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintf(stdout, "agb %s\n", version)
		return 0
	case "serve":
		return serve(stdout, stderr, args[1:], getenv)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app := cli.New(version, getenv)
	app.Stdout, app.Stderr = stdout, stderr
	return app.Run(ctx, args)
}

const serveUsage = `사용법: agb serve [--local] [--addr <주소>] [--data-dir <경로>]

API 서버를 실행합니다. 플래그 없는 기존 실행은 AGB_* 환경변수로 설정합니다.

옵션:
  --local              로컬 전용 서버로 실행. 127.0.0.1 에만 받고 AGB_TOKEN, AGB_ACTORS 를 묻지 않으며
                       <data dir>/local-auth.json 에 토큰과 행위자를 시드합니다.
  --addr <주소>        수신 주소. --local 일 때는 127.0.0.1 의 주소만 허용합니다.
  --data-dir <경로>    로컬 데이터 폴더. 기본은 OS 사용자 데이터 폴더입니다 (AGB_LOCAL_DATA_DIR).
`

func serve(stdout, stderr io.Writer, args []string, getenv func(string) string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var local, addrSet, dataDirSet bool
	var addr, dataDir string
	fs.BoolVar(&local, "local", false, "")
	fs.StringVar(&addr, "addr", "", "")
	fs.StringVar(&dataDir, "data-dir", "", "")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			fmt.Fprint(stdout, serveUsage)
			return 0
		}
		fmt.Fprintf(stderr, "agb serve: %v\n\n%s", err, serveUsage)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "agb serve: 인자가 너무 많습니다\n\n%s", serveUsage)
		return 2
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "addr":
			addrSet = true
		case "data-dir":
			dataDirSet = true
		}
	})
	if !local && (addrSet || dataDirSet) {
		fmt.Fprint(stderr, "agb serve: --addr 와 --data-dir 은 --local 에서만 씁니다.\n")
		return 2
	}

	var cfg *config.Config
	var err error
	if local {
		cfg, err = config.LoadLocal(getenv, config.LocalOptions{Addr: addr, DataDir: dataDir})
	} else {
		cfg, err = config.Load(getenv)
	}
	if err != nil {
		fmt.Fprintf(stderr, "agb serve: configuration error:\n%v\n", err)
		return 1
	}
	return runServer(cfg, stderr)
}

func runServer(cfg *config.Config, stderr io.Writer) int {
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	opts := server.Options{Version: version, Release: release, Logger: logger}
	if cfg.Backup.Enabled() {
		store, err := backup.NewS3(cfg.Backup.Endpoint, cfg.Backup.Bucket, cfg.Backup.AccessKeyID, cfg.Backup.SecretAccessKey)
		if err != nil {
			fmt.Fprintf(stderr, "agb serve: configuration error:\n%v\n", err)
			return 1
		}
		opts.BackupStore = store
	}
	srv, err := server.New(cfg, opts)
	if err != nil {
		fmt.Fprintf(stderr, "agb serve: %v\n", err)
		return 1
	}
	if _, err := srv.Start(); err != nil {
		fmt.Fprintf(stderr, "agb serve: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		fmt.Fprintf(stderr, "agb serve: shutdown: %v\n", err)
		return 1
	}
	return 0
}
