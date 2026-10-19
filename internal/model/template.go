package model

import "strings"

// Template is the body template of one issue type. Every body an agent writes
// must contain its headings in this order.
type Template struct {
	Type IssueType `json:"type"`
	// TitleRule says how to write the title of this type.
	TitleRule string `json:"title_rule"`
	// Headings are the "## " headings of Body, in order, without the marker.
	Headings []string `json:"headings"`
	// Body is the template text an agent fills in, ending with a newline.
	Body string `json:"body"`
}

// Templates holds one template per issue type in the order of IssueTypes.
// The text is the "이슈 템플릿" section of skills/tool-agb/SKILL.md, which is
// the source; template_test.go compares the two so they cannot drift apart.
var Templates = []Template{
	newTemplate(TypeFeature, "만들거나 바꿀 것을 명사형으로 쓴다.",
		"## 배경\n"+
			"왜 필요한지. 요청한 사람과 날짜, 관련 결정이나 이슈.\n"+
			"\n"+
			"## 할 일\n"+
			"- 작업 단위 하나씩\n"+
			"\n"+
			"## 완료 조건\n"+
			"- 확인할 수 있는 조건. 실행할 명령, 볼 화면, 받을 응답.\n",
	),
	newTemplate(TypeBug, "잘못된 동작을 쓴다.",
		"## 현상\n"+
			"기대한 동작과 실제 동작.\n"+
			"\n"+
			"## 재현\n"+
			"1. 단계\n"+
			"- 환경: 버전, 인스턴스, 브라우저나 셸\n"+
			"\n"+
			"## 원인\n"+
			"확인한 원인과 근거. 모르면 `미확인`.\n"+
			"\n"+
			"## 완료 조건\n"+
			"- 재현 절차에서 기대한 동작이 나온다.\n"+
			"- 회귀 테스트\n",
	),
	newTemplate(TypeDesign, "정할 대상을 쓴다.",
		"## 배경\n"+
			"왜 지금 정해야 하는지.\n"+
			"\n"+
			"## 정할 것\n"+
			"- 질문 하나씩\n"+
			"\n"+
			"## 후보\n"+
			"- 후보: 장점과 단점\n"+
			"\n"+
			"## 제약\n"+
			"지켜야 할 결정, 규칙, 환경.\n"+
			"\n"+
			"## 완료 조건\n"+
			"- 정한 내용이 명세나 원칙 문서에 반영되고 참조(`doc:`)로 연결된다.\n",
	),
	newTemplate(TypeIdea, "아이디어를 명사형으로 쓴다.",
		"## 내용\n"+
			"아이디어를 한두 문단으로.\n"+
			"\n"+
			"## 출처\n"+
			"누가, 언제, 어떤 논의에서 냈는지.\n"+
			"\n"+
			"## 검토할 것\n"+
			"- 채택 전에 확인할 질문\n",
	),
	newTemplate(TypeObservation, "관측한 현상을 쓴다.",
		"## 현상\n"+
			"확인한 사실과 근거(명령, 로그, 수치).\n"+
			"\n"+
			"## 지금 고치지 않는 이유\n"+
			"이유. 원문에 없으면 `미확인`.\n"+
			"\n"+
			"## 다시 볼 때 할 일\n"+
			"- 재검토 조건이 되었을 때 확인하거나 할 일\n",
	),
}

// newTemplate builds a template and reads its headings from the body.
func newTemplate(typ IssueType, titleRule, body string) Template {
	var headings []string
	for _, line := range strings.Split(body, "\n") {
		if name, ok := strings.CutPrefix(line, "## "); ok {
			headings = append(headings, name)
		}
	}
	return Template{Type: typ, TitleRule: titleRule, Headings: headings, Body: body}
}

// TemplateFor returns the template of an issue type.
func TemplateFor(typ IssueType) (Template, bool) {
	for _, t := range Templates {
		if t.Type == typ {
			return t, true
		}
	}
	return Template{}, false
}

// headingLines reports which template headings appear as lines of body. A
// heading counts when a line, ignoring the space around it, is "## " and the
// heading.
func headingLines(t Template, body string) map[string]bool {
	found := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, h := range t.Headings {
			if trimmed == "## "+h {
				found[h] = true
			}
		}
	}
	return found
}

// inOrder returns the headings that are not reached when body is read from
// top to bottom looking for the headings one after another. Other headings and
// text may stand between them.
func inOrder(headings []string, body string) []string {
	next := 0
	for _, line := range strings.Split(body, "\n") {
		if next < len(headings) && strings.TrimSpace(line) == "## "+headings[next] {
			next++
		}
	}
	return append([]string(nil), headings[next:]...)
}

// missingHeadings splits what is wrong with body into the headings it lacks
// and the headings it has but not in template order.
func missingHeadings(t Template, body string) (absent, misordered []string) {
	found := headingLines(t, body)
	var present []string
	for _, h := range t.Headings {
		if found[h] {
			present = append(present, h)
		} else {
			absent = append(absent, h)
		}
	}
	return absent, inOrder(present, body)
}

func marked(headings []string) string {
	out := make([]string, len(headings))
	for i, h := range headings {
		out[i] = "## " + h
	}
	return strings.Join(out, ", ")
}

// CheckTemplate verifies that body has the headings of the template of typ in
// order. The violation is reported on the body field and names the headings
// that are missing and those out of order. A type without a template passes;
// the type check reports it.
func CheckTemplate(typ IssueType, body string) error {
	t, ok := TemplateFor(typ)
	if !ok {
		return nil
	}
	absent, misordered := missingHeadings(t, body)
	if len(absent) == 0 && len(misordered) == 0 {
		return nil
	}
	msg := string(typ) + " 템플릿의 제목이 순서대로 모두 있어야 합니다."
	if len(absent) > 0 {
		msg += " 빠진 제목: " + marked(absent) + "."
	}
	if len(misordered) > 0 {
		msg += " 순서가 맞지 않는 제목: " + marked(misordered) + "."
	}
	msg += " 템플릿은 GET /v1/templates에서 받을 수 있습니다."
	var e ValidationError
	e.add("body", "%s", msg)
	return e.orNil()
}
