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
}

// A snapshot is a complete copy of the schema version 3 data: topics, the
// topic of an issue, its status reason and its acknowledgement.
func TestSnapshotCarriesTopicsReasonsAndAcknowledgements(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	flow := mustTopic(t, s, "AGB", "이슈 흐름", nil)
	triage := mustTopic(t, s, "AGB", "트리아지", &flow.ID)
	mustIssue(t, s, ctx, trClaude, IssueInput{Title: "agent work", Topic: &triage.ID})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "user work"})
	cancelled := model.StatusCancelled
	if _, _, err := s.UpdateIssue(ctx, trOwner, "AGB-2", IssuePatch{Status: &cancelled, StatusReason: str("정리")}, nil); err != nil {
		t.Fatal(err)
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
	var version, topics, topicIssues, acked, reasons int
	if err := db.QueryRow("pragma user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`select (select count(*) from topic), (select count(*) from issue where topic_id is not null),
		(select count(*) from issue where acknowledged_at is not null), (select count(*) from issue where status_reason is not null)`).
		Scan(&topics, &topicIssues, &acked, &reasons); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion || topics != 2 || topicIssues != 1 || acked != 1 || reasons != 1 {
		t.Errorf("snapshot: version %d, topics %d, issues with a topic %d, acknowledged %d, with a reason %d", version, topics, topicIssues, acked, reasons)
	}
}

// A snapshot is a complete copy of the schema version 4 data: milestones
// of every status, their closing times, and the milestone of an issue.
func TestSnapshotCarriesMilestones(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	v1 := mustMilestone(t, s, "AGB", "v1")
	v2 := mustMilestone(t, s, "AGB", "v2")
	v3 := mustMilestone(t, s, "AGB", "v3")
	setMilestoneStatus(t, s, v2.ID, model.MilestoneClosed)
	setMilestoneStatus(t, s, v3.ID, model.MilestoneDeleted)
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "planned", Milestone: &v1.ID})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "unplanned"})

	dst := filepath.Join(t.TempDir(), "snap.db")
	if err := s.Snapshot(ctx, dst); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dst))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, milestones, closedAt, open, closed, deleted, issues int
	if err := db.QueryRow("pragma user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`select (select count(*) from milestone), (select count(*) from milestone where closed_at is not null),
		(select count(*) from milestone where status = 'open'), (select count(*) from milestone where status = 'closed'),
		(select count(*) from milestone where status = 'deleted'), (select count(*) from issue where milestone_id = ?)`, v1.ID).
		Scan(&milestones, &closedAt, &open, &closed, &deleted, &issues); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion || milestones != 3 || closedAt != 1 || open != 1 || closed != 1 || deleted != 1 || issues != 1 {
		t.Errorf("snapshot: version %d, milestones %d (open %d, closed %d, deleted %d), with a closing time %d, issues of v1 %d",
			version, milestones, open, closed, deleted, closedAt, issues)
	}
}

// A question set, changed or cleared is an issue write like any other: it
// appends an activity record, so the fingerprint moves and the backup runs, and
// the snapshot holds the column.
func TestFingerprintAndSnapshotCoverDecisionNeeded(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	i := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "a"})
	set := func(q string) IssuePatch { return IssuePatch{DecisionNeeded: Optional[string]{Set: true, Value: &q}} }

	last, err := s.FingerprintNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		name  string
		patch IssuePatch
	}{{"set", set("첫 질문")}, {"change", set("다른 질문")}, {"clear", IssuePatch{DecisionNeeded: Optional[string]{Set: true}}}} {
		if _, changed, err := s.UpdateIssue(ctx, trOwner, i.Key, step.patch, nil); err != nil || !changed {
			t.Fatalf("%s: changed=%v err=%v", step.name, changed, err)
		}
		now, err := s.FingerprintNow(ctx)
		if err != nil || now == last {
			t.Errorf("%s: fingerprint %q -> %q, want a new one (%v)", step.name, last, now, err)
		}
		last = now
	}

	if _, _, err := s.UpdateIssue(ctx, trOwner, i.Key, set("스냅샷에 담길 질문"), nil); err != nil {
		t.Fatal(err)
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
	var question sql.NullString
	if err := db.QueryRow(`select decision_needed from issue where key = ?`, i.Key).Scan(&question); err != nil || question.String != "스냅샷에 담길 질문" {
		t.Errorf("snapshot decision_needed = %q, %v", question.String, err)
	}
}
