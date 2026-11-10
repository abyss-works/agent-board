package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/abyss-works/agent-board/internal/model"
)

// BuildMatch turns a user query into an FTS5 MATCH expression. The whole
// input wrapped in double quotes is one phrase; otherwise each whitespace
// separated term becomes a quoted prefix query and terms are ANDed. User
// text is always quoted so FTS5 syntax characters cannot cause errors.
// A blank query yields "" meaning "no search".
func BuildMatch(q string) (string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", nil
	}
	if len(q) >= 2 && strings.HasPrefix(q, `"`) && strings.HasSuffix(q, `"`) {
		inner := strings.TrimSpace(q[1 : len(q)-1])
		if inner == "" {
			return "", nil
		}
		return quote(inner), nil
	}
	terms := strings.Fields(q)
	if len(terms) > model.MaxSearchTerms {
		return "", fmt.Errorf("search query has %d terms, at most %d allowed", len(terms), model.MaxSearchTerms)
	}
	parts := make([]string, len(terms))
	for i, t := range terms {
		parts[i] = quote(t) + " *"
	}
	return strings.Join(parts, " "), nil
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// SearchFilter selects the hits of the unified search. Query is required.
// Values of one field are OR. CommentKinds narrows the comments only. Statuses
// narrows the issues and, by the status of their issue, the comments; States
// narrows the documents. Both select everything when empty, except that
// deleted issues and their comments never show.
type SearchFilter struct {
	Query        string
	Projects     []string
	Types        []model.SearchType
	CommentKinds []model.CommentKind
	Statuses     []model.IssueStatus
	States       []model.DocumentState
	Limit        int
	Offset       int
}

// issueStatusCond is the condition on the status of the issue i that the filter
// asks for, with its arguments, or none.
func (f SearchFilter) issueStatusCond() (string, []any) {
	if len(f.Statuses) == 0 {
		return "", nil
	}
	var args []any
	for _, s := range f.Statuses {
		args = append(args, string(s))
	}
	return " and i.status in (" + placeholders(len(args)) + ")", args
}

// documentStateCond is the condition on the state of the document d.
func (f SearchFilter) documentStateCond() (string, []any) {
	if len(f.States) == 0 {
		return "", nil
	}
	var args []any
	for _, s := range f.States {
		args = append(args, string(s))
	}
	return " and (" + documentState + ") in (" + placeholders(len(args)) + ")", args
}

// fillHitDocuments sets the supersession of the document hits: docIDs maps the
// index of a hit to the row id of its document. One query reads them all.
func fillHitDocuments(ctx context.Context, q querier, hits []model.SearchHit, docIDs map[int]int64) error {
	if len(docIDs) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(docIDs))
	for _, id := range docIDs {
		ids = append(ids, id)
	}
	rels, err := loadRelations(ctx, q, ids)
	if err != nil {
		return err
	}
	for i, id := range docIDs {
		hits[i].Supersedes, hits[i].SupersededBy = rels[id].supersedes, rels[id].supersededBy
	}
	return nil
}

// Search finds issues, comments and record documents with the query rules of
// the issue search, best match first, and returns a page of hits with the
// number of all hits. Deleted issues and their comments are left out.
//
// ponytail: the three indexes rank with bm25 each, and the ranks are merged as
// they are. The scales are close but not equal; normalize per index if mixed
// results order badly.
func (s *Store) Search(ctx context.Context, f SearchFilter) ([]model.SearchHit, int, error) {
	match, err := BuildMatch(f.Query)
	if err != nil {
		return nil, 0, err
	}
	if match == "" {
		return []model.SearchHit{}, 0, nil
	}
	want := func(t model.SearchType) bool { return len(f.Types) == 0 || slices.Contains(f.Types, t) }
	var parts []string
	var args []any
	projects := func(where string) string {
		if len(f.Projects) == 0 {
			return where
		}
		for _, p := range f.Projects {
			args = append(args, p)
		}
		return where + " and p.key in (" + placeholders(len(f.Projects)) + ")"
	}
	if want(model.SearchIssue) {
		args = append(args, match)
		where := projects(` where issue_fts match ? and i.status <> 'deleted'`)
		cond, condArgs := f.issueStatusCond()
		args = append(args, condArgs...)
		parts = append(parts, `select 'issue', p.key, i.key, null, i.title, i.type, i.status, null, null, i.updated_at,
			snippet(issue_fts, -1, '[', ']', '...', 16), issue_fts.rank
			from issue_fts join issue i on i.id = issue_fts.rowid join project p on p.id = i.project_id`+where+cond)
	}
	if want(model.SearchComment) {
		args = append(args, match)
		where := projects(` where comment_fts match ? and i.status <> 'deleted'`)
		if len(f.CommentKinds) > 0 {
			where += " and a.comment_kind in (" + placeholders(len(f.CommentKinds)) + ")"
			for _, k := range f.CommentKinds {
				args = append(args, string(k))
			}
		}
		cond, condArgs := f.issueStatusCond()
		args = append(args, condArgs...)
		parts = append(parts, `select 'comment', p.key, i.key, a.id, a.summary, a.comment_kind, i.status, null, null, a.at,
			snippet(comment_fts, -1, '[', ']', '...', 16), comment_fts.rank
			from comment_fts join activity a on a.id = comment_fts.rowid join issue i on i.id = a.issue_id join project p on p.id = i.project_id`+where+cond)
	}
	if want(model.SearchDocument) {
		args = append(args, match)
		where := projects(` where document_fts match ?`)
		cond, condArgs := f.documentStateCond()
		args = append(args, condArgs...)
		parts = append(parts, `select 'document', p.key, d.name, null, d.title, d.kind, null, (`+documentState+`), d.id, coalesce(d.date, d.created_at),
			snippet(document_fts, -1, '[', ']', '...', 16), document_fts.rank
			from document_fts join document d on d.id = document_fts.rowid join project p on p.id = d.project_id`+where+cond)
	}
	union := strings.Join(parts, " union all ")
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*) from (`+union+`)`, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `select * from (`+union+`) order by 12, 1, 3 limit ? offset ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	hits := []model.SearchHit{}
	docIDs := map[int]int64{}
	for rows.Next() {
		var h model.SearchHit
		var id, docID sql.NullInt64
		var title, kind, status, state sql.NullString
		var rank float64
		if err := rows.Scan(&h.Type, &h.Project, &h.Key, &id, &title, &kind, &status, &state, &docID, &h.At, &h.Snippet, &rank); err != nil {
			return nil, 0, err
		}
		if id.Valid {
			h.CommentID = &id.Int64
		}
		h.Title, h.Kind, h.Status, h.State = nullable(title), nullable(kind), nullable(status), nullable(state)
		if docID.Valid {
			docIDs[len(hits)] = docID.Int64
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	rows.Close()
	if err := fillHitDocuments(ctx, s.db, hits, docIDs); err != nil {
		return nil, 0, err
	}
	return hits, total, nil
}
