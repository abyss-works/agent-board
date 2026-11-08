package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

var projectCmd = &command{name: "project", summary: "프로젝트 관리", subs: []*command{
	projectListCmd, projectGetCmd, projectCreateCmd, projectUpdateCmd, projectDeleteCmd, projectOverviewCmd, projectActivityCmd,
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
	name: "overview", summary: "프로젝트 현황", aliases: []string{"status"},
	usage: `사용법: agb project overview <KEY> [--limit <N>]

진행·검토·차단·결정 필요·다음 할 일·미확인·최근 활동을 절마다 limit개까지 보여 줍니다. limit은 1~50, 기본 10.
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

var projectActivityCmd = &command{
	name: "activity", summary: "프로젝트, 주제, 마일스톤의 변경 기록 (최신 먼저)",
	usage: `사용법: agb project activity <KEY> [--resource <R>] [--id <N>] [--limit <N>] [--offset <N>]

프로젝트와 그 주제, 마일스톤을 만들거나 고친 기록을 최신 먼저 보입니다. 기록마다 행위자, 세션,
기기, 시각과 바뀐 필드의 이전 값과 새 값이 있습니다. 삭제 표시, 마일스톤을 닫고 다시 여는 것도 status
의 변경으로 남습니다.

옵션:
  --resource <R>          project, topic, milestone 중 하나만
  --id <N>                주제나 마일스톤의 id 하나만 (--resource 와 함께)
  --limit <N>             최대 200. 기본 50
  --offset <N>
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("project activity", g)
		var f client.ResourceActivityFilter
		fs.StringVar(&f.Resource, "resource", "", "")
		fs.Int64Var(&f.ID, "id", 0, "")
		fs.IntVar(&f.Limit, "limit", 0, "")
		fs.IntVar(&f.Offset, "offset", 0, "")
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
		resp, err := c.ProjectActivity(ctx, key, f)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderResourceActivity)
	},
}

func (a *App) renderResourceActivity(w io.Writer, resp *client.Response) error {
	l, err := decode[listOf[model.ResourceActivity]](resp)
	if err != nil {
		return err
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(w, "활동 기록이 없습니다.")
		return nil
	}
	tb := &table{header: []string{"AT", "ACTOR", "KIND", "TARGET", "CHANGES"}}
	for _, it := range l.Items {
		keys := make([]string, 0, len(it.Changes))
		for k := range it.Changes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + ": " + rawString(it.Changes[k].From) + " → " + rawString(it.Changes[k].To)
		}
		tb.add(formatTime(it.At, a.Location), it.Actor, string(it.Kind), fmt.Sprintf("%s#%d", it.Resource, it.ResourceID), strings.Join(parts, "; "))
	}
	tb.write(w, a.TermWidth)
	footer(w, len(l.Items), l.Total, l.Offset)
	return nil
}
