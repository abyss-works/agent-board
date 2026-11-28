package graph

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store/pg"
)

func TestNeighborsBidirectionalCyclesAndLatestSuperseding(t *testing.T) {
	st := openTestPGStore(t)
	ctx := context.Background()
	gs := NewStore(st.Pool())

	proj, err := st.CreateProject(ctx, trOwner, pg.CreateProjectInput{Key: "GRF", Name: "Graph Query"})
	if err != nil {
		t.Fatal(err)
	}

	iss, err := st.CreateIssue(ctx, trOwner, proj.Key, pg.CreateIssueInput{
		Title: "Core auth issue", Body: "Auth implementation details", Type: model.TypeFeature, Priority: model.P1,
	})
	if err != nil {
		t.Fatal(err)
	}
	specA, err := st.UpsertSpec(ctx, trOwner, proj.Key, "auth-spec", pg.UpsertSpecInput{
		Title: "Auth Spec", Kind: "spec", Content: "# Auth Spec\nFull specification body for hop expansion.",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec1, err := st.CreateRecord(ctx, trOwner, proj.Key, pg.CreateRecordInput{
		Name: "2026-10-01-1000-decision-auth-v1.md", Kind: "decision", Title: "Auth v1", Content: "Old auth policy v1.",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec2, err := st.CreateRecord(ctx, trOwner, proj.Key, pg.CreateRecordInput{
		Name: "2026-10-02-1000-decision-auth-v2.md", Kind: "decision", Title: "Auth v2", Content: "Mid auth policy v2.",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec3, err := st.CreateRecord(ctx, trOwner, proj.Key, pg.CreateRecordInput{
		Name: "2026-10-03-1000-decision-auth-v3.md", Kind: "decision", Title: "Auth v3", Content: "Latest auth policy v3.",
	})
	if err != nil {
		t.Fatal(err)
	}

	refIss := pg.NodeRef{Type: model.GraphNodeIssue, ID: iss.Issue.ID}
	refSpec := pg.NodeRef{Type: model.GraphNodeSpec, ID: specA.Spec.ID}
	refR1 := pg.NodeRef{Type: model.GraphNodeRecord, ID: rec1.Record.ID}
	refR2 := pg.NodeRef{Type: model.GraphNodeRecord, ID: rec2.Record.ID}
	refR3 := pg.NodeRef{Type: model.GraphNodeRecord, ID: rec3.Record.ID}

	// Build a cyclic related triangle (iss <-> spec <-> rec3 <-> iss) plus inbound/outbound edges.
	for _, edge := range []pg.UpsertEdgeInput{
		{ProjectID: proj.ID, Source: refSpec, Target: &refIss, Kind: model.EdgeKindAbout},
		{ProjectID: proj.ID, Source: refIss, Target: &refR3, Kind: model.EdgeKindRelated},
		{ProjectID: proj.ID, Source: refR3, Target: &refSpec, Kind: model.EdgeKindRelated},
		{ProjectID: proj.ID, Source: refSpec, Target: &refR3, Kind: model.EdgeKindRelated},
		// Supersedes chain: rec3 -> rec2 -> rec1.
		{ProjectID: proj.ID, Source: refR2, Target: &refR1, Kind: model.EdgeKindSupersedes},
		{ProjectID: proj.ID, Source: refR3, Target: &refR2, Kind: model.EdgeKindSupersedes},
	} {
		if _, err := gs.UpsertExplicitEdge(ctx, edge); err != nil {
			t.Fatalf("upsert edge %+v: %v", edge, err)
		}
	}

	// 1-hop neighbors of specA: outbound about -> iss, outbound related -> rec3, inbound related <- rec3.
	start := time.Now()
	ns1, err := gs.Neighbors(ctx, model.GraphNodeSpec, specA.Spec.ID, 1)
	if err != nil {
		t.Fatalf("Neighbors(1): %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("Neighbors(1) took too long: %v", time.Since(start))
	}
	var sawOutboundIss, sawInboundRec3 bool
	for _, n := range ns1 {
		if n.Content != nil {
			t.Fatalf("Neighbors summary unexpectedly populated content: %+v", n)
		}
		if n.Direction == "outbound" && n.NodeType == model.GraphNodeIssue && n.NodeID == iss.Issue.ID {
			sawOutboundIss = true
			if n.KeyOrName != iss.Issue.Key || n.NodeStatus != "backlog" {
				t.Fatalf("unexpected issue neighbor metadata: %+v", n)
			}
		}
		if n.Direction == "inbound" && n.NodeType == model.GraphNodeRecord && n.NodeID == rec3.Record.ID {
			sawInboundRec3 = true
		}
	}
	if !sawOutboundIss || !sawInboundRec3 {
		t.Fatalf("missing bidirectional 1-hop neighbors: %+v", ns1)
	}

	// 2-hop expansion on a cyclic graph must terminate quickly, include hop=2 (rec2), and expand content.
	expanded, err := gs.Expand1Hop(ctx, []pg.NodeRef{refSpec}, pg.ExpandOptions{
		IncludeContent:  true,
		MaxHops:         2,
		MaxContentRunes: 120,
	})
	if err != nil {
		t.Fatalf("Expand1Hop(2): %v", err)
	}
	var sawHop2Rec2 bool
	for _, n := range expanded[refSpec] {
		if n.Content == nil {
			t.Fatalf("expected content populated when IncludeContent=true: %+v", n)
		}
		if n.NodeType == model.GraphNodeRecord && n.NodeID == rec2.Record.ID && n.Hop == 2 {
			sawHop2Rec2 = true
			if !strings.Contains(*n.Content, "Mid auth policy v2") {
				t.Fatalf("unexpected hop-2 content: %q", *n.Content)
			}
		}
	}
	if !sawHop2Rec2 {
		t.Fatalf("expected hop-2 neighbor rec2, got %+v", expanded[refSpec])
	}

	// LatestSuperseding from rec1 must resolve across rec2 to terminal rec3.
	latest, err := gs.LatestSuperseding(ctx, model.GraphNodeRecord, rec1.Record.ID)
	if err != nil {
		t.Fatalf("LatestSuperseding(rec1): %v", err)
	}
	if latest == nil || latest.NodeType != model.GraphNodeRecord || latest.NodeID != rec3.Record.ID || latest.KeyOrName != rec3.Record.Name {
		t.Fatalf("LatestSuperseding(rec1) = %+v, want rec3 (%d)", latest, rec3.Record.ID)
	}
	// Terminal rec3 itself is not superseded.
	none, err := gs.LatestSuperseding(ctx, model.GraphNodeRecord, rec3.Record.ID)
	if err != nil || none != nil {
		t.Fatalf("LatestSuperseding(rec3) = (%+v, %v), want (nil, nil)", none, err)
	}
}

func TestReadyIssuesDeterministicQueueAndDependencies(t *testing.T) {
	st := openTestPGStore(t)
	ctx := context.Background()
	gs := NewStore(st.Pool())

	proj, err := st.CreateProject(ctx, trOwner, pg.CreateProjectInput{Key: "RDY", Name: "Ready Queue"})
	if err != nil {
		t.Fatal(err)
	}
	msPlanned, err := st.CreateMilestone(ctx, trOwner, proj.Key, pg.CreateMilestoneInput{Name: "m-planned", Status: model.MilestonePlanned})
	if err != nil {
		t.Fatal(err)
	}
	msActive, err := st.CreateMilestone(ctx, trOwner, proj.Key, pg.CreateMilestoneInput{Name: "m-active", Status: model.MilestoneActive})
	if err != nil {
		t.Fatal(err)
	}

	// Create issues:
	// - dep: P1 backlog prerequisite
	// - blocked: P0 backlog depending on dep (must be excluded while dep is backlog)
	// - p1Planned: P1 backlog in planned milestone
	// - p1Active: P1 backlog in active milestone (bug so active milestone accepts it)
	// - claimed: P0 backlog claimed by active session (excluded unless IncludeClaimed=true)
	// - doing: P0 doing (must not appear in backlog ready queue)
	dep, err := st.CreateIssue(ctx, trOwner, proj.Key, pg.CreateIssueInput{
		Title: "Prerequisite", Type: model.TypeFeature, Priority: model.P1, MilestoneID: &msPlanned.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := st.CreateIssue(ctx, trOwner, proj.Key, pg.CreateIssueInput{
		Title: "Blocked P0", Type: model.TypeFeature, Priority: model.P0, MilestoneID: &msActive.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	p1Planned, err := st.CreateIssue(ctx, trOwner, proj.Key, pg.CreateIssueInput{
		Title: "P1 planned", Type: model.TypeFeature, Priority: model.P1, MilestoneID: &msPlanned.ID, Labels: []string{"backend"},
	})
	if err != nil {
		t.Fatal(err)
	}
	p1Active, err := st.CreateIssue(ctx, trOwner, proj.Key, pg.CreateIssueInput{
		Title: "P1 active bug", Type: model.TypeBug, Priority: model.P1, MilestoneID: &msActive.ID, Labels: []string{"backend"},
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := st.CreateIssue(ctx, trOwner, proj.Key, pg.CreateIssueInput{
		Title: "Claimed P0", Type: model.TypeFeature, Priority: model.P0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ClaimIssue(ctx, trOwner, claimed.Issue.Key, "working on it"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateIssue(ctx, trOwner, proj.Key, pg.CreateIssueInput{
		Title: "Already doing", Type: model.TypeFeature, Priority: model.P0, Status: model.StatusDoing,
	}); err != nil {
		t.Fatal(err)
	}

	// Wire blocked --depends_on--> dep.
	refBlocked := pg.NodeRef{Type: model.GraphNodeIssue, ID: blocked.Issue.ID}
	refDep := pg.NodeRef{Type: model.GraphNodeIssue, ID: dep.Issue.ID}
	if _, err := gs.UpsertExplicitEdge(ctx, pg.UpsertEdgeInput{
		ProjectID: proj.ID, Source: refBlocked, Target: &refDep, Kind: model.EdgeKindDependsOn,
	}); err != nil {
		t.Fatal(err)
	}

	// Before dep is done: order must be p1Active (active milestone first), dep (older P1), p1Planned.
	items, total, err := gs.ReadyIssues(ctx, proj.ID, pg.ReadyFilter{})
	if err != nil {
		t.Fatalf("ReadyIssues: %v", err)
	}
	if total != 3 || len(items) != 3 {
		t.Fatalf("got total=%d len=%d items=%+v, want 3", total, len(items), items)
	}
	if items[0].Key != p1Active.Issue.Key || items[1].Key != dep.Issue.Key || items[2].Key != p1Planned.Issue.Key {
		t.Fatalf("unexpected deterministic order: [%s, %s, %s]", items[0].Key, items[1].Key, items[2].Key)
	}

	// IncludeClaimed=true exposes claimed P0 first with claimed_by set.
	withClaimed, totalClaimed, err := gs.ReadyIssues(ctx, proj.ID, pg.ReadyFilter{IncludeClaimed: true})
	if err != nil {
		t.Fatal(err)
	}
	if totalClaimed != 4 || withClaimed[0].Key != claimed.Issue.Key || withClaimed[0].ClaimedBy == nil {
		t.Fatalf("IncludeClaimed got total=%d first=%+v", totalClaimed, withClaimed[0])
	}

	// Complete dep -> blocked P0 immediately unblocks at top of queue with unblocked_dependencies=1.
	doneStatus := model.StatusDone
	if _, err := st.UpdateIssue(ctx, trOwner, dep.Issue.Key, nil, pg.IssuePatch{Status: &doneStatus}); err != nil {
		t.Fatal(err)
	}
	afterDone, totalAfter, err := gs.ReadyIssues(ctx, proj.ID, pg.ReadyFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if totalAfter != 3 || len(afterDone) != 3 {
		t.Fatalf("afterDone total=%d items=%+v", totalAfter, afterDone)
	}
	if afterDone[0].Key != blocked.Issue.Key || afterDone[0].UnblockedDependencies != 1 {
		t.Fatalf("expected unblocked %s first with unblocked_dependencies=1, got %+v", blocked.Issue.Key, afterDone[0])
	}

	// Pagination total stays intact when Limit=1.
	page, pageTotal, err := gs.ReadyIssues(ctx, proj.ID, pg.ReadyFilter{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if pageTotal != 3 || len(page) != 1 || page[0].Key != p1Active.Issue.Key {
		t.Fatalf("page=%+v total=%d, want 1 item (%s) with total=3", page, pageTotal, p1Active.Issue.Key)
	}
}
