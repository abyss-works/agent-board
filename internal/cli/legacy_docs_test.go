package cli

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

// legacyRepo is a management repository with records of the earlier system and
// archive subfolders, all committed except one legacy decision edited later.
func legacyRepo(t *testing.T) string {
	t.Helper()
	root := gitRepo(t)
	writeFiles(t, root, map[string]string{
		"history/" + docDecisionName:              docText("decision", "현재 기록"),
		"decisions/2026-10-17-14-30-scope.md":     "# 범위\n",
		"decisions/2026-10-17-15-19-api.md":       "# API\n",
		"decisions/_template.md":                  "서식",
		"reports/결정 근거.md":                        "# 근거\n",
		"incidents/sub/nested.md":                 "하위 폴더는 이전 체계 기록이 아니다",
		"archive/2026-06-hermes/README.md":        "# 조사\n",
		"archive/2026-06-hermes/agent/notes.md":   "# 노트\n",
		"archive/2026-06-hermes/run.log":          "로그",
		"archive/2026-06-hermes/big.md":           strings.Repeat("a", model.MaxDocumentBytes+1),
		"archive/2026-06-hermes/.cache/hidden.md": "숨긴 폴더",
		"archive/2026-06-hermes/exp/_draft.md":    "서식",
	})
	for _, args := range [][]string{
		{"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "docs"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	writeFiles(t, root, map[string]string{"decisions/2026-10-17-15-19-api.md": "# API 고침\n"})
	return root
}

func TestListDocFilesCollectsLegacyRecords(t *testing.T) {
	root := legacyRepo(t)
	files, skipped, err := listDocFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, string(f.location)+":"+f.name)
	}
	want := []string{
		"history:" + docDecisionName,
		"legacy:decisions/2026-10-17-14-30-scope.md",
		"legacy:decisions/2026-10-17-15-19-api.md",
		"legacy:reports/결정 근거.md",
		"legacy:archive/2026-06-hermes/README.md",
		"legacy:archive/2026-06-hermes/agent/notes.md",
	}
	if !equalStrings(names, want) {
		t.Errorf("files = %v, want %v", names, want)
	}
	if !equalStrings(skipped, []string{"archive/2026-06-hermes/big.md (256KB 초과)"}) {
		t.Errorf("skipped = %v", skipped)
	}

	committed, err := committedDocFiles(root, files)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, f := range committed {
		kept = append(kept, f.name)
	}
	if strings.Contains(strings.Join(kept, ","), "15-19-api") || len(kept) != len(want)-1 {
		t.Errorf("committed = %v; the edited legacy decision must wait", kept)
	}
}

func TestDocFileName(t *testing.T) {
	root := legacyRepo(t)
	for path, want := range map[string]string{
		"history/" + docDecisionName:            "history:" + docDecisionName,
		"decisions/2026-10-17-14-30-scope.md":   "legacy:decisions/2026-10-17-14-30-scope.md",
		"archive/2026-06-hermes/agent/notes.md": "legacy:archive/2026-06-hermes/agent/notes.md",
	} {
		name, location, ok := docFileName(filepath.Join(root, filepath.FromSlash(path)))
		if got := string(location) + ":" + name; !ok || got != want {
			t.Errorf("docFileName(%s) = %s, %v; want %s", path, got, ok, want)
		}
	}
	for _, path := range []string{"incidents/sub/nested.md", "decisions/_template.md", "AGENTS.md"} {
		if name, _, ok := docFileName(filepath.Join(root, filepath.FromSlash(path))); ok {
			t.Errorf("docFileName(%s) = %s, want a refusal", path, name)
		}
	}
}
