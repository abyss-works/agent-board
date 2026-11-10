package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/abyss-works/agent-board/internal/client"
)

// autoSyncRecords uploads the committed documents of the repository that the
// server lacks, when the .agb.json at its top level asks for it with
// auto_sync, and lists their follow-ups on stderr. It runs inside commands
// agents already call, so a failure is a warning and never fails the command.
// A repository without a .agb.json is left alone.
func (a *App) autoSyncRecords(ctx context.Context, c *client.Client) {
	root, err := git(".", "rev-parse", "--show-toplevel")
	if err != nil {
		return
	}
	cfg, err := loadRepoConfig(root)
	if err != nil {
		fmt.Fprintf(a.Stderr, "agb: 경고: 기록 문서를 자동으로 올리지 못했습니다: %v\n", err)
		return
	}
	if cfg == nil || !cfg.AutoSync {
		return
	}
	res := newDocSyncResult()
	err = func() error {
		files, _, err := repoFiles(root, cfg)
		if err != nil {
			return err
		}
		server, err := serverDocs(ctx, c, cfg.Project)
		if err != nil {
			return err
		}
		return syncDocFiles(ctx, c, cfg.Project, files, server, cfg.FollowUpHeadings, res)
	}()
	if n := len(res.Uploaded); n > 0 {
		// The first run in a repository with many documents uploads them all.
		names := strings.Join(res.Uploaded, ", ")
		if n > 10 {
			names = strings.Join(res.Uploaded[:10], ", ") + fmt.Sprintf(" 외 %d건", n-10)
		}
		fmt.Fprintf(a.Stderr, "기록 문서 %d건을 올렸습니다(%s): %s\n", n, cfg.Project, names)
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(a.Stderr, "경고: %s %s: %s\n", w.Name, w.Field, w.Message)
	}
	for _, d := range res.FollowUps {
		fmt.Fprint(a.Stderr, "후속 항목: ")
		writeFollowUps(a.Stderr, d.Name, d.Sections)
	}
	if err != nil {
		fmt.Fprintf(a.Stderr, "agb: 경고: 기록 문서를 자동으로 올리지 못했습니다: %v\n", err)
	}
	if n := len(res.Conflicts) + len(res.Rejected); n > 0 {
		fmt.Fprintf(a.Stderr, "agb: 경고: 기록 문서 %d건이 서버와 충돌하거나 거절되었습니다. agb doc sync 로 확인하세요.\n", n)
	}
}
