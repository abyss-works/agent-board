// Package server wires configuration, the store, the HTTP handler and the
// backup runner into one process.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/wsk0715/agent-board/internal/api"
	"github.com/wsk0715/agent-board/internal/config"
	"github.com/wsk0715/agent-board/internal/store"
)

// Options are the process level settings not read from the environment.
type Options struct {
	Version string
	Logger  *slog.Logger
}

// Server is a configured, not yet listening, API server.
type Server struct {
	cfg    *config.Config
	opts   Options
	store  *store.Store
	http   *http.Server
	ln     net.Listener
	cancel context.CancelFunc
	done   chan struct{}
}

// New opens the store and prepares the HTTP server.
func New(cfg *config.Config, opts Options) (*Server, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "agb.db"))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := st.EnsureActors(context.Background(), cfg.Actors); err != nil {
		st.Close()
		return nil, fmt.Errorf("declare actors: %w", err)
	}
	h := api.New(api.Options{Token: cfg.Token, Actors: cfg.Actors, Version: opts.Version, Logger: opts.Logger, Store: st})
	return &Server{
		cfg:   cfg,
		opts:  opts,
		store: st,
		http: &http.Server{
			Handler:           h,
			ReadHeaderTimeout: 10 * time.Second,
		},
		done: make(chan struct{}),
	}, nil
}

// Start binds the address and serves in the background. It returns the
// bound address, which matters when the configured port is 0.
func (s *Server) Start() (string, error) {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return "", err
	}
	s.ln = ln
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.startBackground(ctx)
	go func() {
		defer close(s.done)
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.opts.Logger.Error("http serve", "err", err.Error())
		}
	}()
	s.opts.Logger.Info("listening", "addr", ln.Addr().String())
	return ln.Addr().String(), nil
}

// startBackground launches periodic jobs. Later features add the backup
// runner here.
func (s *Server) startBackground(context.Context) {}

// Shutdown stops accepting connections, waits for in-flight requests until
// ctx expires, stops background jobs and closes the store.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	err := s.http.Shutdown(ctx)
	<-s.done
	if cerr := s.store.Close(); err == nil {
		err = cerr
	}
	return err
}
