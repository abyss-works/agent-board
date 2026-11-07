package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// ErrSupersedesCycle reports a supersedes relation that would lead back to
// the document that holds it. HTTP maps it to 409.
var ErrSupersedesCycle = errors.New("supersedes cycle")

// docSelf identifies the document whose relations are resolved. id is 0 while
// the document is not stored yet.
type docSelf struct {
	id         int64
	projectID  int64
	projectKey string
	name       string
}

// relTarget is a relation to write: a document, an issue or, when neither
// exists, the text as given. display is the name or key a document object
// shows, or the text.
type relTarget struct {
	kind    model.RelationKind
	docID   int64
	issueID int64
	text    string
	display string
}

func (t relTarget) resolved() bool { return t.docID != 0 || t.issueID != 0 }

// resolveDocument finds the document a relation names. A bare name is a document
// of the same project; PROJECT/name is a document of that project. display is the
// bare name for the same project and PROJECT/name for another.
func resolveDocument(ctx context.Context, q querier, projectID int64, projectKey, raw string) (id int64, display string, found bool, err error) {
	err = q.QueryRowContext(ctx, `select id from document where project_id = ? and name = ?`, projectID, raw).Scan(&id)
	if err == nil {
		return id, raw, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, err
	}
	key, rest, ok := strings.Cut(raw, "/")
	if !ok || key == "" || rest == "" {
		return 0, "", false, nil
	}
	err = q.QueryRowContext(ctx, `select d.id from document d join project p on p.id = d.project_id where p.key = ? and d.name = ?`, key, rest).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
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

// resolveTarget resolves one relation target. It does not check the name form
// or self references.
func resolveTarget(ctx context.Context, q querier, self docSelf, kind model.RelationKind, raw string) (relTarget, error) {
	t := relTarget{kind: kind, text: raw, display: raw}
	if kind == model.RelAbout {
		err := q.QueryRowContext(ctx, `select id from issue where key = ?`, raw).Scan(&t.issueID)
		if errors.Is(err, sql.ErrNoRows) {
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

func (self docSelf) refersToSelf(raw string, t relTarget) bool {
	return raw == self.name || raw == self.projectKey+"/"+self.name || (self.id != 0 && t.docID == self.id)
}

// relationField is the request field a relation kind is given in.
func relationField(kind model.RelationKind) string {
	if kind == model.RelAbout {
		return "issues"
	}
	return string(kind)
}

// resolveRelations resolves the relations a put request names. A target that
// does not exist is a violation (reason unresolved) for a document that is
// not imported, and for an imported one a relation kept as text with a warning.
// A target that is the document itself is a violation either way. Repeated
// targets are merged. A target that is not a document name in a document that
// is not imported is left to the model validation.
func resolveRelations(ctx context.Context, q querier, self docSelf, meta model.DocumentMeta) (rels []relTarget, warnings []model.DocumentWarning, errs []model.FieldError, err error) {
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

func unresolvedMessage(kind model.RelationKind, raw string) string {
	if kind == model.RelAbout {
		return fmt.Sprintf("이슈 %s가 없습니다.", raw)
	}
	return fmt.Sprintf("같은 프로젝트에 문서 %s가 없습니다. 다른 프로젝트의 문서는 PROJECT/이름으로 씁니다.", raw)
}

// supersedesReaches reports whether following supersedes relations from the
// document from leads to the document to, or from == to.
func supersedesReaches(ctx context.Context, q querier, from, to int64) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `with recursive r(id) as (
		select ? union select dr.target_document_id from document_relation dr join r on dr.document_id = r.id
		where dr.kind = 'supersedes' and dr.target_document_id is not null
	) select count(*) from r where id = ?`, from, to).Scan(&n)
	return n > 0, err
}

// checkSupersedesCycle fails with ErrSupersedesCycle when a supersedes
// relation of the document self leads back to it.
func checkSupersedesCycle(ctx context.Context, q querier, self docSelf, rels []relTarget) error {
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
			return ErrSupersedesCycle
		}
	}
	return nil
}

func insertRelation(ctx context.Context, q querier, docID int64, t relTarget) error {
	var doc, issue, text any
	switch {
	case t.docID != 0:
		doc = t.docID
	case t.issueID != 0:
		issue = t.issueID
	default:
		text = t.text
	}
	_, err := q.ExecContext(ctx, `insert or ignore into document_relation(document_id, kind, target_document_id, target_issue_id, target_text)
		values (?, ?, ?, ?, ?)`, docID, string(t.kind), doc, issue, text)
	return err
}

// reresolveRelations resolves the relations of imported documents that were
// kept as text and name the document that was just stored: the bare name in
// the same project, or PROJECT/name. A supersedes relation that would close a
// loop stays as text.
func reresolveRelations(ctx context.Context, q querier, self docSelf) error {
	rows, err := q.QueryContext(ctx, `select r.id, r.document_id, r.kind from document_relation r join document d on d.id = r.document_id
		where r.target_text is not null and r.kind in ('supersedes', 'related')
		and ((d.project_id = ? and r.target_text = ?) or r.target_text = ?) order by r.id`,
		self.projectID, self.name, self.projectKey+"/"+self.name)
	if err != nil {
		return err
	}
	type pending struct {
		id, doc int64
		kind    string
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.doc, &p.kind); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range todo {
		if p.doc == self.id {
			continue
		}
		if p.kind == string(model.RelSupersedes) {
			cycle, err := supersedesReaches(ctx, q, self.id, p.doc)
			if err != nil {
				return err
			}
			if cycle {
				continue
			}
		}
		var exists int
		if err := q.QueryRowContext(ctx, `select count(*) from document_relation where document_id = ? and kind = ? and target_document_id = ?`,
			p.doc, p.kind, self.id).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			_, err = q.ExecContext(ctx, `delete from document_relation where id = ?`, p.id)
		} else {
			_, err = q.ExecContext(ctx, `update document_relation set target_document_id = ?, target_text = null where id = ?`, self.id, p.id)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// docRelations are the relations of one document as a document object shows
// them.
type docRelations struct {
	supersedes, related, issues, supersededBy []string
	unresolved                                []model.UnresolvedRef
}

// loadRelations reads the relations of the documents with the given ids, in the
// order they were written, and the documents that supersede them.
func loadRelations(ctx context.Context, q querier, ids []int64) (map[int64]*docRelations, error) {
	out := make(map[int64]*docRelations, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
		out[id] = &docRelations{supersedes: []string{}, related: []string{}, issues: []string{}, supersededBy: []string{}, unresolved: []model.UnresolvedRef{}}
	}
	in := placeholders(len(ids))
	rows, err := q.QueryContext(ctx, `select r.document_id, r.kind, dp.key, tp.key, td.name, ti.key, r.target_text
		from document_relation r
		join document d on d.id = r.document_id join project dp on dp.id = d.project_id
		left join document td on td.id = r.target_document_id left join project tp on tp.id = td.project_id
		left join issue ti on ti.id = r.target_issue_id
		where r.document_id in (`+in+`) order by r.id`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var kind string
		var dp string
		var tp, tdName, tiKey, text sql.NullString
		if err := rows.Scan(&id, &kind, &dp, &tp, &tdName, &tiKey, &text); err != nil {
			rows.Close()
			return nil, err
		}
		r := out[id]
		switch {
		case text.Valid:
			r.unresolved = append(r.unresolved, model.UnresolvedRef{Kind: model.RelationKind(kind), Target: text.String})
		case tiKey.Valid:
			r.issues = append(r.issues, tiKey.String)
		default:
			name := tdName.String
			if tp.String != dp {
				name = tp.String + "/" + name
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
	rows, err = q.QueryContext(ctx, `select r.target_document_id, sp.key, sd.name, tp.key
		from document_relation r
		join document sd on sd.id = r.document_id join project sp on sp.id = sd.project_id
		join document td on td.id = r.target_document_id join project tp on tp.id = td.project_id
		where r.kind = 'supersedes' and r.target_document_id in (`+in+`) order by r.id`, args...)
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

// relationSet is the relations of one document as sets of texts, to compare
// what a request says with what is stored.
func (r *docRelations) sets() map[string][]string {
	norm := func(l []string) []string {
		l = slices.Clone(l)
		slices.Sort(l)
		return slices.Compact(l)
	}
	sets := map[string][]string{
		"supersedes": slices.Clone(r.supersedes), "related": slices.Clone(r.related), "issues": slices.Clone(r.issues),
	}
	for _, u := range r.unresolved {
		field := relationField(u.Kind)
		sets[field] = append(sets[field], u.Target)
	}
	for k, v := range sets {
		sets[k] = norm(v)
	}
	return sets
}

// requestedSets is the same for the relations a put request names.
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
