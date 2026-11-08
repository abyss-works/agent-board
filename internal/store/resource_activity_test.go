package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

// changesOf renders the changes of a record as field -> "from -> to".
func changesOf(a model.ResourceActivity) map[string]string {
	out := map[string]string{}
	for f, c := range a.Changes {
		from := string(c.From)
		if from == "" {
			from = "-"
		}
		out[f] = from + " -> " + string(c.To)
	}
	return out
}

func listResourceActivity(t *testing.T, s *Store, ctx context.Context, f ResourceActivityFilter) []model.ResourceActivity {
	t.Helper()
	f.Limit = 100
	items, total, err := s.ListResourceActivity(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if total != len(items) {
		t.Fatalf("total = %d, items = %d", total, len(items))
	}
	return items
}

func wantChanges(t *testing.T, label string, got map[string]string, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: changes = %v, want %v", label, got, want)
		return
	}
	for f, w := range want {
		if got[f] != w {
			t.Errorf("%s: %s = %q, want %q (all: %v)", label, f, got[f], w, got)
		}
	}
}

func TestProjectWritesAreRecorded(t *testing.T) {
	s, ctx := seeded(t)
	if _, err := s.CreateProject(ctx, trOwner, "AGB", "에이전트 보드"); err != nil {
		t.Fatal(err)
	}
	name := "보드"
	if _, err := s.UpdateProject(ctx, trClaude, "AGB", ProjectPatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	// A patch that changes nothing records nothing.
	if _, err := s.UpdateProject(ctx, trClaude, "AGB", ProjectPatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	deleted := model.ProjectDeleted
	if _, err := s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &deleted}); err != nil {
		t.Fatal(err)
	}

	items := listResourceActivity(t, s, ctx, ResourceActivityFilter{Project: "AGB"})
	if len(items) != 3 {
		t.Fatalf("records = %d, want 3: %+v", len(items), items)
	}
	// Newest first.
	del, ren, cre := items[0], items[1], items[2]
	if cre.Kind != model.ResourceCreated || cre.Resource != model.ResourceProject || cre.Project != "AGB" || cre.Actor != "owner" || cre.SessionID != "s-owner" || cre.ClientHost != "pc" {
		t.Errorf("created = %+v", cre)
	}
	wantChanges(t, "created", changesOf(cre), map[string]string{"name": `null -> "에이전트 보드"`, "status": `null -> "active"`})
	if ren.Kind != model.ResourceUpdated || ren.Actor != "claude" || ren.SessionID != "s-claude" || ren.ClientHost != "laptop" || ren.ResourceID != cre.ResourceID {
		t.Errorf("renamed = %+v", ren)
	}
	wantChanges(t, "renamed", changesOf(ren), map[string]string{"name": `"에이전트 보드" -> "보드"`})
	wantChanges(t, "deleted", changesOf(del), map[string]string{"status": `"active" -> "deleted"`})
}

func TestTopicWritesAreRecorded(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	parent := mustTopic(t, s, "AGB", "대주제", nil)
	child := mustTopic(t, s, "AGB", "소주제", &parent.ID)
	mustTopic(t, s, "OTH", "다른", nil)

	name := "바뀐 이름"
	if _, err := s.UpdateTopic(ctx, trClaude, child.ID, TopicPatch{Name: &name, Parent: Optional[int64]{Set: true, Value: nil}}); err != nil {
		t.Fatal(err)
	}
	deleted := model.TopicDeleted
	if _, err := s.UpdateTopic(ctx, trClaude, child.ID, TopicPatch{Status: &deleted}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateTopic(ctx, trClaude, child.ID, TopicPatch{Status: &deleted}); err != nil {
		t.Fatal(err)
	}

	items := listResourceActivity(t, s, ctx, ResourceActivityFilter{Project: "AGB", Resource: model.ResourceTopic, ResourceID: child.ID})
	if len(items) != 3 {
		t.Fatalf("records = %d, want 3: %+v", len(items), items)
	}
	pid := strconv.FormatInt(parent.ID, 10)
	wantChanges(t, "deleted", changesOf(items[0]), map[string]string{"status": `"active" -> "deleted"`})
	wantChanges(t, "moved", changesOf(items[1]), map[string]string{"name": `"소주제" -> "바뀐 이름"`, "parent": pid + " -> null"})
	wantChanges(t, "created", changesOf(items[2]), map[string]string{"name": `null -> "소주제"`, "parent": "null -> " + pid, "status": `null -> "active"`})
	if items[2].Kind != model.ResourceCreated || items[2].Resource != model.ResourceTopic || items[2].ResourceID != child.ID || items[2].Actor != "owner" {
		t.Errorf("created = %+v", items[2])
	}

	// The project filter keeps the other project out.
	if all := listResourceActivity(t, s, ctx, ResourceActivityFilter{Project: "AGB"}); len(all) != 5 {
		t.Errorf("AGB records = %d, want 5", len(all))
	}
	if oth := listResourceActivity(t, s, ctx, ResourceActivityFilter{Project: "OTH"}); len(oth) != 2 {
		t.Errorf("OTH records = %d, want 2", len(oth))
	}
}

func TestMilestoneWritesAreRecorded(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	date := "2026-11-01"
	m, err := s.CreateMilestone(ctx, trOwner, "AGB", MilestoneInput{Name: "v1", Description: "설명", TargetDate: &date})
	if err != nil {
		t.Fatal(err)
	}
	desc := "새 설명"
	if _, err := s.UpdateMilestone(ctx, trClaude, m.ID, MilestonePatch{Description: &desc, TargetDate: Optional[string]{Set: true}}); err != nil {
		t.Fatal(err)
	}
	closed := setMilestoneStatus(t, s, m.ID, model.MilestoneClosed)
	setMilestoneStatus(t, s, m.ID, model.MilestoneOpen)
	setMilestoneStatus(t, s, m.ID, model.MilestoneDeleted)

	items := listResourceActivity(t, s, ctx, ResourceActivityFilter{Project: "AGB", Resource: model.ResourceMilestone})
	if len(items) != 5 {
		t.Fatalf("records = %d, want 5", len(items))
	}
	closedAt, _ := json.Marshal(closed.ClosedAt)
	wantChanges(t, "deleted", changesOf(items[0]), map[string]string{"status": `"open" -> "deleted"`})
	wantChanges(t, "reopened", changesOf(items[1]), map[string]string{"status": `"closed" -> "open"`, "closed_at": string(closedAt) + " -> null"})
	wantChanges(t, "closed", changesOf(items[2]), map[string]string{"status": `"open" -> "closed"`, "closed_at": "null -> " + string(closedAt)})
	wantChanges(t, "edited", changesOf(items[3]), map[string]string{"description": `"설명" -> "새 설명"`, "target_date": `"2026-11-01" -> null`})
	wantChanges(t, "created", changesOf(items[4]), map[string]string{"name": `null -> "v1"`, "description": `null -> "설명"`, "target_date": `null -> "2026-11-01"`, "status": `null -> "open"`})
	if items[3].Actor != "claude" || items[3].Kind != model.ResourceUpdated || items[4].Kind != model.ResourceCreated {
		t.Errorf("records = %+v %+v", items[3], items[4])
	}
}

func TestRejectedWriteLeavesNoRecord(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustTopic(t, s, "AGB", "주제", nil)
	if _, err := s.CreateTopic(ctx, trOwner, "AGB", TopicInput{Name: "주제"}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.CreateMilestone(ctx, trOwner, "AGB", MilestoneInput{Name: ""}); err == nil {
		t.Fatal("empty milestone name accepted")
	}
	if items := listResourceActivity(t, s, ctx, ResourceActivityFilter{Project: "AGB"}); len(items) != 2 {
		t.Errorf("records = %d, want 2 (project and topic)", len(items))
	}
}

func TestListResourceActivity(t *testing.T) {
	s, ctx := seeded(t)
	if _, _, err := s.ListResourceActivity(ctx, ResourceActivityFilter{Project: "NOPE", Limit: 10}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown project err = %v", err)
	}
	mustProject(t, s, ctx, "AGB")
	mustTopic(t, s, "AGB", "a", nil)
	mustTopic(t, s, "AGB", "b", nil)
	items, total, err := s.ListResourceActivity(ctx, ResourceActivityFilter{Project: "AGB", Limit: 2, Offset: 1})
	if err != nil || total != 3 || len(items) != 2 {
		t.Fatalf("page = %d of %d, %v", len(items), total, err)
	}
	if items[0].Resource != model.ResourceTopic || items[1].Resource != model.ResourceProject {
		t.Errorf("order = %+v", items)
	}
}

func TestMigration18Columns(t *testing.T) {
	s := openTest(t)
	cols := queryStrings(t, s, `select group_concat(name, ',') from pragma_table_info('resource_activity')`)
	if want := "id,project_id,resource,resource_id,kind,actor_id,session_id,client_host,at,changes"; cols[0] != want {
		t.Errorf("columns = %s", cols[0])
	}
	cols = queryStrings(t, s, `select group_concat(name, ',') from pragma_table_info('token')`)
	if want := "id,actor_id,client_host,token_hash,created_at,created_by,last_used_at,revoked_at,revoked_by,revoked_session,revoked_host"; cols[0] != want {
		t.Errorf("token columns = %s", cols[0])
	}
}
