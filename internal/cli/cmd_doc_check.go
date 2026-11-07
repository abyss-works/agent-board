package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// docCheckResult is the result of a check: the files with problems, and notes
// about checks that were skipped.
type docCheckResult struct {
	Checked int            `json:"checked"`
	Files   []docCheckFile `json:"files"`
	Notes   []string       `json:"notes"`
}

// docCheckFile is a record document file with its problems.
type docCheckFile struct {
	Name     string   `json:"name"`
	Problems []string `json:"problems"`
}

// changedRecordFiles lists the record document files of root that git shows as
// new or modified: the *.md files directly in history/ and archive/ that are
// untracked or differ from HEAD, without the templates (_*). Paths are
// relative to root and use slashes.
func changedRecordFiles(root string) ([]string, error) {
	// Before the first commit every tracked file is new.
	changed := []string{"diff", "--name-only", "--relative", "HEAD", "--", "history", "archive"}
	if _, err := git(root, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		changed = []string{"ls-files", "--cached", "--", "history", "archive"}
	}
	var paths []string
	for _, args := range [][]string{
		{"ls-files", "--others", "--exclude-standard", "--", "history", "archive"},
		changed,
	} {
		out, err := git(root, append([]string{"-c", "core.quotepath=off"}, args...)...)
		if err != nil {
			return nil, fmt.Errorf("git %s 를 실행하지 못했습니다: %w", args[0], err)
		}
		paths = append(paths, strings.Split(out, "\n")...)
	}
	sort.Strings(paths)
	var files []string
	for i, p := range paths {
		dir, name, direct := strings.Cut(p, "/")
		if !direct || strings.Contains(name, "/") || (dir != "history" && dir != "archive") ||
			!strings.HasSuffix(name, ".md") || strings.HasPrefix(name, "_") || (i > 0 && paths[i-1] == p) {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err == nil {
			files = append(files, p)
		}
	}
	return files, nil
}

// checkDocFile checks one file against the template of its kind in root.
// templates caches the templates read, by kind; a kind without a template
// adds a note once.
func checkDocFile(root, path string, templates map[model.DocumentKind]*string, res *docCheckResult) docCheckFile {
	f := docCheckFile{Name: path, Problems: []string{}}
	content, err := readDocContent(path)
	if err != nil {
		reason := err.Error()
		if !errors.Is(err, errNotUTF8) {
			reason = "파일을 읽지 못했습니다."
		}
		f.Problems = append(f.Problems, reason)
		return f
	}
	var tmpl *string
	if kind, ok := model.ParseDocumentName(filepath.Base(path)); ok {
		cached, seen := templates[kind]
		if !seen {
			tmplPath := filepath.Join(root, "history", "_"+string(kind)+".md")
			if raw, err := readDocContent(tmplPath); err == nil {
				cached = &raw
			} else {
				res.Notes = append(res.Notes, fmt.Sprintf("서식 %s 을 읽지 못해 프런트매터 키와 ## 절은 확인하지 않았습니다.", tmplPath))
			}
			templates[kind] = cached
		}
		tmpl = cached
	}
	f.Problems = append(f.Problems, model.CheckRecord(filepath.Base(path), content, tmpl)...)
	return f
}

var docCheckCmd = &command{
	name: "check", summary: "기록 문서 파일이 서식대로 채워졌는지 커밋 전에 검사",
	usage: `사용법: agb doc check [<파일>...] [--root <관리 저장소 경로>]

기록 문서 파일에 서식의 흔적이 남았거나 서식의 형식을 어겼는지 검사합니다. 서버에 접속하지 않습니다.
파일을 주지 않으면 git 이 새 파일(untracked)이나 HEAD 에서 바뀐 파일로 보는 history/ 와 archive/ 의
기록 문서(바로 아래의 *.md, 이름이 _ 로 시작하지 않는 것)를 검사합니다.

파일마다 한꺼번에 알립니다.
- 파일명이 yyyy-mm-dd-HHMM-<종류>-<slug>.md 형식이고 종류가 기록 종류인지
- 닫힌 프런트매터가 있고 kind 가 파일명의 종류와 같은지
- 서식(history/_<종류>.md)의 프런트매터 키가 모두 있고 값이 자리표시자(<...>)가 아닌지
- 서식의 ## 절이 같은 순서로 모두 있는지 (다른 절을 더해도 됩니다)
- 코드 블록 밖에 HTML 주석(<!-- ... -->)이 남지 않았는지
- 코드 블록과 인라인 코드 밖에 <왜 필요했는지> 같은 자리표시자가 남지 않았는지
서식 파일이 없으면 키와 ## 절 검사는 건너뛰고 그렇다고 알립니다.

결과를 파이프에는 JSON(checked, files, notes)으로, 터미널에는 파일 이름과 문제 목록으로 출력합니다.
문제가 있으면 출력한 뒤 종료 코드 1로 끝납니다.

옵션:
  --root <경로>           관리 저장소 경로. 기본 현재 폴더의 git 최상위 폴더
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("doc check", g)
		var root string
		fs.StringVar(&root, "root", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if root, err = managementRoot(root); err != nil {
			return err
		}
		res := &docCheckResult{Files: []docCheckFile{}, Notes: []string{}}
		paths := pos
		if len(paths) == 0 {
			changed, err := changedRecordFiles(root)
			if err != nil {
				return err
			}
			for _, p := range changed {
				paths = append(paths, filepath.Join(root, filepath.FromSlash(p)))
			}
		}
		templates := map[model.DocumentKind]*string{}
		for _, p := range paths {
			f := checkDocFile(root, p, templates, res)
			if rel, err := filepath.Rel(root, p); err == nil && len(pos) == 0 {
				f.Name = filepath.ToSlash(rel)
			}
			res.Checked++
			if len(f.Problems) > 0 {
				res.Files = append(res.Files, f)
			}
		}
		a.printDocCheck(g, res)
		if len(res.Files) > 0 {
			return fmt.Errorf("기록 문서 %d개에 문제가 있습니다.", len(res.Files))
		}
		return nil
	},
}

// printDocCheck writes the result of a check: JSON in a pipe, otherwise the
// files with their problems and a summary.
func (a *App) printDocCheck(g *globals, r *docCheckResult) {
	if a.jsonOutput(g) {
		enc := json.NewEncoder(a.Stdout)
		enc.SetEscapeHTML(false)
		enc.Encode(r)
		return
	}
	for _, f := range r.Files {
		fmt.Fprintln(a.Stdout, f.Name)
		for _, p := range f.Problems {
			fmt.Fprintln(a.Stdout, "  - "+p)
		}
	}
	for _, n := range r.Notes {
		fmt.Fprintln(a.Stdout, "참고: "+n)
	}
	if len(r.Files) == 0 {
		fmt.Fprintf(a.Stdout, "%d개 검사, 문제가 없습니다.\n", r.Checked)
		return
	}
	fmt.Fprintf(a.Stdout, "%d개 검사, %d개에 문제가 있습니다.\n", r.Checked, len(r.Files))
}
