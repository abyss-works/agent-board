package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/wsk0715/agent-board/internal/client"
	"github.com/wsk0715/agent-board/skills"
)

// errCheckMismatch is returned by skill check when the CLI or the skill is
// not the release it should be. The report is already printed, so the exit
// path prints nothing more and exits with code 1.
var errCheckMismatch = errors.New("판이 맞지 않습니다")

var releasePattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// parseRelease splits a release name vX.Y.Z. ok is false for anything else,
// such as dev and v0.9.0-dev.<sha>.
func parseRelease(s string) (v [3]int, ok bool) {
	m := releasePattern.FindStringSubmatch(s)
	if m == nil {
		return v, false
	}
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func compareRelease(a, b [3]int) int {
	for i := range a {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}

var skillCheckCmd = &command{
	name: "check", summary: "CLI와 서버, 설치된 스킬의 판 차이 점검",
	usage: `사용법: agb skill check [--dir <폴더>]

CLI 판을 서버 릴리즈와, 설치된 스킬을 이 CLI에 내장된 스킬과 견줍니다. 아무것도 바꾸지 않습니다.

- 서버 /v1/health 의 release 와 CLI 판을 비교합니다. CLI가 오래됐으면 갱신 명령을 알립니다.
  개발 빌드(dev, -dev.<sha>)이거나 서버가 release 를 알리지 않으면 비교를 건너뜁니다.
  서버에 닿지 못해도 스킬 점검은 이어 하고, 서버 점검은 건너뜁니다.
- <폴더>/tool-agb 의 내용을 내장 스킬과 견줍니다(.version 제외). <폴더>/tool-agb 가 링크(심볼릭 링크,
  정션)이면 가리키는 대상을 견줍니다. 다르면 스킬에 적힌 명령이 이 CLI에 없거나 이 CLI의 명령이
  스킬에 없을 수 있습니다.

종료 코드: 0 모두 맞거나 비교를 건너뜀, 1 CLI가 서버보다 오래됐거나 스킬이 없거나 다름, 2 사용법 오류.

옵션:
  --dir <폴더>    전역 스킬 폴더 (기본 ~/.agents/skills)
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fset := leafFlags("skill check", g)
		var dir string
		fset.StringVar(&dir, "dir", "", "")
		pos, err := parseArgs(fset, args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		if dir == "" {
			dir = a.SkillsDir
		}
		if dir == "" {
			return usageErrorf("홈 폴더를 알 수 없습니다. --dir 로 전역 스킬 폴더를 주세요.")
		}
		okCLI := a.checkServerRelease(ctx, g)
		okSkill, err := a.checkSkillTree(filepath.Join(dir, skillName), okCLI)
		if err != nil {
			return err
		}
		if !okCLI || !okSkill {
			return errCheckMismatch
		}
		return nil
	},
}

// checkServerRelease prints how the CLI version relates to the server
// release. It returns false only when the CLI is older than the server.
func (a *App) checkServerRelease(ctx context.Context, g *globals) bool {
	c, err := a.client(g, false)
	if err == nil {
		var h client.Health
		h, err = c.Health(ctx)
		if err == nil {
			return a.compareWithServer(h.Release)
		}
	}
	fmt.Fprintf(a.Stdout, "서버 확인에 실패했습니다: %v\nCLI와 서버의 판 비교를 건너뜁니다.\n", err)
	return true
}

func (a *App) compareWithServer(release string) bool {
	cli, cliOK := parseRelease(a.Version)
	srv, srvOK := parseRelease(release)
	switch {
	case !cliOK:
		fmt.Fprintf(a.Stdout, "CLI %s 는 개발 빌드나 릴리즈 판이 아니어서 판 비교를 건너뜁니다.\n", a.Version)
		return true
	case release == "":
		fmt.Fprintf(a.Stdout, "CLI %s. 서버가 릴리즈를 알리지 않아 판 비교를 건너뜁니다.\n", a.Version)
		return true
	case !srvOK:
		fmt.Fprintf(a.Stdout, "CLI %s, 서버 %s. 서버 릴리즈를 읽지 못해 판 비교를 건너뜁니다.\n", a.Version, release)
		return true
	}
	switch compareRelease(cli, srv) {
	case -1:
		fmt.Fprintf(a.Stdout, "CLI %s, 서버 %s: CLI가 서버보다 오래됐습니다. 스킬에 적힌 명령이 이 CLI에 없을 수 있습니다.\n", a.Version, release)
		fmt.Fprintf(a.Stdout, "갱신: go install -trimpath -ldflags \"-X main.version=%s\" github.com/wsk0715/agent-board/cmd/agb@main\n", release)
		return false
	case 1:
		fmt.Fprintf(a.Stdout, "CLI %s, 서버 %s: 서버가 CLI보다 오래됐습니다. 서버 배포를 기다리는 중일 수 있습니다.\n", a.Version, release)
	default:
		fmt.Fprintf(a.Stdout, "CLI %s, 서버 %s: 같음\n", a.Version, release)
	}
	return true
}

// checkSkillTree compares the skill at target, or what a link at target
// points to, with the embedded skill. It returns false when the skill is
// missing or differs.
func (a *App) checkSkillTree(target string, cliCurrent bool) (bool, error) {
	fi, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(a.Stdout, "스킬: %s 에 설치되어 있지 않습니다. `agb skill install`로 설치하세요.\n", target)
		if cliCurrent {
			fmt.Fprintf(a.Stdout, "설치하지 않고 `agb skill show`로 읽어 쓴다면 이 결과는 예상한 것입니다. 종료 코드 1은 스킬이 설치되지 않았다는 뜻일 뿐입니다.\n")
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	dir, isLink := target, fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0
	if isLink {
		dir, err = os.Readlink(target)
		if err == nil && !filepath.IsAbs(dir) {
			dir = filepath.Join(filepath.Dir(target), dir)
		}
		if err != nil {
			fmt.Fprintf(a.Stdout, "스킬: %s 는 링크이고 가리키는 대상을 열지 못했습니다: %v\n", target, err)
			return false, nil
		}
		fmt.Fprintf(a.Stdout, "스킬: %s 는 링크이고 %s 를 가리킵니다.\n", target, dir)
	}
	embedded, err := fs.Sub(skills.FS, skillName)
	if err != nil {
		return false, err
	}
	same, err := sameTree(embedded, dir)
	if err != nil {
		return false, fmt.Errorf("스킬 %s 를 읽지 못했습니다: %w", dir, err)
	}
	switch {
	case same:
		fmt.Fprintf(a.Stdout, "스킬: 같음 (%s)\n", dir)
	case isLink:
		fmt.Fprintf(a.Stdout, "스킬: 링크 대상이 이 CLI에 내장된 스킬과 다릅니다. 스킬에 적힌 명령이 이 CLI에 없거나, 이 CLI의 명령이 스킬에 없을 수 있습니다.\n")
		fmt.Fprintf(a.Stdout, "맞추기: 링크가 저장소를 가리키면 그 브랜치에서 CLI를 다시 설치하세요. 이 CLI에 맞는 본문은 `agb skill show`로 읽을 수 있습니다.\n")
	default:
		fmt.Fprintf(a.Stdout, "스킬: %s 가 이 CLI에 내장된 스킬과 다릅니다. 스킬에 적힌 명령이 이 CLI에 없거나, 이 CLI의 명령이 스킬에 없을 수 있습니다.\n", dir)
		fmt.Fprintf(a.Stdout, "맞추기: `agb skill install`로 내장 스킬을 설치하세요.\n")
	}
	return same, nil
}
