package cli

import (
	"context"

	"github.com/wsk0715/agent-board/internal/mcp"
)

var mcpCmd = &command{
	name: "mcp", summary: "MCP 서버를 표준입출력으로 실행 (에이전트 도구: context, ready, edge, spec, record)",
	usage: `사용법: agb mcp

Model Context Protocol 서버를 표준입출력으로 실행합니다. 에이전트 도구 설정에 명령으로 등록해 씁니다.
도구는 CLI 명령과 같은 API 를 같은 인증(AGB_TOKEN, AGB_ACTOR 등)으로 호출합니다.

도구: agb_context(= agb context), agb_ready(= agb ready), agb_edge_candidates, agb_edge_decide(= agb edge confirm|reject),
agb_spec_put(= agb spec put), agb_record_add(= agb record add)

표준출력에는 프로토콜 메시지만 나갑니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("mcp", g), args)
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
		return mcp.New(c, a.Version).Serve(ctx, a.Stdin, a.Stdout)
	},
}
