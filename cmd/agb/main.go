// Command agb is the agent-board binary. "agb serve" runs the API server;
// client commands are added later and talk to the server over HTTP only.
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

	"github.com/wsk0715/agent-board/internal/config"
	"github.com/wsk0715/agent-board/internal/server"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: agb <command>

commands:
  serve     run the API server (configured with AGB_* environment variables)
  version   print the binary version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "agb %s\n", version)
		return 0
	case "serve":
		return serve(stderr, getenv)
	default:
		fmt.Fprintf(stderr, "agb: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func serve(stderr io.Writer, getenv func(string) string) int {
	cfg, err := config.Load(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "agb serve: configuration error:\n%v\n", err)
		return 1
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	srv, err := server.New(cfg, server.Options{Version: version, Logger: logger})
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
