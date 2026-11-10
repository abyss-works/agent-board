package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

const (
	docPriority = "2026-10-20-1544-decision-priority-model.md"
	docLinear   = "2026-10-19-1700-report-linear-gap.md"
	docLock     = "2026-10-21-0900-incident-db-lock.md"
	docRelease  = "2026-10-19-1657-implementation-v020-release.md"
	docBare     = "2026-10-18-1000-decision-no-meta.md"
)

// docText builds a record document: a front matter block of the given kind, a
// default title and the extra lines, then the body. A title among the lines
// comes later and wins.
func docText(kind, body string, lines ...string) string {
	return "---\nkind: " + kind + "\ntitle: 제목\n" + strings.Join(append(lines, ""), "\n") + "---\n\n" + body + "\n"
}

func docSHA(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// locArgs reads a location of a v0.8.x client as the arguments it stands for.
func locArgs(name, location string) model.DocumentArgs {
	var a model.DocumentArgs
	a.ApplyLocation(name, model.DocumentLocation(location))
	return a
}

func putDoc(t *testing.T, s *Store, tr model.Trace, project, name, location, content string) (*model.Document, bool) {
	t.Helper()
	return putDocArgs(t, s, tr, project, name, content, locArgs(name, location))
}

func putDocArgs(t *testing.T, s *Store, tr model.Trace, project, name, content string, args model.DocumentArgs) (*model.Document, bool) {
	t.Helper()
	res, created, err := s.PutDocument(t.Context(), tr, project, name, DocumentInput{Content: content, Args: args})
	if err != nil {
		t.Fatalf("PutDocument(%s, %s): %v", project, name, err)
	}
	if res.Warnings == nil {
		t.Errorf("PutDocument(%s, %s): warnings must be an empty list, not nil", project, name)
	}
	return &res.Document, created
}

func docNames(items []model.Document) []string {
	return keysOf(items, func(d model.Document) string { return d.Name })
}

// seedDocuments stores five documents in two projects: four of AGB (one of
// them in the archive and one without date, title and tags) and one of OTH.
func seedDocuments(t *testing.T, s *Store) {
	t.Helper()
	ctx := t.Context()
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	putDoc(t, s, trClaude, "AGB", docPriority, "history", docText("decision",
		"우선순위 큐는 등급으로 정렬한다. 우선순위 등급은 P0부터 P3까지다. 큐 순서는 서버가 계산한다.",
		"date: 2026-10-20 15:44", "title: 우선순위 큐 모델은 등급과 서버 계산 순서로 한다", "tags: [priority, queue]"))
	putDoc(t, s, trClaude, "AGB", docLinear, "history", docText("report",
		"Linear는 여러 사람이 쓰는 도구다. 우선순위 비교 항목을 남긴다.",
		"date: 2026-10-19 17:00", "title: agb와 Linear 비교", "tags: [product, linear]"))
	putDoc(t, s, trClaude, "AGB", docRelease, "archive", docText("implementation",
		"배포 절차와 마이그레이션 확인.",
		"date: 2026-10-19 16:57", "title: v0.2.0 릴리즈", "tags: [release, migration]"))
	putDoc(t, s, trClaude, "AGB", docBare, "history", docText("decision", "메타데이터가 없는 결정 기록.", "title: 메타 없는 결정"))
	putDoc(t, s, trClaude, "OTH", docLock, "history", docText("incident",
		"쓰기 트랜잭션이 잠겨 요청이 실패했다.",
		"date: 2026-10-21 09:00", "title: 데이터베이스 잠금 사고", "tags: [sqlite, lock]"))
}

func TestPutDocumentStoresTheOriginalBytes(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	// CRLF, trailing spaces, tabs, Korean text and no final newline stay as they are.
	content := docText("decision", "# 본문  \r\n\t들여쓴 줄\r\n\r\n마지막 줄 개행 없음",
		"date: 2026-10-20 15:44", "title: 우선순위 큐 모델은 등급과 서버 계산 순서로 한다", "tags: [priority, queue]")
	content = strings.TrimSuffix(content, "\n")

	d, created := putDoc(t, s, trClaude, "AGB", docPriority, "history", content)
	if !created {
		t.Error("a new document must report created")
	}
	if d.Project != "AGB" || d.Name != docPriority || d.Location != model.LocationHistory || d.Kind != "decision" ||
		d.SHA256 != docSHA(content) || d.Size != len(content) || d.CreatedBy != "claude" || d.UpdatedBy != "claude" {
		t.Errorf("document = %+v", d)
	}
	if d.Date == nil || *d.Date != "2026-10-20 15:44" || d.Title == nil || *d.Title != "우선순위 큐 모델은 등급과 서버 계산 순서로 한다" ||
		!reflect.DeepEqual(d.Tags, []string{"priority", "queue"}) {
		t.Errorf("metadata = date %v, title %v, tags %v", d.Date, d.Title, d.Tags)
	}
	if d.CreatedAt.IsZero() || d.CreatedAt != d.UpdatedAt {
		t.Errorf("timestamps = %v %v", d.CreatedAt, d.UpdatedAt)
	}
	if d.Content != nil || d.Snippet != nil {
		t.Errorf("the put result carries no content or snippet: %+v", d)
	}

	got, err := s.GetDocument(ctx, "AGB", docPriority)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content == nil || *got.Content != content {
		t.Errorf("stored content differs from the uploaded bytes:\n got %q\nwant %q", deref(got.Content), content)
	}
	want := *d
	want.Content = got.Content
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("GetDocument = %+v, want %+v", *got, want)
	}
	if size := len(*got.Content); size != got.Size || docSHA(*got.Content) != got.SHA256 {
		t.Errorf("size %d / sha256 %s do not describe the content", got.Size, got.SHA256)
	}
	if _, err := s.GetDocument(ctx, "AGB", docLinear); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing document err = %v", err)
	}
	if _, err := s.GetDocument(ctx, "NOPE", docPriority); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing project err = %v", err)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestPutDocumentSameContentChangesNothing(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	content := docText("report", "본문", "title: 제목")
	first, _ := putDoc(t, s, trOwner, "AGB", docLinear, "history", content)

	again, created := putDoc(t, s, trClaude, "AGB", docLinear, "history", content)
	if created {
		t.Error("the same document must not report created")
	}
	if !reflect.DeepEqual(again, first) {
		t.Errorf("a repeated put changed the document:\n got %+v\nwant %+v", again, first)
	}
	if items, total, err := s.ListDocuments(ctx, "AGB", 50, 0); err != nil || total != 1 || len(items) != 1 {
		t.Errorf("documents after a repeated put = %d (%d), %v", len(items), total, err)
	}
}

func TestPutDocumentRefusesOtherContent(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	content := docText("decision", "원본", "title: 원본")
	first, _ := putDoc(t, s, trOwner, "AGB", docPriority, "history", content)

	for label, other := range map[string]struct{ location, content string }{
		"other body":                  {"history", docText("decision", "고친 본문", "title: 원본")},
		"a trailing newline more":     {"history", content + "\n"},
		"a byte less":                 {"history", strings.TrimSuffix(content, "\n")},
		"other content and a move":    {"archive", docText("decision", "고친 본문", "title: 원본")},
		"same content, other newline": {"history", strings.ReplaceAll(content, "\n", "\r\n")},
	} {
		_, _, err := s.PutDocument(ctx, trClaude, "AGB", docPriority, DocumentInput{Content: other.content, Args: locArgs(docPriority, other.location)})
		var imm *ImmutableError
		if !errors.As(err, &imm) || imm.SHA256 != first.SHA256 {
			t.Errorf("%s: err = %v, want an ImmutableError carrying %s", label, err, first.SHA256)
		}
	}
	got, _ := s.GetDocument(ctx, "AGB", docPriority)
	if *got.Content != content || got.Location != model.LocationHistory || got.UpdatedBy != "owner" || got.UpdatedAt != first.UpdatedAt {
		t.Errorf("a refused put changed the document: %+v", got)
	}
}

func TestPutDocumentRules(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	ok := docText("decision", "본문", "title: 제목")
	cases := []struct {
		label, name, content string
		want                 []string
	}{
		{"name without the pattern", "priority-model.md", ok, []string{"name"}},
		{"bad kind in the name", "2026-10-20-1544-Plan-x.md", docText("Plan", "본문", "title: 제목"), []string{"name", "kind"}},
		{"upper case slug", "2026-10-20-1544-decision-Priority.md", ok, []string{"name"}},
		{"kind of the front matter differs", "2026-10-20-1544-report-x.md", ok, []string{"kind"}},
		{"no front matter", docPriority, "# 제목\n본문\n", []string{"title"}},
		{"unclosed front matter", docPriority, "---\nkind: decision\n본문\n", []string{"title"}},
		{"empty content", docPriority, "", []string{"title"}},
		{"bytes over the limit", docPriority, docText("decision", strings.Repeat("a", model.MaxDocumentBytes), "title: 제목"), []string{"content"}},
		{"every violation at once", "x.md", "본문", []string{"name", "title"}},
	}
	for _, c := range cases {
		_, _, err := s.PutDocument(ctx, trOwner, "AGB", c.name, DocumentInput{Content: c.content})
		if got := violationFields(err); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: violations = %v (%v), want %v", c.label, got, err, c.want)
		}
	}
	// A document of exactly the limit is accepted.
	head := docText("decision", "", "title: 제목")
	atLimit := head + strings.Repeat("a", model.MaxDocumentBytes-len(head))
	if d, _ := putDoc(t, s, trOwner, "AGB", docPriority, "history", atLimit); d.Size != model.MaxDocumentBytes {
		t.Errorf("size = %d", d.Size)
	}
	if items, total, err := s.ListDocuments(ctx, "AGB", 50, 0); err != nil || total != 1 || len(items) != 1 {
		t.Errorf("refused puts left documents behind: %d (%d), %v", len(items), total, err)
	}
}

func TestPutDocumentNeedsAnActiveProject(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	content := docText("decision", "본문", "title: 제목")
	putDoc(t, s, trOwner, "AGB", docPriority, "history", content)

	if _, _, err := s.PutDocument(ctx, trOwner, "NOPE", docPriority, DocumentInput{Content: content}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing project err = %v", err)
	}
	for _, status := range []model.ProjectStatus{model.ProjectArchived, model.ProjectDeleted} {
		if _, err := s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &status}); err != nil {
			t.Fatal(err)
		}
		// Neither a new document, nor the same one again, nor an invalid one is written;
		// the project state is the first rule that answers.
		for label, put := range map[string]struct{ name, content string }{
			"new":       {docLinear, docText("report", "본문", "title: 제목")},
			"same":      {docPriority, content},
			"invalid":   {"x.md", "본문"},
			"different": {docPriority, docText("decision", "다른 본문", "title: 제목")},
		} {
			if _, _, err := s.PutDocument(ctx, trOwner, "AGB", put.name, DocumentInput{Content: put.content}); !errors.Is(err, ErrProjectNotActive) {
				t.Errorf("%s project, %s document: err = %v, want ErrProjectNotActive", status, label, err)
			}
		}
		// Reading still works.
		if d, err := s.GetDocument(ctx, "AGB", docPriority); err != nil || d.Content == nil {
			t.Errorf("%s project: GetDocument = %+v, %v", status, d, err)
		}
		if _, total, err := s.ListDocuments(ctx, "AGB", 50, 0); err != nil || total != 1 {
			t.Errorf("%s project: ListDocuments total = %d, %v", status, total, err)
		}
	}
	active := model.ProjectActive
	if _, err := s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &active}); err != nil {
		t.Fatal(err)
	}
	if _, created := putDoc(t, s, trOwner, "AGB", docLinear, "history", docText("report", "본문", "title: 제목")); !created {
		t.Error("a document must be accepted again once the project is active")
	}
}

func TestPutDocumentLeavesNoActivity(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	content := docText("decision", "본문", "title: 제목")
	putDoc(t, s, trOwner, "AGB", docPriority, "history", content)
	putDoc(t, s, trOwner, "AGB", docPriority, "history", content)
	for _, table := range []string{"activity", "document_activity"} {
		var n int
		if err := s.db.QueryRow(`select count(*) from ` + table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s records = %d, %v; a put has none", table, n, err)
		}
	}
}

func TestListDocuments(t *testing.T) {
	s, ctx := seeded(t)
	seedDocuments(t, s)

	items, total, err := s.ListDocuments(ctx, "AGB", 50, 0)
	want := []string{docBare, docRelease, docLinear, docPriority}
	if err != nil || total != 4 || !reflect.DeepEqual(docNames(items), want) {
		t.Fatalf("ListDocuments = %v (total %d), %v; want %v by name", docNames(items), total, err, want)
	}
	for _, it := range items {
		if it.Content != nil || it.Snippet != nil || it.Project != "AGB" {
			t.Errorf("a list item has no content or snippet and belongs to the project: %+v", it)
		}
	}
	if items[1].Location != model.LocationArchive || items[1].State != model.StateArchived || !items[1].Archived || items[3].Date == nil || *items[3].Date != "2026-10-20 15:44" {
		t.Errorf("list items = %+v", items)
	}

	page, total, err := s.ListDocuments(ctx, "AGB", 2, 1)
	if err != nil || total != 4 || !reflect.DeepEqual(docNames(page), want[1:3]) {
		t.Errorf("page = %v (total %d), %v; want %v", docNames(page), total, err, want[1:3])
	}
	page, total, _ = s.ListDocuments(ctx, "AGB", 2, 4)
	if total != 4 || page == nil || len(page) != 0 {
		t.Errorf("a page past the end = %#v (total %d); want an empty list", page, total)
	}
	other, total, _ := s.ListDocuments(ctx, "OTH", 50, 0)
	if total != 1 || !reflect.DeepEqual(docNames(other), []string{docLock}) {
		t.Errorf("other project = %v (total %d)", docNames(other), total)
	}
	mustProject(t, s, ctx, "EMP")
	if empty, total, err := s.ListDocuments(ctx, "EMP", 50, 0); err != nil || total != 0 || empty == nil || len(empty) != 0 {
		t.Errorf("project without documents = %#v (total %d), %v; want an empty list", empty, total, err)
	}
	if _, _, err := s.ListDocuments(ctx, "NOPE", 50, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing project err = %v", err)
	}
}

func TestSearchDocumentsWithoutAQueryNewestFirst(t *testing.T) {
	s, ctx := seeded(t)
	seedDocuments(t, s)
	search := func(f DocumentFilter) []string {
		t.Helper()
		f.Limit = 50
		items, _, err := s.SearchDocuments(ctx, f)
		if err != nil {
			t.Fatalf("SearchDocuments(%+v): %v", f, err)
		}
		for _, it := range items {
			if it.Content != nil || it.Snippet != nil {
				t.Errorf("a result without a query has no content or snippet: %+v", it)
			}
		}
		return docNames(items)
	}
	// By date, newest first; the document without a date is last. The release,
	// written one minute before the report, comes after it though its name is older.
	all := []string{docLock, docPriority, docLinear, docRelease, docBare}
	if got := search(DocumentFilter{}); !reflect.DeepEqual(got, all) {
		t.Errorf("all = %v, want %v", got, all)
	}
	if got := search(DocumentFilter{Query: "   "}); !reflect.DeepEqual(got, all) {
		t.Errorf("a blank query = %v, want %v", got, all)
	}
	for label, c := range map[string]struct {
		f    DocumentFilter
		want []string
	}{
		"one project":         {DocumentFilter{Projects: []string{"OTH"}}, []string{docLock}},
		"projects are OR":     {DocumentFilter{Projects: []string{"OTH", "AGB"}}, all},
		"unknown project":     {DocumentFilter{Projects: []string{"NOPE"}}, []string{}},
		"one kind":            {DocumentFilter{Kinds: []model.DocumentKind{"decision"}}, []string{docPriority, docBare}},
		"kinds are OR":        {DocumentFilter{Kinds: []model.DocumentKind{"decision", "report"}}, []string{docPriority, docLinear, docBare}},
		"one state":           {DocumentFilter{States: []model.DocumentState{model.StateArchived}}, []string{docRelease}},
		"states are OR":       {DocumentFilter{States: model.DocumentStates}, all},
		"current":             {DocumentFilter{States: []model.DocumentState{model.StateCurrent}}, []string{docLock, docPriority, docLinear, docBare}},
		"imported only":       {DocumentFilter{Imported: boolPtr(true)}, []string{}},
		"not imported":        {DocumentFilter{Imported: boolPtr(false)}, all},
		"filters are AND":     {DocumentFilter{Projects: []string{"AGB"}, Kinds: []model.DocumentKind{"decision", "report"}, States: []model.DocumentState{model.StateCurrent}}, []string{docPriority, docLinear, docBare}},
		"nothing matches all": {DocumentFilter{Projects: []string{"OTH"}, Kinds: []model.DocumentKind{"decision"}}, []string{}},
	} {
		if got := search(c.f); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", label, got, c.want)
		}
	}

	items, total, err := s.SearchDocuments(ctx, DocumentFilter{Limit: 2, Offset: 1})
	if err != nil || total != 5 || !reflect.DeepEqual(docNames(items), []string{docPriority, docLinear}) {
		t.Errorf("page = %v (total %d), %v", docNames(items), total, err)
	}
	if items, total, _ := s.SearchDocuments(ctx, DocumentFilter{Projects: []string{"NOPE"}, Limit: 5}); total != 0 || items == nil || len(items) != 0 {
		t.Errorf("no match = %#v (total %d); want an empty list", items, total)
	}
}

func TestSearchDocumentsByRelevance(t *testing.T) {
	s, ctx := seeded(t)
	seedDocuments(t, s)
	search := func(f DocumentFilter) ([]model.Document, int) {
		t.Helper()
		f.Limit = 50
		items, total, err := s.SearchDocuments(ctx, f)
		if err != nil {
			t.Fatalf("SearchDocuments(%+v): %v", f, err)
		}
		return items, total
	}

	// The document that is about the words comes before the one that mentions them.
	items, total := search(DocumentFilter{Query: "우선순위"})
	if total != 2 || !reflect.DeepEqual(docNames(items), []string{docPriority, docLinear}) {
		t.Fatalf("우선순위 = %v (total %d), want the decision first", docNames(items), total)
	}
	for _, it := range items {
		if it.Snippet == nil || !strings.Contains(*it.Snippet, "[") || !strings.Contains(*it.Snippet, "]") || !strings.Contains(*it.Snippet, "우선순위") {
			t.Errorf("%s: snippet = %q, want the match inside [ ]", it.Name, deref(it.Snippet))
		}
		if it.Content != nil {
			t.Errorf("%s: a search result carries no content", it.Name)
		}
	}

	for label, c := range map[string]struct {
		f    DocumentFilter
		want []string
	}{
		"a word of the title":               {DocumentFilter{Query: "잠금"}, []string{docLock}},
		"a prefix of a word of the body":    {DocumentFilter{Query: "트랜잭션"}, []string{docLock}},
		"a word of the name":                {DocumentFilter{Query: "priority"}, []string{docPriority}},
		"a word of the name, in the middle": {DocumentFilter{Query: "linear"}, []string{docLinear}},
		"a tag":                             {DocumentFilter{Query: "queue"}, []string{docPriority}},
		"a tag, another document":           {DocumentFilter{Query: "sqlite"}, []string{docLock}},
		"all terms":                         {DocumentFilter{Query: "우선순위 서버"}, []string{docPriority}},
		"a phrase":                          {DocumentFilter{Query: `"우선순위 큐"`}, []string{docPriority}},
		"a phrase that is not there":        {DocumentFilter{Query: `"큐 우선순위"`}, []string{}},
		"no document has it":                {DocumentFilter{Query: "없는검색어"}, []string{}},
		"with a project":                    {DocumentFilter{Query: "우선순위", Projects: []string{"OTH"}}, []string{}},
		"with a kind":                       {DocumentFilter{Query: "우선순위", Kinds: []model.DocumentKind{"report"}}, []string{docLinear}},
		"with a state":                      {DocumentFilter{Query: "배포", States: []model.DocumentState{model.StateArchived}}, []string{docRelease}},
		"with a state that lacks it":        {DocumentFilter{Query: "배포", States: []model.DocumentState{model.StateCurrent}}, []string{}},
		"in every project":                  {DocumentFilter{Query: "기록"}, []string{docBare}},
	} {
		items, total := search(c.f)
		if !reflect.DeepEqual(docNames(items), c.want) || total != len(c.want) {
			t.Errorf("%s: %v (total %d), want %v", label, docNames(items), total, c.want)
		}
	}

	// Text typed by a person never breaks the query syntax.
	for _, q := range []string{`agent-board`, `"`, `""`, `C++`, `a OR b`, `NEAR(`, `*`, `-x`, `title:foo`, `(`, `'`} {
		if _, _, err := s.SearchDocuments(ctx, DocumentFilter{Query: q, Limit: 5}); err != nil {
			t.Errorf("query %q: %v", q, err)
		}
	}
	if _, _, err := s.SearchDocuments(ctx, DocumentFilter{Query: strings.Repeat("a ", model.MaxSearchTerms+1), Limit: 5}); err == nil {
		t.Error("more terms than the issue search allows must be refused")
	}

	items, total = search(DocumentFilter{Query: "우선순위", Projects: []string{"AGB"}, Kinds: []model.DocumentKind{"decision"}})
	if total != 1 || docNames(items)[0] != docPriority {
		t.Errorf("combined = %v (total %d)", docNames(items), total)
	}
	page, total, err := s.SearchDocuments(ctx, DocumentFilter{Query: "우선순위", Limit: 1, Offset: 1})
	if err != nil || total != 2 || !reflect.DeepEqual(docNames(page), []string{docLinear}) {
		t.Errorf("page = %v (total %d), %v", docNames(page), total, err)
	}
}

// The index follows the table: a row inserted, changed or deleted by any path
// leaves the index as the table is.
func TestDocumentIndexFollowsTheTable(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	putDoc(t, s, trOwner, "AGB", docPriority, "history", docText("decision", "betaword", "title: alphaword", "tags: [tagword]"))
	hits := func(q string) int {
		t.Helper()
		_, total, err := s.SearchDocuments(ctx, DocumentFilter{Query: q, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return total
	}
	exec := func(q string) {
		t.Helper()
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, w := range []string{"alphaword", "betaword", "tagword"} {
		if hits(w) != 1 {
			t.Fatalf("%s: inserted row is not found", w)
		}
	}
	exec(`update document set title = 'gammaword'`)
	exec(`update document set tags = '["omegaword"]'`)
	exec(`update document set content = '---
kind: decision
---
deltaword'`)
	for w, want := range map[string]int{"alphaword": 0, "tagword": 0, "betaword": 0, "gammaword": 1, "omegaword": 1, "deltaword": 1} {
		if got := hits(w); got != want {
			t.Errorf("after updates: %s found %d times, want %d", w, got, want)
		}
	}
	exec(`delete from document`)
	for _, w := range []string{"gammaword", "omegaword", "deltaword"} {
		if hits(w) != 0 {
			t.Errorf("after the delete: %s is still found", w)
		}
	}
	if _, err := s.db.Exec(`insert into document_fts(document_fts, rank) values ('integrity-check', 1)`); err != nil {
		t.Errorf("document_fts integrity: %v", err)
	}
}

func TestDocumentWritesChangeTheFingerprint(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	content := docText("decision", "본문", "title: 제목")
	before, _ := s.FingerprintNow(ctx)
	putDoc(t, s, trOwner, "AGB", docPriority, "history", content)
	afterPut, _ := s.FingerprintNow(ctx)
	if afterPut == before {
		t.Error("fingerprint unchanged after a document was stored: a backup would be skipped")
	}
	if again, _ := s.FingerprintNow(ctx); again != afterPut {
		t.Errorf("fingerprint changed without a write: %q -> %q", afterPut, again)
	}
	putDoc(t, s, trOwner, "AGB", docPriority, "history", content)
	if same, _ := s.FingerprintNow(ctx); same != afterPut {
		t.Errorf("fingerprint changed by a put that changed nothing: %q -> %q", afterPut, same)
	}
	if _, err := s.SetDocumentArchived(ctx, trOwner, "AGB", docPriority, true); err != nil {
		t.Fatal(err)
	}
	afterArchive, _ := s.FingerprintNow(ctx)
	if afterArchive == afterPut {
		t.Error("fingerprint unchanged after a document was archived")
	}
	if _, err := s.SetDocumentArchived(ctx, trOwner, "AGB", docPriority, true); err != nil {
		t.Fatal(err)
	}
	if same, _ := s.FingerprintNow(ctx); same != afterArchive {
		t.Errorf("fingerprint changed by an archive mark that was set already: %q -> %q", afterArchive, same)
	}
	putDoc(t, s, trOwner, "AGB", docLinear, "history", docText("report", "본문", "title: 제목"))
	if afterSecond, _ := s.FingerprintNow(ctx); afterSecond == afterArchive {
		t.Error("fingerprint unchanged after a second document was stored")
	}
}

func boolPtr(b bool) *bool { return &b }

func TestEmbeddingWritesChangeTheFingerprint(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	putDoc(t, s, trOwner, "AGB", docPriority, "history", docText("decision", "본문", "title: 제목"))
	putDoc(t, s, trOwner, "AGB", docLinear, "history", docText("report", "본문", "title: 제목"))
	var d1, d2 struct{ ID int64 }
	for name, d := range map[string]*struct{ ID int64 }{docPriority: &d1, docLinear: &d2} {
		if err := s.db.QueryRow(`select id from document where name = ?`, name).Scan(&d.ID); err != nil {
			t.Fatal(err)
		}
	}
	base, _ := s.FingerprintNow(ctx)

	rows := []EmbeddingRow{{Chunk: 0, Model: "m", SHA256: "a", Text: "t", Vector: []float32{1}}}
	if err := s.ReplaceEmbeddings(ctx, SourceDocument, d1.ID, rows); err != nil {
		t.Fatal(err)
	}
	afterEmbed, _ := s.FingerprintNow(ctx)
	if afterEmbed == base {
		t.Error("fingerprint unchanged after an embedding was stored")
	}
	rows[0].SHA256 = "b"
	if err := s.ReplaceEmbeddings(ctx, SourceDocument, d1.ID, rows); err != nil {
		t.Fatal(err)
	}
	if afterReplace, _ := s.FingerprintNow(ctx); afterReplace == afterEmbed {
		t.Error("fingerprint unchanged after an embedding was replaced")
	}
	afterReplace, _ := s.FingerprintNow(ctx)
	if err := s.ReplaceSimilarDocuments(ctx, "m", []SimilarPair{{Document: d1.ID, Similar: d2.ID, Score: 0.9}}); err != nil {
		t.Fatal(err)
	}
	afterSimilar, _ := s.FingerprintNow(ctx)
	if afterSimilar == afterReplace {
		t.Error("fingerprint unchanged after similar documents were stored")
	}
	if err := s.ReplaceSimilarDocuments(ctx, "m", []SimilarPair{{Document: d1.ID, Similar: d2.ID, Score: 0.8}}); err != nil {
		t.Fatal(err)
	}
	if afterScore, _ := s.FingerprintNow(ctx); afterScore == afterSimilar {
		t.Error("fingerprint unchanged after a similarity score changed")
	}
	if err := s.ReplaceEmbeddings(ctx, SourceDocument, d1.ID, nil); err != nil {
		t.Fatal(err)
	}
	if afterRemove, _ := s.FingerprintNow(ctx); afterRemove == afterSimilar {
		t.Error("fingerprint unchanged after an embedding was removed")
	}
}
