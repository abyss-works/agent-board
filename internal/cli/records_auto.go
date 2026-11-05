package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wsk0715/agent-board/internal/client"
)

// knowledgeLine finds the project key in an AGENTS.md line that names agb as
// the knowledge store, e.g. "- 지식 저장소: agb, 프로젝트 키 `AGB`".
var knowledgeLine = regexp.MustCompile("지식 저장소:\\s*agb,\\s*프로젝트 키\\s*`([A-Z][A-Z0-9]*)`")

// knowledgeProject returns the root of the repository holding the current
// folder and the project key its AGENTS.md gives for agb as the knowledge
// store. ok is false outside such a management repository.
func knowledgeProject() (root, key string, ok bool) {
	root, err := git(".", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", false
	}
	raw, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		return "", "", false
	}
	m := knowledgeLine.FindSubmatch(raw)
	if m == nil {
		return "", "", false
	}
	return root, string(m[1]), true
}

// committedDocFiles keeps the files git tracks without uncommitted changes.
// A record document is uploaded once and never changed, so a draft must not
// reach the server before it is committed.
func committedDocFiles(root string, files []docFile) ([]docFile, error) {
	tracked, err := git(root, "ls-files", "--", "history", "archive")
	if err != nil {
		return nil, err
	}
	dirty, err := git(root, "diff", "--name-only", "HEAD", "--", "history", "archive")
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for _, p := range strings.Split(tracked, "\n") {
		keep[p] = true
	}
	for _, p := range strings.Split(dirty, "\n") {
		delete(keep, p)
	}
	var out []docFile
	for _, f := range files {
		if keep[string(f.location)+"/"+f.name] {
			out = append(out, f)
		}
	}
	return out, nil
}

// autoSyncRecords uploads the committed record documents of the management
// repository that the server lacks and lists their follow-ups on stderr. It
// runs inside commands agents already call, so a failure is a warning and
// never fails the command.
func (a *App) autoSyncRecords(ctx context.Context, c *client.Client) {
	root, project, ok := knowledgeProject()
	if !ok {
		return
	}
	res := &docSyncResult{}
	err := func() error {
		files, _, err := listDocFiles(root)
		if err != nil {
			return err
		}
		if files, err = committedDocFiles(root, files); err != nil {
			return err
		}
		server, err := serverDocs(ctx, c, project)
		if err != nil {
			return err
		}
		return syncDocFiles(ctx, c, project, files, server, res)
	}()
	if done := append(res.Uploaded, res.Moved...); len(done) > 0 {
		fmt.Fprintf(a.Stderr, "기록 문서 %d건을 올렸습니다(%s): %s\n", len(done), project, strings.Join(done, ", "))
	}
	for _, d := range res.FollowUps {
		fmt.Fprint(a.Stderr, "후속 항목: ")
		writeFollowUps(a.Stderr, d.Name, d.Sections)
	}
	if err != nil {
		fmt.Fprintf(a.Stderr, "agb: 경고: 기록 문서를 자동으로 올리지 못했습니다: %v\n", err)
	}
	if n := len(res.Conflicts) + len(res.Rejected); n > 0 {
		fmt.Fprintf(a.Stderr, "agb: 경고: 기록 문서 %d건이 서버와 충돌하거나 거절되었습니다. agb doc sync -p %s 로 확인하세요.\n", n, project)
	}
}
