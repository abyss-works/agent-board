package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

var configCmd = &command{name: "config", summary: "설정 파일과 접속 대상 관리", bareHelp: true, subs: []*command{
	configPathCmd, configShowCmd, configSetCmd, configUnsetCmd, configSetTokenCmd,
	configSetURLCmd, configSetActorCmd,
	configListContextsCmd, configSetContextCmd, configUseContextCmd, configCurrentContextCmd, configDeleteContextCmd,
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
	name: "show", summary: "현재 선택된 대상과 설정 파일 내용 표시 (토큰은 가림)",
	usage: `사용법: agb config show [--json] [--context <이름>]

현재 선택된 접속 대상(--context, AGB_CONTEXT, current_context 순)과 기기 공통 설정 값을 보여 줍니다.
토큰 값은 보여 주지 않습니다. 공용 토큰은 저장 여부만, 행위자별 토큰은 토큰이 있는 행위자의 이름만 보여 줍니다.
hook_actors 는 agb setup <대상> 이 저장한, 도구별 훅 행위자입니다.
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
		ctxName, cc, err := resolveContext(g, a.Getenv, cfg)
		if err != nil {
			return err
		}
		actorTokens := sortedTokenActors(cc.Tokens)
		hookActors := make([]string, 0, len(cfg.HookActors))
		for tool, actor := range cfg.HookActors {
			hookActors = append(hookActors, tool+"="+actor)
		}
		sort.Strings(hookActors)
		if a.jsonOutput(g) {
			raw, _ := json.Marshal(map[string]any{
				"path": path, "context": ctxName, "url": cc.URL, "actor": cc.Actor,
				"session_id": cfg.SessionID, "client_host": cfg.ClientHost,
				"token_set": cc.Token != "", "actor_tokens": actorTokens, "hook_actors": orEmptyMap(cfg.HookActors),
			})
			writeJSON(a.Stdout, raw)
			return nil
		}
		token := "(없음)"
		if cc.Token != "" {
			token = "(설정됨)"
		}
		fields(a.Stdout, [][2]string{
			{"path", path},
			{"context", orDash(ctxName)},
			{"url", orDash(cc.URL)}, {"actor", orDash(cc.Actor)}, {"session_id", orDash(cfg.SessionID)}, {"client_host", orDash(cfg.ClientHost)},
			{"token", token},
			{"actor_tokens", joinOr(actorTokens, "(없음)")},
			{"hook_actors", joinOr(hookActors, "(없음)")},
		})
		return nil
	},
}

func sortedTokenActors(tokens map[string]string) []string {
	out := make([]string, 0, len(tokens))
	for actor, tok := range tokens {
		if tok != "" {
			out = append(out, actor)
		}
	}
	sort.Strings(out)
	return out
}

// orEmptyMap is m, or an empty map for a nil one, which JSON writes as {}.
func orEmptyMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

var configSetCmd = &command{
	name: "set", summary: "설정 값 저장 (url, actor, session_id, client_host)",
	usage: `사용법: agb config set <키> <값> [--context <이름>]

키:
  url           선택된 접속 대상의 서버 주소 (예: http://127.0.0.1:8420)
  actor         선택된 접속 대상의 행위자 ID (소문자와 숫자 1~32자)
  session_id    기기 공통 세션 ID. 비우면 cli-YYYYMMDD 를 씁니다
  client_host   기기 공통 기기 이름. 비우면 호스트 이름을 씁니다

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
		return a.setConfigKeyValue(g, pos[0], pos[1])
	},
}

var configSetURLCmd = &command{
	name: "set-url", summary: "선택된 접속 대상의 서버 주소 저장",
	usage: `사용법: agb config set-url <주소> [--context <이름>]

선택된 접속 대상의 서버 주소를 저장합니다 (agb config set url <주소> 와 같음).
`,
	run: func(a *App, _ context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("config set-url", g), args)
		if err != nil {
			return err
		}
		val, err := one(pos, "<주소>")
		if err != nil {
			return err
		}
		return a.setConfigKeyValue(g, "url", val)
	},
}

var configSetActorCmd = &command{
	name: "set-actor", summary: "선택된 접속 대상의 행위자 저장",
	usage: `사용법: agb config set-actor <ID> [--context <이름>]

선택된 접속 대상의 기본 행위자 ID 를 저장합니다 (agb config set actor <ID> 와 같음).
`,
	run: func(a *App, _ context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("config set-actor", g), args)
		if err != nil {
			return err
		}
		val, err := one(pos, "<ID>")
		if err != nil {
			return err
		}
		return a.setConfigKeyValue(g, "actor", val)
	},
}

func (a *App) setConfigKeyValue(g *globals, key, rawValue string) error {
	value := strings.TrimSpace(rawValue)
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
		c.normalize()
		switch key {
		case "session_id":
			c.SessionID = value
			return nil
		case "client_host":
			c.ClientHost = value
			return nil
		case "url", "actor":
			name, err := writableContextName(g, a.Getenv, c)
			if err != nil {
				return err
			}
			if c.Contexts == nil {
				c.Contexts = map[string]ContextConfig{}
			}
			cc := c.Contexts[name]
			if key == "url" {
				cc.URL = value
			} else {
				cc.Actor = value
			}
			c.Contexts[name] = cc
			return nil
		}
		return usageErrorf("모르는 키 %q. 키는 %s 중 하나입니다.", key, strings.Join(configKeys, ", "))
	}, key+" 저장")
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
	usage: `사용법: agb config unset <키> [--context <이름>]

키는 url, actor, session_id, client_host, token 중 하나입니다.
url, actor, token 은 선택된 접속 대상에서 지웁니다.
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
			c.normalize()
			switch key {
			case "session_id":
				c.SessionID = ""
				return nil
			case "client_host":
				c.ClientHost = ""
				return nil
			case "url", "actor", "token":
				name, err := writableContextName(g, a.Getenv, c)
				if err != nil {
					return err
				}
				if c.Contexts == nil {
					return nil
				}
				cc := c.Contexts[name]
				switch key {
				case "url":
					cc.URL = ""
				case "actor":
					cc.Actor = ""
				case "token":
					cc.Token = ""
				}
				c.Contexts[name] = cc
				return nil
			}
			return usageErrorf("모르는 키 %q. 키는 %s, token 중 하나입니다.", key, strings.Join(configKeys, ", "))
		}, key+" 삭제")
	},
}

var configSetTokenCmd = &command{
	name: "set-token", summary: "공용 토큰 저장 (stdin에서 읽음)",
	usage: `사용법: agb config set-token [--context <이름>] < 토큰파일
        echo "$TOKEN" | agb config set-token [--context <이름>]

토큰을 표준 입력의 첫 줄에서 읽어 선택된 접속 대상에 저장합니다. 터미널에서 실행하면
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
			name, err := writableContextName(g, a.Getenv, c)
			if err != nil {
				return err
			}
			if c.Contexts == nil {
				c.Contexts = map[string]ContextConfig{}
			}
			cc := c.Contexts[name]
			cc.Token = token
			c.Contexts[name] = cc
			return nil
		}, "토큰 저장")
	},
}

type contextSummary struct {
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	Actor       string   `json:"actor"`
	Current     bool     `json:"current"`
	TokenSet    bool     `json:"token_set"`
	ActorTokens []string `json:"actor_tokens"`
}

var configListContextsCmd = &command{
	name: "list-contexts", aliases: []string{"contexts", "get-contexts"},
	summary: "등록된 접속 대상 목록 출력 (토큰은 가림)",
	usage: `사용법: agb config list-contexts [--json]

등록된 접속 대상(이름, url, actor, 활성 여부, 토큰 저장 여부)을 출력합니다.
토큰 원문은 어떤 출력에도 포함하지 않습니다.
`,
	run: func(a *App, _ context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("config list-contexts", g), args)
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
		cfg.normalize()
		activeName, _, err := resolveContext(g, a.Getenv, cfg)
		if err != nil {
			return err
		}
		names := make([]string, 0, len(cfg.Contexts))
		for name := range cfg.Contexts {
			names = append(names, name)
		}
		sort.Strings(names)
		items := make([]contextSummary, 0, len(names))
		for _, name := range names {
			cc := cfg.Contexts[name]
			urlVal := cc.URL
			if name == localContextName && urlVal == "" {
				urlVal = defaultLocalURL
			}
			items = append(items, contextSummary{
				Name:        name,
				URL:         urlVal,
				Actor:       cc.Actor,
				Current:     name == activeName,
				TokenSet:    cc.Token != "",
				ActorTokens: sortedTokenActors(cc.Tokens),
			})
		}
		return a.emitValue(g, items, func(w io.Writer) {
			if len(items) == 0 {
				fmt.Fprintln(w, "등록된 접속 대상이 없습니다.")
				return
			}
			tb := &table{header: []string{"CURRENT", "NAME", "URL", "ACTOR", "TOKEN", "ACTOR_TOKENS"}}
			for _, it := range items {
				cur := ""
				if it.Current {
					cur = "*"
				}
				tok := "(없음)"
				if it.TokenSet {
					tok = "(설정됨)"
				}
				tb.add(cur, it.Name, orDash(it.URL), orDash(it.Actor), tok, joinOr(it.ActorTokens, "(없음)"))
			}
			tb.write(w, a.TermWidth)
		})
	},
}

var configSetContextCmd = &command{
	name: "set-context", summary: "접속 대상 추가 또는 수정",
	usage: `사용법: agb config set-context <이름> [--url <주소>] [--actor <ID>] [--use]

이름 붙인 접속 대상을 추가하거나 기존 대상의 속성을 바꿉니다.
새 대상을 만들 때는 --url 이 필수입니다(단 이름이 local 이면 생략 시 http://127.0.0.1:8420).

옵션:
  --url <주소>     서버 주소 (http:// 또는 https://)
  --actor <ID>     기본 행위자 ID
  --use            current_context 도 이 대상으로 전환
`,
	run: func(a *App, _ context.Context, g *globals, args []string) error {
		fs := newFlagSet("config set-context")
		fs.BoolVar(&g.json, "json", g.json, "")
		fs.BoolVar(&g.table, "table", g.table, "")
		fs.StringVar(&g.config, "config", g.config, "")
		var targetURL, targetActor string
		var use bool
		fs.StringVar(&targetURL, "url", "", "")
		fs.StringVar(&targetActor, "actor", "", "")
		fs.BoolVar(&use, "use", false, "")
		pos, err := parseArgs(fs, args)
		if err != nil {
			return err
		}
		name, err := one(pos, "<이름>")
		if err != nil {
			return err
		}
		if !validContextName(name) {
			return usageErrorf("접속 대상 이름 %q 형식이 올바르지 않습니다 (빈 문자열, 공백, 제어문자는 쓸 수 없습니다).", name)
		}
		urlSet := isSet(fs, "url")
		actorSet := isSet(fs, "actor")
		if urlSet {
			targetURL = strings.TrimSpace(targetURL)
			if err := validateConfigValue("url", targetURL); err != nil {
				return err
			}
			targetURL = strings.TrimRight(targetURL, "/")
		}
		if actorSet && targetActor != "" {
			if err := validateConfigValue("actor", targetActor); err != nil {
				return err
			}
		}
		return a.updateConfig(g, func(c *Config) error {
			c.normalize()
			if c.Contexts == nil {
				c.Contexts = map[string]ContextConfig{}
			}
			cc, exists := c.Contexts[name]
			if !exists {
				if !urlSet {
					if name == localContextName {
						cc.URL = defaultLocalURL
					} else {
						return usageErrorf("새 접속 대상 %q 을 만들 때는 --url <주소> 가 필요합니다.", name)
					}
				}
			}
			if urlSet {
				cc.URL = targetURL
			}
			if actorSet {
				cc.Actor = targetActor
			}
			c.Contexts[name] = cc
			if use {
				c.CurrentContext = name
			}
			return nil
		}, "접속 대상 저장: "+name)
	},
}

var configUseContextCmd = &command{
	name: "use-context", summary: "활성 접속 대상 전환",
	usage: `사용법: agb config use-context <이름>

current_context 를 등록된 대상(또는 내장 local)으로 전환합니다.
`,
	run: func(a *App, _ context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("config use-context", g), args)
		if err != nil {
			return err
		}
		name, err := one(pos, "<이름>")
		if err != nil {
			return err
		}
		if !validContextName(name) {
			return usageErrorf("접속 대상 이름 %q 형식이 올바르지 않습니다.", name)
		}
		return a.updateConfig(g, func(c *Config) error {
			c.normalize()
			if _, ok := c.Contexts[name]; !ok {
				if name == localContextName {
					if c.Contexts == nil {
						c.Contexts = map[string]ContextConfig{}
					}
					c.Contexts[localContextName] = ContextConfig{URL: defaultLocalURL}
				} else {
					return usageErrorf("접속 대상 %q 이 등록되어 있지 않습니다.", name)
				}
			}
			c.CurrentContext = name
			return nil
		}, "활성 접속 대상 전환: "+name)
	},
}

var configCurrentContextCmd = &command{
	name: "current-context", summary: "현재 선택된 접속 대상 이름 출력",
	usage: `사용법: agb config current-context

현재 선택된 접속 대상 이름(--context, AGB_CONTEXT, current_context 순)을 출력합니다.
`,
	run: func(a *App, _ context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("config current-context", g), args)
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
		name, _, err := resolveContext(g, a.Getenv, cfg)
		if err != nil {
			return err
		}
		if a.jsonOutput(g) && g.json {
			raw, _ := json.Marshal(map[string]string{"context": name})
			writeJSON(a.Stdout, raw)
			return nil
		}
		fmt.Fprintln(a.Stdout, name)
		return nil
	},
}

var configDeleteContextCmd = &command{
	name: "delete-context", summary: "접속 대상 삭제",
	usage: `사용법: agb config delete-context <이름>

등록된 접속 대상을 설정 파일에서 삭제합니다. 활성 대상을 삭제하면 남은 대상 중 default 가 있으면
default 로, 없으면 빈 값으로 둡니다.
`,
	run: func(a *App, _ context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("config delete-context", g), args)
		if err != nil {
			return err
		}
		name, err := one(pos, "<이름>")
		if err != nil {
			return err
		}
		if !validContextName(name) {
			return usageErrorf("접속 대상 이름 %q 형식이 올바르지 않습니다.", name)
		}
		return a.updateConfig(g, func(c *Config) error {
			c.normalize()
			if _, ok := c.Contexts[name]; !ok {
				return usageErrorf("접속 대상 %q 이 등록되어 있지 않습니다.", name)
			}
			delete(c.Contexts, name)
			if c.CurrentContext == name {
				if _, hasDefault := c.Contexts[defaultContextName]; hasDefault {
					c.CurrentContext = defaultContextName
				} else {
					c.CurrentContext = ""
				}
			}
			return nil
		}, "접속 대상 삭제: "+name)
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
