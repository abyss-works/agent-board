package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

var templateCmd = &command{name: "template", summary: "이슈 종류별 본문 템플릿", subs: []*command{
	templateListCmd, templateShowCmd,
}}

// templateItems decodes the template list, keeping each item as the server
// sent it.
func templateItems(resp *client.Response) ([]json.RawMessage, error) {
	l, err := decode[struct {
		Items []json.RawMessage `json:"items"`
	}](resp)
	return l.Items, err
}

func (a *App) renderTemplateList(w io.Writer, resp *client.Response) error {
	l, err := decode[struct {
		Items []model.Template `json:"items"`
	}](resp)
	if err != nil {
		return err
	}
	// A block per type: the rule and the headings are both long, so a table would cut one of them.
	for i, tpl := range l.Items {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, tpl.Type)
		fields(w, [][2]string{
			{"  title_rule", tpl.TitleRule},
			{"  headings", strings.Join(tpl.Headings, ", ")},
		})
	}
	return nil
}

var templateListCmd = &command{
	name: "list", summary: "종류별 템플릿 제목과 제목 규칙",
	usage: `사용법: agb template list

이슈 종류마다 본문의 "## 제목" 목록과 이슈 제목을 쓰는 규칙을 보여 줍니다.
에이전트가 만드는 이슈는 서버가 이 제목을 순서대로 검사합니다. 본문 전체는 agb template show <type> 으로 받습니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("template list", g), args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.Templates(ctx)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderTemplateList)
	},
}

var templateShowCmd = &command{
	name: "show", summary: "한 종류의 본문 템플릿을 그대로 출력",
	usage: `사용법: agb template show <type> [--json]

종류(feature, bug, design, decision, idea, observation)의 본문 템플릿을 그대로 출력합니다.
파이프로도 같은 글이 나오므로 채워서 바로 쓸 수 있습니다.

  agb template show feature > body.md
  agb issue create -p <KEY> -t "<제목>" --body-file body.md

--json 을 주면 템플릿 객체(type, title_rule, headings, body)를 서버 응답 그대로 냅니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("template show", g), args)
		if err != nil {
			return err
		}
		typ, err := one(pos, "<type>")
		if err != nil {
			return err
		}
		types := make([]string, len(model.IssueTypes))
		for i, t := range model.IssueTypes {
			types[i] = string(t)
		}
		if _, ok := model.ParseIssueType(typ); !ok {
			return usageErrorf("알 수 없는 종류 %q 입니다. %s 중 하나를 주세요.", typ, strings.Join(types, ", "))
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.Templates(ctx)
		if err != nil {
			return err
		}
		items, err := templateItems(resp)
		if err != nil {
			return err
		}
		for _, raw := range items {
			var tpl model.Template
			if err := json.Unmarshal(raw, &tpl); err != nil {
				return fmt.Errorf("응답을 해석하지 못했습니다: %w", err)
			}
			if string(tpl.Type) != typ {
				continue
			}
			if g.json {
				writeJSON(a.Stdout, raw)
				return nil
			}
			_, err := io.WriteString(a.Stdout, tpl.Body)
			return err
		}
		return fmt.Errorf("서버가 %s 템플릿을 보내지 않았습니다", typ)
	},
}
