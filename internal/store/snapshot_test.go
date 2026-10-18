package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func TestSnapshotAndFingerprint(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "a"})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "b"})

	f1, err := s.FingerprintNow(ctx)
	if err != nil || f1 == "" {
		t.Fatalf("fingerprint: %q %v", f1, err)
	}
	f2, _ := s.FingerprintNow(ctx)
	if f1 != f2 {
		t.Errorf("fingerprint changed without writes: %q -> %q", f1, f2)
	}
	s.AddComment(ctx, trOwner, "AGB-1", "c", nil)
	f3, _ := s.FingerprintNow(ctx)
	if f3 == f2 {
		t.Error("fingerprint unchanged after an issue write")
	}
	name := "renamed"
	s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Name: &name})
	f4, _ := s.FingerprintNow(ctx)
	if f4 == f3 {
		t.Error("fingerprint unchanged after a project write")
	}

	dst := filepath.Join(t.TempDir(), "snap.db")
	if err := s.Snapshot(ctx, dst); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dst))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("select count(*) from issue").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("snapshot issues = %d", n)
	}
	var mode string
	db.QueryRow("pragma journal_mode").Scan(&mode)
	if mode == "wal" {
		t.Error("snapshot should be a self-contained non-WAL file")
	}
	if _, err := db.Exec("insert into issue_fts(issue_fts) values ('integrity-check')"); err != nil {
		t.Errorf("fts index in snapshot: %v", err)
	}
	// The live store keeps working after the snapshot.
	if _, err := s.CreateIssue(ctx, trOwner, IssueInput{Project: "AGB", Title: "after"}); err != nil {
		t.Errorf("write after snapshot: %v", err)
	}
	_ = model.StatusTodo
}
