//go:build e2e

package e2e

import (
	"strconv"
	"strings"
	"testing"
)

func TestProjectCreate(t *testing.T) {
	key := projectKey("PC")
	r := as("claude").do(t, "POST", "/v1/projects", map[string]any{"key": key, "name": "프로젝트 생성"})
	p := decodeAs[project](t, r, 201)
	if r.header.Get("Location") != "/v1/projects/"+key {
		t.Errorf("Location = %q", r.header.Get("Location"))
	}
	if p.Key != key || p.Name != "프로젝트 생성" || p.Status != "active" || p.CreatedBy != "claude" || p.UpdatedBy != "claude" {
		t.Errorf("project = %+v", p)
	}
	wantTime(t, "created_at", p.CreatedAt)
	wantTime(t, "updated_at", p.UpdatedAt)
	if p.CreatedAt != p.UpdatedAt {
		t.Errorf("created_at %s != updated_at %s on creation", p.CreatedAt, p.UpdatedAt)
	}

	t.Run("duplicate key is 409 duplicate_key", func(t *testing.T) {
		e := wantError(t, owner.do(t, "POST", "/v1/projects", map[string]any{"key": key, "name": "again"}), 409, "conflict")
		if !e.hasReason("duplicate_key") {
			t.Errorf("details = %+v", e.Error.Details)
		}
	})
	t.Run("get returns the project", func(t *testing.T) {
		got := decodeAs[project](t, owner.do(t, "GET", "/v1/projects/"+key, nil), 200)
		if got != p {
			t.Errorf("get = %+v, want %+v", got, p)
		}
	})
	t.Run("unknown project is 404", func(t *testing.T) {
		wantError(t, owner.do(t, "GET", "/v1/projects/"+projectKey("NOPE"), nil), 404, "not_found")
		wantError(t, owner.do(t, "PATCH", "/v1/projects/"+projectKey("NOPE"), map[string]any{"name": "x"}), 404, "not_found")
		wantError(t, owner.do(t, "DELETE", "/v1/projects/"+projectKey("NOPE"), nil), 404, "not_found")
		wantError(t, owner.do(t, "GET", "/v1/projects/"+projectKey("NOPE")+"/overview", nil), 404, "not_found")
	})
}

func TestProjectValidation(t *testing.T) {
	cases := []struct {
		name   string
		body   map[string]any
		fields []string
	}{
		{"lowercase key", map[string]any{"key": "abc", "name": "n"}, []string{"key"}},
		{"key too short", map[string]any{"key": "A", "name": "n"}, []string{"key"}},
		{"key too long", map[string]any{"key": "ABCDEFGHIJK", "name": "n"}, []string{"key"}},
		{"key starts with digit", map[string]any{"key": "1AB", "name": "n"}, []string{"key"}},
		{"empty name", map[string]any{"key": projectKey("PV"), "name": ""}, []string{"name"}},
		{"name too long", map[string]any{"key": projectKey("PV"), "name": strings.Repeat("가", 101)}, []string{"name"}},
		{"both wrong reported together", map[string]any{"key": "bad key", "name": ""}, []string{"key", "name"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := wantError(t, owner.do(t, "POST", "/v1/projects", c.body), 422, "validation_failed")
			if got := e.fields(); !equalStrings(got, c.fields) {
				t.Errorf("fields = %v, want %v", got, c.fields)
			}
		})
	}
	t.Run("name of 100 runes is accepted", func(t *testing.T) {
		p := decodeAs[project](t, owner.do(t, "POST", "/v1/projects", map[string]any{"key": projectKey("PN"), "name": strings.Repeat("가", 100)}), 201)
		if len([]rune(p.Name)) != 100 {
			t.Errorf("name runes = %d", len([]rune(p.Name)))
		}
	})
	t.Run("unknown body field is 400", func(t *testing.T) {
		e := wantError(t, owner.do(t, "POST", "/v1/projects", map[string]any{"key": projectKey("PU"), "name": "n", "status": "active"}), 400, "bad_request")
		if !e.hasField("status") {
			t.Errorf("details = %+v", e.Error.Details)
		}
	})
	t.Run("invalid JSON is 400", func(t *testing.T) {
		wantError(t, owner.doRaw(t, "POST", "/v1/projects", []byte(`{"key": `)), 400, "bad_request")
		wantError(t, owner.doRaw(t, "POST", "/v1/projects", []byte(`[1,2]`)), 400, "bad_request")
	})
	t.Run("body over 1 MiB is 413", func(t *testing.T) {
		prefix := `{"key":"` + projectKey("PB") + `","name":"n","pad":"`
		suffix := `"}`
		pad := strings.Repeat("a", 1<<20+1-len(prefix)-len(suffix))
		body := []byte(prefix + pad + suffix)
		if len(body) != 1<<20+1 {
			t.Fatalf("body length %d", len(body))
		}
		wantError(t, owner.doRaw(t, "POST", "/v1/projects", body), 413, "payload_too_large")
	})
	t.Run("body of exactly 1 MiB is read", func(t *testing.T) {
		prefix := `{"key":"` + projectKey("PB") + `","name":"n","pad":"`
		suffix := `"}`
		pad := strings.Repeat("a", 1<<20-len(prefix)-len(suffix))
		body := []byte(prefix + pad + suffix)
		if len(body) != 1<<20 {
			t.Fatalf("body length %d", len(body))
		}
		// The size is fine, so the unknown field "pad" is what gets rejected.
		e := wantError(t, owner.doRaw(t, "POST", "/v1/projects", body), 400, "bad_request")
		if !e.hasField("pad") {
			t.Errorf("details = %+v", e.Error.Details)
		}
	})
}

func TestProjectUpdateAndSoftDelete(t *testing.T) {
	p := createProject(t, "PS")
	tick()
	t.Run("patch name", func(t *testing.T) {
		got := decodeAs[project](t, as("codex").do(t, "PATCH", "/v1/projects/"+p.Key, map[string]any{"name": "renamed"}), 200)
		if got.Name != "renamed" || got.Status != "active" || got.UpdatedBy != "codex" || got.CreatedBy != "owner" {
			t.Errorf("patched = %+v", got)
		}
		if !(got.UpdatedAt > p.UpdatedAt) {
			t.Errorf("updated_at %s should be after %s", got.UpdatedAt, p.UpdatedAt)
		}
	})
	t.Run("patch rejects key and unknown fields", func(t *testing.T) {
		wantError(t, owner.do(t, "PATCH", "/v1/projects/"+p.Key, map[string]any{"key": "OTHER"}), 400, "bad_request")
		wantError(t, owner.do(t, "PATCH", "/v1/projects/"+p.Key, map[string]any{"created_by": "x"}), 400, "bad_request")
	})
	t.Run("patch validates values", func(t *testing.T) {
		e := wantError(t, owner.do(t, "PATCH", "/v1/projects/"+p.Key, map[string]any{"name": ""}), 422, "validation_failed")
		if !e.hasField("name") {
			t.Errorf("details = %+v", e.Error.Details)
		}
		e = wantError(t, owner.do(t, "PATCH", "/v1/projects/"+p.Key, map[string]any{"status": "paused"}), 422, "validation_failed")
		if !e.hasField("status") {
			t.Errorf("details = %+v", e.Error.Details)
		}
	})
	t.Run("archived project refuses issue writes but accepts its own patch", func(t *testing.T) {
		iss := newIssue(t, p.Key, "before archive", nil)
		got := decodeAs[project](t, owner.do(t, "PATCH", "/v1/projects/"+p.Key, map[string]any{"status": "archived"}), 200)
		if got.Status != "archived" {
			t.Errorf("status = %s", got.Status)
		}
		e := wantError(t, owner.do(t, "POST", "/v1/issues", map[string]any{"project": p.Key, "title": "x"}), 409, "conflict")
		if !e.hasReason("project_not_active") {
			t.Errorf("details = %+v", e.Error.Details)
		}
		e = wantError(t, owner.do(t, "PATCH", "/v1/issues/"+iss.Key, map[string]any{"title": "y"}), 409, "conflict")
		if !e.hasReason("project_not_active") {
			t.Errorf("details = %+v", e.Error.Details)
		}
		e = wantError(t, owner.do(t, "DELETE", deletePath(iss.Key), nil), 409, "conflict")
		if !e.hasReason("project_not_active") {
			t.Errorf("details = %+v", e.Error.Details)
		}
		e = wantError(t, owner.do(t, "POST", "/v1/issues/"+iss.Key+"/comments", map[string]any{"body": "c"}), 409, "conflict")
		if !e.hasReason("project_not_active") {
			t.Errorf("details = %+v", e.Error.Details)
		}
		// Reads stay open.
		expect(t, owner.do(t, "GET", "/v1/issues/"+iss.Key, nil), 200)
		// The project itself can still be renamed while archived.
		got = decodeAs[project](t, owner.do(t, "PATCH", "/v1/projects/"+p.Key, map[string]any{"name": "archived name"}), 200)
		if got.Name != "archived name" || got.Status != "archived" {
			t.Errorf("patched archived = %+v", got)
		}
	})
	t.Run("delete is a status change and is reversible", func(t *testing.T) {
		got := decodeAs[project](t, as("gemini").do(t, "DELETE", "/v1/projects/"+p.Key, nil), 200)
		if got.Status != "deleted" || got.UpdatedBy != "gemini" {
			t.Errorf("deleted = %+v", got)
		}
		// Still readable, still refuses issue writes.
		got = decodeAs[project](t, owner.do(t, "GET", "/v1/projects/"+p.Key, nil), 200)
		if got.Status != "deleted" {
			t.Errorf("get after delete = %+v", got)
		}
		e := wantError(t, owner.do(t, "POST", "/v1/issues", map[string]any{"project": p.Key, "title": "x"}), 409, "conflict")
		if !e.hasReason("project_not_active") {
			t.Errorf("details = %+v", e.Error.Details)
		}
		// Deleting again changes nothing and still answers 200.
		got = decodeAs[project](t, owner.do(t, "DELETE", "/v1/projects/"+p.Key, nil), 200)
		if got.Status != "deleted" {
			t.Errorf("second delete = %+v", got)
		}
		// Revive.
		got = decodeAs[project](t, owner.do(t, "PATCH", "/v1/projects/"+p.Key, map[string]any{"status": "active"}), 200)
		if got.Status != "active" {
			t.Errorf("revived = %+v", got)
		}
		expect(t, owner.do(t, "POST", "/v1/issues", map[string]any{"project": p.Key, "title": "after revive"}), 201)
	})
}

func TestProjectList(t *testing.T) {
	active := createProject(t, "LA")
	tick()
	archived := createProject(t, "LB")
	tick()
	deleted := createProject(t, "LC")
	tick()
	decodeAs[project](t, owner.do(t, "PATCH", "/v1/projects/"+archived.Key, map[string]any{"status": "archived"}), 200)
	tick()
	decodeAs[project](t, owner.do(t, "DELETE", "/v1/projects/"+deleted.Key, nil), 200)
	mine := func(items []project) []string {
		var out []string
		for _, p := range items {
			if strings.HasPrefix(p.Key, projectKey("L")) {
				out = append(out, p.Key)
			}
		}
		return out
	}
	all := func(path string) list[project] {
		// Pages through so that other runs' projects do not push ours out.
		var out list[project]
		for offset := 0; ; offset += 200 {
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}
			page := decodeAs[list[project]](t, owner.do(t, "GET", path+sep+"limit=200&offset="+strconv.Itoa(offset), nil), 200)
			out.Items = append(out.Items, page.Items...)
			out.Total = page.Total
			if len(page.Items) < 200 {
				return out
			}
		}
	}

	t.Run("default is active only", func(t *testing.T) {
		got := mine(all("/v1/projects").Items)
		if !equalStrings(got, []string{active.Key}) {
			t.Errorf("active list = %v", got)
		}
	})
	t.Run("status=all includes archived and deleted", func(t *testing.T) {
		got := mine(all("/v1/projects?status=all&sort=key").Items)
		if !equalStrings(got, []string{active.Key, archived.Key, deleted.Key}) {
			t.Errorf("all list = %v", got)
		}
	})
	t.Run("status comma is OR", func(t *testing.T) {
		got := mine(all("/v1/projects?status=archived,deleted&sort=key").Items)
		if !equalStrings(got, []string{archived.Key, deleted.Key}) {
			t.Errorf("archived,deleted list = %v", got)
		}
		got = mine(all("/v1/projects?status=deleted").Items)
		if !equalStrings(got, []string{deleted.Key}) {
			t.Errorf("deleted list = %v", got)
		}
	})
	t.Run("sort fields", func(t *testing.T) {
		got := mine(all("/v1/projects?status=all&sort=-key").Items)
		if !equalStrings(got, []string{deleted.Key, archived.Key, active.Key}) {
			t.Errorf("-key list = %v", got)
		}
		got = mine(all("/v1/projects?status=all&sort=created_at").Items)
		if !equalStrings(got, []string{active.Key, archived.Key, deleted.Key}) {
			t.Errorf("created_at list = %v", got)
		}
		got = mine(all("/v1/projects?status=all&sort=-updated_at").Items)
		// active was never updated; archived then deleted were patched in that order.
		if !equalStrings(got, []string{deleted.Key, archived.Key, active.Key}) {
			t.Errorf("-updated_at list = %v", got)
		}
	})
	t.Run("envelope and pagination", func(t *testing.T) {
		page := decodeAs[list[project]](t, owner.do(t, "GET", "/v1/projects?status=all", nil), 200)
		if page.Limit != 50 || page.Offset != 0 || page.Total < 3 {
			t.Errorf("envelope = total %d limit %d offset %d", page.Total, page.Limit, page.Offset)
		}
		page = decodeAs[list[project]](t, owner.do(t, "GET", "/v1/projects?status=all&limit=1&offset=1", nil), 200)
		if page.Limit != 1 || page.Offset != 1 || len(page.Items) != 1 {
			t.Errorf("page = total %d limit %d offset %d items %d", page.Total, page.Limit, page.Offset, len(page.Items))
		}
		page = decodeAs[list[project]](t, owner.do(t, "GET", "/v1/projects?status=all&limit=200", nil), 200)
		if page.Limit != 200 {
			t.Errorf("limit = %d", page.Limit)
		}
	})
	t.Run("bad query parameters are 400", func(t *testing.T) {
		for _, q := range []string{
			"status=paused", "sort=name", "sort=-name", "limit=0", "limit=201", "limit=abc", "offset=-1",
			"status=active&status=archived", "foo=bar", "project=X",
		} {
			wantError(t, owner.do(t, "GET", "/v1/projects?"+q, nil), 400, "bad_request")
		}
	})
	t.Run("list items are plain project objects", func(t *testing.T) {
		page := decodeAs[list[project]](t, owner.do(t, "GET", "/v1/projects?status=all&sort=-created_at&limit=200", nil), 200)
		found := false
		for _, p := range page.Items {
			if p.Key == archived.Key {
				found = true
				if p.Status != "archived" || p.Name != "e2e "+archived.Key || p.CreatedBy != "owner" {
					t.Errorf("item = %+v", p)
				}
			}
		}
		if !found {
			t.Errorf("project %s not in first 200 by -created_at", archived.Key)
		}
	})
}
