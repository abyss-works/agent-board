package model

import (
	"strings"
	"testing"
)

const checkName = "2026-10-20-1544-decision-pick-queue.md"

const checkTemplate = "---\nkind: decision\ndate: <yyyy-mm-dd HH:MM>\ntitle: <문서 제목>\ntags: [<주제어>]\nstatus: <채택 | 기각>\n---\n\n# <제목>\n\n<!-- 파일명: x. 이 주석은 지운다. -->\n\n## 배경\n\n<왜>\n\n## 결정\n\n- <정한 것>\n\n## 미확인\n\n- <확인하지 못한 것>\n"

const checkFilled = "---\nkind: decision\ndate: 2026-10-20 15:44\ntitle: 큐 선택\ntags: [queue]\nstatus: 채택\n---\n\n# 큐 선택\n\n## 배경\n\n이유.\n\n## 논의\n\n추가 절.\n\n## 결정\n\n- 큐를 쓴다.\n\n## 미확인\n\n- 없음\n"

func containsAll(t *testing.T, label string, problems []string, parts ...string) {
	t.Helper()
	joined := strings.Join(problems, "\n")
	for _, p := range parts {
		if !strings.Contains(joined, p) {
			t.Errorf("%s: problems lack %q:\n%s", label, p, joined)
		}
	}
}

func TestCheckRecord(t *testing.T) {
	tmpl := checkTemplate
	replace := func(old, new string) string { return strings.Replace(checkFilled, old, new, 1) }
	cases := []struct {
		label, name, content string
		tmpl                 *string
		want                 []string // each must appear in the problems
		count                int      // number of problems
	}{
		{"filled", checkName, checkFilled, &tmpl, nil, 0},
		{"unfilled template", checkName, checkTemplate, &tmpl, []string{"date", "title", "tags", "status", "<!--", "<왜>", "<정한 것>"}, -1},
		{"bad name", "x.md", checkFilled, &tmpl, []string{"파일명"}, 1},
		{"unknown kind in name", "2026-10-20-1544-plan-x.md", checkFilled, &tmpl, []string{"파일명"}, 1},
		{"no front matter", checkName, "# 제목\n## 배경\n## 결정\n## 미확인\n", &tmpl, []string{"프런트매터"}, 1},
		{"unclosed front matter", checkName, "---\nkind: decision\n## 배경\n", &tmpl, []string{"프런트매터"}, -1},
		{"kind differs from the name", checkName, replace("kind: decision", "kind: report"), &tmpl, []string{"kind", "report"}, 1},
		{"missing key", checkName, replace("status: 채택\n", ""), &tmpl, []string{"status"}, 1},
		{"placeholder date", checkName, replace("2026-10-20 15:44", "<yyyy-mm-dd HH:MM>"), &tmpl, []string{"date"}, 1},
		{"placeholder tags list", checkName, replace("[queue]", "[<주제어>, <주제어>]"), &tmpl, []string{"tags"}, 1},
		{"missing heading", checkName, replace("## 결정\n\n- 큐를 쓴다.\n\n", ""), &tmpl, []string{"## 결정"}, 1},
		{"headings out of order", checkName, replace("## 배경\n\n이유.\n\n", "") + "\n## 배경\n\n이유.\n", &tmpl, []string{"순서", "## 배경"}, 1},
		{"extra heading is fine", checkName, checkFilled + "\n## 덧붙임\n\n내용.\n", &tmpl, nil, 0},
		{"html comment", checkName, replace("이유.", "이유.\n<!-- 메모 -->"), &tmpl, []string{"<!--", "줄"}, 1},
		{"multi-line html comment", checkName, replace("이유.", "<!--\n긴 주석\n-->"), &tmpl, []string{"<!--"}, 1},
		{"placeholder in body", checkName, replace("이유.", "<왜 필요했는지>"), &tmpl, []string{"<왜 필요했는지>"}, 1},
		{"time placeholder in body", checkName, replace("이유.", "<yyyy-mm-dd HH:MM>"), &tmpl, []string{"<yyyy-mm-dd HH:MM>"}, 1},
		{"placeholder in a table cell", checkName, replace("이유.", "| 대안 | 이유 |\n|---|---|\n| <대안> | b |"), &tmpl, []string{"<대안>"}, 1},
		{"plain tags are fine", checkName, replace("이유.", "줄바꿈<br>과 <b>굵게</b>, <https://example.com>."), &tmpl, nil, 0},
		{"comparison is fine", checkName, replace("이유.", "a < b and c > d"), &tmpl, nil, 0},
		{"remnants in a fenced block are fine", checkName, replace("이유.", "```md\n<!-- 파일명: x -->\n<왜 필요했는지>\n## 없는 절\n```"), &tmpl, nil, 0},
		{"remnants in a tilde fence are fine", checkName, replace("이유.", "~~~~\n<왜 필요했는지>\n```\n<!-- a -->\n~~~~"), &tmpl, nil, 0},
		{"remnants after a fence still count", checkName, replace("이유.", "```\n코드\n```\n<왜 필요했는지>"), &tmpl, []string{"<왜 필요했는지>"}, 1},
		{"placeholder in inline code is fine", checkName, replace("이유.", "`<왜 필요했는지>`를 쓴다."), &tmpl, nil, 0},
		{"comment in inline code is not exempt", checkName, replace("이유.", "`<!-- a -->`"), &tmpl, []string{"<!--"}, 1},
		{"heading in a fenced block does not count", checkName, strings.Replace(checkFilled, "## 결정\n\n- 큐를 쓴다.", "```\n## 결정\n```\n- 큐를 쓴다.", 1), &tmpl, []string{"## 결정"}, 1},
		{"no template skips the key and heading checks", checkName, "---\nkind: decision\ntitle: 제목\n---\n본문\n", nil, nil, 0},
		{"no template still checks the rest", "x.md", "본문 <왜 필요했는지> <!-- a -->", nil, []string{"파일명", "프런트매터", "<!--", "<왜 필요했는지>"}, 4},
	}
	for _, c := range cases {
		got := CheckRecord(c.name, c.content, c.tmpl)
		if c.count >= 0 && len(got) != c.count {
			t.Errorf("%s: %d problems, want %d:\n%s", c.label, len(got), c.count, strings.Join(got, "\n"))
		}
		containsAll(t, c.label, got, c.want...)
	}
}

func TestRecordHeadings(t *testing.T) {
	got := strings.Join(recordHeadings(checkTemplate), "|")
	if got != "## 배경|## 결정|## 미확인" {
		t.Errorf("headings = %s", got)
	}
}

func TestCheckRecordTitleAndRelationNames(t *testing.T) {
	tmpl := checkTemplate
	replace := func(old, new string) string { return strings.Replace(checkFilled, old, new, 1) }
	for _, c := range []struct {
		label, content string
		tmpl           *string
		want           []string
		count          int
	}{
		{"names are fine", replace("status: 채택\n", "status: 채택\nsupersedes: 2026-10-19-1000-decision-old.md\nrelated: [2026-10-18-1000-report-a.md, AGB/2026-10-17-1000-report-b.md]\n"), &tmpl, nil, 0},
		{"a sentence in supersedes", replace("status: 채택\n", "status: 채택\nsupersedes: 2026-10-19-1000-decision-old.md의 결론 중 일부\n"), &tmpl, []string{"supersedes", "related"}, 1},
		{"none is no relation", replace("status: 채택\n", "status: 채택\nsupersedes: none\nrelated: [none]\n"), &tmpl, nil, 0},
		{"an empty relation is no relation", replace("status: 채택\n", "status: 채택\nsupersedes:\nrelated: []\n"), &tmpl, nil, 0},
		{"None is not a name", replace("status: 채택\n", "status: 채택\nsupersedes: None\n"), &tmpl, []string{"supersedes", `"None"`}, 1},
		{"a sentence in a related list", replace("status: 채택\n", "status: 채택\nrelated: [a.md, 설명 문장]\n"), &tmpl, []string{"related", "설명 문장"}, 1},
		{"a placeholder is reported once", replace("status: 채택\n", "status: 채택\nsupersedes: <이전 기록 | none>\n"), &tmpl, []string{"supersedes"}, 1},
		{"an empty title", replace("title: 큐 선택", "title:"), &tmpl, []string{"title"}, 1},
		{"no title without a template", "---\nkind: decision\n---\n본문\n", nil, []string{"title"}, 1},
		{"a missing title key is reported once", replace("title: 큐 선택\n", ""), &tmpl, []string{"title"}, 1},
	} {
		got := CheckRecord(checkName, c.content, c.tmpl)
		if len(got) != c.count {
			t.Errorf("%s: %d problems, want %d:\n%s", c.label, len(got), c.count, strings.Join(got, "\n"))
		}
		containsAll(t, c.label, got, c.want...)
	}
}

func TestStripHTMLComments(t *testing.T) {
	for _, c := range []struct{ label, in, want string }{
		{"none", "# 제목\n\n본문\n", "# 제목\n\n본문\n"},
		{"a comment on its own lines goes with its blank line", "# 제목\n\n<!-- 안내\n여러 줄 -->\n\n## 절\n", "# 제목\n\n## 절\n"},
		{"a comment in a line leaves the rest", "앞 <!-- 메모 --> 뒤\n", "앞  뒤\n"},
		{"a comment in a fence stays", "```\n<!-- 코드 -->\n```\n<!-- 지움 -->\n끝\n", "```\n<!-- 코드 -->\n```\n끝\n"},
		{"crlf", "# 제목\r\n\r\n<!-- 안내 -->\r\n\r\n## 절\r\n", "# 제목\r\n\r\n## 절\r\n"},
		{"two comments", "<!-- a -->\n<!-- b -->\n본문\n", "본문\n"},
		{"an unclosed comment stays", "<!-- 안 닫힘\n본문\n", "<!-- 안 닫힘\n본문\n"},
	} {
		if got := StripHTMLComments(c.in); got != c.want {
			t.Errorf("%s: StripHTMLComments(%q) = %q, want %q", c.label, c.in, got, c.want)
		}
	}
}
