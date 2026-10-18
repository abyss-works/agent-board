package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMilestoneStatuses(t *testing.T) {
	want := []MilestoneStatus{MilestoneOpen, MilestoneClosed, MilestoneDeleted}
	if !reflect.DeepEqual(MilestoneStatuses, want) {
		t.Fatalf("MilestoneStatuses = %v, want %v", MilestoneStatuses, want)
	}
	for _, v := range []string{"open", "closed", "deleted"} {
		if got, ok := ParseMilestoneStatus(v); !ok || string(got) != v {
			t.Errorf("ParseMilestoneStatus(%q) = %q, %v", v, got, ok)
		}
	}
	for _, v := range []string{"", "all", "active", "Open", "done"} {
		if _, ok := ParseMilestoneStatus(v); ok {
			t.Errorf("ParseMilestoneStatus(%q) must fail: all is an alias, not a status", v)
		}
	}
}

func TestExpandMilestoneStatuses(t *testing.T) {
	cases := []struct {
		in   string
		want []MilestoneStatus
	}{
		{"open", []MilestoneStatus{MilestoneOpen}},
		{"closed", []MilestoneStatus{MilestoneClosed}},
		{"deleted", []MilestoneStatus{MilestoneDeleted}},
		{"open,closed", []MilestoneStatus{MilestoneOpen, MilestoneClosed}},
		{"all", []MilestoneStatus{MilestoneOpen, MilestoneClosed, MilestoneDeleted}},
		{"deleted,all", []MilestoneStatus{MilestoneDeleted, MilestoneOpen, MilestoneClosed}},
		{"open,open,closed,open", []MilestoneStatus{MilestoneOpen, MilestoneClosed}},
	}
	for _, c := range cases {
		got, err := ExpandMilestoneStatuses(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("ExpandMilestoneStatuses(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{"", ",", "open,", "bogus", "open,bogus", "OPEN", "active"} {
		if got, err := ExpandMilestoneStatuses(in); err == nil {
			t.Errorf("ExpandMilestoneStatuses(%q) = %v, want an error", in, got)
		}
	}
}

func TestValidDate(t *testing.T) {
	for _, v := range []string{"2026-11-01", "2024-02-29", "2026-12-31", "0001-01-01"} {
		if !ValidDate(v) {
			t.Errorf("%q should be a valid date", v)
		}
	}
	for _, v := range []string{
		"", "2026-13-01", "2026-00-10", "2026-11-00", "2026-11-31", "2026-02-30", "2026-02-29",
		"26-11-01", "2026-1-1", "2026-11-1", "2026/11/01", "20261101", "2026-11-01T00:00:00Z", "2026-11-01 ", " 2026-11-01", "내일", "+2026-11-01",
	} {
		if ValidDate(v) {
			t.Errorf("%q should not be a valid date", v)
		}
	}
}

func TestValidateMilestone(t *testing.T) {
	text := func(s string) *string { return &s }
	cases := []struct {
		name        string
		milestone   string
		description string
		date        *string
		want        []string // field of each violation, in order
	}{
		{"minimal", "v1", "", nil, nil},
		{"every field", "v0.2.0", "운영 안정화와 릴리즈 관리", text("2026-11-01"), nil},
		{"name of 50 characters", strings.Repeat("가", 50), "", nil, nil},
		{"description of 2000 characters", "v1", strings.Repeat("가", 2000), nil, nil},
		{"empty name", "", "", nil, []string{"name"}},
		{"name of 51 characters", strings.Repeat("가", 51), "", nil, []string{"name"}},
		{"description of 2001 characters", "v1", strings.Repeat("가", 2001), nil, []string{"description"}},
		{"empty date", "v1", "", text(""), []string{"target_date"}},
		{"malformed date", "v1", "", text("2026-1-1"), []string{"target_date"}},
		{"impossible date", "v1", "", text("2026-02-30"), []string{"target_date"}},
		{"every violation at once", "", strings.Repeat("a", 2001), text("soon"), []string{"name", "description", "target_date"}},
	}
	for _, c := range cases {
		err := ValidateMilestone(c.milestone, c.description, c.date)
		var got []string
		if err != nil {
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Errorf("%s: err = %v, want a *ValidationError", c.name, err)
				continue
			}
			for _, f := range ve.Fields {
				got = append(got, f.Field)
			}
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: violations = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIssueCarriesItsMilestone(t *testing.T) {
	id, name := int64(2), "v0.2.0"
	i := Issue{Key: "AGB-1", Milestone: &id, MilestoneName: &name}
	s := i.Summary()
	if s.Milestone == nil || *s.Milestone != 2 || s.MilestoneName == nil || *s.MilestoneName != "v0.2.0" {
		t.Errorf("summary = %+v", s)
	}
	b, err := json.Marshal(Issue{Key: "AGB-2"})
	if err != nil || !strings.Contains(string(b), `"milestone":null,"milestone_name":null`) {
		t.Errorf("an issue without a milestone must show both fields as null: %s (%v)", b, err)
	}
	b, _ = json.Marshal(i)
	if !strings.Contains(string(b), `"milestone":2,"milestone_name":"v0.2.0"`) {
		t.Errorf("issue JSON = %s", b)
	}
}

func TestMilestoneJSON(t *testing.T) {
	date := "2026-11-01"
	at, err := ParseTime("2026-10-18T09:00:00.000Z")
	if err != nil {
		t.Fatal(err)
	}
	m := Milestone{
		ID: 2, Project: "AGB", Name: "v0.2.0", Description: "운영 안정화와 릴리즈 관리", TargetDate: &date,
		Status: MilestoneOpen, Progress: MilestoneProgress{Total: 5, Done: 2},
		CreatedAt: at, UpdatedAt: at, CreatedBy: "owner", UpdatedBy: "owner",
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":2,"project":"AGB","name":"v0.2.0","description":"운영 안정화와 릴리즈 관리","target_date":"2026-11-01","status":"open",` +
		`"progress":{"total":5,"done":2},"closed_at":null,"created_at":"2026-10-18T09:00:00.000Z","updated_at":"2026-10-18T09:00:00.000Z",` +
		`"created_by":"owner","updated_by":"owner"}`
	if string(b) != want {
		t.Errorf("milestone JSON =\n%s\nwant\n%s", b, want)
	}
}
