package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

func TestDigest(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "PF")
	mustProject(t, s, ctx, "QUIET")
	before := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "이전 일", Status: model.StatusBacklog})
	since := model.NewTime(before.CreatedAt.Time().Add(time.Millisecond))

	made := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "새 일", Status: model.StatusBacklog})
	doing, done := model.StatusDoing, model.StatusDone
	if _, _, err := s.UpdateIssue(ctx, trOwner, before.Key, IssuePatch{Status: &doing}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpdateIssue(ctx, trOwner, before.Key, IssuePatch{Status: &done}, nil); err != nil {
		t.Fatal(err)
	}
	q := "어느 쪽?"
	if _, _, err := s.UpdateIssue(ctx, trOwner, made.Key, IssuePatch{DecisionNeeded: Optional[string]{Set: true, Value: &q}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddComment(ctx, trOwner, made.Key, "메모", nil, nil); err != nil {
		t.Fatal(err)
	}
	putDoc(t, s, trOwner, "AGB", docPriority, "history", docText("decision", "본문"))
	mustIssue(t, s, ctx, trOwner, IssueInput{Project: "PF", Title: "PF 일"})
	until := s.stamp()
	after := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "창 밖의 일"})

	d, err := s.Digest(ctx, nil, since, &until, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got := keysOf(d.Projects, func(p DigestProject) string { return p.Project.Key }); !reflect.DeepEqual(got, []string{"AGB", "PF"}) {
		t.Fatalf("projects = %v", got)
	}
	agb := d.Projects[0]
	if agb.Activity != 5 || agb.Comments != 1 {
		t.Errorf("activity %d comments %d", agb.Activity, agb.Comments)
	}
	if agb.Created.Total != 1 || agb.Created.Items[0].Issue != made.Key {
		t.Errorf("created = %+v", agb.Created)
	}
	if agb.Status.Total != 2 || *agb.Status.Items[0].To != "doing" || *agb.Status.Items[1].From != "doing" || *agb.Status.Items[1].To != "done" || agb.Status.Items[1].Status != model.StatusDone {
		t.Errorf("status = %+v", agb.Status)
	}
	if agb.Decisions.Total != 1 || *agb.Decisions.Items[0].Question != q {
		t.Errorf("decisions = %+v", agb.Decisions)
	}
	if agb.Documents.Total != 1 || agb.Documents.Items[0].Name != docPriority {
		t.Errorf("documents = %+v", agb.Documents)
	}
	for _, e := range agb.Created.Items {
		if e.Issue == after.Key {
			t.Errorf("an issue after until is in the digest")
		}
	}
	d, err = s.Digest(ctx, []string{"QUIET"}, since, &until, 50)
	if err != nil || len(d.Projects) != 1 || d.Projects[0].Activity != 0 {
		t.Errorf("named quiet project = %+v, %v", d, err)
	}
	d, _ = s.Digest(ctx, []string{"AGB"}, since, &until, 1)
	if len(d.Projects[0].Status.Items) != 1 || d.Projects[0].Status.Total != 2 {
		t.Errorf("limit = %+v", d.Projects[0].Status)
	}
}
