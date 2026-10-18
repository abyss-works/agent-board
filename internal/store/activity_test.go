package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func TestCommentsAndActivityFilters(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	a := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "a"})
	b := mustIssue(t, s, ctx, trClaude, IssueInput{Title: "b", Project: "OTH"})

	c, err := s.AddComment(ctx, trClaude, a.Key, "착수한다.", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != model.ActivityCommented || c.Body == nil || *c.Body != "착수한다." || c.Changes != nil || c.Actor != "claude" || c.Issue != a.Key || c.ID == 0 {
		t.Errorf("comment = %+v", c)
	}
	if _, err := s.AddComment(ctx, trClaude, "AGB-9", "x", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing issue err = %v", err)
	}
	// Comments are writes to the issue, so a non-active project rejects them.
	archived := model.ProjectArchived
	s.UpdateProject(ctx, trOwner, "OTH", ProjectPatch{Status: &archived})
	if _, err := s.AddComment(ctx, trClaude, b.Key, "x", nil); !errors.Is(err, ErrProjectNotActive) {
		t.Errorf("archived project comment err = %v", err)
	}
	active := model.ProjectActive
	s.UpdateProject(ctx, trOwner, "OTH", ProjectPatch{Status: &active})
	doing := model.StatusDoing
	s.UpdateIssue(ctx, trOwner, a.Key, IssuePatch{Status: &doing}, nil)
	s.UpdateIssue(ctx, trOwner, b.Key, IssuePatch{Title: str("b2")}, nil)
	// Comments are allowed on deleted issues.
	del := model.StatusDeleted
	if _, _, err := s.UpdateIssue(ctx, trOwner, b.Key, IssuePatch{Status: &del, StatusReason: str("삭제 시험")}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddComment(ctx, trOwner, b.Key, "deleted comment", nil); err != nil {
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

func TestCommentSummaryRoundTrip(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	a := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "a"})

	with, err := s.AddComment(ctx, trClaude, a.Key, "랭킹 함수를 추가했다.", str("서버 랭킹 함수 구현"))
	if err != nil {
		t.Fatal(err)
	}
	if with.Summary == nil || *with.Summary != "서버 랭킹 함수 구현" || with.Body == nil || *with.Body != "랭킹 함수를 추가했다." {
		t.Errorf("comment with summary = %+v", with)
	}
	without, err := s.AddComment(ctx, trClaude, a.Key, "요약 없는 댓글", nil)
	if err != nil {
		t.Fatal(err)
	}
	if without.Summary != nil {
		t.Errorf("comment without summary has Summary %q", *without.Summary)
	}
	doing := model.StatusDoing
	if _, _, err := s.UpdateIssue(ctx, trOwner, a.Key, IssuePatch{Status: &doing}, nil); err != nil {
		t.Fatal(err)
	}

	// Every read path returns the stored summary: created and updated records
	// and comments without one carry nil.
	items, total, err := s.ListActivity(ctx, ActivityFilter{Issues: []string{a.Key}, Asc: true, Limit: 50})
	if err != nil || total != 4 {
		t.Fatalf("ListActivity = %d items, total %d, err %v", len(items), total, err)
	}
	wantKinds := []model.ActivityKind{model.ActivityCreated, model.ActivityCommented, model.ActivityCommented, model.ActivityUpdated}
	for n, it := range items {
		if it.Kind != wantKinds[n] {
			t.Fatalf("item %d kind = %s, want %s", n, it.Kind, wantKinds[n])
		}
		if n == 1 {
			if it.Summary == nil || *it.Summary != "서버 랭킹 함수 구현" {
				t.Errorf("listed comment summary = %v", it.Summary)
			}
		} else if it.Summary != nil {
			t.Errorf("item %d (%s) has Summary %q, want nil", n, it.Kind, *it.Summary)
		}
	}
	// The absent summary is stored as NULL, never as an empty string.
	var nulls, stored int
	if err := s.db.QueryRow(`select count(*) filter (where summary is null), count(*) filter (where summary is not null) from activity`).Scan(&nulls, &stored); err != nil {
		t.Fatal(err)
	}
	if nulls != 3 || stored != 1 {
		t.Errorf("summary NULL rows = %d, stored rows = %d, want 3 and 1", nulls, stored)
	}
}

func TestCommentSummaryValidation(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	a := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "a"})

	ok := map[string]string{
		"one rune":        "가",
		"30 runes":        strings.Repeat("가", 30),
		"inner spaces":    "서버  랭킹 함수",
		"surrounding tab": "\t랭킹\t",
	}
	for name, summary := range ok {
		c, err := s.AddComment(ctx, trClaude, a.Key, "본문", &summary)
		if err != nil || c.Summary == nil || *c.Summary != summary {
			t.Errorf("%s: comment = %+v, err %v", name, c, err)
		}
	}
	rejected := map[string]string{
		"empty":       "",
		"31 runes":    strings.Repeat("가", 31),
		"line feed":   "서버\n랭킹",
		"carriage":    "서버\r랭킹",
		"only breaks": "\r\n",
	}
	var before int
	if err := s.db.QueryRow(`select count(*) from activity`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for name, summary := range rejected {
		_, err := s.AddComment(ctx, trClaude, a.Key, "본문", &summary)
		var verr *model.ValidationError
		if !errors.As(err, &verr) || len(verr.Fields) == 0 || verr.Fields[0].Field != "summary" {
			t.Errorf("%s: err = %v, want a summary validation error", name, err)
		}
	}
	var after int
	if err := s.db.QueryRow(`select count(*) from activity`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("rejected comments wrote %d records", after-before)
	}
}
