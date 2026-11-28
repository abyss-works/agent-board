package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/backup"
	"github.com/wsk0715/agent-board/internal/config"
	"github.com/wsk0715/agent-board/internal/model"
)

// TestPostgresServerUploadsPGDumpBackup starts the PostgreSQL-mode server with a
// bucket configured and expects the first backup cycle to put a pg_dump archive
// into the object store. It needs AGB_TEST_PG_DSN and pg_dump (PATH, or named by
// AGB_TEST_PG_DUMP).
func TestPostgresServerUploadsPGDumpBackup(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("AGB_TEST_PG_DSN"))
	if baseDSN == "" {
		t.Skip("AGB_TEST_PG_DSN not set")
	}
	dump := os.Getenv("AGB_TEST_PG_DUMP")
	if dump == "" {
		dump = "pg_dump"
	}
	if _, err := exec.LookPath(dump); err != nil {
		t.Skipf("%s not available", dump)
	}
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	dbName := fmt.Sprintf("agb_t_bksrv_%d_%d", os.Getpid(), srvTestDBSeq.Add(1))
	admin, err := pgxpool.New(ctx, baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+dbName); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	admin.Close()
	t.Cleanup(func() {
		p, err := pgxpool.New(context.Background(), baseDSN)
		if err != nil {
			return
		}
		defer p.Close()
		_, _ = p.Exec(context.Background(), `DROP DATABASE IF EXISTS `+dbName+` WITH (FORCE)`)
	})
	u.Path = "/" + dbName

	store := backup.NewMemory()
	cfg := &config.Config{
		Addr:        "127.0.0.1:0",
		DataDir:     t.TempDir(),
		DatabaseURL: u.String(),
		DBMaxConns:  8,
		Token:       testToken,
		Actors:      []model.Actor{{ID: "owner", Kind: model.ActorUser}},
		Backup:      config.Backup{Bucket: "b", Prefix: "agb", Interval: time.Hour, Retention: 24 * time.Hour},
	}
	srv, err := New(cfg, Options{Version: "t", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), BackupStore: store, PGDumpBin: dump})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		for _, o := range store.All() {
			if strings.HasSuffix(o.Name, ".pgdump") && o.Size > 0 && strings.HasPrefix(string(store.Get(o.Name)), "PGDMP") {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no pg_dump archive was uploaded; store has %+v", store.All())
}
