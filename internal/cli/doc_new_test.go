package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decisionTemplate is a shortened decision template.
const decisionTemplate = `---
kind: decision
date: <yyyy-mm-dd HH:MM>
project: <프로젝트 이름>
title: <문서 제목>
tags: [<주제어>, <주제어>]
author: <작성한 에이전트나 사람>
status: <채택 | 기각 | 보류>
---

# <결정 제목>

<!-- 파일명: history/yyyy-mm-dd-HHMM-decision-<slug>.md. 커밋한 뒤 고치지 않는다.
이 주석은 지운다. -->

## 배경

<왜 지금 이 결정이 필요했는지>

## 결정

- <정한 것>
`

const newDecisionName = "2026-10-17-1200-decision-pick-queue.md"

// newConfig makes the documents of history/ the ones agb doc new writes.
const newConfig = `{"project": "AGB", "documents": [{"glob": "history/*.md", "name": "base"}],
  "new": {"template": "history/_{kind}.md", "output": "history", "values": {"project": "agent-board", "author": "claude"}}}`

// templateRepo is a repository with the decision template and the config for it.
func templateRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".agb.json":            newConfig,
		"history/_decision.md": decisionTemplate,
	})
	return root
}

func TestDocNewFillsTheTemplate(t *testing.T) {
	root := templateRepo(t)
	h := newHarness(t, nil, false)
	if code := h.run("doc", "new", "decision", "--slug", "pick-queue", "--title", "큐를 고른다", "--root", root); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	path := filepath.Join(root, "history", newDecisionName)
	if got := strings.TrimSpace(h.stdout.String()); got != path {
		t.Errorf("stdout = %q, want the path %q", got, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `---
kind: decision
date: 2026-10-17 12:00
project: agent-board
title: 큐를 고른다
tags: [<주제어>, <주제어>]
author: claude
status: <채택 | 기각 | 보류>
---

# 큐를 고른다

## 배경

<왜 지금 이 결정이 필요했는지>

## 결정

- <정한 것>
`
	if string(raw) != want {
		t.Errorf("file =\n%s\nwant\n%s", raw, want)
	}
}

func TestDocNewWithoutTitleOrValues(t *testing.T) {
	root := templateRepo(t)
	writeFiles(t, root, map[string]string{".agb.json": `{"project": "AGB", "new": {"template": "history/_{kind}.md", "output": "history"}}`})
	h := newHarness(t, nil, false)
	if code := h.run("doc", "new", "decision", "--slug", "pick-queue", "--root", root); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "history", newDecisionName))
	for _, keep := range []string{"project: <프로젝트 이름>", "title: <문서 제목>", "# <결정 제목>", "date: 2026-10-17 12:00", "author: <작성한 에이전트나 사람>"} {
		if !strings.Contains(string(raw), keep) {
			t.Errorf("the file must keep %q:\n%s", keep, raw)
		}
	}
	if strings.Contains(string(raw), "파일명:") {
		t.Errorf("the guidance comment must go:\n%s", raw)
	}
}

func TestDocNewQuotesAValueYamlWouldMisread(t *testing.T) {
	root := templateRepo(t)
	h := newHarness(t, nil, false)
	if code := h.run("doc", "new", "decision", "--slug", "q", "--title", `큐: "빠른" 선택 #1`, "--root", root); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "history", newDecisionName[:len("2026-10-17-1200-decision-")]+"q.md"))
	if !strings.Contains(string(raw), `title: "큐: \"빠른\" 선택 #1"`) || !strings.Contains(string(raw), "\n# 큐: \"빠른\" 선택 #1\n") {
		t.Errorf("title not quoted:\n%s", raw)
	}
}

func TestDocNewDefaultRootIsTheGitTopLevel(t *testing.T) {
	root := gitRepo(t)
	writeFiles(t, root, map[string]string{".agb.json": newConfig, "history/_decision.md": decisionTemplate, "sub/x.txt": "x"})
	t.Chdir(filepath.Join(root, "sub"))
	h := newHarness(t, nil, false)
	if code := h.run("doc", "new", "decision", "--slug", "pick-queue"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "history", newDecisionName)); err != nil {
		t.Error(err)
	}
}

func TestDocNewReadsThePathsAndValuesFromTheConfig(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".agb.json": `{"project": "AGB", "new": {"template": "tmpl/{kind}.tpl.md", "output": "out/records", "values": {"author": "codex"}}}`,
		"tmpl/report.tpl.md": "---\nkind: report\ndate: <d>\ntitle: <t>\nauthor: <a>\n---\n\n# <제목>\n\n<!-- 안내 한 줄 -->\n\n" +
			"```md\n<!-- 코드 안의 주석 -->\n```\n\n## 절\n",
		"out/records/.keep": "",
	})
	h := newHarness(t, nil, false)
	if code := h.run("doc", "new", "report", "--slug", "survey", "--title", "조사", "--root", root); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	raw, err := os.ReadFile(filepath.Join(root, "out", "records", "2026-10-17-1200-report-survey.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nkind: report\ndate: 2026-10-17 12:00\ntitle: 조사\nauthor: codex\n---\n\n# 조사\n\n```md\n<!-- 코드 안의 주석 -->\n```\n\n## 절\n"
	if string(raw) != want {
		t.Errorf("file =\n%q\nwant\n%q", raw, want)
	}
}

const lessonTemplate = `---
kind: lesson
date: <yyyy-mm-dd HH:MM>
title: <문서 제목>
---

# <교훈>
`

func TestDocNewAcceptsAKindWithATemplate(t *testing.T) {
	root := templateRepo(t)
	writeFiles(t, root, map[string]string{"history/_lesson.md": lessonTemplate})
	h := newHarness(t, nil, false)
	if code := h.run("doc", "new", "lesson", "--slug", "retry-twice", "--title", "두 번 재시도", "--root", root); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	raw, err := os.ReadFile(filepath.Join(root, "history", "2026-10-17-1200-lesson-retry-twice.md"))
	if err != nil || !strings.Contains(string(raw), "kind: lesson") || !strings.Contains(string(raw), "# 두 번 재시도") {
		t.Errorf("file = %q (%v)", raw, err)
	}
}

func TestDocNewUsageErrors(t *testing.T) {
	root := templateRepo(t)
	writeFiles(t, root, map[string]string{"history/" + newDecisionName: "이미 있음"})
	notRepo := t.TempDir()
	noNew := t.TempDir()
	writeFiles(t, noNew, map[string]string{".agb.json": `{"project": "AGB"}`})
	h := newHarness(t, nil, false)
	for label, c := range map[string]struct {
		args []string
		want string
	}{
		"existing file":         {[]string{"decision", "--slug", "pick-queue", "--root", root}, newDecisionName},
		"missing template":      {[]string{"report", "--slug", "x", "--root", root}, filepath.Join(root, "history", "_report.md")},
		"kind without template": {[]string{"plan", "--slug", "x", "--root", root}, "plan 의 서식이 없습니다"},
		"bad kind format":       {[]string{"Plan", "--slug", "x", "--root", root}, "Plan"},
		"kind with hyphen":      {[]string{"a-b", "--slug", "x", "--root", root}, "a-b"},
		"other kind":            {[]string{"other", "--slug", "x", "--root", root}, "other"},
		"no kind":               {[]string{"--slug", "x", "--root", root}, "<종류>"},
		"no slug":               {[]string{"decision", "--root", root}, "--slug"},
		"upper case slug":       {[]string{"decision", "--slug", "Pick", "--root", root}, "slug"},
		"slug with space":       {[]string{"decision", "--slug", "a b", "--root", root}, "slug"},
		"slug with slash":       {[]string{"decision", "--slug", "../a", "--root", root}, "slug"},
		"empty slug":            {[]string{"decision", "--slug", "", "--root", root}, "--slug"},
		"too many args":         {[]string{"decision", "extra", "--slug", "x", "--root", root}, "extra"},
		"root without a config": {[]string{"decision", "--slug", "x", "--root", notRepo}, ".agb.json"},
		"config without new":    {[]string{"decision", "--slug", "x", "--root", noNew}, "new"},
	} {
		if code := h.run(append([]string{"doc", "new"}, c.args...)...); code != 2 || !strings.Contains(h.stderr.String(), c.want) {
			t.Errorf("%s: code=%d stderr=%s", label, code, h.stderr)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(root, "history", newDecisionName)); string(raw) != "이미 있음" {
		t.Errorf("an existing file must not change: %q", raw)
	}
}

func TestDocNewNeedsTheServerNot(t *testing.T) {
	root := templateRepo(t)
	h := newHarness(t, nil, false)
	delete(h.env, "AGB_TOKEN")
	delete(h.env, "AGB_URL")
	if code := h.run("doc", "new", "decision", "--slug", "pick-queue", "--root", root); code != 0 {
		t.Errorf("code=%d stderr=%s", code, h.stderr)
	}
}
