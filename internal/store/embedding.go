package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"math"
	"slices"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// Embedding sources, the values of embedding.source.
const (
	SourceIssue    = "issue"
	SourceComment  = "comment"
	SourceDocument = "document"
)

// SourceText is the text of one searchable source. Source is one of the
// Source* constants and ID the row id of the issue, the activity record or the
// document. As the result of a semantic query Text is the best matching chunk.
type SourceText struct {
	Source string
	ID     int64
	Text   string
}

// EmbeddingRow is one stored chunk of a source. EmbeddingMeta leaves Text and
// Vector empty.
type EmbeddingRow struct {
	Source   string
	SourceID int64
	Chunk    int
	Model    string
	SHA256   string
	Text     string
	Vector   []float32
}

// SourceTexts returns the text of every source that is searchable by meaning:
// the title and body of each issue that is not deleted, the summary and body of
// each comment of such an issue, and the content of each record document.
func (s *Store) SourceTexts(ctx context.Context) ([]SourceText, error) {
	var out []SourceText
	for _, q := range []struct{ source, query string }{
		{SourceIssue, `select id, title || char(10) || body from issue where status <> 'deleted' order by id`},
		{SourceComment, `select a.id, coalesce(a.summary, '') || case when coalesce(a.summary, '') = '' then '' else char(10) end || coalesce(a.body, '')
			from activity a join issue i on i.id = a.issue_id where a.kind = 'commented' and i.status <> 'deleted' order by a.id`},
		{SourceDocument, `select id, content from document order by id`},
	} {
		rows, err := s.db.QueryContext(ctx, q.query)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			st := SourceText{Source: q.source}
			if err := rows.Scan(&st.ID, &st.Text); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, st)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// EmbeddingMeta returns every stored chunk without its text and vector, in
// order of source, id and chunk.
func (s *Store) EmbeddingMeta(ctx context.Context) ([]EmbeddingRow, error) {
	rows, err := s.db.QueryContext(ctx, `select source, source_id, chunk, model, text_sha256 from embedding order by source, source_id, chunk`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EmbeddingRow
	for rows.Next() {
		var r EmbeddingRow
		if err := rows.Scan(&r.Source, &r.SourceID, &r.Chunk, &r.Model, &r.SHA256); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LoadEmbeddings returns every chunk stored for the model with its text and
// vector.
func (s *Store) LoadEmbeddings(ctx context.Context, model string) ([]EmbeddingRow, error) {
	rows, err := s.db.QueryContext(ctx, `select source, source_id, chunk, model, text_sha256, text, vector from embedding
		where model = ? order by source, source_id, chunk`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EmbeddingRow
	for rows.Next() {
		var r EmbeddingRow
		var blob []byte
		if err := rows.Scan(&r.Source, &r.SourceID, &r.Chunk, &r.Model, &r.SHA256, &r.Text, &blob); err != nil {
			return nil, err
		}
		r.Vector = decodeVector(blob)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReplaceEmbeddings makes rows the complete set of chunks stored for the
// source in one transaction. No rows removes the source.
func (s *Store) ReplaceEmbeddings(ctx context.Context, source string, id int64, rows []EmbeddingRow) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `delete from embedding where source = ? and source_id = ?`, source, id); err != nil {
			return err
		}
		now := s.stamp().String()
		for _, r := range rows {
			if _, err := tx.ExecContext(ctx, `insert into embedding(source, source_id, chunk, model, text_sha256, text, vector, created_at)
				values (?, ?, ?, ?, ?, ?, ?, ?)`, source, id, r.Chunk, r.Model, r.SHA256, r.Text, encodeVector(r.Vector), now); err != nil {
				return err
			}
		}
		return nil
	})
}

func encodeVector(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

func decodeVector(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}

// maxSemanticSnippet is the length in runes of the snippet of a hit found by
// meaning.
const maxSemanticSnippet = 160

// SemanticHits resolves the sources found by meaning to search hits, in the
// order of refs, with the projects, types and comment kinds of f. A source
// that does not exist, belongs to a deleted issue or is excluded by f yields
// no hit. The snippet is the chunk text of the ref with its whitespace
// collapsed and no match marks.
func (s *Store) SemanticHits(ctx context.Context, refs []SourceText, f SearchFilter) ([]model.SearchHit, error) {
	found := map[SourceText]model.SearchHit{}
	for _, source := range []string{SourceIssue, SourceComment, SourceDocument} {
		typ := model.SearchType(source)
		if len(f.Types) > 0 && !slices.Contains(f.Types, typ) {
			continue
		}
		var args []any
		for _, r := range refs {
			if r.Source == source {
				args = append(args, r.ID)
			}
		}
		if len(args) == 0 {
			continue
		}
		in := placeholders(len(args))
		var q string
		switch source {
		case SourceIssue:
			q = `select i.id, p.key, i.key, null, i.title, i.type, i.updated_at from issue i join project p on p.id = i.project_id
				where i.status <> 'deleted' and i.id in (` + in + `)`
		case SourceComment:
			q = `select a.id, p.key, i.key, a.id, a.summary, a.comment_kind, a.at from activity a join issue i on i.id = a.issue_id join project p on p.id = i.project_id
				where a.kind = 'commented' and i.status <> 'deleted' and a.id in (` + in + `)`
			if len(f.CommentKinds) > 0 {
				q += " and a.comment_kind in (" + placeholders(len(f.CommentKinds)) + ")"
				for _, k := range f.CommentKinds {
					args = append(args, string(k))
				}
			}
		case SourceDocument:
			q = `select d.id, p.key, d.name, null, d.title, d.kind, coalesce(d.date, d.created_at) from document d join project p on p.id = d.project_id
				where d.id in (` + in + `)`
		}
		if len(f.Projects) > 0 {
			q += " and p.key in (" + placeholders(len(f.Projects)) + ")"
			for _, p := range f.Projects {
				args = append(args, p)
			}
		}
		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			h := model.SearchHit{Type: typ}
			var commentID sql.NullInt64
			var title, kind sql.NullString
			if err := rows.Scan(&id, &h.Project, &h.Key, &commentID, &title, &kind, &h.At); err != nil {
				rows.Close()
				return nil, err
			}
			if commentID.Valid {
				h.CommentID = &commentID.Int64
			}
			if title.Valid {
				h.Title = &title.String
			}
			if kind.Valid {
				h.Kind = &kind.String
			}
			found[SourceText{Source: source, ID: id}] = h
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	hits := []model.SearchHit{}
	for _, r := range refs {
		h, ok := found[SourceText{Source: r.Source, ID: r.ID}]
		if !ok {
			continue
		}
		h.Snippet = collapse(r.Text, maxSemanticSnippet)
		hits = append(hits, h)
	}
	return hits, nil
}

// collapse joins the whitespace separated words of s with single spaces and
// cuts the result to at most n runes.
func collapse(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
