package cli

import (
	"context"
	"net/http"
)

var healthCmd = &command{
	name: "health", summary: "서버 상태 (인증 없음)",
	usage: `사용법: agb health

GET /v1/health. 서버 주소만 있으면 됩니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("health", g), args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		c, err := a.client(g, false)
		if err != nil {
			return err
		}
		resp, err := c.Request(ctx, http.MethodGet, "/v1/health", nil, nil)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderHealth)
	},
}

var whoamiCmd = &command{
	name: "whoami", summary: "현재 행위자",
	usage: `사용법: agb whoami

GET /v1/actors/me. 지금 설정된 행위자를 서버가 어떻게 보는지 확인합니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("whoami", g), args)
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
		resp, err := c.Request(ctx, http.MethodGet, "/v1/actors/me", nil, nil)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderActor)
	},
}

var actorsCmd = &command{
	name: "actors", summary: "선언된 행위자 목록",
	usage: `사용법: agb actors

GET /v1/actors.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("actors", g), args)
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
		resp, err := c.Actors(ctx)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderActors)
	},
}

var statsCmd = &command{
	name: "stats", summary: "서버 성능 지표",
	usage: `사용법: agb stats

GET /v1/stats. 서버가 메모리에 모은 지표를 그대로 보입니다.
  started_at   집계를 시작한 시각(UTC). 서버를 다시 시작하면 이 시각부터 0에서 다시 셉니다.
  db_bytes     DB 파일 크기(바이트). WAL 파일은 포함하지 않습니다.
  routes       라우트 패턴(예: /v1/issues/{key})과 메서드별 항목.
               표준 메서드 밖의 메서드는 OTHER 하나로 묶습니다.
    requests   요청 수
    errors     5xx로 응답한 요청 수
    total_ms   처리 시간 합계(밀리초)
    max_ms     처리 시간 최댓값(밀리초)
    buckets    처리 시간 구간별 요청 수. le_ms 가 구간 상한(포함)이고 겹치지 않으며,
               마지막 항목(le_ms 가 null)은 가장 큰 상한을 넘은 요청입니다.
  어떤 라우트도 처리하지 않은 요청(인증·헤더 거부, 없는 경로, 웹 클라이언트)은 라우트 (unrouted) 로 묶습니다.

판정은 하지 않습니다. 평균은 total_ms / requests 로 구합니다.
표는 평균(AVG_MS)을 함께 보이지만 JSON 에는 없습니다.

예: agb stats --fields routes.route,routes.method,routes.requests,routes.max_ms --text
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("stats", g), args)
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
		resp, err := c.Request(ctx, http.MethodGet, "/v1/stats", nil, nil)
		if err != nil {
			return err
		}
		return a.emit(g, resp, a.renderStats)
	},
}
