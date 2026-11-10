package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abyss-works/agent-board/internal/client"
	"github.com/abyss-works/agent-board/internal/model"
)

// docSyncResult sorts the files of a sync by what happened to them.
type docSyncResult struct {
	Uploaded  []string       `json:"uploaded"`
	Unchanged []string       `json:"unchanged"`
	Conflicts []docConflict  `json:"conflicts"`
	Skipped   []string       `json:"skipped"`
	Rejected  []docRejected  `json:"rejected"`
	Warnings  []docWarning   `json:"warnings"`
	FollowUps []docFollowUps `json:"follow_ups"`
}

func newDocSyncResult() *docSyncResult {
	return &docSyncResult{Uploaded: []string{}, Unchanged: []string{}, Conflicts: []docConflict{}, Skipped: []string{}, Rejected: []docRejected{},
		Warnings: []docWarning{}, FollowUps: []docFollowUps{}}
}

// docFollowUps are the open sections of a document the sync uploaded.
type docFollowUps struct {
	Name     string     `json:"name"`
	Sections []followUp `json:"sections"`
}

// docConflict is a file whose content differs from the document of the same
// name on the server. It is not uploaded.
type docConflict struct {
	Name         string `json:"name"`
	LocalSHA256  string `json:"local_sha256"`
	ServerSHA256 string `json:"server_sha256"`
}

// docRejected is a file the server, or the CLI for a file that is not UTF-8,
// refused with a reason.
type docRejected struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// docWarning is a problem the server noted about a document it accepted.
type docWarning struct {
	Name    string `json:"name"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// docFile is a file of the repository that a rule uploads.
type docFile struct {
	name string // the name of the document on the server
	rel  string // the path from the repository root, with slashes
	path string // the path on disk
	rule *docRule
}

// putArgs are the metadata of a rule, as the arguments of a put. What the rule
// leaves at its default is not sent.
func (r *docRule) putArgs() client.DocumentPut {
	var p client.DocumentPut
	if r.Kind != "" {
		p.Kind = &r.Kind
	}
	if r.Archived {
		t := true
		p.Archived = &t
	}
	if r.Imported {
		t := true
		p.Imported = &t
	}
	return p
}

// repoFiles lists the files of the repository at root that a rule of cfg
// uploads, by document name. Only what is committed and unchanged since is a
// candidate: a document is uploaded once and never changed, so a draft must not
// reach the server. The others come back in skipped, with the reason. Markdown
// is all the server takes, and a template (a file name that starts with _ or ends
// in .example.md) is never a document, so those are left out without a word.
func repoFiles(root string, cfg *repoConfig) (files []docFile, skipped []string, err error) {
	all, err := git(root, "-c", "core.quotepath=off", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, nil, fmt.Errorf("git 저장소의 파일 목록을 읽지 못해 올리지 않았습니다. --root 는 git 저장소여야 합니다: %w", err)
	}
	dirty, err := git(root, "-c", "core.quotepath=off", "diff", "-z", "--name-only", "HEAD")
	if err != nil {
		return nil, nil, fmt.Errorf("커밋 여부를 확인하지 못해 올리지 않았습니다. --root 는 커밋이 있는 git 저장소여야 합니다: %w", err)
	}
	changed := map[string]bool{}
	for _, p := range strings.Split(dirty, "\x00") {
		changed[p] = true
	}
	untracked, err := git(root, "-c", "core.quotepath=off", "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, nil, err
	}
	isNew := map[string]bool{}
	for _, p := range strings.Split(untracked, "\x00") {
		isNew[p] = true
	}

	seen := map[string]bool{}
	byName := map[string]string{}
	for _, rel := range strings.Split(all, "\x00") {
		if rel == "" || seen[rel] || !strings.HasSuffix(rel, ".md") {
			continue
		}
		seen[rel] = true
		base := rel[strings.LastIndex(rel, "/")+1:]
		if strings.HasPrefix(base, "_") || strings.HasSuffix(base, ".example.md") {
			continue
		}
		rule := cfg.ruleFor(rel)
		if rule == nil {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		switch {
		case err != nil:
			// Tracked and deleted from the working tree.
			continue
		case isNew[rel] || changed[rel]:
			skipped = append(skipped, rel+" (커밋하지 않았거나 커밋 뒤 바뀜)")
			continue
		case info.Size() > model.MaxDocumentBytes:
			skipped = append(skipped, rel+" (1MB 초과)")
			continue
		}
		name := rule.docName(rel)
		if first, dup := byName[name]; dup {
			skipped = append(skipped, fmt.Sprintf("%s (이름 %s 이 %s 와 같음)", rel, name, first))
			continue
		}
		byName[name] = rel
		files = append(files, docFile{name: name, rel: rel, path: abs, rule: rule})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files, skipped, nil
}

// serverDocs lists the SHA-256 of every document of the project, by name, page
// by page.
func serverDocs(ctx context.Context, c *client.Client, project string) (map[string]string, error) {
	docs := map[string]string{}
	for offset := 0; ; {
		resp, err := c.ListDocuments(ctx, project, client.Page{Limit: 500, Offset: offset})
		if err != nil {
			return nil, err
		}
		l, err := decode[listOf[model.Document]](resp)
		if err != nil {
			return nil, err
		}
		for _, d := range l.Items {
			docs[d.Name] = d.SHA256
		}
		offset += len(l.Items)
		if len(l.Items) == 0 || offset >= l.Total {
			return docs, nil
		}
	}
}

// syncDocFiles uploads the files the server lacks, in the order they come, and
// sorts every file into res. A refusal that is about one file puts that file
// aside; any other error ends the sync and is returned.
func syncDocFiles(ctx context.Context, c *client.Client, project string, files []docFile, server map[string]string, headings []string, res *docSyncResult) error {
	for _, f := range files {
		content, err := readDocContent(f.path)
		if errors.Is(err, errNotUTF8) {
			res.Rejected = append(res.Rejected, docRejected{Name: f.name, Reason: err.Error()})
			continue
		}
		if err != nil {
			return err
		}
		sha := sha256Hex(content)
		if stored, onServer := server[f.name]; onServer {
			if stored == sha {
				res.Unchanged = append(res.Unchanged, f.name)
			} else {
				res.Conflicts = append(res.Conflicts, docConflict{Name: f.name, LocalSHA256: sha, ServerSHA256: stored})
			}
			continue
		}
		args := f.rule.putArgs()
		args.Content = content
		resp, err := c.PutDocumentWith(ctx, project, f.name, args)
		if err != nil {
			var apiErr *client.Error
			if !errors.As(err, &apiErr) {
				return err
			}
			switch {
			case apiErr.Status == http.StatusConflict && hasReason(apiErr.Details, "immutable"):
				res.Conflicts = append(res.Conflicts, docConflict{Name: f.name, LocalSHA256: sha, ServerSHA256: immutableSHA(apiErr.Details)})
				continue
			case apiErr.Status == http.StatusUnprocessableEntity || apiErr.Status == http.StatusRequestEntityTooLarge:
				res.Rejected = append(res.Rejected, docRejected{Name: f.name, Reason: apiReason(apiErr)})
				continue
			}
			return err
		}
		server[f.name] = sha
		res.Uploaded = append(res.Uploaded, f.name)
		if put, err := decode[model.DocumentResult](resp); err == nil {
			for _, w := range put.Warnings {
				res.Warnings = append(res.Warnings, docWarning{Name: f.name, Field: w.Field, Message: w.Message})
			}
		}
		// The open items of an imported document are history, not work to track now.
		if fu := followUps(content, headings); !f.rule.Imported && len(fu) > 0 {
			res.FollowUps = append(res.FollowUps, docFollowUps{Name: f.name, Sections: fu})
		}
	}
	return nil
}

// immutableSHA is the hash of the stored content that a refusal reports.
func immutableSHA(details []client.Detail) string {
	for _, d := range details {
		if d.SHA256 != "" {
			return d.SHA256
		}
	}
	return ""
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// printDocSync writes the result of a sync: JSON in a pipe, otherwise the
// counts and the names of each category.
func (a *App) printDocSync(g *globals, r *docSyncResult) error {
	return a.emitValue(g, r, func(io.Writer) { a.renderDocSync(r) })
}

func (a *App) renderDocSync(r *docSyncResult) {
	summary := fmt.Sprintf("uploaded %d, unchanged %d, conflicts %d", len(r.Uploaded), len(r.Unchanged), len(r.Conflicts))
	if len(r.Skipped) > 0 {
		summary += fmt.Sprintf(", skipped %d", len(r.Skipped))
	}
	if len(r.Rejected) > 0 {
		summary += fmt.Sprintf(", rejected %d", len(r.Rejected))
	}
	fmt.Fprintln(a.Stdout, summary)
	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(a.Stdout, "\n== %s (%d) ==\n", title, len(lines))
		for _, line := range lines {
			fmt.Fprintln(a.Stdout, line)
		}
	}
	section("uploaded", r.Uploaded)
	section("unchanged", r.Unchanged)
	var conflicts, rejected, warnings []string
	for _, c := range r.Conflicts {
		conflicts = append(conflicts, fmt.Sprintf("%s  local %s  server %s", c.Name, shortSHA(c.LocalSHA256), shortSHA(c.ServerSHA256)))
	}
	for _, x := range r.Rejected {
		rejected = append(rejected, x.Name+"  "+x.Reason)
	}
	for _, w := range r.Warnings {
		warnings = append(warnings, fmt.Sprintf("%s  %s: %s", w.Name, w.Field, w.Message))
	}
	section("conflicts", conflicts)
	section("skipped", r.Skipped)
	section("rejected", rejected)
	section("warnings", warnings)
	if len(r.FollowUps) > 0 {
		fmt.Fprintf(a.Stdout, "\n== follow-ups (%d) ==\n", len(r.FollowUps))
		for _, d := range r.FollowUps {
			writeFollowUps(a.Stdout, d.Name, d.Sections)
		}
	}
}

// writeFollowUps lists the open sections of one document.
func writeFollowUps(w io.Writer, name string, sections []followUp) {
	fmt.Fprintln(w, name)
	for _, s := range sections {
		for _, it := range s.Items {
			fmt.Fprintf(w, "  [%s] %s\n", s.Section, it)
		}
	}
}

// repoRoot is root as given, or the top level of the git repository that holds
// the current folder.
func repoRoot(root string) (string, error) {
	if root != "" {
		return root, nil
	}
	top, err := git(".", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", usageErrorf("git 저장소 안이 아닙니다. --root 로 저장소 경로를 주세요.")
	}
	return top, nil
}

// requireRepoConfig loads the .agb.json of root, which a command that works
// on the files of a repository needs.
func requireRepoConfig(root string) (*repoConfig, error) {
	cfg, err := loadRepoConfig(root)
	if err != nil {
		return nil, usageErrorf("%v", err)
	}
	if cfg == nil {
		return nil, usageErrorf("%s 에 %s 가 없습니다. 저장소 최상위에서 agb init -p <KEY> 로 만드세요.", root, repoConfigFile)
	}
	return cfg, nil
}

var docSyncCmd = &command{
	name: "sync", summary: "저장소의 기록 문서 중 서버에 없는 것을 올림",
	usage: `사용법: agb doc sync [--project <KEY>] [--root <저장소 경로>]

저장소 최상위의 .agb.json 의 documents 규칙에 맞는 *.md 파일 중 서버에 없는 것을 이름순으로 올립니다.
규칙은 위에서부터 처음 맞는 것을 쓰고, 어느 규칙에도 맞지 않는 파일은 보지 않습니다. 규칙이 정한 이름 방식
(base, path)과 기본 값(kind, archived, imported)이 서버에 보내는 이름과 메타데이터가 됩니다. 이름이 _ 로
시작하거나 .example.md 로 끝나는 파일(서식)은 문서가 아니라 보지 않습니다.

커밋했고 그 뒤 바뀌지 않은 파일만 올리며, 나머지는 skipped 로 알립니다. 1MB 를 넘는 파일과 규칙이 같은
이름을 만드는 파일도 skipped 입니다. --root 가 git 저장소가 아니면 아무것도 올리지 않고 실패합니다.

- 서버에 없는 파일은 올립니다(uploaded).
- 서버에 같은 내용이 있으면 그대로 둡니다(unchanged). 서버에 있는 문서의 메타데이터가 규칙과 달라도 고치지
  않습니다. 아카이브와 관계는 agb doc archive, agb doc link 로 고칩니다.
- 서버의 같은 이름 문서와 내용(sha256)이 다르면 올리지 않고 conflicts 로 알립니다. 덮어쓰지 않습니다.
- 서버가 규칙 위반으로 거절한 파일과 UTF-8 이 아닌 파일은 rejected 로 알립니다.
- 서버가 경고한 것(가져온 문서의 풀리지 않는 참조 등)은 warnings 로 알립니다.

follow_up_headings 에 적은 제목의 ## 절이 있는 새 문서(가져온 문서는 뺍니다)는 그 항목을 follow_ups 로
알립니다. 이슈는 만들지 않습니다.

결과를 파이프에는 JSON(uploaded, unchanged, conflicts, skipped, rejected, warnings, follow_ups)으로,
터미널에는 개수와 이름으로 출력합니다. conflicts 나 rejected 가 있으면 출력한 뒤 종료 코드 1로 끝납니다.
--fields, --text 를 따릅니다. 예: --fields uploaded,conflicts,rejected --text (한 줄, 필드는 탭, 값은 한 줄 JSON).

옵션:
  --project, -p <KEY>     프로젝트 키. 없으면 .agb.json 의 project
  --root <경로>           저장소 경로. 기본 현재 폴더의 git 최상위 폴더
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("doc sync", g)
		var project, root string
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.StringVar(&root, "root", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		if root, err = repoRoot(root); err != nil {
			return err
		}
		if _, err := os.Stat(root); err != nil {
			return usageErrorf("저장소 경로를 읽지 못했습니다: %v", err)
		}
		cfg, err := requireRepoConfig(root)
		if err != nil {
			return err
		}
		if project == "" {
			project = cfg.Project
		}
		files, skipped, err := repoFiles(root, cfg)
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		server, err := serverDocs(ctx, c, project)
		if err != nil {
			return err
		}
		res := newDocSyncResult()
		res.Skipped = append(res.Skipped, skipped...)
		syncErr := syncDocFiles(ctx, c, project, files, server, cfg.FollowUpHeadings, res)
		if err := a.printDocSync(g, res); err != nil {
			return err
		}
		if syncErr != nil {
			return syncErr
		}
		if len(res.Conflicts)+len(res.Rejected) > 0 {
			return fmt.Errorf("충돌 %d건, 거절 %d건: 서버의 기록 문서는 고칠 수 없어 올리지 않았습니다. 출력의 conflicts, rejected 를 확인하세요.", len(res.Conflicts), len(res.Rejected))
		}
		return nil
	},
}
