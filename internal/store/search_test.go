package store

import (
	"slices"
	"testing"

	"github.com/abyss-works/agent-board/internal/model"
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

// seedSearchStates stores the word "잠금" in an issue and a comment of each
// status and in a current, a superseded and an archived document.
func seedSearchStates(t *testing.T, s *Store) (issues map[model.IssueStatus]*model.Issue) {
	t.Helper()
	ctx := t.Context()
	mustProject(t, s, ctx, "AGB")
	issues = map[model.IssueStatus]*model.Issue{}
	for _, status := range []model.IssueStatus{model.StatusBacklog, model.StatusDoing, model.StatusReview, model.StatusDone, model.StatusCancelled, model.StatusDeleted} {
		i := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "잠금 " + string(status), Status: status})
		if _, err := s.AddComment(ctx, trClaude, i.Key, "잠금 댓글 "+string(status), nil, nil); err != nil {
			t.Fatal(err)
		}
		issues[status] = i
	}
	putDoc(t, s, trClaude, "AGB", relOld, "history", docText("decision", "잠금 옛 결정"))
	putDoc(t, s, trClaude, "AGB", relNew, "history", docText("decision", "잠금 새 결정", "supersedes: "+relOld))
	putDoc(t, s, trClaude, "AGB", relRep, "archive", docText("report", "잠금 보관 자료"))
	return issues
}

func hitKeys(hits []model.SearchHit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, string(h.Type)+":"+h.Key)
	}
	slices.Sort(out)
	return out
}

func TestSearchFiltersByIssueStatusAndDocumentState(t *testing.T) {
	s, ctx := seeded(t)
	issues := seedSearchStates(t, s)
	search := func(f SearchFilter) []string {
		t.Helper()
		f.Query, f.Limit = "잠금", 50
		hits, total, err := s.Search(ctx, f)
		if err != nil || total != len(hits) {
			t.Fatalf("Search(%+v) = %d hits of %d, %v", f, len(hits), total, err)
		}
		return hitKeys(hits)
	}
	key := func(status model.IssueStatus) []string {
		k := issues[status].Key
		return []string{"comment:" + k, "issue:" + k}
	}
	docs := func(names ...string) []string {
		var out []string
		for _, n := range names {
			out = append(out, "document:"+n)
		}
		return out
	}
	join := func(lists ...[]string) []string {
		var out []string
		for _, l := range lists {
			out = append(out, l...)
		}
		slices.Sort(out)
		return out
	}
	allDocs := docs(relOld, relNew, relRep)
	open := join(key(model.StatusBacklog), key(model.StatusDoing), key(model.StatusReview))

	// No filter: every status but deleted, every state.
	if got, want := search(SearchFilter{}), join(open, key(model.StatusDone), key(model.StatusCancelled), allDocs); !slices.Equal(got, want) {
		t.Errorf("no filter = %v\nwant %v", got, want)
	}
	// Statuses narrow issues and comments, and leave documents alone.
	if got, want := search(SearchFilter{Statuses: []model.IssueStatus{model.StatusBacklog, model.StatusDoing, model.StatusReview}}), join(open, allDocs); !slices.Equal(got, want) {
		t.Errorf("open = %v\nwant %v", got, want)
	}
	if got, want := search(SearchFilter{Statuses: []model.IssueStatus{model.StatusDone, model.StatusCancelled}}), join(key(model.StatusDone), key(model.StatusCancelled), allDocs); !slices.Equal(got, want) {
		t.Errorf("closed = %v\nwant %v", got, want)
	}
	// States narrow documents, and leave issues and comments alone.
	issuesOnly := join(open, key(model.StatusDone), key(model.StatusCancelled))
	if got, want := search(SearchFilter{States: []model.DocumentState{model.StateCurrent}}), join(issuesOnly, docs(relNew)); !slices.Equal(got, want) {
		t.Errorf("current = %v\nwant %v", got, want)
	}
	if got, want := search(SearchFilter{States: []model.DocumentState{model.StateSuperseded, model.StateArchived}}), join(issuesOnly, docs(relOld, relRep)); !slices.Equal(got, want) {
		t.Errorf("superseded and archived = %v\nwant %v", got, want)
	}
	// Both together, and with the other filters.
	if got, want := search(SearchFilter{Statuses: []model.IssueStatus{model.StatusDone}, States: []model.DocumentState{model.StateArchived}}), join(key(model.StatusDone), docs(relRep)); !slices.Equal(got, want) {
		t.Errorf("done and archived = %v\nwant %v", got, want)
	}
	if got, want := search(SearchFilter{Types: []model.SearchType{model.SearchComment}, Statuses: []model.IssueStatus{model.StatusDoing}}), []string{"comment:" + issues[model.StatusDoing].Key}; !slices.Equal(got, want) {
		t.Errorf("comments of doing = %v\nwant %v", got, want)
	}
	// A deleted issue never shows, whatever is asked.
	if got := search(SearchFilter{Statuses: []model.IssueStatus{model.StatusDeleted}}); slices.Contains(got, "issue:"+issues[model.StatusDeleted].Key) {
		t.Errorf("a deleted issue showed: %v", got)
	}
}

func TestSearchHitsCarryStatusStateAndSupersession(t *testing.T) {
	s, ctx := seeded(t)
	issues := seedSearchStates(t, s)
	hits, _, err := s.Search(ctx, SearchFilter{Query: "잠금", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]model.SearchHit{}
	for _, h := range hits {
		byKey[string(h.Type)+":"+h.Key] = h
	}
	str := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}
	for status, i := range issues {
		if status == model.StatusDeleted {
			continue
		}
		for _, typ := range []string{"issue", "comment"} {
			h := byKey[typ+":"+i.Key]
			if str(h.Status) != string(status) || h.State != nil || h.Supersedes != nil || h.SupersededBy != nil {
				t.Errorf("%s of %s: status %s, state %s, supersedes %v, superseded_by %v", typ, status, str(h.Status), str(h.State), h.Supersedes, h.SupersededBy)
			}
		}
	}
	old, cur, arch := byKey["document:"+relOld], byKey["document:"+relNew], byKey["document:"+relRep]
	if old.Status != nil || str(old.State) != "superseded" || old.Supersedes == nil || len(old.Supersedes) != 0 || !slices.Equal(old.SupersededBy, []string{relNew}) {
		t.Errorf("superseded hit = %+v", old)
	}
	if str(cur.State) != "current" || !slices.Equal(cur.Supersedes, []string{relOld}) || cur.SupersededBy == nil || len(cur.SupersededBy) != 0 {
		t.Errorf("current hit = %+v", cur)
	}
	if str(arch.State) != "archived" || arch.Supersedes == nil || arch.SupersededBy == nil {
		t.Errorf("archived hit = %+v", arch)
	}
}
