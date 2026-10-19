package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

var configCmd = &command{name: "config", summary: "설정 파일 관리", subs: []*command{
	configPathCmd, configShowCmd, configSetCmd, configUnsetCmd, configSetTokenCmd,
}}

var configPathCmd = &command{
	name: "path", summary: "설정 파일 경로 출력",
	usage: `사용법: agb config path

설정 파일 경로를 출력합니다. 기본값은 사용자 설정 폴더의 agb/config.json 입니다.
`,
	run: func(a *App, _ context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("config path", g), args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		path, err := a.configPath(g)
		if err != nil {
			return err
		}
		fmt.Fprintln(a.Stdout, path)
		return nil
	},
}

var configShowCmd = &command{
	name: "show", summary: "설정 파일 내용 표시 (토큰은 가림)",
	usage: `사용법: agb config show [--json]

설정 파일의 값을 보여 줍니다. 토큰 값은 보여 주지 않습니다. 공용 토큰은 저장 여부만, 행위자별 토큰은
토큰이 있는 행위자의 이름만 보여 줍니다.
`,
	run: func(a *App, _ context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("config show", g), args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		path, err := a.configPath(g)
		if err != nil {
			return err
		}
		cfg, err := loadConfig(path)
		if err != nil {
			return err
		}
		actorTokens := make([]string, 0, len(cfg.Tokens))
		for actor := range cfg.Tokens {
			actorTokens = append(actorTokens, actor)
		}
		sort.Strings(actorTokens)
		if a.jsonOutput(g) {
			raw, _ := json.Marshal(map[string]any{
				"path": path, "url": cfg.URL, "actor": cfg.Actor, "session_id": cfg.SessionID, "client_host": cfg.ClientHost,
				"token_set": cfg.Token != "", "actor_tokens": actorTokens,
			})
			writeJSON(a.Stdout, raw)
			return nil
		}
		token := "(없음)"
		if cfg.Token != "" {
			token = "(설정됨)"
		}
		fields(a.Stdout, [][2]string{
			{"path", path},
			{"url", orDash(cfg.URL)}, {"actor", orDash(cfg.Actor)}, {"session_id", orDash(cfg.SessionID)}, {"client_host", orDash(cfg.ClientHost)},
			{"token", token},
			{"actor_tokens", joinOr(actorTokens, "(없음)")},
		})
		return nil
	},
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

var configSetCmd = &command{
	name: "set", summary: "설정 값 저장 (url, actor, session_id, client_host)",
	usage: `사용법: agb config set <키> <값>

키:
  url           서버 주소 (예: http://127.0.0.1:8420)
  actor         행위자 ID (소문자와 숫자 1~32자)
  session_id    세션 ID. 비우면 cli-YYYYMMDD 를 씁니다
  client_host   기기 이름. 비우면 호스트 이름을 씁니다

토큰은 인자로 받지 않습니다. agb config set-token 을 쓰세요.
설정 파일은 사용자만 읽을 수 있는 권한으로 만듭니다.
`,
	run: func(a *App, _ context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("config set", g), args)
		if err != nil {
			return err
		}
		if len(pos) != 2 {
			return usageErrorf("<키>와 <값> 두 인자가 필요합니다.")
		}
		key, value := pos[0], strings.TrimSpace(pos[1])
		if key == "token" {
			return usageErrorf("토큰은 인자로 받지 않습니다. `agb config set-token`으로 stdin에서 읽습니다.")
		}
		if value == "" {
			return usageErrorf("값이 비어 있습니다. 지우려면 `agb config unset %s`를 쓰세요.", key)
		}
		if err := validateConfigValue(key, value); err != nil {
			return err
		}
		if key == "url" {
			value = strings.TrimRight(value, "/")
		}
		return a.updateConfig(g, func(c *Config) error {
			f, ok := c.field(key)
			if !ok {
				return usageErrorf("모르는 키 %q. 키는 %s 중 하나입니다.", key, strings.Join(configKeys, ", "))
			}
			*f = value
			return nil
		}, key+" 저장")
	},
}

func validateConfigValue(key, value string) error {
	switch key {
	case "url":
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return usageErrorf("url은 http:// 또는 https:// 로 시작하는 주소여야 합니다.")
		}
	case "actor":
		if !model.ValidActorID(value) {
			return usageErrorf("actor는 소문자와 숫자 1~32자여야 합니다.")
		}
	case "session_id":
		if !model.ValidSessionID(value) {
			return usageErrorf("session_id는 영숫자로 시작하는 영숫자, '.', '_', '-' 1~128자여야 합니다.")
		}
	case "client_host":
		if !model.ValidClientHost(value) {
			return usageErrorf("client_host는 점으로 나뉜 영숫자·하이픈 라벨 64자 이내여야 합니다.")
		}
	default:
		return usageErrorf("모르는 키 %q. 키는 %s 중 하나입니다.", key, strings.Join(configKeys, ", "))
	}
	return nil
}

var configUnsetCmd = &command{
	name: "unset", summary: "설정 값 삭제",
	usage: `사용법: agb config unset <키>

키는 url, actor, session_id, client_host, token 중 하나입니다.
`,
	run: func(a *App, _ context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("config unset", g), args)
		if err != nil {
			return err
		}
		key, err := one(pos, "<키>")
		if err != nil {
			return err
		}
		return a.updateConfig(g, func(c *Config) error {
			if key == "token" {
				c.Token = ""
				return nil
			}
			f, ok := c.field(key)
			if !ok {
				return usageErrorf("모르는 키 %q. 키는 %s, token 중 하나입니다.", key, strings.Join(configKeys, ", "))
			}
			*f = ""
			return nil
		}, key+" 삭제")
	},
}

var configSetTokenCmd = &command{
	name: "set-token", summary: "공용 토큰 저장 (stdin에서 읽음)",
	usage: `사용법: agb config set-token < 토큰파일
        echo "$TOKEN" | agb config set-token

토큰을 표준 입력의 첫 줄에서 읽어 설정 파일에 저장합니다. 터미널에서 실행하면
입력이 화면에 표시되지 않습니다. 토큰은 어떤 출력에도 쓰지 않습니다.

공용 토큰은 사용자 행위자(owner)만 씁니다. 에이전트 행위자의 토큰은 agb login --actor <ID> 로 받습니다.
`,
	run: func(a *App, _ context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("config set-token", g), args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		var token string
		if a.StdinIsTerminal && a.ReadSecret != nil {
			fmt.Fprint(a.Stderr, "토큰 (화면에 표시되지 않음): ")
			token, err = a.ReadSecret()
			fmt.Fprintln(a.Stderr)
		} else {
			token, err = readLine(a.Stdin)
		}
		if err != nil {
			return err
		}
		token = strings.TrimSpace(token)
		if token == "" {
			return usageErrorf("토큰이 비어 있습니다. 표준 입력으로 토큰 한 줄을 넣으세요.")
		}
		return a.updateConfig(g, func(c *Config) error {
			c.Token = token
			return nil
		}, "토큰 저장")
	},
}

// modifyConfig loads, changes and saves the config file and returns its path.
// It reads the file anew each time, so a command that runs for long does not
// write back what it read when it started.
func (a *App) modifyConfig(g *globals, change func(*Config) error) (string, error) {
	path, err := a.configPath(g)
	if err != nil {
		return "", err
	}
	cfg, err := loadConfig(path)
	if err != nil {
		return "", err
	}
	if err := change(&cfg); err != nil {
		return "", err
	}
	if err := saveConfig(path, cfg); err != nil {
		return "", fmt.Errorf("설정 파일을 쓰지 못했습니다: %w", err)
	}
	return path, nil
}

// updateConfig is modifyConfig followed by a line that says what changed.
func (a *App) updateConfig(g *globals, change func(*Config) error, what string) error {
	path, err := a.modifyConfig(g, change)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "%s: %s\n", what, path)
	return nil
}
