package store

import (
	"reflect"
	"testing"

	"github.com/abyss-works/agent-board/internal/model"
)

func TestRemovedRelationIsKeptAndRevived(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	putDoc(t, s, trOwner, "AGB", relOld, "history", docText("decision", "옛"))
	putDoc(t, s, trOwner, "AGB", relNew, "history", docText("decision", "새", "supersedes: "+relOld))

	tr := model.Trace{Actor: "claude", SessionID: "sess-1", ClientHost: "host-1"}
	if _, err := s.ChangeDocumentRelation(ctx, tr, "AGB", relNew, model.RelSupersedes, relOld, false); err != nil {
		t.Fatal(err)
	}
	row := queryStrings(t, s, `select count(*) || '|' || ifnull(removed_by, '') || '|' || ifnull(removed_session, '') || '|' || ifnull(removed_host, '') || '|' || (removed_at is not null)
		from document_relation where kind = 'supersedes'`)
	if row[0] != "1|claude|sess-1|host-1|1" {
		t.Errorf("relation row after removal = %v", row)
	}
	removedAt := queryStrings(t, s, `select removed_at from document_relation`)[0]
	if activity, _, _ := s.ListDocumentActivity(ctx, "AGB", relNew, 50, 0); len(activity) != 1 || activity[0].At.String() != removedAt {
		t.Errorf("activity = %+v, removed_at = %s", activity, removedAt)
	}

	// Reads leave it out.
	if d, _ := s.GetDocument(ctx, "AGB", relNew); len(d.Supersedes) != 0 {
		t.Errorf("removed relation shown: %+v", d.Supersedes)
	}
	if d, _ := s.GetDocument(ctx, "AGB", relOld); d.State != model.StateCurrent || len(d.SupersededBy) != 0 {
		t.Errorf("target = %+v", d)
	}
	g, err := s.Graph(ctx, model.DocumentNodeID("AGB", relNew), 1, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range g.Edges {
		if e.Type == model.EdgeSupersedes {
			t.Errorf("removed relation in the graph: %+v", e)
		}
	}

	// Adding it again revives the same row.
	tr2 := model.Trace{Actor: "owner", SessionID: "sess-2", ClientHost: "host-2"}
	res, err := s.ChangeDocumentRelation(ctx, tr2, "AGB", relNew, model.RelSupersedes, relOld, true)
	if err != nil || !reflect.DeepEqual(res.Supersedes, []string{relOld}) {
		t.Fatalf("re-added = %+v, %v", res, err)
	}
	row = queryStrings(t, s, `select count(*) || '|' || (removed_at is null) || '|' || (removed_by is null) || '|' || (removed_session is null) || '|' || (removed_host is null)
		from document_relation`)
	if row[0] != "1|1|1|1|1" {
		t.Errorf("relation row after reviving = %v", row)
	}
	if activity, _, _ := s.ListDocumentActivity(ctx, "AGB", relNew, 50, 0); len(activity) != 2 {
		t.Errorf("activity = %d, want 2", len(activity))
	}
	if d, _ := s.GetDocument(ctx, "AGB", relOld); d.State != model.StateSuperseded {
		t.Errorf("target state = %v", d.State)
	}
}

func TestReresolveRecordsWhatItChanges(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	// The same target written two ways stays as two text relations until it exists.
	putDocArgs(t, s, trOwner, "AGB", relNote, "# 가져온 문서", imported(model.DocumentArgs{Related: []string{relOld, "AGB/" + relOld}}))
	if n := queryStrings(t, s, `select count(*) from document_relation`); n[0] != "2" {
		t.Fatalf("setup: %v", n)
	}

	tr := model.Trace{Actor: "claude", SessionID: "sess-9", ClientHost: "host-9"}
	putDoc(t, s, tr, "AGB", relOld, "history", docText("decision", "나중"))

	// Both rows stay; one resolved, one removed.
	if n := queryStrings(t, s, `select count(*) || '|' || count(removed_at) || '|' || count(target_document_id) from document_relation`); n[0] != "2|1|1" {
		t.Errorf("rows = %v", n)
	}
	if r := queryStrings(t, s, `select removed_by || '|' || removed_session || '|' || removed_host from document_relation where removed_at is not null`); r[0] != "claude|sess-9|host-9" {
		t.Errorf("removed by = %v", r)
	}
	d, _ := s.GetDocument(ctx, "AGB", relNote)
	if !reflect.DeepEqual(d.Related, []string{relOld}) || len(d.UnresolvedRefs) != 0 {
		t.Errorf("document = %+v", d)
	}
	items, _, err := s.ListDocumentActivity(ctx, "AGB", relNote, 50, 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("activity = %+v, %v", items, err)
	}
	a := items[0]
	if a.Actor != "claude" || a.SessionID != "sess-9" || a.ClientHost != "host-9" {
		t.Errorf("recorded by %s %s %s", a.Actor, a.SessionID, a.ClientHost)
	}
	if c := a.Changes["unresolved_refs"]; string(c.From) != `[{"kind":"related","target":"`+relOld+`"},{"kind":"related","target":"AGB/`+relOld+`"}]` || string(c.To) != `[]` {
		t.Errorf("unresolved_refs = %+v", c)
	}
	if c := a.Changes["related"]; string(c.From) != `[]` || string(c.To) != `["`+relOld+`"]` {
		t.Errorf("related = %+v", c)
	}
}
