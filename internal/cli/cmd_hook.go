package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
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

// hookTitleMax and hookToMax are how many characters of a title and of a to
// label the session start hook shows. With hookListMax lines the output stays
// far below 10,000 characters. The server allows a to label 64 characters.
const (
	hookTitleMax = 300
	hookToMax    = 64
)

// hookText makes text from the server fit into one line of hook output: each
// control character and each line or paragraph separator becomes a space, and
// what goes beyond max characters is cut.
func hookText(s string, max int) string {
	r := []rune(strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || c == '\u2028' || c == '\u2029' {
			return ' '
		}
		return c
	}, s))
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return string(r)
}

var hookSessionStartCmd = &command{
	name: "session-start", summary: "세션을 시작할 때 확인하지 않은 메시지를 알림",
	usage: `사용법: agb hook session-start [--tool <이름>]

세션 시작 훅이 부르는 명령입니다. 표준 입력으로 훅의 JSON 을 받아 그 cwd 가 속한 저장소의 .agb.json 에서
프로젝트를 찾고, 그 프로젝트의 메시지 공간에서 그 프로젝트 키가 확인하지 않은 메시지를 표준 출력에 적습니다
(agb msg list --unread 와 같은 조회). 표준 입력이 비었거나 JSON 이 아니거나 cwd 가 없으면 현재 작업 폴더를 씁니다.
표준 입력 맨 앞의 UTF-8 BOM 은 무시합니다.

출력은 건수와 프로젝트 키 한 줄, 메시지마다 한 줄(최대 20줄, 넘으면 남은 건수 한 줄), 읽고 확인하는 명령 한 줄입니다.
확인 표시는 만들지 않습니다. 서버 조회는 5초 안에 끝나지 않으면 그만둡니다.

옵션:
  --tool <이름>   훅을 부르는 에이전트 도구의 이름 (agb setup <대상> 의 대상 이름. 예: claude)

행위자는 --actor, AGB_ACTOR, 설정 파일에 --tool 의 도구 이름으로 저장된 훅 행위자(hook_actors), 설정 파일의 actor
순서로 정합니다. 훅 행위자는 agb setup <대상> 이 저장하고 이 명령만 씁니다. --tool 이 없으면 훅 행위자를 건너뜁니다.
세션 ID 는 --session-id, 표준 입력 JSON 의 session_id(세션 ID 형식에 맞을 때), AGB_SESSION_ID, 설정 파일의
session_id, 기본값 순서로 정합니다.

.agb.json 이 없거나, 설정이나 행위자나 토큰이 없거나, 서버에 닿지 않거나, 서버가 오류를 돌려주거나, 확인하지 않은
메시지가 없으면 아무것도 출력하지 않습니다. 모르는 옵션과 남는 인자는 무시합니다. 어떤 경우에도 종료 코드는 0 이고
표준 오류에 쓰지 않습니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("hook session-start", g)
		tool := fs.String("tool", "", "")
		if _, err := parseArgs(fs, args); err == flag.ErrHelp {
			return err
		}
		var in hookStdin
		if !a.StdinIsTerminal {
			if raw, err := io.ReadAll(a.Stdin); err == nil {
				json.Unmarshal(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")), &in)
			}
		}
		// Every failure ends in silence: the hook must neither hold up the
		// session nor put an error text into its context.
		if out, _ := a.unreadNotice(ctx, g, *tool, in); out != "" {
			io.WriteString(a.Stdout, out)
		}
		return nil
	},
}

// hookStdin is what a hook reads of the JSON on its standard input.
type hookStdin struct {
	Cwd       string `json:"cwd"`
	SessionID string `json:"session_id"`
}

// unreadNotice is the text of the session start hook, the messages that the
// project of the session's folder has not acknowledged, and one line that
// says what the session gets: how many messages, or why there is no text.
// notice is "" when there are no messages and when anything on the way fails.
//
// The request is made as the flag's actor, else AGB_ACTOR, else the hook actor
// of tool in the config file, else the actor of the file. Its session ID is
// the flag's, else the one of the input when it is well formed, else what
// every command uses.
func (a *App) unreadNotice(ctx context.Context, g *globals, tool string, in hookStdin) (notice, summary string) {
	dir := in.Cwd
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return "", "이 폴더에서는 프로젝트를 판정할 수 없습니다"
		}
	}
	_, cfg, err := fileRepo(dir)
	if err != nil || cfg == nil {
		return "", "이 폴더에서는 프로젝트를 판정할 수 없습니다"
	}
	hg := *g
	if hg.actor == "" && a.Getenv("AGB_ACTOR") == "" {
		if path, err := a.configPath(g); err == nil {
			if file, err := loadConfig(path); err == nil {
				hg.actor = file.HookActors[tool]
			}
		}
	}
	if hg.sessionID == "" && model.ValidSessionID(in.SessionID) {
		hg.sessionID = in.SessionID
	}
	c, err := a.client(&hg, true)
	if err != nil {
		return "", err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, hookQueryTimeout)
	defer cancel()
	resp, err := c.ListMessages(ctx, cfg.Project, "", cfg.Project, false, client.Page{Limit: hookListMax})
	var apiErr *client.Error
	if errors.As(err, &apiErr) {
		return "", fmt.Sprintf("서버가 오류를 돌려주었습니다 (%d %s)", apiErr.Status, apiErr.Code)
	}
	if err != nil {
		return "", "서버에 닿지 않습니다"
	}
	l, err := decode[msgList](resp)
	if err != nil {
		return "", "서버의 응답을 읽지 못했습니다"
	}
	items := l.Items[:min(len(l.Items), hookListMax)]
	total := max(l.Total, len(items))
	summary = fmt.Sprintf("프로젝트 %s의 확인하지 않은 메시지 %d건", cfg.Project, total)
	if len(items) == 0 {
		return "", summary
	}
	var b strings.Builder
	fmt.Fprintf(&b, "확인하지 않은 메시지 %d건 (프로젝트 %s)\n", total, cfg.Project)
	for _, m := range items {
		fmt.Fprintf(&b, "#%d (%s 전) %s", m.ID, m.Age, hookText(m.Title, hookTitleMax))
		if m.To != nil {
			fmt.Fprintf(&b, " [to: %s]", hookText(*m.To, hookToMax))
		}
		b.WriteString("\n")
	}
	if rest := l.Total - len(items); rest > 0 {
		fmt.Fprintf(&b, "외 %d건\n", rest)
	}
	b.WriteString("읽기: agb msg get <ID> / 처리한 뒤 확인: agb msg ack <ID>\n")
	return b.String(), summary
}
