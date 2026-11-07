package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

var graphCmd = &command{
	name: "graph", summary: "이슈·기록 문서의 결정 그래프 (연결된 이슈와 문서)",
	usage: `사용법: agb graph <노드> [--depth <1|2>]

이슈나 기록 문서에 연결된 이슈와 문서를 보입니다. 연결은 상위 이슈(parent), 선행 이슈(depends_on), 참조(ref),
기록 문서 프런트매터의 issue(about), supersedes, related 에서 만듭니다. 서버가 의미 검색을 켜 두었으면 내용이 비슷한
문서끼리 similar 로도 잇습니다. similar 는 누가 적은 연결이 아니라 임베딩으로 추정한 것이라 점수가 붙습니다.
방향과 관계없이 depth 만큼 따라갑니다.

노드:
  AGB-10                  이슈 키 (issue:AGB-10 과 같음)
  AGB/<문서 이름>         기록 문서 (doc:AGB/<문서 이름> 과 같음). 이전 체계 문서는 이름에 / 가 있습니다

출력의 줄마다 기준 노드에서 본 방향(→ 나가는 연결, ← 들어오는 연결)과 간선 종류가 나옵니다. depth 2 의 간선은
기준 노드에 닿지 않아도 나옵니다.

옵션:
  --depth <N>             1(기본) 또는 2
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("graph", g)
		var depth int
		fs.IntVar(&depth, "depth", 0, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		arg, err := one(pos, "<노드>")
		if err != nil {
			return err
		}
		node := graphNode(arg)
		if _, _, _, ok := model.ParseNodeID(node); !ok {
			return usageErrorf("노드는 이슈 키(AGB-10)나 <PROJECT>/<문서 이름> 이어야 합니다: %s", arg)
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.Graph(ctx, node, depth)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderGraph)
	},
}

// graphNode turns the short forms into a node ID: an issue key, or
// PROJECT/name for a document. Full IDs pass through.
func graphNode(arg string) string {
	if strings.HasPrefix(arg, "issue:") || strings.HasPrefix(arg, "doc:") {
		return arg
	}
	if strings.Contains(arg, "/") {
		return "doc:" + arg
	}
	return "issue:" + arg
}

func (a *App) renderGraph(w io.Writer, resp *client.Response) error {
	g, err := decode[model.Graph](resp)
	if err != nil {
		return err
	}
	if len(g.Nodes) == 0 {
		return nil
	}
	nodes := map[string]model.GraphNode{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	root := g.Nodes[0]
	fmt.Fprintf(w, "%s  %s  %s\n", root.ID, root.Kind, deref(root.Title, "-"))
	if len(root.Refs) > 0 {
		fmt.Fprintf(w, "refs: %s\n", strings.Join(root.Refs, ", "))
	}
	if len(g.Edges) == 0 {
		fmt.Fprintln(w, "연결된 이슈나 문서가 없습니다.")
		return nil
	}
	fmt.Fprintln(w)
	tb := &table{header: []string{"DIR", "EDGE", "NODE", "KIND", "STATUS", "TITLE"}}
	for _, e := range g.Edges {
		dir, other := "→", e.To
		switch {
		case e.To == root.ID:
			dir, other = "←", e.From
		case e.From != root.ID:
			dir, other = e.From+" →", e.To
		}
		n := nodes[other]
		tb.add(dir, edgeLabel(e.Type, e.Score), other, n.Kind, deref(n.Status, "-"), deref(n.Title, "-"))
	}
	tb.write(w, a.TermWidth)
	return nil
}

// edgeLabel names an edge; a similar edge carries its score, since it is
// inferred and not written by anyone.
func edgeLabel(t model.GraphEdgeType, score *float64) string {
	if score == nil {
		return string(t)
	}
	return fmt.Sprintf("%s(%.2f)", t, *score)
}
