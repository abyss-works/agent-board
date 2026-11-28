//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// requirePG skips a test when the server under test runs on SQLite, which has
// none of these routes.
func requirePG(t *testing.T) {
	t.Helper()
	r := owner.do(t, "POST", "/v1/context/query", map[string]any{})
	if r.status == 404 {
		t.Skip("server is not in PostgreSQL mode")
	}
}

type ingestResult struct {
	ID                   int64  `json:"id"`
	IngestStatus         string `json:"ingest_status"`
	SupersedesCandidates []struct {
		EdgeID int64 `json:"edge_id"`
	} `json:"supersedes_candidates"`
}

type contextItem struct {
	NodeType  string `json:"node_type"`
	KeyOrName string `json:"key_or_name"`
	Title     string `json:"title"`
	Neighbors []struct {
		Direction string `json:"direction"`
		Kind      string `json:"kind"`
	} `json:"neighbors"`
	SupersededBy *struct {
		KeyOrName string `json:"key_or_name"`
	} `json:"superseded_by"`
}

type contextAnswer struct {
	Items []contextItem `json:"items"`
}

type edgeRow struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

type edgePage struct {
	Items []edgeRow `json:"items"`
}

func ctxQuery(t *testing.T, project, query string, extra map[string]any) contextAnswer {
	t.Helper()
	body := map[string]any{"query": query, "project": project, "include_global": false}
	for k, v := range extra {
		body[k] = v
	}
	return decodeAs[contextAnswer](t, owner.do(t, "POST", "/v1/context/query", body), 200)
}

func hasItem(a contextAnswer, name string) bool {
	for _, it := range a.Items {
		if it.KeyOrName == name {
			return true
		}
	}
	return false
}

func TestPGSpecAndRecordWait(t *testing.T) {
	requirePG(t)
	p := createProject(t, "PS")
	specPath := "/v1/projects/" + p.Key + "/specs/api-contract?wait=true&wait_timeout_ms=15000"

	first := decodeAs[ingestResult](t, owner.do(t, "PUT", specPath, map[string]any{
		"title": "API 계약", "kind": "contract", "content": "# API 계약\n응답은 JSON 이다.", "change_summary": "초안"}), 201)
	if first.IngestStatus != "done" {
		t.Errorf("first upsert ingest_status = %q, want done", first.IngestStatus)
	}
	second := decodeAs[ingestResult](t, owner.do(t, "PUT", specPath, map[string]any{
		"title": "API 계약", "kind": "contract", "content": "# API 계약\n응답은 JSON 이고 오류는 code 를 가진다.", "change_summary": "오류 형식"}), 200)
	if second.ID != first.ID {
		t.Errorf("an upsert of the same slug must keep the document: %d vs %d", second.ID, first.ID)
	}
	revs := decodeAs[struct {
		Items []struct {
			RevisionNo int `json:"revision_no"`
		} `json:"items"`
	}](t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/specs/api-contract/revisions", nil), 200)
	if len(revs.Items) != 2 {
		t.Errorf("revisions = %+v, want 2", revs.Items)
	}
	wantError(t, owner.do(t, "PUT", "/v1/projects/"+p.Key+"/specs/Bad%20Slug", map[string]any{"content": "x"}), 422, "validation_failed")

	name := "2026-11-01-1000-decision-" + strings.ToLower(runID) + ".md"
	rec := decodeAs[ingestResult](t, owner.do(t, "POST", "/v1/projects/"+p.Key+"/records?wait=true&wait_timeout_ms=15000", map[string]any{
		"name": name, "kind": "decision", "title": "캐시 정책", "content": "# 캐시 정책\n만료 시간은 5분이다."}), 201)
	if rec.IngestStatus != "done" {
		t.Errorf("record ingest_status = %q, want done", rec.IngestStatus)
	}
	wantError(t, owner.do(t, "POST", "/v1/projects/"+p.Key+"/records", map[string]any{
		"name": name, "kind": "decision", "title": "중복", "content": "x"}), 409, "conflict")
	wantError(t, owner.do(t, "POST", "/v1/projects/"+p.Key+"/records?wait=maybe", map[string]any{
		"name": "x.md", "kind": "decision", "content": "x"}), 400, "bad_request")
}

func TestPGContextQuery(t *testing.T) {
	requirePG(t)
	p := createProject(t, "PC")
	word := "zq" + strings.ToLower(runID)
	for i, title := range []string{"락 경합 해소", "큐 적체 해소"} {
		decodeAs[ingestResult](t, owner.do(t, "POST", fmt.Sprintf("/v1/projects/%s/records?wait=true&wait_timeout_ms=15000", p.Key), map[string]any{
			"name": fmt.Sprintf("2026-11-0%d-1000-decision-%s-%d.md", i+1, strings.ToLower(runID), i), "kind": "decision", "title": title,
			"content": fmt.Sprintf("# %s\n%s 에 대한 결정 기록 %d", title, word, i)}), 201)
	}
	ans := ctxQuery(t, p.Key, word, map[string]any{"max_results": 5})
	if len(ans.Items) == 0 {
		t.Fatalf("context query found nothing for %q", word)
	}
	for _, it := range ans.Items {
		if it.Neighbors == nil {
			t.Errorf("item %s lacks a neighbors array", it.KeyOrName)
		}
	}
	wantError(t, owner.do(t, "POST", "/v1/context/query", map[string]any{"query": "   "}), 422, "validation_failed")
	wantError(t, owner.do(t, "POST", "/v1/context/query", map[string]any{"query": "x", "max_results": 11}), 422, "validation_failed")
	wantError(t, owner.do(t, "POST", "/v1/context/query", map[string]any{"query": "x", "confidence_cutoff": 1.5}), 422, "validation_failed")
	wantError(t, owner.do(t, "POST", "/v1/context/query", map[string]any{"query": "x", "expand_hops": 3}), 422, "validation_failed")
	wantError(t, owner.do(t, "POST", "/v1/context/query", map[string]any{"query": "x", "source_types": []string{"nope"}}), 422, "validation_failed")
}

func TestPGEdgeConfirmRejectAndSupersedes(t *testing.T) {
	requirePG(t)
	p := createProject(t, "PE")
	tag := strings.ToLower(runID)
	v1 := fmt.Sprintf("2026-10-01-1000-decision-cache-%s.md", tag)
	v2 := fmt.Sprintf("2026-10-05-1000-decision-cache-v2-%s.md", tag)
	v3 := fmt.Sprintf("2026-10-06-1000-decision-cache-v3-%s.md", tag)
	post := func(name, title, content string) ingestResult {
		return decodeAs[ingestResult](t, owner.do(t, "POST", "/v1/projects/"+p.Key+"/records?wait=true&wait_timeout_ms=15000", map[string]any{
			"name": name, "kind": "decision", "title": title, "content": content}), 201)
	}
	post(v1, "캐시 만료 정책 v1", "# 캐시 만료 정책\n기존 캐시 만료 시간은 5분으로 고정한다.")
	r2 := post(v2, "캐시 만료 정책 v2", "# 캐시 만료 정책 개정\n기존 결정 "+v1+" 를 대체한다. 새 캐시 만료 시간은 30분이다.")
	if len(r2.SupersedesCandidates) == 0 {
		t.Fatalf("a record that names its predecessor must propose a supersedes edge: %+v", r2)
	}
	edge := r2.SupersedesCandidates[0].EdgeID

	cands := decodeAs[edgePage](t, owner.do(t, "GET", "/v1/edges/candidates?project="+p.Key+"&kind=supersedes", nil), 200)
	found := false
	for _, c := range cands.Items {
		found = found || c.ID == edge
	}
	if !found {
		t.Errorf("edge %d missing from the candidate list %+v", edge, cands.Items)
	}
	// An unconfirmed edge does not hide the old document.
	if ans := ctxQuery(t, p.Key, "캐시 만료 정책", map[string]any{"include_superseded": true}); hasItemWithSuperseder(ans) {
		t.Errorf("a candidate edge must not mark a document superseded: %+v", ans.Items)
	}
	confirmed := decodeAs[edgeRow](t, owner.do(t, "POST", fmt.Sprintf("/v1/edges/%d/confirm", edge), nil), 200)
	if confirmed.Status != "confirmed" {
		t.Errorf("confirm status = %q", confirmed.Status)
	}
	wantError(t, owner.do(t, "POST", fmt.Sprintf("/v1/edges/%d/confirm", edge), nil), 409, "edge_already_decided")
	wantError(t, owner.do(t, "POST", fmt.Sprintf("/v1/edges/%d/reject", edge), nil), 409, "edge_already_decided")
	old := ctxQuery(t, p.Key, "캐시 만료 정책 v1 5분", map[string]any{"include_superseded": true, "max_results": 5})
	if !hasItem(old, v1) {
		t.Errorf("include_superseded must find the old record: %+v", old.Items)
	}
	hidden := ctxQuery(t, p.Key, "캐시 만료 정책 v1 5분", map[string]any{"max_results": 5})
	if hasItem(hidden, v1) {
		t.Errorf("a superseded record must be left out by default: %+v", hidden.Items)
	}

	r3 := post(v3, "캐시 만료 정책 v3", "# 캐시 만료 정책 검토\n기존 결정 "+v2+" 를 대체한다.")
	if len(r3.SupersedesCandidates) == 0 {
		t.Fatalf("expected a candidate on v3: %+v", r3)
	}
	rej := r3.SupersedesCandidates[0].EdgeID
	rejected := decodeAs[edgeRow](t, owner.do(t, "POST", fmt.Sprintf("/v1/edges/%d/reject", rej), nil), 200)
	if rejected.Status != "rejected" {
		t.Errorf("reject status = %q", rejected.Status)
	}
	after := decodeAs[edgePage](t, owner.do(t, "GET", "/v1/edges/candidates?project="+p.Key, nil), 200)
	for _, c := range after.Items {
		if c.ID == rej || c.ID == edge {
			t.Errorf("decided edge %d is still a candidate", c.ID)
		}
	}
	wantError(t, owner.do(t, "POST", "/v1/edges/0/confirm", nil), 400, "bad_request")
	wantError(t, owner.do(t, "POST", "/v1/edges/999999999/confirm", nil), 404, "not_found")
}

func hasItemWithSuperseder(a contextAnswer) bool {
	for _, it := range a.Items {
		if it.SupersededBy != nil {
			return true
		}
	}
	return false
}

func TestPGMilestoneScopeLockAndReadyQueue(t *testing.T) {
	requirePG(t)
	p := createProject(t, "PM")
	ms := decodeAs[milestone](t, owner.do(t, "POST", "/v1/projects/"+p.Key+"/milestones", map[string]any{"name": "v1.0"}), 201)
	wantError(t, owner.do(t, "POST", "/v1/projects/"+p.Key+"/milestones", map[string]any{"name": "v1.0"}), 409, "conflict")
	active := decodeAs[milestone](t, owner.do(t, "PATCH", fmt.Sprintf("/v1/milestones/%d", ms.ID), map[string]any{"status": "active"}), 200)
	if active.Status != "active" {
		t.Fatalf("milestone status = %q, want active", active.Status)
	}
	path := "/v1/projects/" + p.Key + "/issues"
	feature := map[string]any{"title": "새 기능", "type": "feature", "priority": "P2", "milestone_id": ms.ID}
	locked := wantError(t, owner.do(t, "POST", path, feature), 422, "milestone_scope_locked")
	if !locked.hasField("milestone_id") {
		t.Errorf("the lock must name milestone_id: %+v", locked)
	}
	decodeAs[issue](t, owner.do(t, "POST", path, map[string]any{"title": "급한 버그", "type": "bug", "priority": "P2", "milestone_id": ms.ID}), 201)
	decodeAs[issue](t, owner.do(t, "POST", path, map[string]any{"title": "P0 기능", "type": "feature", "priority": "P0", "milestone_id": ms.ID}), 201)
	forced := map[string]any{"title": "예외 기능", "type": "feature", "priority": "P2", "milestone_id": ms.ID, "force": true, "reason": "릴리즈 전 필요"}
	decodeAs[issue](t, owner.do(t, "POST", path, forced), 201)

	// ready queue: an issue behind an open dependency is not ready until the dependency is done.
	type issueRef struct {
		ID  int64  `json:"id"`
		Key string `json:"key"`
	}
	base := decodeAs[issueRef](t, owner.do(t, "POST", path, map[string]any{"title": "선행", "type": "feature", "priority": "P1"}), 201)
	next := decodeAs[issueRef](t, owner.do(t, "POST", path, map[string]any{"title": "후행", "type": "feature", "priority": "P1"}), 201)
	decodeAs[map[string]any](t, owner.do(t, "POST", "/v1/edges", map[string]any{
		"source_type": "issue", "source_id": next.ID, "target_type": "issue", "target_id": base.ID, "kind": "depends_on"}), 201)
	type readyPage struct {
		Items []struct {
			Key string `json:"key"`
		} `json:"items"`
		Total int `json:"total"`
	}
	ready := decodeAs[readyPage](t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/ready?limit=100", nil), 200)
	hasKey := func(page readyPage, key string) bool {
		for _, it := range page.Items {
			if it.Key == key {
				return true
			}
		}
		return false
	}
	if !hasKey(ready, base.Key) || hasKey(ready, next.Key) {
		t.Errorf("ready = %+v; want %s ready and %s blocked", ready.Items, base.Key, next.Key)
	}
	patchIssue(t, owner, base.Key, statusPatch("done"))
	ready = decodeAs[readyPage](t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/ready?limit=100", nil), 200)
	if !hasKey(ready, next.Key) {
		t.Errorf("after %s is done %s must be ready: %+v", base.Key, next.Key, ready.Items)
	}
	wantError(t, owner.do(t, "GET", "/v1/projects/"+p.Key+"/ready?limit=1000", nil), 400, "bad_request")
}
