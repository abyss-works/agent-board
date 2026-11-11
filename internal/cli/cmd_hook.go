package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/wsk0715/agent-board/internal/client"
)

var hookCmd = &command{name: "hook", summary: "에이전트 도구의 훅이 부르는 명령", subs: []*command{
	hookSessionStartCmd,
}, note: `에이전트 도구의 훅 설정이 부르는 명령입니다. 사람이 직접 실행할 일은 없습니다.
Claude Code 에는 agb setup claude 가 이 명령을 부르는 플러그인을 설치합니다.
`}

// hookQueryTimeout bounds the whole server query of a hook, so a server that
// is slow or away does not hold up the start of a session.
var hookQueryTimeout = 5 * time.Second

// hookListMax is how many messages the session start hook lists.
const hookListMax = 20

// hookTitleMax is how many characters of a title the session start hook
// shows. With hookListMax lines and the 64 characters the server allows a to
// label, the output stays far below 10,000 characters.
const hookTitleMax = 300

var hookSessionStartCmd = &command{
	name: "session-start", summary: "세션을 시작할 때 확인하지 않은 메시지를 알림",
	usage: `사용법: agb hook session-start

세션 시작 훅이 부르는 명령입니다. 표준 입력으로 훅의 JSON 을 받아 그 cwd 가 속한 저장소의 .agb.json 에서
프로젝트를 찾고, 그 프로젝트의 메시지 공간에서 그 프로젝트 키가 확인하지 않은 메시지를 표준 출력에 적습니다
(agb msg list --unread 와 같은 조회). 표준 입력이 비었거나 JSON 이 아니거나 cwd 가 없으면 현재 작업 폴더를 씁니다.

출력은 건수와 프로젝트 키 한 줄, 메시지마다 한 줄(최대 20줄, 넘으면 남은 건수 한 줄), 읽고 확인하는 명령 한 줄입니다.
확인 표시는 만들지 않습니다. 서버 조회는 5초 안에 끝나지 않으면 그만둡니다.

.agb.json 이 없거나, 설정이나 토큰이 없거나, 서버에 닿지 않거나, 서버가 오류를 돌려주거나, 확인하지 않은 메시지가
없으면 아무것도 출력하지 않습니다. 어떤 경우에도 종료 코드는 0 이고 표준 오류에 쓰지 않습니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		if _, err := parseArgs(leafFlags("hook session-start", g), args); err == flag.ErrHelp {
			return err
		}
		// Every failure ends in silence: the hook must neither hold up the
		// session nor put an error text into its context.
		if out := a.unreadNotice(ctx, g); out != "" {
			io.WriteString(a.Stdout, out)
		}
		return nil
	},
}

// unreadNotice is the text of the session start hook: the messages that the
// project of the session's folder has not acknowledged. It is "" when there
// are none and when anything on the way fails.
func (a *App) unreadNotice(ctx context.Context, g *globals) string {
	var in struct {
		Cwd string `json:"cwd"`
	}
	if !a.StdinIsTerminal {
		if raw, err := io.ReadAll(a.Stdin); err == nil {
			json.Unmarshal(raw, &in)
		}
	}
	dir := in.Cwd
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return ""
		}
	}
	_, cfg, err := fileRepo(dir)
	if err != nil || cfg == nil {
		return ""
	}
	c, err := a.client(g, true)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, hookQueryTimeout)
	defer cancel()
	resp, err := c.ListMessages(ctx, cfg.Project, "", cfg.Project, false, client.Page{Limit: hookListMax})
	if err != nil {
		return ""
	}
	l, err := decode[msgList](resp)
	if err != nil || len(l.Items) == 0 {
		return ""
	}
	items := l.Items[:min(len(l.Items), hookListMax)]
	var b strings.Builder
	fmt.Fprintf(&b, "확인하지 않은 메시지 %d건 (프로젝트 %s)\n", max(l.Total, len(items)), cfg.Project)
	for _, m := range items {
		title := m.Title
		if r := []rune(title); len(r) > hookTitleMax {
			title = string(r[:hookTitleMax]) + "…"
		}
		fmt.Fprintf(&b, "#%d (%s 전) %s", m.ID, m.Age, title)
		if m.To != nil {
			fmt.Fprintf(&b, " [to: %s]", *m.To)
		}
		b.WriteString("\n")
	}
	if rest := l.Total - len(items); rest > 0 {
		fmt.Fprintf(&b, "외 %d건\n", rest)
	}
	b.WriteString("읽기: agb msg get <ID> / 처리한 뒤 확인: agb msg ack <ID>\n")
	return b.String()
}
