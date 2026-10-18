package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

var milestoneCmd = &command{name: "milestone", summary: "마일스톤 관리", subs: []*command{
	milestoneListCmd, milestoneGetCmd, milestoneCreateCmd, milestoneUpdateCmd, milestoneCloseCmd, milestoneReopenCmd, milestoneDeleteCmd,
}}

// milestoneList is the response of the milestone list endpoint.
type milestoneList struct {
	Items []model.Milestone `json:"items"`
}

// parseID reports whether spec is a positive number, which names an id.
func parseID(spec string) (int64, bool) {
	id, err := strconv.ParseInt(spec, 10, 64)
	return id, err == nil && id > 0
}

// milestoneIDArg reads the <ID> argument of the commands that act on one
// milestone.
func milestoneIDArg(pos []string) (int64, error) {
	raw, err := one(pos, "<ID>")
	if err != nil {
		return 0, err
	}
	id, ok := parseID(raw)
	if !ok {
		return 0, usageErrorf("<ID>는 마일스톤 ID(숫자)여야 합니다: %q. agb milestone list -p <KEY> 로 확인하세요.", raw)
	}
	return id, nil
}

// milestoneID resolves a milestone given on the command line to its id. A
// number is an id and needs no lookup. Anything else is a name, looked up
// among all the milestones of project whatever their status; exactly one of
// them must carry the name.
func (a *App) milestoneID(ctx context.Context, c *client.Client, project, spec string) (int64, error) {
	if id, ok := parseID(spec); ok {
		return id, nil
	}
	if project == "" {
		return 0, usageErrorf("이름이 %q인 마일스톤을 찾으려면 프로젝트가 필요합니다. -p <KEY>를 주거나 마일스톤 ID를 쓰세요.", spec)
	}
	resp, err := c.ListMilestones(ctx, project, "all")
	if err != nil {
		return 0, err
	}
	l, err := decode[milestoneList](resp)
	if err != nil {
		return 0, err
	}
	var same []string
	var id int64
	for _, m := range l.Items {
		if m.Name == spec {
			id = m.ID
			same = append(same, fmt.Sprintf("%d(%s)", m.ID, m.Status))
		}
	}
	switch len(same) {
	case 0:
		return 0, usageErrorf("이름이 %q인 마일스톤을 찾지 못했습니다. agb milestone list -p %s --status all 로 확인하세요.", spec, project)
	case 1:
		return id, nil
	}
	return 0, usageErrorf("이름이 %q인 마일스톤이 여럿입니다: ID %s. ID로 지정하세요.", spec, strings.Join(same, ", "))
}

// milestoneFilterValue turns the --milestone values of a list command into
// the filter parameter: ids and none pass through and names are resolved in
// the single project of the filter.
func (a *App) milestoneFilterValue(ctx context.Context, c *client.Client, project string, specs []string) (string, error) {
	if strings.Contains(project, ",") {
		for _, s := range specs {
			if _, ok := parseID(s); !ok && s != "none" {
				return "", usageErrorf("마일스톤 이름은 프로젝트 하나에서만 찾을 수 있습니다. --project 를 하나만 주거나 마일스톤 ID를 쓰세요.")
			}
		}
	}
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		if s == "none" {
			out = append(out, s)
			continue
		}
		id, err := a.milestoneID(ctx, c, project, s)
		if err != nil {
			return "", err
		}
		out = append(out, strconv.FormatInt(id, 10))
	}
	return strings.Join(out, ","), nil
}

// progressText shows the done and counted issues of a milestone with the
// percentage rounded down, so that 100% means every counted issue is done.
// A milestone without counted issues has no percentage.
func progressText(p model.MilestoneProgress) string {
	text := fmt.Sprintf("%d/%d", p.Done, p.Total)
	if p.Total > 0 {
		text += fmt.Sprintf(" (%d%%)", p.Done*100/p.Total)
	}
	return text
}

func (a *App) milestoneTable(items []model.Milestone) *table {
	tb := &table{header: []string{"ID", "STATUS", "TARGET", "PROGRESS", "NAME"}}
	for _, m := range items {
		tb.add(strconv.FormatInt(m.ID, 10), string(m.Status), deref(m.TargetDate, "-"), progressText(m.Progress), m.Name)
	}
	return tb
}

func (a *App) renderMilestoneList(w io.Writer, resp *client.Response) error {
	l, err := decode[milestoneList](resp)
	if err != nil {
		return err
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(w, "마일스톤이 없습니다.")
		return nil
	}
	a.milestoneTable(l.Items).write(w, a.TermWidth)
	return nil
}

func (a *App) renderMilestone(w io.Writer, resp *client.Response) error {
	m, err := decode[model.Milestone](resp)
	if err != nil {
		return err
	}
	fields(w, [][2]string{
		{"id", strconv.FormatInt(m.ID, 10)},
		{"project", m.Project},
		{"name", m.Name},
		{"status", string(m.Status)},
		{"target_date", deref(m.TargetDate, "-")},
		{"progress", progressText(m.Progress)},
		{"closed", formatTimePtr(m.ClosedAt, a.Location)},
		{"created", formatTime(m.CreatedAt, a.Location) + " (" + m.CreatedBy + ")"},
		{"updated", formatTime(m.UpdatedAt, a.Location) + " (" + m.UpdatedBy + ")"},
	})
	if strings.TrimSpace(m.Description) != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, strings.TrimRight(m.Description, "\n"))
	}
	return nil
}

var milestoneListCmd = &command{
	name: "list", summary: "마일스톤 목록 (목표일 순, 진행 포함)",
	usage: `사용법: agb milestone list --project <KEY> [--status <S>]

목표일이 이른 순으로 보이고 목표일이 없는 마일스톤은 뒤에 옵니다. PROGRESS 는 취소·삭제한 이슈를 뺀
이슈 중 완료한 수와 비율(소수점 이하 버림)입니다.

옵션:
  --project, -p <KEY>     프로젝트 키 (필수)
  --status <S>            open(기본), closed, deleted 를 쉼표로 잇거나 all
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("milestone list", g)
		var project, status string
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.StringVar(&status, "status", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		if project == "" {
			return usageErrorf("--project 가 필요합니다.")
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.ListMilestones(ctx, project, status)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderMilestoneList)
	},
}

var milestoneGetCmd = &command{
	name: "get", summary: "마일스톤 단건",
	usage: `사용법: agb milestone get <ID>
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("milestone get", g), args)
		if err != nil {
			return err
		}
		id, err := milestoneIDArg(pos)
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.GetMilestone(ctx, id)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderMilestone)
	},
}

var milestoneCreateCmd = &command{
	name: "create", summary: "마일스톤 생성",
	usage: `사용법: agb milestone create --project <KEY> <이름> [옵션]

이름은 1~50자이고, 같은 프로젝트의 삭제되지 않은 마일스톤과 겹칠 수 없습니다.
이름에 공백이 있으면 따옴표로 감쌉니다.

옵션:
  --project, -p <KEY>     프로젝트 키 (필수)
  --description <텍스트>  설명. 2000자 이하
  --target-date <날짜>    목표일. YYYY-MM-DD
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("milestone create", g)
		var project, description, targetDate string
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.StringVar(&description, "description", "", "")
		fs.StringVar(&targetDate, "target-date", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		name, err := one(pos, "<이름>")
		if err != nil {
			return err
		}
		if project == "" {
			return usageErrorf("--project 가 필요합니다.")
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.CreateMilestone(ctx, project, name, description, targetDate)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderMilestone)
	},
}

var milestoneUpdateCmd = &command{
	name: "update", summary: "마일스톤 이름·설명·목표일·상태 수정",
	usage: `사용법: agb milestone update <ID> [옵션]

준 옵션만 바꿉니다.

옵션:
  --name <이름>           이름 변경. 1~50자
  --description <텍스트>  설명 변경. 빈 문자열이면 설명을 지움
  --target-date <날짜>    목표일 변경. YYYY-MM-DD, none 이면 목표일을 지움
  --status <S>            open, closed. 같은 변경을 close, reopen 으로도 합니다
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("milestone update", g)
		var name, description, targetDate, status string
		fs.StringVar(&name, "name", "", "")
		fs.StringVar(&description, "description", "", "")
		fs.StringVar(&targetDate, "target-date", "", "")
		fs.StringVar(&status, "status", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		id, err := milestoneIDArg(pos)
		if err != nil {
			return err
		}
		patch := map[string]any{}
		for field, v := range map[string]*string{"name": &name, "description": &description, "status": &status} {
			if isSet(fs, field) {
				patch[field] = *v
			}
		}
		if isSet(fs, "target-date") {
			if targetDate == "none" {
				patch["target_date"] = nil
			} else {
				patch["target_date"] = targetDate
			}
		}
		if len(patch) == 0 {
			return usageErrorf("바꿀 필드가 없습니다. --name, --description, --target-date, --status 중 하나를 주세요.")
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.PatchMilestone(ctx, id, patch)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderMilestone)
	},
}

// milestoneStatusCmd builds a command that sets the status of one milestone.
func milestoneStatusCmd(name, summary, usage, status string) *command {
	return &command{
		name: name, summary: summary, usage: usage,
		run: func(a *App, ctx context.Context, g *globals, args []string) error {
			pos, err := parseArgs(leafFlags("milestone "+name, g), args)
			if err != nil {
				return err
			}
			id, err := milestoneIDArg(pos)
			if err != nil {
				return err
			}
			c, err := a.client(g, true)
			if err != nil {
				return err
			}
			resp, err := c.PatchMilestone(ctx, id, map[string]any{"status": status})
			if err != nil {
				return err
			}
			return a.emit(g, resp, a.renderMilestone)
		},
	}
}

var milestoneCloseCmd = milestoneStatusCmd("close", "마일스톤 닫기", `사용법: agb milestone close <ID>

상태를 closed 로 바꾸고 닫은 시각을 적습니다. 이 마일스톤의 이슈는 그대로 둡니다.
닫은 마일스톤에도 이슈를 붙일 수 있습니다. 다시 열려면 agb milestone reopen <ID>.
`, "closed")

var milestoneReopenCmd = milestoneStatusCmd("reopen", "마일스톤 다시 열기", `사용법: agb milestone reopen <ID>

상태를 open 으로 바꾸고 닫은 시각을 지웁니다. 닫은 마일스톤을 다시 열고, 삭제한 마일스톤도 되살립니다.
삭제한 마일스톤은 같은 이름의 삭제되지 않은 마일스톤이 없을 때만 되살아납니다(있으면 409).
`, "open")

var milestoneDeleteCmd = &command{
	name: "delete", summary: "마일스톤 소프트 삭제",
	usage: `사용법: agb milestone delete <ID>

상태를 deleted 로 바꿉니다. 이 마일스톤을 가진 이슈는 그대로 두지만, 삭제한 마일스톤은 이슈에 새로
붙일 수 없습니다. 되살리려면 agb milestone reopen <ID>.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("milestone delete", g), args)
		if err != nil {
			return err
		}
		id, err := milestoneIDArg(pos)
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.DeleteMilestone(ctx, id)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderMilestone)
	},
}
