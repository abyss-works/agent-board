package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
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
// Values of one field are OR. CommentKinds narrows the comments only.
type SearchFilter struct {
	Query        string
	Projects     []string
	Types        []model.SearchType
	CommentKinds []model.CommentKind
	Limit        int
	Offset       int
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
		parts = append(parts, `select 'issue', p.key, i.key, null, i.title, i.type, i.updated_at,
			snippet(issue_fts, -1, '[', ']', '...', 16), issue_fts.rank
			from issue_fts join issue i on i.id = issue_fts.rowid join project p on p.id = i.project_id`+
			projects(` where issue_fts match ? and i.status <> 'deleted'`))
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
		parts = append(parts, `select 'comment', p.key, i.key, a.id, a.summary, a.comment_kind, a.at,
			snippet(comment_fts, -1, '[', ']', '...', 16), comment_fts.rank
			from comment_fts join activity a on a.id = comment_fts.rowid join issue i on i.id = a.issue_id join project p on p.id = i.project_id`+where)
	}
	if want(model.SearchDocument) {
		args = append(args, match)
		parts = append(parts, `select 'document', p.key, d.name, null, d.title, d.kind, coalesce(d.date, d.created_at),
			snippet(document_fts, -1, '[', ']', '...', 16), document_fts.rank
			from document_fts join document d on d.id = document_fts.rowid join project p on p.id = d.project_id`+
			projects(` where document_fts match ?`))
	}
	union := strings.Join(parts, " union all ")
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*) from (`+union+`)`, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `select * from (`+union+`) order by 9, 1, 3 limit ? offset ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	hits := []model.SearchHit{}
	for rows.Next() {
		var h model.SearchHit
		var id sql.NullInt64
		var title, kind sql.NullString
		var rank float64
		if err := rows.Scan(&h.Type, &h.Project, &h.Key, &id, &title, &kind, &h.At, &h.Snippet, &rank); err != nil {
			return nil, 0, err
		}
		if id.Valid {
			h.CommentID = &id.Int64
		}
		if title.Valid {
			h.Title = &title.String
		}
		if kind.Valid {
			h.Kind = &kind.String
		}
		hits = append(hits, h)
	}
	return hits, total, rows.Err()
}
