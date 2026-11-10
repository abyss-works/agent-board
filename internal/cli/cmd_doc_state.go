package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/abyss-works/agent-board/internal/client"
	"github.com/abyss-works/agent-board/internal/model"
)

// printWarnings writes the warnings of the answer to a write of a document.
func (a *App) printWarnings(resp *client.Response) {
	if res, err := decode[model.DocumentResult](resp); err == nil {
		for _, w := range res.Warnings {
			fmt.Fprintf(a.Stderr, "경고: %s: %s\n", w.Field, w.Message)
		}
	}
}

// docTarget reads <PROJECT> <이름> from the positional arguments.
func docTarget(pos []string) (project, name string, err error) {
	switch {
	case len(pos) < 2:
		return "", "", usageErrorf("<PROJECT>와 <이름> 인자가 필요합니다.")
	case len(pos) > 2:
		return "", "", usageErrorf("인자가 너무 많습니다: %s", strings.Join(pos[2:], " "))
	}
	return pos[0], pos[1], nil
}

func archiveCommand(name, summary, usage string, call func(c *client.Client, ctx context.Context, project, doc string) (*client.Response, error)) *command {
	return &command{
		name: name, summary: summary, usage: usage,
		run: func(a *App, ctx context.Context, g *globals, args []string) error {
			fs := leafFlags("doc "+name, g)
			pos, err := parseArgs(fs, args)
			if err != nil {
				return err
			}
			project, doc, err := docTarget(pos)
			if err != nil {
				return err
			}
			c, err := a.client(g, true)
			if err != nil {
				return err
			}
			resp, err := call(c, ctx, project, doc)
			if err != nil {
				return err
			}
			return a.emit(g, resp, a.renderDocument)
		},
	}
}

var docArchiveCmd = archiveCommand("archive", "올린 문서를 아카이브로 표시",
	`사용법: agb doc archive <PROJECT> <이름>

올린 문서에 아카이브 표시를 합니다. 아카이브 문서는 판단 근거가 아닌 배경과 조사 자료이고, 조회에서는
--state archived 나 all 로 넓혀야 나옵니다. 원문은 바뀌지 않습니다. 바뀌면 문서의 활동 기록에 남고(agb doc
activity), 이미 아카이브이면 아무것도 바뀌지 않고 성공합니다.
`, func(c *client.Client, ctx context.Context, project, doc string) (*client.Response, error) {
		return c.ArchiveDocument(ctx, project, doc)
	})

var docUnarchiveCmd = archiveCommand("unarchive", "아카이브 표시를 해제",
	`사용법: agb doc unarchive <PROJECT> <이름>

올린 문서의 아카이브 표시를 해제합니다. 바뀌면 문서의 활동 기록에 남고, 아카이브가 아니면 아무것도
바뀌지 않고 성공합니다.
`, func(c *client.Client, ctx context.Context, project, doc string) (*client.Response, error) {
		return c.UnarchiveDocument(ctx, project, doc)
	})

// relationCommand builds link and unlink, which differ in the call they make.
func relationCommand(name, summary, usage string, call func(c *client.Client, ctx context.Context, project, doc, kind, target string) (*client.Response, error)) *command {
	return &command{
		name: name, summary: summary, usage: usage,
		run: func(a *App, ctx context.Context, g *globals, args []string) error {
			fs := leafFlags("doc "+name, g)
			var supersedes, related, issues stringList
			fs.Var(&supersedes, "supersedes", "")
			fs.Var(&related, "related", "")
			fs.Var(&issues, "issue", "")
			pos, err := parseArgs(fs, args)
			if err != nil {
				return err
			}
			project, doc, err := docTarget(pos)
			if err != nil {
				return err
			}
			type relation struct{ kind, target string }
			var rels []relation
			for _, t := range supersedes {
				rels = append(rels, relation{string(model.RelSupersedes), t})
			}
			for _, t := range related {
				rels = append(rels, relation{string(model.RelRelated), t})
			}
			for _, t := range issues {
				rels = append(rels, relation{string(model.RelAbout), t})
			}
			if len(rels) == 0 {
				return usageErrorf("--supersedes, --related, --issue 중 하나 이상이 필요합니다.")
			}
			c, err := a.client(g, true)
			if err != nil {
				return err
			}
			var resp *client.Response
			for _, r := range rels {
				if resp, err = call(c, ctx, project, doc, r.kind, r.target); err != nil {
					return err
				}
				a.printWarnings(resp)
			}
			return a.emit(g, resp, a.renderDocument)
		},
	}
}

const relationOptions = `
옵션 (하나 이상, 각각 반복 가능):
  --supersedes <이름>     이 문서가 통째로 대체하는 문서. 일부만 대체하면 --related
  --related <이름>        관련 문서
  --issue <이슈 키>       이 문서가 다루는 이슈
`

var docLinkCmd = relationCommand("link", "올린 문서에 관계를 더함",
	`사용법: agb doc link <PROJECT> <이름> [--supersedes <대상>] [--related <대상>] [--issue <이슈 키>]

올린 문서에 관계를 더합니다. 대상은 올릴 때와 같이 풀립니다. 같은 프로젝트의 문서는 이름으로, 다른
프로젝트의 문서는 PROJECT/이름으로 씁니다. 일반 문서에서 풀리지 않는 대상은 서버가 거절합니다
(unresolved). 가져온 문서는 풀리지 않는 대상을 경고와 함께 받고, 대상이 올라오면 풀립니다. 전체 대체를
더하면 대상 문서가 대체됨 상태가 됩니다. 순환은 거절합니다(cycle).

대상마다 요청을 하나씩 보내며, 중간에 거절되면 그 앞의 것은 이미 더해진 것입니다. 바뀌면 문서의 활동
기록에 남고, 이미 있는 관계는 아무것도 바뀌지 않고 성공합니다.
`+relationOptions, func(c *client.Client, ctx context.Context, project, doc, kind, target string) (*client.Response, error) {
		return c.AddDocumentRelation(ctx, project, doc, kind, target)
	})

var docUnlinkCmd = relationCommand("unlink", "올린 문서에서 관계를 뺌",
	`사용법: agb doc unlink <PROJECT> <이름> [--supersedes <대상>] [--related <대상>] [--issue <이슈 키>]

올린 문서에서 관계를 뺍니다. 풀린 관계와 풀리지 않은 관계를 모두 뺍니다. 없는 관계를 빼면 아무것도
바뀌지 않고 성공합니다. 바뀌면 문서의 활동 기록에 남습니다.
`+relationOptions, func(c *client.Client, ctx context.Context, project, doc, kind, target string) (*client.Response, error) {
		return c.RemoveDocumentRelation(ctx, project, doc, kind, target)
	})

func (a *App) renderDocumentActivity(w io.Writer, resp *client.Response) error {
	l, err := decode[listOf[model.DocumentActivity]](resp)
	if err != nil {
		return err
	}
	if len(l.Items) == 0 {
		fmt.Fprintln(w, "활동 기록이 없습니다.")
		return nil
	}
	tb := &table{header: []string{"AT", "ACTOR", "CHANGES"}}
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
		tb.add(formatTime(it.At, a.Location), it.Actor, strings.Join(parts, "; "))
	}
	tb.write(w, a.TermWidth)
	footer(w, len(l.Items), l.Total, l.Offset)
	return nil
}

var docActivityCmd = &command{
	name: "activity", summary: "올린 문서의 아카이브·관계 변경 이력 (최신 먼저)",
	usage: `사용법: agb doc activity <PROJECT> <이름> [--limit <N>] [--offset <N>]

올린 문서의 아카이브 표시와 관계(supersedes, related, 이슈)를 고친 이력을 최신 먼저 보입니다. 등록은
이력에 남지 않고, 누가 등록했는지는 agb doc get 의 created, updated 에 있습니다.

옵션:
  --limit <N>             최대 200. 기본 50
  --offset <N>
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("doc activity", g)
		var p client.Page
		fs.IntVar(&p.Limit, "limit", 0, "")
		fs.IntVar(&p.Offset, "offset", 0, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		project, doc, err := docTarget(pos)
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.DocumentActivity(ctx, project, doc, p)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderDocumentActivity)
	},
}
