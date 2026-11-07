package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// repoConfigFile is the configuration file at the top of a repository that
// uses agb as its knowledge store. It exists only in such repositories; the
// server never reads it.
const repoConfigFile = ".agb.json"

// repoConfig is the content of .agb.json.
type repoConfig struct {
	// Project is the key of the agb project the documents of the repository go to.
	Project string `json:"project"`
	// AutoSync lets commands that agents call anyway upload the committed
	// documents that the server lacks.
	AutoSync bool `json:"auto_sync"`
	// Documents are the rules that say which files are uploaded and how. The
	// first rule that matches a file is the one used.
	Documents []docRule `json:"documents"`
	// New is what agb doc new needs; it is absent when the repository does not
	// write documents from a template.
	New *newDocConfig `json:"new,omitempty"`
	// FollowUpHeadings are the "## " headings of a document that name work left
	// open.
	FollowUpHeadings []string `json:"follow_up_headings,omitempty"`
}

// docRule says how the files that its glob matches are uploaded.
type docRule struct {
	// Glob is a path from the repository root with slashes: * stands for any
	// characters within one segment, ? for one character, and a segment ** for
	// any number of folders.
	Glob string `json:"glob"`
	// Name is how the document is named on the server: base is the file name and
	// path is the path from the repository root.
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty"`
	Archived bool   `json:"archived,omitempty"`
	Imported bool   `json:"imported,omitempty"`
}

const (
	nameBase = "base"
	namePath = "path"
)

// newDocConfig is the template of agb doc new. Template is the path of the
// template file from the repository root, where {kind} stands for the kind of
// the document; Output is the folder the new file goes to; Values fill the
// front matter keys of the same name.
type newDocConfig struct {
	Template string            `json:"template"`
	Output   string            `json:"output"`
	Values   map[string]string `json:"values,omitempty"`
}

// parseRepoConfig reads the content of a .agb.json. A field it does not know,
// a value of the wrong type and a value that does not make sense are refused,
// so a mistake in the file does not silently upload nothing.
func parseRepoConfig(raw []byte) (*repoConfig, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var cfg repoConfig
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%s를 읽지 못했습니다: %w", repoConfigFile, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s를 읽지 못했습니다: JSON 값 뒤에 다른 내용이 있습니다.", repoConfigFile)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", repoConfigFile, err)
	}
	return &cfg, nil
}

func (c *repoConfig) validate() error {
	if !model.ValidProjectKey(c.Project) {
		return fmt.Errorf("project: 프로젝트 키가 필요합니다. 대문자로 시작하는 대문자와 숫자입니다: %q", c.Project)
	}
	for i, r := range c.Documents {
		field := fmt.Sprintf("documents[%d]", i)
		if err := validGlob(r.Glob); err != nil {
			return fmt.Errorf("%s.glob: %w", field, err)
		}
		if r.Name != nameBase && r.Name != namePath {
			return fmt.Errorf("%s.name: base 또는 path여야 합니다: %q", field, r.Name)
		}
		if r.Kind != "" {
			kind, ok := model.ParseDocumentKind(r.Kind)
			if !ok {
				return fmt.Errorf("%s.kind: 문서 종류가 아닙니다: %q", field, r.Kind)
			}
			if kind == model.DocumentOther && !r.Imported {
				return fmt.Errorf("%s.kind: other는 imported 규칙에만 쓸 수 있습니다.", field)
			}
		}
	}
	if n := c.New; n != nil {
		if n.Template == "" {
			return errors.New("new.template: 서식 파일 경로가 필요합니다.")
		}
		if n.Output == "" {
			return errors.New("new.output: 새 파일을 둘 폴더가 필요합니다.")
		}
		for k := range n.Values {
			if k == "" {
				return errors.New("new.values: 키가 비어 있습니다.")
			}
		}
	}
	for i, h := range c.FollowUpHeadings {
		if strings.TrimSpace(h) == "" {
			return fmt.Errorf("follow_up_headings[%d]: 비어 있습니다.", i)
		}
	}
	return nil
}

// validGlob checks a rule glob: a relative path whose segments are not empty,
// where ** stands alone as a segment.
func validGlob(g string) error {
	switch {
	case g == "":
		return errors.New("패턴이 필요합니다.")
	case strings.HasPrefix(g, "/"), strings.Contains(g, "\\"):
		return fmt.Errorf("저장소 루트에서 본 상대 경로를 /로 나눠 써야 합니다: %q", g)
	}
	for _, seg := range strings.Split(g, "/") {
		if seg == "" {
			return fmt.Errorf("빈 조각이 있습니다: %q", g)
		}
		if strings.Contains(seg, "**") && seg != "**" {
			return fmt.Errorf("**는 조각 하나를 통째로 차지해야 합니다: %q", g)
		}
	}
	return nil
}

// loadRepoConfig reads the .agb.json at the top of root. It returns nil, nil
// when the repository has none.
func loadRepoConfig(root string) (*repoConfig, error) {
	raw, err := os.ReadFile(filepath.Join(root, repoConfigFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s를 읽지 못했습니다: %w", repoConfigFile, err)
	}
	return parseRepoConfig(raw)
}

// ruleFor returns the first rule whose glob matches rel, a path from the
// repository root with slashes, or nil.
func (c *repoConfig) ruleFor(rel string) *docRule {
	for i := range c.Documents {
		if globMatch(c.Documents[i].Glob, rel) {
			return &c.Documents[i]
		}
	}
	return nil
}

// docName is the name the document of the file at rel has on the server.
func (r docRule) docName(rel string) string {
	if r.Name == namePath {
		return rel
	}
	return path.Base(rel)
}

// globMatch reports whether the slash separated path p matches the pattern.
func globMatch(pattern, p string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func matchSegments(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		if len(pat) == 1 {
			return len(segs) > 0
		}
		for i := 0; i <= len(segs); i++ {
			if matchSegments(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	return len(segs) > 0 && matchSegment([]rune(pat[0]), []rune(segs[0])) && matchSegments(pat[1:], segs[1:])
}

// matchSegment matches one path segment against a pattern of ordinary
// characters, ? and *.
func matchSegment(pat, s []rune) bool {
	for len(pat) > 0 {
		switch pat[0] {
		case '*':
			for i := 0; i <= len(s); i++ {
				if matchSegment(pat[1:], s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
		default:
			if len(s) == 0 || s[0] != pat[0] {
				return false
			}
		}
		pat, s = pat[1:], s[1:]
	}
	return len(s) == 0
}
