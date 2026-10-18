package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

var (
	trOwner  = model.Trace{Actor: "owner", SessionID: "s-owner", ClientHost: "pc"}
	trClaude = model.Trace{Actor: "claude", SessionID: "s-claude", ClientHost: "laptop"}
)

// seeded opens a store with the two test actors declared and a fixed clock
// that advances one second per call so timestamps are distinct and ordered.
func seeded(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s := openTest(t)
	ctx := context.Background()
	if err := s.EnsureActors(ctx, []model.Actor{{ID: "owner", Kind: model.ActorUser}, {ID: "claude", Kind: model.ActorAgent}}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	n := 0
	s.SetClock(func() time.Time { n++; return base.Add(time.Duration(n) * time.Second) })
	return s, ctx
}

func TestCreateAndGetProject(t *testing.T) {
	s, ctx := seeded(t)
	p, err := s.CreateProject(ctx, trOwner, "AGB", "agent-board")
	if err != nil {
		t.Fatal(err)
	}
	if p.Key != "AGB" || p.Name != "agent-board" || p.Status != model.ProjectActive || p.CreatedBy != "owner" || p.UpdatedBy != "owner" {
		t.Errorf("project = %+v", p)
	}
	if p.CreatedAt.IsZero() || p.CreatedAt != p.UpdatedAt {
		t.Errorf("timestamps = %v %v", p.CreatedAt, p.UpdatedAt)
	}
	got, err := s.GetProject(ctx, "AGB")
	if err != nil || *got != *p {
		t.Errorf("GetProject = %+v, %v; want %+v", got, err, p)
	}
	if _, err := s.GetProject(ctx, "NOPE"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing project err = %v", err)
	}
	if _, err := s.CreateProject(ctx, trOwner, "AGB", "again"); !errors.Is(err, ErrDuplicateKey) {
		t.Errorf("duplicate err = %v", err)
	}
}

func TestListProjects(t *testing.T) {
	s, ctx := seeded(t)
	for _, k := range []string{"BB", "AA", "CC"} {
		if _, err := s.CreateProject(ctx, trOwner, k, k); err != nil {
			t.Fatal(err)
		}
	}
	archived := model.ProjectArchived
	if _, err := s.UpdateProject(ctx, trClaude, "CC", ProjectPatch{Status: &archived}); err != nil {
		t.Fatal(err)
	}
	items, total, err := s.ListProjects(ctx, ProjectFilter{Statuses: []model.ProjectStatus{model.ProjectActive}, Sort: []Sort{{Field: "key"}}, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 || items[0].Key != "AA" || items[1].Key != "BB" {
		t.Errorf("active = %+v (total %d)", items, total)
	}
	items, total, _ = s.ListProjects(ctx, ProjectFilter{Statuses: model.ProjectStatuses, Sort: []Sort{{Field: "created_at", Desc: true}}, Limit: 2})
	if total != 3 || len(items) != 2 || items[0].Key != "CC" {
		t.Errorf("all desc limit 2 = %+v (total %d)", items, total)
	}
	items, _, _ = s.ListProjects(ctx, ProjectFilter{Statuses: model.ProjectStatuses, Sort: []Sort{{Field: "key"}}, Limit: 2, Offset: 2})
	if len(items) != 1 || items[0].Key != "CC" {
		t.Errorf("offset 2 = %+v", items)
	}
}

func TestUpdateProject(t *testing.T) {
	s, ctx := seeded(t)
	p, _ := s.CreateProject(ctx, trOwner, "AGB", "agent-board")
	name := "agent board"
	deleted := model.ProjectDeleted
	got, err := s.UpdateProject(ctx, trClaude, "AGB", ProjectPatch{Name: &name, Status: &deleted})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "agent board" || got.Status != model.ProjectDeleted || got.UpdatedBy != "claude" || !p.UpdatedAt.Before(got.UpdatedAt) || got.CreatedBy != "owner" {
		t.Errorf("updated = %+v", got)
	}
	// Deleted projects stay readable and can be restored.
	again, err := s.GetProject(ctx, "AGB")
	if err != nil || again.Status != model.ProjectDeleted {
		t.Errorf("deleted project read = %+v, %v", again, err)
	}
	active := model.ProjectActive
	if got, err = s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &active}); err != nil || got.Status != model.ProjectActive {
		t.Errorf("restore = %+v, %v", got, err)
	}
	// No-op patch does not touch updated_at.
	before := got.UpdatedAt
	if got, err = s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &active}); err != nil || got.UpdatedAt != before {
		t.Errorf("no-op patch changed updated_at: %v -> %v (%v)", before, got.UpdatedAt, err)
	}
	if _, err := s.UpdateProject(ctx, trOwner, "NOPE", ProjectPatch{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing err = %v", err)
	}
}
