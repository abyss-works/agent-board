//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestIssueCreate(t *testing.T) {
	p := createProject(t, "IC")
	other := createProject(t, "IC2")

	t.Run("minimal body takes the defaults", func(t *testing.T) {
		r := as("codex").do(t, "POST", "/v1/issues", map[string]any{"project": p.Key, "title": "첫 이슈", "body": templateBody(t, "feature")})
		iss := decodeAs[issue](t, r, 201)
		if r.header.Get("Location") != "/v1/issues/"+iss.Key {
			t.Errorf("Location = %q", r.header.Get("Location"))
		}
		if r.header.Get("ETag") != `"1"` {
			t.Errorf("ETag = %q", r.header.Get("ETag"))
		}
		if iss.Key != p.Key+"-1" || iss.Project != p.Key || iss.Number != 1 || iss.Title != "첫 이슈" {
			t.Errorf("identity = %+v", iss)
		}
		if iss.Body == nil || *iss.Body != templateBody(t, "feature") {
			t.Errorf("body = %v, want the body that was sent", iss.Body)
		}
		if iss.Type != "feature" || iss.Status != "backlog" || iss.Priority != "P2" {
			t.Errorf("defaults = type %s status %s priority %s", iss.Type, iss.Status, iss.Priority)
		}
		if iss.Labels == nil || len(iss.Labels) != 0 || iss.DependsOn == nil || len(iss.DependsOn) != 0 || iss.Refs == nil || len(iss.Refs) != 0 {
			t.Errorf("arrays should be empty, not null: labels %v depends_on %v refs %v", iss.Labels, iss.DependsOn, iss.Refs)
		}
		if iss.Parent != nil || iss.RecheckCondition != nil || iss.ClosedAt != nil {
			t.Errorf("nullable fields = parent %v recheck %v closed_at %v", iss.Parent, iss.RecheckCondition, iss.ClosedAt)
		}
		if iss.Version != 1 {
			t.Errorf("version = %d", iss.Version)
		}
		if iss.CreatedBy != "codex" || iss.UpdatedBy != "codex" || iss.StatusChangedBy != "codex" {
			t.Errorf("actors = %s %s %s", iss.CreatedBy, iss.UpdatedBy, iss.StatusChangedBy)
		}
		wantTime(t, "created_at", iss.CreatedAt)
		wantTime(t, "updated_at", iss.UpdatedAt)
		wantTime(t, "status_changed_at", iss.StatusChangedAt)
		if iss.CreatedAt != iss.UpdatedAt || iss.CreatedAt != iss.StatusChangedAt {
			t.Errorf("timestamps differ on creation: %s %s %s", iss.CreatedAt, iss.UpdatedAt, iss.StatusChangedAt)
		}
	})
	t.Run("numbers are issued in sequence", func(t *testing.T) {
		second := newIssue(t, p.Key, "second", nil)
		if second.Key != p.Key+"-2" || second.Number != 2 {
			t.Errorf("second = %s #%d", second.Key, second.Number)
		}
		// A failed create must not consume a number.
		wantError(t, owner.do(t, "POST", "/v1/issues", map[string]any{"project": p.Key, "title": ""}), 422, "validation_failed")
		third := newIssue(t, p.Key, "third", nil)
		if third.Key != p.Key+"-3" {
			t.Errorf("third = %s", third.Key)
		}
		// Numbers are per project.
		o := newIssue(t, other.Key, "other first", nil)
		if o.Key != other.Key+"-1" {
			t.Errorf("other = %s", o.Key)
		}
	})
	t.Run("every attribute round-trips", func(t *testing.T) {
		parent := getIssue(t, p.Key+"-1")
		dep := getIssue(t, other.Key+"-1")
		in := map[string]any{
			"project": p.Key, "title": "전체 속성", "body": "## 배경\n\n본문",
			"type": "bug", "status": "todo", "priority": "P0",
			"labels":     strs("api", "cli", "api"),
			"parent":     parent.Key,
			"depends_on": strs(dep.Key, p.Key+"-2"),
			"refs":       strs("repo:wsk0715/agent-board", "doc:api.md", "repo:wsk0715/agent-board", "url:https://example.com/x?y=1"),
		}
		iss := createIssue(t, owner, in)
		if iss.Title != "전체 속성" || iss.Body == nil || *iss.Body != "## 배경\n\n본문" {
			t.Errorf("text = %+v", iss)
		}
		if iss.Type != "bug" || iss.Status != "todo" || iss.Priority != "P0" {
			t.Errorf("enums = %s %s %s", iss.Type, iss.Status, iss.Priority)
		}
		if !equalStrings(sortedCopy(iss.Labels), []string{"api", "cli"}) {
			t.Errorf("labels = %v (duplicates must merge)", iss.Labels)
		}
		if iss.Parent == nil || *iss.Parent != parent.Key {
			t.Errorf("parent = %v", iss.Parent)
		}
		if !equalStrings(sortedCopy(iss.DependsOn), sortedCopy([]string{dep.Key, p.Key + "-2"})) {
			t.Errorf("depends_on = %v (cross-project allowed)", iss.DependsOn)
		}
		if !equalStrings(sortedCopy(iss.Refs), []string{"doc:api.md", "repo:wsk0715/agent-board", "url:https://example.com/x?y=1"}) {
			t.Errorf("refs = %v (duplicates must merge)", iss.Refs)
		}
		got := getIssue(t, iss.Key)
		if got.Key != iss.Key || got.Title != iss.Title || *got.Body != *iss.Body || got.Version != 1 {
			t.Errorf("get = %+v", got)
		}
	})
	t.Run("created closed sets closed_at", func(t *testing.T) {
		iss := newIssue(t, p.Key, "born done", map[string]any{"status": "done"})
		if iss.ClosedAt == nil || *iss.ClosedAt != iss.CreatedAt {
			t.Errorf("closed_at = %v, created_at %s", iss.ClosedAt, iss.CreatedAt)
		}
	})
	t.Run("observation requires recheck_condition", func(t *testing.T) {
		iss := newIssue(t, p.Key, "관측", map[string]any{"type": "observation", "recheck_condition": "다음 릴리즈 뒤 다시 본다"})
		if iss.RecheckCondition == nil || *iss.RecheckCondition != "다음 릴리즈 뒤 다시 본다" {
			t.Errorf("recheck_condition = %v", iss.RecheckCondition)
		}
	})
	t.Run("parent and depends_on may point at a deleted issue", func(t *testing.T) {
		gone := newIssue(t, p.Key, "to delete", nil)
		deleteIssue(t, owner, gone.Key)
		iss := newIssue(t, p.Key, "child of deleted", map[string]any{"parent": gone.Key, "depends_on": strs(gone.Key)})
		if iss.Parent == nil || *iss.Parent != gone.Key || !equalStrings(iss.DependsOn, []string{gone.Key}) {
			t.Errorf("links = parent %v depends_on %v", iss.Parent, iss.DependsOn)
		}
	})
	t.Run("size limits at the boundary", func(t *testing.T) {
		iss := newIssue(t, p.Key, strings.Repeat("가", 200), map[string]any{"body": strings.Repeat("b", 64*1024)})
		if len([]rune(iss.Title)) != 200 || len(*iss.Body) != 64*1024 {
			t.Errorf("title runes %d body bytes %d", len([]rune(iss.Title)), len(*iss.Body))
		}
		labels := make([]string, 20)
		for i := range labels {
			labels[i] = fmt.Sprintf("l%02d", i)
		}
		refs := make([]string, 50)
		for i := range refs {
			refs[i] = fmt.Sprintf("file:%d", i)
		}
		iss = newIssue(t, p.Key, "max arrays", map[string]any{"labels": labels, "refs": refs})
		if len(iss.Labels) != 20 || len(iss.Refs) != 50 {
			t.Errorf("labels %d refs %d", len(iss.Labels), len(iss.Refs))
		}
	})
	t.Run("missing project is 404", func(t *testing.T) {
		wantError(t, owner.do(t, "POST", "/v1/issues", map[string]any{"project": projectKey("NOPE"), "title": "x"}), 404, "not_found")
	})
	t.Run("unknown issue is 404", func(t *testing.T) {
		for _, m := range []string{"GET", "PATCH", "DELETE"} {
			var body any
			if m == "PATCH" {
				body = map[string]any{"title": "x"}
			}
			wantError(t, owner.do(t, m, "/v1/issues/"+p.Key+"-999", body), 404, "not_found")
		}
		wantError(t, owner.do(t, "POST", "/v1/issues/"+p.Key+"-999/comments", map[string]any{"body": "x"}), 404, "not_found")
		wantError(t, owner.do(t, "GET", "/v1/issues/"+p.Key+"-999/activity", nil), 404, "not_found")
	})
}

func TestIssueValidation(t *testing.T) {
	p := createProject(t, "IV")
	base := newIssue(t, p.Key, "base", nil)
	many := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%s%d", prefix, i)
		}
		return out
	}
	cases := []struct {
		name   string
		fields map[string]any
		field  string // a details[].field that must appear (prefix match for indexed fields)
	}{
		{"empty title", map[string]any{"title": ""}, "title"},
		{"title over 200 runes", map[string]any{"title": strings.Repeat("가", 201)}, "title"},
		{"body over 64 KiB", map[string]any{"body": strings.Repeat("b", 64*1024+1)}, "body"},
		{"unknown type", map[string]any{"type": "task"}, "type"},
		{"unknown status", map[string]any{"status": "open"}, "status"},
		{"unknown priority", map[string]any{"priority": "P4"}, "priority"},
		{"label with uppercase", map[string]any{"labels": strs("API")}, "labels[0]"},
		{"label starting with dash", map[string]any{"labels": strs("-api")}, "labels[0]"},
		{"label too long", map[string]any{"labels": strs(strings.Repeat("a", 33))}, "labels[0]"},
		{"21 labels", map[string]any{"labels": many("l", 21)}, "labels"},
		{"21 depends_on", map[string]any{"depends_on": many(p.Key+"-", 21)}, "depends_on"},
		{"ref with unknown kind", map[string]any{"refs": strs("ticket:1")}, "refs[0]"},
		{"ref without value", map[string]any{"refs": strs("repo:")}, "refs[0]"},
		{"ref value over 500", map[string]any{"refs": strs("url:" + strings.Repeat("a", 501))}, "refs[0]"},
		{"51 refs", map[string]any{"refs": many("file:", 51)}, "refs"},
		{"recheck_condition outside observation", map[string]any{"recheck_condition": "x"}, "recheck_condition"},
		{"observation without recheck_condition", map[string]any{"type": "observation"}, "recheck_condition"},
		{"observation with empty recheck_condition", map[string]any{"type": "observation", "recheck_condition": ""}, "recheck_condition"},
		{"parent in wrong format", map[string]any{"parent": "lower-1"}, "parent"},
		{"parent that does not exist", map[string]any{"parent": p.Key + "-999"}, "parent"},
		{"depends_on that does not exist", map[string]any{"depends_on": strs(base.Key, p.Key+"-999")}, "depends_on[1]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := map[string]any{"project": p.Key, "title": "valid"}
			for k, v := range c.fields {
				in[k] = v
			}
			e := wantError(t, owner.do(t, "POST", "/v1/issues", in), 422, "validation_failed")
			found := false
			for _, d := range e.Error.Details {
				if d.Field == c.field || strings.HasPrefix(d.Field, c.field+"[") {
					found = true
				}
			}
			if !found {
				t.Errorf("details should name %s: %+v", c.field, e.Error.Details)
			}
		})
	}
	t.Run("all violations are reported together", func(t *testing.T) {
		e := wantError(t, owner.do(t, "POST", "/v1/issues", map[string]any{"project": p.Key, "title": "", "priority": "P9", "type": "nope"}), 422, "validation_failed")
		if got := e.fields(); !equalStrings(got, []string{"priority", "title", "type"}) {
			t.Errorf("fields = %v", got)
		}
	})
	t.Run("unknown and server-managed fields are 400", func(t *testing.T) {
		for _, f := range []string{"key", "number", "version", "created_at", "created_by", "closed_at", "snippet", "foo"} {
			e := wantError(t, owner.do(t, "POST", "/v1/issues", map[string]any{"project": p.Key, "title": "x", f: "v"}), 400, "bad_request")
			if !e.hasField(f) {
				t.Errorf("%s: details = %+v", f, e.Error.Details)
			}
		}
	})
	t.Run("malformed JSON is 400", func(t *testing.T) {
		wantError(t, owner.doRaw(t, "POST", "/v1/issues", []byte(`{"project": "`+p.Key+`", "title": }`)), 400, "bad_request")
	})
	t.Run("the same rules apply to PATCH", func(t *testing.T) {
		e := wantError(t, owner.do(t, "PATCH", "/v1/issues/"+base.Key, map[string]any{"title": "", "labels": strs("Bad")}), 422, "validation_failed")
		if got := e.fields(); !equalStrings(got, []string{"labels[0]", "title"}) {
			t.Errorf("fields = %v", got)
		}
		e = wantError(t, owner.do(t, "PATCH", "/v1/issues/"+base.Key, map[string]any{"depends_on": strs(p.Key + "-999")}), 422, "validation_failed")
		if !e.hasField("depends_on[0]") {
			t.Errorf("details = %+v", e.Error.Details)
		}
	})
}

func TestIssuePatch(t *testing.T) {
	p := createProject(t, "IP")
	parent := newIssue(t, p.Key, "parent", nil)
	iss := newIssue(t, p.Key, "patch me", map[string]any{"labels": strs("api"), "refs": strs("doc:a.md")})

	t.Run("merge patch changes only the sent fields", func(t *testing.T) {
		tick()
		r := as("claude").do(t, "PATCH", "/v1/issues/"+iss.Key, map[string]any{"title": "patched", "labels": strs("api", "cli"), "parent": parent.Key, "body": templateBody(t, "feature")})
		got := decodeAs[issue](t, r, 200)
		if r.header.Get("ETag") != `"2"` {
			t.Errorf("ETag = %q", r.header.Get("ETag"))
		}
		if got.Title != "patched" || *got.Body != templateBody(t, "feature") || !equalStrings(sortedCopy(got.Labels), []string{"api", "cli"}) || got.Parent == nil || *got.Parent != parent.Key {
			t.Errorf("patched = %+v", got)
		}
		if !equalStrings(got.Refs, []string{"doc:a.md"}) || got.Status != "backlog" || got.Priority != "P2" {
			t.Errorf("untouched fields changed: %+v", got)
		}
		if got.Version != 2 || got.UpdatedBy != "claude" || got.CreatedBy != "owner" || !(got.UpdatedAt > iss.UpdatedAt) {
			t.Errorf("bookkeeping = version %d updated_by %s updated_at %s (was %s)", got.Version, got.UpdatedBy, got.UpdatedAt, iss.UpdatedAt)
		}
		if got.StatusChangedAt != iss.StatusChangedAt || got.StatusChangedBy != "owner" {
			t.Errorf("status_changed_* must not move without a status change: %s %s", got.StatusChangedAt, got.StatusChangedBy)
		}
	})
	t.Run("null clears, empty array empties", func(t *testing.T) {
		got := patchIssue(t, owner, iss.Key, map[string]any{"parent": nil, "labels": []string{}, "refs": []string{}})
		if got.Parent != nil || len(got.Labels) != 0 || len(got.Refs) != 0 || got.Version != 3 {
			t.Errorf("cleared = %+v", got)
		}
	})
	t.Run("same values leave the issue untouched", func(t *testing.T) {
		before := getIssue(t, iss.Key)
		acts := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/issues/"+iss.Key+"/activity", nil), 200)
		r := as("gemini").do(t, "PATCH", "/v1/issues/"+iss.Key, map[string]any{"title": before.Title, "labels": []string{}, "parent": nil})
		got := decodeAs[issue](t, r, 200)
		if got.Version != before.Version || got.UpdatedAt != before.UpdatedAt || got.UpdatedBy != before.UpdatedBy {
			t.Errorf("no-op patch changed the issue: %+v -> %+v", before, got)
		}
		if r.header.Get("ETag") != fmt.Sprintf(`"%d"`, before.Version) {
			t.Errorf("ETag = %q", r.header.Get("ETag"))
		}
		after := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/issues/"+iss.Key+"/activity", nil), 200)
		if after.Total != acts.Total {
			t.Errorf("no-op patch wrote activity: %d -> %d", acts.Total, after.Total)
		}
		// An empty patch is also a no-op.
		decodeAs[issue](t, owner.do(t, "PATCH", "/v1/issues/"+iss.Key, map[string]any{}), 200)
	})
	t.Run("server-managed and identity fields are 400", func(t *testing.T) {
		for _, f := range []string{"project", "key", "number", "version", "created_at", "updated_at", "closed_at", "status_changed_at", "status_changed_by", "created_by", "updated_by"} {
			e := wantError(t, owner.do(t, "PATCH", "/v1/issues/"+iss.Key, map[string]any{f: "x"}), 400, "bad_request")
			if !e.hasField(f) {
				t.Errorf("%s: details = %+v", f, e.Error.Details)
			}
		}
	})
	t.Run("GET carries the ETag", func(t *testing.T) {
		r := owner.do(t, "GET", "/v1/issues/"+iss.Key, nil)
		got := decodeAs[issue](t, r, 200)
		if r.header.Get("ETag") != fmt.Sprintf(`"%d"`, got.Version) {
			t.Errorf("ETag = %q for version %d", r.header.Get("ETag"), got.Version)
		}
	})
	t.Run("If-Match", func(t *testing.T) {
		cur := getIssue(t, iss.Key)
		stale := http.Header{"If-Match": {fmt.Sprintf(`"%d"`, cur.Version-1)}}
		wantError(t, owner.do(t, "PATCH", "/v1/issues/"+iss.Key, map[string]any{"title": "stale"}, stale), 412, "precondition_failed")
		wantError(t, owner.do(t, "DELETE", deletePath(iss.Key), nil, stale), 412, "precondition_failed")
		if getIssue(t, iss.Key).Title == "stale" {
			t.Errorf("stale patch was applied")
		}
		for _, bad := range []string{fmt.Sprint(cur.Version), `"abc"`, `W/"1"`, `"`} {
			wantError(t, owner.do(t, "PATCH", "/v1/issues/"+iss.Key, map[string]any{"title": "x"}, http.Header{"If-Match": {bad}}), 400, "bad_request")
		}
		fresh := http.Header{"If-Match": {fmt.Sprintf(`"%d"`, cur.Version)}}
		r := owner.do(t, "PATCH", "/v1/issues/"+iss.Key, map[string]any{"title": "fresh"}, fresh)
		got := decodeAs[issue](t, r, 200)
		if got.Title != "fresh" || got.Version != cur.Version+1 || r.header.Get("ETag") != fmt.Sprintf(`"%d"`, cur.Version+1) {
			t.Errorf("fresh patch = %+v ETag %q", got, r.header.Get("ETag"))
		}
		// If-Match on a no-op still has to match.
		wantError(t, owner.do(t, "PATCH", "/v1/issues/"+iss.Key, map[string]any{"title": "fresh"}, fresh), 412, "precondition_failed")
	})
	t.Run("type change to observation needs the condition in the same patch", func(t *testing.T) {
		wantError(t, owner.do(t, "PATCH", "/v1/issues/"+iss.Key, map[string]any{"type": "observation"}), 422, "validation_failed")
		got := patchIssue(t, owner, iss.Key, map[string]any{"type": "observation", "recheck_condition": "조건"})
		if got.Type != "observation" || got.RecheckCondition == nil || *got.RecheckCondition != "조건" {
			t.Errorf("observation = %+v", got)
		}
		wantError(t, owner.do(t, "PATCH", "/v1/issues/"+iss.Key, map[string]any{"type": "feature"}), 422, "validation_failed")
		got = patchIssue(t, owner, iss.Key, map[string]any{"type": "feature", "recheck_condition": nil})
		if got.Type != "feature" || got.RecheckCondition != nil {
			t.Errorf("back to feature = %+v", got)
		}
	})
}

func TestIssueStatusTransitions(t *testing.T) {
	p := createProject(t, "IT")
	statuses := []string{"backlog", "todo", "doing", "review", "done", "cancelled", "deleted"}
	closed := map[string]bool{"done": true, "cancelled": true, "deleted": true}

	t.Run("every ordered pair is allowed", func(t *testing.T) {
		iss := newIssue(t, p.Key, "transitions", nil)
		for _, from := range statuses {
			for _, to := range statuses {
				if from == to {
					continue
				}
				cur := getIssue(t, iss.Key)
				if cur.Status != from {
					cur = patchIssue(t, owner, iss.Key, statusPatch(from))
				}
				got := patchIssue(t, as("hermes"), iss.Key, statusPatch(to))
				if got.Status != to {
					t.Fatalf("%s -> %s: status = %s", from, to, got.Status)
				}
				wantReason := statusLabel(from) + " → " + statusLabel(to)
				if to == "cancelled" || to == "deleted" {
					wantReason = "e2e 시험"
				}
				if got.StatusReason == nil || *got.StatusReason != wantReason {
					t.Errorf("%s -> %s: status_reason = %v, want %q", from, to, got.StatusReason, wantReason)
				}
				if got.StatusChangedBy != "hermes" || got.Version != cur.Version+1 {
					t.Errorf("%s -> %s: status_changed_by %s version %d (was %d)", from, to, got.StatusChangedBy, got.Version, cur.Version)
				}
				switch {
				case !closed[to]:
					if got.ClosedAt != nil {
						t.Errorf("%s -> %s: closed_at should be null, got %s", from, to, *got.ClosedAt)
					}
				case !closed[from]:
					if got.ClosedAt == nil || *got.ClosedAt != got.StatusChangedAt {
						t.Errorf("%s -> %s: closed_at %v should equal status_changed_at %s", from, to, got.ClosedAt, got.StatusChangedAt)
					}
				default:
					if got.ClosedAt == nil || cur.ClosedAt == nil || *got.ClosedAt != *cur.ClosedAt {
						t.Errorf("%s -> %s: closed_at must be kept between closed states: %v -> %v", from, to, cur.ClosedAt, got.ClosedAt)
					}
				}
			}
		}
	})
	t.Run("status change stamps status_changed_at", func(t *testing.T) {
		iss := newIssue(t, p.Key, "stamp", nil)
		tick()
		got := patchIssue(t, as("codex"), iss.Key, map[string]any{"status": "doing"})
		if !(got.StatusChangedAt > iss.StatusChangedAt) || got.StatusChangedAt != got.UpdatedAt || got.StatusChangedBy != "codex" {
			t.Errorf("stamp = status_changed_at %s (was %s) updated_at %s by %s", got.StatusChangedAt, iss.StatusChangedAt, got.UpdatedAt, got.StatusChangedBy)
		}
		tick()
		again := patchIssue(t, as("gemini"), iss.Key, map[string]any{"priority": "P0"})
		if again.StatusChangedAt != got.StatusChangedAt || again.StatusChangedBy != "codex" {
			t.Errorf("non-status patch moved status_changed_*: %s %s", again.StatusChangedAt, again.StatusChangedBy)
		}
	})
}

// statusLabel is the Korean status name the server uses in default reasons.
func statusLabel(s string) string {
	return map[string]string{"backlog": "백로그", "todo": "할 일", "doing": "진행", "review": "검토", "done": "완료", "cancelled": "취소", "deleted": "삭제"}[s]
}

func TestIssueStatusReason(t *testing.T) {
	p := createProject(t, "IR")
	iss := newIssue(t, p.Key, "reason", map[string]any{"status": "todo"})
	path := "/v1/issues/" + iss.Key
	t.Run("cancelling and deleting need a reason", func(t *testing.T) {
		for _, st := range []string{"cancelled", "deleted"} {
			e := wantError(t, owner.do(t, "PATCH", path, map[string]any{"status": st}), 422, "validation_failed")
			if !e.hasField("status_reason") {
				t.Errorf("%s: details = %+v", st, e.Error.Details)
			}
		}
		e := wantError(t, owner.do(t, "DELETE", "/v1/issues/"+iss.Key, nil), 422, "validation_failed")
		if !e.hasField("status_reason") {
			t.Errorf("DELETE without a reason: details = %+v", e.Error.Details)
		}
		if got := getIssue(t, iss.Key); got.Status != "todo" || got.Version != 1 {
			t.Errorf("refused requests changed the issue: %+v", got)
		}
	})
	t.Run("a reason is only for a status change", func(t *testing.T) {
		for _, body := range []map[string]any{{"status_reason": "x"}, {"status": "todo", "status_reason": "x"}, {"status": "doing", "status_reason": ""}} {
			e := wantError(t, owner.do(t, "PATCH", path, body), 422, "validation_failed")
			if !e.hasField("status_reason") {
				t.Errorf("%v: details = %+v", body, e.Error.Details)
			}
		}
	})
	t.Run("the reason is stored and recorded", func(t *testing.T) {
		got := patchIssue(t, owner, iss.Key, map[string]any{"status": "doing"})
		if got.StatusReason == nil || *got.StatusReason != "할 일 → 진행" {
			t.Errorf("default reason = %v", got.StatusReason)
		}
		got = patchIssue(t, owner, iss.Key, map[string]any{"status": "cancelled", "status_reason": "AGB-20과 중복"})
		if got.StatusReason == nil || *got.StatusReason != "AGB-20과 중복" {
			t.Errorf("reason = %v", got.StatusReason)
		}
		acts := decodeAs[list[activity]](t, owner.do(t, "GET", path+"/activity", nil), 200)
		ch := (*acts.Items[0].Changes)["status_reason"]
		if string(ch.To) != `"AGB-20과 중복"` || string(ch.From) != `"할 일 → 진행"` {
			t.Errorf("status_reason change = %s -> %s", ch.From, ch.To)
		}
		if got := decodeAs[list[issue]](t, owner.do(t, "GET", "/v1/issues?project="+p.Key+"&status=cancelled", nil), 200); len(got.Items) != 1 || got.Items[0].StatusReason == nil {
			t.Errorf("list does not carry status_reason: %+v", got.Items)
		}
	})
}

func TestIssueCycles(t *testing.T) {
	p := createProject(t, "IY")
	a := newIssue(t, p.Key, "a", nil)
	b := newIssue(t, p.Key, "b", map[string]any{"parent": a.Key, "depends_on": strs(a.Key)})
	c := newIssue(t, p.Key, "c", map[string]any{"parent": b.Key, "depends_on": strs(b.Key)})
	cycle := func(t *testing.T, key string, patch map[string]any) {
		t.Helper()
		e := wantError(t, owner.do(t, "PATCH", "/v1/issues/"+key, patch), 409, "conflict")
		if !e.hasReason("cycle") {
			t.Errorf("details = %+v", e.Error.Details)
		}
	}
	t.Run("self reference", func(t *testing.T) {
		cycle(t, a.Key, map[string]any{"parent": a.Key})
		cycle(t, a.Key, map[string]any{"depends_on": strs(a.Key)})
	})
	t.Run("parent chain", func(t *testing.T) {
		cycle(t, a.Key, map[string]any{"parent": b.Key}) // a -> b -> a
		cycle(t, a.Key, map[string]any{"parent": c.Key}) // a -> c -> b -> a
	})
	t.Run("dependency graph", func(t *testing.T) {
		cycle(t, a.Key, map[string]any{"depends_on": strs(b.Key)})
		cycle(t, a.Key, map[string]any{"depends_on": strs(c.Key)})
		// Mixed with a harmless dependency the cycle is still refused.
		d := newIssue(t, p.Key, "d", nil)
		cycle(t, a.Key, map[string]any{"depends_on": strs(d.Key, c.Key)})
	})
	t.Run("the rejected patch left nothing behind", func(t *testing.T) {
		got := getIssue(t, a.Key)
		if got.Parent != nil || len(got.DependsOn) != 0 || got.Version != 1 {
			t.Errorf("a = %+v", got)
		}
	})
	t.Run("a chain without a cycle is fine", func(t *testing.T) {
		d := newIssue(t, p.Key, "d2", map[string]any{"parent": c.Key, "depends_on": strs(c.Key, a.Key)})
		if d.Parent == nil || *d.Parent != c.Key || len(d.DependsOn) != 2 {
			t.Errorf("d = %+v", d)
		}
	})
}

func TestIssueSoftDelete(t *testing.T) {
	p := createProject(t, "ID")
	iss := newIssue(t, p.Key, "delete me", map[string]any{"status": "todo"})
	tick()
	t.Run("DELETE sets status deleted and answers with the issue", func(t *testing.T) {
		r := as("opencode").do(t, "DELETE", "/v1/issues/"+iss.Key+"?reason="+url.QueryEscape("잘못 만든 이슈"), nil)
		got := decodeAs[issue](t, r, 200)
		if got.Status != "deleted" || got.Version != 2 || got.ClosedAt == nil || got.StatusChangedBy != "opencode" || got.UpdatedBy != "opencode" {
			t.Errorf("deleted = %+v", got)
		}
		if got.StatusReason == nil || *got.StatusReason != "잘못 만든 이슈" {
			t.Errorf("status_reason = %v", got.StatusReason)
		}
		if r.header.Get("ETag") != `"2"` {
			t.Errorf("ETag = %q", r.header.Get("ETag"))
		}
	})
	t.Run("deleted issue stays readable", func(t *testing.T) {
		got := getIssue(t, iss.Key)
		if got.Status != "deleted" {
			t.Errorf("get = %+v", got)
		}
		acts := decodeAs[list[activity]](t, owner.do(t, "GET", "/v1/issues/"+iss.Key+"/activity", nil), 200)
		if acts.Total != 2 || acts.Items[0].Kind != "updated" {
			t.Fatalf("activity = %+v", acts)
		}
		ch := (*acts.Items[0].Changes)["status"]
		if string(ch.From) != `"todo"` || string(ch.To) != `"deleted"` {
			t.Errorf("status change = %s -> %s", ch.From, ch.To)
		}
	})
	t.Run("list visibility follows the status filter", func(t *testing.T) {
		q := "/v1/issues?project=" + p.Key
		if got := decodeAs[list[issue]](t, owner.do(t, "GET", q, nil), 200); got.Total != 0 {
			t.Errorf("default (open) list = %v", keys(got.Items))
		}
		for _, f := range []string{"status=deleted", "status=closed", "status=all"} {
			if got := decodeAs[list[issue]](t, owner.do(t, "GET", q+"&"+f, nil), 200); got.Total != 1 || got.Items[0].Key != iss.Key {
				t.Errorf("%s list = %v", f, keys(got.Items))
			}
		}
	})
	t.Run("deleting a deleted issue is refused because the status does not change", func(t *testing.T) {
		e := wantError(t, owner.do(t, "DELETE", deletePath(iss.Key), nil), 422, "validation_failed")
		if !e.hasField("status_reason") {
			t.Errorf("details = %+v", e.Error.Details)
		}
		if got := getIssue(t, iss.Key); got.Version != 2 {
			t.Errorf("version = %d", got.Version)
		}
	})
	t.Run("comments are still accepted", func(t *testing.T) {
		expect(t, owner.do(t, "POST", "/v1/issues/"+iss.Key+"/comments", map[string]any{"body": "삭제 뒤 댓글"}), 201)
	})
	t.Run("any status revives", func(t *testing.T) {
		got := patchIssue(t, owner, iss.Key, map[string]any{"status": "review"})
		if got.Status != "review" || got.ClosedAt != nil || got.Version != 3 {
			t.Errorf("revived = %+v", got)
		}
	})
}

func TestIssueList(t *testing.T) {
	p := createProject(t, "IL")
	la, lb := label("la"), label("lb")
	// Created in this order so created_at is monotonic.
	i1 := newIssue(t, p.Key, "one", map[string]any{"status": "backlog", "type": "feature", "priority": "P2", "labels": strs(la)})
	tick()
	i2 := newIssue(t, p.Key, "two", map[string]any{"status": "todo", "type": "bug", "priority": "P0", "labels": strs(la, lb), "parent": i1.Key})
	tick()
	i3 := createIssue(t, as("claude"), map[string]any{"project": p.Key, "title": "three", "status": "doing", "type": "design", "priority": "P1", "labels": strs(lb), "parent": i1.Key, "body": templateBody(t, "design")})
	tick()
	i4 := newIssue(t, p.Key, "four", map[string]any{"status": "done", "type": "feature", "priority": "P0"})
	tick()
	i5 := newIssue(t, p.Key, "five", map[string]any{"status": "cancelled", "type": "idea", "priority": "P3"})
	tick()
	i6 := newIssue(t, p.Key, "six", map[string]any{"status": "review", "type": "decision", "priority": "P1"})
	tick()
	patchIssue(t, as("codex"), i1.Key, map[string]any{"priority": "P3"}) // updated by codex
	tick()
	patchIssue(t, as("gemini"), i6.Key, map[string]any{"status": "todo"}) // status changed by gemini, updated last
	tick()
	deleteIssue(t, owner, i5.Key)

	get := func(t *testing.T, q string) list[issue] {
		t.Helper()
		return decodeAs[list[issue]](t, owner.do(t, "GET", "/v1/issues?project="+p.Key+"&"+q, nil), 200)
	}
	t.Run("default is open issues by priority then created_at", func(t *testing.T) {
		got := decodeAs[list[issue]](t, owner.do(t, "GET", "/v1/issues?project="+p.Key, nil), 200)
		wantKeys(t, got.Items, i2.Key, i3.Key, i6.Key, i1.Key)
		if got.Total != 4 || got.Limit != 50 || got.Offset != 0 {
			t.Errorf("envelope = %d %d %d", got.Total, got.Limit, got.Offset)
		}
	})
	t.Run("summary items have no body", func(t *testing.T) {
		r := owner.do(t, "GET", "/v1/issues?project="+p.Key+"&limit=1", nil)
		raw := decodeAs[struct {
			Items []map[string]any `json:"items"`
		}](t, r, 200)
		if len(raw.Items) != 1 {
			t.Fatalf("items = %d", len(raw.Items))
		}
		if _, has := raw.Items[0]["body"]; has {
			t.Errorf("summary carries body: %v", raw.Items[0])
		}
		for _, k := range []string{"key", "project", "number", "title", "type", "status", "priority", "labels", "parent", "depends_on", "refs", "recheck_condition", "version", "created_at", "updated_at", "closed_at", "status_changed_at", "status_changed_by", "created_by", "updated_by"} {
			if _, has := raw.Items[0][k]; !has {
				t.Errorf("summary lacks %s", k)
			}
		}
		if _, has := raw.Items[0]["snippet"]; has {
			t.Errorf("snippet present without q")
		}
	})
	t.Run("status filters and aliases", func(t *testing.T) {
		wantKeys(t, get(t, "status=closed&sort=key").Items, i4.Key, i5.Key)
		wantKeys(t, get(t, "status=all&sort=key").Items, i1.Key, i2.Key, i3.Key, i4.Key, i5.Key, i6.Key)
		wantKeys(t, get(t, "status=todo,doing&sort=key").Items, i2.Key, i3.Key, i6.Key)
		wantKeys(t, get(t, "status=deleted").Items, i5.Key)
		wantKeys(t, get(t, "status=open,done&sort=key").Items, i1.Key, i2.Key, i3.Key, i4.Key, i6.Key)
	})
	t.Run("attribute filters", func(t *testing.T) {
		wantKeys(t, get(t, "type=bug,design&sort=key").Items, i2.Key, i3.Key)
		wantKeys(t, get(t, "priority=P1&sort=key").Items, i3.Key, i6.Key)
		wantKeys(t, get(t, "priority=P0&status=all").Items, i2.Key, i4.Key)
		wantKeys(t, get(t, "label="+la+"&sort=key").Items, i1.Key, i2.Key)
		wantKeys(t, get(t, "label="+la+"&label="+lb).Items, i2.Key)
		wantKeys(t, get(t, "parent="+i1.Key+"&sort=key").Items, i2.Key, i3.Key)
		wantKeys(t, get(t, "parent=none&sort=key").Items, i1.Key, i6.Key)
		wantKeys(t, get(t, "created_by=claude").Items, i3.Key)
		wantKeys(t, get(t, "created_by=claude,owner&sort=key").Items, i1.Key, i2.Key, i3.Key, i6.Key)
		wantKeys(t, get(t, "updated_by=codex").Items, i1.Key)
		wantKeys(t, get(t, "status_changed_by=gemini").Items, i6.Key)
		wantKeys(t, get(t, "status_changed_by=gemini&type=bug").Items)
	})
	t.Run("sort fields", func(t *testing.T) {
		wantKeys(t, get(t, "sort=-created_at").Items, i6.Key, i3.Key, i2.Key, i1.Key)
		wantKeys(t, get(t, "sort=created_at").Items, i1.Key, i2.Key, i3.Key, i6.Key)
		wantKeys(t, get(t, "sort=-updated_at&limit=2").Items, i6.Key, i1.Key)
		wantKeys(t, get(t, "sort=-key").Items, i6.Key, i3.Key, i2.Key, i1.Key)
		wantKeys(t, get(t, "sort=-priority,created_at").Items, i1.Key, i3.Key, i6.Key, i2.Key)
		wantKeys(t, get(t, "sort=priority,-created_at").Items, i2.Key, i6.Key, i3.Key, i1.Key)
	})
	t.Run("pagination", func(t *testing.T) {
		got := get(t, "status=all&sort=key&limit=2&offset=0")
		wantKeys(t, got.Items, i1.Key, i2.Key)
		if got.Total != 6 || got.Limit != 2 || got.Offset != 0 {
			t.Errorf("envelope = %d %d %d", got.Total, got.Limit, got.Offset)
		}
		got = get(t, "status=all&sort=key&limit=2&offset=4")
		wantKeys(t, got.Items, i5.Key, i6.Key)
		got = get(t, "status=all&sort=key&limit=2&offset=6")
		if len(got.Items) != 0 || got.Total != 6 || got.Items == nil {
			t.Errorf("past the end = %+v", got)
		}
		got = get(t, "status=all&limit=200")
		if got.Limit != 200 {
			t.Errorf("limit = %d", got.Limit)
		}
	})
	t.Run("multi-project OR and no project", func(t *testing.T) {
		q := createProject(t, "IL2")
		x := newIssue(t, q.Key, "x", nil)
		got := decodeAs[list[issue]](t, owner.do(t, "GET", "/v1/issues?project="+p.Key+","+q.Key+"&sort=key&status=all", nil), 200)
		wantKeys(t, got.Items, i1.Key, i2.Key, i3.Key, i4.Key, i5.Key, i6.Key, x.Key)
		all := decodeAs[list[issue]](t, owner.do(t, "GET", "/v1/issues?status=all&limit=1", nil), 200)
		if all.Total < 7 {
			t.Errorf("global total = %d", all.Total)
		}
	})
	t.Run("bad query parameters are 400", func(t *testing.T) {
		for _, q := range []string{
			"status=nope", "type=task", "priority=P9", "sort=title", "sort=relevance", "limit=0", "limit=201",
			"offset=x", "parent=bad", "label=BAD", "project=bad", "status=todo&status=doing", "type=bug&type=idea", "unknown=1",
		} {
			wantError(t, owner.do(t, "GET", "/v1/issues?project="+p.Key+"&"+q, nil), 400, "bad_request")
		}
	})
}

func TestIssueSearch(t *testing.T) {
	p := createProject(t, "IS")
	s1 := newIssue(t, p.Key, "드라이버에서 설계안 검토", map[string]any{"body": "FTS 토크나이저 실험 기록"})
	s2 := newIssue(t, p.Key, "agent-board api design", map[string]any{"body": "라벨 필터를 더한다"})
	s3 := newIssue(t, p.Key, "unrelated title", map[string]any{"body": "nothing here", "status": "done"})
	// search lists the project's issues of every status matching q; extra
	// holds further name/value pairs.
	search := func(t *testing.T, q string, extra ...string) list[issue] {
		t.Helper()
		v := url.Values{"project": {p.Key}, "status": {"all"}, "q": {q}}
		for i := 0; i+1 < len(extra); i += 2 {
			v.Set(extra[i], extra[i+1])
		}
		return decodeAs[list[issue]](t, owner.do(t, "GET", "/v1/issues?"+v.Encode(), nil), 200)
	}
	t.Run("korean prefix match on title", func(t *testing.T) {
		got := search(t, "드라이버")
		wantKeys(t, got.Items, s1.Key)
		if got.Items[0].Snippet == nil || !strings.Contains(*got.Items[0].Snippet, "[") || !strings.Contains(*got.Items[0].Snippet, "]") {
			t.Errorf("snippet = %v", got.Items[0].Snippet)
		}
	})
	t.Run("korean prefix match on compound and body", func(t *testing.T) {
		wantKeys(t, search(t, "설계").Items, s1.Key)
		wantKeys(t, search(t, "토크나이저").Items, s1.Key)
		wantKeys(t, search(t, "라벨").Items, s2.Key)
	})
	t.Run("terms are ANDed", func(t *testing.T) {
		wantKeys(t, search(t, "설계 api").Items)
		wantKeys(t, search(t, "api 라벨").Items, s2.Key)
		wantKeys(t, search(t, "api").Items, s2.Key)
	})
	t.Run("key is indexed", func(t *testing.T) {
		wantKeys(t, search(t, s3.Key).Items, s3.Key)
	})
	t.Run("hyphenated input is not FTS syntax", func(t *testing.T) {
		wantKeys(t, search(t, "agent-board").Items, s2.Key)
		wantKeys(t, search(t, "api OR nothing").Items)
		wantKeys(t, search(t, `api"`).Items, s2.Key)
	})
	t.Run("quoted input is one phrase", func(t *testing.T) {
		wantKeys(t, search(t, `"api design"`).Items, s2.Key)
		wantKeys(t, search(t, `"design api"`).Items)
	})
	t.Run("search honours the other filters", func(t *testing.T) {
		v := url.Values{"project": {p.Key}, "q": {"title"}}
		got := decodeAs[list[issue]](t, owner.do(t, "GET", "/v1/issues?"+v.Encode(), nil), 200)
		wantKeys(t, got.Items) // s3 is done and the default status is open
		wantKeys(t, search(t, "title", "status", "done").Items, s3.Key)
	})
	t.Run("relevance sort needs q", func(t *testing.T) {
		expect(t, owner.do(t, "GET", "/v1/issues?project="+p.Key+"&q=api&sort=relevance", nil), 200)
		expect(t, owner.do(t, "GET", "/v1/issues?project="+p.Key+"&q=api&sort=-key", nil), 200)
		wantError(t, owner.do(t, "GET", "/v1/issues?project="+p.Key+"&sort=relevance", nil), 400, "bad_request")
		wantError(t, owner.do(t, "GET", "/v1/issues?project="+p.Key+"&q=%20%20&sort=relevance", nil), 400, "bad_request")
	})
	t.Run("blank q is no search", func(t *testing.T) {
		got := search(t, "   ", "sort", "key")
		wantKeys(t, got.Items, s1.Key, s2.Key, s3.Key)
		for _, it := range got.Items {
			if it.Snippet != nil {
				t.Errorf("snippet on a blank search: %s", *it.Snippet)
			}
		}
	})
	t.Run("more than ten terms is 400", func(t *testing.T) {
		wantError(t, owner.do(t, "GET", "/v1/issues?q="+strings.Repeat("a+", 10)+"a", nil), 400, "bad_request")
		expect(t, owner.do(t, "GET", "/v1/issues?q="+strings.Repeat("a+", 9)+"a", nil), 200)
	})
}
