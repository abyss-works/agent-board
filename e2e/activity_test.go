//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestComments(t *testing.T) {
	p := createProject(t, "CM")
	iss := newIssue(t, p.Key, "with comments", nil)
	writer := caller{actor: "gemini", session: sessionID + "-cm", host: "laptop-win11"}

	t.Run("201 with a commented activity", func(t *testing.T) {
		r := writer.do(t, "POST", "/v1/issues/"+iss.Key+"/comments", map[string]any{"body": "선행 이슈가 끝나 **착수**한다."})
		a := decodeAs[activity](t, r, 201)
		if a.ID == 0 || a.Issue != iss.Key || a.Project != p.Key || a.Kind != "commented" {
			t.Errorf("activity = %+v", a)
		}
		if a.Actor != "gemini" || a.SessionID != sessionID+"-cm" || a.ClientHost != "laptop-win11" {
			t.Errorf("trace = %s %s %s", a.Actor, a.SessionID, a.ClientHost)
		}
		if a.Body == nil || *a.Body != "선행 이슈가 끝나 **착수**한다." {
			t.Errorf("body = %v", a.Body)
		}
		if a.Changes != nil {
			t.Errorf("changes = %v, want null", *a.Changes)
		}
		wantTime(t, "at", a.At)
	})
	t.Run("a comment does not touch the issue", func(t *testing.T) {
		got := getIssue(t, iss.Key)
		if got.Version != 1 || got.UpdatedBy != "owner" {
			t.Errorf("issue = version %d updated_by %s", got.Version, got.UpdatedBy)
		}
	})
	t.Run("validation", func(t *testing.T) {
		e := wantError(t, owner.do(t, "POST", "/v1/issues/"+iss.Key+"/comments", map[string]any{"body": ""}), 422, "validation_failed")
		if !e.hasField("body") {
			t.Errorf("details = %+v", e.Error.Details)
		}
		wantError(t, owner.do(t, "POST", "/v1/issues/"+iss.Key+"/comments", map[string]any{"body": strings.Repeat("a", 64*1024+1)}), 422, "validation_failed")
		expect(t, owner.do(t, "POST", "/v1/issues/"+iss.Key+"/comments", map[string]any{"body": strings.Repeat("a", 64*1024)}), 201)
		wantError(t, owner.do(t, "POST", "/v1/issues/"+iss.Key+"/comments", map[string]any{"body": "x", "title": "y"}), 400, "bad_request")
		wantError(t, owner.do(t, "POST", "/v1/issues/"+iss.Key+"/comments", map[string]any{}), 422, "validation_failed")
	})
	t.Run("GET on comments is 405", func(t *testing.T) {
		r := owner.do(t, "GET", "/v1/issues/"+iss.Key+"/comments", nil)
		wantError(t, r, 405, "method_not_allowed")
		if r.header.Get("Allow") != "POST" {
			t.Errorf("Allow = %q", r.header.Get("Allow"))
		}
	})
}

func TestActivity(t *testing.T) {
	p := createProject(t, "AC")
	creator := caller{actor: "claude", session: sessionID + "-ac", host: "desk-win10"}
	editor := caller{actor: "codex", session: sessionID + "-ac2", host: "ab"}
	iss := createIssue(t, creator, map[string]any{"project": p.Key, "title": "활동 기록", "labels": strs("api"), "priority": "P1", "status": "todo", "body": templateBody(t, "feature")})
	tick()
	upd := patchIssue(t, editor, iss.Key, map[string]any{"status": "doing", "labels": strs("api", "cli"), "title": "활동 기록"})
	tick()
	cmt := decodeAs[activity](t, owner.do(t, "POST", "/v1/issues/"+iss.Key+"/comments", map[string]any{"body": "댓글"}), 201)

	t.Run("per-issue activity is newest first", func(t *testing.T) {
		got := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/issues/"+iss.Key+"/activity", nil), 200)
		if got.Total != 3 || len(got.Items) != 3 || got.Limit != 50 {
			t.Fatalf("activity = %+v", got)
		}
		kinds := []string{got.Items[0].Kind, got.Items[1].Kind, got.Items[2].Kind}
		if !equalStrings(kinds, []string{"commented", "updated", "created"}) {
			t.Errorf("kinds = %v", kinds)
		}
		if !(got.Items[0].At >= got.Items[1].At && got.Items[1].At >= got.Items[2].At) {
			t.Errorf("not newest first: %s %s %s", got.Items[0].At, got.Items[1].At, got.Items[2].At)
		}
		for _, a := range got.Items {
			if a.Issue != iss.Key || a.Project != p.Key {
				t.Errorf("record %d belongs to %s/%s", a.ID, a.Project, a.Issue)
			}
		}
	})
	t.Run("created record holds every field as to", func(t *testing.T) {
		got := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/activity?issue="+iss.Key+"&kind=created", nil), 200)
		if got.Total != 1 {
			t.Fatalf("created records = %d", got.Total)
		}
		a := got.Items[0]
		if a.Actor != "claude" || a.SessionID != sessionID+"-ac" || a.ClientHost != "desk-win10" || a.At != iss.CreatedAt || a.Body != nil {
			t.Errorf("created = %+v", a)
		}
		if a.Changes == nil {
			t.Fatalf("changes = null")
		}
		ch := *a.Changes
		for _, f := range []string{"title", "body", "type", "status", "priority", "labels", "parent", "depends_on", "refs", "recheck_condition"} {
			c, ok := ch[f]
			if !ok {
				t.Errorf("created changes lack %s", f)
				continue
			}
			if len(c.From) != 0 && string(c.From) != "null" {
				t.Errorf("created change %s has from %s", f, c.From)
			}
		}
		if string(ch["title"].To) != `"활동 기록"` || string(ch["status"].To) != `"todo"` || string(ch["priority"].To) != `"P1"` || string(ch["labels"].To) != `["api"]` || string(ch["parent"].To) != "null" {
			t.Errorf("created to-values = %v", ch)
		}
	})
	t.Run("updated record holds only the changed fields", func(t *testing.T) {
		got := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/activity?issue="+iss.Key+"&kind=updated", nil), 200)
		if got.Total != 1 {
			t.Fatalf("updated records = %d", got.Total)
		}
		a := got.Items[0]
		if a.Actor != "codex" || a.SessionID != sessionID+"-ac2" || a.ClientHost != "ab" || a.At != upd.UpdatedAt {
			t.Errorf("updated = %+v", a)
		}
		ch := *a.Changes
		if len(ch) != 3 {
			t.Errorf("changes = %v, want status, status_reason and labels only", ch)
		}
		if string(ch["status"].From) != `"todo"` || string(ch["status"].To) != `"doing"` {
			t.Errorf("status change = %+v", ch["status"])
		}
		if string(ch["status_reason"].From) != `null` || string(ch["status_reason"].To) != `"할 일 → 진행"` {
			t.Errorf("status_reason change = %+v", ch["status_reason"])
		}
		var from, to []string
		json.Unmarshal(ch["labels"].From, &from)
		json.Unmarshal(ch["labels"].To, &to)
		if !equalStrings(from, []string{"api"}) || !equalStrings(sortedCopy(to), []string{"api", "cli"}) {
			t.Errorf("labels change = %s -> %s", ch["labels"].From, ch["labels"].To)
		}
	})
	t.Run("global filters", func(t *testing.T) {
		count := func(q string) int {
			t.Helper()
			return decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/activity?"+q, nil), 200).Total
		}
		if n := count("project=" + p.Key); n != 3 {
			t.Errorf("project = %d", n)
		}
		if n := count("issue=" + iss.Key); n != 3 {
			t.Errorf("issue = %d", n)
		}
		if n := count("session=" + sessionID + "-ac"); n != 1 {
			t.Errorf("session = %d", n)
		}
		if n := count("session=" + sessionID + "-ac," + sessionID + "-ac2"); n != 2 {
			t.Errorf("session OR = %d", n)
		}
		if n := count("issue=" + iss.Key + "&actor=codex"); n != 1 {
			t.Errorf("actor = %d", n)
		}
		if n := count("issue=" + iss.Key + "&actor=codex,owner"); n != 2 {
			t.Errorf("actor OR = %d", n)
		}
		if n := count("issue=" + iss.Key + "&client_host=ab"); n != 1 {
			t.Errorf("client_host = %d", n)
		}
		if n := count("issue=" + iss.Key + "&kind=commented"); n != 1 {
			t.Errorf("kind = %d", n)
		}
		if n := count("issue=" + iss.Key + "&kind=created,commented"); n != 2 {
			t.Errorf("kind OR = %d", n)
		}
		if n := count("issue=" + iss.Key + "&field=status&kind=updated"); n != 1 {
			t.Errorf("field=status&kind=updated = %d", n)
		}
		if n := count("issue=" + iss.Key + "&field=status"); n != 2 {
			t.Errorf("field=status = %d (created carries every field)", n)
		}
		if n := count("issue=" + iss.Key + "&field=title"); n != 1 {
			t.Errorf("field=title = %d (the patch repeated the same title)", n)
		}
		if n := count("issue=" + iss.Key + "&since=" + iss.CreatedAt); n != 3 {
			t.Errorf("since created = %d", n)
		}
		if n := count("issue=" + iss.Key + "&until=" + iss.CreatedAt); n != 0 {
			t.Errorf("until created (exclusive) = %d", n)
		}
		if n := count("issue=" + iss.Key + "&since=" + upd.UpdatedAt + "&until=" + cmt.At); n != 1 {
			t.Errorf("since update until comment = %d", n)
		}
	})
	t.Run("sort at and -at", func(t *testing.T) {
		asc := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/activity?issue="+iss.Key+"&sort=at", nil), 200)
		desc := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/activity?issue="+iss.Key+"&sort=-at", nil), 200)
		def := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/activity?issue="+iss.Key, nil), 200)
		if asc.Items[0].Kind != "created" || asc.Items[2].Kind != "commented" {
			t.Errorf("asc = %s %s %s", asc.Items[0].Kind, asc.Items[1].Kind, asc.Items[2].Kind)
		}
		if desc.Items[0].Kind != "commented" || def.Items[0].Kind != "commented" {
			t.Errorf("desc = %s, default = %s", desc.Items[0].Kind, def.Items[0].Kind)
		}
	})
	t.Run("pagination", func(t *testing.T) {
		got := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/activity?issue="+iss.Key+"&sort=at&limit=1&offset=1", nil), 200)
		if got.Total != 3 || len(got.Items) != 1 || got.Items[0].Kind != "updated" || got.Limit != 1 || got.Offset != 1 {
			t.Errorf("page = %+v", got)
		}
		got = decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/issues/"+iss.Key+"/activity?limit=2", nil), 200)
		if got.Total != 3 || len(got.Items) != 2 {
			t.Errorf("issue page = total %d items %d", got.Total, len(got.Items))
		}
	})
	t.Run("bad query parameters are 400", func(t *testing.T) {
		for _, q := range []string{
			"kind=deleted", "field=nope", "sort=id", "since=yesterday", "until=2026-13-01T00:00:00Z", "limit=0", "limit=201",
			"issue=A-1&issue=A-2", "project=X&project=Y", "foo=1",
		} {
			wantError(t, owner.do(t, "GET", "/v1/activity?"+q, nil), 400, "bad_request")
		}
		wantError(t, owner.do(t, "GET", "/v1/issues/"+iss.Key+"/activity?sort=at", nil), 400, "bad_request")
	})
	t.Run("project writes leave no activity", func(t *testing.T) {
		before := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/activity?project="+p.Key, nil), 200).Total
		decodeAs[project](t, owner.do(t, "PATCH", "/v1/projects/"+p.Key, map[string]any{"name": "renamed"}), 200)
		after := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/activity?project="+p.Key, nil), 200).Total
		if before != after {
			t.Errorf("project patch wrote activity: %d -> %d", before, after)
		}
	})
}
