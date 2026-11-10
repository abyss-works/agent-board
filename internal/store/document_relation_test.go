package store

import (
	"errors"
	"reflect"
	"testing"

	"github.com/abyss-works/agent-board/internal/model"
)

const (
	relOld  = "2026-10-01-1000-decision-old.md"
	relNew  = "2026-10-02-1000-decision-new.md"
	relMid  = "2026-10-03-1000-decision-mid.md"
	relRep  = "2026-10-04-1000-report-notes.md"
	relNote = "notes/legacy.md"
)

// violationsOf lists "field:reason" of the violations in err.
func violationsOf(err error) []string {
	var verr *model.ValidationError
	if !errors.As(err, &verr) {
		return nil
	}
	out := make([]string, len(verr.Fields))
	for i, f := range verr.Fields {
		out[i] = f.Field + ":" + f.Reason
	}
	return out
}

func imported(args model.DocumentArgs) model.DocumentArgs {
	t := true
	args.Imported = &t
	return args
}

func TestPutDocumentResolvesRelations(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "이슈"})
	putDoc(t, s, trOwner, "AGB", relOld, "history", docText("decision", "옛"))
	putDoc(t, s, trOwner, "AGB", relRep, "history", docText("report", "보고"))

	d, _ := putDoc(t, s, trClaude, "AGB", relNew, "history", docText("decision", "새", "supersedes: "+relOld, "related: ["+relRep+"]", "issue: AGB-1"))
	if !reflect.DeepEqual(d.Supersedes, []string{relOld}) || !reflect.DeepEqual(d.Related, []string{relRep}) || !reflect.DeepEqual(d.Issues, []string{"AGB-1"}) ||
		len(d.UnresolvedRefs) != 0 || d.SupersededBy == nil || len(d.SupersededBy) != 0 || d.State != model.StateCurrent {
		t.Errorf("new document = %+v", d)
	}
	old, err := s.GetDocument(ctx, "AGB", relOld)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old.SupersededBy, []string{relNew}) || old.State != model.StateSuperseded || old.Supersedes == nil || old.UnresolvedRefs == nil {
		t.Errorf("superseded document = %+v", old)
	}
	// related and about do not touch the state of their targets.
	if rep, _ := s.GetDocument(ctx, "AGB", relRep); rep.State != model.StateCurrent || len(rep.SupersededBy) != 0 {
		t.Errorf("related target = %+v", rep)
	}
	// A repeated target is one relation.
	dup, _ := putDoc(t, s, trClaude, "AGB", relMid, "history", docText("decision", "중", "related: ["+relOld+", "+relOld+"]"))
	if !reflect.DeepEqual(dup.Related, []string{relOld}) {
		t.Errorf("related = %v", dup.Related)
	}
}

func TestPutDocumentRefusesUnresolvedRelationsOfARecord(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	putDoc(t, s, trOwner, "OTH", relOld, "history", docText("decision", "다른 프로젝트"))
	_, _, err := s.PutDocument(ctx, trClaude, "AGB", relNew, DocumentInput{Content: docText("decision", "새",
		"supersedes: "+relOld, "related: [x.md, "+relRep+"]", "issue: AGB-9")})
	want := []string{"supersedes[0]:unresolved", "related[0]:unresolved", "related[1]:unresolved", "issues[0]:unresolved"}
	if got := violationsOf(err); !reflect.DeepEqual(got, want) {
		t.Errorf("violations = %v (%v), want %v", got, err, want)
	}
	// Nothing was stored.
	if _, err := s.GetDocument(ctx, "AGB", relNew); !errors.Is(err, ErrNotFound) {
		t.Errorf("a refused put left a document: %v", err)
	}
	// Model and relation violations come together.
	_, _, err = s.PutDocument(ctx, trClaude, "AGB", relNew, DocumentInput{Content: "본문", Args: model.DocumentArgs{Supersedes: []string{"x.md", "문장 섞임"}}})
	if got, want := violationsOf(err), []string{"title:required", "supersedes[1]:not_a_name", "supersedes[0]:unresolved"}; !reflect.DeepEqual(got, want) {
		t.Errorf("violations = %v, want %v", got, want)
	}
}

func TestPutDocumentCrossProjectRelations(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	putDoc(t, s, trOwner, "OTH", relOld, "history", docText("decision", "다른 프로젝트"))

	// A bare name is the same project only, even when the name is unique.
	if _, _, err := s.PutDocument(ctx, trClaude, "AGB", relNew, DocumentInput{Content: docText("decision", "새", "related: "+relOld)}); len(violationsOf(err)) != 1 {
		t.Errorf("a bare name must not reach another project: %v", err)
	}
	d, _ := putDoc(t, s, trClaude, "AGB", relNew, "history", docText("decision", "새", "supersedes: OTH/"+relOld))
	if !reflect.DeepEqual(d.Supersedes, []string{"OTH/" + relOld}) {
		t.Errorf("supersedes = %v", d.Supersedes)
	}
	old, _ := s.GetDocument(ctx, "OTH", relOld)
	if old.State != model.StateSuperseded || !reflect.DeepEqual(old.SupersededBy, []string{"AGB/" + relNew}) {
		t.Errorf("target in the other project = %+v", old)
	}
	// The explicit form of the own project is the bare name.
	again, _ := putDoc(t, s, trClaude, "AGB", relNew, "history", docText("decision", "새", "supersedes: OTH/"+relOld))
	if again.Supersedes[0] != "OTH/"+relOld {
		t.Errorf("again = %v", again.Supersedes)
	}
	putDoc(t, s, trClaude, "AGB", relMid, "history", docText("decision", "중", "related: AGB/"+relNew))
	mid, _ := s.GetDocument(ctx, "AGB", relMid)
	if !reflect.DeepEqual(mid.Related, []string{relNew}) {
		t.Errorf("an explicit own project must read as the bare name: %v", mid.Related)
	}
	// Names with slashes of imported documents still resolve before the project form.
	putDocArgs(t, s, trClaude, "AGB", "OTH/x.md", "# x", imported(model.DocumentArgs{}))
	d, _ = putDoc(t, s, trClaude, "AGB", relRep, "history", docText("report", "보고", "related: OTH/x.md"))
	if !reflect.DeepEqual(d.Related, []string{"OTH/x.md"}) || len(d.UnresolvedRefs) != 0 {
		t.Errorf("a path name must win over the project form: %+v", d)
	}
}

func TestPutDocumentRefusesSelfReference(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	for label, args := range map[string]model.DocumentArgs{
		"record":   {},
		"imported": imported(model.DocumentArgs{}),
	} {
		for _, target := range []string{relNew, "AGB/" + relNew} {
			for _, kind := range []string{"supersedes", "related"} {
				content := docText("decision", "자기 참조", kind+": "+target)
				_, _, err := s.PutDocument(ctx, trClaude, "AGB", relNew, DocumentInput{Content: content, Args: args})
				if got, want := violationsOf(err), []string{kind + "[0]:self_reference"}; !reflect.DeepEqual(got, want) {
					t.Errorf("%s %s %s: violations = %v (%v)", label, kind, target, got, err)
				}
			}
		}
	}
	// An existing document is the same.
	putDoc(t, s, trClaude, "AGB", relNew, "history", docText("decision", "자기 참조 없음"))
	if _, err := s.ChangeDocumentRelation(ctx, trClaude, "AGB", relNew, model.RelRelated, relNew, true); !reflect.DeepEqual(violationsOf(err), []string{"target:self_reference"}) {
		t.Errorf("relation to itself: %v", err)
	}
}

func TestImportedDocumentKeepsUnresolvedRelations(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	content := "---\ntitle: 가져온 문서\nsupersedes: " + relOld + "\nrelated: [" + relRep + ", 일부 문장]\nissue: AGB-9\n---\n본문\n"
	res, created, err := s.PutDocument(ctx, trClaude, "AGB", relNote, DocumentInput{Content: content, Args: imported(model.DocumentArgs{})})
	if err != nil || !created {
		t.Fatalf("PutDocument = %+v, %v, %v", res, created, err)
	}
	wantRefs := []model.UnresolvedRef{{Kind: model.RelSupersedes, Target: relOld}, {Kind: model.RelRelated, Target: relRep},
		{Kind: model.RelRelated, Target: "일부 문장"}, {Kind: model.RelAbout, Target: "AGB-9"}}
	if !reflect.DeepEqual(res.UnresolvedRefs, wantRefs) || len(res.Supersedes) != 0 || len(res.Related) != 0 || len(res.Issues) != 0 {
		t.Errorf("unresolved = %+v, document = %+v", res.UnresolvedRefs, res.Document)
	}
	var fields []string
	for _, w := range res.Warnings {
		if w.Reason != "unresolved" || w.Message == "" {
			t.Errorf("warning = %+v", w)
		}
		fields = append(fields, w.Field)
	}
	if want := []string{"supersedes[0]", "related[0]", "related[1]", "issues[0]"}; !reflect.DeepEqual(fields, want) {
		t.Errorf("warning fields = %v, want %v", fields, want)
	}

	// The same put again is the same document, with the same warnings.
	again, created, err := s.PutDocument(ctx, trClaude, "AGB", relNote, DocumentInput{Content: content, Args: imported(model.DocumentArgs{})})
	if err != nil || created || len(again.Warnings) != 4 {
		t.Errorf("repeated put = %+v, %v, %v", again, created, err)
	}

	// Registering the target resolves the relation. The issue key stays text.
	putDoc(t, s, trClaude, "AGB", relOld, "history", docText("decision", "옛"))
	got, _ := s.GetDocument(ctx, "AGB", relNote)
	if !reflect.DeepEqual(got.Supersedes, []string{relOld}) || len(got.UnresolvedRefs) != 3 {
		t.Errorf("after the target was stored: %+v", got)
	}
	old, _ := s.GetDocument(ctx, "AGB", relOld)
	if old.State != model.StateSuperseded || !reflect.DeepEqual(old.SupersededBy, []string{relNote}) {
		t.Errorf("target = %+v", old)
	}
	// Still the same document for a put: its metadata matches what is stored.
	if res, _, err := s.PutDocument(ctx, trClaude, "AGB", relNote, DocumentInput{Content: content, Args: imported(model.DocumentArgs{})}); err != nil || len(res.Warnings) != 3 {
		t.Errorf("put after resolving = %+v, %v", res, err)
	}
}

func TestReresolutionIsPerProjectAndKeepsLoopsOpen(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	pending := func(project, name, rel string) {
		t.Helper()
		putDocArgs(t, s, trClaude, project, name, "# "+name+"\n"+"", imported(model.DocumentArgs{Related: []string{rel}}))
	}
	// A bare name waits for a document of its own project only; PROJECT/name for that project.
	pending("OTH", "bare.md", relOld)
	pending("OTH", "explicit.md", "AGB/"+relOld)
	putDoc(t, s, trClaude, "AGB", relOld, "history", docText("decision", "옛"))
	bare, _ := s.GetDocument(ctx, "OTH", "bare.md")
	explicit, _ := s.GetDocument(ctx, "OTH", "explicit.md")
	if len(bare.Related) != 0 || len(bare.UnresolvedRefs) != 1 {
		t.Errorf("a bare name must not resolve across projects: %+v", bare)
	}
	if !reflect.DeepEqual(explicit.Related, []string{"AGB/" + relOld}) || len(explicit.UnresolvedRefs) != 0 {
		t.Errorf("PROJECT/name must resolve: %+v", explicit)
	}

	// A supersedes that would close a loop stays unresolved.
	putDocArgs(t, s, trClaude, "AGB", "a.md", "# a", imported(model.DocumentArgs{Supersedes: []string{"b.md"}}))
	putDocArgs(t, s, trClaude, "AGB", "b.md", "# b", imported(model.DocumentArgs{Supersedes: []string{"a.md"}}))
	a, _ := s.GetDocument(ctx, "AGB", "a.md")
	b, _ := s.GetDocument(ctx, "AGB", "b.md")
	// b names a.md, which exists, so the put resolves it; a's pending relation to b would close the loop.
	if len(a.Supersedes) != 0 || len(a.UnresolvedRefs) != 1 || !reflect.DeepEqual(b.Supersedes, []string{"a.md"}) {
		t.Errorf("a = %+v, b = %+v", a, b)
	}
	if hasLoop(t, s) {
		t.Error("supersedes relations form a loop")
	}
}

func hasLoop(t *testing.T, s *Store) bool {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`with recursive r(start, id) as (
		select document_id, target_document_id from document_relation where kind = 'supersedes' and target_document_id is not null
		union select r.start, dr.target_document_id from document_relation dr join r on dr.document_id = r.id
		where dr.kind = 'supersedes' and dr.target_document_id is not null)
		select count(*) from r where start = id`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func TestSupersedesCycleIsRefused(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	putDoc(t, s, trClaude, "AGB", relOld, "history", docText("decision", "옛"))
	putDoc(t, s, trClaude, "AGB", relNew, "history", docText("decision", "새", "supersedes: "+relOld))
	putDoc(t, s, trClaude, "AGB", relMid, "history", docText("decision", "중", "supersedes: "+relNew))

	// old -> mid would close old <- new <- mid.
	if _, err := s.ChangeDocumentRelation(ctx, trClaude, "AGB", relOld, model.RelSupersedes, relMid, true); !errors.Is(err, ErrSupersedesCycle) {
		t.Errorf("indirect loop err = %v", err)
	}
	if _, err := s.ChangeDocumentRelation(ctx, trClaude, "AGB", relOld, model.RelSupersedes, relNew, true); !errors.Is(err, ErrSupersedesCycle) {
		t.Errorf("direct loop err = %v", err)
	}
	// As related it is fine: only supersedes must not loop.
	if _, err := s.ChangeDocumentRelation(ctx, trClaude, "AGB", relOld, model.RelRelated, relMid, true); err != nil {
		t.Errorf("related loop err = %v", err)
	}
	if hasLoop(t, s) {
		t.Error("supersedes relations form a loop")
	}
	var n int
	s.db.QueryRow(`select count(*) from document_activity`).Scan(&n)
	if n != 1 {
		t.Errorf("document activity = %d; only the accepted change counts", n)
	}
}

func TestPutDocumentSameMetadataIsTheSameDocument(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	putDoc(t, s, trClaude, "AGB", relOld, "history", docText("decision", "옛"))
	putDoc(t, s, trClaude, "AGB", relRep, "history", docText("report", "보고"))
	content := docText("decision", "새", "tags: [a, b]", "supersedes: "+relOld, "related: ["+relRep+"]")
	first, _ := putDoc(t, s, trClaude, "AGB", relNew, "history", content)

	// The same metadata by argument instead of front matter, in another order.
	again, created := putDocArgs(t, s, trOwner, "AGB", relNew, content, model.DocumentArgs{Related: []string{relRep}, Supersedes: []string{relOld}})
	if created || !reflect.DeepEqual(again, first) {
		t.Errorf("again = %+v (created %v), want %+v", again, created, first)
	}
}

func TestPutDocumentMetadataDiffers(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "이슈"})
	putDoc(t, s, trClaude, "AGB", relOld, "history", docText("decision", "옛"))
	content := docText("decision", "새", "tags: [a]", "date: 2026-10-02 10:00", "related: "+relOld)
	putDoc(t, s, trClaude, "AGB", relNew, "history", content)

	title, date, path, kind := "다른 제목", "2026-10-03", "x/y.md", "decision"
	tr, fl := true, false
	cases := map[string]struct {
		args model.DocumentArgs
		want []string
	}{
		"title":      {model.DocumentArgs{Title: &title}, []string{"title"}},
		"date":       {model.DocumentArgs{Date: &date}, []string{"date"}},
		"tags":       {model.DocumentArgs{Tags: []string{}}, []string{"tags"}},
		"path":       {model.DocumentArgs{Path: &path}, []string{"path"}},
		"archived":   {model.DocumentArgs{Archived: &tr}, []string{"archived"}},
		"imported":   {model.DocumentArgs{Imported: &tr}, []string{"imported"}},
		"related":    {model.DocumentArgs{Related: []string{}}, []string{"related"}},
		"supersedes": {model.DocumentArgs{Supersedes: []string{relOld}}, []string{"supersedes"}},
		"issues":     {model.DocumentArgs{Issues: []string{"AGB-1"}}, []string{"issues"}},
		"several":    {model.DocumentArgs{Title: &title, Archived: &tr, Tags: []string{"z"}}, []string{"title", "tags", "archived"}},
		"same":       {model.DocumentArgs{Kind: &kind, Archived: &fl}, nil},
	}
	for label, c := range cases {
		_, _, err := s.PutDocument(ctx, trClaude, "AGB", relNew, DocumentInput{Content: content, Args: c.args})
		var diff *MetadataDiffError
		if c.want == nil {
			if err != nil {
				t.Errorf("%s: %v", label, err)
			}
			continue
		}
		// An imported flag that differs makes the name a path name, which a record name also is.
		if !errors.As(err, &diff) {
			t.Errorf("%s: err = %v, want a MetadataDiffError", label, err)
			continue
		}
		var got []string
		for _, d := range diff.Diffs {
			got = append(got, d.Field)
			if d.Stored == "" || d.Sent == "" {
				t.Errorf("%s: diff %+v lacks a value", label, d)
			}
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: fields = %v, want %v", label, got, c.want)
		}
	}
	// Other content comes before the metadata check.
	var imm *ImmutableError
	if _, _, err := s.PutDocument(ctx, trClaude, "AGB", relNew, DocumentInput{Content: content + "x", Args: model.DocumentArgs{Title: &title}}); !errors.As(err, &imm) {
		t.Errorf("other content err = %v", err)
	}
	// A violation comes before both.
	if _, _, err := s.PutDocument(ctx, trClaude, "AGB", relNew, DocumentInput{Content: content + "x", Args: model.DocumentArgs{Kind: strPtr("report")}}); len(violationsOf(err)) != 1 {
		t.Errorf("violation err = %v", err)
	}
}

func strPtr(s string) *string { return &s }

func TestDocumentStatePriority(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	putDoc(t, s, trClaude, "AGB", relOld, "history", docText("decision", "옛"))
	putDoc(t, s, trClaude, "AGB", relNew, "history", docText("decision", "새", "supersedes: "+relOld))
	state := func(name string) model.DocumentState {
		d, err := s.GetDocument(ctx, "AGB", name)
		if err != nil {
			t.Fatal(err)
		}
		return d.State
	}
	if state(relOld) != model.StateSuperseded || state(relNew) != model.StateCurrent {
		t.Fatal("setup")
	}
	if _, err := s.SetDocumentArchived(ctx, trClaude, "AGB", relOld, true); err != nil {
		t.Fatal(err)
	}
	if state(relOld) != model.StateArchived {
		t.Errorf("archived beats superseded: %v", state(relOld))
	}
	// The superseding document's own state does not matter.
	if _, err := s.SetDocumentArchived(ctx, trClaude, "AGB", relNew, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetDocumentArchived(ctx, trClaude, "AGB", relOld, false); err != nil {
		t.Fatal(err)
	}
	if state(relOld) != model.StateSuperseded {
		t.Errorf("an archived superseding document still supersedes: %v", state(relOld))
	}
	// Removing the relation makes it current again.
	if _, err := s.ChangeDocumentRelation(ctx, trClaude, "AGB", relNew, model.RelSupersedes, relOld, false); err != nil {
		t.Fatal(err)
	}
	if state(relOld) != model.StateCurrent {
		t.Errorf("after removing the relation: %v", state(relOld))
	}
}

func TestSetDocumentArchivedRecordsActivity(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	first, _ := putDoc(t, s, trOwner, "AGB", relOld, "history", docText("decision", "옛"))

	res, err := s.SetDocumentArchived(ctx, trClaude, "AGB", relOld, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Archived || res.State != model.StateArchived || res.Warnings == nil || res.Content != nil ||
		res.UpdatedBy != "claude" || !first.UpdatedAt.Before(res.UpdatedAt) || res.CreatedBy != "owner" || res.CreatedAt != first.CreatedAt {
		t.Errorf("archived = %+v", res)
	}
	// Setting it again changes nothing and records nothing.
	again, err := s.SetDocumentArchived(ctx, trOwner, "AGB", relOld, true)
	if err != nil || again.UpdatedBy != "claude" || again.UpdatedAt != res.UpdatedAt {
		t.Errorf("again = %+v, %v", again, err)
	}
	if _, err := s.SetDocumentArchived(ctx, trOwner, "AGB", relOld, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetDocumentArchived(ctx, trOwner, "AGB", relOld, false); err != nil {
		t.Fatal(err)
	}
	items, total, err := s.ListDocumentActivity(ctx, "AGB", relOld, 50, 0)
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("activity = %+v (%d), %v", items, total, err)
	}
	// Newest first, with the trace of the request.
	a := items[0]
	if a.Actor != "owner" || a.SessionID != "s-owner" || a.ClientHost != "pc" || a.Project != "AGB" || a.Document != relOld || a.ID <= items[1].ID {
		t.Errorf("newest = %+v", a)
	}
	if c, ok := a.Changes["archived"]; !ok || string(c.From) != "true" || string(c.To) != "false" || len(a.Changes) != 1 {
		t.Errorf("changes = %+v", a.Changes)
	}
	if c := items[1].Changes["archived"]; string(c.From) != "false" || string(c.To) != "true" || items[1].Actor != "claude" {
		t.Errorf("oldest = %+v", items[1])
	}
	page, total, _ := s.ListDocumentActivity(ctx, "AGB", relOld, 1, 1)
	if total != 2 || len(page) != 1 || page[0].ID != items[1].ID {
		t.Errorf("page = %+v (%d)", page, total)
	}
	// No issue activity, and none for a document that was only put.
	var n int
	s.db.QueryRow(`select count(*) from activity`).Scan(&n)
	if n != 0 {
		t.Errorf("issue activity = %d", n)
	}
	putDoc(t, s, trOwner, "AGB", relNew, "history", docText("decision", "새"))
	if items, total, err := s.ListDocumentActivity(ctx, "AGB", relNew, 50, 0); err != nil || total != 0 || items == nil || len(items) != 0 {
		t.Errorf("activity of a document without changes = %#v (%d), %v", items, total, err)
	}
	// Missing targets and inactive projects.
	if _, err := s.SetDocumentArchived(ctx, trOwner, "AGB", "x.md", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing document err = %v", err)
	}
	if _, err := s.SetDocumentArchived(ctx, trOwner, "NOPE", relOld, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing project err = %v", err)
	}
	if _, _, err := s.ListDocumentActivity(ctx, "AGB", "x.md", 50, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("activity of a missing document err = %v", err)
	}
	archived := model.ProjectArchived
	if _, err := s.UpdateProject(ctx, trOwner, "AGB", ProjectPatch{Status: &archived}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetDocumentArchived(ctx, trOwner, "AGB", relOld, true); !errors.Is(err, ErrProjectNotActive) {
		t.Errorf("inactive project err = %v", err)
	}
	if _, err := s.ChangeDocumentRelation(ctx, trOwner, "AGB", relOld, model.RelRelated, relNew, true); !errors.Is(err, ErrProjectNotActive) {
		t.Errorf("relation in an inactive project err = %v", err)
	}
}

func TestChangeDocumentRelation(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "이슈"})
	putDoc(t, s, trOwner, "AGB", relOld, "history", docText("decision", "옛"))
	putDoc(t, s, trOwner, "AGB", relNew, "history", docText("decision", "새", "related: "+relOld))

	res, err := s.ChangeDocumentRelation(ctx, trClaude, "AGB", relNew, model.RelSupersedes, relOld, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Supersedes, []string{relOld}) || !reflect.DeepEqual(res.Related, []string{relOld}) || res.UpdatedBy != "claude" || len(res.Warnings) != 0 {
		t.Errorf("added = %+v", res)
	}
	if old, _ := s.GetDocument(ctx, "AGB", relOld); old.State != model.StateSuperseded {
		t.Errorf("target state = %v", old.State)
	}
	// An issue.
	if res, err = s.ChangeDocumentRelation(ctx, trClaude, "AGB", relNew, model.RelAbout, "AGB-1", true); err != nil || !reflect.DeepEqual(res.Issues, []string{"AGB-1"}) {
		t.Errorf("about = %+v, %v", res, err)
	}
	// Adding what is there changes nothing.
	same, err := s.ChangeDocumentRelation(ctx, trOwner, "AGB", relNew, model.RelAbout, "AGB-1", true)
	if err != nil || same.UpdatedBy != "claude" || same.UpdatedAt != res.UpdatedAt {
		t.Errorf("same = %+v, %v", same, err)
	}
	// Removing.
	if res, err = s.ChangeDocumentRelation(ctx, trClaude, "AGB", relNew, model.RelRelated, relOld, false); err != nil || len(res.Related) != 0 || !reflect.DeepEqual(res.Supersedes, []string{relOld}) {
		t.Errorf("removed related = %+v, %v", res, err)
	}
	// Removing what is not there is fine and records nothing.
	before, _, _ := s.ListDocumentActivity(ctx, "AGB", relNew, 50, 0)
	if _, err = s.ChangeDocumentRelation(ctx, trClaude, "AGB", relNew, model.RelRelated, "nothing.md", false); err != nil {
		t.Errorf("removing a missing relation: %v", err)
	}
	if after, _, _ := s.ListDocumentActivity(ctx, "AGB", relNew, 50, 0); len(after) != len(before) {
		t.Errorf("a removal without effect was recorded")
	}

	items, total, _ := s.ListDocumentActivity(ctx, "AGB", relNew, 50, 0)
	if total != 3 {
		t.Fatalf("activity = %d, want 3", total)
	}
	// Newest first: the removal, the issue, the supersedes. Lists are whole lists.
	if c := items[0].Changes["related"]; string(c.From) != `["`+relOld+`"]` || string(c.To) != `[]` || len(items[0].Changes) != 1 {
		t.Errorf("removal changes = %+v", items[0].Changes)
	}
	if c := items[1].Changes["issues"]; string(c.From) != `[]` || string(c.To) != `["AGB-1"]` {
		t.Errorf("issue changes = %+v", items[1].Changes)
	}
	if c := items[2].Changes["supersedes"]; string(c.From) != `[]` || string(c.To) != `["`+relOld+`"]` {
		t.Errorf("supersedes changes = %+v", items[2].Changes)
	}

	// Validation of the target, as for a put.
	for label, c := range map[string]struct {
		kind   model.RelationKind
		target string
		want   []string
	}{
		"unresolved document": {model.RelRelated, "x.md", []string{"target:unresolved"}},
		"unresolved issue":    {model.RelAbout, "AGB-9", []string{"target:unresolved"}},
		"a sentence":          {model.RelSupersedes, "일부 문장", []string{"target:not_a_name"}},
		"self":                {model.RelRelated, relNew, []string{"target:self_reference"}},
	} {
		if _, err := s.ChangeDocumentRelation(ctx, trClaude, "AGB", relNew, c.kind, c.target, true); !reflect.DeepEqual(violationsOf(err), c.want) {
			t.Errorf("%s: err = %v", label, err)
		}
	}
	if _, err := s.ChangeDocumentRelation(ctx, trClaude, "AGB", "x.md", model.RelRelated, relOld, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing document err = %v", err)
	}
}

func TestChangeDocumentRelationOfAnImportedDocument(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	putDocArgs(t, s, trOwner, "AGB", relNote, "# 가져온 문서", imported(model.DocumentArgs{}))

	res, err := s.ChangeDocumentRelation(ctx, trClaude, "AGB", relNote, model.RelRelated, "later.md", true)
	if err != nil || len(res.Related) != 0 || !reflect.DeepEqual(res.UnresolvedRefs, []model.UnresolvedRef{{Kind: model.RelRelated, Target: "later.md"}}) ||
		len(res.Warnings) != 1 || res.Warnings[0].Field != "target" || res.Warnings[0].Reason != "unresolved" {
		t.Fatalf("added unresolved = %+v, %v", res, err)
	}
	// A sentence is kept too; it is no name.
	if res, err = s.ChangeDocumentRelation(ctx, trClaude, "AGB", relNote, model.RelSupersedes, "일부 문장", true); err != nil || len(res.UnresolvedRefs) != 2 {
		t.Errorf("sentence = %+v, %v", res, err)
	}
	items, _, _ := s.ListDocumentActivity(ctx, "AGB", relNote, 50, 0)
	if c := items[1].Changes["unresolved_refs"]; string(c.From) != `[]` || string(c.To) != `[{"kind":"related","target":"later.md"}]` || len(items[1].Changes) != 1 {
		t.Errorf("changes = %+v", items[1].Changes)
	}
	// The target comes, and the relation resolves without a record.
	putDoc(t, s, trClaude, "AGB", relOld, "history", docText("decision", "옛"))
	if _, err := s.ChangeDocumentRelation(ctx, trClaude, "AGB", relNote, model.RelRelated, relOld, true); err != nil {
		t.Fatal(err)
	}
	// Removing a text relation by its text.
	if res, err = s.ChangeDocumentRelation(ctx, trClaude, "AGB", relNote, model.RelRelated, "later.md", false); err != nil || len(res.UnresolvedRefs) != 1 || !reflect.DeepEqual(res.Related, []string{relOld}) {
		t.Errorf("removed text = %+v, %v", res, err)
	}
	if res, err = s.ChangeDocumentRelation(ctx, trClaude, "AGB", relNote, model.RelSupersedes, "일부 문장", false); err != nil || len(res.UnresolvedRefs) != 0 {
		t.Errorf("removed sentence = %+v, %v", res, err)
	}
}
