// Package server wires configuration, the store, the HTTP handler and the
// background runners (backup and embedding) into one process.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wsk0715/agent-board/internal/api"
	"github.com/wsk0715/agent-board/internal/backup"
	"github.com/wsk0715/agent-board/internal/config"
	"github.com/wsk0715/agent-board/internal/embed"
	"github.com/wsk0715/agent-board/internal/graph"
	"github.com/wsk0715/agent-board/internal/ingest"
	"github.com/wsk0715/agent-board/internal/search"
	"github.com/wsk0715/agent-board/internal/store"
	pgstore "github.com/wsk0715/agent-board/internal/store/pg"
)

// Options are the process level settings not read from the environment.
// BackupStore is the object store adapter; it is required when backups are
// enabled in the configuration.
type Options struct {
	Version     string
	Release     string
	Logger      *slog.Logger
	BackupStore backup.ObjectStore
	// PGDumpBin overrides the pg_dump executable of the PostgreSQL backup.
	PGDumpBin string
	// PGEmbedder and PGReranker optionally override the Voyage AI adapters in
	// PostgreSQL mode (primarily for tests and custom wiring).
	PGEmbedder pgstore.Embedder
	PGReranker pgstore.Reranker
}

// Server is a configured, not yet listening, API server.
type Server struct {
	cfg          *config.Config
	opts         Options
	store        *store.Store
	pgStore      *pgstore.Store
	ingestQueue  *ingest.Queue
	ingestWorker *ingest.Worker
	http         *http.Server
	ln           net.Listener
	cancel       context.CancelFunc
	done         chan struct{}
	bg           sync.WaitGroup
	// embedder and searcher are set only when the search by meaning is enabled.
	embedder *embed.Runner
	searcher *embed.Searcher
}

func (s *Server) closeStore() error {
	if s.ingestQueue != nil {
		s.ingestQueue.Close()
	}
	if s.pgStore != nil {
		return s.pgStore.Close()
	}
	if s.store != nil {
		return s.store.Close()
	}
	return nil
}

// New opens the store and prepares the HTTP server.
func New(cfg *config.Config, opts Options) (*Server, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if cfg.UsePostgres() {
		return newPostgresServer(cfg, opts)
	}
	if cfg.Backup.Enabled() && opts.BackupStore == nil {
		return nil, errors.New("AGB_BACKUP_BUCKET is set but this build has no object store adapter")
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
	// A local server registers the seeded per-actor tokens so agent actors can
	// authenticate without the browser approval flow.
	for actor, secret := range cfg.SeedTokens {
		if _, err := st.EnsureSeedToken(context.Background(), store.TokenInput{
			Actor:      actor,
			ClientHost: "local",
			Secret:     secret,
			CreatedBy:  "system",
		}); err != nil {
			st.Close()
			return nil, fmt.Errorf("seed token for %s: %w", actor, err)
		}
	}
	persisted, err := st.ListActors(context.Background())
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("list actors: %w", err)
	}
	var embedder *embed.Runner
	var searcher *embed.Searcher
	if cfg.Embedding.Enabled() {
		client := embed.NewClient(cfg.Embedding.URL, cfg.Embedding.APIKey, cfg.Embedding.Model, cfg.Embedding.Provider)
		index := &embed.Index{}
		rows, err := st.LoadEmbeddings(context.Background(), cfg.Embedding.Model)
		if err != nil {
			st.Close()
			return nil, fmt.Errorf("load embeddings: %w", err)
		}
		index.Load(rows)
		embedder = &embed.Runner{Store: st, Embedder: client, Model: cfg.Embedding.Model, Interval: cfg.Embedding.Interval, Index: index, Logger: opts.Logger}
		searcher = &embed.Searcher{Embedder: client, Prefix: cfg.Embedding.QueryPrefix, Index: index, Timeout: cfg.Embedding.QueryTimeout, Model: cfg.Embedding.Model, Logger: opts.Logger}
	}
	var web fs.FS
	if cfg.WebDir != "" {
		web = os.DirFS(cfg.WebDir)
	}
	apiOpts := api.Options{Token: cfg.Token, Actors: persisted, Version: opts.Version, Release: opts.Release, Logger: opts.Logger, Store: st, Web: web}
	if searcher != nil {
		apiOpts.Semantic = searcher
		apiOpts.SimilarModel = cfg.Embedding.Model
	}
	h := api.New(apiOpts)
	return &Server{
		cfg:   cfg,
		opts:  opts,
		store: st,
		http: &http.Server{
			Handler:           h,
			ReadHeaderTimeout: 10 * time.Second,
		},
		done:     make(chan struct{}),
		embedder: embedder,
		searcher: searcher,
	}, nil
}

func newPostgresServer(cfg *config.Config, opts Options) (*Server, error) {
	ctx := context.Background()
	pgs, err := pgstore.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return nil, fmt.Errorf("open postgres database: %w", err)
	}
	if err := pgs.EnsureActors(ctx, cfg.Actors); err != nil {
		_ = pgs.Close()
		return nil, fmt.Errorf("declare actors: %w", err)
	}
	for actor, secret := range cfg.SeedTokens {
		if _, err := pgs.EnsureSeedToken(ctx, pgstore.CreateTokenInput{
			Actor:      actor,
			ClientHost: "local",
			Secret:     secret,
			CreatedBy:  "system",
		}); err != nil {
			_ = pgs.Close()
			return nil, fmt.Errorf("seed token for %s: %w", actor, err)
		}
	}
	persisted, err := pgs.ListActors(ctx)
	if err != nil {
		_ = pgs.Close()
		return nil, fmt.Errorf("list actors: %w", err)
	}
	var web fs.FS
	if cfg.WebDir != "" {
		web = os.DirFS(cfg.WebDir)
	}

	graphStore := graph.NewStore(pgs.Pool())
	ingestQueue := ingest.NewQueue(pgs.Pool(), ingest.QueueConfig{})

	pgEmbedder := opts.PGEmbedder
	pgReranker := opts.PGReranker
	if cfg.ContextEngine.VoyageEnabled() {
		if pgEmbedder == nil {
			pgEmbedder = embed.NewVoyageEmbedder(embed.VoyageEmbedderConfig{
				BaseURL: cfg.ContextEngine.VoyageBaseURL,
				APIKey:  cfg.ContextEngine.VoyageAPIKey,
				Model:   cfg.ContextEngine.VoyageEmbedModel,
			})
		}
		if pgReranker == nil {
			pgReranker = embed.NewVoyageReranker(embed.VoyageRerankerConfig{
				BaseURL: cfg.ContextEngine.VoyageBaseURL,
				APIKey:  cfg.ContextEngine.VoyageAPIKey,
				Model:   cfg.ContextEngine.VoyageRerankModel,
				Timeout: cfg.ContextEngine.RerankTimeout,
			})
		}
	}

	ingestWorker := ingest.NewWorker(ingest.WorkerConfig{
		Pool:       pgs.Pool(),
		Queue:      ingestQueue,
		GraphStore: graphStore,
		Embedder:   pgEmbedder,
		Logger:     opts.Logger,
	})
	contextEngine := search.NewEngine(search.EngineConfig{
		Pool:            pgs.Pool(),
		GraphStore:      graphStore,
		Embedder:        pgEmbedder,
		Reranker:        pgReranker,
		CutoffThreshold: cfg.ContextEngine.ContextConfidenceCutoff,
		RerankTimeout:   cfg.ContextEngine.RerankTimeout,
		Logger:          opts.Logger,
	})

	h := api.New(api.Options{
		Token:         cfg.Token,
		Actors:        persisted,
		Version:       opts.Version,
		Release:       opts.Release,
		Logger:        opts.Logger,
		PGStore:       pgs,
		GraphStore:    graphStore,
		IngestQueue:   ingestQueue,
		ContextEngine: contextEngine,
		Web:           web,
	})
	return &Server{
		cfg:          cfg,
		opts:         opts,
		pgStore:      pgs,
		ingestQueue:  ingestQueue,
		ingestWorker: ingestWorker,
		http: &http.Server{
			Handler:           h,
			ReadHeaderTimeout: 10 * time.Second,
		},
		done: make(chan struct{}),
	}, nil
}

// Start binds the address and serves in the background. It returns the
// bound address, which matters when the configured port is 0. When Start
// fails it closes the store, so the Server is unusable and Shutdown must not
// be called.
func (s *Server) Start() (string, error) {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		_ = s.closeStore()
		return "", err
	}
	s.ln = ln
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.startBackground(ctx)
	if s.cfg.WebDir != "" {
		s.opts.Logger.Info("serving web client", "dir", s.cfg.WebDir)
	}
	go func() {
		defer close(s.done)
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.opts.Logger.Error("http serve", "err", err.Error())
		}
	}()
	s.opts.Logger.Info("listening", "addr", ln.Addr().String())
	return ln.Addr().String(), nil
}

// startBackground launches the backup runner when a bucket is configured and
// the embedding runner when an embeddings API key is configured.
func (s *Server) startBackground(ctx context.Context) {
	if s.store != nil && s.cfg.Backup.Enabled() {
		r := &backup.Runner{
			Store:     s.opts.BackupStore,
			Source:    s.store,
			Prefix:    s.cfg.Backup.Prefix,
			Interval:  s.cfg.Backup.Interval,
			Retention: s.cfg.Backup.Retention,
			TempDir:   filepath.Join(s.cfg.DataDir, "backup-tmp"),
			Logger:    s.opts.Logger,
		}
		s.bg.Go(func() { r.Run(ctx) })
	} else if s.store != nil {
		s.opts.Logger.Info("backup disabled: AGB_BACKUP_BUCKET is empty")
	}
	if s.pgStore != nil {
		s.startPGBackup(ctx)
	}
	if s.embedder != nil {
		s.bg.Go(func() { s.embedder.Run(ctx) })
	} else if s.store != nil {
		s.opts.Logger.Info("semantic search disabled: AGB_EMBEDDING_API_KEY is empty")
	}
	if s.ingestWorker != nil {
		s.bg.Go(func() { _ = s.ingestWorker.Run(ctx) })
	}
}

// startPGBackup launches the pg_dump backup runner in PostgreSQL mode when a
// bucket is configured.
func (s *Server) startPGBackup(ctx context.Context) {
	if !s.cfg.Backup.Enabled() {
		s.opts.Logger.Info("backup disabled: AGB_BACKUP_BUCKET is empty")
		return
	}
	if s.opts.BackupStore == nil {
		s.opts.Logger.Error("backup disabled: no object store configured")
		return
	}
	src, err := backup.NewPGSource(s.cfg.DatabaseURL)
	if err != nil {
		s.opts.Logger.Error("backup disabled", "err", err.Error())
		return
	}
	src.DumpBin = s.opts.PGDumpBin
	tmp := os.TempDir()
	if s.cfg.DataDir != "" {
		tmp = s.cfg.DataDir
	}
	r := &backup.Runner{
		Store:     s.opts.BackupStore,
		Source:    src,
		Prefix:    s.cfg.Backup.Prefix,
		Interval:  s.cfg.Backup.Interval,
		Retention: s.cfg.Backup.Retention,
		TempDir:   filepath.Join(tmp, "backup-tmp"),
		Logger:    s.opts.Logger,
	}
	s.bg.Go(func() { r.Run(ctx) })
}

// Shutdown stops accepting connections, closes open SSE streams, waits for
// in-flight requests until ctx expires, stops background jobs and closes the
// store.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	if closer, ok := s.http.Handler.(io.Closer); ok {
		_ = closer.Close()
	}
	err := s.http.Shutdown(ctx)
	<-s.done
	s.bg.Wait()
	if cerr := s.closeStore(); err == nil {
		err = cerr
	}
	return err
}
