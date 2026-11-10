package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abyss-works/agent-board/internal/model"
)

// fillRecordTemplate turns the template of a record kind into a new document:
// it sets the front matter keys that values names, writes title in the first
// "# <...>" heading when there is one, and drops the HTML comments of the
// template, which are guidance for its author. Everything else stays for the
// author to fill. A value that is empty is left as the template has it.
func fillRecordTemplate(tmpl string, values map[string]string, title string) string {
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
		case !headingDone && title != "" && strings.HasPrefix(bare, "# <") && strings.HasSuffix(bare, ">"):
			lines[i] = "# " + title + eol
			headingDone = true
		}
	}
	return model.StripHTMLComments(strings.Join(lines, ""))
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

var docNewCmd = &command{
	name: "new", summary: "서식에서 새 기록 문서 파일 만들기",
	usage: `사용법: agb doc new <종류> --slug <slug> [--title <제목>] [--root <저장소 경로>]

.agb.json 의 new 설정으로 새 기록 문서 파일을 만들고 그 경로를 출력합니다. 서식은 new.template 에서 {kind} 를
종류로 바꾼 경로의 파일이고, 파일은 new.output 폴더에 yyyy-mm-dd-HHMM-<종류>-<slug>.md 로 만듭니다.
종류는 영문 소문자로 시작하고 영문 소문자, 숫자, _ 로만 이루어진 1~32자이고(other 는 쓸 수 없습니다), 서식
파일이 있는 종류만 만들 수 있습니다. 어떤 종류를 쓸지는 저장소의 서식이 정합니다. 서버에 접속하지 않습니다.

- 프런트매터의 date 는 지금 시각(yyyy-mm-dd HH:MM)으로 채웁니다. --title 을 주면 title 과 첫 제목(# <...>)도
  채웁니다. new.values 에 적은 키는 프런트매터의 같은 키를 그 값으로 채웁니다.
- 서식의 HTML 주석(<!-- ... -->)은 지웁니다. 코드 블록 안의 것은 남깁니다.
- 나머지 자리표시자(<...>)는 그대로 둡니다. 모두 채운 뒤 agb doc check 로 확인하세요.
- 같은 이름의 파일이 있으면 만들지 않습니다.

옵션:
  --slug <slug>           파일 이름의 slug. 영문 소문자, 숫자, 하이픈 (필수)
  --title <제목>          문서 제목
  --root <경로>           저장소 경로. 기본 현재 폴더의 git 최상위 폴더
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
			return usageErrorf("종류는 %s여야 하며 other 는 쓸 수 없습니다: %s", model.DocumentKindFormat, kindArg)
		}
		if slug == "" {
			return usageErrorf("--slug 가 필요합니다.")
		}
		now := a.Now().In(a.Location)
		name := now.Format("2006-01-02-1504") + "-" + kindArg + "-" + slug + ".md"
		if _, ok := model.ParseDocumentName(name); !ok {
			return usageErrorf("slug 는 영문 소문자, 숫자, 하이픈이어야 합니다: %s", slug)
		}
		if root, err = repoRoot(root); err != nil {
			return err
		}
		cfg, err := requireRepoConfig(root)
		if err != nil {
			return err
		}
		if cfg.New == nil {
			return usageErrorf("%s 에 new 설정이 없습니다. new.template 과 new.output 을 적으세요.", repoConfigFile)
		}
		tmplPath := filepath.Join(root, filepath.FromSlash(strings.ReplaceAll(cfg.New.Template, "{kind}", kindArg)))
		tmpl, err := os.ReadFile(tmplPath)
		if errors.Is(err, os.ErrNotExist) {
			return usageErrorf("종류 %s 의 서식이 없습니다: %s", kindArg, tmplPath)
		}
		if err != nil {
			return usageErrorf("서식을 읽지 못했습니다: %s", tmplPath)
		}
		values := map[string]string{}
		for k, v := range cfg.New.Values {
			values[k] = v
		}
		values["date"] = now.Format("2006-01-02 15:04")
		if title != "" {
			values["title"] = title
		}
		doc := fillRecordTemplate(string(tmpl), values, title)
		path := filepath.Join(root, filepath.FromSlash(cfg.New.Output), name)
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
