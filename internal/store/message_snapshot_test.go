package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestFingerprintAndSnapshotCoverMessages(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	last, err := s.FingerprintNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := putMessage(t, s, "AGB", "desk", "지문", "")
	posted, _ := s.FingerprintNow(ctx)
	if posted == last {
		t.Errorf("posting a message left the fingerprint %q", last)
	}
	if _, err := s.DeleteMessage(ctx, trOwner, m.ID); err != nil {
		t.Fatal(err)
	}
	if deleted, _ := s.FingerprintNow(ctx); deleted == posted {
		t.Errorf("deleting a message left the fingerprint %q", posted)
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
	var title string
	var deletedAt sql.NullString
	if err := db.QueryRow(`select title, deleted_at from message where id = ?`, m.ID).Scan(&title, &deletedAt); err != nil || title != "지문" || !deletedAt.Valid {
		t.Errorf("snapshot message = %q %v, %v", title, deletedAt, err)
	}
}

func TestFingerprintCoversMessageReads(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	m := putMessage(t, s, "AGB", "desk", "지문", "")
	before, err := s.FingerprintNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AckMessage(ctx, trOwner, m.ID, "alpha"); err != nil {
		t.Fatal(err)
	}
	after, _ := s.FingerprintNow(ctx)
	if after == before {
		t.Errorf("acknowledging a message left the fingerprint %q", before)
	}
	// Acknowledging again writes nothing.
	if _, err := s.AckMessage(ctx, trOwner, m.ID, "alpha"); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.FingerprintNow(ctx); again != after {
		t.Errorf("fingerprint changed without a write: %q, %q", after, again)
	}
}
