package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// claudeMarketplaceSource is the repository whose .claude-plugin/marketplace.json
	// lists the plugin, pinned to the release branch.
	claudeMarketplaceSource = "abyss-works/agent-board#main"
	// claudeMarketplace is the name in that marketplace.json.
	claudeMarketplace = "agent-board"
	// claudePlugin is the name of the plugin in plugin/.
	claudePlugin = "agb"
)

var setupCmd = &command{name: "setup", summary: "에이전트 도구에 agb 연결 (플러그인과 스킬 설치)", subs: []*command{
	setupClaudeCmd,
}}

var setupClaudeCmd = &command{
	name: "claude", summary: "Claude Code 에 agb 플러그인과 스킬 설치",
	usage: `사용법: agb setup claude

Claude Code 에 agb 를 연결합니다. 다음 셋을 차례로 하고, 하나가 실패하면 거기서 멈춥니다.

1. 마켓플레이스 등록: claude plugin marketplace add ` + claudeMarketplaceSource + `
2. 플러그인 설치:     claude plugin install ` + claudePlugin + `@` + claudeMarketplace + `
3. 스킬 설치:         agb skill install 과 같습니다. ~/.agents/skills/tool-agb 에 쓰고, ~/.claude/skills 가
                      다른 폴더이면 거기에도 씁니다. ~/.claude/skills/tool-agb 가 링크이면 그대로 둡니다.

플러그인은 세션을 시작할 때 agb hook session-start 를 불러 확인하지 않은 메시지를 알립니다. 새 세션부터 적용됩니다.
다시 실행해도 됩니다. 이미 등록하고 설치한 것은 그대로 두고, 스킬은 이 CLI 에 내장된 것으로 맞춥니다.
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
		_, err = fmt.Fprintln(a.Stdout, "새 Claude Code 세션부터 적용됩니다.")
		return err
	},
}

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
