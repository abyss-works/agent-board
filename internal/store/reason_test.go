package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

func reasonOf(iss *model.Issue) string {
	if iss.StatusReason == nil {
		return "<nil>"
	}
	return *iss.StatusReason
}

func TestStatusReason(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	i := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "t", Status: model.StatusTodo})
	if i.StatusReason != nil {
		t.Errorf("a new issue has status_reason %q", *i.StatusReason)
	}
	change := func(tr model.Trace, st model.IssueStatus, reason *string) (*model.Issue, error) {
		got, _, err := s.UpdateIssue(ctx, tr, "AGB-1", IssuePatch{Status: &st, StatusReason: reason}, nil)
		return got, err
	}
	activityCount := func() int {
		t.Helper()
		_, total, err := s.ListActivity(ctx, ActivityFilter{Issues: []string{"AGB-1"}, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		return total
	}
	latest := func() map[string]model.Change {
		t.Helper()
		acts, _, err := s.ListActivity(ctx, ActivityFilter{Issues: []string{"AGB-1"}, Kinds: []model.ActivityKind{model.ActivityUpdated}, Limit: 1})
		if err != nil || len(acts) == 0 {
			t.Fatalf("latest activity: %v", err)
		}
		return acts[0].Changes
	}

	// Without a reason an ordinary change records "{from} → {to}".
	doing, err := change(trClaude, model.StatusDoing, nil)
	if err != nil || reasonOf(doing) != "할 일 → 진행" {
		t.Fatalf("default reason = %s, %v", reasonOf(doing), err)
	}
	if rc, ok := latest()["status_reason"]; !ok || string(rc.From) != `null` || string(rc.To) != `"할 일 → 진행"` {
		t.Errorf("activity status_reason = %+v (present %v)", rc, ok)
	}
	// A given reason wins over the default.
	review, err := change(trClaude, model.StatusReview, str("구현을 마쳐 검토를 요청한다"))
	if err != nil || reasonOf(review) != "구현을 마쳐 검토를 요청한다" {
		t.Fatalf("given reason = %s, %v", reasonOf(review), err)
	}
	if rc := latest()["status_reason"]; string(rc.From) != `"할 일 → 진행"` || string(rc.To) != `"구현을 마쳐 검토를 요청한다"` {
		t.Errorf("reason change = %+v", rc)
	}

	// Cancelling and deleting need a reason; a refused request writes nothing.
	before, count := review.Version, activityCount()
	for _, st := range []model.IssueStatus{model.StatusCancelled, model.StatusDeleted} {
		var verr *model.ValidationError
		if _, err := change(trClaude, st, nil); !errors.As(err, &verr) || len(verr.Fields) != 1 || verr.Fields[0].Field != "status_reason" {
			t.Errorf("%s without a reason: err = %v", st, err)
		}
	}
	if cur, _ := s.GetIssue(ctx, "AGB-1"); cur.Version != before || cur.Status != model.StatusReview || activityCount() != count {
		t.Errorf("a refused change wrote something: %+v, %d records (was %d)", cur, activityCount(), count)
	}
	cancelled, err := change(trClaude, model.StatusCancelled, str("AGB-20과 중복"))
	if err != nil || reasonOf(cancelled) != "AGB-20과 중복" || cancelled.Status != model.StatusCancelled || cancelled.ClosedAt == nil {
		t.Fatalf("cancel with reason = %+v, %v", cancelled, err)
	}
	// A status change always lists status_reason, even when the text repeats.
	deleted, err := change(trClaude, model.StatusDeleted, str("AGB-20과 중복"))
	if err != nil || reasonOf(deleted) != "AGB-20과 중복" {
		t.Fatalf("delete with the same reason = %+v, %v", deleted, err)
	}
	if sr, ok := latest()["status_reason"]; !ok || string(sr.From) != `"AGB-20과 중복"` || string(sr.To) != `"AGB-20과 중복"` {
		t.Errorf("a status change must always carry status_reason: %+v", latest())
	}
	// Reviving falls back to the default message.
	todo, err := change(trOwner, model.StatusTodo, nil)
	if err != nil || reasonOf(todo) != "삭제 → 할 일" {
		t.Fatalf("revive = %s, %v", reasonOf(todo), err)
	}

	// The reason belongs to a status change only.
	var verr *model.ValidationError
	same, doingStatus, cancelStatus := model.StatusTodo, model.StatusDoing, model.StatusCancelled
	for name, patch := range map[string]IssuePatch{
		"reason alone":        {StatusReason: str("이유")},
		"reason with a title": {Title: str("x"), StatusReason: str("이유")},
		"reason, same status": {Status: &same, StatusReason: str("이유")},
		"empty reason":        {Status: &doingStatus, StatusReason: str("")},
		"501 characters":      {Status: &doingStatus, StatusReason: str(strings.Repeat("가", 501))},
		"empty on cancel":     {Status: &cancelStatus, StatusReason: str("")},
	} {
		if _, _, err := s.UpdateIssue(ctx, trOwner, "AGB-1", patch, nil); !errors.As(err, &verr) || verr.Fields[0].Field != "status_reason" {
			t.Errorf("%s: err = %v, want a status_reason violation", name, err)
		}
	}
	long, err := change(trOwner, model.StatusDoing, str(strings.Repeat("가", 500)))
	if err != nil || len([]rune(reasonOf(long))) != 500 {
		t.Errorf("500 characters = %v", err)
	}

	// A change that is not a status change leaves the reason as it was, and a
	// no-op writes nothing.
	titled, changed, err := s.UpdateIssue(ctx, trClaude, "AGB-1", IssuePatch{Title: str("새 제목")}, nil)
	if err != nil || !changed || reasonOf(titled) != reasonOf(long) {
		t.Errorf("title change = %s changed=%v err=%v", reasonOf(titled), changed, err)
	}
	if _, changed, err := s.UpdateIssue(ctx, trClaude, "AGB-1", IssuePatch{Status: &titled.Status}, nil); err != nil || changed {
		t.Errorf("same status = changed %v, err %v", changed, err)
	}

	// Problems are reported together with the other field rules.
	empty := ""
	_, _, err = s.UpdateIssue(ctx, trOwner, "AGB-1", IssuePatch{Title: &empty, Status: &cancelStatus}, nil)
	if !errors.As(err, &verr) || len(verr.Fields) != 2 || verr.Fields[0].Field != "title" || verr.Fields[1].Field != "status_reason" {
		t.Errorf("combined violations = %v", err)
	}

	// Creating an issue in any status is not a change: no reason is needed or stored.
	born := mustIssue(t, s, ctx, trOwner, IssueInput{Title: "born cancelled", Status: model.StatusCancelled})
	if born.StatusReason != nil {
		t.Errorf("created cancelled has status_reason %q", *born.StatusReason)
	}
	// Reads return it everywhere an issue appears.
	items, _, err := s.ListIssues(ctx, IssueFilter{Statuses: model.IssueStatuses, Projects: []string{"AGB"}, Limit: 10, Sort: []Sort{{Field: "key"}}})
	if err != nil || len(items) != 2 || items[0].StatusReason == nil || *items[0].StatusReason != reasonOf(long) || items[1].StatusReason != nil {
		t.Errorf("list status_reason = %+v, %v", items, err)
	}
}
