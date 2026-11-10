package store

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/abyss-works/agent-board/internal/model"
)

// TestSourceTextsListsIndexableSources covers the text of each source kind and
// the sources that are left out: created and updated records, deleted issues
// and the comments of deleted issues.
func TestSourceTextsListsIndexableSources(t *testing.T) {
	s, ctx := seeded(t)
	seedDocuments(t, s)
	i := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "잠금 조사", Body: "본문"})
	gone := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "지운 이슈", Status: model.StatusDeleted})
	withSummary, err := s.AddComment(ctx, trClaude, i.Key, "원인은 긴 트랜잭션", str("잠금 원인"), nil)
	if err != nil {
		t.Fatal(err)
	}
	bare, err := s.AddComment(ctx, trClaude, i.Key, "요약 없는 댓글", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddComment(ctx, trClaude, gone.Key, "지운 이슈의 댓글", nil, nil); err != nil {
		t.Fatal(err)
	}

	got, err := s.SourceTexts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{}
	for _, st := range got {
		texts[st.Source+":"+fmt.Sprint(st.ID)] = st.Text
	}
	if n := len(got); n != 1+2+5 || len(texts) != n {
		t.Fatalf("sources = %d (%d distinct): %v", n, len(texts), texts)
	}
	if texts["issue:"+fmt.Sprint(issueRowID(t, s, i.Key))] != "잠금 조사\n본문" {
		t.Errorf("issue text = %q", texts["issue:"+fmt.Sprint(issueRowID(t, s, i.Key))])
	}
	if texts["comment:"+fmt.Sprint(withSummary.ID)] != "잠금 원인\n원인은 긴 트랜잭션" {
		t.Errorf("comment with summary = %q", texts["comment:"+fmt.Sprint(withSummary.ID)])
	}
	if texts["comment:"+fmt.Sprint(bare.ID)] != "요약 없는 댓글" {
		t.Errorf("comment without summary = %q", texts["comment:"+fmt.Sprint(bare.ID)])
	}
	for k := range texts {
		if k == "issue:"+fmt.Sprint(issueRowID(t, s, gone.Key)) {
			t.Errorf("deleted issue is a source")
		}
	}
}

func TestReplaceEmbeddingsKeepsOnlyTheRowsOfTheSource(t *testing.T) {
	s, ctx := seeded(t)
	row := func(id int64, chunk int, model, sha string, v ...float32) EmbeddingRow {
		return EmbeddingRow{Source: "issue", SourceID: id, Chunk: chunk, Model: model, SHA256: sha, Text: "t" + sha, Vector: v}
	}
	if err := s.ReplaceEmbeddings(ctx, "issue", 1, []EmbeddingRow{row(1, 0, "m", "a", 1, 0.5), row(1, 1, "m", "b", 0, -1)}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceEmbeddings(ctx, "issue", 2, []EmbeddingRow{row(2, 0, "other", "c", 1, 1)}); err != nil {
		t.Fatal(err)
	}
	// Replacing source 1 with one chunk drops its second chunk and leaves source 2.
	if err := s.ReplaceEmbeddings(ctx, "issue", 1, []EmbeddingRow{row(1, 0, "m", "z", 0.25, 2)}); err != nil {
		t.Fatal(err)
	}
	meta, err := s.EmbeddingMeta(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 2 || meta[0].SourceID != 1 || meta[0].SHA256 != "z" || meta[0].Model != "m" || meta[1].SourceID != 2 || meta[1].Vector != nil || meta[1].Text != "" {
		t.Fatalf("meta = %+v", meta)
	}
	loaded, err := s.LoadEmbeddings(ctx, "m")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Text != "tz" || !slices.Equal(loaded[0].Vector, []float32{0.25, 2}) {
		t.Fatalf("loaded = %+v", loaded)
	}
	if err := s.ReplaceEmbeddings(ctx, "issue", 1, nil); err != nil {
		t.Fatal(err)
	}
	if meta, _ := s.EmbeddingMeta(ctx); len(meta) != 1 || meta[0].SourceID != 2 {
		t.Errorf("after delete = %+v", meta)
	}
}

// TestSemanticHitsApplyTheSearchFilters resolves sources to hits in the given
// order and drops the ones the filters or the issue state exclude.
func TestSemanticHitsApplyTheSearchFilters(t *testing.T) {
	s, ctx := seeded(t)
	seedDocuments(t, s)
	i := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "잠금 조사"})
	other := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "OTH", Title: "다른 프로젝트"})
	gone := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "지운 이슈", Status: model.StatusDeleted})
	finding := model.CommentFinding
	c, err := s.AddComment(ctx, trClaude, i.Key, "원인", str("요약"), &finding)
	if err != nil {
		t.Fatal(err)
	}
	var docID int64
	if err := s.db.QueryRow(`select id from document where name = ?`, docPriority).Scan(&docID); err != nil {
		t.Fatal(err)
	}
	refs := []SourceText{
		{Source: "document", ID: docID, Text: "문서\n조각"},
		{Source: "issue", ID: issueRowID(t, s, gone.Key), Text: "x"},
		{Source: "comment", ID: c.ID, Text: "댓글 조각"},
		{Source: "issue", ID: issueRowID(t, s, other.Key), Text: "y"},
		{Source: "issue", ID: issueRowID(t, s, i.Key), Text: "z"},
		{Source: "issue", ID: 9999, Text: "missing"},
	}
	hits, err := s.SemanticHits(ctx, refs, SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, h := range hits {
		keys = append(keys, string(h.Type)+":"+h.Key)
	}
	if want := []string{"document:" + docPriority, "comment:" + i.Key, "issue:" + other.Key, "issue:" + i.Key}; !slices.Equal(keys, want) {
		t.Fatalf("hits = %v, want %v", keys, want)
	}
	if h := hits[0]; h.Snippet != "문서 조각" || h.Title == nil || h.Kind == nil || *h.Kind != "decision" || h.At == "" || h.Project != "AGB" {
		t.Errorf("document hit = %+v", h)
	}
	if h := hits[1]; h.CommentID == nil || *h.CommentID != c.ID || *h.Title != "요약" || *h.Kind != "finding" {
		t.Errorf("comment hit = %+v", h)
	}

	hits, err = s.SemanticHits(ctx, refs, SearchFilter{Projects: []string{"AGB"}, Types: []model.SearchType{model.SearchComment, model.SearchIssue}, CommentKinds: []model.CommentKind{model.CommentDecision}})
	if err != nil {
		t.Fatal(err)
	}
	keys = nil
	for _, h := range hits {
		keys = append(keys, string(h.Type)+":"+h.Key)
	}
	if want := []string{"issue:" + i.Key}; !slices.Equal(keys, want) {
		t.Errorf("filtered hits = %v, want %v", keys, want)
	}
}

func issueRowID(t *testing.T, s *Store, key string) int64 {
	t.Helper()
	var id int64
	if err := s.db.QueryRow(`select id from issue where key = ?`, key).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// The status and state filters, and the fields of a hit, are the same for the
// hits found by meaning as for those found by keywords.
func TestSemanticHitsApplyStatusAndState(t *testing.T) {
	s, ctx := seeded(t)
	issues := seedSearchStates(t, s)
	var refs []SourceText
	for _, status := range []model.IssueStatus{model.StatusBacklog, model.StatusDone} {
		refs = append(refs, SourceText{Source: "issue", ID: issueRowID(t, s, issues[status].Key), Text: "x"})
	}
	for _, name := range []string{relOld, relNew, relRep} {
		var id int64
		if err := s.db.QueryRow(`select id from document where name = ?`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, SourceText{Source: "document", ID: id, Text: "문서"})
	}
	hits, err := s.SemanticHits(ctx, refs, SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 5 || hits[0].Status == nil || *hits[0].Status != "backlog" || hits[0].State != nil || hits[0].Supersedes != nil {
		t.Fatalf("hits = %+v", hits)
	}
	old, cur := hits[2], hits[3]
	if old.Status != nil || old.State == nil || *old.State != "superseded" || old.Supersedes == nil || !slices.Equal(old.SupersededBy, []string{relNew}) ||
		*cur.State != "current" || !slices.Equal(cur.Supersedes, []string{relOld}) {
		t.Errorf("document hits = %+v, %+v", old, cur)
	}
	for _, c := range []struct {
		f    SearchFilter
		want []string
	}{
		{SearchFilter{Statuses: []model.IssueStatus{model.StatusDone}}, []string{"issue:" + issues[model.StatusDone].Key, "document:" + relOld, "document:" + relNew, "document:" + relRep}},
		{SearchFilter{States: []model.DocumentState{model.StateCurrent}}, []string{"issue:" + issues[model.StatusBacklog].Key, "issue:" + issues[model.StatusDone].Key, "document:" + relNew}},
		{SearchFilter{States: []model.DocumentState{model.StateArchived}, Statuses: []model.IssueStatus{model.StatusBacklog}}, []string{"issue:" + issues[model.StatusBacklog].Key, "document:" + relRep}},
	} {
		hits, err := s.SemanticHits(ctx, refs, c.f)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, h := range hits {
			keys = append(keys, string(h.Type)+":"+h.Key)
		}
		if !slices.Equal(keys, c.want) {
			t.Errorf("%+v: hits = %v, want %v", c.f, keys, c.want)
		}
	}
}

// TestAllowedSourcesAppliesTheSearchFilters lists the sources the filters let
// through, read from the current rows: a closed issue, a deleted issue, a
// superseded document and another project stay out as the filter says.
func TestAllowedSourcesAppliesTheSearchFilters(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "XYZ")
	mustProject(t, s, ctx, "ABC")
	open := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "XYZ", Title: "open issue"})
	done := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "XYZ", Title: "done issue", Status: model.StatusDone})
	mustIssue(t, s, ctx, trOwner, IssueInput{Project: "XYZ", Title: "deleted issue", Status: model.StatusDeleted})
	other := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "ABC", Title: "other project"})
	finding := model.CommentFinding
	c1, err := s.AddComment(ctx, trClaude, open.Key, "cause", str("summary"), &finding)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := s.AddComment(ctx, trClaude, open.Key, "plain", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	doneComment, err := s.AddComment(ctx, trClaude, done.Key, "closed thread", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	putDoc(t, s, trClaude, "XYZ", relOld, "history", docText("decision", "old decision"))
	putDoc(t, s, trClaude, "XYZ", relNew, "history", docText("decision", "new decision", "supersedes: "+relOld))
	docID := func(name string) int64 {
		var id int64
		if err := s.db.QueryRow(`select id from document where name = ?`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	ref := func(source string, id int64) SourceRef { return SourceRef{source, id} }
	order := func(a, b SourceRef) int { return cmp.Or(strings.Compare(a.Source, b.Source), cmp.Compare(a.ID, b.ID)) }
	allowed := func(f SearchFilter) []SourceRef {
		t.Helper()
		m, err := s.AllowedSources(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []SourceRef
		for r := range m {
			out = append(out, r)
		}
		slices.SortFunc(out, order)
		return out
	}
	oi, di, xi := ref("issue", issueRowID(t, s, open.Key)), ref("issue", issueRowID(t, s, done.Key)), ref("issue", issueRowID(t, s, other.Key))
	cm1, cm2, cmDone := ref("comment", c1.ID), ref("comment", c2.ID), ref("comment", doneComment.ID)
	dOld, dNew := ref("document", docID(relOld)), ref("document", docID(relNew))
	for _, c := range []struct {
		name string
		f    SearchFilter
		want []SourceRef
	}{
		{"no filter", SearchFilter{}, []SourceRef{cm1, cm2, cmDone, dNew, dOld, di, oi, xi}},
		{"project", SearchFilter{Projects: []string{"ABC"}}, []SourceRef{xi}},
		{"type", SearchFilter{Types: []model.SearchType{model.SearchComment}}, []SourceRef{cm1, cm2, cmDone}},
		{"comment kind", SearchFilter{CommentKinds: []model.CommentKind{model.CommentFinding}}, []SourceRef{cm1, dNew, dOld, di, oi, xi}},
		{"status", SearchFilter{Statuses: []model.IssueStatus{model.StatusDone}}, []SourceRef{cmDone, dNew, dOld, di}},
		{"status excludes the comments of closed issues", SearchFilter{Statuses: []model.IssueStatus{model.StatusBacklog}}, []SourceRef{cm1, cm2, dNew, dOld, oi, xi}},
		{"state", SearchFilter{States: []model.DocumentState{model.StateCurrent}}, []SourceRef{cm1, cm2, cmDone, dNew, di, oi, xi}},
	} {
		slices.SortFunc(c.want, order)
		if got := allowed(c.f); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
