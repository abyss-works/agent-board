package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

var msgCmd = &command{name: "msg", summary: "프로젝트별 메시지 공간 (짧게 살다 만료)", subs: []*command{
	msgPutCmd, msgListCmd, msgGetCmd, msgAckCmd, msgRmCmd,
}, note: `메시지는 다른 호스트·세션·에이전트에 짧은 맥락을 넘기는 곳입니다. 보내는 쪽은 넣고 자기 일로 돌아가고,
읽는 쪽은 여럿일 수 있습니다. 읽는 쪽은 agb msg list --unread 로 자기가 확인하지 않은 메시지를 받아
처리한 뒤 agb msg ack 로 확인 표시를 합니다. 자기 일이 아닌 메시지도 넘기면서 확인 표시를 합니다.
확인 표시는 읽은 쪽마다 따로 남고 메시지를 지우지 않으므로, 다른 읽는 쪽은 그 메시지를 계속 봅니다.
이슈나 기록 문서가 아닙니다. 기간이 지난 메시지와 지운 메시지는 기본 목록에서 빠질 뿐 서버에 남고,
agb msg list --all 과 agb msg get 으로 볼 수 있습니다. 지운 메시지를 되돌릴 수 없습니다.
`}

// msgList is the response of the message list endpoint.
type msgList struct {
	Items  []model.Message `json:"items"`
	Total  int             `json:"total"`
	Offset int             `json:"offset"`
}

// repoProject is the project in the .agb.json above the working folder, or ""
// when there is none.
func repoProject() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", usageErrorf("작업 폴더를 알 수 없습니다: %v", err)
	}
	_, cfg, err := fileRepo(dir)
	if err != nil {
		return "", usageErrorf("%v", err)
	}
	if cfg == nil {
		return "", nil
	}
	return cfg.Project, nil
}

// msgProject is the project of a message command: --project, else the one in
// the .agb.json above the working folder.
func msgProject(project string) (string, error) {
	if project != "" {
		normalized := model.NormalizeProjectKey(project)
		if !model.ValidProjectKey(normalized) {
			return "", usageErrorf("프로젝트 키 형식이 맞지 않습니다: %q (영문으로 시작하는 대문자 영숫자 2~10자).", project)
		}
		return normalized, nil
	}
	project, err := repoProject()
	if err != nil || project != "" {
		return project, err
	}
	return "", usageErrorf("--project 가 필요합니다. 저장소에 %s 를 만들면(agb init -p <KEY>) 생략할 수 있습니다.", repoConfigFile)
}

// msgReader is the reader name of a read mark: --as, else the project in the
// .agb.json above the working folder, whatever message space --project names.
// An explicit empty --as "" is rejected so it does not silently fall back to
// the repository project.
func msgReader(fs *flag.FlagSet, as string) (string, error) {
	if isSet(fs, "as") {
		if as == "" {
			return "", usageErrorf("--as 값이 비어 있습니다. 읽은 쪽 이름을 주거나 옵션을 생략하세요.")
		}
		return as, nil
	}
	reader, err := repoProject()
	if err != nil || reader != "" {
		return reader, err
	}
	return "", usageErrorf("--as <이름> 이 필요합니다. 저장소에 %s 가 있으면(agb init -p <KEY>) 그 프로젝트 키를 읽은 쪽 이름으로 씁니다.", repoConfigFile)
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

func msgTargetText(m model.Message) string {
	var parts []string
	if m.To != nil && *m.To != "" {
		parts = append(parts, *m.To)
	}
	if m.ToSession != nil {
		parts = append(parts, sessionRefText(m.ToSession))
	}
	if m.ToRole != nil && *m.ToRole != "" {
		parts = append(parts, "role:"+*m.ToRole)
	}
	if m.Issue != nil && *m.Issue != "" {
		parts = append(parts, *m.Issue)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
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
		tb.header = append(tb.header, "TO", "REPLIES", "AGE", "BY", "TITLE")
		for _, m := range l.Items {
			row := []string{strconv.FormatInt(m.ID, 10)}
			if showState {
				row = append(row, m.State)
			}
			replies := "-"
			if m.ReplyCount > 0 {
				replies = strconv.Itoa(m.ReplyCount)
			}
			tb.add(append(row, msgTargetText(m), replies, m.Age, m.CreatedBy, m.Title)...)
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
	}
	if m.ToSession != nil {
		pairs = append(pairs, [2]string{"to_session", sessionRefText(m.ToSession)})
	}
	if m.ToRole != nil {
		pairs = append(pairs, [2]string{"to_role", *m.ToRole})
	}
	if m.Issue != nil {
		pairs = append(pairs, [2]string{"issue", *m.Issue})
	}
	if m.ReplyTo != nil {
		pairs = append(pairs, [2]string{"reply_to", strconv.FormatInt(*m.ReplyTo, 10)})
	}
	pairs = append(pairs, [][2]string{
		{"state", m.State},
		{"age", m.Age},
		{"created", formatTime(m.CreatedAt, a.Location) + " (" + m.CreatedBy + ", " + m.CreatedHost + ")"},
		{"expires", formatTime(m.ExpiresAt, a.Location)},
	}...)
	if m.DeletedAt != nil {
		pairs = append(pairs, [2]string{"deleted", formatTime(*m.DeletedAt, a.Location) + " (" + deref(m.DeletedBy, "-") + ")"})
	}
	for _, r := range m.Reads {
		pairs = append(pairs, [2]string{"read", r.Reader + "  " + a.readStamp(r)})
	}
	fields(w, pairs)
	if m.Body != nil && strings.TrimSpace(*m.Body) != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, strings.TrimRight(*m.Body, "\n"))
	}
	if m.Replies != nil && len(*m.Replies) > 0 {
		fmt.Fprintf(w, "\n== 답장 (%d) ==\n", len(*m.Replies))
		for _, r := range *m.Replies {
			state := ""
			if r.State != "" && r.State != model.MessageActive {
				state = "[" + r.State + "]  "
			}
			fmt.Fprintf(w, "\n#%d  %s  %s  %s%s\n", r.ID, formatTime(r.CreatedAt, a.Location), r.CreatedBy, state, r.Title)
			if r.Body != nil && strings.TrimSpace(*r.Body) != "" {
				fmt.Fprintln(w, strings.TrimRight(*r.Body, "\n"))
			}
		}
	}
	return nil
}

// readStamp is when and by whom a read mark was made.
func (a *App) readStamp(r model.MessageRead) string {
	return formatTime(r.ReadAt, a.Location) + " (" + r.ReadBy + ", " + r.ReadHost + ")"
}

func (a *App) renderMsgRead(w io.Writer, resp *client.Response) error {
	r, err := decode[model.MessageRead](resp)
	if err != nil {
		return err
	}
	fields(w, [][2]string{
		{"message", strconv.FormatInt(r.MessageID, 10)},
		{"reader", r.Reader},
		{"read", a.readStamp(r)},
	})
	return nil
}

type msgRoutingFlags struct {
	toSession string
	toRole    string
	issue     string
	replyTo   int64
}

func (r *msgRoutingFlags) addFlags(fs *flag.FlagSet) {
	fs.StringVar(&r.toSession, "to-session", "", "")
	fs.StringVar(&r.toRole, "to-role", "", "")
	fs.StringVar(&r.issue, "issue", "", "")
	fs.Int64Var(&r.replyTo, "reply-to", 0, "")
}

func (r msgRoutingFlags) validate(fs *flag.FlagSet) (*model.SessionRef, error) {
	var ref *model.SessionRef
	if isSet(fs, "to-session") {
		parsed, err := parseSessionArg(r.toSession, "--to-session")
		if err != nil {
			return nil, err
		}
		ref = &parsed
	}
	if isSet(fs, "to-role") {
		if _, ok := model.ParseSessionRole(r.toRole); !ok {
			return nil, usageErrorf("--to-role 은 main, subagent, runner 중 하나여야 합니다: %q", r.toRole)
		}
	}
	if isSet(fs, "issue") {
		if !model.ValidIssueKey(r.issue) {
			return nil, usageErrorf("--issue 는 이슈 키 형식(예: AGB-12)이어야 합니다: %q", r.issue)
		}
	}
	if isSet(fs, "reply-to") && r.replyTo < 1 {
		return nil, usageErrorf("--reply-to 는 1 이상의 정수여야 합니다: %d", r.replyTo)
	}
	return ref, nil
}

var msgPutCmd = &command{
	name: "put", summary: "메시지 넣기",
	usage: `사용법: agb msg put [--project <KEY>] --title <제목> [옵션]

프로젝트의 메시지 공간에 메시지를 넣고 바로 돌아옵니다. 받는 쪽 확인을 기다리지 않습니다.
보낸 행위자, 세션, 기기가 메시지에 함께 남습니다.

옵션:
  --project, -p <KEY>         프로젝트 키. 생략하면 저장소의 .agb.json 의 프로젝트
  --title, -t <제목>          1~200자 (필수)
  --to <라벨>                 대상 라벨 관례(에이전트·역할·유형, 예: reviewer, desk, codex). 공백 없이 1~64자
  --to-session <행위자/세션>  받는 세션 지정. 이 세션의 기본·--unread 목록에만 보임
  --to-role <역할>            받는 역할 지정: main, subagent, runner 중 하나
  --issue <KEY>               연결할 같은 프로젝트의 이슈 키
  --reply-to <ID>             답장할 같은 프로젝트의 메시지 ID (만료·삭제된 메시지도 가능)
  --ttl <기간>                살아 있는 기간. 24h, 30m, 3d 형식. 1분 이상 90일 이하, 생략하면 7일
` + bodyUsage + `
본문은 마크다운이고 64KiB 이하입니다. 생략하면 제목만 남습니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("msg put", g)
		var project, title, to, ttl, body, bodyFile string
		var routing msgRoutingFlags
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.StringVar(&title, "title", "", "")
		fs.StringVar(&title, "t", "", "")
		fs.StringVar(&to, "to", "", "")
		fs.StringVar(&ttl, "ttl", "", "")
		fs.StringVar(&body, "body", "", "")
		fs.StringVar(&bodyFile, "body-file", "", "")
		routing.addFlags(fs)
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
		toSessionRef, err := routing.validate(fs)
		if err != nil {
			return err
		}
		in := map[string]any{"title": title}
		for name, v := range map[string]string{"to": to, "ttl": ttl, "to_role": routing.toRole, "issue": routing.issue} {
			if v != "" {
				in[name] = v
			}
		}
		if toSessionRef != nil {
			in["to_session"] = map[string]any{"actor": toSessionRef.Actor, "session_id": toSessionRef.SessionID}
		}
		if isSet(fs, "reply-to") {
			in["reply_to"] = routing.replyTo
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
	usage: `사용법: agb msg list [--project <KEY>] [--to <라벨>] [--to-session <행위자/세션>] [--to-role <역할>] [--issue <KEY>] [--reply-to <ID>] [--unread [--as <이름>]] [--all]

기간이 지나지 않았고 지우지 않은 메시지를 최신 순으로 보입니다. 본문은 agb msg get <ID> 로 읽습니다.
AGE 는 넣은 지 얼마나 지났는지입니다. 예: agb msg list -p AGB --fields id,to,title,created_by,age --text
특정 세션(--to-session)을 지정한 메시지는 해당 세션의 기본·--unread 목록에만 나오고, 브로드캐스트 메시지는 모두에게 나옵니다.
--unread 는 읽은 쪽이 agb msg ack 로 확인 표시를 하지 않은 메시지만 보입니다.

옵션:
  --project, -p <KEY>         프로젝트 키. 생략하면 저장소의 .agb.json 의 프로젝트
  --to <라벨>                 받는 쪽 라벨이 같은 메시지만
  --to-session <행위자/세션>  받는 세션이 같은 메시지만
  --to-role <역할>            받는 역할(main, subagent, runner)이 같은 메시지만
  --issue <KEY>               이 이슈와 연결된 메시지만
  --reply-to <ID>             이 메시지의 답장만
  --unread                    읽은 쪽이 확인 표시를 하지 않은 메시지만
  --as <이름>                 --unread 의 읽은 쪽 이름. 공백 없이 1~64자. 생략하면 명령을 실행한 저장소의
                              .agb.json 의 프로젝트 키 (--project 로 다른 프로젝트를 가리켜도 같음)
  --all                       기간이 지난 메시지·지운 메시지·다른 세션 지정 메시지도 모두 보임 (STATE 열: active, expired, deleted)
  --limit <N>                 최대 200. 기본 50
  --offset <N>
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("msg list", g)
		var project, as string
		var unread bool
		var f client.MessageFilter
		var routing msgRoutingFlags
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.StringVar(&f.To, "to", "", "")
		routing.addFlags(fs)
		fs.BoolVar(&unread, "unread", false, "")
		fs.StringVar(&as, "as", "", "")
		fs.BoolVar(&f.All, "all", false, "")
		fs.IntVar(&f.Limit, "limit", 0, "")
		fs.IntVar(&f.Offset, "offset", 0, "")
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
		if _, err := routing.validate(fs); err != nil {
			return err
		}
		f.ToSession = routing.toSession
		f.ToRole = routing.toRole
		f.Issue = routing.issue
		f.ReplyTo = routing.replyTo
		if unread {
			if f.UnreadBy, err = msgReader(fs, as); err != nil {
				return err
			}
		} else if isSet(fs, "as") {
			return usageErrorf("--as 는 --unread 와 함께 씁니다.")
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.ListMessages(ctx, project, f)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderMsgList(f.All))
	},
}

var msgGetCmd = &command{
	name: "get", summary: "메시지 단건 (기간이 지났거나 지운 것도)",
	usage: `사용법: agb msg get <ID>

본문과 직계 답장까지 보입니다. 기간이 지났거나 지운 메시지도 읽을 수 있고 STATE 로 구분합니다.
확인 표시가 있으면 읽은 쪽마다 read 줄에 이름, 시각, 확인한 행위자와 기기를 보입니다.
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

var msgAckCmd = &command{
	name: "ack", summary: "읽은 쪽의 확인 표시 (여러 개 가능)",
	usage: `사용법: agb msg ack <ID>... [--as <이름>]

메시지를 처리한 뒤 확인 표시를 합니다. 처리 방식은 쓰는 쪽이 정하며, 자기 일이 아닌 메시지도 넘기면서 표시할 수 있습니다.
확인 표시는 읽은 쪽 이름마다 따로 남고 메시지는 그대로 있어, 다른 읽는 쪽의 목록에는 계속 보입니다.
표시한 메시지는 같은 이름의 agb msg list --unread 에서 빠집니다. 이미 표시한 메시지를 다시 표시하면
처음 기록이 그대로 남습니다. 확인 표시를 되돌릴 수 없습니다. 기간이 지났거나 지운 메시지에도 표시할 수 있습니다.
ID 를 여러 개 주면 차례로 처리하고, 처음 실패하는 ID 에서 멈춥니다. JSON 출력은 ID 마다 서버 응답을
한 줄씩 냅니다.

옵션:
  --as <이름>             읽은 쪽 이름. 공백 없이 1~64자. 생략하면 명령을 실행한 저장소의 .agb.json 의 프로젝트 키
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("msg ack", g)
		var as string
		fs.StringVar(&as, "as", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return usageErrorf("<ID> 인자가 필요합니다.")
		}
		ids := make([]int64, len(pos))
		for n, raw := range pos {
			id, ok := parseID(raw)
			if !ok {
				return usageErrorf("<ID>는 메시지 ID(숫자)여야 합니다: %q. agb msg list -p <KEY> 로 확인하세요.", raw)
			}
			ids[n] = id
		}
		reader, err := msgReader(fs, as)
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		for _, id := range ids {
			resp, err := c.AckMessage(ctx, id, reader)
			if err != nil {
				return err
			}
			if err := a.emit(g, resp, a.renderMsgRead); err != nil {
				return err
			}
		}
		return nil
	},
}

var msgRmCmd = &command{
	name: "rm", summary: "메시지 지우기 (소프트 삭제)",
	usage: `사용법: agb msg rm <ID>

메시지를 모든 읽는 쪽의 기본 목록에서 뺍니다. 자기가 읽었다는 표시는 agb msg ack 로 합니다.
지운 메시지는 기본 목록에서 빠지지만 서버에 남아 agb msg list --all 과 agb msg get 으로 볼 수 있습니다.
지운 메시지를 되돌릴 수 없습니다. 이미 지운 메시지를 다시 지워도 같은 결과입니다.
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
