package store

import (
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

// TestSearchAcrossSources finds one word in issues, comments and documents of
// two projects, and narrows by project, type and comment kind.
func TestSearchAcrossSources(t *testing.T) {
	s, ctx := seeded(t)
	seedDocuments(t, s) // projects AGB and OTH; "우선순위" is in two AGB documents
	i := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "우선순위 큐 재계산", Body: "등급"})
	mustIssue(t, s, ctx, trOwner, IssueInput{Project: "OTH", Title: "관계없는 이슈", Body: "우선순위 없음"})
	gone := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "지운 우선순위", Status: model.StatusDeleted})
	decision := model.CommentDecision
	if _, err := s.AddComment(ctx, trClaude, i.Key, "우선순위는 서버가 정한다", str("큐 결정"), &decision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddComment(ctx, trClaude, i.Key, "우선순위 진행 기록", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddComment(ctx, trClaude, gone.Key, "지운 이슈의 우선순위 댓글", nil, nil); err != nil {
		t.Fatal(err)
	}

	count := func(hits []model.SearchHit) map[model.SearchType]int {
		n := map[model.SearchType]int{}
		for _, h := range hits {
			n[h.Type]++
		}
		return n
	}
	hits, total, err := s.Search(ctx, SearchFilter{Query: "우선순위", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if n := count(hits); total != 6 || n[model.SearchIssue] != 2 || n[model.SearchComment] != 2 || n[model.SearchDocument] != 2 {
		t.Fatalf("all hits = %d %v: %+v", total, n, hits)
	}
	for _, h := range hits {
		if h.Snippet == "" || h.At == "" || h.Project == "" || h.Key == "" {
			t.Errorf("hit lacks fields: %+v", h)
		}
		if (h.Type == model.SearchComment) != (h.CommentID != nil) {
			t.Errorf("comment id on %s: %+v", h.Type, h)
		}
	}

	hits, total, err = s.Search(ctx, SearchFilter{Query: "우선순위", Projects: []string{"AGB"}, Types: []model.SearchType{model.SearchComment}, CommentKinds: []model.CommentKind{model.CommentDecision}, Limit: 50})
	if err != nil || total != 1 || len(hits) != 1 || hits[0].Key != i.Key || hits[0].Title == nil || *hits[0].Title != "큐 결정" || hits[0].Kind == nil || *hits[0].Kind != "decision" {
		t.Errorf("decision comments = %d %+v, %v", total, hits, err)
	}
	if hits, total, err := s.Search(ctx, SearchFilter{Query: "우선순위", Projects: []string{"OTH"}, Limit: 50}); err != nil || total != 1 || hits[0].Type != model.SearchIssue {
		t.Errorf("OTH hits = %d %+v, %v", total, hits, err)
	}
	if hits, total, err := s.Search(ctx, SearchFilter{Query: "우선순위", Limit: 2, Offset: 4}); err != nil || total != 6 || len(hits) != 2 {
		t.Errorf("page = %d of %d, %v", len(hits), total, err)
	}
	if hits, total, err := s.Search(ctx, SearchFilter{Query: "  ", Limit: 50}); err != nil || total != 0 || hits == nil {
		t.Errorf("blank query = %v %d %v", hits, total, err)
	}
}
