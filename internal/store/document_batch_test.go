package store

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

const (
	batchA = "2026-10-10-1000-decision-alpha.md"
	batchB = "2026-10-10-1001-decision-beta.md"
	batchC = "2026-10-10-1002-decision-gamma.md"
)

func TestPutDocumentsBatchMutualAndCyclicRelated(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "PROJ")

	res, err := s.PutDocumentsBatch(ctx, trOwner, "PROJ", []BatchDocumentInput{
		{Name: batchA, DocumentInput: DocumentInput{Content: docText("decision", "alpha", "related: "+batchB)}},
		{Name: batchB, DocumentInput: DocumentInput{Content: docText("decision", "beta", "related: "+batchA)}},
	})
	if err != nil {
		t.Fatalf("PutDocumentsBatch mutual related: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("len(res) = %d, want 2", len(res))
	}
	if !reflect.DeepEqual(res[0].Related, []string{batchB}) || res[0].State != model.StateCurrent || len(res[0].Warnings) != 0 {
		t.Errorf("res[0] = %+v", res[0])
	}
	if !reflect.DeepEqual(res[1].Related, []string{batchA}) || res[1].State != model.StateCurrent || len(res[1].Warnings) != 0 {
		t.Errorf("res[1] = %+v", res[1])
	}
	for i, name := range []string{batchA, batchB} {
		got, err := s.GetDocument(ctx, "PROJ", name)
		if err != nil {
			t.Fatalf("GetDocument(%s): %v", name, err)
		}
		wantRel := []string{batchB}
		if i == 1 {
			wantRel = []string{batchA}
		}
		if !reflect.DeepEqual(got.Related, wantRel) || got.State != model.StateCurrent {
			t.Errorf("stored %s = %+v", name, got)
		}
	}

	// 3-way cyclic related in a fresh project.
	mustProject(t, s, ctx, "CYC")
	res3, err := s.PutDocumentsBatch(ctx, trOwner, "CYC", []BatchDocumentInput{
		{Name: batchA, DocumentInput: DocumentInput{Content: docText("decision", "a", "related: "+batchB)}},
		{Name: batchB, DocumentInput: DocumentInput{Content: docText("decision", "b", "related: "+batchC)}},
		{Name: batchC, DocumentInput: DocumentInput{Content: docText("decision", "c", "related: "+batchA)}},
	})
	if err != nil {
		t.Fatalf("PutDocumentsBatch 3-cycle: %v", err)
	}
	if len(res3) != 3 || !reflect.DeepEqual(res3[0].Related, []string{batchB}) ||
		!reflect.DeepEqual(res3[1].Related, []string{batchC}) || !reflect.DeepEqual(res3[2].Related, []string{batchA}) {
		t.Errorf("res3 = %+v", res3)
	}
}

func TestPutDocumentsBatchSupersedesWithinBatch(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "PROJ")

	res, err := s.PutDocumentsBatch(ctx, trOwner, "PROJ", []BatchDocumentInput{
		{Name: batchA, DocumentInput: DocumentInput{Content: docText("decision", "old")}},
		{Name: batchB, DocumentInput: DocumentInput{Content: docText("decision", "new", "supersedes: "+batchA)}},
	})
	if err != nil {
		t.Fatalf("PutDocumentsBatch supersedes: %v", err)
	}
	if res[0].State != model.StateSuperseded || !reflect.DeepEqual(res[0].SupersededBy, []string{batchB}) {
		t.Errorf("res[0] = %+v", res[0])
	}
	if res[1].State != model.StateCurrent || !reflect.DeepEqual(res[1].Supersedes, []string{batchA}) {
		t.Errorf("res[1] = %+v", res[1])
	}

	// Mutual supersedes cycle in the same batch is refused and rolls back all rows.
	mustProject(t, s, ctx, "LOOP")
	_, err = s.PutDocumentsBatch(ctx, trOwner, "LOOP", []BatchDocumentInput{
		{Name: batchA, DocumentInput: DocumentInput{Content: docText("decision", "a", "supersedes: "+batchB)}},
		{Name: batchB, DocumentInput: DocumentInput{Content: docText("decision", "b", "supersedes: "+batchA)}},
	})
	if !errors.Is(err, ErrSupersedesCycle) {
		t.Errorf("mutual supersedes cycle err = %v, want ErrSupersedesCycle", err)
	}
	if items, total, _ := s.ListDocuments(ctx, "LOOP", 50, 0); total != 0 || len(items) != 0 {
		t.Errorf("LOOP documents after rollback = %d, %v", total, items)
	}
}

func TestPutDocumentsBatchRejectsInvalidAndRollsBack(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "PROJ")

	// Empty and >100 bounds.
	if _, err := s.PutDocumentsBatch(ctx, trOwner, "PROJ", nil); !reflect.DeepEqual(violationsOf(err), []string{"documents:invalid"}) {
		t.Errorf("empty batch violations = %v (%v)", violationsOf(err), err)
	}
	over := make([]BatchDocumentInput, MaxBatchDocuments+1)
	for i := range over {
		over[i] = BatchDocumentInput{
			Name:          fmt.Sprintf("2026-10-10-%04d-decision-doc-%d.md", 1000+i, i),
			DocumentInput: DocumentInput{Content: docText("decision", "body")},
		}
	}
	if _, err := s.PutDocumentsBatch(ctx, trOwner, "PROJ", over); !reflect.DeepEqual(violationsOf(err), []string{"documents:invalid"}) {
		t.Errorf("over-limit batch violations = %v (%v)", violationsOf(err), err)
	}

	// Duplicate name in batch.
	_, err := s.PutDocumentsBatch(ctx, trOwner, "PROJ", []BatchDocumentInput{
		{Name: batchA, DocumentInput: DocumentInput{Content: docText("decision", "one")}},
		{Name: batchA, DocumentInput: DocumentInput{Content: docText("decision", "one")}},
	})
	if !reflect.DeepEqual(violationsOf(err), []string{"documents[1].name:duplicate"}) {
		t.Errorf("duplicate name violations = %v (%v)", violationsOf(err), err)
	}

	// Self-reference (related and supersedes) rolls back entire batch.
	for _, kind := range []string{"related", "supersedes"} {
		_, err := s.PutDocumentsBatch(ctx, trOwner, "PROJ", []BatchDocumentInput{
			{Name: batchB, DocumentInput: DocumentInput{Content: docText("decision", "valid")}},
			{Name: batchA, DocumentInput: DocumentInput{Content: docText("decision", "self", kind+": "+batchA)}},
		})
		want := []string{"documents[1]." + kind + "[0]:self_reference"}
		if got := violationsOf(err); !reflect.DeepEqual(got, want) {
			t.Errorf("self %s violations = %v (%v), want %v", kind, got, err, want)
		}
	}

	// Missing target rolls back valid peer in the same batch.
	_, err = s.PutDocumentsBatch(ctx, trOwner, "PROJ", []BatchDocumentInput{
		{Name: batchA, DocumentInput: DocumentInput{Content: docText("decision", "valid")}},
		{Name: batchB, DocumentInput: DocumentInput{Content: docText("decision", "broken", "related: 2026-10-10-9999-decision-missing.md")}},
	})
	if got, want := violationsOf(err), []string{"documents[1].related[0]:unresolved"}; !reflect.DeepEqual(got, want) {
		t.Errorf("missing target violations = %v (%v), want %v", got, err, want)
	}
	if _, err := s.GetDocument(ctx, "PROJ", batchA); !errors.Is(err, ErrNotFound) {
		t.Errorf("batchA stored despite rollback: %v", err)
	}
}

func TestPutDocumentsBatchIdempotencyAndConflicts(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "PROJ")

	contentA := docText("decision", "existing alpha", "tags: [one]")
	firstA, _ := putDoc(t, s, trOwner, "PROJ", batchA, "history", contentA)

	// Same content and metadata for A + new B -> A unchanged, B created, no activity rows.
	contentB := docText("decision", "new beta", "related: "+batchA)
	res, err := s.PutDocumentsBatch(ctx, trOwner, "PROJ", []BatchDocumentInput{
		{Name: batchA, DocumentInput: DocumentInput{Content: contentA}},
		{Name: batchB, DocumentInput: DocumentInput{Content: contentB}},
	})
	if err != nil {
		t.Fatalf("idempotent + new batch: %v", err)
	}
	if !reflect.DeepEqual(&res[0].Document, firstA) {
		t.Errorf("res[0] = %+v, want %+v", res[0].Document, *firstA)
	}
	if !reflect.DeepEqual(res[1].Related, []string{batchA}) {
		t.Errorf("res[1] = %+v", res[1])
	}
	for _, table := range []string{"activity", "document_activity"} {
		var n int
		if err := s.db.QueryRow(`select count(*) from ` + table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s count = %d, %v", table, n, err)
		}
	}

	// Immutable conflict on A rolls back new C.
	_, err = s.PutDocumentsBatch(ctx, trOwner, "PROJ", []BatchDocumentInput{
		{Name: batchC, DocumentInput: DocumentInput{Content: docText("decision", "gamma")}},
		{Name: batchA, DocumentInput: DocumentInput{Content: contentA + "extra\n"}},
	})
	var imm *ImmutableError
	if !errors.As(err, &imm) || imm.Name != batchA || imm.SHA256 != firstA.SHA256 {
		t.Errorf("immutable err = %#v (%v)", imm, err)
	}
	if _, err := s.GetDocument(ctx, "PROJ", batchC); !errors.Is(err, ErrNotFound) {
		t.Errorf("batchC stored despite immutable conflict: %v", err)
	}

	// Metadata diff conflict on A rolls back new C.
	otherTitle := "다른 제목"
	_, err = s.PutDocumentsBatch(ctx, trOwner, "PROJ", []BatchDocumentInput{
		{Name: batchC, DocumentInput: DocumentInput{Content: docText("decision", "gamma")}},
		{Name: batchA, DocumentInput: DocumentInput{Content: contentA, Args: model.DocumentArgs{Title: &otherTitle}}},
	})
	var mdiff *MetadataDiffError
	if !errors.As(err, &mdiff) || mdiff.Name != batchA || len(mdiff.Diffs) != 1 || mdiff.Diffs[0].Field != "documents[1].title" {
		t.Errorf("metadata diff err = %#v (%v)", mdiff, err)
	}
	if _, err := s.GetDocument(ctx, "PROJ", batchC); !errors.Is(err, ErrNotFound) {
		t.Errorf("batchC stored despite metadata diff: %v", err)
	}

	// FTS index finds newly inserted B.
	found, total, err := s.SearchDocuments(ctx, DocumentFilter{Query: "beta", Projects: []string{"PROJ"}, Limit: 10})
	if err != nil || total != 1 || found[0].Name != batchB {
		t.Errorf("FTS search = %v, %d, %v", found, total, err)
	}
}
