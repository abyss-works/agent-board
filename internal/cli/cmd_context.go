package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/wsk0715/agent-board/internal/client"
)

// renderPretty writes a response body as indented JSON, for the commands whose
// result has no table form.
func renderPretty(w io.Writer, resp *client.Response) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, resp.Body, "", "  "); err != nil {
		_, err = w.Write(resp.Body)
		return err
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// --- context ---

type contextNeighbor struct {
	Direction string `json:"direction"`
	Kind      string `json:"kind"`
	NodeType  string `json:"node_type"`
	Key       string `json:"key_or_name"`
	Title     string `json:"title"`
	Hop       int    `json:"hop"`
}

type contextItem struct {
	NodeType     string            `json:"node_type"`
	Project      string            `json:"project"`
	Key          string            `json:"key_or_name"`
	Kind         string            `json:"kind"`
	Title        string            `json:"title"`
	Score        float64           `json:"score"`
	Summary      string            `json:"summary"`
	Content      string            `json:"content"`
	SupersededBy *contextNeighbor  `json:"superseded_by"`
	Neighbors    []contextNeighbor `json:"neighbors"`
}

type contextResult struct {
	CutoffApplied bool          `json:"cutoff_applied"`
	ScoreGap12    float64       `json:"score_gap_1_2"`
	Reranked      bool          `json:"reranked"`
	FallbackUsed  bool          `json:"fallback_used"`
	FallbackMode  string        `json:"fallback_mode"`
	Items         []contextItem `json:"items"`
}

var contextCmd = &command{
	name: "context", summary: "질문 하나로 관련 문서·이슈와 연결된 이웃을 한 번에 조회 (하이브리드 검색 + 리랭크)",
	usage: `사용법: agb context <질문...> [옵션]

키워드(PGroonga)와 의미(벡터) 검색을 합쳐 순위를 매기고, 상위 항목의 본문과 연결된 이웃(양방향, 확정된 간선)을
한 번의 호출로 돌려줍니다. 상위 1위와 2위의 점수 차가 하한 이상이면 1위만 돌려줍니다(cutoff_applied).
리랭커가 실패하면 순위 합산(RRF)으로 대신하고 fallback_mode 에 그 사실을 적습니다.

옵션:
  --project, -p <P>       프로젝트 키. 없으면 모든 프로젝트
  --no-global             전역 프로젝트 문서를 결과에서 뺍니다
  --type <T>              issue, activity, spec, record. 쉼표 OR
  --max-results <N>       1~10. 기본 3
  --cutoff <F>            0~1. 자동 컷오프 점수 차 하한. 서버 기본 0.15
  --expand-hops <N>       0~2. 이웃 본문을 함께 받을 단계. 기본 0(요약만 1단계)
  --include-archived      아카이브 문서도 찾습니다
  --include-superseded    대체된 문서도 찾고, 대체한 최신 문서를 superseded_by 로 알려 줍니다
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("context", g)
		var project, types string
		var maxResults, hops int
		var cutoff float64
		var noGlobal, archived, superseded bool
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.BoolVar(&noGlobal, "no-global", false, "")
		fs.StringVar(&types, "type", "", "")
		fs.IntVar(&maxResults, "max-results", 0, "")
		fs.Float64Var(&cutoff, "cutoff", 0, "")
		fs.IntVar(&hops, "expand-hops", 0, "")
		fs.BoolVar(&archived, "include-archived", false, "")
		fs.BoolVar(&superseded, "include-superseded", false, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return usageErrorf("질문이 필요합니다.")
		}
		req := map[string]any{"query": strings.Join(pos, " ")}
		if project != "" {
			req["project"] = project
		}
		if noGlobal {
			req["include_global"] = false
		}
		if t := splitCSV(types); len(t) > 0 {
			req["source_types"] = t
		}
		if maxResults != 0 {
			req["max_results"] = maxResults
		}
		if isSet(fs, "cutoff") {
			req["confidence_cutoff"] = cutoff
		}
		if hops != 0 {
			req["expand_hops"] = hops
		}
		if archived {
			req["include_archived"] = true
		}
		if superseded {
			req["include_superseded"] = true
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.ContextQuery(ctx, req)
		if err != nil {
			return err
		}
		return a.emit(g, resp, renderContext)
	},
}

func renderContext(w io.Writer, resp *client.Response) error {
	r, err := decode[contextResult](resp)
	if err != nil {
		return err
	}
	if len(r.Items) == 0 {
		fmt.Fprintln(w, "관련 항목이 없습니다.")
		return nil
	}
	for i, it := range r.Items {
		fmt.Fprintf(w, "[%d] %s %s/%s  (%s, score %.3f)\n    %s\n", i+1, it.NodeType, it.Project, it.Key, it.Kind, it.Score, it.Title)
		if it.Summary != "" {
			fmt.Fprintf(w, "    요약: %s\n", strings.Join(strings.Fields(it.Summary), " "))
		}
		if it.SupersededBy != nil {
			fmt.Fprintf(w, "    대체됨 → %s %s  %s\n", it.SupersededBy.NodeType, it.SupersededBy.Key, it.SupersededBy.Title)
		}
		for _, n := range it.Neighbors {
			arrow := "→"
			if n.Direction == "inbound" {
				arrow = "←"
			}
			fmt.Fprintf(w, "    %s %s %s %s  %s\n", arrow, n.Kind, n.NodeType, n.Key, n.Title)
		}
		if it.Content != "" {
			fmt.Fprintf(w, "\n%s\n\n", it.Content)
		}
	}
	if r.CutoffApplied {
		fmt.Fprintf(w, "확신도 컷오프: 1위와 2위의 점수 차 %.3f, 상위 1건만 반환했습니다.\n", r.ScoreGap12)
	}
	if r.FallbackUsed {
		fmt.Fprintf(w, "알림: 일부 단계가 실패해 %s 경로로 대신했습니다.\n", r.FallbackMode)
	}
	return nil
}

// --- ready ---

type readyIssue struct {
	Key         string  `json:"key"`
	Title       string  `json:"title"`
	Type        string  `json:"type"`
	Status      string  `json:"status"`
	Priority    string  `json:"priority"`
	EpicKey     *string `json:"epic_key"`
	MilestoneID *int64  `json:"milestone_id"`
	ClaimedBy   *string `json:"claimed_by"`
}

type readyList struct {
	Items  []readyIssue `json:"items"`
	Total  int          `json:"total"`
	Offset int          `json:"offset"`
}

var readyCmd = &command{
	name: "ready", summary: "지금 착수할 수 있는 이슈 큐 (선행 이슈가 모두 끝난 것)",
	usage: `사용법: agb ready <PROJECT> [옵션]

선행 이슈(depends_on)가 모두 닫혔고 아직 아무도 잡지 않은 열린 이슈를 우선순위 순으로 보입니다.

옵션:
  --epic <KEY>            에픽 키로 거릅니다
  --label <L>             라벨로 거릅니다
  --milestone <ID>        마일스톤 ID로 거릅니다
  --include-claimed       다른 세션이 잡은 이슈도 보입니다
  --limit <N>             최대 100. 기본 20
  --offset <N>
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("ready", g)
		var epic, label string
		var milestone int64
		var claimed bool
		var limit, offset int
		fs.StringVar(&epic, "epic", "", "")
		fs.StringVar(&label, "label", "", "")
		fs.Int64Var(&milestone, "milestone", 0, "")
		fs.BoolVar(&claimed, "include-claimed", false, "")
		fs.IntVar(&limit, "limit", 0, "")
		fs.IntVar(&offset, "offset", 0, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		project, err := one(pos, "<PROJECT>")
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.Ready(ctx, project, epic, label, milestone, claimed, limit, offset)
		if err != nil {
			return err
		}
		return a.emit(g, resp, func(w io.Writer, resp *client.Response) error {
			l, err := decode[readyList](resp)
			if err != nil {
				return err
			}
			if len(l.Items) == 0 {
				fmt.Fprintln(w, "착수할 수 있는 이슈가 없습니다.")
				return nil
			}
			tb := &table{header: []string{"KEY", "PRI", "TYPE", "STATUS", "EPIC", "CLAIMED", "TITLE"}}
			for _, it := range l.Items {
				tb.add(it.Key, it.Priority, it.Type, it.Status, deref(it.EpicKey, "-"), deref(it.ClaimedBy, "-"), it.Title)
			}
			tb.write(w, a.TermWidth)
			footer(w, len(l.Items), l.Total, l.Offset)
			return nil
		})
	},
}

// --- edge ---

type edgeRow struct {
	ID         int64    `json:"id"`
	Project    string   `json:"project"`
	SourceType string   `json:"source_type"`
	SourceKey  string   `json:"source_key_or_name"`
	TargetType *string  `json:"target_type"`
	TargetKey  string   `json:"target_key_or_name"`
	Unresolved *string  `json:"unresolved_target_text"`
	Kind       string   `json:"kind"`
	Status     string   `json:"status"`
	Confidence *float64 `json:"confidence"`
	Evidence   *string  `json:"evidence"`
}

type edgeList struct {
	Items  []edgeRow `json:"items"`
	Total  int       `json:"total"`
	Offset int       `json:"offset"`
}

var edgeCmd = &command{name: "edge", summary: "그래프 간선 후보 검토 (candidates, confirm, reject, remove)", subs: []*command{
	edgeCandidatesCmd, edgeConfirmCmd, edgeRejectCmd, edgeRemoveCmd,
}}

var edgeCandidatesCmd = &command{
	name: "candidates", summary: "확정을 기다리는 간선 후보 목록",
	usage: `사용법: agb edge candidates [--project <KEY>] [--kind <K>] [--limit <N>] [--offset <N>]

수집 단계가 제안한 간선(supersedes, about, depends_on, related) 중 아직 확정하거나 거절하지 않은 것을 보입니다.
확정한 간선만 조회 결과의 이웃과 대체 관계에 반영됩니다. agb edge confirm <ID> 또는 agb edge reject <ID> 로 결정합니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("edge candidates", g)
		var project, kind string
		var limit, offset int
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.StringVar(&kind, "kind", "", "")
		fs.IntVar(&limit, "limit", 0, "")
		fs.IntVar(&offset, "offset", 0, "")
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
		resp, err := c.EdgeCandidates(ctx, project, kind, limit, offset)
		if err != nil {
			return err
		}
		return a.emit(g, resp, func(w io.Writer, resp *client.Response) error {
			l, err := decode[edgeList](resp)
			if err != nil {
				return err
			}
			if len(l.Items) == 0 {
				fmt.Fprintln(w, "검토할 간선 후보가 없습니다.")
				return nil
			}
			tb := &table{header: []string{"ID", "KIND", "FROM", "TO", "CONF", "EVIDENCE"}}
			for _, e := range l.Items {
				to := e.TargetKey
				if to == "" {
					to = deref(e.Unresolved, "-")
				}
				conf := "-"
				if e.Confidence != nil {
					conf = fmt.Sprintf("%.2f", *e.Confidence)
				}
				tb.add(strconv.FormatInt(e.ID, 10), e.Kind, e.SourceType+":"+e.SourceKey, to, conf,
					strings.Join(strings.Fields(deref(e.Evidence, "")), " "))
			}
			tb.write(w, a.TermWidth)
			footer(w, len(l.Items), l.Total, l.Offset)
			return nil
		})
	},
}

func edgeDecisionCmd(name, summary, action string) *command {
	return &command{
		name: name, summary: summary,
		usage: "사용법: agb edge " + name + " <ID>\n\n간선 후보를 " + summary + ". 이미 결정한 간선이면 409 입니다.\n",
		run: func(a *App, ctx context.Context, g *globals, args []string) error {
			pos, err := parseArgs(leafFlags("edge "+name, g), args)
			if err != nil {
				return err
			}
			id, err := edgeIDArg(pos)
			if err != nil {
				return err
			}
			c, err := a.client(g, true)
			if err != nil {
				return err
			}
			resp, err := c.DecideEdge(ctx, id, action)
			if err != nil {
				return err
			}
			return a.emit(g, resp, func(w io.Writer, resp *client.Response) error {
				e, err := decode[edgeRow](resp)
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "간선 %d (%s): %s\n", e.ID, e.Kind, e.Status)
				return nil
			})
		},
	}
}

func edgeIDArg(pos []string) (int64, error) {
	raw, err := one(pos, "<ID>")
	if err != nil {
		return 0, err
	}
	id, ok := parseID(raw)
	if !ok {
		return 0, usageErrorf("<ID>는 간선 ID(숫자)여야 합니다: %q. agb edge candidates 로 확인하세요.", raw)
	}
	return id, nil
}

var (
	edgeConfirmCmd = edgeDecisionCmd("confirm", "확정", "confirm")
	edgeRejectCmd  = edgeDecisionCmd("reject", "거절", "reject")
)

var edgeRemoveCmd = &command{
	name: "remove", summary: "간선 제거",
	usage: "사용법: agb edge remove <ID>\n\n간선을 removed 상태로 바꿉니다. 이미 제거했으면 그대로 성공합니다.\n",
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("edge remove", g), args)
		if err != nil {
			return err
		}
		id, err := edgeIDArg(pos)
		if err != nil {
			return err
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.RemoveEdge(ctx, id)
		if err != nil {
			return err
		}
		return a.emit(g, resp, renderPretty)
	},
}

// --- spec and record writes ---

var specCmd = &command{name: "spec", summary: "가변 스펙 문서 업서트·조회 (--wait 로 색인 완료까지 대기)", subs: []*command{specPutCmd, specGetCmd}}

var specPutCmd = &command{
	name: "put", summary: "스펙 문서를 만들거나 새 개정으로 갱신",
	usage: `사용법: agb spec put <PROJECT> <slug> --body-file <파일|-> [옵션]

같은 slug 가 있으면 새 개정을 쌓습니다. 본문은 UTF-8 텍스트·마크다운 2MiB 이하입니다.

옵션:
  --title <T>             제목
  --kind <K>              종류
  --tags <a,b>            태그. 쉼표로 잇습니다
  --change-summary <S>    개정 요약
  --body <TEXT>, --body-file <PATH|->   본문
  --wait                  색인이 끝날 때까지 기다립니다(최대 15초). 응답에 ingest_status 와 supersedes 후보가 담깁니다
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("spec put", g)
		var title, kind, tags, summary, body, bodyFile string
		var wait bool
		fs.StringVar(&title, "title", "", "")
		fs.StringVar(&kind, "kind", "", "")
		fs.StringVar(&tags, "tags", "", "")
		fs.StringVar(&summary, "change-summary", "", "")
		fs.StringVar(&body, "body", "", "")
		fs.StringVar(&bodyFile, "body-file", "", "")
		fs.BoolVar(&wait, "wait", false, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if len(pos) != 2 {
			return usageErrorf("<PROJECT>와 <slug> 인자가 필요합니다.")
		}
		text, present, err := a.readBody(fs, body, bodyFile)
		if err != nil {
			return err
		}
		if !present {
			return usageErrorf("--body 또는 --body-file 이 필요합니다.")
		}
		in := map[string]any{"content": text, "title": title, "kind": kind, "change_summary": summary, "tags": splitCSV(tags)}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.UpsertSpec(ctx, pos[0], pos[1], in, wait)
		if err != nil {
			return err
		}
		return a.emit(g, resp, renderPretty)
	},
}

var specGetCmd = &command{
	name: "get", summary: "스펙 문서 조회",
	usage: "사용법: agb spec get <PROJECT> <slug>\n",
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("spec get", g), args)
		if err != nil {
			return err
		}
		if len(pos) != 2 {
			return usageErrorf("<PROJECT>와 <slug> 인자가 필요합니다.")
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.GetSpec(ctx, pos[0], pos[1])
		if err != nil {
			return err
		}
		return a.emit(g, resp, renderPretty)
	},
}

var recordCmd = &command{name: "record", summary: "불변 기록 문서 추가 (--wait 로 색인 완료까지 대기)", subs: []*command{recordAddCmd}}

var recordAddCmd = &command{
	name: "add", summary: "기록 문서 추가",
	usage: `사용법: agb record add <PROJECT> <이름> --kind <K> --body-file <파일|-> [옵션]

기록 문서는 한 번 쓰면 바꾸지 않습니다. 같은 이름이 있으면 409 입니다.

옵션:
  --kind <K>              종류(필수)
  --title <T>             제목
  --tags <a,b>            태그
  --body <TEXT>, --body-file <PATH|->   본문
  --wait                  색인이 끝날 때까지 기다립니다(최대 15초)
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("record add", g)
		var title, kind, tags, body, bodyFile string
		var wait bool
		fs.StringVar(&title, "title", "", "")
		fs.StringVar(&kind, "kind", "", "")
		fs.StringVar(&tags, "tags", "", "")
		fs.StringVar(&body, "body", "", "")
		fs.StringVar(&bodyFile, "body-file", "", "")
		fs.BoolVar(&wait, "wait", false, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if len(pos) != 2 {
			return usageErrorf("<PROJECT>와 <이름> 인자가 필요합니다.")
		}
		if kind == "" {
			return usageErrorf("--kind 가 필요합니다.")
		}
		text, present, err := a.readBody(fs, body, bodyFile)
		if err != nil {
			return err
		}
		if !present {
			return usageErrorf("--body 또는 --body-file 이 필요합니다.")
		}
		in := map[string]any{"name": pos[1], "kind": kind, "title": title, "tags": splitCSV(tags), "content": text}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.CreateRecord(ctx, pos[0], in, wait)
		if err != nil {
			return err
		}
		return a.emit(g, resp, renderPretty)
	},
}

// --- epic ---

var epicCmd = &command{name: "epic", summary: "에픽 관리 (에픽 → 마일스톤 → 이슈 3단계의 최상위)", subs: []*command{
	epicListCmd, epicGetCmd, epicCreateCmd, epicUpdateCmd, epicCloseCmd, epicReopenCmd,
}}

type epicRow struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type epicList struct {
	Items  []epicRow `json:"items"`
	Total  int       `json:"total"`
	Offset int       `json:"offset"`
}

var epicListCmd = &command{
	name: "list", summary: "에픽 목록",
	usage: "사용법: agb epic list --project <KEY> [--status <S>]\n\n--status 는 open(기본), closed, deleted 를 쉼표로 잇거나 all 입니다.\n",
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("epic list", g)
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
		resp, err := c.ListEpics(ctx, project, status)
		if err != nil {
			return err
		}
		return a.emit(g, resp, func(w io.Writer, resp *client.Response) error {
			l, err := decode[epicList](resp)
			if err != nil {
				return err
			}
			if len(l.Items) == 0 {
				fmt.Fprintln(w, "에픽이 없습니다.")
				return nil
			}
			tb := &table{header: []string{"KEY", "STATUS", "TITLE"}}
			for _, e := range l.Items {
				tb.add(e.Key, e.Status, e.Title)
			}
			tb.write(w, a.TermWidth)
			footer(w, len(l.Items), l.Total, l.Offset)
			return nil
		})
	},
}

var epicGetCmd = &command{
	name: "get", summary: "에픽 조회",
	usage: "사용법: agb epic get <KEY>\n",
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("epic get", g), args)
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
		resp, err := c.GetEpic(ctx, key)
		if err != nil {
			return err
		}
		return a.emit(g, resp, renderPretty)
	},
}

var epicCreateCmd = &command{
	name: "create", summary: "에픽 만들기",
	usage: `사용법: agb epic create --project <KEY> --key <EPIC-KEY> <제목> [옵션]

옵션:
  --description <TEXT>    설명
  --source-issue <KEY>    이 에픽의 출처 이슈 키
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("epic create", g)
		var project, key, desc, source string
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.StringVar(&key, "key", "", "")
		fs.StringVar(&desc, "description", "", "")
		fs.StringVar(&source, "source-issue", "", "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if project == "" || key == "" || len(pos) == 0 {
			return usageErrorf("--project, --key 와 <제목>이 필요합니다.")
		}
		in := map[string]any{"key": key, "title": strings.Join(pos, " "), "description": desc}
		if source != "" {
			in["source_issue_key"] = source
		}
		c, err := a.client(g, true)
		if err != nil {
			return err
		}
		resp, err := c.CreateEpic(ctx, project, in)
		if err != nil {
			return err
		}
		return a.emit(g, resp, renderPretty)
	},
}

var epicUpdateCmd = &command{
	name: "update", summary: "에픽 수정",
	usage: "사용법: agb epic update <KEY> [--title <T>] [--description <TEXT>] [--status <open|closed>]\n",
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("epic update", g)
		var title, desc, status string
		fs.StringVar(&title, "title", "", "")
		fs.StringVar(&desc, "description", "", "")
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
		for field, v := range map[string]string{"title": title, "description": desc, "status": status} {
			if isSet(fs, field) {
				patch[field] = v
			}
		}
		if len(patch) == 0 {
			return usageErrorf("바꿀 필드가 없습니다. --title, --description, --status 중 하나를 주세요.")
		}
		return epicPatch(a, ctx, g, key, patch)
	},
}

func epicPatch(a *App, ctx context.Context, g *globals, key string, patch map[string]any) error {
	c, err := a.client(g, true)
	if err != nil {
		return err
	}
	resp, err := c.PatchEpic(ctx, key, patch)
	if err != nil {
		return err
	}
	return a.emit(g, resp, renderPretty)
}

func epicStatusCmd(name, summary, status string) *command {
	return &command{
		name: name, summary: summary,
		usage: "사용법: agb epic " + name + " <KEY>\n\n에픽 상태를 " + status + " 로 바꿉니다.\n",
		run: func(a *App, ctx context.Context, g *globals, args []string) error {
			pos, err := parseArgs(leafFlags("epic "+name, g), args)
			if err != nil {
				return err
			}
			key, err := one(pos, "<KEY>")
			if err != nil {
				return err
			}
			return epicPatch(a, ctx, g, key, map[string]any{"status": status})
		},
	}
}

var (
	epicCloseCmd  = epicStatusCmd("close", "에픽 닫기", "closed")
	epicReopenCmd = epicStatusCmd("reopen", "에픽 다시 열기", "open")
)
