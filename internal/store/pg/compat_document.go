package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

// This file serves the record document API of the classic clients on the record
// and graph tables: a document is a record_document row, its relations are
// confirmed graph edges (supersedes and related to documents, about to issues), and
// a relation to a name that does not exist yet is an edge that keeps the text.

type docQ interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// docSelf identifies the document whose relations are resolved. id is 0 while the
// document is not stored yet.
type docSelf struct {
	id         int64
	projectID  int64
	projectKey string
	name       string
}

type relTarget struct {
	kind    model.RelationKind
	docID   int64
	issueID int64
	text    string
	display string
}

func (t relTarget) resolved() bool { return t.docID != 0 || t.issueID != 0 }

func (self docSelf) refersToSelf(raw string, t relTarget) bool {
	return raw == self.name || raw == self.projectKey+"/"+self.name || (self.id != 0 && t.docID == self.id)
}

func relationField(kind model.RelationKind) string {
	if kind == model.RelAbout {
		return "issues"
	}
	return string(kind)
}

func unresolvedMessage(kind model.RelationKind, raw string) string {
	if kind == model.RelAbout {
		return fmt.Sprintf("이슈 %s가 없습니다.", raw)
	}
	return fmt.Sprintf("같은 프로젝트에 문서 %s가 없습니다. 다른 프로젝트의 문서는 PROJECT/이름으로 씁니다.", raw)
}

func ptrValidation(fields []model.FieldError) error {
	if len(fields) == 0 {
		return nil
	}
	return &model.ValidationError{Fields: fields}
}

var docDateLayouts = []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"}

// parseDocDate reads the date of a document; it was checked by the model.
func parseDocDate(s *string) *time.Time {
	if s == nil {
		return nil
	}
	for _, l := range docDateLayouts {
		if t, err := time.ParseInLocation(l, strings.TrimSpace(*s), time.UTC); err == nil {
			return &t
		}
	}
	return nil
}

// formatDocDate writes the date of a document: the day, and the minute when the
// time is not midnight.
func formatDocDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	u := t.UTC()
	layout := "2006-01-02 15:04"
	if u.Hour() == 0 && u.Minute() == 0 && u.Second() == 0 {
		layout = "2006-01-02"
	}
	s := u.Format(layout)
	return &s
}

// normalizeDocDate is the date as a read of the stored document would show it.
func normalizeDocDate(s *string) *string { return formatDocDate(parseDocDate(s)) }

func resolveDocument(ctx context.Context, q docQ, projectID int64, projectKey, raw string) (id int64, display string, found bool, err error) {
	err = q.QueryRow(ctx, `SELECT id FROM record_document WHERE project_id = $1 AND name = $2`, projectID, raw).Scan(&id)
	if err == nil {
		return id, raw, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, "", false, err
	}
	key, rest, ok := strings.Cut(raw, "/")
	if !ok || key == "" || rest == "" {
		return 0, "", false, nil
	}
	err = q.QueryRow(ctx, `SELECT d.id FROM record_document d JOIN project p ON p.id = d.project_id WHERE p.key = $1 AND d.name = $2`, key, rest).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	if key == projectKey {
		return id, rest, true, nil
	}
	return id, raw, true, nil
}

func resolveTarget(ctx context.Context, q docQ, self docSelf, kind model.RelationKind, raw string) (relTarget, error) {
	t := relTarget{kind: kind, text: raw, display: raw}
	if kind == model.RelAbout {
		err := q.QueryRow(ctx, `SELECT id FROM issue WHERE key = $1`, raw).Scan(&t.issueID)
		if errors.Is(err, pgx.ErrNoRows) {
			return t, nil
		}
		if err == nil {
			t.text = ""
		}
		return t, err
	}
	id, display, found, err := resolveDocument(ctx, q, self.projectID, self.projectKey, raw)
	if err != nil || !found {
		return t, err
	}
	t.docID, t.display, t.text = id, display, ""
	return t, nil
}

func resolveRelations(ctx context.Context, q docQ, self docSelf, meta model.DocumentMeta) (rels []relTarget, warnings []model.DocumentWarning, errs []model.FieldError, err error) {
	seen := map[[2]string]bool{}
	for _, list := range []struct {
		kind  model.RelationKind
		items []string
	}{{model.RelSupersedes, meta.Supersedes}, {model.RelRelated, meta.Related}, {model.RelAbout, meta.Issues}} {
		for i, raw := range list.items {
			field := fmt.Sprintf("%s[%d]", relationField(list.kind), i)
			if !meta.Imported && list.kind != model.RelAbout && !model.IsDocumentName(raw) {
				continue
			}
			t, err := resolveTarget(ctx, q, self, list.kind, raw)
			if err != nil {
				return nil, nil, nil, err
			}
			switch {
			case list.kind != model.RelAbout && self.refersToSelf(raw, t):
				errs = append(errs, model.FieldError{Field: field, Reason: "self_reference", Message: "자기 자신을 가리킬 수 없습니다."})
				continue
			case !t.resolved() && !meta.Imported:
				errs = append(errs, model.FieldError{Field: field, Reason: "unresolved", Message: unresolvedMessage(list.kind, raw)})
				continue
			case !t.resolved():
				warnings = append(warnings, model.DocumentWarning{Field: field, Reason: "unresolved", Message: unresolvedMessage(list.kind, raw) + " 풀리지 않은 관계로 두었습니다."})
			}
			if key := [2]string{string(list.kind), t.display}; !seen[key] {
				seen[key] = true
				rels = append(rels, t)
			}
		}
	}
	return rels, warnings, errs, nil
}

func supersedesReaches(ctx context.Context, q docQ, from, to int64) (bool, error) {
	var n int
	err := q.QueryRow(ctx, `WITH RECURSIVE r(id) AS (
		SELECT $1::bigint
		UNION
		SELECT ge.target_id FROM graph_edge ge JOIN r ON ge.source_id = r.id
		WHERE ge.source_type = 'record' AND ge.kind = 'supersedes' AND ge.target_type = 'record' AND ge.status = 'confirmed'
	) SELECT count(*) FROM r WHERE id = $2`, from, to).Scan(&n)
	return n > 0, err
}

func checkSupersedesCycle(ctx context.Context, q docQ, self docSelf, rels []relTarget) error {
	if self.id == 0 {
		return nil
	}
	for _, t := range rels {
		if t.kind != model.RelSupersedes || t.docID == 0 {
			continue
		}
		cycle, err := supersedesReaches(ctx, q, t.docID, self.id)
		if err != nil {
			return err
		}
		if cycle {
			return store.ErrSupersedesCycle
		}
	}
	return nil
}

// insertRelation adds a confirmed edge. A removed or still proposed edge to the same
// target comes back as the confirmed one.
func insertRelation(ctx context.Context, q docQ, projectID, docID int64, t relTarget) error {
	var match string
	var arg any
	var targetType *string
	var targetID *int64
	var text *string
	switch {
	case t.docID != 0:
		match, arg = `target_type = 'record' AND target_id = $3`, t.docID
		tt, id := "record", t.docID
		targetType, targetID = &tt, &id
	case t.issueID != 0:
		match, arg = `target_type = 'issue' AND target_id = $3`, t.issueID
		tt, id := "issue", t.issueID
		targetType, targetID = &tt, &id
	default:
		match, arg = `unresolved_target_text = $3`, t.text
		tx := t.text
		text = &tx
	}
	tag, err := q.Exec(ctx, `UPDATE graph_edge SET status = 'confirmed', origin = 'explicit'
		WHERE id = (SELECT id FROM graph_edge WHERE source_type = 'record' AND source_id = $1 AND kind = $2 AND `+match+`
		            AND status IN ('removed', 'candidate') ORDER BY (status = 'candidate') DESC, id LIMIT 1)
		  AND NOT EXISTS (SELECT 1 FROM graph_edge WHERE source_type = 'record' AND source_id = $1 AND kind = $2 AND `+match+` AND status = 'confirmed')`,
		docID, string(t.kind), arg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	_, err = q.Exec(ctx, `INSERT INTO graph_edge (project_id, source_type, source_id, target_type, target_id, unresolved_target_text, kind, origin, status)
		VALUES ($1, 'record', $2, $3, $4, $5, $6, 'explicit', 'confirmed') ON CONFLICT DO NOTHING`,
		projectID, docID, targetType, targetID, text, string(t.kind))
	return err
}

type docRelations struct {
	supersedes, related, issues, supersededBy []string
	unresolved                                []model.UnresolvedRef
}

func loadRelations(ctx context.Context, q docQ, ids []int64) (map[int64]*docRelations, error) {
	out := make(map[int64]*docRelations, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	for _, id := range ids {
		out[id] = &docRelations{supersedes: []string{}, related: []string{}, issues: []string{}, supersededBy: []string{}, unresolved: []model.UnresolvedRef{}}
	}
	rows, err := q.Query(ctx, `SELECT ge.source_id, ge.kind, sp.key, tp.key, td.name, ti.key, ge.unresolved_target_text
		FROM graph_edge ge
		JOIN record_document d ON d.id = ge.source_id JOIN project sp ON sp.id = d.project_id
		LEFT JOIN record_document td ON ge.target_type = 'record' AND td.id = ge.target_id
		LEFT JOIN project tp ON tp.id = td.project_id
		LEFT JOIN issue ti ON ge.target_type = 'issue' AND ti.id = ge.target_id
		WHERE ge.source_type = 'record' AND ge.source_id = ANY($1) AND ge.status = 'confirmed' AND ge.kind IN ('supersedes', 'related', 'about')
		  AND (ge.origin = 'explicit' OR ge.kind = 'supersedes')
		ORDER BY ge.id`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var kind, dp string
		var tp, tdName, tiKey, text *string
		if err := rows.Scan(&id, &kind, &dp, &tp, &tdName, &tiKey, &text); err != nil {
			rows.Close()
			return nil, err
		}
		r := out[id]
		switch {
		case text != nil:
			r.unresolved = append(r.unresolved, model.UnresolvedRef{Kind: model.RelationKind(kind), Target: *text})
		case tiKey != nil:
			r.issues = append(r.issues, *tiKey)
		case tdName != nil && tp != nil:
			name := *tdName
			if *tp != dp {
				name = *tp + "/" + name
			}
			if kind == string(model.RelSupersedes) {
				r.supersedes = append(r.supersedes, name)
			} else {
				r.related = append(r.related, name)
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.Query(ctx, `SELECT ge.target_id, sp.key, sd.name, tp.key
		FROM graph_edge ge
		JOIN record_document sd ON sd.id = ge.source_id JOIN project sp ON sp.id = sd.project_id
		JOIN record_document td ON td.id = ge.target_id JOIN project tp ON tp.id = td.project_id
		WHERE ge.source_type = 'record' AND ge.kind = 'supersedes' AND ge.status = 'confirmed' AND ge.target_type = 'record' AND ge.target_id = ANY($1)
		ORDER BY ge.id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var sp, name, tp string
		if err := rows.Scan(&id, &sp, &name, &tp); err != nil {
			return nil, err
		}
		if sp != tp {
			name = sp + "/" + name
		}
		out[id].supersededBy = append(out[id].supersededBy, name)
	}
	return out, rows.Err()
}

func (r *docRelations) sets() map[string][]string {
	norm := func(l []string) []string {
		l = slices.Clone(l)
		slices.Sort(l)
		return slices.Compact(l)
	}
	sets := map[string][]string{"supersedes": slices.Clone(r.supersedes), "related": slices.Clone(r.related), "issues": slices.Clone(r.issues)}
	for _, u := range r.unresolved {
		field := relationField(u.Kind)
		sets[field] = append(sets[field], u.Target)
	}
	for k, v := range sets {
		sets[k] = norm(v)
	}
	return sets
}

func requestedSets(rels []relTarget) map[string][]string {
	sets := map[string][]string{"supersedes": nil, "related": nil, "issues": nil}
	for _, t := range rels {
		field := relationField(t.kind)
		sets[field] = append(sets[field], t.display)
	}
	for k, v := range sets {
		slices.Sort(v)
		sets[k] = slices.Compact(v)
	}
	return sets
}

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
			changes[f.field] = model.Change{From: jsonRaw(f.before), To: jsonRaw(f.after)}
		}
	}
	if !slices.Equal(unresolvedTexts(before.unresolved), unresolvedTexts(after.unresolved)) {
		changes["unresolved_refs"] = model.Change{From: jsonRaw(before.unresolved), To: jsonRaw(after.unresolved)}
	}
	return changes
}

func unresolvedTexts(l []model.UnresolvedRef) []string {
	out := make([]string, len(l))
	for i, u := range l {
		out[i] = string(u.Kind) + ":" + u.Target
	}
	return out
}

func metadataDiffs(cur *model.Document, meta model.DocumentMeta, rels []relTarget) []store.FieldDiff {
	var diffs []store.FieldDiff
	js := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}
	add := func(field string, stored, sent any) {
		if js(stored) != js(sent) {
			diffs = append(diffs, store.FieldDiff{Field: field, Stored: js(stored), Sent: js(sent)})
		}
	}
	add("kind", cur.Kind, meta.Kind)
	add("date", cur.Date, normalizeDocDate(meta.Date))
	add("title", cur.Title, meta.Title)
	add("tags", cur.Tags, nonNilStrings(meta.Tags))
	add("imported", cur.Imported, meta.Imported)
	add("path", cur.Path, meta.Path)
	add("archived", cur.Archived, meta.Archived)
	stored := (&docRelations{supersedes: cur.Supersedes, related: cur.Related, issues: cur.Issues, unresolved: cur.UnresolvedRefs}).sets()
	sent := requestedSets(rels)
	for _, field := range []string{"supersedes", "related", "issues"} {
		if !slices.Equal(stored[field], sent[field]) {
			diffs = append(diffs, store.FieldDiff{Field: field, Stored: js(stored[field]), Sent: js(sent[field])})
		}
	}
	return diffs
}

const docSelectCols = `d.id, p.key, d.name, d.kind, d.recorded_at, NULLIF(d.title, ''), d.tags, d.archived, d.imported, d.source_path, d.sha256, d.size_bytes,
	d.created_at, d.created_by, d.updated_at, d.updated_by`

const docFrom = ` FROM record_document d JOIN project p ON p.id = d.project_id`

const docStateExpr = `CASE WHEN d.archived THEN 'archived'
	WHEN EXISTS (SELECT 1 FROM graph_edge sr WHERE sr.kind = 'supersedes' AND sr.status = 'confirmed' AND sr.target_type = 'record' AND sr.target_id = d.id) THEN 'superseded'
	ELSE 'current' END`

func scanDoc(row pgx.Row, extra ...any) (*model.Document, int64, error) {
	var d model.Document
	var id int64
	var recorded *time.Time
	var tags []byte
	var created, updated time.Time
	var kind string
	dest := []any{&id, &d.Project, &d.Name, &kind, &recorded, &d.Title, &tags, &d.Archived, &d.Imported, &d.Path, &d.SHA256, &d.Size,
		&created, &d.CreatedBy, &updated, &d.UpdatedBy}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, 0, err
	}
	d.Kind = model.DocumentKind(kind)
	d.Date = formatDocDate(recorded)
	d.Tags = unmarshalStringArray(tags)
	if d.Tags == nil {
		d.Tags = []string{}
	}
	d.CreatedAt, d.UpdatedAt = model.NewTime(created), model.NewTime(updated)
	return &d, id, nil
}

func fillDocs(ctx context.Context, q docQ, docs []*model.Document, ids []int64) error {
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

func getDoc(ctx context.Context, q docQ, project, name string, withContent bool) (*model.Document, error) {
	cols := docSelectCols
	var content string
	var extra []any
	if withContent {
		cols += `, d.content`
		extra = []any{&content}
	}
	d, id, err := scanDoc(q.QueryRow(ctx, `SELECT `+cols+docFrom+` WHERE p.key = $1 AND d.name = $2`, model.NormalizeProjectKey(project), name), extra...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if withContent {
		d.Content = &content
	}
	if err := fillDocs(ctx, q, []*model.Document{d}, []int64{id}); err != nil {
		return nil, err
	}
	return d, nil
}

// CompatGetDocument returns a document with its content.
func (s *Store) CompatGetDocument(ctx context.Context, project, name string) (*model.Document, error) {
	return getDoc(ctx, s.pool, project, name, true)
}

func (s *Store) docResult(ctx context.Context, q docQ, project, name string, warnings []model.DocumentWarning) (*model.DocumentResult, error) {
	d, err := getDoc(ctx, q, project, name, false)
	if err != nil {
		return nil, err
	}
	if warnings == nil {
		warnings = []model.DocumentWarning{}
	}
	return &model.DocumentResult{Document: *d, Warnings: warnings}, nil
}

func (s *Store) loadProjectForWrite(ctx context.Context, tx pgx.Tx, project string) (id int64, err error) {
	var status string
	err = tx.QueryRow(ctx, `SELECT id, status FROM project WHERE key = $1`, model.NormalizeProjectKey(project)).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if model.ProjectStatus(status) != model.ProjectActive {
		return 0, ErrProjectNotActive
	}
	return id, nil
}

func (s *Store) insertDocument(ctx context.Context, tx pgx.Tx, events *[]model.Event, tr Trace, at time.Time, projectID int64, project, name string, meta model.DocumentMeta, content, hash string) (int64, error) {
	tags := marshalStringArray(nonNilStrings(meta.Tags))
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO record_document (project_id, name, kind, recorded_at, title, tags, archived, imported, source_path,
			sha256, size_bytes, content, created_at, created_by, updated_at, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11, $12, $13, $14, $13, $14) RETURNING id`,
		projectID, name, string(meta.Kind), parseDocDate(meta.Date), derefStr(meta.Title), tags, meta.Archived, meta.Imported, meta.Path,
		hash, len(content), content, at, tr.Actor).Scan(&id); err != nil {
		return 0, err
	}
	if _, err := enqueueIngestJobTx(ctx, tx, model.IngestSourceRecord, id, hash); err != nil {
		return 0, err
	}
	pk := model.NormalizeProjectKey(project)
	return id, emitEventTx(ctx, tx, events, at, &projectID, &pk, model.EventDocumentCreated, name, tr.Actor, tr.SessionID, nil)
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// CompatPutDocument stores a record document. A document is never changed by a put:
// the same content and metadata again changes nothing, other content under the same
// name is refused with a *store.ImmutableError, and the same content with other
// metadata with a *store.MetadataDiffError.
func (s *Store) CompatPutDocument(ctx context.Context, tr Trace, project, name string, in store.DocumentInput) (*model.DocumentResult, bool, error) {
	meta, invalid := model.ValidateDocument(name, in.Content, in.Args)
	hash := sha256Hex(in.Content)
	at := s.now()
	var warnings []model.DocumentWarning
	var created bool
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		projectID, err := s.loadProjectForWrite(ctx, tx, project)
		if err != nil {
			return err
		}
		self := docSelf{projectID: projectID, projectKey: model.NormalizeProjectKey(project), name: name}
		var stored string
		err = tx.QueryRow(ctx, `SELECT id, sha256 FROM record_document WHERE project_id = $1 AND name = $2 FOR UPDATE`, projectID, name).Scan(&self.id, &stored)
		exists := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
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
			id, err := s.insertDocument(ctx, tx, events, tr, at, projectID, project, name, meta, in.Content, hash)
			if err != nil {
				return err
			}
			self.id = id
			for _, t := range rels {
				if err := insertRelation(ctx, tx, projectID, id, t); err != nil {
					return err
				}
			}
			created = true
			return s.reresolveRelations(ctx, tx, events, tr, at, self)
		case stored != hash:
			return &store.ImmutableError{SHA256: stored}
		}
		cur, err := getDoc(ctx, tx, project, name, false)
		if err != nil {
			return err
		}
		if diffs := metadataDiffs(cur, meta, rels); len(diffs) > 0 {
			return &store.MetadataDiffError{Diffs: diffs}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	res, err := s.docResult(ctx, s.pool, project, name, warnings)
	return res, created, err
}

// CompatPutDocumentsBatch stores up to store.MaxBatchDocuments documents atomically.
func (s *Store) CompatPutDocumentsBatch(ctx context.Context, tr Trace, project string, items []store.BatchDocumentInput) ([]model.DocumentResult, error) {
	if len(items) == 0 || len(items) > store.MaxBatchDocuments {
		return nil, ptrValidation([]model.FieldError{{Field: "documents", Reason: "invalid", Message: fmt.Sprintf("1~%d건이어야 합니다.", store.MaxBatchDocuments)}})
	}
	type prepared struct {
		name     string
		in       store.DocumentInput
		meta     model.DocumentMeta
		hash     string
		self     docSelf
		exists   bool
		stored   string
		warnings []model.DocumentWarning
	}
	prep := make([]prepared, len(items))
	var vfields []model.FieldError
	seen := make(map[string]bool, len(items))
	for i, it := range items {
		prefix := fmt.Sprintf("documents[%d].", i)
		if seen[it.Name] {
			vfields = append(vfields, model.FieldError{Field: prefix + "name", Reason: "duplicate", Message: fmt.Sprintf("같은 요청 안에 문서 이름 %s가 중복됩니다.", it.Name)})
			continue
		}
		seen[it.Name] = true
		meta, invalid := model.ValidateDocument(it.Name, it.Content, it.Args)
		var ve *model.ValidationError
		if errors.As(invalid, &ve) {
			for _, f := range ve.Fields {
				vfields = append(vfields, model.FieldError{Field: prefix + f.Field, Reason: f.Reason, Message: f.Message})
			}
		}
		prep[i] = prepared{name: it.Name, in: it.DocumentInput, meta: meta, hash: sha256Hex(it.Content)}
	}
	at := s.now()
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		projectID, err := s.loadProjectForWrite(ctx, tx, project)
		if err != nil {
			return err
		}
		pk := model.NormalizeProjectKey(project)
		if len(vfields) > 0 {
			for i := range prep {
				if prep[i].name == "" {
					continue
				}
				prep[i].self = docSelf{projectID: projectID, projectKey: pk, name: prep[i].name}
				_, _, rerrs, err := resolveRelations(ctx, tx, prep[i].self, prep[i].meta)
				if err != nil {
					return err
				}
				for _, f := range rerrs {
					vfields = append(vfields, model.FieldError{Field: fmt.Sprintf("documents[%d].%s", i, f.Field), Reason: f.Reason, Message: f.Message})
				}
			}
			return ptrValidation(vfields)
		}
		for i := range prep {
			p := &prep[i]
			p.self = docSelf{projectID: projectID, projectKey: pk, name: p.name}
			err := tx.QueryRow(ctx, `SELECT id, sha256 FROM record_document WHERE project_id = $1 AND name = $2`, projectID, p.name).Scan(&p.self.id, &p.stored)
			p.exists = err == nil
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if p.exists && p.stored != p.hash {
				return &store.ImmutableError{Name: p.name, Field: fmt.Sprintf("documents[%d].content", i), SHA256: p.stored}
			}
		}
		for i := range prep {
			p := &prep[i]
			if p.exists {
				continue
			}
			id, err := s.insertDocument(ctx, tx, events, tr, at, projectID, project, p.name, p.meta, p.in.Content, p.hash)
			if err != nil {
				return err
			}
			p.self.id = id
		}
		relsList := make([][]relTarget, len(prep))
		var relErrs []model.FieldError
		for i := range prep {
			p := &prep[i]
			rels, warns, rerrs, err := resolveRelations(ctx, tx, p.self, p.meta)
			if err != nil {
				return err
			}
			p.warnings = warns
			relsList[i] = rels
			for _, f := range rerrs {
				relErrs = append(relErrs, model.FieldError{Field: fmt.Sprintf("documents[%d].%s", i, f.Field), Reason: f.Reason, Message: f.Message})
			}
		}
		if len(relErrs) > 0 {
			return ptrValidation(relErrs)
		}
		for i := range prep {
			p := &prep[i]
			if !p.exists {
				continue
			}
			cur, err := getDoc(ctx, tx, project, p.name, false)
			if err != nil {
				return err
			}
			if diffs := metadataDiffs(cur, p.meta, relsList[i]); len(diffs) > 0 {
				for j := range diffs {
					diffs[j].Field = fmt.Sprintf("documents[%d].%s", i, diffs[j].Field)
				}
				return &store.MetadataDiffError{Name: p.name, Diffs: diffs}
			}
		}
		for i := range prep {
			p := &prep[i]
			if p.exists {
				continue
			}
			if err := checkSupersedesCycle(ctx, tx, p.self, relsList[i]); err != nil {
				return err
			}
			for _, t := range relsList[i] {
				if err := insertRelation(ctx, tx, projectID, p.self.id, t); err != nil {
					return err
				}
			}
		}
		for i := range prep {
			if prep[i].exists {
				continue
			}
			if err := s.reresolveRelations(ctx, tx, events, tr, at, prep[i].self); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]model.DocumentResult, len(prep))
	for i := range prep {
		r, err := s.docResult(ctx, s.pool, project, prep[i].name, prep[i].warnings)
		if err != nil {
			return nil, err
		}
		out[i] = *r
	}
	return out, nil
}

// reresolveRelations resolves the relations of imported documents that were kept as
// text and name the document that was just stored. A supersedes relation that
// would close a loop stays as text; a relation that duplicates a resolved one is
// removed.
func (s *Store) reresolveRelations(ctx context.Context, tx pgx.Tx, events *[]model.Event, tr Trace, at time.Time, self docSelf) error {
	rows, err := tx.Query(ctx, `SELECT ge.id, ge.source_id, ge.kind, d.project_id, p.key, d.name
		FROM graph_edge ge JOIN record_document d ON d.id = ge.source_id JOIN project p ON p.id = d.project_id
		WHERE ge.source_type = 'record' AND ge.status = 'confirmed' AND ge.unresolved_target_text IS NOT NULL AND ge.kind IN ('supersedes', 'related')
		  AND ((d.project_id = $1 AND ge.unresolved_target_text = $2) OR ge.unresolved_target_text = $3) ORDER BY ge.id`,
		self.projectID, self.name, self.projectKey+"/"+self.name)
	if err != nil {
		return err
	}
	type pending struct {
		id, doc    int64
		kind       string
		projectID  int64
		projectKey string
		docName    string
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.doc, &p.kind, &p.projectID, &p.projectKey, &p.docName); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var docs []int64
	meta := map[int64]pending{}
	for _, p := range todo {
		if !slices.Contains(docs, p.doc) {
			docs = append(docs, p.doc)
			meta[p.doc] = p
		}
	}
	before, err := loadRelations(ctx, tx, docs)
	if err != nil {
		return err
	}
	for _, p := range todo {
		if p.doc == self.id {
			continue
		}
		if p.kind == string(model.RelSupersedes) {
			cycle, err := supersedesReaches(ctx, tx, self.id, p.doc)
			if err != nil {
				return err
			}
			if cycle {
				continue
			}
		}
		var exists int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM graph_edge WHERE source_type = 'record' AND source_id = $1 AND kind = $2 AND target_type = 'record' AND target_id = $3 AND status IN ('confirmed', 'candidate')`,
			p.doc, p.kind, self.id).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			_, err = tx.Exec(ctx, `UPDATE graph_edge SET status = 'removed', decided_by = $2, decided_at = $3 WHERE id = $1`, p.id, tr.Actor, at)
		} else {
			_, err = tx.Exec(ctx, `UPDATE graph_edge SET target_type = 'record', target_id = $2, unresolved_target_text = NULL WHERE id = $1`, p.id, self.id)
		}
		if err != nil {
			return err
		}
	}
	after, err := loadRelations(ctx, tx, docs)
	if err != nil {
		return err
	}
	for _, id := range docs {
		if changes := relationChanges(before[id], after[id]); len(changes) > 0 {
			actID, err := insertDocumentActivityTx(ctx, tx, "record", id, tr, at, changes)
			if err != nil {
				return err
			}
			dm := meta[id]
			pid, pk := dm.projectID, dm.projectKey
			if err := emitEventTx(ctx, tx, events, at, &pid, &pk, model.EventDocumentUpdated, dm.docName, tr.Actor, tr.SessionID, map[string]any{"activity_id": actID}); err != nil {
				return err
			}
		}
	}
	return nil
}

func scanDocs(ctx context.Context, q docQ, rows pgx.Rows, snippets func(*model.Document, string)) ([]model.Document, error) {
	defer rows.Close()
	var docs []*model.Document
	var ids []int64
	for rows.Next() {
		var text string
		var extra []any
		if snippets != nil {
			extra = []any{&text}
		}
		d, id, err := scanDoc(rows, extra...)
		if err != nil {
			return nil, err
		}
		if snippets != nil {
			snippets(d, text)
		}
		docs = append(docs, d)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := fillDocs(ctx, q, docs, ids); err != nil {
		return nil, err
	}
	items := make([]model.Document, len(docs))
	for i, d := range docs {
		items[i] = *d
	}
	return items, nil
}

// CompatListDocuments returns a page of a project's documents by name, without
// their content, and the number of documents the project has.
func (s *Store) CompatListDocuments(ctx context.Context, project string, limit, offset int) ([]model.Document, int, error) {
	if _, err := s.GetProject(ctx, project); err != nil {
		return nil, 0, err
	}
	pk := model.NormalizeProjectKey(project)
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*)`+docFrom+` WHERE p.key = $1`, pk).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+docSelectCols+docFrom+` WHERE p.key = $1 ORDER BY d.name LIMIT $2 OFFSET $3`, pk, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	items, err := scanDocs(ctx, s.pool, rows, nil)
	return items, total, err
}

// CompatSearchDocuments returns a page of documents without their content and the
// number of documents that match. With a query it matches every word in the name,
// title, tags or text and attaches a snippet.
func (s *Store) CompatSearchDocuments(ctx context.Context, f store.DocumentFilter) ([]model.Document, int, error) {
	terms := searchTerms(f.Query)
	var where []string
	var args []any
	add := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if len(f.Projects) > 0 {
		where = append(where, "p.key = ANY("+add(f.Projects)+")")
	}
	if len(f.Kinds) > 0 {
		kinds := make([]string, len(f.Kinds))
		for i, k := range f.Kinds {
			kinds[i] = string(k)
		}
		where = append(where, "d.kind = ANY("+add(kinds)+")")
	}
	if len(f.States) > 0 {
		states := make([]string, len(f.States))
		for i, st := range f.States {
			states[i] = string(st)
		}
		where = append(where, "("+docStateExpr+") = ANY("+add(states)+")")
	}
	if f.Imported != nil {
		where = append(where, "d.imported = "+add(*f.Imported))
	}
	for _, t := range terms {
		p := add("%" + escapeLike(t) + "%")
		where = append(where, "(d.name ILIKE "+p+" OR d.title ILIKE "+p+" OR d.content ILIKE "+p+" OR d.tags::text ILIKE "+p+")")
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*)`+docFrom+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	cols, order := docSelectCols, ` ORDER BY d.recorded_at DESC NULLS LAST, d.name DESC, d.id DESC`
	var snip func(*model.Document, string)
	if len(terms) > 0 {
		cols += `, d.title || ' ' || d.content`
		order = ` ORDER BY d.id`
		snip = func(d *model.Document, text string) {
			sn := Snippet(text, terms)
			d.Snippet = &sn
		}
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.pool.Query(ctx, `SELECT `+cols+docFrom+cond+order+fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	items, err := scanDocs(ctx, s.pool, rows, snip)
	return items, total, err
}

type docRow struct {
	docSelf
	imported, archived bool
}

func loadDocRow(ctx context.Context, tx pgx.Tx, s *Store, project, name string) (docRow, error) {
	r := docRow{docSelf: docSelf{projectKey: model.NormalizeProjectKey(project), name: name}}
	pid, err := s.loadProjectForWrite(ctx, tx, project)
	if err != nil {
		return r, err
	}
	r.projectID = pid
	err = tx.QueryRow(ctx, `SELECT id, imported, archived FROM record_document WHERE project_id = $1 AND name = $2 FOR UPDATE`, pid, name).Scan(&r.id, &r.imported, &r.archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func touchDocument(ctx context.Context, tx pgx.Tx, id int64, actor string, at time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE record_document SET updated_at = $2, updated_by = $3 WHERE id = $1`, id, at, actor)
	return err
}

// CompatSetDocumentArchived marks a document archived or takes the mark off.
func (s *Store) CompatSetDocumentArchived(ctx context.Context, tr Trace, project, name string, archived bool) (*model.DocumentResult, error) {
	at := s.now()
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		row, err := loadDocRow(ctx, tx, s, project, name)
		if err != nil {
			return err
		}
		if row.archived == archived {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE record_document SET archived = $2 WHERE id = $1`, row.id, archived); err != nil {
			return err
		}
		if err := touchDocument(ctx, tx, row.id, tr.Actor, at); err != nil {
			return err
		}
		actID, err := insertDocumentActivityTx(ctx, tx, "record", row.id, tr, at, map[string]model.Change{"archived": {From: jsonRaw(row.archived), To: jsonRaw(archived)}})
		if err != nil {
			return err
		}
		pid, pk := row.projectID, row.projectKey
		return emitEventTx(ctx, tx, events, at, &pid, &pk, model.EventDocumentUpdated, name, tr.Actor, tr.SessionID, map[string]any{"activity_id": actID})
	})
	if err != nil {
		return nil, err
	}
	return s.docResult(ctx, s.pool, project, name, nil)
}

// CompatChangeDocumentRelation adds or removes one relation of a document.
func (s *Store) CompatChangeDocumentRelation(ctx context.Context, tr Trace, project, name string, kind model.RelationKind, target string, add bool) (*model.DocumentResult, error) {
	var warnings []model.DocumentWarning
	at := s.now()
	err := s.withTx(ctx, func(tx pgx.Tx, events *[]model.Event) error {
		row, err := loadDocRow(ctx, tx, s, project, name)
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
			if err := insertRelation(ctx, tx, row.projectID, row.id, t); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `UPDATE graph_edge SET status = 'removed', decided_by = $2, decided_at = $3
				WHERE source_type = 'record' AND source_id = $1 AND kind = $4 AND status = 'confirmed' AND (
				  (target_type = 'record' AND target_id = $5 AND $5 <> 0) OR (target_type = 'issue' AND target_id = $6 AND $6 <> 0) OR unresolved_target_text = $7)`,
				row.id, tr.Actor, at, string(kind), t.docID, t.issueID, target); err != nil {
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
		if err := touchDocument(ctx, tx, row.id, tr.Actor, at); err != nil {
			return err
		}
		actID, err := insertDocumentActivityTx(ctx, tx, "record", row.id, tr, at, changes)
		if err != nil {
			return err
		}
		pid, pk := row.projectID, row.projectKey
		return emitEventTx(ctx, tx, events, at, &pid, &pk, model.EventDocumentUpdated, name, tr.Actor, tr.SessionID, map[string]any{"activity_id": actID})
	})
	if err != nil {
		return nil, err
	}
	return s.docResult(ctx, s.pool, project, name, warnings)
}

// CompatListDocumentActivity returns a page of the activity of a document, newest
// first, and the number of records it has.
func (s *Store) CompatListDocumentActivity(ctx context.Context, project, name string, limit, offset int) ([]model.DocumentActivity, int, error) {
	pk := model.NormalizeProjectKey(project)
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT d.id FROM record_document d JOIN project p ON p.id = d.project_id WHERE p.key = $1 AND d.name = $2`, pk, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM document_activity WHERE target_type = 'record' AND target_id = $1`, id).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, actor_id, session_id, client_host, at, changes FROM document_activity WHERE target_type = 'record' AND target_id = $1 ORDER BY id DESC LIMIT $2 OFFSET $3`, id, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []model.DocumentActivity{}
	for rows.Next() {
		a := model.DocumentActivity{Project: pk, Document: name}
		var at time.Time
		var changes []byte
		if err := rows.Scan(&a.ID, &a.Actor, &a.SessionID, &a.ClientHost, &at, &changes); err != nil {
			return nil, 0, err
		}
		a.At = model.NewTime(at)
		if err := json.Unmarshal(changes, &a.Changes); err != nil {
			return nil, 0, err
		}
		items = append(items, a)
	}
	return items, total, rows.Err()
}

// FormatDocDate is the date of a document as the API shows it.
func FormatDocDate(t *time.Time) *string { return formatDocDate(t) }

// refTargets are the issue or record document an issue: or doc: reference resolved to.
type refTargets struct {
	issue, record *int64
}

func storedRefTargets(ctx context.Context, q docQ, issueID int64) (map[string]refTargets, error) {
	rows, err := q.Query(ctx, `SELECT ref, target_issue_id, target_record_id FROM issue_ref WHERE issue_id = $1`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]refTargets{}
	for rows.Next() {
		var ref string
		var t refTargets
		if err := rows.Scan(&ref, &t.issue, &t.record); err != nil {
			return nil, err
		}
		out[ref] = t
	}
	return out, rows.Err()
}

// resolveIssueRefs finds the target of each issue: and doc: reference: an issue by
// key, or a record document by name (of the project of the issue, else
// PROJECT/name). A reference in keep keeps the target it has. A new reference that
// resolves to nothing is a violation, reported for all of them at once.
func resolveIssueRefs(ctx context.Context, q docQ, projectID int64, refs []string, keep map[string]refTargets) (map[string]refTargets, error) {
	out := make(map[string]refTargets, len(refs))
	var errs []model.FieldError
	for i, ref := range refs {
		if t, ok := keep[ref]; ok {
			out[ref] = t
			continue
		}
		kind, value, _ := strings.Cut(ref, ":")
		var t refTargets
		switch kind {
		case "issue":
			var id int64
			err := q.QueryRow(ctx, `SELECT id FROM issue WHERE key = $1`, value).Scan(&id)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			if err == nil {
				t.issue = &id
			}
		case "doc":
			id, _, found, err := resolveDocument(ctx, q, projectID, "", value)
			if err != nil {
				return nil, err
			}
			if found {
				t.record = &id
			}
		default:
			continue
		}
		if t.issue == nil && t.record == nil {
			what := "이슈 " + value
			if kind == "doc" {
				what = "기록 문서 " + value
			}
			errs = append(errs, model.FieldError{Field: fmt.Sprintf("refs[%d]", i), Reason: "unresolved", Message: what + "가 없습니다. 이슈와 문서를 먼저 만들어야 참조를 걸 수 있습니다."})
			continue
		}
		out[ref] = t
	}
	return out, ptrValidation(errs)
}

func insertIssueRefs(ctx context.Context, q docQ, issueID int64, refs []string, targets map[string]refTargets) error {
	for _, ref := range refs {
		t := targets[ref]
		if _, err := q.Exec(ctx, `INSERT INTO issue_ref (issue_id, ref, target_issue_id, target_record_id) VALUES ($1, $2, $3, $4)`, issueID, ref, t.issue, t.record); err != nil {
			return err
		}
	}
	return nil
}
