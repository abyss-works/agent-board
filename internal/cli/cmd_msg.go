package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

var msgCmd = &command{name: "msg", summary: "프로젝트별 메시지 공간 (짧게 살다 만료)", subs: []*command{
	msgPutCmd, msgListCmd, msgGetCmd, msgRmCmd,
}, note: `메시지는 다른 호스트·세션·에이전트에 짧은 맥락을 넘기는 곳입니다. 보내는 쪽은 넣고 자기 일로 돌아가고,
받는 쪽은 읽고 처리한 뒤 지웁니다. 이슈나 기록 문서가 아닙니다. 기간이 지난 메시지와 지운 메시지는 기본
목록에서 빠질 뿐 서버에 남고, agb msg list --all 과 agb msg get 으로 볼 수 있습니다.
`}

// msgList is the response of the message list endpoint.
type msgList struct {
	Items  []model.Message `json:"items"`
	Total  int             `json:"total"`
	Offset int             `json:"offset"`
}

// msgProject is the project of a message command: --project, else the one in
// the .agb.json above the working folder.
func msgProject(project string) (string, error) {
	if project != "" {
		return project, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", usageErrorf("작업 폴더를 알 수 없습니다: %v", err)
	}
	_, cfg, err := fileRepo(dir)
	if err != nil {
		return "", usageErrorf("%v", err)
	}
	if cfg != nil {
		return cfg.Project, nil
	}
	return "", usageErrorf("--project 가 필요합니다. 저장소에 %s 를 만들면(agb init -p <KEY>) 생략할 수 있습니다.", repoConfigFile)
}

// msgIDArg reads the <ID> argument of the commands that act on one message.
func msgIDArg(pos []string) (int64, error) {
	raw, err := one(pos, "<ID>")
	if err != nil {
		return 0, err
	}
	id, ok := parseID(raw)
	if !ok {
		return 0, usageErrorf("<ID>는 메시지 ID(숫자)여야 합니다: %q. agb msg list -p <KEY> 로 확인하세요.", raw)
	}
	return id, nil
}

func (a *App) renderMsgList(showState bool) func(w io.Writer, resp *client.Response) error {
	return func(w io.Writer, resp *client.Response) error {
		l, err := decode[msgList](resp)
		if err != nil {
			return err
		}
		if len(l.Items) == 0 {
			fmt.Fprintln(w, "메시지가 없습니다.")
			return nil
		}
		tb := &table{header: []string{"ID"}}
		if showState {
			tb.header = append(tb.header, "STATE")
		}
		tb.header = append(tb.header, "TO", "AGE", "BY", "TITLE")
		for _, m := range l.Items {
			row := []string{strconv.FormatInt(m.ID, 10)}
			if showState {
				row = append(row, m.State)
			}
			tb.add(append(row, deref(m.To, "-"), m.Age, m.CreatedBy, m.Title)...)
		}
		tb.write(w, a.TermWidth)
		footer(w, len(l.Items), l.Total, l.Offset)
		return nil
	}
}

func (a *App) renderMsg(w io.Writer, resp *client.Response) error {
	m, err := decode[model.Message](resp)
	if err != nil {
		return err
	}
	pairs := [][2]string{
		{"id", strconv.FormatInt(m.ID, 10)},
		{"project", m.Project},
		{"title", m.Title},
		{"to", deref(m.To, "-")},
		{"state", m.State},
		{"age", m.Age},
		{"created", formatTime(m.CreatedAt, a.Location) + " (" + m.CreatedBy + ", " + m.CreatedHost + ")"},
		{"expires", formatTime(m.ExpiresAt, a.Location)},
	}
	if m.DeletedAt != nil {
		pairs = append(pairs, [2]string{"deleted", formatTime(*m.DeletedAt, a.Location) + " (" + deref(m.DeletedBy, "-") + ")"})
	}
	fields(w, pairs)
	if m.Body != nil && strings.TrimSpace(*m.Body) != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, strings.TrimRight(*m.Body, "\n"))
	}
	return nil
}

var msgPutCmd = &command{
	name: "put", summary: "메시지 넣기",
	usage: `사용법: agb msg put [--project <KEY>] --title <제목> [옵션]

프로젝트의 메시지 공간에 메시지를 넣고 바로 돌아옵니다. 받는 쪽 확인을 기다리지 않습니다.
보낸 행위자, 세션, 기기가 메시지에 함께 남습니다.

옵션:
  --project, -p <KEY>     프로젝트 키. 생략하면 저장소의 .agb.json 의 프로젝트
  --title, -t <제목>      1~200자 (필수)
  --to <라벨>             받는 쪽을 가리키는 자유 문자열. 예: framework, ab, codex. 공백 없이 1~64자. 생략하면 받는 쪽 없음
  --ttl <기간>            살아 있는 기간. 24h, 30m, 3d 형식. 1분 이상 90일 이하, 생략하면 7일
` + bodyUsage + `
본문은 마크다운이고 64KiB 이하입니다. 생략하면 제목만 남습니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("msg put", g)
		var project, title, to, ttl, body, bodyFile string
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.StringVar(&title, "title", "", "")
		fs.StringVar(&title, "t", "", "")
		fs.StringVar(&to, "to", "", "")
		fs.StringVar(&ttl, "ttl", "", "")
		fs.StringVar(&body, "body", "", "")
		fs.StringVar(&bodyFile, "body-file", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		if project, err = msgProject(project); err != nil {
			return err
		}
		if title == "" {
			return usageErrorf("--title 이 필요합니다.")
		}
		in := map[string]any{"title": title}
		for name, v := range map[string]string{"to": to, "ttl": ttl} {
			if v != "" {
				in[name] = v
			}
		}
		text, present, err := a.readBody(fs, body, bodyFile)
		if err != nil {
			return err
		}
		if present {
			in["body"] = text
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.PutMessage(ctx, project, in)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderMsg)
	},
}

var msgListCmd = &command{
	name: "list", summary: "메시지 목록 (최신 순, 만료·삭제 제외)",
	usage: `사용법: agb msg list [--project <KEY>] [--to <라벨>] [--all]

기간이 지나지 않았고 지우지 않은 메시지를 최신 순으로 보입니다. 본문은 agb msg get <ID> 로 읽습니다.
AGE 는 넣은 지 얼마나 지났는지입니다. 예: agb msg list -p AGB --fields id,to,title,created_by,age --text

옵션:
  --project, -p <KEY>     프로젝트 키. 생략하면 저장소의 .agb.json 의 프로젝트
  --to <라벨>             받는 쪽 라벨이 같은 메시지만
  --all                   기간이 지난 메시지와 지운 메시지도 보임 (STATE 열: active, expired, deleted)
  --limit <N>             최대 200. 기본 50
  --offset <N>
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("msg list", g)
		var project, to string
		var all bool
		var limit, offset int
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.StringVar(&to, "to", "", "")
		fs.BoolVar(&all, "all", false, "")
		fs.IntVar(&limit, "limit", 0, "")
		fs.IntVar(&offset, "offset", 0, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		if project, err = msgProject(project); err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.ListMessages(ctx, project, to, all, client.Page{Limit: limit, Offset: offset})
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderMsgList(all))
	},
}

var msgGetCmd = &command{
	name: "get", summary: "메시지 단건 (기간이 지났거나 지운 것도)",
	usage: `사용법: agb msg get <ID>

본문까지 보입니다. 기간이 지났거나 지운 메시지도 읽을 수 있고 STATE 로 구분합니다.
본문만 그대로 받으려면 agb msg get <ID> --fields body --text.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("msg get", g), args)
		if err != nil {
			return err
		}
		id, err := msgIDArg(pos)
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.GetMessage(ctx, id)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderMsg)
	},
}

var msgRmCmd = &command{
	name: "rm", summary: "메시지 지우기 (소프트 삭제)",
	usage: `사용법: agb msg rm <ID>

받은 내용을 처리한 뒤 메시지를 지웁니다. 지운 메시지는 기본 목록에서 빠지지만 서버에 남아
agb msg list --all 과 agb msg get 으로 볼 수 있습니다. 이미 지운 메시지를 다시 지워도 같은 결과입니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("msg rm", g), args)
		if err != nil {
			return err
		}
		id, err := msgIDArg(pos)
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.DeleteMessage(ctx, id)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderMsg)
	},
}
