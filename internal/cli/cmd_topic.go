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

var topicCmd = &command{name: "topic", summary: "대주제·주제 관리", subs: []*command{
	topicListCmd, topicCreateCmd, topicUpdateCmd, topicDeleteCmd,
}}

// topicList is the response of the topic list endpoint.
type topicList struct {
	Items []model.Topic `json:"items"`
}

// topicID resolves a topic given on the command line to its id. A number is
// an id and needs no lookup. Anything else is a path, "대주제" or
// "대주제/주제", looked up among the active topics of project. With
// topLevelOnly only a top-level topic matches.
func (a *App) topicID(ctx context.Context, c *client.Client, project, spec string, topLevelOnly bool) (int64, error) {
	if id, err := strconv.ParseInt(spec, 10, 64); err == nil && id > 0 {
		return id, nil
	}
	if project == "" {
		return 0, usageErrorf("주제 경로 %q를 찾으려면 프로젝트가 필요합니다. -p <KEY>를 주거나 주제 ID를 쓰세요.", spec)
	}
	resp, err := c.ListTopics(ctx, project, "")
	if err != nil {
		return 0, err
	}
	l, err := decode[topicList](resp)
	if err != nil {
		return 0, err
	}
	for _, tp := range l.Items {
		if tp.Path == spec && (!topLevelOnly || tp.Parent == nil) {
			return tp.ID, nil
		}
	}
	if topLevelOnly {
		return 0, usageErrorf("대주제 %q를 찾지 못했습니다. agb topic list -p %s 로 확인하세요. 상위는 대주제만 쓸 수 있습니다.", spec, project)
	}
	return 0, usageErrorf("주제 %q를 찾지 못했습니다. agb topic list -p %s 로 확인하세요.", spec, project)
}

// topicTreeTable lists topics with the topics under a top-level topic
// indented in the NAME column.
func (a *App) topicTreeTable(items []model.Topic) *table {
	tb := &table{header: []string{"ID", "STATUS", "NAME"}}
	for _, tp := range items {
		name := tp.Name
		if tp.Parent != nil {
			name = "  " + name
		}
		tb.add(strconv.FormatInt(tp.ID, 10), string(tp.Status), name)
	}
	return tb
}

func (a *App) renderTopicList(w io.Writer, resp *client.Response) error {
	l, err := decode[topicList](resp)
	if err != nil {
		return err
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(w, "주제가 없습니다.")
		return nil
	}
	a.topicTreeTable(l.Items).write(w, a.TermWidth)
	return nil
}

func (a *App) renderTopic(w io.Writer, resp *client.Response) error {
	tp, err := decode[model.Topic](resp)
	if err != nil {
		return err
	}
	parent := "-"
	if tp.Parent != nil {
		parent = strconv.FormatInt(*tp.Parent, 10)
	}
	fields(w, [][2]string{
		{"id", strconv.FormatInt(tp.ID, 10)},
		{"project", tp.Project},
		{"name", tp.Name},
		{"path", tp.Path},
		{"parent", parent},
		{"status", string(tp.Status)},
		{"created", formatTime(tp.CreatedAt, a.Location) + " (" + tp.CreatedBy + ")"},
		{"updated", formatTime(tp.UpdatedAt, a.Location) + " (" + tp.UpdatedBy + ")"},
	})
	return nil
}

const topicRefUsage = `주제는 ID(숫자)나 경로로 줍니다. 경로는 대주제 "이슈 흐름"이나 주제 "이슈 흐름/트리아지"이고,
프로젝트의 활성 주제 목록(agb topic list)에서 찾습니다.`

var topicListCmd = &command{
	name: "list", summary: "주제 목록 (대주제 아래 주제를 들여써서)",
	usage: `사용법: agb topic list --project <KEY> [--status <S>]

옵션:
  --project, -p <KEY>     프로젝트 키 (필수)
  --status <S>            active(기본), deleted, all
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("topic list", g)
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
		resp, err := c.ListTopics(ctx, project, status)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderTopicList)
	},
}

var topicCreateCmd = &command{
	name: "create", summary: "주제 생성",
	usage: `사용법: agb topic create --project <KEY> <이름> [--parent <대주제>]

--parent 없이 만들면 대주제이고, 대주제를 주면 그 아래 주제입니다. 이름은 1~50자이고 '/'를 넣을 수 없으며,
같은 상위 아래 활성 주제와 겹칠 수 없습니다. 단계는 대주제와 주제 둘뿐입니다.

옵션:
  --project, -p <KEY>     프로젝트 키 (필수)
  --parent <대주제>       상위 대주제의 ID나 이름

` + topicRefUsage + `
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("topic create", g)
		var project, parent string
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.StringVar(&parent, "parent", "", "")
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
		var parentID *int64
		if isSet(fs, "parent") {
			id, err := a.topicID(ctx, c, project, parent, true)
			if err != nil {
				return err
			}
			parentID = &id
		}
		resp, err := c.CreateTopic(ctx, project, name, parentID)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderTopic)
	},
}

var topicUpdateCmd = &command{
	name: "update", summary: "주제 이름·상위·상태 수정",
	usage: `사용법: agb topic update <ID> [옵션]

준 옵션만 바꿉니다.

옵션:
  --name <이름>           이름 변경
  --parent <대주제>       다른 대주제 아래로 옮김. ID나 이름이며, 이름을 쓰면 --project 가 필요합니다
  --no-parent             대주제로 올림
  --status <S>            active, deleted. 삭제한 주제는 active 로 되살립니다
  --project, -p <KEY>     --parent 에 이름을 줄 때의 프로젝트 키

활성 하위 주제가 있는 대주제는 삭제할 수 없고, 하위 주제가 있는 대주제에는 상위를 붙일 수 없습니다.
` + "\n" + topicRefUsage + `
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("topic update", g)
		var project, name, parent, status string
		var noParent bool
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.StringVar(&name, "name", "", "")
		fs.StringVar(&parent, "parent", "", "")
		fs.BoolVar(&noParent, "no-parent", false, "")
		fs.StringVar(&status, "status", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		raw, err := one(pos, "<ID>")
		if err != nil {
			return err
		}
		id, perr := strconv.ParseInt(raw, 10, 64)
		if perr != nil || id < 1 {
			return usageErrorf("<ID>는 주제 ID(숫자)여야 합니다: %q", raw)
		}
		patch := map[string]any{}
		if isSet(fs, "name") {
			patch["name"] = name
		}
		if isSet(fs, "status") {
			patch["status"] = status
		}
		switch {
		case isSet(fs, "parent") && noParent:
			return usageErrorf("--parent 와 --no-parent 는 함께 쓸 수 없습니다.")
		case noParent:
			patch["parent"] = nil
		}
		if len(patch) == 0 && !isSet(fs, "parent") {
			return usageErrorf("바꿀 필드가 없습니다. --name, --parent, --no-parent, --status 중 하나를 주세요.")
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		if isSet(fs, "parent") {
			pid, err := a.topicID(ctx, c, project, parent, true)
			if err != nil {
				return err
			}
			patch["parent"] = pid
		}
		resp, err := c.PatchTopic(ctx, id, patch)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderTopic)
	},
}

var topicDeleteCmd = &command{
	name: "delete", summary: "주제 소프트 삭제",
	usage: `사용법: agb topic delete <ID>

상태를 deleted 로 바꿉니다. 활성 하위 주제가 있는 대주제는 지울 수 없습니다.
이 주제를 가진 이슈는 그대로 둡니다. 되살리려면 agb topic update <ID> --status active.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("topic delete", g), args)
		if err != nil {
			return err
		}
		raw, err := one(pos, "<ID>")
		if err != nil {
			return err
		}
		id, perr := strconv.ParseInt(raw, 10, 64)
		if perr != nil || id < 1 {
			return usageErrorf("<ID>는 주제 ID(숫자)여야 합니다: %q", raw)
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.DeleteTopic(ctx, id)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderTopic)
	},
}

// topicFilterValue turns the --topic values of a list command into the
// filter parameter: ids and none pass through and paths are resolved in the
// single project of the filter.
func (a *App) topicFilterValue(ctx context.Context, c *client.Client, project string, specs []string) (string, error) {
	if strings.Contains(project, ",") {
		for _, s := range specs {
			if _, err := strconv.ParseInt(s, 10, 64); err != nil && s != "none" {
				return "", usageErrorf("주제 경로는 프로젝트 하나에서만 찾을 수 있습니다. --project 를 하나만 주거나 주제 ID를 쓰세요.")
			}
		}
	}
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		if s == "none" {
			out = append(out, s)
			continue
		}
		id, err := a.topicID(ctx, c, project, s, false)
		if err != nil {
			return "", err
		}
		out = append(out, strconv.FormatInt(id, 10))
	}
	return strings.Join(out, ","), nil
}
