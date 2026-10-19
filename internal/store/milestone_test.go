package store

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func mustMilestone(t *testing.T, s *Store, project, name string) *model.Milestone {
	t.Helper()
	m, err := s.CreateMilestone(t.Context(), trOwner, project, MilestoneInput{Name: name})
	if err != nil {
		t.Fatalf("CreateMilestone(%s, %s): %v", project, name, err)
	}
	return m
}

func setMilestoneStatus(t *testing.T, s *Store, id int64, status model.MilestoneStatus) *model.Milestone {
	t.Helper()
	m, err := s.UpdateMilestone(t.Context(), trOwner, id, MilestonePatch{Status: &status})
	if err != nil {
		t.Fatalf("UpdateMilestone(%d, %s): %v", id, status, err)
	}
	return m
}

func TestCreateAndGetMilestone(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	date := "2026-11-01"
	m, err := s.CreateMilestone(ctx, trOwner, "AGB", MilestoneInput{Name: "v0.2.0", Description: "운영 안정화와 릴리즈 관리", TargetDate: &date})
	if err != nil {
		t.Fatal(err)
	}
	if m.ID < 1 || m.Project != "AGB" || m.Name != "v0.2.0" || m.Description != "운영 안정화와 릴리즈 관리" || m.TargetDate == nil || *m.TargetDate != date ||
		m.Status != model.MilestoneOpen || m.ClosedAt != nil || m.CreatedBy != "owner" || m.UpdatedBy != "owner" {
		t.Errorf("milestone = %+v", m)
	}
	if m.Progress != (model.MilestoneProgress{}) {
		t.Errorf("a milestone without issues has progress %+v", m.Progress)
	}
	if m.CreatedAt.IsZero() || m.CreatedAt != m.UpdatedAt {
		t.Errorf("timestamps = %v %v", m.CreatedAt, m.UpdatedAt)
	}
	got, err := s.GetMilestone(ctx, m.ID)
	if err != nil || !reflect.DeepEqual(got, m) {
		t.Errorf("GetMilestone = %+v, %v; want %+v", got, err, m)
	}

	// Without a description and a target date the milestone has an empty description and no date.
	plain := mustMilestone(t, s, "AGB", "목표일 없음")
	if plain.ID == m.ID || plain.Description != "" || plain.TargetDate != nil {
		t.Errorf("plain milestone = %+v", plain)
	}
	if _, err := s.GetMilestone(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing milestone err = %v", err)
	}
	if _, err := s.CreateMilestone(ctx, trOwner, "NOPE", MilestoneInput{Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing project err = %v", err)
	}
}

func TestMilestoneFieldRules(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	created := 0
	create := func(in MilestoneInput) error {
		_, err := s.CreateMilestone(ctx, trOwner, "AGB", in)
		if err == nil {
			created++
		}
		return err
	}
	// The name has 1 to 50 characters; a slash is fine.
	for name, ok := range map[string]bool{"가": true, strings.Repeat("가", 50): true, "v0.2/rc": true, "": false, strings.Repeat("가", 51): false} {
		err := create(MilestoneInput{Name: name})
		if ok && err != nil || !ok && !reflect.DeepEqual(violationFields(err), []string{"name"}) {
			t.Errorf("name %q: err = %v", name, err)
		}
	}
	// The description has 0 to 2000 characters.
	for n, ok := range map[int]bool{0: true, 1: true, 2000: true, 2001: false} {
		err := create(MilestoneInput{Name: "d" + strconv.Itoa(n), Description: strings.Repeat("가", n)})
		if ok && err != nil || !ok && !reflect.DeepEqual(violationFields(err), []string{"description"}) {
			t.Errorf("description of %d characters: err = %v", n, err)
		}
	}
	// The target date is a calendar date written YYYY-MM-DD.
	for date, ok := range map[string]bool{
		"2026-11-01": true, "2024-02-29": true,
		"": false, "2026-13-01": false, "2026-02-30": false, "2026-02-29": false, "26-11-01": false, "2026-1-1": false,
		"2026/11/01": false, "2026-11-01T00:00:00Z": false, " 2026-11-01": false, "내일": false,
	} {
		d := date
		err := create(MilestoneInput{Name: "t" + date, TargetDate: &d})
		if ok && err != nil || !ok && !reflect.DeepEqual(violationFields(err), []string{"target_date"}) {
			t.Errorf("target date %q: err = %v", date, err)
		}
	}
	// Every violation of one request comes back together.
	bad := "soon"
	err := create(MilestoneInput{Name: "", Description: strings.Repeat("가", 2001), TargetDate: &bad})
	if !reflect.DeepEqual(violationFields(err), []string{"name", "description", "target_date"}) {
		t.Errorf("all violations err = %v", err)
	}
	// A refused request leaves no milestone behind.
	items, err := s.ListMilestones(ctx, "AGB", model.MilestoneStatuses)
	if err != nil || len(items) != created || created != 3+3+2 {
		t.Errorf("milestones after the rule checks = %d, accepted requests = %d (want 8): %v", len(items), created, err)
	}
}

func TestMilestoneNamesAreUniqueAmongNonDeleted(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	v1 := mustMilestone(t, s, "AGB", "v1")
	create := func(project, name string) error {
		_, err := s.CreateMilestone(ctx, trOwner, project, MilestoneInput{Name: name})
		return err
	}
	if err := create("AGB", "v1"); !errors.Is(err, ErrDuplicateMilestoneName) {
		t.Errorf("duplicate err = %v", err)
	}
	if err := create("OTH", "v1"); err != nil {
		t.Errorf("the same name in another project: %v", err)
	}
	// A closed milestone still holds its name.
	setMilestoneStatus(t, s, v1.ID, model.MilestoneClosed)
	if err := create("AGB", "v1"); !errors.Is(err, ErrDuplicateMilestoneName) {
		t.Errorf("duplicate of a closed milestone err = %v", err)
	}
	// Deleting frees the name.
	setMilestoneStatus(t, s, v1.ID, model.MilestoneDeleted)
	if err := create("AGB", "v1"); err != nil {
		t.Errorf("name of a deleted milestone: %v", err)
	}
	// The deleted milestone cannot come back, open or closed, while another one holds the name.
	for _, st := range []model.MilestoneStatus{model.MilestoneOpen, model.MilestoneClosed} {
		status := st
		if _, err := s.UpdateMilestone(ctx, trOwner, v1.ID, MilestonePatch{Status: &status}); !errors.Is(err, ErrDuplicateMilestoneName) {
			t.Errorf("restore as %s into a taken name err = %v", st, err)
		}
	}
	// Renaming into a taken name is refused; renaming a deleted milestone is not restricted.
	v2 := mustMilestone(t, s, "AGB", "v2")
	if _, err := s.UpdateMilestone(ctx, trOwner, v2.ID, MilestonePatch{Name: str("v1")}); !errors.Is(err, ErrDuplicateMilestoneName) {
		t.Errorf("rename into a taken name err = %v", err)
	}
	if _, err := s.UpdateMilestone(ctx, trOwner, v1.ID, MilestonePatch{Name: str("v2")}); err != nil {
		t.Errorf("rename of a deleted milestone: %v", err)
	}
	// With the name free again, the deleted milestone comes back.
	if _, err := s.UpdateMilestone(ctx, trOwner, v2.ID, MilestonePatch{Name: str("v3")}); err != nil {
		t.Fatal(err)
	}
	if back := setMilestoneStatus(t, s, v1.ID, model.MilestoneOpen); back.Status != model.MilestoneOpen || back.Name != "v2" {
		t.Errorf("restored = %+v", back)
	}
	// Editing something else of a milestone does not trip over its own name.
	if _, err := s.UpdateMilestone(ctx, trOwner, v1.ID, MilestonePatch{Description: str("설명")}); err != nil {
		t.Errorf("edit that keeps the name: %v", err)
	}
}

func TestUpdateMilestone(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	m := mustMilestone(t, s, "AGB", "v0.2.0")
	date := "2026-11-01"

	got, err := s.UpdateMilestone(ctx, trClaude, m.ID, MilestonePatch{
		Name: str("v0.2.1"), Description: str("설명"), TargetDate: Optional[string]{Set: true, Value: &date},
	})
	if err != nil || got.Name != "v0.2.1" || got.Description != "설명" || got.TargetDate == nil || *got.TargetDate != date ||
		got.UpdatedBy != "claude" || !m.UpdatedAt.Before(got.UpdatedAt) || got.CreatedBy != "owner" || got.CreatedAt != m.CreatedAt || got.Project != "AGB" {
		t.Fatalf("update = %+v, %v", got, err)
	}
	// A patch that changes nothing leaves updated_at and updated_by alone.
	same, err := s.UpdateMilestone(ctx, trOwner, m.ID, MilestonePatch{Name: str("v0.2.1"), TargetDate: Optional[string]{Set: true, Value: str(date)}})
	if err != nil || same.UpdatedAt != got.UpdatedAt || same.UpdatedBy != "claude" {
		t.Errorf("no-op = %+v, %v", same, err)
	}
	empty, err := s.UpdateMilestone(ctx, trOwner, m.ID, MilestonePatch{})
	if err != nil || empty.UpdatedAt != got.UpdatedAt {
		t.Errorf("empty patch = %+v, %v", empty, err)
	}
	// A null target date clears it; an empty description is allowed.
	cleared, err := s.UpdateMilestone(ctx, trOwner, m.ID, MilestonePatch{TargetDate: Optional[string]{Set: true}, Description: str("")})
	if err != nil || cleared.TargetDate != nil || cleared.Description != "" || cleared.UpdatedBy != "owner" {
		t.Errorf("clear = %+v, %v", cleared, err)
	}
	// The rules of the fields hold on update too, and every violation comes back together.
	bad, soon := model.MilestoneStatus("gone"), "soon"
	for name, c := range map[string]struct {
		patch  MilestonePatch
		fields []string
	}{
		"empty name":    {MilestonePatch{Name: str("")}, []string{"name"}},
		"long name":     {MilestonePatch{Name: str(strings.Repeat("가", 51))}, []string{"name"}},
		"long text":     {MilestonePatch{Description: str(strings.Repeat("가", 2001))}, []string{"description"}},
		"date":          {MilestonePatch{TargetDate: Optional[string]{Set: true, Value: &soon}}, []string{"target_date"}},
		"status":        {MilestonePatch{Status: &bad}, []string{"status"}},
		"several":       {MilestonePatch{Name: str(""), TargetDate: Optional[string]{Set: true, Value: &soon}, Status: &bad}, []string{"name", "target_date", "status"}},
		"empty target":  {MilestonePatch{TargetDate: Optional[string]{Set: true, Value: str("")}}, []string{"target_date"}},
		"description x": {MilestonePatch{Description: str(strings.Repeat("a", 2001)), Name: str("ok")}, []string{"description"}},
	} {
		if _, err := s.UpdateMilestone(ctx, trOwner, m.ID, c.patch); !reflect.DeepEqual(violationFields(err), c.fields) {
			t.Errorf("%s: err = %v, want violations of %v", name, err, c.fields)
		}
	}
	after, _ := s.GetMilestone(ctx, m.ID)
	if after.Name != "v0.2.1" || !reflect.DeepEqual(after, cleared) {
		t.Errorf("a refused patch changed the milestone: %+v", after)
	}
	if _, err := s.UpdateMilestone(ctx, trOwner, 9999, MilestonePatch{Name: str("x")}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing milestone err = %v", err)
	}
}

func TestMilestoneClosedAt(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	m := mustMilestone(t, s, "AGB", "v1")
	if m.ClosedAt != nil {
		t.Fatalf("a new milestone has closed_at %v", m.ClosedAt)
	}
	// Closing records the time.
	closed := setMilestoneStatus(t, s, m.ID, model.MilestoneClosed)
	if closed.Status != model.MilestoneClosed || closed.ClosedAt == nil || *closed.ClosedAt != closed.UpdatedAt {
		t.Fatalf("closed = %+v", closed)
	}
	// Other edits and a repeated close keep the time it was closed.
	renamed, err := s.UpdateMilestone(ctx, trOwner, m.ID, MilestonePatch{Name: str("v1 final")})
	if err != nil || renamed.ClosedAt == nil || *renamed.ClosedAt != *closed.ClosedAt || renamed.Status != model.MilestoneClosed {
		t.Errorf("rename of a closed milestone = %+v, %v", renamed, err)
	}
	again := setMilestoneStatus(t, s, m.ID, model.MilestoneClosed)
	if again.ClosedAt == nil || *again.ClosedAt != *closed.ClosedAt || again.UpdatedAt != renamed.UpdatedAt {
		t.Errorf("closing a closed milestone = %+v", again)
	}
	// Reopening clears it, and closing once more records a new time.
	reopened := setMilestoneStatus(t, s, m.ID, model.MilestoneOpen)
	if reopened.Status != model.MilestoneOpen || reopened.ClosedAt != nil {
		t.Errorf("reopened = %+v", reopened)
	}
	closedAgain := setMilestoneStatus(t, s, m.ID, model.MilestoneClosed)
	if closedAgain.ClosedAt == nil || !closed.ClosedAt.Before(*closedAgain.ClosedAt) {
		t.Errorf("closed again = %+v (first close %v)", closedAgain, closed.ClosedAt)
	}
	// Deleting leaves closed_at as it is: a closed milestone keeps its time.
	gone := setMilestoneStatus(t, s, m.ID, model.MilestoneDeleted)
	if gone.Status != model.MilestoneDeleted || gone.ClosedAt == nil || *gone.ClosedAt != *closedAgain.ClosedAt {
		t.Errorf("deleted after a close = %+v", gone)
	}
	// Coming back open clears it.
	back := setMilestoneStatus(t, s, m.ID, model.MilestoneOpen)
	if back.ClosedAt != nil {
		t.Errorf("restored open = %+v", back)
	}
	// An open milestone that is deleted never had a closing time, and closing it after the restore records one.
	gone = setMilestoneStatus(t, s, m.ID, model.MilestoneDeleted)
	if gone.ClosedAt != nil {
		t.Errorf("deleted while open = %+v", gone)
	}
	if c := setMilestoneStatus(t, s, m.ID, model.MilestoneClosed); c.ClosedAt == nil || *c.ClosedAt != c.UpdatedAt {
		t.Errorf("restored as closed = %+v", c)
	}
}

func TestMilestoneWritesNeedAnActiveProject(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	m := mustMilestone(t, s, "AGB", "v1")
	closed, deleted := model.MilestoneClosed, model.MilestoneDeleted

	for _, status := range []model.ProjectStatus{model.ProjectArchived, model.ProjectDeleted} {
		status := status
		if _, err := s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &status}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateMilestone(ctx, trOwner, "AGB", MilestoneInput{Name: "v2"}); !errors.Is(err, ErrProjectNotActive) {
			t.Errorf("%s project: create err = %v", status, err)
		}
		for name, patch := range map[string]MilestonePatch{
			"rename": {Name: str("v1.1")}, "describe": {Description: str("x")}, "close": {Status: &closed}, "delete": {Status: &deleted},
			"target date": {TargetDate: Optional[string]{Set: true, Value: str("2026-11-01")}},
		} {
			if _, err := s.UpdateMilestone(ctx, trOwner, m.ID, patch); !errors.Is(err, ErrProjectNotActive) {
				t.Errorf("%s project: %s err = %v", status, name, err)
			}
		}
		// A patch that changes nothing is not a write, and reads are open.
		if _, err := s.UpdateMilestone(ctx, trOwner, m.ID, MilestonePatch{Name: str("v1")}); err != nil {
			t.Errorf("%s project: no-op err = %v", status, err)
		}
		if got, err := s.GetMilestone(ctx, m.ID); err != nil || got.Name != "v1" || got.Status != model.MilestoneOpen {
			t.Errorf("%s project: get = %+v, %v", status, got, err)
		}
		if items, err := s.ListMilestones(ctx, "AGB", nil); err != nil || len(items) != 1 {
			t.Errorf("%s project: list = %+v, %v", status, items, err)
		}
	}
	active := model.ProjectActive
	if _, err := s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &active}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateMilestone(ctx, trOwner, m.ID, MilestonePatch{Name: str("v1.1")}); err != nil {
		t.Errorf("update after the project is active again: %v", err)
	}
}

func TestListMilestones(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	mk := func(name string, date *string) *model.Milestone {
		t.Helper()
		m, err := s.CreateMilestone(ctx, trOwner, "AGB", MilestoneInput{Name: name, TargetDate: date})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	mk("late", str("2026-12-01"))
	mk("early", str("2026-11-01"))
	mk("same day", str("2026-11-01"))
	mk("no date", nil)
	mk("no date either", nil)
	mustMilestone(t, s, "OTH", "other project")
	closed := mk("closed one", str("2026-10-01"))
	setMilestoneStatus(t, s, closed.ID, model.MilestoneClosed)
	gone := mk("gone", str("2026-09-01"))
	setMilestoneStatus(t, s, gone.ID, model.MilestoneDeleted)

	names := func(statuses []model.MilestoneStatus) []string {
		t.Helper()
		items, err := s.ListMilestones(ctx, "AGB", statuses)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(items))
		for i, it := range items {
			if it.Project != "AGB" {
				t.Errorf("%s belongs to %s", it.Name, it.Project)
			}
			out[i] = it.Name
		}
		return out
	}
	cases := []struct {
		name     string
		statuses []model.MilestoneStatus
		want     []string
	}{
		{"open", []model.MilestoneStatus{model.MilestoneOpen}, []string{"early", "same day", "late", "no date", "no date either"}},
		{"closed", []model.MilestoneStatus{model.MilestoneClosed}, []string{"closed one"}},
		{"deleted", []model.MilestoneStatus{model.MilestoneDeleted}, []string{"gone"}},
		{"open and closed", []model.MilestoneStatus{model.MilestoneOpen, model.MilestoneClosed}, []string{"closed one", "early", "same day", "late", "no date", "no date either"}},
		{"all", model.MilestoneStatuses, []string{"gone", "closed one", "early", "same day", "late", "no date", "no date either"}},
		{"no filter", nil, []string{"gone", "closed one", "early", "same day", "late", "no date", "no date either"}},
	}
	for _, c := range cases {
		if got := names(c.statuses); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if _, err := s.ListMilestones(ctx, "NOPE", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing project err = %v", err)
	}
	empty, err := s.ListMilestones(ctx, "OTH", []model.MilestoneStatus{model.MilestoneClosed})
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("empty list must be [] not nil: %#v %v", empty, err)
	}
}

func TestMilestoneProgress(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	v1 := mustMilestone(t, s, "AGB", "v1")
	v2 := mustMilestone(t, s, "AGB", "v2")
	progress := func(id int64) model.MilestoneProgress {
		t.Helper()
		m, err := s.GetMilestone(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return m.Progress
	}
	if p := progress(v1.ID); p != (model.MilestoneProgress{}) {
		t.Fatalf("empty milestone progress = %+v", p)
	}

	// Cancelled and deleted issues are not counted at all; done is the part that is finished.
	issues := map[model.IssueStatus]*model.Issue{}
	for _, st := range model.IssueStatuses {
		issues[st] = mustIssue(t, s, ctx, trOwner, IssueInput{Title: string(st), Status: st, Milestone: &v1.ID})
	}
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "in the other milestone", Status: model.StatusDone, Milestone: &v2.ID})
	mustIssue(t, s, ctx, trOwner, IssueInput{Title: "in none", Status: model.StatusDone})
	if p := progress(v1.ID); p != (model.MilestoneProgress{Total: 4, Done: 1}) {
		t.Errorf("progress = %+v, want 4 in total with 1 done", p)
	}
	if p := progress(v2.ID); p != (model.MilestoneProgress{Total: 1, Done: 1}) {
		t.Errorf("other milestone progress = %+v", p)
	}

	// Progress follows the issues.
	done := model.StatusDone
	if _, _, err := s.UpdateIssue(ctx, trOwner, issues[model.StatusDoing].Key, IssuePatch{Status: &done}, nil); err != nil {
		t.Fatal(err)
	}
	if p := progress(v1.ID); p != (model.MilestoneProgress{Total: 4, Done: 2}) {
		t.Errorf("after finishing an issue = %+v", p)
	}
	cancelled := model.StatusCancelled
	if _, _, err := s.UpdateIssue(ctx, trOwner, issues[model.StatusReview].Key, IssuePatch{Status: &cancelled, StatusReason: str("정리")}, nil); err != nil {
		t.Fatal(err)
	}
	if p := progress(v1.ID); p != (model.MilestoneProgress{Total: 3, Done: 2}) {
		t.Errorf("after cancelling an issue = %+v", p)
	}
	if _, _, err := s.UpdateIssue(ctx, trOwner, issues[model.StatusDone].Key, IssuePatch{Milestone: Optional[int64]{Set: true, Value: &v2.ID}}, nil); err != nil {
		t.Fatal(err)
	}
	if a, b := progress(v1.ID), progress(v2.ID); a != (model.MilestoneProgress{Total: 2, Done: 1}) || b != (model.MilestoneProgress{Total: 2, Done: 2}) {
		t.Errorf("after moving a finished issue: %+v and %+v", a, b)
	}
	if _, _, err := s.UpdateIssue(ctx, trOwner, issues[model.StatusBacklog].Key, IssuePatch{Milestone: Optional[int64]{Set: true}}, nil); err != nil {
		t.Fatal(err)
	}
	if p := progress(v1.ID); p != (model.MilestoneProgress{Total: 1, Done: 1}) {
		t.Errorf("after taking an issue out = %+v", p)
	}
	reopened := model.StatusReview
	if _, _, err := s.UpdateIssue(ctx, trOwner, issues[model.StatusCancelled].Key, IssuePatch{Status: &reopened}, nil); err != nil {
		t.Fatal(err)
	}
	if p := progress(v1.ID); p != (model.MilestoneProgress{Total: 2, Done: 1}) {
		t.Errorf("after reviving a cancelled issue = %+v", p)
	}

	// Every read of a milestone carries the progress, including a closed or deleted one.
	setMilestoneStatus(t, s, v1.ID, model.MilestoneClosed)
	gone := setMilestoneStatus(t, s, v1.ID, model.MilestoneDeleted)
	if gone.Progress != (model.MilestoneProgress{Total: 2, Done: 1}) {
		t.Errorf("update result progress = %+v", gone.Progress)
	}
	items, err := s.ListMilestones(ctx, "AGB", model.MilestoneStatuses)
	if err != nil || len(items) != 2 || items[0].Name != "v1" || items[0].Progress != (model.MilestoneProgress{Total: 2, Done: 1}) ||
		items[1].Name != "v2" || items[1].Progress != (model.MilestoneProgress{Total: 2, Done: 2}) {
		t.Errorf("list = %+v, %v", items, err)
	}
}

func TestIssueMilestone(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	v1 := mustMilestone(t, s, "AGB", "v0.2.0")
	v2 := mustMilestone(t, s, "AGB", "v0.3.0")
	foreign := mustMilestone(t, s, "OTH", "다른 프로젝트")
	flow := mustTopic(t, s, "AGB", "이슈 흐름", nil)

	i := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "마일스톤이 붙은 이슈", Milestone: &v1.ID})
	if i.Milestone == nil || *i.Milestone != v1.ID || i.MilestoneName == nil || *i.MilestoneName != "v0.2.0" || i.Version != 1 {
		t.Errorf("created with a milestone = %v %v", i.Milestone, i.MilestoneName)
	}
	plain := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "마일스톤 없음"})
	if plain.Milestone != nil || plain.MilestoneName != nil {
		t.Errorf("no milestone = %v %v", plain.Milestone, plain.MilestoneName)
	}
	// The created record lists the milestone, with null when there is none.
	acts, _, _ := s.ListActivity(ctx, ActivityFilter{Issues: []string{i.Key}, Limit: 1})
	if c, ok := acts[0].Changes["milestone"]; !ok || string(c.To) != strconv.FormatInt(v1.ID, 10) || c.From != nil {
		t.Errorf("created milestone change = %+v (%v)", c, ok)
	}
	acts, _, _ = s.ListActivity(ctx, ActivityFilter{Issues: []string{plain.Key}, Limit: 1})
	if c, ok := acts[0].Changes["milestone"]; !ok || string(c.To) != "null" || c.From != nil {
		t.Errorf("created change without a milestone = %+v (%v)", c, ok)
	}
	// A closed milestone still takes issues.
	closed := mustMilestone(t, s, "AGB", "closed")
	setMilestoneStatus(t, s, closed.ID, model.MilestoneClosed)
	if c := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "to a closed milestone", Milestone: &closed.ID}); c.Milestone == nil || *c.Milestone != closed.ID {
		t.Errorf("issue of a closed milestone = %+v", c)
	}

	// Refusals on create: another project, a missing milestone, a deleted one.
	gone := mustMilestone(t, s, "AGB", "gone")
	setMilestoneStatus(t, s, gone.ID, model.MilestoneDeleted)
	for name, id := range map[string]int64{"other project": foreign.ID, "missing": 9999, "deleted": gone.ID} {
		if _, err := s.CreateIssue(ctx, trOwner, IssueInput{Project: "AGB", Title: "x", Milestone: idp(id)}); !reflect.DeepEqual(violationFields(err), []string{"milestone"}) {
			t.Errorf("create with a %s milestone: err = %v", name, err)
		}
	}
	// A wrong topic and a wrong milestone are both reported.
	if _, err := s.CreateIssue(ctx, trOwner, IssueInput{Project: "AGB", Title: "x", Topic: idp(9999), Milestone: idp(9999)}); !reflect.DeepEqual(violationFields(err), []string{"topic", "milestone"}) {
		t.Errorf("topic and milestone err = %v", err)
	}

	// Update: set, keep, move, clear, with the activity records.
	set, changed, err := s.UpdateIssue(ctx, trClaude, plain.Key, IssuePatch{Milestone: Optional[int64]{Set: true, Value: &v1.ID}}, nil)
	if err != nil || !changed || set.Milestone == nil || *set.Milestone != v1.ID || set.MilestoneName == nil || *set.MilestoneName != "v0.2.0" || set.Version != 2 {
		t.Fatalf("set milestone = %+v changed=%v err=%v", set, changed, err)
	}
	acts, _, _ = s.ListActivity(ctx, ActivityFilter{Issues: []string{plain.Key}, Limit: 1})
	if c := acts[0].Changes["milestone"]; string(c.From) != "null" || string(c.To) != strconv.FormatInt(v1.ID, 10) || len(acts[0].Changes) != 1 || acts[0].Kind != model.ActivityUpdated {
		t.Errorf("milestone change = %+v", acts[0].Changes)
	}
	if _, changed, err := s.UpdateIssue(ctx, trClaude, plain.Key, IssuePatch{Milestone: Optional[int64]{Set: true, Value: &v1.ID}}, nil); err != nil || changed {
		t.Errorf("the same milestone: changed=%v err=%v", changed, err)
	}
	moved, changed, err := s.UpdateIssue(ctx, trClaude, plain.Key, IssuePatch{Milestone: Optional[int64]{Set: true, Value: &v2.ID}}, nil)
	if err != nil || !changed || *moved.Milestone != v2.ID || *moved.MilestoneName != "v0.3.0" {
		t.Fatalf("move = %+v changed=%v err=%v", moved, changed, err)
	}
	acts, _, _ = s.ListActivity(ctx, ActivityFilter{Issues: []string{plain.Key}, Limit: 1})
	if c := acts[0].Changes["milestone"]; string(c.From) != strconv.FormatInt(v1.ID, 10) || string(c.To) != strconv.FormatInt(v2.ID, 10) {
		t.Errorf("move change = %+v", acts[0].Changes)
	}
	for name, id := range map[string]int64{"other project": foreign.ID, "missing": 9999, "deleted": gone.ID} {
		if _, _, err := s.UpdateIssue(ctx, trClaude, plain.Key, IssuePatch{Milestone: Optional[int64]{Set: true, Value: idp(id)}}, nil); !reflect.DeepEqual(violationFields(err), []string{"milestone"}) {
			t.Errorf("update to a %s milestone: err = %v", name, err)
		}
	}
	// A topic and a milestone can change in one request, each in its own change.
	both, changed, err := s.UpdateIssue(ctx, trClaude, plain.Key, IssuePatch{Topic: Optional[int64]{Set: true, Value: &flow.ID}, Milestone: Optional[int64]{Set: true, Value: &v1.ID}}, nil)
	if err != nil || !changed || both.Topic == nil || both.Milestone == nil || *both.Milestone != v1.ID {
		t.Fatalf("topic and milestone together = %+v changed=%v err=%v", both, changed, err)
	}
	acts, _, _ = s.ListActivity(ctx, ActivityFilter{Issues: []string{plain.Key}, Limit: 1})
	if len(acts[0].Changes) != 2 || acts[0].Changes["topic"].To == nil || acts[0].Changes["milestone"].To == nil {
		t.Errorf("combined changes = %+v", acts[0].Changes)
	}
	cleared, changed, err := s.UpdateIssue(ctx, trClaude, plain.Key, IssuePatch{Milestone: Optional[int64]{Set: true}}, nil)
	if err != nil || !changed || cleared.Milestone != nil || cleared.MilestoneName != nil {
		t.Errorf("clear milestone = %+v changed=%v err=%v", cleared, changed, err)
	}

	// An issue keeps pointing at a milestone that is deleted later, and its other edits still work.
	setMilestoneStatus(t, s, v1.ID, model.MilestoneDeleted)
	kept, _, err := s.UpdateIssue(ctx, trClaude, i.Key, IssuePatch{Title: str("제목만 바꿈")}, nil)
	if err != nil || kept.Milestone == nil || *kept.Milestone != v1.ID || kept.MilestoneName == nil || *kept.MilestoneName != "v0.2.0" {
		t.Errorf("issue of a deleted milestone = %+v err=%v", kept, err)
	}
	// Moving off the deleted milestone works; going back to it does not.
	if _, _, err := s.UpdateIssue(ctx, trClaude, i.Key, IssuePatch{Milestone: Optional[int64]{Set: true, Value: &v2.ID}}, nil); err != nil {
		t.Errorf("leave a deleted milestone: %v", err)
	}
	if _, _, err := s.UpdateIssue(ctx, trClaude, i.Key, IssuePatch{Milestone: Optional[int64]{Set: true, Value: &v1.ID}}, nil); !reflect.DeepEqual(violationFields(err), []string{"milestone"}) {
		t.Errorf("return to a deleted milestone err = %v", err)
	}
	// Renaming a milestone shows in the issues that belong to it.
	if _, err := s.UpdateMilestone(ctx, trOwner, v2.ID, MilestonePatch{Name: str("v0.3")}); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.GetIssue(ctx, i.Key); again.MilestoneName == nil || *again.MilestoneName != "v0.3" {
		t.Errorf("name after the rename = %v", again.MilestoneName)
	}
	// Summaries carry the same two fields.
	items, _, err := s.ListIssues(ctx, IssueFilter{Statuses: model.IssueStatuses, Sort: []Sort{{Field: "key"}}, Limit: 10})
	if err != nil || len(items) < 2 || items[0].Milestone == nil || items[0].MilestoneName == nil || *items[0].MilestoneName != "v0.3" || items[1].Milestone != nil || items[1].MilestoneName != nil {
		t.Errorf("summary milestone fields = %+v err=%v", items, err)
	}
	// An archived project takes no change of an issue's milestone.
	archived := model.ProjectArchived
	if _, err := s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &archived}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpdateIssue(ctx, trClaude, plain.Key, IssuePatch{Milestone: Optional[int64]{Set: true, Value: &v2.ID}}, nil); !errors.Is(err, ErrProjectNotActive) {
		t.Errorf("archived project err = %v", err)
	}
}

func TestListIssuesMilestoneFilter(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	v1 := mustMilestone(t, s, "AGB", "v1")
	v2 := mustMilestone(t, s, "AGB", "v2")
	v3 := mustMilestone(t, s, "AGB", "v3")
	on1 := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "a", Milestone: &v1.ID})
	on1b := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "b", Milestone: &v1.ID, Status: model.StatusDone})
	on2 := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "c", Milestone: &v2.ID})
	none := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "d"})
	other := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "e", Project: "OTH"})

	list := func(f IssueFilter) []string {
		t.Helper()
		f.Statuses, f.Sort, f.Limit = model.IssueStatuses, []Sort{{Field: "key"}}, 20
		items, _, err := s.ListIssues(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return keysOf(items, func(i model.IssueSummary) string { return i.Key })
	}
	cases := []struct {
		name string
		f    IssueFilter
		want []*model.Issue
	}{
		{"one milestone", IssueFilter{Milestones: []int64{v1.ID}}, []*model.Issue{on1, on1b}},
		{"several milestones are OR", IssueFilter{Milestones: []int64{v1.ID, v2.ID}}, []*model.Issue{on1, on1b, on2}},
		{"none", IssueFilter{MilestoneNone: true}, []*model.Issue{none, other}},
		{"none and a milestone", IssueFilter{MilestoneNone: true, Milestones: []int64{v2.ID}}, []*model.Issue{on2, none, other}},
		{"a milestone without issues", IssueFilter{Milestones: []int64{v3.ID}}, nil},
		{"unknown milestone", IssueFilter{Milestones: []int64{9999}}, nil},
		{"no filter", IssueFilter{}, []*model.Issue{on1, on1b, on2, none, other}},
	}
	for _, c := range cases {
		var want []string
		for _, i := range c.want {
			want = append(want, i.Key)
		}
		if got := list(c.f); !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("%s: %v, want %v", c.name, got, want)
		}
	}
	// The milestone filter is ANDed with the other filters.
	items, total, err := s.ListIssues(ctx, IssueFilter{Milestones: []int64{v1.ID}, Statuses: []model.IssueStatus{model.StatusDone}, Limit: 20})
	if got := keysOf(items, func(i model.IssueSummary) string { return i.Key }); err != nil || total != 1 || !reflect.DeepEqual(got, []string{on1b.Key}) {
		t.Errorf("milestone and status: %v total %d, %v", got, total, err)
	}
	// The issues of a deleted milestone are still found by its id.
	setMilestoneStatus(t, s, v1.ID, model.MilestoneDeleted)
	if got := list(IssueFilter{Milestones: []int64{v1.ID}}); len(got) != 2 {
		t.Errorf("deleted milestone: %v", got)
	}
}

func TestMilestoneWritesChangeTheFingerprint(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	before, _ := s.FingerprintNow(ctx)
	m := mustMilestone(t, s, "AGB", "v1")
	afterCreate, _ := s.FingerprintNow(ctx)
	if afterCreate == before {
		t.Error("fingerprint unchanged after a milestone was created: a backup would be skipped")
	}
	if again, _ := s.FingerprintNow(ctx); again != afterCreate {
		t.Errorf("fingerprint changed without a write: %q -> %q", afterCreate, again)
	}
	s.UpdateMilestone(ctx, trOwner, m.ID, MilestonePatch{Name: str("v1.0")})
	afterRename, _ := s.FingerprintNow(ctx)
	if afterRename == afterCreate {
		t.Error("fingerprint unchanged after a milestone was renamed")
	}
	closed := model.MilestoneClosed
	s.UpdateMilestone(ctx, trOwner, m.ID, MilestonePatch{Status: &closed})
	afterClose, _ := s.FingerprintNow(ctx)
	if afterClose == afterRename {
		t.Error("fingerprint unchanged after a milestone was closed")
	}
}
