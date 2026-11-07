package store

import (
	"reflect"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

type refRow struct{ ref, issue, doc string }

func refRows(t *testing.T, s *Store, issueKey string) []refRow {
	t.Helper()
	rows, err := s.db.Query(`select r.ref, coalesce(t.key, ''), coalesce(d.name, '') from issue_ref r join issue i on i.id = r.issue_id
		left join issue t on t.id = r.target_issue_id left join document d on d.id = r.target_document_id where i.key = ? order by r.ref`, issueKey)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []refRow
	for rows.Next() {
		var r refRow
		if err := rows.Scan(&r.ref, &r.issue, &r.doc); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestIssueRefsResolveOnWrite(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	other := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "OTH", Title: "다른 프로젝트의 이슈"})
	gone := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "지운 이슈", Status: model.StatusDeleted})
	putDoc(t, s, trOwner, "AGB", docPriority, "history", docText("decision", "본문"))
	putDoc(t, s, trOwner, "OTH", docLock, "history", docText("incident", "다른 프로젝트"))

	in := IssueInput{Project: "AGB", Title: "참조", Refs: []string{"issue:" + other.Key, "issue:" + gone.Key, "doc:" + docPriority, "commit:abc1234", "url:https://example.com"}}
	issue := mustIssue(t, s, ctx, trOwner, in)
	want := []refRow{
		{"commit:abc1234", "", ""},
		{"doc:" + docPriority, "", docPriority},
		{"issue:" + gone.Key, gone.Key, ""}, // a deleted issue resolves
		{"issue:" + other.Key, other.Key, ""},
		{"url:https://example.com", "", ""},
	}
	if got := refRows(t, s, issue.Key); !reflect.DeepEqual(got, want) {
		t.Errorf("refs = %+v\nwant %+v", got, want)
	}
}

func TestIssueRefsRefuseWhatDoesNotResolve(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	putDoc(t, s, trOwner, "OTH", docLock, "history", docText("incident", "다른 프로젝트"))

	// The refs are numbered in the normalized (sorted) order the request is stored in.
	_, err := s.CreateIssue(ctx, trOwner, IssueInput{Project: "AGB", Title: "참조", Refs: []string{"doc:missing.md", "issue:AGB-9", "doc:" + docLock, "commit:abc"}})
	if got, want := violationsOf(err), []string{"refs[1]:unresolved", "refs[2]:unresolved", "refs[3]:unresolved"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("violations = %v (%v), want %v", got, err, want)
	}
	if n := countRows(t, s, "issue"); n != 0 {
		t.Errorf("a refused create left %d issues", n)
	}
}

func TestIssueRefsOnUpdate(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	putDoc(t, s, trOwner, "AGB", docPriority, "history", docText("decision", "본문"))
	issue := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "참조"})
	// A reference written before the targets were resolved stays as it is.
	if _, err := s.db.Exec(`insert into issue_ref(issue_id, ref) values (?, 'doc:legacy.md')`, 1); err != nil {
		t.Fatal(err)
	}

	refs := []string{"doc:legacy.md", "doc:" + docPriority}
	patch := IssuePatch{Refs: &refs}
	if _, changed, err := s.UpdateIssue(ctx, trOwner, issue.Key, patch, nil); err != nil || !changed {
		t.Fatalf("update = %v, %v", changed, err)
	}
	want := []refRow{{"doc:" + docPriority, "", docPriority}, {"doc:legacy.md", "", ""}}
	if got := refRows(t, s, issue.Key); !reflect.DeepEqual(got, want) {
		t.Errorf("refs = %+v, want %+v", got, want)
	}
	// Sending the same refs again changes nothing and is not refused.
	if _, changed, err := s.UpdateIssue(ctx, trOwner, issue.Key, patch, nil); err != nil || changed {
		t.Errorf("same refs = %v, %v", changed, err)
	}
	if got := refRows(t, s, issue.Key); !reflect.DeepEqual(got, want) {
		t.Errorf("refs after the same update = %+v", got)
	}
	// A new reference has to resolve.
	more := append(refs, "doc:missing.md")
	_, _, err := s.UpdateIssue(ctx, trOwner, issue.Key, IssuePatch{Refs: &more}, nil)
	if got := violationsOf(err); !reflect.DeepEqual(got, []string{"refs[2]:unresolved"}) {
		t.Errorf("violations = %v (%v)", got, err)
	}
	// Dropping the unresolved one is fine.
	only := []string{"doc:" + docPriority}
	if _, _, err := s.UpdateIssue(ctx, trOwner, issue.Key, IssuePatch{Refs: &only}, nil); err != nil {
		t.Errorf("dropping a reference: %v", err)
	}
	if got := refRows(t, s, issue.Key); len(got) != 1 {
		t.Errorf("refs = %+v", got)
	}
}

func TestIssueDocRefsResolveDocumentsOfOtherProjects(t *testing.T) {
	s, ctx := seeded(t)
	mustProject(t, s, ctx, "AGB")
	mustProject(t, s, ctx, "OTH")
	putDoc(t, s, trOwner, "OTH", docLock, "history", docText("incident", "다른 프로젝트"))
	putDoc(t, s, trOwner, "AGB", "OTH/"+docPriority, "legacy", "# 같은 프로젝트의 경로 이름\n")
	putDoc(t, s, trOwner, "AGB", docPriority, "history", docText("decision", "본문"))
	putDoc(t, s, trOwner, "OTH", docPriority, "history", docText("decision", "다른 프로젝트의 같은 이름"))

	// PROJECT/name reaches a document of another project; a bare name stays in the
	// project of the issue; a name that exists in the project wins over the split at the first slash.
	issue := mustIssue(t, s, ctx, trOwner, IssueInput{Project: "AGB", Title: "참조",
		Refs: []string{"doc:OTH/" + docLock, "doc:" + docPriority, "doc:OTH/" + docPriority}})
	want := []refRow{
		{"doc:" + docPriority, "", docPriority},
		{"doc:OTH/" + docPriority, "", "OTH/" + docPriority},
		{"doc:OTH/" + docLock, "", docLock},
	}
	if got := refRows(t, s, issue.Key); !reflect.DeepEqual(got, want) {
		t.Errorf("refs = %+v\nwant %+v", got, want)
	}

	// A name that matches no document of the project and no PROJECT/name is refused.
	_, err := s.CreateIssue(ctx, trOwner, IssueInput{Project: "AGB", Title: "참조", Refs: []string{"doc:NONE/" + docLock, "doc:OTH/missing.md"}})
	if got, want := violationsOf(err), []string{"refs[0]:unresolved", "refs[1]:unresolved"}; !reflect.DeepEqual(got, want) {
		t.Errorf("violations = %v (%v), want %v", got, err, want)
	}
}
