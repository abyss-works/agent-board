package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
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

// FieldDiff is a metadata field whose stored value differs from the request.
// Stored and Sent are the values as JSON.
type FieldDiff struct {
	Field, Stored, Sent string
}

// MetadataDiffError reports the same content under the name of a record
// document that exists, with other metadata. HTTP maps it to 409.
type MetadataDiffError struct {
	Diffs []FieldDiff
}

func (e *MetadataDiffError) Error() string {
	fields := make([]string, len(e.Diffs))
	for i, d := range e.Diffs {
		fields[i] = d.Field
	}
	return "record document exists with other metadata: " + strings.Join(fields, ", ")
}

// DocumentInput is a put request: the content, which is stored byte for byte,
// and the metadata the request carries.
type DocumentInput struct {
	Content string
	Args    model.DocumentArgs
}

// DocumentFilter selects and searches record documents. Values of one field are
// OR; no States selects every state. With a Query the result is in order of
// relevance and carries snippets; without one it is newest first by date.
type DocumentFilter struct {
	Query    string
	Projects []string
	Kinds    []model.DocumentKind
	States   []model.DocumentState
	Imported *bool
	Limit    int
	Offset   int
}

// documentState is the SQL expression of the state of the document d.
const documentState = `case when d.archived = 1 then 'archived'
	when exists (select 1 from document_relation sr where sr.kind = 'supersedes' and sr.target_document_id = d.id and sr.removed_at is null) then 'superseded'
	else 'current' end`

// documentCols are the columns of a document without its content.
const documentCols = `d.id, p.key, d.name, d.kind, d.date, d.title, d.tags, d.archived, d.imported, d.path, d.sha256, d.size,
	d.created_at, d.created_by, d.updated_at, d.updated_by`

const documentFrom = ` from document d join project p on p.id = d.project_id`

// scanDocument scans the columns of documentCols followed by the extra
// destinations of columns a query appended. The relations and the state are
// left to fillDocuments.
func scanDocument(sc rowScanner, extra ...any) (*model.Document, int64, error) {
	var d model.Document
	var id int64
	var date, title, docPath sql.NullString
	var tags, created, updated string
	dest := []any{&id, &d.Project, &d.Name, &d.Kind, &date, &title, &tags, &d.Archived, &d.Imported, &docPath, &d.SHA256, &d.Size,
		&created, &d.CreatedBy, &updated, &d.UpdatedBy}
	if err := sc.Scan(append(dest, extra...)...); err != nil {
		return nil, 0, err
	}
	d.Date, d.Title, d.Path = nullable(date), nullable(title), nullable(docPath)
	if err := json.Unmarshal([]byte(tags), &d.Tags); err != nil {
		return nil, 0, err
	}
	if d.Tags == nil {
		d.Tags = []string{}
	}
	var err error
	if d.CreatedAt, err = model.ParseTime(created); err != nil {
		return nil, 0, err
	}
	if d.UpdatedAt, err = model.ParseTime(updated); err != nil {
		return nil, 0, err
	}
	return &d, id, nil
}

// fillDocuments sets the relations, the state and the location of documents,
// which the document table does not hold. ids are their row ids; one query reads
// the relations of all of them.
func fillDocuments(ctx context.Context, q querier, docs []*model.Document, ids []int64) error {
	rels, err := loadRelations(ctx, q, ids)
	if err != nil {
		return err
	}
	for i, d := range docs {
		r := rels[ids[i]]
		d.Supersedes, d.Related, d.Issues, d.SupersededBy, d.UnresolvedRefs = r.supersedes, r.related, r.issues, r.supersededBy, r.unresolved
		switch {
		case d.Archived:
			d.State = model.StateArchived
		case len(r.supersededBy) > 0:
			d.State = model.StateSuperseded
		default:
			d.State = model.StateCurrent
		}
	}
	return nil
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

// PutDocument stores a record document. A document is never changed by a put:
// the same content and metadata again changes nothing, other content under the
// same name is refused with an *ImmutableError, and the same content with other
// metadata with a *MetadataDiffError. created reports a document that is new. A
// put leaves no activity record; created_by and updated_by name the actor.
// Relations that an imported document could not resolve come back as warnings,
// and are resolved when their target is stored. The result carries no content.
func (s *Store) PutDocument(ctx context.Context, tr model.Trace, project, name string, in DocumentInput) (res *model.DocumentResult, created bool, err error) {
	meta, invalid := model.ValidateDocument(name, in.Content, in.Args)
	sum := sha256.Sum256([]byte(in.Content))
	hash := hex.EncodeToString(sum[:])
	now := s.stamp().String()
	var warnings []model.DocumentWarning
	err = s.write(ctx, func(tx *sql.Tx) error {
		self := docSelf{projectKey: project, name: name}
		var status string
		err := tx.QueryRowContext(ctx, `select id, status from project where key = ?`, project).Scan(&self.projectID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if model.ProjectStatus(status) != model.ProjectActive {
			return ErrProjectNotActive
		}
		var stored string
		err = tx.QueryRowContext(ctx, `select id, sha256 from document where project_id = ? and name = ?`, self.projectID, name).Scan(&self.id, &stored)
		exists := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		rels, warns, rerrs, err := resolveRelations(ctx, tx, self, meta)
		if err != nil {
			return err
		}
		warnings = warns
		if err := model.MergeValidation(invalid, ptrValidation(rerrs)); err != nil {
			return err
		}
		if err := checkSupersedesCycle(ctx, tx, self, rels); err != nil {
			return err
		}
		switch {
		case !exists:
			tags, err := marshalTags(meta.Tags)
			if err != nil {
				return err
			}
			r, err := tx.ExecContext(ctx, `insert into document(project_id, name, kind, date, title, tags, archived, imported, path, sha256, size, content,
				created_at, created_by, updated_at, updated_by) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				self.projectID, name, string(meta.Kind), meta.Date, meta.Title, tags, meta.Archived, meta.Imported, meta.Path, hash, len(in.Content), in.Content,
				now, tr.Actor, now, tr.Actor)
			if err != nil {
				return err
			}
			if self.id, err = r.LastInsertId(); err != nil {
				return err
			}
			for _, t := range rels {
				if err := insertRelation(ctx, tx, self.id, t); err != nil {
					return err
				}
			}
			created = true
			return reresolveRelations(ctx, tx, tr, s.stamp(), self)
		case stored != hash:
			return &ImmutableError{SHA256: stored}
		}
		cur, err := getDocument(ctx, tx, project, name, false)
		if err != nil {
			return err
		}
		if diffs := metadataDiffs(cur, meta, rels); len(diffs) > 0 {
			return &MetadataDiffError{Diffs: diffs}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	d, err := getDocument(ctx, s.db, project, name, false)
	if err != nil {
		return nil, false, err
	}
	if warnings == nil {
		warnings = []model.DocumentWarning{}
	}
	return &model.DocumentResult{Document: *d, Warnings: warnings}, created, nil
}

// ptrValidation wraps field errors as a validation error, or nil for none.
func ptrValidation(fields []model.FieldError) error {
	if len(fields) == 0 {
		return nil
	}
	return &model.ValidationError{Fields: fields}
}

// metadataDiffs lists the metadata of the stored document cur that differ from
// the metadata a request says. Relations compare as sets of targets, resolved
// ones by name or key and unresolved ones by text.
func metadataDiffs(cur *model.Document, meta model.DocumentMeta, rels []relTarget) []FieldDiff {
	var diffs []FieldDiff
	js := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}
	add := func(field string, stored, sent any) {
		if js(stored) != js(sent) {
			diffs = append(diffs, FieldDiff{field, js(stored), js(sent)})
		}
	}
	add("kind", cur.Kind, meta.Kind)
	add("date", cur.Date, meta.Date)
	add("title", cur.Title, meta.Title)
	add("tags", cur.Tags, meta.Tags)
	add("imported", cur.Imported, meta.Imported)
	add("path", cur.Path, meta.Path)
	add("archived", cur.Archived, meta.Archived)
	stored := (&docRelations{supersedes: cur.Supersedes, related: cur.Related, issues: cur.Issues, unresolved: cur.UnresolvedRefs}).sets()
	sent := requestedSets(rels)
	for _, field := range []string{"supersedes", "related", "issues"} {
		if !slices.Equal(stored[field], sent[field]) {
			diffs = append(diffs, FieldDiff{field, js(stored[field]), js(sent[field])})
		}
	}
	return diffs
}

// GetDocument returns a document with its content.
func (s *Store) GetDocument(ctx context.Context, project, name string) (*model.Document, error) {
	return getDocument(ctx, s.db, project, name, true)
}

func getDocument(ctx context.Context, q querier, project, name string, withContent bool) (*model.Document, error) {
	cols := documentCols
	var content string
	var extra []any
	if withContent {
		cols += `, d.content`
		extra = []any{&content}
	}
	d, id, err := scanDocument(q.QueryRowContext(ctx, `select `+cols+documentFrom+` where p.key = ? and d.name = ?`, project, name), extra...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if withContent {
		d.Content = &content
	}
	if err := fillDocuments(ctx, q, []*model.Document{d}, []int64{id}); err != nil {
		return nil, err
	}
	return d, nil
}

// scanDocuments reads documents from rows until they end and fills them.
func scanDocuments(ctx context.Context, q querier, rows *sql.Rows, snippets bool) ([]model.Document, error) {
	defer rows.Close()
	var docs []*model.Document
	var ids []int64
	for rows.Next() {
		var snippet string
		var d *model.Document
		var id int64
		var err error
		if snippets {
			d, id, err = scanDocument(rows, &snippet)
		} else {
			d, id, err = scanDocument(rows)
		}
		if err != nil {
			return nil, err
		}
		if snippets {
			d.Snippet = &snippet
		}
		docs = append(docs, d)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := fillDocuments(ctx, q, docs, ids); err != nil {
		return nil, err
	}
	items := make([]model.Document, len(docs))
	for i, d := range docs {
		items[i] = *d
	}
	return items, nil
}

// ListDocuments returns a page of a project's documents by name, without their
// content, and the number of documents the project has. It lists every state.
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
	items, err := scanDocuments(ctx, s.db, rows, false)
	return items, total, err
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
	if len(f.States) > 0 {
		where, args = appendIn(where, args, "("+documentState+")", enumStrings(f.States))
	}
	if f.Imported != nil {
		where = append(where, "d.imported = ?")
		args = append(args, *f.Imported)
	}
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
	items, err := scanDocuments(ctx, s.db, rows, match != "")
	return items, total, err
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

// documentRow is the document a change of its flags or relations works on.
type documentRow struct {
	docSelf
	imported, archived bool
}

func loadDocumentRow(ctx context.Context, tx *sql.Tx, project, name string) (documentRow, error) {
	r := documentRow{docSelf: docSelf{projectKey: project, name: name}}
	var status string
	err := tx.QueryRowContext(ctx, `select id, status from project where key = ?`, project).Scan(&r.projectID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if model.ProjectStatus(status) != model.ProjectActive {
		return r, ErrProjectNotActive
	}
	err = tx.QueryRowContext(ctx, `select id, imported, archived from document where project_id = ? and name = ?`, r.projectID, name).
		Scan(&r.id, &r.imported, &r.archived)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// appendDocumentActivity appends a record to the activity of a document.
func appendDocumentActivity(ctx context.Context, tx *sql.Tx, docID int64, tr model.Trace, at model.Time, changes map[string]model.Change) error {
	b, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `insert into document_activity(document_id, actor_id, session_id, client_host, at, changes) values (?, ?, ?, ?, ?, ?)`,
		docID, tr.Actor, tr.SessionID, tr.ClientHost, at.String(), string(b))
	return err
}

// insertDocumentActivity appends the change of a document to its activity and
// stamps the document.
func insertDocumentActivity(ctx context.Context, tx *sql.Tx, docID int64, tr model.Trace, at model.Time, changes map[string]model.Change) error {
	if err := appendDocumentActivity(ctx, tx, docID, tr, at, changes); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `update document set updated_at = ?, updated_by = ? where id = ?`, at.String(), tr.Actor, docID)
	return err
}

// SetDocumentArchived marks a document archived or takes the mark off. A
// change is recorded in the activity of the document; marking what is marked
// changes nothing.
func (s *Store) SetDocumentArchived(ctx context.Context, tr model.Trace, project, name string, archived bool) (*model.DocumentResult, error) {
	err := s.write(ctx, func(tx *sql.Tx) error {
		row, err := loadDocumentRow(ctx, tx, project, name)
		if err != nil {
			return err
		}
		if row.archived == archived {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `update document set archived = ? where id = ?`, archived, row.id); err != nil {
			return err
		}
		return insertDocumentActivity(ctx, tx, row.id, tr, s.stamp(), map[string]model.Change{"archived": {From: rawJSON(row.archived), To: rawJSON(archived)}})
	})
	if err != nil {
		return nil, err
	}
	return s.documentResult(ctx, project, name, nil)
}

// ChangeDocumentRelation adds or removes one relation of a document. The target
// is resolved like the relations of a put. Removing takes away the relation to
// the document or issue the target names and the one kept as that text. A
// change is recorded in the activity of the document.
func (s *Store) ChangeDocumentRelation(ctx context.Context, tr model.Trace, project, name string, kind model.RelationKind, target string, add bool) (*model.DocumentResult, error) {
	var warnings []model.DocumentWarning
	at := s.stamp()
	err := s.write(ctx, func(tx *sql.Tx) error {
		row, err := loadDocumentRow(ctx, tx, project, name)
		if err != nil {
			return err
		}
		t, err := resolveTarget(ctx, tx, row.docSelf, kind, target)
		if err != nil {
			return err
		}
		before, err := loadRelations(ctx, tx, []int64{row.id})
		if err != nil {
			return err
		}
		if add {
			switch {
			case kind != model.RelAbout && !row.imported && !model.IsDocumentName(target):
				hint := ""
				if kind == model.RelSupersedes {
					hint = " 문서의 일부만 대체하면 related로 씁니다."
				}
				return ptrValidation([]model.FieldError{{Field: "target", Reason: "not_a_name", Message: "문서 이름(.md로 끝나는 한 단어)이어야 합니다." + hint}})
			case kind != model.RelAbout && row.refersToSelf(target, t):
				return ptrValidation([]model.FieldError{{Field: "target", Reason: "self_reference", Message: "자기 자신을 가리킬 수 없습니다."}})
			case !t.resolved() && !row.imported:
				return ptrValidation([]model.FieldError{{Field: "target", Reason: "unresolved", Message: unresolvedMessage(kind, target)}})
			case !t.resolved():
				warnings = append(warnings, model.DocumentWarning{Field: "target", Reason: "unresolved", Message: unresolvedMessage(kind, target) + " 풀리지 않은 관계로 두었습니다."})
			}
			if err := checkSupersedesCycle(ctx, tx, row.docSelf, []relTarget{t}); err != nil {
				return err
			}
			if err := insertRelation(ctx, tx, row.id, t); err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `update document_relation set removed_at = ?, removed_by = ?, removed_session = ?, removed_host = ?
				where removed_at is null and document_id = ? and kind = ? and
				((target_document_id is not null and target_document_id = ?) or (target_issue_id is not null and target_issue_id = ?) or target_text = ?)`,
				at.String(), tr.Actor, tr.SessionID, tr.ClientHost, row.id, string(kind), t.docID, t.issueID, target); err != nil {
				return err
			}
		}
		after, err := loadRelations(ctx, tx, []int64{row.id})
		if err != nil {
			return err
		}
		changes := relationChanges(before[row.id], after[row.id])
		if len(changes) == 0 {
			return nil
		}
		return insertDocumentActivity(ctx, tx, row.id, tr, at, changes)
	})
	if err != nil {
		return nil, err
	}
	return s.documentResult(ctx, project, name, warnings)
}

// relationChanges lists the relation fields that differ, with their whole lists.
func relationChanges(before, after *docRelations) map[string]model.Change {
	changes := map[string]model.Change{}
	for _, f := range []struct {
		field         string
		before, after []string
	}{
		{"supersedes", before.supersedes, after.supersedes},
		{"related", before.related, after.related},
		{"issues", before.issues, after.issues},
	} {
		if !slices.Equal(f.before, f.after) {
			changes[f.field] = model.Change{From: rawJSON(f.before), To: rawJSON(f.after)}
		}
	}
	if !slices.Equal(before.unresolved, after.unresolved) {
		changes["unresolved_refs"] = model.Change{From: rawJSON(before.unresolved), To: rawJSON(after.unresolved)}
	}
	return changes
}

func (s *Store) documentResult(ctx context.Context, project, name string, warnings []model.DocumentWarning) (*model.DocumentResult, error) {
	d, err := getDocument(ctx, s.db, project, name, false)
	if err != nil {
		return nil, err
	}
	if warnings == nil {
		warnings = []model.DocumentWarning{}
	}
	return &model.DocumentResult{Document: *d, Warnings: warnings}, nil
}

// ListDocumentActivity returns a page of the activity of a document, newest
// first, and the number of records it has.
func (s *Store) ListDocumentActivity(ctx context.Context, project, name string, limit, offset int) ([]model.DocumentActivity, int, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `select d.id from document d join project p on p.id = d.project_id where p.key = ? and d.name = ?`, project, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*) from document_activity where document_id = ?`, id).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `select id, actor_id, session_id, client_host, at, changes from document_activity where document_id = ? order by id desc limit ? offset ?`, id, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.DocumentActivity{}
	for rows.Next() {
		a := model.DocumentActivity{Project: project, Document: name}
		var at, changes string
		if err := rows.Scan(&a.ID, &a.Actor, &a.SessionID, &a.ClientHost, &at, &changes); err != nil {
			return nil, 0, err
		}
		if a.At, err = model.ParseTime(at); err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal([]byte(changes), &a.Changes); err != nil {
			return nil, 0, err
		}
		items = append(items, a)
	}
	return items, total, rows.Err()
}
