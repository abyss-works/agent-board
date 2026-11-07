package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

// fileRepo finds the repository of a file: the nearest folder above it that
// holds a .agb.json, else the top level of the git repository it lies in. root
// is empty and cfg nil when there is neither. cfg is nil for a repository
// without a .agb.json.
func fileRepo(dir string) (root string, cfg *repoConfig, err error) {
	for d := dir; ; d = filepath.Dir(d) {
		if _, statErr := os.Stat(filepath.Join(d, repoConfigFile)); statErr == nil {
			cfg, err = loadRepoConfig(d)
			return d, cfg, err
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	if top, gitErr := git(dir, "rev-parse", "--show-toplevel"); gitErr == nil {
		return filepath.FromSlash(top), nil, nil
	}
	return "", nil, nil
}

var docPutCmd = &command{
	name: "put", summary: "기록 문서 파일 하나를 서버에 등록",
	usage: `사용법: agb doc put <파일> [--project <KEY>] [메타데이터 옵션]

파일 하나를 기록 문서로 서버에 등록합니다. 파일은 UTF-8 이어야 하고 읽은 바이트 그대로 올라갑니다.
메타데이터는 원문을 고치지 않고 옵션으로 따로 넘깁니다. 옵션이 없는 필드는 원문 프런트매터가, 그것도 없으면
서버의 대체값이 정합니다.

파일이 있는 저장소의 .agb.json 에 규칙이 맞으면 그 규칙의 이름 방식과 기본 값(kind, archived, imported)을
쓰고, 옵션이 있으면 옵션이 이깁니다. 규칙이 없으면 이름은 파일 이름이고, --imported 이면 git 저장소 최상위에서
본 상대 경로입니다.

- 처음 올리면 등록됩니다(201).
- 같은 내용과 같은 메타데이터로 다시 올리면 바뀌는 것이 없습니다(200).
- 같은 이름에 다른 내용이면 서버가 409(immutable)로 거절합니다. 기록 문서는 고칠 수 없습니다.
- 같은 내용에 다른 메타데이터이면 409(metadata_differs)입니다. 아카이브는 agb doc archive, 관계는
  agb doc link 로 고칩니다.
- 서버의 경고(가져온 문서의 풀리지 않는 참조 등)는 표준 오류에 출력합니다.

follow_up_headings 에 적은 제목의 ## 절이 있는 새 문서는 그 항목을 표준 오류에 알립니다.

옵션:
  --project, -p <KEY>     프로젝트 키. 없으면 .agb.json 의 project
  --kind <종류>           decision, incident, report, implementation, other(가져온 문서만)
  --date <날짜>           yyyy-mm-dd 또는 "yyyy-mm-dd HH:MM"
  --title <제목>
  --tag <태그>            반복 가능. 원문 프런트매터의 tags 를 통째로 대신합니다
  --supersedes <이름>     이 문서가 통째로 대체하는 문서. 반복 가능. 일부만 대체하면 --related
  --related <이름>        관련 문서. 반복 가능
  --issue <이슈 키>       이 문서가 다루는 이슈. 반복 가능
  --archived              아카이브로 올림 (--archived=false 로 규칙의 값을 뒤집음)
  --imported              가져온 문서로 올림 (--imported=false 로 규칙의 값을 뒤집음)
  --path <상대 경로>      정보용 경로. 판단에는 쓰이지 않음
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("doc put", g)
		var project, kind, date, title, pathArg string
		var tags, supersedes, related, issues stringList
		var archived, imported bool
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.StringVar(&kind, "kind", "", "")
		fs.StringVar(&date, "date", "", "")
		fs.StringVar(&title, "title", "", "")
		fs.StringVar(&pathArg, "path", "", "")
		fs.Var(&tags, "tag", "")
		fs.Var(&supersedes, "supersedes", "")
		fs.Var(&related, "related", "")
		fs.Var(&issues, "issue", "")
		fs.BoolVar(&archived, "archived", false, "")
		fs.BoolVar(&imported, "imported", false, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		file, err := one(pos, "<파일>")
		if err != nil {
			return err
		}
		abs, err := filepath.Abs(file)
		if err != nil {
			return usageErrorf("경로를 해석하지 못했습니다: %v", err)
		}
		content, err := readDocContent(abs)
		if err != nil {
			return usageErrorf("파일을 읽지 못했습니다: %v", err)
		}
		root, cfg, err := fileRepo(filepath.Dir(abs))
		if err != nil {
			return usageErrorf("%v", err)
		}
		var rel string
		var rule *docRule
		if root != "" {
			if r, err := filepath.Rel(root, abs); err == nil {
				rel = filepath.ToSlash(r)
			}
		}
		if cfg != nil && rel != "" {
			rule = cfg.ruleFor(rel)
		}
		if project == "" && cfg != nil {
			project = cfg.Project
		}
		if project == "" {
			return usageErrorf("--project 가 필요합니다. 저장소에 %s 를 만들면(agb init -p <KEY>) 생략할 수 있습니다.", repoConfigFile)
		}

		p := ruleArgs(rule)
		isImported := rule != nil && rule.Imported
		if isSet(fs, "imported") {
			isImported = imported
			p.Imported = &imported
		}
		if isSet(fs, "archived") {
			p.Archived = &archived
		}
		name := filepath.Base(abs)
		switch {
		case rule != nil:
			name = rule.docName(rel)
		case isImported:
			if rel == "" {
				return usageErrorf("가져온 문서의 이름은 git 저장소 최상위에서 본 상대 경로인데 %s 는 git 저장소 안에 있지 않습니다.", file)
			}
			name = rel
		}
		for flagName, dst := range map[string]**string{"kind": &p.Kind, "date": &p.Date, "title": &p.Title, "path": &p.Path} {
			if isSet(fs, flagName) {
				v := map[string]string{"kind": kind, "date": date, "title": title, "path": pathArg}[flagName]
				*dst = &v
			}
		}
		p.Tags, p.Supersedes, p.Related, p.Issues = tags, supersedes, related, issues
		p.Content = content

		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.PutDocumentWith(ctx, project, name, p)
		if err != nil {
			return err
		}
		if err := a.emit(g, resp, a.renderDocument); err != nil {
			return err
		}
		if put, err := decode[model.DocumentResult](resp); err == nil {
			for _, w := range put.Warnings {
				fmt.Fprintf(a.Stderr, "경고: %s: %s\n", w.Field, w.Message)
			}
		}
		if fu := followUps(content, cfgHeadings(cfg)); !isImported && resp.Status == http.StatusCreated && len(fu) > 0 {
			fmt.Fprintln(a.Stderr, "후속 항목이 있습니다. 이슈로 추적할 것은 등록하고 참조 doc:<문서 이름>을 붙이세요.")
			writeFollowUps(a.Stderr, name, fu)
		}
		return nil
	},
}

// ruleArgs is the put arguments the rule gives; nothing for no rule.
func ruleArgs(r *docRule) (p client.DocumentPut) {
	if r == nil {
		return p
	}
	return r.putArgs()
}

func cfgHeadings(cfg *repoConfig) []string {
	if cfg == nil {
		return nil
	}
	return cfg.FollowUpHeadings
}
