package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

const (
	// claudeMarketplaceSource is the repository whose .claude-plugin/marketplace.json
	// lists the plugin, pinned to the release branch.
	claudeMarketplaceSource = "abyss-works/agent-board#main"
	// claudeMarketplace is the name in that marketplace.json.
	claudeMarketplace = "agent-board"
	// claudePlugin is the name of the plugin in plugin/.
	claudePlugin = "agb"
	// claudeTool is the name the plugin's hooks pass as --tool, which is the
	// key of the hook actor in the config file.
	claudeTool = "claude"
)

var setupCmd = &command{name: "setup", summary: "에이전트 도구에 agb 연결 (플러그인과 스킬 설치)", subs: []*command{
	setupClaudeCmd,
}, note: `설치를 실행하는 행위자(--actor <ID> 나 AGB_ACTOR)가 있어야 하고, 기본 설정 파일에 서버 주소와 그 행위자가 쓸 토큰이
있어야 합니다. 설치한 훅은 그 행위자로, 기본 설정 파일의 주소와 토큰으로 조회합니다.
`}

var setupClaudeCmd = &command{
	name: "claude", summary: "Claude Code 에 agb 플러그인과 스킬 설치",
	usage: `사용법: agb setup claude [--actor <ID>]

Claude Code 에 agb 를 연결합니다. 설치를 실행하는 행위자가 있어야 합니다(--actor, AGB_ACTOR, 설정 파일의 actor 순서).
훅은 이 셸의 환경변수 없이 실행되므로, 그 행위자가 쓸 토큰과 서버 주소가 설정 파일에 있어야 합니다(AGB_TOKEN,
AGB_URL, --url 은 세지 않습니다). 행위자, 토큰, 주소 중 하나가 없으면 아무것도 하지 않습니다. 토큰은
agb login --actor <ID> 로 받고, 주소는 agb config set url <주소> 로 저장합니다.
훅은 기본 설정 파일만 읽으므로 --config 를 주면 아무것도 하지 않고 사용법 오류로 끝납니다.

다음을 차례로 하고, 하나가 실패하면 거기서 멈춥니다.

1. 마켓플레이스 등록: claude plugin marketplace add ` + claudeMarketplaceSource + `
2. 플러그인 설치:     claude plugin install ` + claudePlugin + `@` + claudeMarketplace + `
3. 스킬 설치:         agb skill install 과 같습니다. ~/.agents/skills/tool-agb 에 쓰고, ~/.claude/skills 가
                      다른 폴더이면 거기에도 씁니다. ~/.claude/skills/tool-agb 가 링크이면 그대로 둡니다.
4. 훅 행위자 저장:    설정 파일의 hook_actors 에 ` + claudeTool + ` 의 값으로 이 실행의 행위자를 씁니다. 훅만 이 값을 쓰고,
                      다른 명령의 행위자는 바뀌지 않습니다.
5. 훅 점검:           훅과 같은 조회를 현재 폴더에서 한 번 하고 결과를 "훅 점검:" 한 줄로 알립니다. 환경변수와
                      옵션 없이 설정 파일의 값(훅 행위자, 토큰, 주소)만으로 조회합니다. 세션에 보일 메시지
                      건수이거나, 보이지 않을 이유(프로젝트를 판정할 수 없음, 서버에 닿지 않음 등)입니다.
                      점검 결과는 종료 코드를 바꾸지 않습니다.

플러그인은 세션을 시작할 때 agb hook session-start --tool ` + claudeTool + ` 를 불러 확인하지 않은 메시지를 알립니다.
훅은 저장된 훅 행위자와 Claude Code 의 세션 ID 로 조회합니다. 새 세션부터 적용됩니다.
다시 실행해도 됩니다. 이미 등록하고 설치한 것은 그대로 두고, 스킬은 이 CLI 에 내장된 것으로 맞추고, 훅 행위자는
이 실행의 행위자로 바꿉니다.
claude 실행 파일이 PATH 에 없으면 아무것도 하지 않습니다.

갱신: CLI 를 먼저 갱신한 뒤 플러그인을 갱신합니다. 플러그인이 CLI 에 없는 명령을 부르지 않게 하는 순서입니다.
  claude plugin marketplace update ` + claudeMarketplace + `
  claude plugin update ` + claudePlugin + `@` + claudeMarketplace + `
제거: claude plugin uninstall ` + claudePlugin + `@` + claudeMarketplace + `
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("setup claude", g), args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		if a.SkillsDir == "" {
			return usageErrorf("홈 폴더를 알 수 없어 스킬을 둘 곳이 없습니다.")
		}
		if g.config != "" {
			return usageErrorf("--config 와 함께 쓸 수 없습니다. 훅은 기본 설정 파일만 읽으므로 다른 설정 파일에 저장한 훅 행위자를 쓰지 못합니다. --config 없이 다시 실행하세요.")
		}
		path, err := a.configPath(g)
		if err != nil {
			return err
		}
		file, err := loadConfig(path)
		if err != nil {
			return err
		}
		actor := actorOf(g, a.Getenv, file)
		if actor == "" {
			return usageErrorf("훅이 쓸 행위자가 없습니다. --actor <ID> 나 AGB_ACTOR 로 이 설치를 실행하는 행위자를 주세요.")
		}
		if !model.ValidActorID(actor) {
			return usageErrorf("행위자 ID 형식이 아닙니다 (소문자와 숫자 1~32자).")
		}
		// The agent tool runs the hook without the environment of this
		// shell, so the hook gets its token and its address from the file.
		if tokenOf(actor, noEnv, file) == "" {
			return usageErrorf("설정 파일에 행위자 %s 가 쓸 토큰이 없습니다. 훅은 이 셸의 환경변수를 물려받지 않으므로 토큰이 설정 파일에 있어야 합니다(AGB_TOKEN 만으로는 부족합니다). `agb login --actor %s`로 토큰을 설정 파일에 받은 뒤 다시 실행하세요.", actor, actor)
		}
		if file.URL == "" {
			return usageErrorf("설정 파일에 서버 주소가 없습니다. 훅은 이 셸의 환경변수와 옵션을 물려받지 않으므로 주소가 설정 파일에 있어야 합니다(AGB_URL 이나 --url 만으로는 부족합니다). `agb config set url <주소>`로 저장한 뒤 다시 실행하세요.")
		}
		run := a.RunClaude
		if run == nil {
			run = runClaude
		}
		for _, step := range []struct {
			name string
			args []string
		}{
			{"마켓플레이스 등록", []string{"plugin", "marketplace", "add", claudeMarketplaceSource}},
			{"플러그인 설치", []string{"plugin", "install", claudePlugin + "@" + claudeMarketplace}},
		} {
			fmt.Fprintf(a.Stdout, "%s: claude %s\n", step.name, strings.Join(step.args, " "))
			out, err := run(ctx, step.args...)
			if errors.Is(err, exec.ErrNotFound) {
				return usageErrorf("claude 실행 파일이 PATH 에 없습니다. Claude Code 를 설치하고 claude 가 실행되는지 확인한 뒤 agb setup claude 를 다시 실행하세요.")
			}
			if err != nil {
				return fmt.Errorf("%s 실패 (%v):\n%s", step.name, err, strings.TrimSpace(string(out)))
			}
			a.Stdout.Write(out)
		}
		fmt.Fprintln(a.Stdout, "스킬 설치:")
		if err := a.installSkill(filepath.Join(a.SkillsDir, skillName)); err != nil {
			return err
		}
		if a.claudeNeedsOwnSkill() {
			if err := a.installSkill(filepath.Join(a.ClaudeSkillsDir, skillName)); err != nil {
				return err
			}
		}
		if err := a.updateConfig(g, func(c *Config) error {
			if c.HookActors == nil {
				c.HookActors = map[string]string{}
			}
			c.HookActors[claudeTool] = actor
			return nil
		}, "훅 행위자 저장: "+claudeTool+" → "+actor); err != nil {
			return err
		}
		fmt.Fprintln(a.Stdout, "새 Claude Code 세션부터 적용됩니다.")
		// The check tells what a session started in this folder gets: it
		// asks as the hook does when neither the environment nor a flag
		// gives it anything, with the config file alone. Its result does
		// not change how the setup ends.
		hook := *a
		hook.Getenv = noEnv
		_, summary := hook.unreadNotice(ctx, &globals{}, claudeTool, hookStdin{})
		_, err = fmt.Fprintf(a.Stdout, "훅 점검: %s\n", summary)
		return err
	},
}

// noEnv is an environment without variables.
func noEnv(string) string { return "" }

// runClaude runs the claude executable on PATH and returns what it wrote.
func runClaude(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "claude", args...).CombinedOutput()
}

// defaultClaudeSkillsDir is the global skills folder Claude Code reads.
func defaultClaudeSkillsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "skills"), nil
}

// claudeNeedsOwnSkill reports whether the skills folder of Claude Code needs
// a copy of the skill besides the one in the default folder: it is another
// folder once links are followed, and the skill in it is not a link, which is
// left as the user set it up.
func (a *App) claudeNeedsOwnSkill() bool {
	if a.ClaudeSkillsDir == "" {
		return false
	}
	if def, err := os.Stat(a.SkillsDir); err == nil {
		if cl, err := os.Stat(a.ClaudeSkillsDir); err == nil && os.SameFile(def, cl) {
			return false
		}
	}
	fi, err := os.Lstat(filepath.Join(a.ClaudeSkillsDir, skillName))
	return err != nil || fi.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0
}
