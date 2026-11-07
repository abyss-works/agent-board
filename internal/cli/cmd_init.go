package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wsk0715/agent-board/internal/model"
)

var initCmd = &command{
	name: "init", summary: "저장소 최상위에 .agb.json 만들기",
	usage: `사용법: agb init --project <KEY> [--root <저장소 경로>]

저장소 최상위에 .agb.json 을 만들고 그 경로를 출력합니다. 이 파일이 있는 저장소가 agb 를 지식 저장소로
씁니다. 만든 파일은 규칙(documents)이 비어 있어서 규칙을 채우기 전에는 아무 파일도 올리지 않습니다.
규칙, new, follow_up_headings 는 직접 적습니다. 서버에 접속하지 않습니다.

이미 .agb.json 이 있으면 덮어쓰지 않고 실패합니다.

옵션:
  --project, -p <KEY>     프로젝트 키 (필수)
  --root <경로>           저장소 경로. 기본 현재 폴더의 git 최상위 폴더
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fs := leafFlags("init", g)
		var project, root string
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&project, "p", "", "")
		fs.StringVar(&root, "root", "", "")
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
		if !model.ValidProjectKey(project) {
			return usageErrorf("프로젝트 키는 대문자로 시작하는 대문자와 숫자입니다: %s", project)
		}
		if root, err = repoRoot(root); err != nil {
			return err
		}
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			return usageErrorf("저장소 경로가 폴더가 아닙니다: %s", root)
		}
		raw, err := json.MarshalIndent(&repoConfig{Project: project, Documents: []docRule{}}, "", "  ")
		if err != nil {
			return err
		}
		path := filepath.Join(root, repoConfigFile)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			return usageErrorf("이미 있는 파일입니다. 덮어쓰지 않습니다: %s", path)
		}
		if err != nil {
			return err
		}
		if _, err := f.Write(append(raw, '\n')); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		_, err = fmt.Fprintln(a.Stdout, path)
		return err
	},
}
