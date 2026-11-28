package pg

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

// CompatSearch finds issues, comments and record documents for the words of the
// query, which must all appear, and returns a page of hits with the number of all
// hits. Deleted issues and their comments are left out. Hits whose title or key holds
// every word come first.
func (s *Store) CompatSearch(ctx context.Context, f store.SearchFilter) ([]model.SearchHit, int, error) {
	terms := searchTerms(f.Query)
	if len(terms) == 0 {
		return []model.SearchHit{}, 0, nil
	}
	want := func(t model.SearchType) bool { return len(f.Types) == 0 || slices.Contains(f.Types, t) }
	var args []any
	add := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	likes := make([]string, len(terms))
	for i, t := range terms {
		likes[i] = add("%" + escapeLike(t) + "%")
	}
	all := func(cols ...string) string {
		parts := make([]string, len(likes))
		for i, l := range likes {
			var ors []string
			for _, c := range cols {
				ors = append(ors, c+" ILIKE "+l)
			}
			parts[i] = "(" + strings.Join(ors, " OR ") + ")"
		}
		return strings.Join(parts, " AND ")
	}
	projectCond := func(col string) string {
		if len(f.Projects) == 0 {
			return ""
		}
		return " AND " + col + " = ANY(" + add(f.Projects) + ")"
	}
	statusCond := func() string {
		if len(f.Statuses) == 0 {
			return ""
		}
		vals := make([]string, len(f.Statuses))
		for i, st := range f.Statuses {
			vals[i] = string(st)
		}
		return " AND i.status = ANY(" + add(vals) + ")"
	}
	var parts []string
	if want(model.SearchIssue) {
		parts = append(parts, `SELECT 'issue' AS type, p.key AS project, i.key AS key, NULL::bigint AS comment_id, i.title AS title, i.type AS kind,
			i.status AS status, NULL::text AS state, NULL::bigint AS doc_id, i.updated_at AS at, FALSE AS has_date, i.title || ' ' || i.body AS text,
			(CASE WHEN `+all("i.title", "i.key")+` THEN 0 ELSE 1 END) AS rank
			FROM issue i JOIN project p ON p.id = i.project_id
			WHERE i.status <> 'deleted' AND `+all("i.title", "i.body", "i.key")+projectCond("p.key")+statusCond())
	}
	if want(model.SearchComment) {
		kinds := ""
		if len(f.CommentKinds) > 0 {
			kinds = " AND a.comment_kind = ANY(" + add(strs(f.CommentKinds)) + ")"
		}
		parts = append(parts, `SELECT 'comment', p.key, i.key, a.id, a.summary, a.comment_kind, i.status, NULL::text, NULL::bigint, a.at, FALSE,
			COALESCE(a.summary, '') || ' ' || COALESCE(a.body, ''), (CASE WHEN `+all("a.summary")+` THEN 0 ELSE 1 END)
			FROM activity a JOIN issue i ON i.id = a.issue_id JOIN project p ON p.id = i.project_id
			WHERE a.kind = 'commented' AND i.status <> 'deleted' AND `+all("a.summary", "a.body")+projectCond("p.key")+kinds+statusCond())
	}
	if want(model.SearchDocument) {
		stateCond := ""
		if len(f.States) > 0 {
			stateCond = " AND (" + docStateExpr + ") = ANY(" + add(strs(f.States)) + ")"
		}
		parts = append(parts, `SELECT 'document', p.key, d.name, NULL::bigint, NULLIF(d.title, ''), d.kind, NULL::text, (`+docStateExpr+`), d.id,
			COALESCE(d.recorded_at, d.created_at), d.recorded_at IS NOT NULL, d.title || ' ' || d.content, (CASE WHEN `+all("d.title", "d.name")+` THEN 0 ELSE 1 END)
			FROM record_document d JOIN project p ON p.id = d.project_id
			WHERE `+all("d.name", "d.title", "d.content", "d.tags::text")+projectCond("p.key")+stateCond)
	}
	if len(parts) == 0 {
		return []model.SearchHit{}, 0, nil
	}
	union := strings.Join(parts, " UNION ALL ")
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM (`+union+`) u`, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.pool.Query(ctx, `SELECT * FROM (`+union+`) u(type, project, key, comment_id, title, kind, status, state, doc_id, at, has_date, text, rank) ORDER BY rank, type, key LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	hits := []model.SearchHit{}
	docIDs := map[int]int64{}
	for rows.Next() {
		var h model.SearchHit
		var id, docID *int64
		var title, kind, status, state *string
		var at time.Time
		var hasDate bool
		var text string
		var rank int
		if err := rows.Scan(&h.Type, &h.Project, &h.Key, &id, &title, &kind, &status, &state, &docID, &at, &hasDate, &text, &rank); err != nil {
			return nil, 0, err
		}
		h.CommentID, h.Title, h.Kind, h.Status, h.State = id, title, kind, status, state
		h.At = model.NewTime(at).String()
		if hasDate {
			// A document hit carries the date of the document, not the time it was stored.
			h.At = *formatDocDate(&at)
		}
		h.Snippet = Snippet(text, terms)
		h.Supersedes, h.SupersededBy = []string{}, []string{}
		if docID != nil {
			docIDs[len(hits)] = *docID
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	rows.Close()
	if len(docIDs) > 0 {
		ids := make([]int64, 0, len(docIDs))
		for _, id := range docIDs {
			ids = append(ids, id)
		}
		rels, err := loadRelations(ctx, s.pool, ids)
		if err != nil {
			return nil, 0, err
		}
		for i, id := range docIDs {
			hits[i].Supersedes, hits[i].SupersededBy = rels[id].supersedes, rels[id].supersededBy
		}
	}
	return hits, total, nil
}
