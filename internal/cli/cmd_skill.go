package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wsk0715/agent-board/skills"
)

// skillName is the folder the skill lives in, both in the embedded tree and
// under the global skills folder.
const skillName = "tool-agb"

// versionFile records which agb release wrote an installed skill. It is not
// part of the skill, so content comparison skips it.
const versionFile = ".version"

var skillCmd = &command{name: "skill", summary: "내장 에이전트 스킬 출력과 설치", subs: []*command{
	skillShowCmd, skillInstallCmd,
}}

var skillShowCmd = &command{
	name: "show", summary: "내장된 SKILL.md 출력",
	usage: `사용법: agb skill show

이 바이너리에 내장된 tool-agb 스킬의 SKILL.md를 그대로 출력합니다.
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		pos, err := parseArgs(leafFlags("skill show", g), args)
		if err != nil {
			return err
		}
		if err := none(pos); err != nil {
			return err
		}
		b, err := fs.ReadFile(skills.FS, skillName+"/SKILL.md")
		if err != nil {
			return err
		}
		_, err = a.Stdout.Write(b)
		return err
	},
}

var skillInstallCmd = &command{
	name: "install", summary: "내장 스킬을 전역 스킬 폴더에 설치",
	usage: `사용법: agb skill install [--dir <폴더>]

이 바이너리에 내장된 tool-agb 스킬을 <폴더>/tool-agb 에 씁니다. 기본 폴더는 ~/.agents/skills 입니다.

- 설치된 파일이 내장 스킬과 같으면 아무것도 바꾸지 않고 "변경 없음"을 출력합니다.
- 다르면 그 폴더를 내장 스킬로 바꾸고, 이전과 새 agb 버전을 출력합니다. 내장 스킬에 없는 파일은 지웁니다.
- <폴더>/tool-agb 가 링크(심볼릭 링크, 정션)이면 링크만 지우고 복사본을 둡니다. 링크가 가리키던 파일은 그대로 둡니다.
- 설치한 agb 버전을 <폴더>/tool-agb/.version 에 남깁니다.

옵션:
  --dir <폴더>    전역 스킬 폴더
`,
	run: func(a *App, ctx context.Context, g *globals, args []string) error {
		fset := leafFlags("skill install", g)
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
		return a.installSkill(filepath.Join(dir, skillName))
	},
}

// defaultSkillsDir is the global skills folder the agent tools read.
func defaultSkillsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agents", "skills"), nil
}

func (a *App) installSkill(target string) error {
	embedded, err := fs.Sub(skills.FS, skillName)
	if err != nil {
		return err
	}
	fi, err := os.Lstat(target)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return a.writeSkill(embedded, target, "없음")
	case err != nil:
		return err
	case fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0:
		prev := "링크"
		if dst, err := os.Readlink(target); err == nil {
			prev = "링크(" + dst + ")"
		}
		if err := os.Remove(target); err != nil {
			return fmt.Errorf("링크 %s 를 지우지 못했습니다: %w", target, err)
		}
		return a.writeSkill(embedded, target, prev)
	}
	prev := "없음"
	if b, err := os.ReadFile(filepath.Join(target, versionFile)); err == nil {
		prev = strings.TrimSpace(string(b))
	}
	same, err := sameTree(embedded, target)
	if err != nil {
		return err
	}
	if same {
		_, err := fmt.Fprintf(a.Stdout, "변경 없음: %s (%s)\n", target, prev)
		return err
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("%s 를 지우지 못했습니다: %w", target, err)
	}
	return a.writeSkill(embedded, target, prev)
}

func (a *App) writeSkill(src fs.FS, target, prev string) error {
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(target, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		return fmt.Errorf("스킬을 %s 에 쓰지 못했습니다: %w", target, err)
	}
	if err := os.WriteFile(filepath.Join(target, versionFile), []byte(a.Version+"\n"), 0o644); err != nil {
		return err
	}
	_, err = fmt.Fprintf(a.Stdout, "설치했습니다: %s (이전 %s, 새 %s)\n", target, prev, a.Version)
	return err
}

// sameTree reports whether dir holds exactly the files of src with the same
// content, ignoring the version file.
func sameTree(src fs.FS, dir string) (bool, error) {
	want := map[string][]byte{}
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(src, p)
		want[p] = b
		return err
	})
	if err != nil {
		return false, err
	}
	same := true
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == versionFile {
			return nil
		}
		b, ok := want[rel]
		if !ok {
			same = false
			return fs.SkipAll
		}
		got, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, b) {
			same = false
			return fs.SkipAll
		}
		delete(want, rel)
		return nil
	})
	if err != nil {
		return false, err
	}
	return same && len(want) == 0, nil
}
