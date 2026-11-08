// Package cli implements the agb client commands. They talk to the server
// over HTTP only and carry no business rules.
package cli

import (
	"bytes"
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
	note    string   // lines added to the help of a group, for pages of help it points to
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
	b.WriteString(c.note)
	return b.String()
}

// Usage is the top-level help, including the server commands handled by
// the main package.
const Usage = `사용법: agb <명령> [옵션]

서버:
  serve             API 서버 실행 (AGB_* 환경변수로 설정)
  version           바이너리 버전 출력 (--version 도 같음)

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
  doc               기록 문서 작성·검사·등록·동기화·상태 수정·검색·조회
  msg               프로젝트별 짧게 사는 메시지 넣기·목록·조회·지우기
  init              저장소 최상위에 .agb.json 만들기 (agb 를 지식 저장소로 씀)

조회:
  agenda            프로젝트별 사용자 차례, 에이전트 할 일, 대기 (별칭 next)
  digest            시점 이후 프로젝트별 변경 요약 (--since 3d)
  activity          전체 활동 기록
  board             상태별 보드
  queue             우선순위 큐
  labels            라벨과 사용 개수
  search <검색어>   이슈 키워드 검색: 모든 단어 포함(AND), 접두 일치 (issue list -q 와 같음)
  find <검색어>     이슈·댓글·기록 문서 통합 검색: 키워드 + 의미 검색(서버가 켠 경우), 관련도 순
  graph <노드>      이슈·기록 문서에 연결된 이슈와 문서
  recall <질문>     find 와 같은 검색으로 관련 결정·사고·이슈를 찾고 그 연결을 붙임

주제 (agb help <주제>):
  repo-config       저장소 설정 .agb.json 의 키, 규칙, 예시
  doc-format        기록 문서의 형식, 메타데이터, 상태, 참조 표기, 오류와 대처
  migrate-v0.8      v0.8 에서 v0.9 로 옮겨 오는 절차

공통 옵션 (명령 앞뒤 어디에나):
  --json            JSON 출력 (서버 응답 그대로). 기본: 파이프·파일일 때
  --fields <a,b>    이 필드만 남김. 점 경로(files.name)로 중첩 값을 고름. 목록은 항목마다. JSON 출력을 강제
  --text            --fields 와 함께. 항목마다 한 줄, 필드는 탭으로 구분 (아래 "--fields 와 --text")
  --table           표 출력. 기본: 터미널일 때
  --url <주소>      서버 주소 (AGB_URL, 설정 파일보다 우선)
  --actor <ID>      행위자 (AGB_ACTOR)
  --session-id <ID> 세션 ID (AGB_SESSION_ID). 기본 cli-YYYYMMDD
  --client-host <H> 기기 이름 (AGB_CLIENT_HOST). 기본 호스트 이름
  --config <경로>   설정 파일 경로

--fields 와 --text (JSON 을 스크립트로 가공하지 않고 옵션으로 고릅니다):
  --fields a,b      JSON 객체에서 이 키만 남깁니다. 없는 키는 건너뜁니다. 목록 응답(items)은 항목마다 남기고 total 등은 그대로 둡니다.
                    a.b 는 중첩 객체 a 의 b 입니다. 배열을 만나면 원소마다 적용합니다. files.name 은 {"files":[{"name":..}]} 형태로 남깁니다.
                    목록 응답의 경로는 항목 기준이라 items. 를 붙이지 않습니다. 필드 이름이 하나도 없으면(예: --fields ,) 사용법 오류입니다.
  --text            JSON 대신 텍스트로 출력합니다. --fields 가 필요합니다 (없으면 사용법 오류). --table 과 함께 주면 --text 가 이깁니다.
                    줄 하나가 행 하나, 필드는 탭으로 구분, 머리글은 없습니다.
                    행: 목록 응답은 items 의 항목, 그 밖에는 모든 경로가 같은 배열 a 로 시작하면(a.x,a.y) a 의 원소, 아니면 응답 전체 한 줄.
                    키를 여러 개 준 명령(issue get K1 K2)은 응답마다 한 행씩 한 표로 이어 붙입니다.
                    목록 응답의 행은 items 의 항목이라 total 같은 최상위 키는 텍스트에서 읽을 수 없습니다. JSON 으로 --fields total 을 씁니다.
                    값: 문자열은 그대로, 숫자·true·false 는 JSON 글자 그대로, null 과 없는 필드는 빈 칸(열이 밀리지 않음),
                    배열·객체는 한 줄 JSON. 경로가 배열을 지나면 그 칸은 원소별 값의 배열이고, 경로가 없는 원소는 null 로 자리를 지킵니다.
                    같은 배열 아래 배열이 겹치면 텍스트에서는 한 겹으로 펴서 담습니다.
                    행이 하나이고 필드도 하나일 때만 값을 그대로 출력합니다. 문자열은 바꾸지 않고, 배열이면 원소마다 한 줄입니다.
                    원문 출력에서 null 은 빈 줄이고, 없는 필드는 아무것도 출력하지 않습니다.
                    그 밖에는(행이 둘 이상이거나 필드가 둘 이상) 문자열의 역슬래시, 탭, 줄바꿈, 캐리지 리턴을 \\, \t, \n, \r 로 바꿉니다.
  예: agb issue list -p AGB --fields key,status,title --text      키, 상태, 제목을 한 줄씩
      agb issue get AGB-1 --fields body --text                    본문 원문
      agb issue activity AGB-1 --fields kind,actor,at,body --text  활동 행. 댓글은 kind 가 commented 인 행
      agb doc sync --fields uploaded,conflicts,rejected --text    올린 것, 충돌, 거절
      agb doc check --fields files.name,files.problems --text     문제 있는 파일과 문제 (종료 코드는 그대로)
  doc sync, doc check, recall 도 두 옵션을 따르고, 옵션이 없으면 출력은 전과 같습니다.

우선순위: 옵션 > 환경변수 > 설정 파일. 토큰은 옵션으로 받지 않습니다.
토큰 선택: 설정 파일의 행위자별 토큰(agb login) > AGB_TOKEN > 설정 파일의 공용 토큰.
공용 토큰은 사용자 행위자(owner)만 씁니다. 에이전트는 agb login --actor <ID> 로 받은 토큰을 씁니다.
종료 코드: 0 성공, 1 API·네트워크 오류, 2 사용법 오류

agb help <명령> 또는 agb <명령> --help 로 자세한 사용법을, agb help <주제> 로 주제별 명세를 봅니다.
`

var root = &command{name: "agb", subs: []*command{
	configCmd, loginCmd, logoutCmd, skillCmd, healthCmd, whoamiCmd, actorsCmd, projectCmd, topicCmd, milestoneCmd, templateCmd, issueCmd, docCmd, msgCmd, initCmd,
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
		fmt.Fprintf(a.Stderr, "agb: 모르는 명령 %q\n설치된 CLI가 스킬이나 문서보다 오래됐을 수 있습니다. `agb skill check`로 확인하고, 갱신 방법은 references/setup.md 1절을 따르세요.\n%s\n%s", rest[0], root.suggest("agb", rest[0]), Usage)
		return 2
	}
	err := a.dispatch(ctx, g, cmd, "agb "+cmd.name, rest[1:])
	return a.exit(g, err)
}

// helpFor prints the usage of the command named by path, or the page of the
// topic it names.
func (a *App) helpFor(path []string) int {
	if len(path) == 1 && root.find(path[0]) == nil {
		if text := helpTopicText(path[0]); text != "" {
			io.WriteString(a.Stdout, text)
			return 0
		}
	}
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
		a.printMetadataDiffersHint(e)
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
	a.printMetadataDiffersHint(e)
}

// printMetadataDiffersHint tells what to do about a document that is on the
// server with the same content and other metadata: only the archive mark and
// the relations can be changed, each with a command of its own.
func (a *App) printMetadataDiffersHint(e *client.Error) {
	if e.Status == http.StatusConflict && hasReason(e.Details, "metadata_differs") {
		fmt.Fprintln(a.Stderr, "  올린 문서의 아카이브 표시는 agb doc archive|unarchive 로, 관계(supersedes, related, 이슈)는 agb doc link|unlink 로 고칩니다.")
		fmt.Fprintln(a.Stderr, "  종류, 날짜, 제목, 태그, 가져온 문서 여부, 경로는 고칠 수 없으니 서버의 값을 그대로 둡니다.")
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

// emit writes the response: the chosen fields with --fields, the body
// verbatim in JSON mode, otherwise through render.
func (a *App) emit(g *globals, resp *client.Response, render func(w io.Writer, resp *client.Response) error) error {
	if g.fields != "" {
		return a.emitFields(g, resp.Body)
	}
	if a.jsonOutput(g) {
		writeJSON(a.Stdout, resp.Body)
		return nil
	}
	return render(a.Stdout, resp)
}

// emitFields writes the fields --fields names: lines of text with --text,
// otherwise JSON.
func (a *App) emitFields(g *globals, body []byte) error {
	if g.text {
		text, err := textLines(body, g.fields)
		if err != nil {
			return err
		}
		io.WriteString(a.Stdout, text)
		return nil
	}
	picked, err := selectFields(body, g.fields)
	if err != nil {
		return err
	}
	writeJSON(a.Stdout, picked)
	return nil
}

// emitValue is emit for a result the CLI builds itself: v stands for the
// response body.
func (a *App) emitValue(g *globals, v any, render func(w io.Writer)) error {
	if g.fields != "" || a.jsonOutput(g) {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			return err
		}
		if g.fields != "" {
			return a.emitFields(g, buf.Bytes())
		}
		a.Stdout.Write(buf.Bytes())
		return nil
	}
	render(a.Stdout)
	return nil
}

// emitMany writes several responses: a JSON array of the bodies in JSON
// mode, otherwise each rendered in turn with a blank line between. With
// --text the responses are the rows of one table.
func (a *App) emitMany(g *globals, resps []*client.Response, render func(w io.Writer, resp *client.Response) error) error {
	if g.text {
		bodies := make([][]byte, len(resps))
		for n, r := range resps {
			bodies[n] = r.Body
		}
		text, err := textLinesOf(bodies, g.fields)
		if err != nil {
			return err
		}
		io.WriteString(a.Stdout, text)
		return nil
	}
	if g.fields != "" || a.jsonOutput(g) {
		parts := make([]string, len(resps))
		for n, r := range resps {
			body := r.Body
			if g.fields != "" {
				var err error
				if body, err = selectFields(body, g.fields); err != nil {
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
