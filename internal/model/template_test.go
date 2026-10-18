package model

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// skillTemplates reads the "이슈 템플릿" section of the agent skill: for every
// "### 이름 (`type`)" part, the title rule on its "제목:" line (the first
// sentence) and the body in its fenced block.
func skillTemplates(t *testing.T) []Template {
	t.Helper()
	path := filepath.Join("..", "..", "skills", "agent-board", "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the skill: %v", err)
	}
	var (
		out     []Template
		cur     *Template
		inFence bool
		inSect  bool
		body    strings.Builder
	)
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "```") {
			if inFence && cur != nil {
				cur.Body = body.String()
				body.Reset()
			}
			inFence = !inFence
			continue
		}
		if inFence {
			if cur != nil {
				body.WriteString(line + "\n")
			}
			continue
		}
		switch {
		case line == "## 이슈 템플릿":
			inSect = true
		case strings.HasPrefix(line, "## ") && inSect:
			inSect = false
		case inSect && strings.HasPrefix(line, "### "):
			_, rest, ok := strings.Cut(line, "(`")
			typ, _, ok2 := strings.Cut(rest, "`)")
			if !ok || !ok2 {
				t.Fatalf("section heading without a type: %q", line)
			}
			out = append(out, Template{Type: IssueType(typ)})
			cur = &out[len(out)-1]
		case inSect && cur != nil && strings.HasPrefix(line, "제목: "):
			rule := strings.TrimPrefix(line, "제목: ")
			cur.TitleRule = rule[:strings.Index(rule, ".")+1]
		}
	}
	for i := range out {
		for _, line := range strings.Split(out[i].Body, "\n") {
			if name, ok := strings.CutPrefix(line, "## "); ok {
				out[i].Headings = append(out[i].Headings, name)
			}
		}
	}
	return out
}

// The server serves and checks the same templates the skill tells agents to
// use. Edit the skill first, then the server.
func TestTemplatesMatchTheSkill(t *testing.T) {
	skill := skillTemplates(t)
	if len(skill) != len(IssueTypes) || len(Templates) != len(IssueTypes) {
		t.Fatalf("templates: skill %d, server %d, types %d", len(skill), len(Templates), len(IssueTypes))
	}
	for i, typ := range IssueTypes {
		want, got := skill[i], Templates[i]
		if want.Type != typ || got.Type != typ {
			t.Errorf("template %d: skill %q, server %q, want %q (types in IssueTypes order)", i, want.Type, got.Type, typ)
			continue
		}
		if got.TitleRule != want.TitleRule {
			t.Errorf("%s title_rule:\nserver %q\nskill  %q", typ, got.TitleRule, want.TitleRule)
		}
		if !reflect.DeepEqual(got.Headings, want.Headings) {
			t.Errorf("%s headings:\nserver %q\nskill  %q", typ, got.Headings, want.Headings)
		}
		if got.Body != want.Body {
			t.Errorf("%s body:\nserver %q\nskill  %q", typ, got.Body, want.Body)
		}
		if len(got.Headings) == 0 || !strings.HasSuffix(got.Body, "\n") || got.TitleRule == "" {
			t.Errorf("%s template is incomplete: %+v", typ, got)
		}
	}
}

func TestTemplateHeadings(t *testing.T) {
	want := map[IssueType][]string{
		TypeFeature:     {"배경", "할 일", "완료 조건"},
		TypeBug:         {"현상", "재현", "원인", "완료 조건"},
		TypeDesign:      {"배경", "정할 것", "후보", "제약", "완료 조건"},
		TypeDecision:    {"현황", "결정할 것", "선택지", "영향"},
		TypeIdea:        {"내용", "출처", "검토할 것"},
		TypeObservation: {"현상", "지금 고치지 않는 이유", "다시 볼 때 할 일"},
	}
	for typ, headings := range want {
		tpl, ok := TemplateFor(typ)
		if !ok || !reflect.DeepEqual(tpl.Headings, headings) {
			t.Errorf("%s headings = %v (found %v), want %v", typ, tpl.Headings, ok, headings)
		}
	}
	if _, ok := TemplateFor(IssueType("task")); ok {
		t.Error("an unknown type has no template")
	}
}

func TestTemplatesSerializeAsTheAPIDescribes(t *testing.T) {
	b, err := json.Marshal(Templates[0])
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"feature","title_rule":"만들거나 바꿀 것을 명사형으로 쓴다.","headings":["배경","할 일","완료 조건"],` +
		`"body":"## 배경\n왜 필요한지. 요청한 사람과 날짜, 관련 결정이나 이슈.\n\n## 할 일\n- 작업 단위 하나씩\n\n## 완료 조건\n- 확인할 수 있는 조건. 실행할 명령, 볼 화면, 받을 응답.\n"}`
	if string(b) != want {
		t.Errorf("feature template JSON:\n%s\nwant\n%s", b, want)
	}
}

func bodyField(err error) (message string, ok bool) {
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Fields) != 1 || ve.Fields[0].Field != "body" {
		return "", false
	}
	return ve.Fields[0].Message, true
}

func TestCheckTemplate(t *testing.T) {
	feature := Templates[0]
	for _, tpl := range Templates {
		if err := CheckTemplate(tpl.Type, tpl.Body); err != nil {
			t.Errorf("the template of %s must pass its own check: %v", tpl.Type, err)
		}
	}
	cases := []struct {
		name    string
		typ     IssueType
		body    string
		missing []string // headings the message must name; nil when the body passes
	}{
		{"headings only", TypeFeature, "## 배경\n## 할 일\n## 완료 조건", nil},
		{"filled in", TypeFeature, "## 배경\n오늘 요청\n\n## 할 일\n- 구현\n\n## 완료 조건\n- 테스트 통과\n", nil},
		{"empty body", TypeFeature, "", []string{"배경", "할 일", "완료 조건"}},
		{"one heading missing", TypeFeature, "## 배경\n## 완료 조건", []string{"할 일"}},
		{"last heading missing", TypeFeature, "## 배경\n## 할 일", []string{"완료 조건"}},
		{"out of order", TypeFeature, "## 완료 조건\n## 배경\n## 할 일", []string{"완료 조건"}},
		{"another heading between is fine", TypeFeature, "## 배경\n## 참고\n## 할 일\n### 세부\n## 완료 조건\n## 덧붙임", nil},
		{"a repeated heading is fine", TypeFeature, "## 배경\n## 배경\n## 할 일\n## 완료 조건", nil},
		{"space around the line is ignored", TypeFeature, "  ## 배경  \n\t## 할 일\t\n## 완료 조건 \n", nil},
		{"windows line ends", TypeFeature, "## 배경\r\n본문\r\n## 할 일\r\n## 완료 조건\r\n", nil},
		{"a third-level heading is not a heading", TypeFeature, "### 배경\n## 할 일\n## 완료 조건", []string{"배경"}},
		{"no space after the hashes", TypeFeature, "##배경\n## 할 일\n## 완료 조건", []string{"배경"}},
		{"extra text on the line", TypeFeature, "## 배경 (추가)\n## 할 일\n## 완료 조건", []string{"배경"}},
		{"heading inside a sentence", TypeFeature, "문장 안의 ## 배경\n## 할 일\n## 완료 조건", []string{"배경"}},
		{"nothing but unrelated text", TypeFeature, "그냥 메모입니다.", []string{"배경", "할 일", "완료 조건"}},
		{"another type's headings", TypeBug, feature.Body, []string{"현상", "재현", "원인"}},
		{"unknown type passes", IssueType("task"), "", nil},
	}
	for _, c := range cases {
		err := CheckTemplate(c.typ, c.body)
		if c.missing == nil {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
			}
			continue
		}
		msg, ok := bodyField(err)
		if !ok {
			t.Errorf("%s: err = %v, want one violation on body", c.name, err)
			continue
		}
		for _, h := range c.missing {
			if !strings.Contains(msg, "## "+h) {
				t.Errorf("%s: message %q does not name %q", c.name, msg, h)
			}
		}
		if !strings.Contains(msg, string(c.typ)) {
			t.Errorf("%s: message %q does not name the type", c.name, msg)
		}
	}

	// A heading that is present and in place must not be reported.
	msg, _ := bodyField(CheckTemplate(TypeFeature, "## 배경\n## 완료 조건"))
	tail := msg[strings.Index(msg, "빠진 제목: "):]
	if !strings.Contains(tail, "## 할 일") || strings.Contains(tail, "## 배경") || strings.Contains(tail, "## 완료 조건") || strings.Contains(msg, "순서가 맞지 않는") {
		t.Errorf("message names headings that are present: %q", msg)
	}
	// Headings that are all there but in the wrong order are called out as such.
	msg, _ = bodyField(CheckTemplate(TypeFeature, "## 완료 조건\n## 배경\n## 할 일"))
	if !strings.Contains(msg, "순서가 맞지 않는 제목: ## 완료 조건") || strings.Contains(msg, "빠진 제목") {
		t.Errorf("order message = %q", msg)
	}
	// Missing and out of order at once.
	msg, _ = bodyField(CheckTemplate(TypeBug, "## 원인\n## 현상\n## 완료 조건"))
	if !strings.Contains(msg, "빠진 제목: ## 재현") || !strings.Contains(msg, "순서가 맞지 않는 제목:") {
		t.Errorf("combined message = %q", msg)
	}
	// Checking must not change the shared templates.
	if got, _ := TemplateFor(TypeFeature); !reflect.DeepEqual(got.Headings, []string{"배경", "할 일", "완료 조건"}) {
		t.Errorf("CheckTemplate changed the template: %v", got.Headings)
	}
}
