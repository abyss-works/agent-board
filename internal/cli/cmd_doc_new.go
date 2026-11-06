package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// templateGuidance matches the guidance comment of a record template, which
// may span lines, with the line break and the one blank line after it.
var templateGuidance = regexp.MustCompile(`(?s)<!-- 파일명:.*?-->(?:\r?\n)?(?:\r?\n)?`)

// projectNameLine finds the project name in a line of the AGENTS.md of a
// management repository, e.g. "- 이름: agent-board".
var projectNameLine = regexp.MustCompile(`(?m)^- 이름:[ \t]*(\S[^\r\n]*?)[ \t]*\r?$`)

// recordFill is what a new record document takes over from the context:
// the time, the title, the author and the project.
type recordFill struct {
	date, title, author, project string
}

// fillRecordTemplate turns the template of a record kind into a new document:
// it sets date, author and project (and title, when there is one) in the front
// matter, writes the title in the first "# <...>" heading and drops the
// guidance comment. Everything else stays for the author to fill. A value that
// is empty is left as the template has it.
func fillRecordTemplate(tmpl string, f recordFill) string {
	values := map[string]string{"date": f.date, "title": f.title, "author": f.author, "project": f.project}
	lines := strings.SplitAfter(strings.TrimPrefix(tmpl, "\xef\xbb\xbf"), "\n")
	if strings.HasPrefix(tmpl, "\xef\xbb\xbf") {
		lines[0] = "\xef\xbb\xbf" + lines[0]
	}
	inFront, headingDone := false, false
	for i, line := range lines {
		bare := strings.TrimRight(line, "\r\n")
		eol := line[len(bare):]
		switch {
		case i == 0 && strings.TrimPrefix(bare, "\xef\xbb\xbf") == "---":
			inFront = true
		case inFront && bare == "---":
			inFront = false
		case inFront:
			key, _, ok := strings.Cut(bare, ":")
			if v := values[key]; ok && v != "" {
				lines[i] = key + ": " + yamlValue(v) + eol
			}
		case !headingDone && f.title != "" && strings.HasPrefix(bare, "# <") && strings.HasSuffix(bare, ">"):
			lines[i] = "# " + f.title + eol
			headingDone = true
		}
	}
	return templateGuidance.ReplaceAllString(strings.Join(lines, ""), "")
}

// yamlValue writes v as a front matter value: as it is when YAML reads it back
// the same, otherwise in double quotes.
func yamlValue(v string) string {
	if v != strings.TrimSpace(v) || strings.ContainsAny(v[:1], "\"'[{&*!|>%@`#") ||
		strings.Contains(v, ": ") || strings.Contains(v, " #") || strings.HasSuffix(v, ":") {
		return `"` + strings.NewReplacer(`\`, `\`, `"`, `\"`).Replace(v) + `"`
	}
	return v
}

// projectName reads the name of the project from the AGENTS.md of root, or ""
// when there is none.
func projectName(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		return ""
	}
	if m := projectNameLine.FindSubmatch(raw); m != nil {
		return string(m[1])
	}
	return ""
}

// managementRoot is root as given, or the top level of the git repository that
// holds the current folder.
func managementRoot(root string) (string, error) {
	if root != "" {
		return root, nil
	}
	top, err := git(".", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", usageErrorf("git 저장소 안이 아닙니다. --root 로 관리 저장소 경로를 주세요.")
	}
	return top, nil
}

var docNewCmd = &command{
	name: "new", summary: "서식에서 새 기록 문서 파일 만들기",
	usage: `사용법: agb doc new <종류> --slug <slug> [--title <제목>] [--root <관리 저장소 경로>]

history/_<종류>.md 서식으로 history/yyyy-mm-dd-HHMM-<종류>-<slug>.md 를 만들고 그 경로를 출력합니다.
종류는 decision, incident, report, implementation 입니다. 서버에 접속하지 않습니다.

- 프런트매터의 date 는 지금 시각(yyyy-mm-dd HH:MM), author 는 쓰는 행위자, project 는 관리 저장소
  AGENTS.md 의 "- 이름:" 값으로 채웁니다. --title 을 주면 title 과 첫 제목(# <...>)도 채웁니다.
- 서식의 안내 주석(<!-- 파일명: ... -->)은 지웁니다.
- 나머지 자리표시자(<...>)는 그대로 둡니다. 모두 채운 뒤 agb doc check 로 확인하세요.
- 같은 이름의 파일이 있으면 만들지 않습니다.

옵션:
  --slug <slug>           파일 이름의 slug. 영문 소문자, 숫자, 하이픈 (필수)
  --title <제목>          문서 제목
  --root <경로>           관리 저장소 경로. 기본 현재 폴더의 git 최상위 폴더
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("doc new", g)
		var slug, title, root string
		fs.StringVar(&slug, "slug", "", "")
		fs.StringVar(&title, "title", "", "")
		fs.StringVar(&root, "root", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		kindArg, err := one(pos, "<종류>")
		if err != nil {
			return err
		}
		if kind, ok := model.ParseDocumentKind(kindArg); !ok || kind == model.DocumentOther {
			return usageErrorf("종류는 decision, incident, report, implementation 중 하나여야 합니다: %s", kindArg)
		}
		if slug == "" {
			return usageErrorf("--slug 가 필요합니다.")
		}
		now := a.Now().In(a.Location)
		name := now.Format("2006-01-02-1504") + "-" + kindArg + "-" + slug + ".md"
		if _, ok := model.ParseDocumentName(name); !ok {
			return usageErrorf("slug 는 영문 소문자, 숫자, 하이픈이어야 합니다: %s", slug)
		}
		if root, err = managementRoot(root); err != nil {
			return err
		}
		tmplPath := filepath.Join(root, "history", "_"+kindArg+".md")
		tmpl, err := os.ReadFile(tmplPath)
		if err != nil {
			return usageErrorf("서식을 읽지 못했습니다: %s", tmplPath)
		}
		doc := fillRecordTemplate(string(tmpl), recordFill{
			date: now.Format("2006-01-02 15:04"), title: title, author: a.requestActor(g), project: projectName(root),
		})
		path := filepath.Join(root, "history", name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			return usageErrorf("이미 있는 파일입니다: %s", path)
		}
		if err != nil {
			return err
		}
		if _, err := f.WriteString(doc); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		_, err = fmt.Fprintln(a.Stdout, path)
		return err
	},
}
