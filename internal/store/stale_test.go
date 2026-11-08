package store

import (
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

func TestStaleMarkAndFilter(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	at := func(age time.Duration) { s.SetClock(func() time.Time { return now.Add(-age) }) }
	const day = 24 * time.Hour

	at(31 * day)
	old := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "old", Status: model.StatusBacklog})
	commented := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "commented", Status: model.StatusDoing})
	closed := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "closed", Status: model.StatusDone})
	at(30 * day)
	edge := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "edge", Status: model.StatusReview})
	at(1 * day)
	if _, err := s.AddComment(ctx, trOwner, commented.Key, "still alive", nil, nil); err != nil {
		t.Fatal(err)
	}
	fresh := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "fresh", Status: model.StatusBacklog})
	s.SetClock(func() time.Time { return now })

	want := map[string]bool{old.Key: true, commented.Key: false, closed.Key: false, edge.Key: false, fresh.Key: false}
	items, total, err := s.ListIssues(ctx, IssueFilter{Statuses: model.IssueStatuses, Limit: 50})
	if err != nil || total != 5 {
		t.Fatalf("list: total %d err %v", total, err)
	}
	for _, it := range items {
		if it.Stale != want[it.Key] {
			t.Errorf("%s stale = %v, want %v", it.Key, it.Stale, want[it.Key])
		}
	}
	for key, stale := range want {
		got, err := s.GetIssue(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if got.Stale != stale {
			t.Errorf("GetIssue %s stale = %v, want %v", key, got.Stale, stale)
		}
	}

	yes := true
	items, total, err = s.ListIssues(ctx, IssueFilter{Statuses: model.IssueStatuses, Stale: &yes, Limit: 50})
	if err != nil || total != 1 || len(items) != 1 || items[0].Key != old.Key {
		t.Errorf("stale=true: total %d items %v err %v", total, items, err)
	}
	// Pagination counts only stale issues.
	items, total, _ = s.ListIssues(ctx, IssueFilter{Statuses: model.IssueStatuses, Stale: &yes, Limit: 1, Offset: 1})
	if total != 1 || len(items) != 0 {
		t.Errorf("offset past the only stale issue: total %d items %d", total, len(items))
	}

	// Moving the old issue refreshes updated_at and clears the mark.
	doing := model.StatusDoing
	if _, _, err := s.UpdateIssue(ctx, trOwner, old.Key, IssuePatch{Status: &doing}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetIssue(ctx, old.Key)
	if got.Stale {
		t.Error("issue updated now must not be stale")
	}
}

func TestAgendaCountsStaleIssues(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now.Add(-31 * 24 * time.Hour) })
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "a", Status: model.StatusBacklog})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "b", Status: model.StatusReview})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "c", Status: model.StatusDone})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "d", Project: "OTH", Status: model.StatusDoing})
	s.SetClock(func() time.Time { return now })
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "e", Status: model.StatusBacklog})

	a, err := s.Agenda(ctx, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, p := range a.Projects {
		got[p.Project.Key] = p.Stale
	}
	if got["AGB"] != 2 || got["OTH"] != 1 {
		t.Errorf("stale counts = %v, want AGB 2 and OTH 1", got)
	}
}
