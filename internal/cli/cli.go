// Package cli implements the agb client commands. They talk to the server
// over HTTP only and carry no business rules.
package cli

import (
	"context"
	"encoding/json"
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
	SkillsDir  string // default global skills folder for agb skill install; empty when the home folder is unknown
	HTTP       *http.Client

	Sleep       func(ctx context.Context, d time.Duration) error // waits for d, or until ctx ends with its error; nil waits in real time
	OpenBrowser func(url string) error                           // starts the user's browser on url without waiting for it; nil opens nothing

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
		OpenBrowser:      openBrowser,
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
	if d, err := defaultSkillsDir(); err == nil {
		a.SkillsDir = d
	}
	return a
}

// command is one node of the command tree. Groups have subs; leaves have
// run.
type command struct {
	name    string
	summary string
	usage   string
	aliases []string // other names that run the command, for names agents often guess
	run     func(a *App, ctx context.Context, g *globals, args []string) error
	subs    []*command
}

func (c *command) find(name string) *command {
	for _, s := range c.subs {
		if s.name == name {
			return s
		}
		for _, al := range s.aliases {
			if al == name {
				return s
			}
		}
	}
	return nil
}

// suggest names the commands close to an unknown name: the children of c
// within edit distance 2, and anywhere below c a command with that exact
// name. It returns "" when nothing is close.
func (c *command) suggest(path, name string) string {
	var near []string
	for _, s := range c.subs {
		if editDistance(s.name, name) <= 2 {
			near = append(near, path+" "+s.name)
		}
	}
	if len(near) == 0 {
		var walk func(c *command, path string)
		walk = func(c *command, path string) {
			for _, s := range c.subs {
				if s.name == name {
					near = append(near, path+" "+s.name)
				}
				walk(s, path+" "+s.name)
			}
		}
		walk(c, path)
	}
	if len(near) == 0 {
		return ""
	}
	return "가까운 명령: " + strings.Join(near, ", ") + "\n"
}

// editDistance is the Levenshtein distance between two command names.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
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
  login             기기 승인으로 이 행위자의 토큰 받기 (--actor)
  logout            이 행위자의 토큰 폐기와 로컬 삭제
  skill             내장 에이전트 스킬 출력, 설치, 점검 (~/.agents/skills/tool-agb)

상태:
  health            서버 상태 (인증 없음)
  whoami            현재 행위자
  actors            선언된 행위자 목록

프로젝트와 이슈:
  project           프로젝트 목록·단건·생성·수정·삭제·현황
  topic             대주제·주제 목록·생성·수정·삭제
  milestone         마일스톤 목록·단건·생성·수정·닫기·다시 열기·삭제
  template          이슈 종류별 본문 템플릿 목록·출력
  issue             이슈 목록·단건·생성·수정·삭제·확인·댓글·활동
  doc               기록 문서 작성·검사·등록·동기화·검색·조회

조회:
  agenda            프로젝트별 사용자 차례, 에이전트 할 일, 대기 (별칭 next)
  digest            시점 이후 프로젝트별 변경 요약 (--since 3d)
  activity          전체 활동 기록
  board             상태별 보드
  queue             우선순위 큐
  labels            라벨과 사용 개수
  search <검색어>   전문 검색 (issue list -q 와 같음)
  find <검색어>     이슈·댓글·기록 문서 통합 검색
  graph <노드>      이슈·기록 문서에 연결된 이슈와 문서
  recall <질문>     질문 하나로 관련 결정·사고·이슈와 그 연결

공통 옵션 (명령 앞뒤 어디에나):
  --json            JSON 출력 (서버 응답 그대로). 기본: 파이프·파일일 때
  --fields <a,b>    JSON 출력에서 이 필드만 남김. 목록은 항목마다 남김. JSON 출력을 강제
  --table           표 출력. 기본: 터미널일 때
  --url <주소>      서버 주소 (AGB_URL, 설정 파일보다 우선)
  --actor <ID>      행위자 (AGB_ACTOR)
  --session-id <ID> 세션 ID (AGB_SESSION_ID). 기본 cli-YYYYMMDD
  --client-host <H> 기기 이름 (AGB_CLIENT_HOST). 기본 호스트 이름
  --config <경로>   설정 파일 경로

우선순위: 옵션 > 환경변수 > 설정 파일. 토큰은 옵션으로 받지 않습니다.
토큰 선택: AGB_TOKEN > 설정 파일의 행위자별 토큰(agb login) > 설정 파일의 공용 토큰.
공용 토큰은 사용자 행위자(owner)만 씁니다. 에이전트는 agb login --actor <ID> 로 받은 토큰을 씁니다.
종료 코드: 0 성공, 1 API·네트워크 오류, 2 사용법 오류

agb help <명령> 또는 agb <명령> --help 로 자세한 사용법을 봅니다.
`

var root = &command{name: "agb", subs: []*command{
	configCmd, loginCmd, logoutCmd, skillCmd, healthCmd, whoamiCmd, actorsCmd, projectCmd, topicCmd, milestoneCmd, templateCmd, issueCmd, docCmd,
	agendaCmd, digestCmd, activityCmd, boardCmd, queueCmd, labelsCmd, searchCmd, findCmd, graphCmd, recallCmd,
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
		fmt.Fprintf(a.Stderr, "agb: 모르는 명령 %q\n%s\n%s", rest[0], root.suggest("agb", rest[0]), Usage)
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
			return &usageError{msg: fmt.Sprintf("모르는 명령 %q", args[0]), help: cmd.suggest(path, args[0]) + "\n" + cmd.help(path)}
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
	case errors.Is(err, errCheckMismatch):
		return 1
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
	switch {
	case e.Status == http.StatusPreconditionFailed:
		fmt.Fprintln(a.Stderr, "  다른 쪽에서 먼저 수정했습니다. 다시 조회한 뒤 재시도하세요.")
	case e.Status == http.StatusUnauthorized:
		fmt.Fprintf(a.Stderr, "  `agb login --actor %s`로 다시 로그인하세요.\n", orPlaceholder(a.requestActor(g)))
	case e.Status == http.StatusForbidden && hasReason(e.Details, "actor_mismatch"):
		fmt.Fprintf(a.Stderr, "  이 행위자의 토큰이 없거나 다른 행위자의 토큰입니다. `agb login --actor %s`를 실행하세요.\n", orPlaceholder(a.requestActor(g)))
	}
}

// hasReason reports whether one of the details of a server error has reason.
func hasReason(details []client.Detail, reason string) bool {
	for _, d := range details {
		if d.Reason == reason {
			return true
		}
	}
	return false
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

// requestActor is the actor the run acts as, as far as the flag, the
// environment and the config file tell. It is "" when none of them does.
func (a *App) requestActor(g *globals) string {
	var file Config
	if path, err := a.configPath(g); err == nil {
		file, _ = loadConfig(path)
	}
	return actorOf(g, a.Getenv, file)
}

// settings reads the config file and resolves the settings from it and the
// command line. It returns the file's content too, for the commands that look
// at the tokens in it.
func (a *App) settings(g *globals) (Config, Settings, error) {
	path, err := a.configPath(g)
	if err != nil {
		return Config{}, Settings{}, err
	}
	cfg, err := loadConfig(path)
	if err != nil {
		return Config{}, Settings{}, err
	}
	host, err := a.Hostname()
	if err != nil {
		host = ""
	}
	s, err := resolveSettings(g, a.Getenv, cfg, a.Now(), host)
	return cfg, s, err
}

// client resolves the settings and builds the HTTP client. needAuth
// requires a token and an actor.
func (a *App) client(g *globals, needAuth bool) (*client.Client, error) {
	_, s, err := a.settings(g)
	if err != nil {
		return nil, err
	}
	if needAuth {
		if err := s.requireAuth(); err != nil {
			return nil, err
		}
	}
	return a.newClient(s), nil
}

// newClient builds the HTTP client for the settings.
func (a *App) newClient(s Settings) *client.Client {
	return &client.Client{
		BaseURL: s.URL, Token: s.Token, Actor: s.Actor, SessionID: s.SessionID, ClientHost: s.ClientHost,
		Version: a.Version, HTTP: a.HTTP,
	}
}

// wait pauses for d. When ctx ends first it returns early with the error of ctx.
func (a *App) wait(ctx context.Context, d time.Duration) error {
	if a.Sleep != nil {
		return a.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// emit writes the response: the body verbatim in JSON mode, otherwise
// through render.
func (a *App) emit(g *globals, resp *client.Response, render func(w io.Writer, resp *client.Response) error) error {
	if g.fields != "" {
		body, err := selectFields(resp.Body, strings.Split(g.fields, ","))
		if err != nil {
			return err
		}
		writeJSON(a.Stdout, body)
		return nil
	}
	if a.jsonOutput(g) {
		writeJSON(a.Stdout, resp.Body)
		return nil
	}
	return render(a.Stdout, resp)
}

// emitMany writes several responses: a JSON array of the bodies in JSON
// mode, otherwise each rendered in turn with a blank line between.
func (a *App) emitMany(g *globals, resps []*client.Response, render func(w io.Writer, resp *client.Response) error) error {
	if g.fields != "" || a.jsonOutput(g) {
		parts := make([]string, len(resps))
		for n, r := range resps {
			body := r.Body
			if g.fields != "" {
				var err error
				if body, err = selectFields(body, strings.Split(g.fields, ",")); err != nil {
					return err
				}
			}
			parts[n] = strings.TrimSpace(string(body))
		}
		writeJSON(a.Stdout, []byte("["+strings.Join(parts, ",")+"]"))
		return nil
	}
	for n, r := range resps {
		if n > 0 {
			io.WriteString(a.Stdout, "\n")
		}
		if err := render(a.Stdout, r); err != nil {
			return err
		}
	}
	return nil
}

// selectFields keeps only the named top-level fields of a JSON object, or
// of each object in its items list for list responses, in the given order.
func selectFields(body []byte, names []string) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, fmt.Errorf("--fields 는 JSON 객체 응답에만 씁니다: %w", err)
	}
	pick := func(o map[string]json.RawMessage) []byte {
		var b strings.Builder
		b.WriteString("{")
		n := 0
		for _, name := range names {
			name = strings.TrimSpace(name)
			v, ok := o[name]
			if !ok {
				continue
			}
			if n > 0 {
				b.WriteString(",")
			}
			k, _ := json.Marshal(name)
			b.Write(k)
			b.WriteString(":")
			b.Write(v)
			n++
		}
		b.WriteString("}")
		return []byte(b.String())
	}
	raw, ok := obj["items"]
	if !ok {
		return pick(obj), nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return pick(obj), nil
	}
	picked := make([]json.RawMessage, len(items))
	for n, it := range items {
		picked[n] = pick(it)
	}
	obj["items"], _ = json.Marshal(picked)
	return json.Marshal(obj)
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
