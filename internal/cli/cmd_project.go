package cli

import (
	"context"

	"github.com/wsk0715/agent-board/internal/client"
)

var projectCmd = &command{name: "project", summary: "프로젝트 관리", subs: []*command{
	projectListCmd, projectGetCmd, projectCreateCmd, projectUpdateCmd, projectDeleteCmd, projectOverviewCmd,
}}

var projectListCmd = &command{
	name: "list", summary: "프로젝트 목록",
	usage: `사용법: agb project list [옵션]

옵션:
  --status <S>   active, archived, deleted 를 쉼표로 잇거나 all. 기본 active
  --sort <F>     key, created_at, updated_at. 앞에 - 를 붙이면 내림차순
  --limit <N>    최대 200. 기본 50
  --offset <N>
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("project list", g)
		var f client.ProjectFilter
		fs.StringVar(&f.Status, "status", "", "")
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
		resp, err := c.ListProjects(ctx, f)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderProjectList)
	},
}

var projectGetCmd = &command{
	name: "get", summary: "프로젝트 단건",
	usage: `사용법: agb project get <KEY>
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("project get", g), args)
		if err != nil {
			return err
		}
		key, err := one(pos, "<KEY>")
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.GetProject(ctx, key)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderProject)
	},
}

var projectCreateCmd = &command{
	name: "create", summary: "프로젝트 생성",
	usage: `사용법: agb project create <KEY> <이름>

KEY는 대문자로 시작하는 대문자·숫자 2~10자이며 이슈 키의 접두어가 됩니다.
이름에 공백이 있으면 따옴표로 감쌉니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("project create", g), args)
		if err != nil {
			return err
		}
		if len(pos) != 2 {
			return usageErrorf("<KEY>와 <이름> 두 인자가 필요합니다.")
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.CreateProject(ctx, pos[0], pos[1])
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderProject)
	},
}

var projectUpdateCmd = &command{
	name: "update", summary: "프로젝트 이름·상태 수정",
	usage: `사용법: agb project update <KEY> [--name <이름>] [--status <상태>]

상태는 active, archived, deleted 중 하나입니다. 삭제된 프로젝트는 --status active 로 되살립니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("project update", g)
		var name, status string
		fs.StringVar(&name, "name", "", "")
		fs.StringVar(&status, "status", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		key, err := one(pos, "<KEY>")
		if err != nil {
			return err
		}
		patch := map[string]any{}
		if isSet(fs, "name") {
			patch["name"] = name
		}
		if isSet(fs, "status") {
			patch["status"] = status
		}
		if len(patch) == 0 {
			return usageErrorf("바꿀 필드가 없습니다. --name 또는 --status 를 주세요.")
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.PatchProject(ctx, key, patch)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderProject)
	},
}

var projectDeleteCmd = &command{
	name: "delete", summary: "프로젝트 소프트 삭제",
	usage: `사용법: agb project delete <KEY>

상태를 deleted 로 바꿉니다. 되살리려면 agb project update <KEY> --status active.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("project delete", g), args)
		if err != nil {
			return err
		}
		key, err := one(pos, "<KEY>")
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.DeleteProject(ctx, key)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderProject)
	},
}

var projectOverviewCmd = &command{
	name: "overview", summary: "프로젝트 현황",
	usage: `사용법: agb project overview <KEY> [--limit <N>]

진행·검토·차단·결정 대기·다음 할 일·최근 활동을 절마다 limit개까지 보여 줍니다. limit은 1~50, 기본 10.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("project overview", g)
		var limit int
		fs.IntVar(&limit, "limit", 0, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		key, err := one(pos, "<KEY>")
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.Overview(ctx, key, limit)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderOverview)
	},
}
