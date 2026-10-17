//go:build e2e

package e2e

import (
	"testing"
)

func TestBoard(t *testing.T) {
	p := createProject(t, "BD")
	q := createProject(t, "BD2")
	b1 := newIssue(t, p.Key, "backlog", map[string]any{"priority": "P2"})
	tick()
	b2 := newIssue(t, p.Key, "todo P1", map[string]any{"status": "todo", "priority": "P1"})
	tick()
	b3 := newIssue(t, p.Key, "todo P0", map[string]any{"status": "todo", "priority": "P0"})
	tick()
	b4 := newIssue(t, p.Key, "todo P1 later", map[string]any{"status": "todo", "priority": "P1"})
	tick()
	b5 := newIssue(t, p.Key, "doing", map[string]any{"status": "doing"})
	b6 := newIssue(t, p.Key, "done", map[string]any{"status": "done"})
	b7 := newIssue(t, p.Key, "cancelled", map[string]any{"status": "cancelled"})
	b8 := newIssue(t, p.Key, "deleted", nil)
	decodeAs[issue](t, owner.do(t, "DELETE", "/v1/issues/"+b8.Key, nil), 200)
	other := newIssue(t, q.Key, "other backlog", nil)

	columns := func(b board) map[string]boardColumn {
		m := map[string]boardColumn{}
		for _, c := range b.Columns {
			m[c.Status] = c
		}
		return m
	}
	t.Run("columns are fixed and ordered by priority then created_at", func(t *testing.T) {
		b := decodeAs[board](t, owner.do(t, "GET", "/v1/board?project="+p.Key, nil), 200)
		if b.Project == nil || *b.Project != p.Key {
			t.Errorf("project = %v", b.Project)
		}
		order := make([]string, len(b.Columns))
		for i, c := range b.Columns {
			order[i] = c.Status
		}
		if !equalStrings(order, []string{"backlog", "todo", "doing", "review", "done", "cancelled", "deleted"}) {
			t.Fatalf("column order = %v", order)
		}
		cols := columns(b)
		wantKeys(t, cols["backlog"].Issues, b1.Key)
		wantKeys(t, cols["todo"].Issues, b3.Key, b2.Key, b4.Key)
		wantKeys(t, cols["doing"].Issues, b5.Key)
		wantKeys(t, cols["review"].Issues)
		wantKeys(t, cols["done"].Issues, b6.Key)
		wantKeys(t, cols["cancelled"].Issues, b7.Key)
		wantKeys(t, cols["deleted"].Issues, b8.Key)
		for _, st := range []string{"backlog", "todo", "doing", "review", "done", "cancelled", "deleted"} {
			if cols[st].Total != len(cols[st].Issues) {
				t.Errorf("%s total %d vs %d issues", st, cols[st].Total, len(cols[st].Issues))
			}
			if cols[st].Issues == nil {
				t.Errorf("%s issues is null", st)
			}
		}
		if cols["todo"].Issues[0].Body != nil {
			t.Errorf("board items must be summaries")
		}
	})
	t.Run("per-column limit keeps the full total", func(t *testing.T) {
		b := decodeAs[board](t, owner.do(t, "GET", "/v1/board?project="+p.Key+"&limit=1", nil), 200)
		cols := columns(b)
		wantKeys(t, cols["todo"].Issues, b3.Key)
		if cols["todo"].Total != 3 {
			t.Errorf("todo total = %d", cols["todo"].Total)
		}
	})
	t.Run("closed_days", func(t *testing.T) {
		b := decodeAs[board](t, owner.do(t, "GET", "/v1/board?project="+p.Key+"&closed_days=0", nil), 200)
		cols := columns(b)
		if cols["done"].Total != 1 || cols["deleted"].Total != 1 {
			t.Errorf("closed_days=0 totals = done %d deleted %d", cols["done"].Total, cols["deleted"].Total)
		}
		b = decodeAs[board](t, owner.do(t, "GET", "/v1/board?project="+p.Key+"&closed_days=1", nil), 200)
		if columns(b)["done"].Total != 1 {
			t.Errorf("closed_days=1 should include an issue closed just now")
		}
	})
	t.Run("several projects", func(t *testing.T) {
		b := decodeAs[board](t, owner.do(t, "GET", "/v1/board?project="+p.Key+","+q.Key, nil), 200)
		if b.Project == nil || *b.Project != p.Key+","+q.Key {
			t.Errorf("project = %v", b.Project)
		}
		wantKeys(t, columns(b)["backlog"].Issues, b1.Key, other.Key)
	})
	t.Run("no project means every project", func(t *testing.T) {
		b := decodeAs[board](t, owner.do(t, "GET", "/v1/board?limit=200", nil), 200)
		if b.Project != nil {
			t.Errorf("project = %q, want null", *b.Project)
		}
		if len(b.Columns) != 7 {
			t.Errorf("columns = %d", len(b.Columns))
		}
		found := false
		for _, it := range columns(b)["doing"].Issues {
			if it.Key == b5.Key {
				found = true
			}
		}
		if !found && columns(b)["doing"].Total <= 200 {
			t.Errorf("global board lacks %s", b5.Key)
		}
	})
	t.Run("bad query parameters are 400", func(t *testing.T) {
		for _, q := range []string{"project=bad", "closed_days=-1", "closed_days=x", "limit=0", "limit=201", "status=todo", "project=A&project=B"} {
			wantError(t, owner.do(t, "GET", "/v1/board?"+q, nil), 400, "bad_request")
		}
	})
}

func TestQueue(t *testing.T) {
	p := createProject(t, "QU")
	a := newIssue(t, p.Key, "A todo P1", map[string]any{"status": "todo", "priority": "P1"})
	tick()
	b := newIssue(t, p.Key, "B todo P0 after A", map[string]any{"status": "todo", "priority": "P0", "depends_on": strs(a.Key)})
	tick()
	c := newIssue(t, p.Key, "C todo P0", map[string]any{"status": "todo", "priority": "P0"})
	tick()
	e := newIssue(t, p.Key, "E done P3", map[string]any{"status": "done", "priority": "P3"})
	tick()
	d := newIssue(t, p.Key, "D todo P2 after A and E", map[string]any{"status": "todo", "priority": "P2", "depends_on": strs(a.Key, e.Key)})
	tick()
	f := newIssue(t, p.Key, "F backlog P0", map[string]any{"priority": "P0"})

	queue := func(t *testing.T, q string) list[issue] {
		t.Helper()
		return decodeAs[list[issue]](t, owner.do(t, "GET", "/v1/queue?project="+p.Key+q, nil), 200)
	}
	item := func(items []issue, key string) issue {
		for _, it := range items {
			if it.Key == key {
				return it
			}
		}
		t.Fatalf("%s not in queue", key)
		return issue{}
	}
	t.Run("default status todo ordered by priority, ready, created_at", func(t *testing.T) {
		got := queue(t, "")
		wantKeys(t, got.Items, c.Key, b.Key, a.Key, d.Key)
		if got.Total != 4 || got.Limit != 50 || got.Offset != 0 {
			t.Errorf("envelope = %d %d %d", got.Total, got.Limit, got.Offset)
		}
		for key, want := range map[string][]string{c.Key: {}, b.Key: {a.Key}, a.Key: {}, d.Key: {a.Key}} {
			it := item(got.Items, key)
			if it.Ready == nil || *it.Ready != (len(want) == 0) || it.BlockedBy == nil || !equalStrings(it.BlockedBy, want) {
				t.Errorf("%s ready %v blocked_by %v, want blocked_by %v", key, it.Ready, it.BlockedBy, want)
			}
			if it.Body != nil {
				t.Errorf("queue items must be summaries")
			}
		}
	})
	t.Run("limit=1 is the next thing to do", func(t *testing.T) {
		got := queue(t, "&limit=1")
		wantKeys(t, got.Items, c.Key)
		if got.Total != 4 {
			t.Errorf("total = %d", got.Total)
		}
	})
	t.Run("status filters and aliases", func(t *testing.T) {
		wantKeys(t, queue(t, "&status=todo,backlog").Items, c.Key, f.Key, b.Key, a.Key, d.Key)
		wantKeys(t, queue(t, "&status=open").Items, c.Key, f.Key, b.Key, a.Key, d.Key)
		wantKeys(t, queue(t, "&status=closed").Items, e.Key)
		wantKeys(t, queue(t, "&status=all").Items, c.Key, f.Key, b.Key, a.Key, d.Key, e.Key)
		wantKeys(t, queue(t, "&status=backlog").Items, f.Key)
	})
	t.Run("closing a dependency makes dependants ready", func(t *testing.T) {
		patchIssue(t, owner, a.Key, map[string]any{"status": "done"})
		got := queue(t, "")
		wantKeys(t, got.Items, b.Key, c.Key, d.Key)
		for _, it := range got.Items {
			if it.Ready == nil || !*it.Ready || len(it.BlockedBy) != 0 {
				t.Errorf("%s ready %v blocked_by %v", it.Key, it.Ready, it.BlockedBy)
			}
		}
		if got := getIssue(t, b.Key); got.Version != 1 {
			t.Errorf("closing a dependency wrote to the dependant: version %d", got.Version)
		}
		patchIssue(t, owner, a.Key, map[string]any{"status": "todo"})
		wantKeys(t, queue(t, "").Items, c.Key, b.Key, a.Key, d.Key)
	})
	t.Run("pagination", func(t *testing.T) {
		got := queue(t, "&limit=2&offset=2")
		wantKeys(t, got.Items, a.Key, d.Key)
		if got.Total != 4 || got.Limit != 2 || got.Offset != 2 {
			t.Errorf("envelope = %d %d %d", got.Total, got.Limit, got.Offset)
		}
	})
	t.Run("no project spans every project", func(t *testing.T) {
		got := decodeAs[list[issue]](t, owner.do(t, "GET", "/v1/queue?limit=1", nil), 200)
		if got.Total < 4 || len(got.Items) != 1 {
			t.Errorf("global queue = total %d items %d", got.Total, len(got.Items))
		}
	})
	t.Run("bad query parameters are 400", func(t *testing.T) {
		for _, q := range []string{"status=nope", "project=bad", "limit=0", "limit=201", "sort=priority", "label=x", "project=A&project=B"} {
			wantError(t, owner.do(t, "GET", "/v1/queue?"+q, nil), 400, "bad_request")
		}
	})
}

func TestLabels(t *testing.T) {
	p := createProject(t, "TL")
	q := createProject(t, "TL2")
	la, lb, lc, ld := label("alpha"), label("beta"), label("gamma"), label("delta")
	newIssue(t, p.Key, "1", map[string]any{"labels": strs(la, lb)})
	newIssue(t, p.Key, "2", map[string]any{"labels": strs(la), "status": "todo"})
	newIssue(t, p.Key, "3", map[string]any{"labels": strs(la, lc), "status": "done"})
	newIssue(t, p.Key, "4", map[string]any{"labels": strs(ld, lb), "status": "doing"})
	newIssue(t, q.Key, "5", map[string]any{"labels": strs(lc)})

	t.Run("open issues only, count desc then label asc", func(t *testing.T) {
		got := decodeAs[struct {
			Items []labelCount `json:"items"`
		}](t, owner.do(t, "GET", "/v1/labels?project="+p.Key, nil), 200)
		want := []labelCount{{la, 2}, {lb, 2}, {ld, 1}}
		if len(got.Items) != len(want) {
			t.Fatalf("labels = %+v, want %+v", got.Items, want)
		}
		for i := range want {
			if got.Items[i] != want[i] {
				t.Errorf("labels[%d] = %+v, want %+v", i, got.Items[i], want[i])
			}
		}
	})
	t.Run("several projects and no project", func(t *testing.T) {
		got := decodeAs[struct {
			Items []labelCount `json:"items"`
		}](t, owner.do(t, "GET", "/v1/labels?project="+p.Key+","+q.Key, nil), 200)
		counts := map[string]int{}
		for _, it := range got.Items {
			counts[it.Label] = it.Count
		}
		if counts[la] != 2 || counts[lb] != 2 || counts[lc] != 1 || counts[ld] != 1 {
			t.Errorf("counts = %v", counts)
		}
		all := decodeAs[struct {
			Items []labelCount `json:"items"`
		}](t, owner.do(t, "GET", "/v1/labels", nil), 200)
		counts = map[string]int{}
		for _, it := range all.Items {
			counts[it.Label] = it.Count
		}
		if counts[la] != 2 || counts[lc] != 1 {
			t.Errorf("global counts for this run = %v %v", counts[la], counts[lc])
		}
		for i := 1; i < len(all.Items); i++ {
			prev, cur := all.Items[i-1], all.Items[i]
			if prev.Count < cur.Count || (prev.Count == cur.Count && prev.Label > cur.Label) {
				t.Errorf("global labels out of order at %d: %+v then %+v", i, prev, cur)
			}
		}
	})
	t.Run("bad query parameters are 400", func(t *testing.T) {
		for _, q := range []string{"project=bad", "status=open", "limit=1", "project=A&project=B"} {
			wantError(t, owner.do(t, "GET", "/v1/labels?"+q, nil), 400, "bad_request")
		}
	})
}
