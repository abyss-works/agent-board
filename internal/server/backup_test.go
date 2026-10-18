package server

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/backup"
	"github.com/wsk0715/agent-board/internal/config"
	"github.com/wsk0715/agent-board/internal/model"
)

func backupConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Addr:    "127.0.0.1:0",
		DataDir: filepath.Join(t.TempDir(), "data"),
		Token:   testToken,
		Actors:  []model.Actor{{ID: "owner", Kind: model.ActorUser}},
		Backup: config.Backup{
			Bucket: "agb-backup", Endpoint: "https://example.invalid", AccessKeyID: "dummy", SecretAccessKey: "dummy",
			Prefix: "agb", Interval: 20 * time.Millisecond, Retention: 672 * time.Hour,
		},
	}
}

func TestBackupRunsWhenConfigured(t *testing.T) {
	store := backup.NewMemory()
	srv, err := New(backupConfig(t), Options{Version: "t", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), BackupStore: store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(store.All()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	objs := store.All()
	if len(objs) == 0 {
		t.Fatal("no backup uploaded")
	}
	if !strings.HasPrefix(objs[0].Name, "agb/agb-") || !strings.HasSuffix(objs[0].Name, ".db.gz") || objs[0].Size == 0 {
		t.Errorf("object = %+v", objs[0])
	}
}

func TestBackupConfiguredWithoutAdapterFails(t *testing.T) {
	_, err := New(backupConfig(t), Options{Version: "t", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err == nil || !strings.Contains(err.Error(), "AGB_BACKUP_BUCKET") {
		t.Fatalf("err = %v", err)
	}
}

func TestBackupDisabledWithoutBucket(t *testing.T) {
	cfg := backupConfig(t)
	cfg.Backup = config.Backup{Interval: time.Hour, Retention: time.Hour, Prefix: "agb"}
	store := backup.NewMemory()
	srv, err := New(cfg, Options{Version: "t", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), BackupStore: store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	srv.Shutdown(context.Background())
	if len(store.All()) != 0 {
		t.Error("backup ran although no bucket is configured")
	}
}
