package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "agb.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenAppliesPragmasAndMigrations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agb.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var mode string
	if err := s.db.QueryRow("pragma journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	var fk int
	if err := s.db.QueryRow("pragma foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
	var version int
	if err := s.db.QueryRow("pragma user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion {
		t.Errorf("user_version = %d, want %d", version, SchemaVersion)
	}
	for _, table := range []string{"actor", "project", "issue", "issue_label", "issue_dependency", "issue_ref", "activity", "issue_fts"} {
		var n int
		if err := s.db.QueryRow("select count(*) from sqlite_master where name = ?", table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("table %s missing", table)
		}
	}
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Errorf("wal file: %v", err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agb.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s.Close()
	var version int
	if err := s.db.QueryRow("pragma user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion {
		t.Errorf("user_version = %d after reopen", version)
	}
}

func TestEnsureActors(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	actors := []model.Actor{{ID: "owner", Kind: model.ActorUser}, {ID: "claude", Kind: model.ActorAgent}}
	if err := s.EnsureActors(ctx, actors); err != nil {
		t.Fatal(err)
	}
	// Re-declaring with a changed kind updates the row; nothing is deleted.
	if err := s.EnsureActors(ctx, []model.Actor{{ID: "claude", Kind: model.ActorUser}}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow("select count(*) from actor").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("actor rows = %d, want 2", n)
	}
	var kind string
	if err := s.db.QueryRow("select kind from actor where id = 'claude'").Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "user" {
		t.Errorf("claude kind = %q, want user", kind)
	}
}

func TestFTS5Available(t *testing.T) {
	s := openTest(t)
	if _, err := s.db.Exec("insert into issue_fts(issue_fts) values ('rebuild')"); err != nil {
		t.Fatalf("fts5 rebuild: %v", err)
	}
}
