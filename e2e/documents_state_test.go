//go:build e2e

package e2e

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
)

type documentActivityItem struct {
	ID         int64                     `json:"id"`
	Project    string                    `json:"project"`
	Document   string                    `json:"document"`
	Actor      string                    `json:"actor"`
	SessionID  string                    `json:"session_id"`
	ClientHost string                    `json:"client_host"`
	At         string                    `json:"at"`
	Changes    map[string]map[string]any `json:"changes"`
}

// TestDocumentState registers documents with their metadata, relates, archives
// and searches them: the state is computed, the edits go through the dedicated
// endpoints and each leaves a record.
func TestDocumentState(t *testing.T) {
	p := createProject(t, "DS")
	q := createProject(t, "DT")
	word := "state" + strings.ToLower(runID)
	const (
		nOld    = "2026-11-01-1000-decision-old.md"
		nNew    = "2026-11-02-1000-decision-new.md"
		nNotes  = "2026-11-03-1000-report-notes.md"
		nOther  = "2026-11-04-1000-report-other.md"
		nLegacy = "decisions/2026-10-17-14-30-scope.md"
	)
	issue := newIssue(t, p.Key, "근거 이슈", nil)
	put := func(c caller, project, name string, fields map[string]any) response {
		t.Helper()
		return c.do(t, "PUT", documentPath(project, name), fields)
	}
	body := func(kind, text string) string { return documentText(kind, "", "", "", text+" "+word) }

	// Register with metadata by argument; the front matter fills what is left.
	r := put(as("claude"), p.Key, nOld, map[string]any{"content": body("decision", "옛 결정"), "title": "옛 결정 " + word, "tags": []string{"a"}})
	old := decodeAs[document](t, r, 201)
	if old.State != "current" || old.Title == nil || *old.Title != "옛 결정 "+word || !reflect.DeepEqual(old.Tags, []string{"a"}) || old.Date == nil || *old.Date != "2026-11-01 10:00" {
		t.Errorf("old = %+v", old)
	}
	newDoc := decodeAs[document](t, put(as("claude"), p.Key, nNew, map[string]any{
		"content": body("decision", "새 결정"), "supersedes": []string{nOld}, "issues": []string{issue.Key}}), 201)
	if !reflect.DeepEqual(newDoc.Supersedes, []string{nOld}) || !reflect.DeepEqual(newDoc.Issues, []string{issue.Key}) || newDoc.State != "current" {
		t.Errorf("new = %+v", newDoc)
	}
	get := func(project, name string) document {
		t.Helper()
		return decodeAs[document](t, owner.do(t, "GET", documentPath(project, name), nil), 200)
	}
	if o := get(p.Key, nOld); o.State != "superseded" || !reflect.DeepEqual(o.SupersededBy, []string{nNew}) {
		t.Errorf("the replaced document = %+v", o)
	}

	// Refusals: an unresolved target, a sentence, a self reference, another metadata for the same content.
	e := wantError(t, put(as("claude"), p.Key, nNotes, map[string]any{"content": body("report", "메모"), "related": []string{"nothing.md", "일부 문장", nNotes}}), 422, "validation_failed")
	if !e.hasReason("unresolved") || !e.hasReason("not_a_name") || !e.hasReason("self_reference") {
		t.Errorf("related violations = %+v", e.Error.Details)
	}
	wantError(t, put(as("claude"), p.Key, nNotes, map[string]any{"content": body("report", "메모"), "issues": []string{p.Key + "-9999"}}), 422, "validation_failed")
	e = wantError(t, put(as("claude"), p.Key, nOld, map[string]any{"content": body("decision", "옛 결정"), "title": "다른 제목"}), 409, "conflict")
	if !e.hasReason("metadata_differs") || !e.hasField("title") {
		t.Errorf("metadata differs = %+v", e.Error.Details)
	}

	// An imported document may name what is not there yet; the relation resolves when the target comes.
	imp := decodeAs[document](t, put(as("claude"), p.Key, nLegacy, map[string]any{
		"content": "# 범위 결정 " + word + "\n", "imported": true, "related": []string{nNotes}, "path": nLegacy}), 201)
	if !imp.Imported || imp.Kind != "other" || len(imp.Warnings) != 1 || len(imp.UnresolvedRefs) != 1 || imp.Path == nil {
		t.Errorf("imported = %+v", imp)
	}
	decodeAs[document](t, put(as("claude"), p.Key, nNotes, map[string]any{"content": body("report", "메모"), "archived": true}), 201)
	if got := get(p.Key, nLegacy); !reflect.DeepEqual(got.Related, []string{nNotes}) || len(got.UnresolvedRefs) != 0 {
		t.Errorf("after the target was registered: %+v", got)
	}

	// Archive and relations: the dedicated endpoints.
	tick()
	a := decodeAs[document](t, as("codex").do(t, "POST", documentPath(p.Key, nNew)+"/archive", nil), 200)
	if a.State != "archived" || !a.Archived || a.UpdatedBy != "codex" {
		t.Errorf("archived = %+v", a)
	}
	if again := decodeAs[document](t, owner.do(t, "POST", documentPath(p.Key, nNew)+"/archive", nil), 200); again.UpdatedBy != "codex" {
		t.Errorf("archiving again changed the document: %+v", again)
	}
	if o := get(p.Key, nOld); o.State != "superseded" {
		t.Errorf("the replaced document of an archived one stays replaced: %+v", o)
	}
	decodeAs[document](t, owner.do(t, "DELETE", documentPath(p.Key, nNew)+"/archive", nil), 200)
	rel := map[string]any{"kind": "related", "target": q.Key + "/" + nOther}
	decodeAs[document](t, put(as("claude"), q.Key, nOther, map[string]any{"content": body("report", "다른 프로젝트")}), 201)
	if d := decodeAs[document](t, owner.do(t, "POST", documentPath(p.Key, nNew)+"/relations", rel), 200); !reflect.DeepEqual(d.Related, []string{q.Key + "/" + nOther}) {
		t.Errorf("related across projects = %+v", d)
	}
	wantError(t, owner.do(t, "POST", documentPath(p.Key, nNew)+"/relations", map[string]any{"kind": "related", "target": nOther}), 422, "validation_failed")
	e = wantError(t, owner.do(t, "POST", documentPath(p.Key, nOld)+"/relations", map[string]any{"kind": "supersedes", "target": nNew}), 409, "conflict")
	if !e.hasReason("cycle") {
		t.Errorf("cycle = %+v", e.Error.Details)
	}
	decodeAs[document](t, owner.do(t, "DELETE", documentPath(p.Key, nNew)+"/relations", rel), 200)
	if d := get(p.Key, nNew); len(d.Related) != 0 {
		t.Errorf("after removing the relation: %+v", d)
	}
	wantError(t, owner.do(t, "PATCH", documentPath(p.Key, nNew)+"/archive", map[string]any{}), 405, "method_not_allowed")

	// The activity: newest first, one record per change.
	acts := decodeAs[list[documentActivityItem]](t, owner.do(t, "GET", documentPath(p.Key, nNew)+"/activity", nil), 200)
	if acts.Total != 4 || len(acts.Items) != 4 || acts.Items[0].Actor != "owner" || acts.Items[3].Actor != "codex" || acts.Items[3].Document != nNew || acts.Items[3].Project != p.Key ||
		acts.Items[3].SessionID == "" || acts.Items[3].ClientHost == "" {
		t.Errorf("activity = %+v", acts)
	}
	if c := acts.Items[3].Changes["archived"]; c["from"] != false || c["to"] != true {
		t.Errorf("archive change = %+v", acts.Items[3].Changes)
	}
	if got := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/activity?project="+p.Key, nil), 200); got.Total != 1 {
		// Only the issue's own record of its creation.
		t.Errorf("issue activity of the project = %d", got.Total)
	}
	wantError(t, owner.do(t, "GET", documentPath(p.Key, "x.md")+"/activity", nil), 404, "not_found")

	// Search: the state default is current; the filters widen it.
	names := func(query string) string {
		t.Helper()
		return sortedJoin(documentNames(decodeAs[list[document]](t, owner.do(t, "GET", "/v1/documents?project="+p.Key+","+q.Key+"&"+query, nil), 200).Items))
	}
	join := func(n ...string) string { return sortedJoin(strings.Join(n, ",")) }
	for query, want := range map[string]string{
		"q=" + url.QueryEscape(word):                              join(nNew, nLegacy, nOther),
		"q=" + url.QueryEscape(word) + "&state=all":               join(nOld, nNew, nNotes, nLegacy, nOther),
		"q=" + url.QueryEscape(word) + "&state=superseded":        nOld,
		"q=" + url.QueryEscape(word) + "&state=archived":          nNotes,
		"q=" + url.QueryEscape(word) + "&state=all&imported=true": nLegacy,
		"q=" + url.QueryEscape(word) + "&state=all&kind=other":    nLegacy,
	} {
		if got := names(query); got != want {
			t.Errorf("%s: %q, want %q", query, got, want)
		}
	}
	wantError(t, owner.do(t, "GET", "/v1/documents?location=archive", nil), 400, "bad_request")

	// The unified search carries status and state, and the supersession.
	hits := decodeAs[list[map[string]any]](t, owner.do(t, "GET", "/v1/search?q="+url.QueryEscape(word)+"&project="+p.Key+"&state=all", nil), 200)
	if hits.Total != 4 {
		t.Fatalf("unified search = %d hits", hits.Total)
	}
	for _, h := range hits.Items {
		if h["key"] == nOld && (h["state"] != "superseded" || h["status"] != nil || !reflect.DeepEqual(h["superseded_by"], []any{nNew})) {
			t.Errorf("hit of the replaced document = %+v", h)
		}
	}
	statusHits := decodeAs[list[map[string]any]](t, owner.do(t, "GET", "/v1/search?q="+url.QueryEscape("근거")+"&project="+p.Key+"&status=open", nil), 200)
	if statusHits.Total != 1 || statusHits.Items[0]["status"] != "backlog" || statusHits.Items[0]["state"] != nil {
		t.Errorf("issue hit = %+v", statusHits)
	}
	wantError(t, owner.do(t, "GET", "/v1/search?q=x&status=deleted", nil), 400, "bad_request")

	// The graph folds what is replaced, and state=all shows it.
	node := url.QueryEscape("issue:" + issue.Key)
	g := decodeAs[struct {
		Nodes []struct {
			ID     string         `json:"id"`
			Folded map[string]int `json:"folded"`
		} `json:"nodes"`
	}](t, owner.do(t, "GET", "/v1/graph?depth=2&node="+node, nil), 200)
	ids := map[string]bool{}
	for _, n := range g.Nodes {
		ids[n.ID] = true
	}
	if !ids["doc:"+p.Key+"/"+nNew] || ids["doc:"+p.Key+"/"+nOld] || g.Nodes[1].Folded["superseded"] != 1 {
		t.Errorf("folded graph = %+v", g.Nodes)
	}
	all := decodeAs[struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	}](t, owner.do(t, "GET", "/v1/graph?depth=2&state=all&node="+node, nil), 200)
	if len(all.Nodes) != 3 {
		t.Errorf("graph with state=all = %+v", all.Nodes)
	}
	wantError(t, owner.do(t, "GET", "/v1/graph?node="+node+"&state=old", nil), 400, "bad_request")

	// An issue reference has to resolve; one the issue has stays.
	wantError(t, owner.do(t, "POST", "/v1/issues", map[string]any{"project": p.Key, "title": "참조", "refs": []string{"doc:nothing.md"}}), 422, "validation_failed")
	ref := newIssue(t, p.Key, "참조", map[string]any{"refs": []string{"doc:" + nNew, "issue:" + issue.Key}})
	if !equalStrings(sortedCopy(ref.Refs), []string{"doc:" + nNew, "issue:" + issue.Key}) {
		t.Errorf("refs = %v", ref.Refs)
	}

	// The digest lists the documents that are current.
	d := decodeAs[struct {
		Projects []struct {
			Project   struct{ Key string } `json:"project"`
			Documents struct {
				Total int `json:"total"`
				Items []struct {
					Name       string   `json:"name"`
					Supersedes []string `json:"supersedes"`
				} `json:"items"`
			} `json:"documents"`
		} `json:"projects"`
	}](t, owner.do(t, "GET", "/v1/digest?project="+p.Key+"&since=2000-01-01T00:00:00Z&limit=200", nil), 200)
	if len(d.Projects) != 1 || d.Projects[0].Documents.Total != 2 {
		t.Fatalf("digest = %+v", d.Projects)
	}
	for _, it := range d.Projects[0].Documents.Items {
		if it.Name == nNew && !reflect.DeepEqual(it.Supersedes, []string{nOld}) {
			t.Errorf("digest document = %+v", it)
		}
	}
}
