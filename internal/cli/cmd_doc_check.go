package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wsk0715/agent-board/internal/client"
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

// isTemplateFile reports whether the file name of rel is that of a template,
// which is never a document.
func isTemplateFile(rel string) bool {
	base := rel[strings.LastIndex(rel, "/")+1:]
	return strings.HasPrefix(base, "_") || strings.HasSuffix(base, ".example.md")
}

// repoMarkdown lists the markdown files of the repository at root that git
// knows, tracked or not ignored, that a rule of cfg matches, by path from the
// root. When changedOnly, only those that are new or differ from HEAD stay.
func repoMarkdown(root string, cfg *repoConfig, changedOnly bool) ([]string, error) {
	list := func(args ...string) ([]string, error) {
		out, err := git(root, append([]string{"-c", "core.quotepath=off"}, args...)...)
		if err != nil {
			return nil, fmt.Errorf("git %s 를 실행하지 못했습니다: %w", args[0], err)
		}
		return strings.Split(out, "\x00"), nil
	}
	var paths []string
	if changedOnly {
		untracked, err := list("ls-files", "-z", "--others", "--exclude-standard")
		if err != nil {
			return nil, err
		}
		diff := []string{"diff", "-z", "--name-only", "--relative", "HEAD"}
		if _, err := git(root, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
			// Before the first commit every tracked file is new.
			diff = []string{"ls-files", "-z", "--cached"}
		}
		changed, err := list(diff...)
		if err != nil {
			return nil, err
		}
		paths = append(untracked, changed...)
	} else {
		var err error
		if paths, err = list("ls-files", "-z", "--cached", "--others", "--exclude-standard"); err != nil {
			return nil, err
		}
	}
	sort.Strings(paths)
	var out []string
	for i, rel := range paths {
		if rel == "" || (i > 0 && paths[i-1] == rel) || !strings.HasSuffix(rel, ".md") || isTemplateFile(rel) || cfg.ruleFor(rel) == nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			out = append(out, rel)
		}
	}
	return out, nil
}

// kindTemplate is what the config has for a kind: its template, or the
// problem of a template that the config names and the repository lacks.
type kindTemplate struct {
	tmpl    *string
	problem string
}

// templateReader reads the template of a document kind, once per kind.
type templateReader struct {
	root  string
	cfg   *repoConfig
	cache map[model.DocumentKind]kindTemplate
	res   *docCheckResult
	noted bool
}

func (t *templateReader) of(kind model.DocumentKind) kindTemplate {
	if kt, seen := t.cache[kind]; seen {
		return kt
	}
	var kt kindTemplate
	if t.cfg.New == nil {
		if !t.noted {
			t.res.Notes = append(t.res.Notes, "설정에 new 가 없어 서식을 읽지 못했습니다. 프런트매터 키와 ## 절은 확인하지 않았습니다.")
			t.noted = true
		}
	} else {
		rel := strings.ReplaceAll(t.cfg.New.Template, "{kind}", string(kind))
		path := filepath.Join(t.root, filepath.FromSlash(rel))
		switch raw, err := readDocContent(path); {
		case err == nil:
			kt.tmpl = &raw
		case errors.Is(err, fs.ErrNotExist):
			kt.problem = fmt.Sprintf("종류 %s 의 서식(%s)이 없습니다. 이 저장소는 .agb.json 의 new.template 에 서식이 있는 종류만 씁니다.", kind, rel)
		default:
			t.res.Notes = append(t.res.Notes, fmt.Sprintf("서식 %s 을 읽지 못해 프런트매터 키와 ## 절은 확인하지 않았습니다.", path))
		}
	}
	t.cache[kind] = kt
	return kt
}

// checkDocFile checks one file against the template of its kind.
func checkDocFile(path string, templates *templateReader) (docCheckFile, string) {
	f := docCheckFile{Name: path, Problems: []string{}}
	content, err := readDocContent(path)
	if err != nil {
		reason := err.Error()
		if !errors.Is(err, errNotUTF8) {
			reason = "파일을 읽지 못했습니다."
		}
		f.Problems = append(f.Problems, reason)
		return f, ""
	}
	var kt kindTemplate
	if kind, ok := model.ParseDocumentName(filepath.Base(path)); ok {
		kt = templates.of(kind)
	}
	f.Problems = append(f.Problems, model.CheckRecord(filepath.Base(path), content, kt.tmpl)...)
	if kt.problem != "" {
		f.Problems = append(f.Problems, kt.problem)
	}
	return f, content
}

// refChecker asks the server whether the targets of the relations of a
// document exist, once per target. After an error that is not "not found" it
// stops asking.
type refChecker struct {
	ctx     context.Context
	c       *client.Client
	project string
	local   map[string]bool
	seen    map[string]bool
	err     error
}

func isNotFound(err error) bool {
	var apiErr *client.Error
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound
}

// document reports whether the project has the document: by the name as given,
// else by PROJECT/name for another project.
func (r *refChecker) document(name string) bool {
	if r.local[name] {
		return true
	}
	if v, known := r.seen[name]; known {
		return v
	}
	found := r.lookupDocument(r.project, name)
	if !found && r.err == nil {
		if key, rest, ok := strings.Cut(name, "/"); ok && model.ValidProjectKey(key) && rest != "" {
			found = r.lookupDocument(key, rest)
		}
	}
	r.seen[name] = found
	return found
}

func (r *refChecker) lookupDocument(project, name string) bool {
	if r.err != nil {
		return false
	}
	_, err := r.c.GetDocument(r.ctx, project, name)
	if err != nil && !isNotFound(err) {
		r.err = err
	}
	return err == nil
}

func (r *refChecker) issue(key string) bool {
	id := "issue:" + key
	if v, known := r.seen[id]; known {
		return v
	}
	found := false
	if r.err == nil {
		_, err := r.c.GetIssue(r.ctx, key)
		if err != nil && !isNotFound(err) {
			r.err = err
		}
		found = err == nil
	}
	r.seen[id] = found
	return found
}

// refProblems lists the relations of a document whose targets the server does
// not have.
func (r *refChecker) refProblems(content string) []string {
	meta, _ := model.ParseFrontMatter(content)
	var out []string
	for _, rel := range []struct {
		key   string
		items []string
	}{{"supersedes", meta.Supersedes}, {"related", meta.Related}} {
		for _, name := range rel.items {
			// Placeholders and sentences are the business of the form check.
			if !model.IsDocumentName(name) {
				continue
			}
			if !r.document(name) && r.err == nil {
				out = append(out, fmt.Sprintf("%s 의 문서 %s 가 서버에 없습니다. 대상 문서를 먼저 올려야 합니다.", rel.key, name))
			}
		}
	}
	for _, key := range meta.Issues {
		if _, _, ok := model.ParseIssueKey(key); !ok {
			continue
		}
		if !r.issue(key) && r.err == nil {
			out = append(out, fmt.Sprintf("issue 의 이슈 %s 가 서버에 없습니다.", key))
		}
	}
	return out
}

var docCheckCmd = &command{
	name: "check", summary: "기록 문서 파일이 서식대로 채워졌는지 커밋 전에 검사",
	usage: `사용법: agb doc check [<파일>...] [--root <저장소 경로>]

기록 문서 파일에 서식의 흔적이 남았거나 서버가 거절할 형식인지 커밋 전에 검사합니다. 서버가 검사하지 않는
서식의 흔적(남은 안내 주석, 서식과 다른 절)은 여기서만 잡습니다.
파일을 주지 않으면 git 이 새 파일(untracked)이나 HEAD 에서 바뀐 파일로 보는 파일 중 .agb.json 의
documents 규칙이 맞고 가져온 문서(imported)가 아닌 *.md 를 검사합니다. 이름이 _ 로 시작하는 파일(서식)은
보지 않습니다.

파일마다 한꺼번에 알립니다.
- 파일명이 yyyy-mm-dd-HHMM-<종류>-<slug>.md 형식인지. 종류는 영문 소문자로 시작하고 영문 소문자, 숫자, _ 로만
  이루어진 1~32자입니다 (other 는 가져온 문서 전용)
- .agb.json 에 new 가 있으면 그 종류의 서식(new.template 의 {kind} 를 종류로 바꾼 파일)이 있는지. 저장소가
  쓰는 종류는 서식이 있는 종류입니다
- 닫힌 프런트매터가 있고 kind 가 파일명의 종류와 같으며 title 이 있는지
- 서식(.agb.json 의 new.template)의 프런트매터 키가 모두 있고 값이 자리표시자(<...>)가 아닌지
- 서식의 ## 절이 같은 순서로 모두 있는지 (다른 절을 더해도 됩니다)
- supersedes, related 의 값이 문서 이름 하나씩인지 (문장이면 부분 대체이므로 related 로 씁니다)
- 코드 블록 밖에 HTML 주석(<!-- ... -->)이 남지 않았는지
- 코드 블록과 인라인 코드 밖에 <왜 필요했는지> 같은 자리표시자가 남지 않았는지
- supersedes, related, issue 가 가리키는 문서와 이슈가 서버에 있는지 (저장소에서 아직 올리지 않은 문서는 있는
  것으로 봅니다)
서식을 읽지 못하면 키와 ## 절 검사는 건너뛰고 그렇다고 알립니다. .agb.json 에 new 가 없으면 서식이 없다고
한 번 알립니다. 서버에 접속하지 못하면 참조 검사는
건너뛰고 그렇다고 알립니다.

결과를 파이프에는 JSON(checked, files, notes)으로, 터미널에는 파일 이름과 문제 목록으로 출력합니다.
문제가 있으면 출력한 뒤 종료 코드 1로 끝납니다.
--fields, --text 를 따릅니다. 문제 있는 파일을 한 줄씩 보려면 --fields files.name,files.problems --text 입니다.

옵션:
  --root <경로>           저장소 경로. 기본 현재 폴더의 git 최상위 폴더
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("doc check", g)
		var root string
		fs.StringVar(&root, "root", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if root, err = repoRoot(root); err != nil {
			return err
		}
		cfg, err := requireRepoConfig(root)
		if err != nil {
			return err
		}
		res := &docCheckResult{Files: []docCheckFile{}, Notes: []string{}}
		paths := pos
		if len(paths) == 0 {
			changed, err := repoMarkdown(root, cfg, true)
			if err != nil {
				return err
			}
			for _, rel := range changed {
				if rule := cfg.ruleFor(rel); rule != nil && !rule.Imported {
					paths = append(paths, filepath.Join(root, filepath.FromSlash(rel)))
				}
			}
		}
		templates := &templateReader{root: root, cfg: cfg, cache: map[model.DocumentKind]kindTemplate{}, res: res}
		var refs *refChecker
		var refErr error
		for _, p := range paths {
			f, content := checkDocFile(p, templates)
			if rel, err := filepath.Rel(root, p); err == nil && len(pos) == 0 {
				f.Name = filepath.ToSlash(rel)
			}
			if content != "" && refErr == nil {
				if refs == nil && hasRelations(content) {
					refs, refErr = a.newRefChecker(ctx, g, root, cfg)
				}
				if refs != nil {
					f.Problems = append(f.Problems, refs.refProblems(content)...)
				}
			}
			res.Checked++
			if len(f.Problems) > 0 {
				res.Files = append(res.Files, f)
			}
		}
		switch {
		case refErr != nil:
			res.Notes = append(res.Notes, fmt.Sprintf("서버에 접속하지 못해 참조는 확인하지 않았습니다: %v", refErr))
		case refs != nil && refs.err != nil:
			res.Notes = append(res.Notes, fmt.Sprintf("서버 조회가 실패해 참조 검사를 멈췄습니다: %v", refs.err))
		}
		if err := a.printDocCheck(g, res); err != nil {
			return err
		}
		if len(res.Files) > 0 {
			return fmt.Errorf("기록 문서 %d개에 문제가 있습니다.", len(res.Files))
		}
		return nil
	},
}

// hasRelations reports whether the front matter of content names a document or
// an issue.
func hasRelations(content string) bool {
	meta, _ := model.ParseFrontMatter(content)
	return len(meta.Supersedes)+len(meta.Related)+len(meta.Issues) > 0
}

// newRefChecker connects to the server for a check of the relations. The names
// of the documents the repository holds count as existing.
func (a *App) newRefChecker(ctx context.Context, g *globals, root string, cfg *repoConfig) (*refChecker, error) {
	c, err := a.client(g, true)
	if err != nil {
		return nil, err
	}
	local := map[string]bool{}
	all, err := repoMarkdown(root, cfg, false)
	if err != nil {
		return nil, err
	}
	for _, rel := range all {
		local[cfg.ruleFor(rel).docName(rel)] = true
	}
	return &refChecker{ctx: ctx, c: c, project: cfg.Project, local: local, seen: map[string]bool{}}, nil
}

// printDocCheck writes the result of a check: JSON in a pipe, otherwise the
// files with their problems and a summary.
func (a *App) printDocCheck(g *globals, r *docCheckResult) error {
	return a.emitValue(g, r, func(io.Writer) { a.renderDocCheck(r) })
}

func (a *App) renderDocCheck(r *docCheckResult) {
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
