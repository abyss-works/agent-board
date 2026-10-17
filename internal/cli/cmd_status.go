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
