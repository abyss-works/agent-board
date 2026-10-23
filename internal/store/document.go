package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// ImmutableError reports other content for the name of a record document that
// exists. SHA256 is the hash of the content that is stored. HTTP maps it to 409.
type ImmutableError struct {
	SHA256 string
}

func (e *ImmutableError) Error() string {
	return "record document exists with other content (sha256 " + e.SHA256 + ")"
}

// DocumentInput is a put request: the folder the document lies in and its
// content, which is stored byte for byte.
type DocumentInput struct {
	Location string
	Content  string
}

// DocumentFilter selects and searches record documents. Values of one field are
// OR. With a Query the result is in order of relevance and carries snippets;
// without one it is newest first by the date of the front matter.
type DocumentFilter struct {
	Query     string
	Projects  []string
	Kinds     []model.DocumentKind
	Locations []model.DocumentLocation
	Limit     int
	Offset    int
}

// documentCols are the columns of a document without its content.
const documentCols = `p.key, d.name, d.location, d.kind, d.date, d.title, d.tags, d.sha256, d.size,
	d.created_at, d.created_by, d.updated_at, d.updated_by`

const documentFrom = ` from document d join project p on p.id = d.project_id`

// scanDocument scans the columns of documentCols followed by the extra
// destinations of columns a query appended.
func scanDocument(sc rowScanner, extra ...any) (*model.Document, error) {
	var d model.Document
	var date, title sql.NullString
	var tags, created, updated string
	dest := []any{&d.Project, &d.Name, &d.Location, &d.Kind, &date, &title, &tags, &d.SHA256, &d.Size,
		&created, &d.CreatedBy, &updated, &d.UpdatedBy}
	if err := sc.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	if date.Valid {
		d.Date = &date.String
	}
	if title.Valid {
		d.Title = &title.String
	}
	if err := json.Unmarshal([]byte(tags), &d.Tags); err != nil {
		return nil, err
	}
	if d.Tags == nil {
		d.Tags = []string{}
	}
	var err error
	if d.CreatedAt, err = model.ParseTime(created); err != nil {
		return nil, err
	}
	if d.UpdatedAt, err = model.ParseTime(updated); err != nil {
		return nil, err
	}
	return &d, nil
}

// marshalTags writes the tags as a JSON array. The index reads the text, so
// characters such as < and & stay as they are.
func marshalTags(tags []string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(tags); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// PutDocument stores a record document. A document is never changed: the same
// content again changes nothing, except that a different location moves the
// document, and other content under the same name is refused with an
// *ImmutableError. created reports a document that is new. A document has no
// activity record; created_by and updated_by name the actor. The result
// carries no content.
func (s *Store) PutDocument(ctx context.Context, tr model.Trace, project, name string, in DocumentInput) (doc *model.Document, created bool, err error) {
	meta, invalid := model.ValidateDocument(name, in.Location, in.Content)
	sum := sha256.Sum256([]byte(in.Content))
	hash := hex.EncodeToString(sum[:])
	now := s.stamp().String()
	err = s.write(ctx, func(tx *sql.Tx) error {
		var projectID int64
		var status string
		err := tx.QueryRowContext(ctx, `select id, status from project where key = ?`, project).Scan(&projectID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if model.ProjectStatus(status) != model.ProjectActive {
			return ErrProjectNotActive
		}
		if invalid != nil {
			return invalid
		}
		var id int64
		var location, stored string
		err = tx.QueryRowContext(ctx, `select id, location, sha256 from document where project_id = ? and name = ?`, projectID, name).
			Scan(&id, &location, &stored)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			tags, err := marshalTags(meta.Tags)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `insert into document(project_id, name, location, kind, date, title, tags, sha256, size, content,
				created_at, created_by, updated_at, updated_by) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				projectID, name, in.Location, meta.Kind, meta.Date, meta.Title, tags, hash, len(in.Content), in.Content,
				now, tr.Actor, now, tr.Actor); err != nil {
				return err
			}
			created = true
			return nil
		case err != nil:
			return err
		case stored != hash:
			return &ImmutableError{SHA256: stored}
		case location != in.Location:
			_, err := tx.ExecContext(ctx, `update document set location = ?, updated_at = ?, updated_by = ? where id = ?`, in.Location, now, tr.Actor, id)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	doc, err = s.getDocument(ctx, project, name, false)
	return doc, created, err
}

// GetDocument returns a document with its content.
func (s *Store) GetDocument(ctx context.Context, project, name string) (*model.Document, error) {
	return s.getDocument(ctx, project, name, true)
}

func (s *Store) getDocument(ctx context.Context, project, name string, withContent bool) (*model.Document, error) {
	cols := documentCols
	var content string
	var extra []any
	if withContent {
		cols += `, d.content`
		extra = []any{&content}
	}
	d, err := scanDocument(s.db.QueryRowContext(ctx, `select `+cols+documentFrom+` where p.key = ? and d.name = ?`, project, name), extra...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if withContent {
		d.Content = &content
	}
	return d, nil
}

// ListDocuments returns a page of a project's documents by name, without their
// content, and the number of documents the project has.
func (s *Store) ListDocuments(ctx context.Context, project string, limit, offset int) ([]model.Document, int, error) {
	if _, err := getProject(ctx, s.db, project); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*)`+documentFrom+` where p.key = ?`, project).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `select `+documentCols+documentFrom+` where p.key = ? order by d.name limit ? offset ?`, project, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.Document{}
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *d)
	}
	return items, total, rows.Err()
}

// SearchDocuments returns a page of documents without their content and the
// number of documents that match. With a Query it searches the index with the
// query rules of the issue search, orders by relevance and attaches snippets.
func (s *Store) SearchDocuments(ctx context.Context, f DocumentFilter) ([]model.Document, int, error) {
	match, err := BuildMatch(f.Query)
	if err != nil {
		return nil, 0, err
	}
	from, cols, order := documentFrom, documentCols, ` order by d.date desc, d.name desc, d.id desc`
	var where []string
	var args []any
	if match != "" {
		from = ` from document_fts join document d on d.id = document_fts.rowid join project p on p.id = d.project_id`
		cols += `, snippet(document_fts, -1, '[', ']', '...', 16)`
		order = ` order by document_fts.rank, d.id`
		where = append(where, "document_fts match ?")
		args = append(args, match)
	}
	where, args = appendIn(where, args, "p.key", f.Projects)
	where, args = appendIn(where, args, "d.kind", enumStrings(f.Kinds))
	where, args = appendIn(where, args, "d.location", enumStrings(f.Locations))
	cond := ""
	if len(where) > 0 {
		cond = " where " + strings.Join(where, " and ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*)`+from+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `select `+cols+from+cond+order+` limit ? offset ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.Document{}
	for rows.Next() {
		var snippet string
		var d *model.Document
		if match != "" {
			d, err = scanDocument(rows, &snippet)
		} else {
			d, err = scanDocument(rows)
		}
		if err != nil {
			return nil, 0, err
		}
		if match != "" {
			d.Snippet = &snippet
		}
		items = append(items, *d)
	}
	return items, total, rows.Err()
}

// appendIn adds "col in (vals)" to a where list when vals is not empty.
func appendIn(where []string, args []any, col string, vals []string) ([]string, []any) {
	if len(vals) == 0 {
		return where, args
	}
	where = append(where, col+" in ("+placeholders(len(vals))+")")
	for _, v := range vals {
		args = append(args, v)
	}
	return where, args
}
