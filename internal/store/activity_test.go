package store

import (
	"errors"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func TestCommentsAndActivityFilters(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	a := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "a"})
	b := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "b", Project: "OTH"})

	c, err := s.AddComment(ctx, trClaude, a.Key, "착수한다.")
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != model.ActivityCommented || c.Body == nil || *c.Body != "착수한다." || c.Changes != nil || c.Actor != "claude" || c.Issue != a.Key || c.ID == 0 {
		t.Errorf("comment = %+v", c)
	}
	if _, err := s.AddComment(ctx, trClaude, "AGB-9", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing issue err = %v", err)
	}
	// Comments are writes to the issue, so a non-active project rejects them.
	archived := model.ProjectArchived
	s.UpdateProject(ctx, trOwner, "OTH", ProjectPatch{Status: &archived})
	if _, err := s.AddComment(ctx, trClaude, b.Key, "x"); !errors.Is(err, ErrProjectNotActive) {
		t.Errorf("archived project comment err = %v", err)
	}
	active := model.ProjectActive
	s.UpdateProject(ctx, trOwner, "OTH", ProjectPatch{Status: &active})
	doing := model.StatusDoing
	s.UpdateIssue(ctx, trOwner, a.Key, IssuePatch{Status: &doing}, nil)
	s.UpdateIssue(ctx, trOwner, b.Key, IssuePatch{Title: str("b2")}, nil)
	// Comments are allowed on deleted issues.
	del := model.StatusDeleted
	s.UpdateIssue(ctx, trOwner, b.Key, IssuePatch{Status: &del}, nil)
	if _, err := s.AddComment(ctx, trOwner, b.Key, "deleted comment"); err != nil {
		t.Errorf("comment on deleted issue: %v", err)
	}

	count := func(f ActivityFilter) int {
		t.Helper()
		f.Limit = 50
		_, total, err := s.ListActivity(ctx, f)
		if err != nil {
			t.Fatalf("ListActivity(%+v): %v", f, err)
		}
		return total
	}
	// a: created, commented, updated(status) = 3; b: created, updated(title), updated(status), commented = 4
	if n := count(ActivityFilter{}); n != 7 {
		t.Errorf("all = %d", n)
	}
	if n := count(ActivityFilter{Projects: []string{"OTH"}}); n != 4 {
		t.Errorf("project OTH = %d", n)
	}
	if n := count(ActivityFilter{Issues: []string{a.Key}}); n != 3 {
		t.Errorf("issue a = %d", n)
	}
	if n := count(ActivityFilter{Actors: []string{"claude"}}); n != 2 {
		t.Errorf("actor claude = %d", n)
	}
	if n := count(ActivityFilter{Sessions: []string{"s-claude"}}); n != 2 {
		t.Errorf("session = %d", n)
	}
	if n := count(ActivityFilter{ClientHosts: []string{"pc"}}); n != 5 {
		t.Errorf("client host pc = %d", n)
	}
	if n := count(ActivityFilter{Kinds: []model.ActivityKind{model.ActivityCommented}}); n != 2 {
		t.Errorf("kind commented = %d", n)
	}
	// created records carry every field, so field=status alone also counts them.
	if n := count(ActivityFilter{Field: "status"}); n != 4 {
		t.Errorf("field status = %d", n)
	}
	if n := count(ActivityFilter{Field: "status", Kinds: []model.ActivityKind{model.ActivityUpdated}}); n != 2 {
		t.Errorf("field status updated = %d", n)
	}
	if n := count(ActivityFilter{Field: "title", Kinds: []model.ActivityKind{model.ActivityUpdated}}); n != 1 {
		t.Errorf("field title updated = %d", n)
	}

	items, _, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{a.Key}, Limit: 50})
	if items[0].Kind != model.ActivityUpdated || items[2].Kind != model.ActivityCreated {
		t.Errorf("default order must be newest first: %+v", items)
	}
	items, _, _ = s.ListActivity(ctx, ActivityFilter{Issues: []string{a.Key}, Asc: true, Limit: 50})
	if items[0].Kind != model.ActivityCreated {
		t.Errorf("asc order: %+v", items)
	}
	since := items[1].At
	if n := count(ActivityFilter{Issues: []string{a.Key}, Since: &since}); n != 2 {
		t.Errorf("since inclusive = %d", n)
	}
	if n := count(ActivityFilter{Issues: []string{a.Key}, Until: &since}); n != 1 {
		t.Errorf("until exclusive = %d", n)
	}
	page, total, _ := s.ListActivity(ctx, ActivityFilter{Limit: 2, Offset: 5})
	if total != 7 || len(page) != 2 {
		t.Errorf("paging = %d items, total %d", len(page), total)
	}
}
