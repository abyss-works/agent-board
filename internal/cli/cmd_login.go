package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/internal/model"
)

var (
	errLoginDenied  = errors.New("승인이 거부되었습니다.")
	errLoginExpired = errors.New("승인 시간이 지났습니다. 다시 실행하세요.")
)

var loginCmd = &command{
	name: "login", summary: "기기 승인으로 이 행위자의 토큰 받기",
	usage: `사용법: agb login --actor <ID> [--no-browser]

기기 승인으로 이 행위자의 토큰을 받아 설정 파일에 행위자별로 저장합니다. 이후 이 행위자의 요청은
그 토큰을 씁니다. 에이전트는 공용 토큰 대신 이 토큰을 씁니다.

1. 서버에 승인 요청을 만들고 승인 주소(<서버 주소>/device?code=<코드>)와 코드를 표준 오류에 씁니다.
2. 브라우저로 승인 주소를 엽니다. 사용자가 웹 화면에서 코드를 확인하고 승인합니다.
3. 승인되면 토큰을 받아 설정 파일의 tokens에 저장하고 결과를 출력합니다.

승인 요청은 서버가 정한 시간(10분) 안에 승인해야 합니다. 거부되거나 시간이 지나면 종료 코드 1로
끝납니다. 표준 출력에는 결과만 쓰고, 토큰 값은 어떤 출력에도 쓰지 않습니다. 같은 기기에서 같은
행위자가 다시 로그인하면 서버가 이전 토큰을 폐기합니다.

행위자는 --actor, AGB_ACTOR, 설정의 actor 순으로 정합니다. 사용자 행위자(owner)는 로그인하지 않고
공용 토큰을 씁니다.

옵션:
  --no-browser    브라우저를 열지 않고 주소만 씁니다
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("login", g)
		var noBrowser bool
		fs.BoolVar(&noBrowser, "no-browser", false, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		_, s, err := a.settings(g)
		if err != nil {
			return err
		}
		if err := s.requireActor(); err != nil {
			return err
		}
		c := a.newClient(s)
		resp, err := c.CreateDeviceAuthorization(ctx, s.Actor, s.ClientHost)
		if err != nil {
			return err
		}
		grant, err := decode[model.DeviceGrant](resp)
		if err != nil {
			return err
		}
		approvalURL := s.URL + "/device?code=" + grant.UserCode
		a.announceApproval(approvalURL, grant)
		if !noBrowser && a.OpenBrowser != nil {
			if err := a.OpenBrowser(approvalURL); err != nil {
				fmt.Fprintln(a.Stderr, "브라우저를 열지 못했습니다. 주소를 직접 여세요.")
			}
		}
		issued, err := a.awaitToken(ctx, c, grant)
		if err != nil {
			return err
		}
		if _, err := a.modifyConfig(g, func(cfg *Config) error {
			if cfg.Tokens == nil {
				cfg.Tokens = map[string]string{}
			}
			cfg.Tokens[s.Actor] = issued.Secret
			return nil
		}); err != nil {
			return err
		}
		return a.printLogin(g, issued)
	},
}

// announceApproval tells on stderr where the user approves the login, with
// the code to compare and the minutes left. Stdout is kept for the result.
func (a *App) announceApproval(approvalURL string, grant model.DeviceGrant) {
	minutes := max(1, (grant.ExpiresIn+59)/60)
	fmt.Fprintf(a.Stderr, "승인 주소: %s\n승인 코드: %s\n웹 화면의 코드가 같은지 확인하고 승인하면 로그인이 끝납니다. 만료까지 %d분 남았습니다.\n",
		approvalURL, grant.UserCode, minutes)
}

// awaitToken trades the device code for the actor token. It tries once per
// interval of the grant until the user approves, which gives the token, or
// refuses, the request expires or ctx ends, which give an error.
func (a *App) awaitToken(ctx context.Context, c *client.Client, grant model.DeviceGrant) (model.IssuedToken, error) {
	interval := time.Duration(grant.Interval) * time.Second
	deadline := a.Now().Add(time.Duration(grant.ExpiresIn) * time.Second)
	for {
		if err := a.wait(ctx, interval); err != nil {
			return model.IssuedToken{}, err
		}
		resp, err := c.ExchangeToken(ctx, grant.DeviceCode)
		var apiErr *client.Error
		switch {
		case err == nil:
			return decode[model.IssuedToken](resp)
		case !errors.As(err, &apiErr):
			return model.IssuedToken{}, err
		case apiErr.Code == "access_denied":
			return model.IssuedToken{}, errLoginDenied
		case apiErr.Code == "expired_token":
			return model.IssuedToken{}, errLoginExpired
		case apiErr.Code != "authorization_pending":
			return model.IssuedToken{}, err
		}
		if !a.Now().Before(deadline) {
			return model.IssuedToken{}, errLoginExpired
		}
	}
}

// loginResult is what a login prints. The token itself is never part of it.
type loginResult struct {
	Actor      string     `json:"actor"`
	ClientHost string     `json:"client_host"`
	TokenID    int64      `json:"token_id"`
	CreatedAt  model.Time `json:"created_at"`
}

func (a *App) printLogin(g *globals, t model.IssuedToken) error {
	res := loginResult{Actor: t.Actor, ClientHost: t.ClientHost, TokenID: t.ID, CreatedAt: t.CreatedAt}
	if a.jsonOutput(g) {
		raw, _ := json.Marshal(res)
		writeJSON(a.Stdout, raw)
		return nil
	}
	fmt.Fprintf(a.Stdout, "로그인했습니다: %s (토큰 %d, 기기 %s)\n", res.Actor, res.TokenID, res.ClientHost)
	return nil
}
