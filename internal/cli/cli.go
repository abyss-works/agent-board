// Package cli implements the agb client commands. They talk to the server
// over HTTP only and carry no business rules.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/wsk0715/agent-board/internal/client"
)

// App holds everything a command needs from its environment so tests can
// substitute every part.
type App struct {
	Version    string
	Getenv     func(string) string
	Hostname   func() (string, error)
	Now        func() time.Time
	Location   *time.Location
	ConfigPath string // default config file; --config overrides it
	HTTP       *http.Client

	Stdin            io.Reader
	Stdout           io.Writer
	Stderr           io.Writer
	StdoutIsTerminal bool
	StdinIsTerminal  bool
	TermWidth        int                    // 0 when unknown
	ReadSecret       func() (string, error) // reads a line from a terminal without echo; nil reads Stdin
}

// New builds an App bound to the process environment.
func New(version string, getenv func(string) string) *App {
	a := &App{
		Version:          version,
		Getenv:           getenv,
		Hostname:         os.Hostname,
		Now:              time.Now,
		Location:         time.Local,
		HTTP:             &http.Client{Timeout: 30 * time.Second},
		Stdin:            os.Stdin,
		Stdout:           os.Stdout,
		Stderr:           os.Stderr,
		StdoutIsTerminal: isTerminal(os.Stdout),
		StdinIsTerminal:  isTerminal(os.Stdin),
	}
	if a.StdoutIsTerminal {
		a.TermWidth = terminalWidth(os.Stdout)
	}
	if a.StdinIsTerminal {
		a.ReadSecret = func() (string, error) { return readSecret(os.Stdin) }
	}
	if p, err := defaultConfigPath(); err == nil {
		a.ConfigPath = p
	}
	return a
}

// command is one node of the command tree. Groups have subs; leaves have
// run.
type command struct {
	name    string
	summary string
	usage   string
	run     func(a *App, ctx context.Context, g *globals, args []string) error
	subs    []*command
}

func (c *command) find(name string) *command {
	for _, s := range c.subs {
		if s.name == name {
			return s
		}
	}
	return nil
}

// help renders a group's usage from its summary lines.
func (c *command) help(path string) string {
	if len(c.subs) == 0 {
		return c.usage
	}
	var b strings.Builder
	fmt.Fprintf(&b, "사용법: %s <명령> [옵션]\n\n명령:\n", path)
	for _, s := range c.subs {
		fmt.Fprintf(&b, "  %-12s %s\n", s.name, s.summary)
	}
	fmt.Fprintf(&b, "\n%s <명령> --help 로 명령별 사용법을 봅니다.\n", path)
	return b.String()
}

// Usage is the top-level help, including the server commands handled by
// the main package.
const Usage = `사용법: agb <명령> [옵션]

서버:
  serve             API 서버 실행 (AGB_* 환경변수로 설정)
  version           바이너리 버전 출력

설정:
  config            설정 파일 경로, 표시, 수정 (url, actor, session_id, client_host, 토큰)

상태:
  health            서버 상태 (인증 없음)
  whoami            현재 행위자
  actors            선언된 행위자 목록

프로젝트와 이슈:
  project           프로젝트 목록·단건·생성·수정·삭제·현황
  topic             대주제·주제 목록·생성·수정·삭제
  issue             이슈 목록·단건·생성·수정·삭제·확인·댓글·활동

조회:
  activity          전체 활동 기록
  board             상태별 보드
  queue             우선순위 큐
  labels            라벨과 사용 개수
  search <검색어>   전문 검색 (issue list -q 와 같음)

공통 옵션 (명령 앞뒤 어디에나):
  --json            JSON 출력 (서버 응답 그대로). 기본: 파이프·파일일 때
  --table           표 출력. 기본: 터미널일 때
  --url <주소>      서버 주소 (AGB_URL, 설정 파일보다 우선)
  --actor <ID>      행위자 (AGB_ACTOR)
  --session-id <ID> 세션 ID (AGB_SESSION_ID). 기본 cli-YYYYMMDD
  --client-host <H> 기기 이름 (AGB_CLIENT_HOST). 기본 호스트 이름
  --config <경로>   설정 파일 경로

우선순위: 옵션 > 환경변수 > 설정 파일. 토큰은 AGB_TOKEN 또는 설정 파일에서만 읽습니다.
종료 코드: 0 성공, 1 API·네트워크 오류, 2 사용법 오류

agb help <명령> 또는 agb <명령> --help 로 자세한 사용법을 봅니다.
`

var root = &command{name: "agb", subs: []*command{
	configCmd, healthCmd, whoamiCmd, actorsCmd, projectCmd, topicCmd, issueCmd,
	activityCmd, boardCmd, queueCmd, labelsCmd, searchCmd,
}}

// Run executes args and returns the exit code: 0 success, 1 API or
// network error, 2 usage error.
func (a *App) Run(ctx context.Context, args []string) int {
	g := &globals{}
	fs := newFlagSet("agb")
	g.addTo(fs)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			io.WriteString(a.Stdout, Usage)
			return 0
		}
		fmt.Fprintf(a.Stderr, "agb: %v\n\n%s", err, Usage)
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		io.WriteString(a.Stderr, Usage)
		return 2
	}
	if rest[0] == "help" {
		return a.helpFor(rest[1:])
	}
	cmd := root.find(rest[0])
	if cmd == nil {
		fmt.Fprintf(a.Stderr, "agb: 모르는 명령 %q\n\n%s", rest[0], Usage)
		return 2
	}
	err := a.dispatch(ctx, g, cmd, "agb "+cmd.name, rest[1:])
	return a.exit(g, err)
}

// helpFor prints the usage of the command named by path.
func (a *App) helpFor(path []string) int {
	cmd, name := root, "agb"
	for _, p := range path {
		next := cmd.find(p)
		if next == nil {
			fmt.Fprintf(a.Stderr, "agb: 모르는 명령 %q\n", strings.Join(path, " "))
			return 2
		}
		cmd, name = next, name+" "+p
	}
	if cmd == root {
		io.WriteString(a.Stdout, Usage)
	} else {
		io.WriteString(a.Stdout, cmd.help(name))
	}
	return 0
}

func (a *App) dispatch(ctx context.Context, g *globals, cmd *command, path string, args []string) error {
	if len(cmd.subs) > 0 {
		if len(args) == 0 {
			return &usageError{msg: "하위 명령이 필요합니다.", help: cmd.help(path)}
		}
		if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
			io.WriteString(a.Stdout, cmd.help(path))
			return nil
		}
		sub := cmd.find(args[0])
		if sub == nil {
			return &usageError{msg: fmt.Sprintf("모르는 명령 %q", args[0]), help: cmd.help(path)}
		}
		return a.dispatch(ctx, g, sub, path+" "+sub.name, args[1:])
	}
	err := cmd.run(a, ctx, g, args)
	if err == flag.ErrHelp {
		io.WriteString(a.Stdout, cmd.usage)
		return nil
	}
	var ue *usageError
	if errors.As(err, &ue) && ue.help == "" {
		ue.help = "자세한 사용법: " + path + " --help\n"
	}
	return err
}

// exit reports err and maps it to an exit code.
func (a *App) exit(g *globals, err error) int {
	if err == nil {
		return 0
	}
	var ue *usageError
	var apiErr *client.Error
	switch {
	case errors.As(err, &ue):
		fmt.Fprintf(a.Stderr, "agb: %s\n", ue.msg)
		if ue.help != "" {
			fmt.Fprintf(a.Stderr, "%s", ue.help)
		}
		return 2
	case errors.As(err, &apiErr):
		a.printAPIError(g, apiErr)
		return 1
	default:
		fmt.Fprintf(a.Stderr, "agb: %v\n", err)
		return 1
	}
}

// printAPIError writes the server error: verbatim in JSON mode, as text
// otherwise.
func (a *App) printAPIError(g *globals, e *client.Error) {
	if a.jsonOutput(g) && len(e.Body) > 0 && e.Code != "http_error" {
		writeJSON(a.Stderr, e.Body)
		return
	}
	fmt.Fprintf(a.Stderr, "agb: %d %s: %s\n", e.Status, e.Code, e.Message)
	for _, d := range e.Details {
		line := "  "
		if d.Field != "" {
			line += d.Field + ": "
		}
		line += d.Message
		if d.Reason != "" {
			line += " (" + d.Reason + ")"
		}
		fmt.Fprintln(a.Stderr, line)
	}
	if e.Status == http.StatusPreconditionFailed {
		fmt.Fprintln(a.Stderr, "  다른 쪽에서 먼저 수정했습니다. 다시 조회한 뒤 재시도하세요.")
	}
}

// configPath is the config file in use.
func (a *App) configPath(g *globals) (string, error) {
	if g.config != "" {
		return g.config, nil
	}
	if a.ConfigPath == "" {
		return "", errors.New("사용자 설정 폴더를 찾지 못했습니다. --config <경로>로 설정 파일을 지정하세요.")
	}
	return a.ConfigPath, nil
}

// client resolves the settings and builds the HTTP client. needAuth
// requires a token and an actor.
func (a *App) client(g *globals, needAuth bool) (*client.Client, error) {
	path, err := a.configPath(g)
	if err != nil {
		return nil, err
	}
	cfg, err := loadConfig(path)
	if err != nil {
		return nil, err
	}
	host, err := a.Hostname()
	if err != nil {
		host = ""
	}
	s, err := resolveSettings(g, a.Getenv, cfg, a.Now(), host)
	if err != nil {
		return nil, err
	}
	if needAuth {
		if err := s.requireAuth(); err != nil {
			return nil, err
		}
	}
	return &client.Client{
		BaseURL: s.URL, Token: s.Token, Actor: s.Actor, SessionID: s.SessionID, ClientHost: s.ClientHost,
		Version: a.Version, HTTP: a.HTTP,
	}, nil
}

// emit writes the response: the body verbatim in JSON mode, otherwise
// through render.
func (a *App) emit(g *globals, resp *client.Response, render func(w io.Writer, resp *client.Response) error) error {
	if a.jsonOutput(g) {
		writeJSON(a.Stdout, resp.Body)
		return nil
	}
	return render(a.Stdout, resp)
}

// leafFlags builds a leaf command's flag set with the global flags added.
func leafFlags(name string, g *globals) *flag.FlagSet {
	fs := newFlagSet(name)
	g.addTo(fs)
	return fs
}

// one returns exactly one positional argument, named in errors.
func one(pos []string, name string) (string, error) {
	switch len(pos) {
	case 1:
		return pos[0], nil
	case 0:
		return "", usageErrorf("%s 인자가 필요합니다.", name)
	}
	return "", usageErrorf("인자가 너무 많습니다: %s", strings.Join(pos[1:], " "))
}

// none rejects positional arguments.
func none(pos []string) error {
	if len(pos) > 0 {
		return usageErrorf("인자가 너무 많습니다: %s", strings.Join(pos, " "))
	}
	return nil
}

// readBody resolves --body and --body-file. "-" reads stdin. A UTF-8 BOM
// is dropped. present reports whether either flag was given.
func (a *App) readBody(fs *flag.FlagSet, body, bodyFile string) (text string, present bool, err error) {
	hasBody, hasFile := isSet(fs, "body"), bodyFile != ""
	switch {
	case hasBody && hasFile:
		return "", false, usageErrorf("--body와 --body-file은 함께 쓸 수 없습니다.")
	case hasBody:
		return body, true, nil
	case !hasFile:
		return "", false, nil
	}
	var raw []byte
	if bodyFile == "-" {
		raw, err = io.ReadAll(a.Stdin)
	} else {
		raw, err = os.ReadFile(bodyFile)
	}
	if err != nil {
		return "", false, usageErrorf("본문 파일을 읽지 못했습니다: %v", err)
	}
	return strings.TrimPrefix(string(raw), string(utf8BOM)), true, nil
}

// utf8BOM is the byte order mark some Windows editors put in front of
// UTF-8 files.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// quoteETag turns a bare version into an If-Match value.
func quoteETag(v string) string {
	if v == "" || strings.HasPrefix(v, `"`) {
		return v
	}
	return `"` + v + `"`
}
