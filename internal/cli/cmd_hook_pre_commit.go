package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// preCommitReasonMax is how many characters the reason of a denial holds at
// most. hookProblemMax is how many characters of one problem it shows.
const (
	preCommitReasonMax = 4000
	hookProblemMax     = 500
)

var hookPreCommitCmd = &command{
	name: "pre-commit", summary: "git commit 전에 기록 문서를 검사해 문제가 있으면 커밋을 막음",
	usage: `사용법: agb hook pre-commit [--tool <이름>]

도구 호출 전 훅(Claude Code 의 PreToolUse)이 부르는 명령입니다. 표준 입력으로 훅의 JSON 을 받아, Bash 도구가
git commit 을 실행하려는 것이면 그 저장소의 기록 문서를 agb doc check 와 같은 검사로 확인합니다. 문제가 있으면
도구 호출을 거절하는 JSON(hookSpecificOutput.permissionDecision 이 deny)을 표준 출력에 한 줄로 적고, 문제가 없으면
아무것도 출력하지 않습니다. 파일을 고치지 않고 서버에 쓰지 않습니다.

커밋으로 보는 명령: 명령 줄을 &&, ||, ;, |, &, 줄바꿈으로 나눈 명령 가운데 git 으로 시작하고 하위 명령이 commit 인
것이 있을 때입니다. 앞의 환경변수 지정(X=y)과 git 의 -C <경로>, -c <설정>은 건너뜁니다. 따옴표 안의 글,
here-document 의 본문, # 주석은 명령으로 보지 않습니다. git commit-tree, git log --grep commit, echo "git commit",
git commit --dry-run, git commit --help 는 커밋이 아닙니다. 따옴표나 here-document 가 닫히지 않은 명령 줄과 그 밖의
형태는 커밋으로 보지 않습니다.

검사하는 저장소: 커밋이 실행될 폴더가 속한 git 저장소입니다. 폴더는 훅 JSON 의 cwd 이고, 명령 줄의 첫 명령이
cd <경로> 이면 그 경로, git -C <경로> 가 있으면 거기서 본 그 경로입니다. 상대 경로는 cwd 에서 봅니다. 그 밖에
폴더를 바꾸는 것(다른 자리나 다른 형태의 cd, pushd, popd)이 커밋 앞에 있거나 커밋이 괄호나 중괄호 묶음의 안이나
뒤에 있으면 폴더를 정할 수 없으므로 검사하지 않습니다. -C 가 절대 경로이면 그 경로로 정합니다. ~ 로 시작하거나
$ 가 든 경로와, Windows 에서 /c/... 나 /tmp/... 처럼 / 로 시작하는 경로는 풀지 않으므로 검사하지 않습니다. 그
저장소의 최상위에 .agb.json 이 있을 때만 검사합니다.

검사하는 파일: agb doc check 에 파일을 주지 않았을 때와 같습니다. git 이 새 파일(untracked)이나 HEAD 에서 바뀐
파일로 보는 파일 중 .agb.json 의 documents 규칙이 맞고 가져온 문서(imported)가 아닌 *.md 입니다. 스테이징하지
않은 파일도 포함합니다.

거절 사유에는 문제가 있는 파일과 문제(agb doc check 의 문구), 고친 뒤 다시 커밋하라는 안내, agb doc check 안내가
들어갑니다. 사유는 4,000자를 넘지 않고, 넘는 문제는 남은 건수로 적습니다.

옵션:
  --tool <이름>   훅을 부르는 에이전트 도구의 이름 (agb setup <대상> 의 대상 이름. 예: claude)

행위자와 세션 ID 는 agb hook session-start 와 같은 순서로 정합니다. 서버는 문서의 관계(supersedes, related,
issue)를 확인할 때만 조회하고, 조회 전체가 5초 안에 끝나지 않으면 그만둡니다.

커밋을 막는 것은 검사가 문제를 찾았을 때뿐입니다. 표준 입력을 읽지 못하거나, Bash 도구가 아니거나, git 저장소가
아니거나, .agb.json 이 없거나 읽지 못하거나, 검사가 실패하면 아무것도 출력하지 않습니다. 행위자나 토큰이 없거나,
서버에 닿지 않거나, 서버가 오류를 돌려주면 서버 없이 하는 검사의 결과만 씁니다. 모르는 옵션과 남는 인자는
무시합니다. 어떤 경우에도 종료 코드는 0 이고 표준 오류에 쓰지 않습니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) (err error) {
		fs := leafFlags("hook pre-commit", g)
		tool := fs.String("tool", "", "")
		if _, err := parseArgs(fs, args); err == flag.ErrHelp {
			return err
		}
		// A panic would end the process with exit code 2, with which a hook
		// that runs before a tool call blocks the call.
		defer func() { recover() }()
		if out := a.commitDenial(ctx, g, *tool, a.hookInput()); out != "" {
			io.WriteString(a.Stdout, out)
		}
		return nil
	},
}

// commitDenial is the output with which the hook denies the tool call of in:
// one line of JSON when the call is a git commit by the Bash tool in a
// repository whose record documents have problems. It is "" for every other
// call and when anything on the way fails.
func (a *App) commitDenial(ctx context.Context, g *globals, tool string, in hookStdin) string {
	if in.ToolName != "Bash" {
		return ""
	}
	cwd := in.Cwd
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return ""
		}
	}
	dir, ok := gitCommitDir(in.ToolInput.Command, cwd)
	if !ok {
		return ""
	}
	root, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	cfg, err := loadRepoConfig(root)
	if err != nil || cfg == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, hookQueryTimeout)
	defer cancel()
	res, err := a.checkDocs(ctx, a.hookGlobals(g, tool, in), root, cfg, nil)
	if err != nil || len(res.Files) == 0 {
		return ""
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       "deny",
		"permissionDecisionReason": preCommitReason(root, res),
	}}); err != nil {
		return ""
	}
	return b.String()
}

// preCommitReason is the reason of a denial: the repository at root, the files
// of res with their problems, a line each, and what to do. The path, a file
// name and a problem lose their control characters as in hookText. The
// problems that do not fit into preCommitReasonMax characters are counted in a
// line of their own.
func preCommitReason(root string, res *docCheckResult) string {
	const tail = "문제를 고친 뒤 다시 커밋하세요.\n자세히 보려면 그 저장소에서 agb doc check 를 실행하세요."
	rest := 0
	for _, f := range res.Files {
		rest += len(f.Problems)
	}
	head := fmt.Sprintf("저장소 %s 의 기록 문서 %d개에 문제가 있어 커밋을 막았습니다.\n", hookText(root, hookTitleMax), len(res.Files))
	room := preCommitReasonMax - utf8.RuneCountInString(head+fmt.Sprintf("외 문제 %d건\n", rest)+tail)
	var b strings.Builder
	b.WriteString(head)
files:
	for _, f := range res.Files {
		// The name goes in with the first problem of the file that fits.
		name := hookText(f.Name, hookTitleMax) + "\n"
		for _, p := range f.Problems {
			line := name + "  - " + hookText(p, hookProblemMax) + "\n"
			n := utf8.RuneCountInString(line)
			if n > room {
				break files
			}
			b.WriteString(line)
			room -= n
			rest--
			name = ""
		}
	}
	if rest > 0 {
		fmt.Fprintf(&b, "외 문제 %d건\n", rest)
	}
	b.WriteString(tail)
	return b.String()
}

// shellAssignment matches a word that sets an environment variable for the
// command after it.
var shellAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// gitCommitDir reports whether command, a line for a POSIX shell that starts
// in cwd, runs git commit in a folder that the line settles, and that folder.
//
// A commit is a command of the line that is git, after any variable
// assignments, with the subcommand commit after any -C <path> and -c <setting>,
// and without --dry-run, --help and -h.
//
// The folder is settled when the commit has a -C with an absolute path, or
// when nothing before the commit changes the folder, which then is cwd, or the
// path of a cd <path> that is the first command of the line, followed by the
// paths of -C. A cd anywhere else or of another form, pushd, popd, a commit
// after or inside parentheses or braces, and a path that joinDir does not
// resolve leave it open. A line whose quotes or here-documents do not close
// has no commit.
func gitCommitDir(command, cwd string) (string, bool) {
	cmds, ok := shellCommands(command)
	if !ok {
		return "", false
	}
	dir, settled := cwd, true
	for i, c := range cmds {
		words := c.words
		for len(words) > 0 && shellAssignment.MatchString(words[0]) {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		switch words[0] {
		case "cd", "pushd", "popd":
			if i == 0 && !c.grouped && len(c.words) == 2 && words[0] == "cd" && !strings.HasPrefix(words[1], "-") {
				dir, settled = joinDir(dir, words[1])
			} else {
				settled = false
			}
			continue
		case "git":
		default:
			continue
		}
		at, sure := dir, settled && !c.grouped
		words = words[1:]
		for len(words) >= 2 && (words[0] == "-C" || words[0] == "-c") {
			if words[0] == "-C" {
				known := false
				at, known = joinDir(at, words[1])
				sure = known && (sure || filepath.IsAbs(words[1]))
			}
			words = words[2:]
		}
		if at := len(c.words) - len(words); c.inner > 0 && at >= c.inner {
			sure = false
		}
		if !sure || len(words) == 0 || words[0] != "commit" {
			continue
		}
		if slices.ContainsFunc(words[1:], func(w string) bool { return w == "--dry-run" || w == "--help" || w == "-h" }) {
			continue
		}
		return at, true
	}
	return "", false
}

// joinDir is path as seen from base. ok is false for a path that only the
// shell can resolve: one with a ~ at its start, a $ or a backquote, and one
// that starts with a slash but is not absolute for the system, as /c/work and
// /tmp are on Windows.
func joinDir(base, path string) (dir string, ok bool) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), true
	}
	if strings.HasPrefix(path, "~") || strings.ContainsAny(path, "$`") || strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`) {
		return "", false
	}
	return filepath.Join(base, path), true
}

// shellCommand is one command of a shell line: its words, whether it starts
// after a parenthesis or a brace outside quotes, and how many of its words
// come before the first one inside it, 0 when it has none.
type shellCommand struct {
	words   []string
	grouped bool
	inner   int
}

// hereDoc is the end line of a here-document; with dash the tabs before it do
// not count.
type hereDoc struct {
	end  string
	dash bool
}

// shellCommands splits a line for a POSIX shell into its commands, at &&, ||,
// ;, |, & and line ends outside quotes, and each command into its words, with
// the quotes and the escaping backslashes taken off. A comment, from a # at
// the start of a word to the end of the line, and the body of a here-document,
// up to its end line, are left out. Parentheses and braces outside quotes end
// a word and mark the commands that start after them. It expands nothing. ok
// is false when a quote or a here-document does not close.
func shellCommands(line string) (cmds []shellCommand, ok bool) {
	var (
		cur     shellCommand
		word    []rune
		inWord  bool
		quote   rune
		grouped bool
		pending []hereDoc
	)
	begin := func() {
		if !inWord && len(cur.words) == 0 {
			cur.grouped = grouped
		}
		inWord = true
	}
	endWord := func() {
		if inWord {
			cur.words = append(cur.words, string(word))
			word, inWord = nil, false
		}
	}
	endCommand := func() {
		endWord()
		if len(cur.words) > 0 {
			cmds = append(cmds, cur)
		}
		cur = shellCommand{}
	}
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if c == '\n' && len(pending) > 0 {
			// The bodies start on the next line. i moves to the end of the
			// last end line.
			if i, ok = skipHereDocs(rs, i+1, pending); !ok {
				return nil, false
			}
			pending = nil
			if i >= len(rs) {
				break
			}
		}
		if quote != '\'' && c == '<' && i+1 < len(rs) && rs[i+1] == '<' {
			if quote == 0 {
				endWord()
			}
			if i+2 < len(rs) && rs[i+2] == '<' {
				// A here-string has no body.
				i += 2
				continue
			}
			doc, next, found := hereDocStart(rs, i+2)
			if !found {
				return nil, false
			}
			pending = append(pending, doc)
			i = next - 1
			continue
		}
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				word = append(word, c)
			}
		case quote == '"':
			switch {
			case c == '"':
				quote = 0
			case c == '\\' && i+1 < len(rs) && strings.ContainsRune("\"\\$`", rs[i+1]):
				i++
				word = append(word, rs[i])
			default:
				word = append(word, c)
			}
		case c == '\'' || c == '"':
			begin()
			quote = c
		case c == '\\' && i+1 < len(rs):
			i++
			if rs[i] != '\n' {
				begin()
				word = append(word, rs[i])
			}
		case c == '#' && !inWord:
			for i+1 < len(rs) && rs[i+1] != '\n' {
				i++
			}
		case c == '(' || c == ')' || c == '{' || c == '}':
			endWord()
			grouped = true
			if cur.inner == 0 {
				cur.inner = len(cur.words)
			}
		case c == ';' || c == '|' || c == '&' || c == '\n':
			endCommand()
		case c == ' ' || c == '\t' || c == '\r':
			endWord()
		default:
			begin()
			word = append(word, c)
		}
	}
	endCommand()
	return cmds, quote == 0 && len(pending) == 0
}

// hereDocStart reads what follows the << of a here-document at rs[i]: an
// optional -, blanks, and the end word, whose quotes do not count. next is the
// index after the word. found is false when there is no word.
func hereDocStart(rs []rune, i int) (doc hereDoc, next int, found bool) {
	if i < len(rs) && rs[i] == '-' {
		doc.dash = true
		i++
	}
	for i < len(rs) && (rs[i] == ' ' || rs[i] == '\t') {
		i++
	}
	var end []rune
	for ; i < len(rs) && !strings.ContainsRune(" \t\r\n;|&()<>", rs[i]); i++ {
		if !strings.ContainsRune(`'"\`, rs[i]) {
			end = append(end, rs[i])
		}
	}
	doc.end = string(end)
	return doc, i, len(end) > 0
}

// skipHereDocs passes the bodies of the here-documents in docs, which start at
// rs[i], and returns the index of the line end after the last end line, or
// len(rs) when the text stops there. ok is false when an end line is missing.
func skipHereDocs(rs []rune, i int, docs []hereDoc) (next int, ok bool) {
	for _, doc := range docs {
		for found := false; !found; {
			if i > len(rs) {
				return 0, false
			}
			end := i
			for end < len(rs) && rs[end] != '\n' {
				end++
			}
			l := strings.TrimSuffix(string(rs[i:end]), "\r")
			if doc.dash {
				l = strings.TrimLeft(l, "\t")
			}
			found = l == doc.end
			i = end + 1
		}
	}
	return i - 1, true
}
