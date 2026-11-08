package cli

import (
	"context"

	"github.com/wsk0715/agent-board/internal/client"
)

var activityCmd = &command{
	name: "activity", summary: "전체 활동 기록",
	usage: `사용법: agb activity [옵션]

옵션:
  --project, -p <P>       쉼표 OR
  --issue <KEY>           쉼표 OR
  --actor <ID>            쉼표 OR
  --session <ID>          세션 ID. 쉼표 OR
  --client-host <H>       쉼표 OR
  --kind <K>              created, updated, commented. 쉼표 OR
  --comment-kind <K>      댓글 종류 decision, finding, lesson. 쉼표 OR
  --field <F>             changes 에 이 필드가 있는 기록만. 상태 변경만 보려면 --field status --kind updated
  --since <RFC3339>, --until <RFC3339>
  --sort <at|-at>         기본 -at (최신 먼저)
  --limit <N>, --offset <N>

주의: --actor 와 --client-host 는 이 명령에서는 필터입니다. 요청 헤더의 행위자는 AGB_ACTOR 나 설정 파일로 정합니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := newFlagSet("activity")
		// --actor and --client-host filter here, so only the other globals
		// are shared.
		fs.BoolVar(&g.json, "json", g.json, "")
		fs.BoolVar(&g.table, "table", g.table, "")
		fs.StringVar(&g.url, "url", g.url, "")
		fs.StringVar(&g.sessionID, "session-id", g.sessionID, "")
		fs.StringVar(&g.config, "config", g.config, "")
		var f client.ActivityFilter
		fs.StringVar(&f.Project, "project", "", "")
		fs.StringVar(&f.Project, "p", "", "")
		fs.StringVar(&f.Issue, "issue", "", "")
		fs.StringVar(&f.Actor, "actor", "", "")
		fs.StringVar(&f.Session, "session", "", "")
		fs.StringVar(&f.ClientHost, "client-host", "", "")
		fs.StringVar(&f.Kind, "kind", "", "")
		fs.StringVar(&f.CommentKind, "comment-kind", "", "")
		fs.StringVar(&f.Field, "field", "", "")
		fs.StringVar(&f.Since, "since", "", "")
		fs.StringVar(&f.Until, "until", "", "")
		fs.StringVar(&f.Sort, "sort", "", "")
		fs.IntVar(&f.Limit, "limit", 0, "")
		fs.IntVar(&f.Offset, "offset", 0, "")
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
		resp, err := c.ListActivity(ctx, f)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderActivityList)
	},
}

var boardCmd = &command{
	name: "board", summary: "상태별 보드",
	usage: `사용법: agb board [옵션]

옵션:
  --project, -p <P>       프로젝트 키. 쉼표 OR. 없으면 전체
  --closed-days <N>       닫힌 열에 최근 N일 안에 닫힌 이슈만. 0 이면 전부. 기본 7
  --limit <N>             열마다 최대 N건. 기본 100, 최대 200
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("board", g)
		p := client.BoardParams{ClosedDays: -1}
		fs.StringVar(&p.Project, "project", "", "")
		fs.StringVar(&p.Project, "p", "", "")
		fs.IntVar(&p.ClosedDays, "closed-days", -1, "")
		fs.IntVar(&p.Limit, "limit", 0, "")
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
		resp, err := c.Board(ctx, p)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderBoard)
	},
}

var queueCmd = &command{
	name: "queue", summary: "우선순위 큐 (첫 항목이 다음 할 일)",
	usage: `사용법: agb queue [옵션]

옵션:
  --project, -p <P>       프로젝트 키. 쉼표 OR
  --status <S>            기본 backlog. 쉼표로 여러 상태를 잇거나 open, closed, all 도 됩니다
  --limit <N>             다음 할 일 하나만 보려면 1
  --offset <N>
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("queue", g)
		var p client.QueueParams
		fs.StringVar(&p.Project, "project", "", "")
		fs.StringVar(&p.Project, "p", "", "")
		fs.StringVar(&p.Status, "status", "", "")
		fs.IntVar(&p.Limit, "limit", 0, "")
		fs.IntVar(&p.Offset, "offset", 0, "")
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
		resp, err := c.Queue(ctx, p)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderQueue)
	},
}

var agendaCmd = &command{
	name: "agenda", summary: "프로젝트별 사용자 차례, 에이전트 할 일, 대기", aliases: []string{"next"},
	usage: `사용법: agb agenda [--project <P>] [--limit <N>]

프로젝트마다 세 묶음을 보입니다.
  사용자 차례   검토, 결정 필요, 미확인, 사용자 할 일(라벨 user-task)
  에이전트      진행 중(점유 세션), 다음 할 일(착수할 수 있는 백로그, 큐 순서)
  대기          막힌 이슈, 외부 대기 이슈(라벨 external)
프로젝트마다 마지막에 오래됨 개수를 한 줄로 보입니다. 30일 넘게 활동(수정, 댓글)이 없는 열린
이슈이며, 목록은 agb issue list --stale 로 봅니다. 표시만 하고 이슈를 닫지 않습니다.

.agb.json 에 auto_sync 를 켠 저장소 안에서 부르면, 커밋한 기록 문서 중 서버에 없는 것을 올리고
그 후속 항목을 표준 오류에 알립니다. 올리지 못하면 경고만 남깁니다.

옵션:
  --project, -p <P>       프로젝트 키. 쉼표로 여럿. 생략하면 활성 프로젝트 전부
  --limit <N>             목록마다 최대 개수. 기본 10, 최대 50
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("agenda", g)
		var project string
		var limit int
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.IntVar(&limit, "limit", 0, "")
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
		resp, err := c.Agenda(ctx, project, limit)
		if err != nil {
			return err
		}
		if err := a.emit(g, resp, a.renderAgenda); err != nil {
			return err
		}
		a.autoSyncRecords(ctx, c)
		return nil
	},
}

var labelsCmd = &command{
	name: "labels", summary: "라벨과 사용 개수 (열린 이슈 기준)",
	usage: `사용법: agb labels [--project <P>]
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("labels", g)
		var project string
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
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
		resp, err := c.Labels(ctx, project)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderLabels)
	},
}
