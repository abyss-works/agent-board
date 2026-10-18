// Command agb is the agent-board binary. "agb serve" runs the API server;
// every other command is a client that talks to the server over HTTP only.
package main

import (
	"context"
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

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, cli.Usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "agb %s\n", version)
		return 0
	case "serve":
		return serve(stderr, getenv)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app := cli.New(version, getenv)
	app.Stdout, app.Stderr = stdout, stderr
	return app.Run(ctx, args)
}

func serve(stderr io.Writer, getenv func(string) string) int {
	cfg, err := config.Load(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "agb serve: configuration error:\n%v\n", err)
		return 1
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	opts := server.Options{Version: version, Logger: logger}
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
