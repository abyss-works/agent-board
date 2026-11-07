package store

import (
	"fmt"
	"slices"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
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
