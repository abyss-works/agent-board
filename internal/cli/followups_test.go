package cli

import (
	"reflect"
	"testing"
)

var testHeadings = []string{"남은 것", "미확인", "후속"}

func TestFollowUps(t *testing.T) {
	doc := "---\nkind: report\ntitle: x\n---\n\n# 제목\n\n## 결론\n\n- 결론 항목\n\n" +
		"## 한계와 미확인\n\n- 다른 프로젝트는 미확인이다.\n  - 하위 설명은 항목이 아니다.\n- 실제 세션은 열어 보지 않았다.\n\n" +
		"```\n## 남은 것\n- 코드 블록 안\n```\n\n" +
		"## 후속\n\n1. CSP 헤더\n2. HSTS\n\n### 세부\n\n- 하위 절도 후속에 든다\n\n" +
		"## 남은 것\n\n없음\n\n## 재검토 조건\n\n- 항목 아님\n"
	got := followUps(doc, testHeadings)
	want := []followUp{
		{Section: "한계와 미확인", Items: []string{"다른 프로젝트는 미확인이다.", "실제 세션은 열어 보지 않았다."}},
		{Section: "후속", Items: []string{"CSP 헤더", "HSTS", "하위 절도 후속에 든다"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("followUps = %#v\nwant %#v", got, want)
	}

	if got := followUps("## 미확인\n\n두 줄로 쓴\n문단 하나.\n", testHeadings); !reflect.DeepEqual(got, []followUp{{Section: "미확인", Items: []string{"두 줄로 쓴 문단 하나."}}}) {
		t.Errorf("paragraph = %#v", got)
	}
	for _, empty := range []string{"", "# 제목\n\n본문", "## 미확인\n\n- 없음\n", "## 남은 것\n\n없음.\n", "## 남은 여지\n\n- 항목\n"} {
		if got := followUps(empty, testHeadings); len(got) != 0 {
			t.Errorf("followUps(%q) = %#v, want none", empty, got)
		}
	}
	// CRLF files read the same.
	if got := followUps("## 후속\r\n\r\n- 항목\r\n", testHeadings); !reflect.DeepEqual(got, []followUp{{Section: "후속", Items: []string{"항목"}}}) {
		t.Errorf("crlf = %#v", got)
	}
}

func TestFollowUpsOnlyReadTheHeadingsOfTheConfig(t *testing.T) {
	doc := "## 후속\n\n- 하나\n\n## TODO\n\n- 둘\n"
	if got := followUps(doc, []string{"TODO"}); !reflect.DeepEqual(got, []followUp{{Section: "TODO", Items: []string{"둘"}}}) {
		t.Errorf("TODO = %#v", got)
	}
	if got := followUps(doc, nil); len(got) != 0 {
		t.Errorf("no headings = %#v, want none", got)
	}
}
