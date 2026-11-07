package cli

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFollowUps(t *testing.T) {
	doc := "---\nkind: report\ntitle: x\n---\n\n# 제목\n\n## 결론\n\n- 결론 항목\n\n" +
		"## 한계와 미확인\n\n- 다른 프로젝트는 미확인이다.\n  - 하위 설명은 항목이 아니다.\n- 실제 세션은 열어 보지 않았다.\n\n" +
		"```\n## 남은 것\n- 코드 블록 안\n```\n\n" +
		"## 후속\n\n1. CSP 헤더\n2. HSTS\n\n### 세부\n\n- 하위 절도 후속에 든다\n\n" +
		"## 남은 것\n\n없음\n\n## 재검토 조건\n\n- 항목 아님\n"
	got := followUps(doc)
	want := []followUp{
		{Section: "한계와 미확인", Items: []string{"다른 프로젝트는 미확인이다.", "실제 세션은 열어 보지 않았다."}},
		{Section: "후속", Items: []string{"CSP 헤더", "HSTS", "하위 절도 후속에 든다"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("followUps = %#v\nwant %#v", got, want)
	}

	if got := followUps("## 미확인\n\n두 줄로 쓴\n문단 하나.\n"); !reflect.DeepEqual(got, []followUp{{Section: "미확인", Items: []string{"두 줄로 쓴 문단 하나."}}}) {
		t.Errorf("paragraph = %#v", got)
	}
	for _, empty := range []string{"", "# 제목\n\n본문", "## 미확인\n\n- 없음\n", "## 남은 것\n\n없음.\n", "## 남은 여지\n\n- 항목\n"} {
		if got := followUps(empty); len(got) != 0 {
			t.Errorf("followUps(%q) = %#v, want none", empty, got)
		}
	}
	// CRLF files read the same.
	if got := followUps("## 후속\r\n\r\n- 항목\r\n"); !reflect.DeepEqual(got, []followUp{{Section: "후속", Items: []string{"항목"}}}) {
		t.Errorf("crlf = %#v", got)
	}
}

func TestDocSyncAndPutReportFollowUps(t *testing.T) {
	const (
		nNew  = "2026-10-20-1544-report-new-one.md"
		nSame = "2026-10-19-1700-report-same-one.md"
	)
	open := docText("report", "본문") + "\n## 후속\n\n- CSP 헤더\n"
	same := open + "\n"
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"history/" + nNew: open, "history/" + nSame: same})
	commitAll(t, root)
	srv := docSyncServer(t, []string{serverDoc("CLI", nSame, "history", docSHA(same))}, map[string]canned{
		"PUT /v1/projects/CLI/documents/" + nNew: {status: 201, body: serverDoc("CLI", nNew, "history", docSHA(open))},
	})

	// Only the document the sync uploads is read for follow-ups.
	h := newHarness(t, srv, false)
	if code := h.run("doc", "sync", "-p", "CLI", "--root", root); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var got struct {
		FollowUps []docFollowUps `json:"follow_ups"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []docFollowUps{{Name: nNew, Sections: []followUp{{Section: "후속", Items: []string{"CSP 헤더"}}}}}
	if !reflect.DeepEqual(got.FollowUps, want) {
		t.Errorf("follow_ups = %#v", got.FollowUps)
	}
	ht := newHarness(t, docSyncServer(t, nil, map[string]canned{
		"PUT /v1/projects/CLI/documents/" + nNew:  {status: 201, body: serverDoc("CLI", nNew, "history", docSHA(open))},
		"PUT /v1/projects/CLI/documents/" + nSame: {status: 201, body: serverDoc("CLI", nSame, "history", docSHA(same))},
	}), true)
	if code := ht.run("doc", "sync", "-p", "CLI", "--root", root); code != 0 || !strings.Contains(ht.stdout.String(), "== follow-ups (2) ==\n") ||
		!strings.Contains(ht.stdout.String(), "\n"+nNew+"\n  [후속] CSP 헤더\n") {
		t.Errorf("table: code=%d out=%s", code, ht.stdout)
	}

	// put reports follow-ups on stderr when the document is new, and not when it was there.
	srv = newFakeServer(t)
	srv.on("PUT", "/v1/projects/CLI/documents/"+nNew, canned{status: 201, body: serverDoc("CLI", nNew, "history", docSHA(open))})
	srv.on("PUT", "/v1/projects/CLI/documents/"+nSame, canned{status: 200, body: serverDoc("CLI", nSame, "history", docSHA(same))})
	hp := newHarness(t, srv, false)
	if code := hp.run("doc", "put", filepath.Join(root, "history", nNew), "-p", "CLI"); code != 0 || !strings.Contains(hp.stderr.String(), "  [후속] CSP 헤더") {
		t.Errorf("put new: code=%d stderr=%s", code, hp.stderr)
	}
	hp2 := newHarness(t, srv, false)
	if code := hp2.run("doc", "put", filepath.Join(root, "history", nSame), "-p", "CLI"); code != 0 || hp2.stderr.Len() != 0 {
		t.Errorf("put existing: code=%d stderr=%s", code, hp2.stderr)
	}
}
