package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

var recallCmd = &command{
	name: "recall", summary: "질문 하나로 관련 결정·사고·이슈와 그 연결을 찾음",
	usage: `사용법: agb recall <질문...> [옵션]

작업 전에 과거 선례를 찾는 명령입니다. 이슈·댓글·기록 문서를 통합 검색(agb find)한 뒤, 상위 결과마다 결정
그래프(agb graph)에서 바로 연결된 이슈와 문서를 붙여 보입니다. 연결은 상위·선행 이슈, 참조, 기록 문서의
issue·supersedes·related, 그리고
의미 검색을 켠 서버에서는 내용이 비슷한 문서(similar, 점수 표시)입니다. 댓글이 찾아지면 그 댓글이 달린 이슈의 연결을 봅니다.

서버가 의미 검색을 켜 두었으면 질문 문장을 그대로 줍니다. 키워드 검색만 하는 서버에서는 모든 단어를 AND 로
찾으므로 핵심어 두세 개를 줍니다.

옵션:
  --project, -p <P>       프로젝트 키. 쉼표로 여러 개(OR). 없으면 모든 프로젝트
  --type <T>              issue, comment, document. 쉼표 OR
  --comment-kind <K>      댓글 종류 decision, finding, lesson. 쉼표 OR
  --limit <N>             연결을 붙일 검색 결과 수. 기본 5, 최대 20
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("recall", g)
		var f client.SearchFilter
		fs.StringVar(&f.Project, "project", "", "")
		fs.StringVar(&f.Project, "p", "", "")
		fs.StringVar(&f.Type, "type", "", "")
		fs.StringVar(&f.CommentKind, "comment-kind", "", "")
		fs.IntVar(&f.Limit, "limit", 5, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return usageErrorf("질문이 필요합니다.")
		}
		if f.Limit < 1 || f.Limit > 20 {
			return usageErrorf("--limit 은 1~20 이어야 합니다.")
		}
		f.Query = strings.Join(pos, " ")
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.Search(ctx, f)
		if err != nil {
			return err
		}
		hits, err := decode[listOf[model.SearchHit]](resp)
		if err != nil {
			return err
		}
		out := recallResult{Query: f.Query, Total: hits.Total, Items: []recallItem{}}
		for _, h := range hits.Items {
			item := recallItem{Hit: h, Links: []recallLink{}}
			node := recallNode(h)
			gr, err := c.Graph(ctx, node, 1)
			if err != nil {
				return err
			}
			graph, err := decode[model.Graph](gr)
			if err != nil {
				return err
			}
			nodes := map[string]model.GraphNode{}
			for _, n := range graph.Nodes {
				nodes[n.ID] = n
			}
			for _, e := range graph.Edges {
				dir, other := "out", e.To
				if e.To == node {
					dir, other = "in", e.From
				}
				item.Links = append(item.Links, recallLink{Edge: e.Type, Score: e.Score, Direction: dir, Node: nodes[other]})
			}
			out.Items = append(out.Items, item)
		}
		if a.jsonOutput(g) {
			enc := json.NewEncoder(a.Stdout)
			enc.SetEscapeHTML(false)
			return enc.Encode(out)
		}
		a.renderRecall(a.Stdout, out)
		return nil
	},
}

// recallResult is the search hits of a question, each with the nodes the
// decision graph links to it.
type recallResult struct {
	Query string       `json:"query"`
	Total int          `json:"total"`
	Items []recallItem `json:"items"`
}

type recallItem struct {
	Hit   model.SearchHit `json:"hit"`
	Links []recallLink    `json:"links"`
}

// recallLink is one edge of a hit: Direction is "out" when the hit links to
// Node and "in" when Node links to the hit.
type recallLink struct {
	Edge      model.GraphEdgeType `json:"edge"`
	Score     *float64            `json:"score,omitempty"`
	Direction string              `json:"direction"`
	Node      model.GraphNode     `json:"node"`
}

// recallNode is the graph node of a hit; a comment stands for its issue.
func recallNode(h model.SearchHit) string {
	if h.Type == model.SearchDocument {
		return model.DocumentNodeID(h.Project, h.Key)
	}
	return model.IssueNodeID(h.Key)
}

func (a *App) renderRecall(w io.Writer, r recallResult) {
	if len(r.Items) == 0 {
		fmt.Fprintln(w, "검색 결과가 없습니다.")
		return
	}
	for n, it := range r.Items {
		h := it.Hit
		key := h.Key
		if h.CommentID != nil {
			key += "#" + strconv.FormatInt(*h.CommentID, 10)
		}
		if n > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%d. [%s] %s %s  %s  %s\n", n+1, h.Type, h.Project, key, deref(h.Kind, "-"), deref(h.Title, "-"))
		if s := strings.Join(strings.Fields(h.Snippet), " "); s != "" {
			fmt.Fprintf(w, "   %s\n", s)
		}
		for _, l := range it.Links {
			arrow := "→"
			if l.Direction == "in" {
				arrow = "←"
			}
			status := ""
			if l.Node.Status != nil {
				status = " (" + *l.Node.Status + ")"
			}
			fmt.Fprintf(w, "   %s %s %s %s%s  %s\n", arrow, edgeLabel(l.Edge, l.Score), l.Node.ID, l.Node.Kind, status, deref(l.Node.Title, "-"))
		}
	}
	if r.Total > len(r.Items) {
		fmt.Fprintf(w, "\n%d건 중 %d건. 더 보려면 --limit 을 늘리거나 agb find 로 찾습니다.\n", r.Total, len(r.Items))
	}
}
