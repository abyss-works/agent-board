package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

var releaseCmd = &command{name: "release", summary: "릴리즈 전 점검", subs: []*command{
	releaseCheckCmd,
}}

var releaseCheckCmd = &command{
	name: "check", summary: "미검증 항목 점검 (남은 항목이 있으면 종료 코드 1)",
	usage: `사용법: agb release check [옵션]

취소·삭제되지 않은 이슈(backlog, doing, review, done) 가운데 사용자 검증이 필요한 상태(pending)로
남아 있는 항목을 조회합니다.

종료 코드: 0 미검증 항목 없음, 1 미검증 항목이 남아 있거나 API 오류, 2 사용법 오류.

옵션:
  --project, -p <P>       프로젝트 키. 쉼표로 여러 개(OR)
  --milestone <M>         마일스톤의 ID나 이름, none 이면 마일스톤이 없는 이슈. 반복하면 OR.
                          이름은 --project 하나가 필요하고, 삭제된 마일스톤을 포함해 정확히 같은 이름을 찾음
  --limit <N>             최대 200. 기본 200
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("release check", g)
		var project string
		var milestones stringList
		var limit int
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.Var(&milestones, "milestone", "")
		fs.IntVar(&limit, "limit", 200, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		f := client.IssueFilter{
			Project:      project,
			Status:       "backlog,doing,review,done",
			Verification: string(model.VerificationPending),
			Limit:        limit,
		}
		if len(milestones) > 0 {
			if f.Milestone, err = a.milestoneFilterValue(ctx, c, project, milestones); err != nil {
				return err
			}
		}
		resp, err := c.ListIssues(ctx, f)
		if err != nil {
			return err
		}
		if err := a.emit(g, resp, a.renderReleaseCheck); err != nil {
			return err
		}
		l, err := decode[listOf[model.IssueSummary]](resp)
		if err != nil {
			return err
		}
		if l.Total > 0 {
			return errCheckMismatch
		}
		return nil
	},
}

func (a *App) renderReleaseCheck(w io.Writer, resp *client.Response) error {
	l, err := decode[listOf[model.IssueSummary]](resp)
	if err != nil {
		return err
	}
	if l.Total == 0 {
		fmt.Fprintln(w, "미검증 항목이 없습니다. 릴리즈할 수 있습니다.")
		return nil
	}
	tb := &table{header: []string{"KEY", "STATUS", "PRI", "TITLE", "NOTE"}}
	for _, it := range l.Items {
		tb.add(it.Key, string(it.Status), string(it.Priority), it.Title, deref(it.VerificationNote, "-"))
	}
	tb.write(w, a.TermWidth)
	fmt.Fprintf(w, "\n미검증 항목 %d건이 남아 있습니다. 검증 후 다시 확인하세요.\n", l.Total)
	return nil
}
