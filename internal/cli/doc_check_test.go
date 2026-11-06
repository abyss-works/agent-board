package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const filledDecision = `---
kind: decision
date: 2026-10-17 12:00
project: agent-board
title: 큐를 고른다
tags: [queue]
author: claude
status: 채택
---

# 큐를 고른다

## 배경

이유.

## 결정

- 큐를 쓴다.
`

const (
	checkedName  = "2026-10-17-1200-decision-checked.md"
	freshName    = "2026-10-17-1201-decision-fresh.md"
	goodNewName  = "2026-10-17-1202-decision-good-new.md"
	otherDirName = "2026-10-17-1203-decision-archived.md"
)

// checkRepo is a git repository with the decision template and one committed
// record that has a remnant; the committed file is not checked unless named.
func checkRepo(t *testing.T) string {
	t.Helper()
	root := gitRepo(t)
	writeFiles(t, root, map[string]string{
		"history/_decision.md":   decisionTemplate,
		"history/" + checkedName: decisionTemplate,
		"README.md":              "x",
	})
	gitRun(t, root, "add", "history", "README.md")
	gitRun(t, root, "commit", "-q", "-m", "docs")
	return root
}

type checkOutput struct {
	Checked int `json:"checked"`
	Files   []struct {
		Name     string   `json:"name"`
		Problems []string `json:"problems"`
	} `json:"files"`
	Notes []string `json:"notes"`
}

func TestDocCheckPassesAFilledFile(t *testing.T) {
	root := checkRepo(t)
	writeFiles(t, root, map[string]string{"history/" + goodNewName: filledDecision})
	h := newHarness(t, nil, false)
	if code := h.run("doc", "check", "--root", root); code != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, h.stdout, h.stderr)
	}
	var got checkOutput
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil || got.Checked != 1 || len(got.Files) != 0 {
		t.Errorf("out = %s (%v)", h.stdout, err)
	}
	if !strings.Contains(h.stdout.String(), `"files":[]`) {
		t.Errorf("files must be a list, never null: %s", h.stdout)
	}
}

func TestDocCheckFailsAFreshFileFromDocNew(t *testing.T) {
	root := templateRepo(t)
	gitRun(t, root, "init", "-q")
	h := newHarness(t, nil, false)
	if code := h.run("doc", "new", "decision", "--slug", "pick-queue", "--root", root); code != 0 {
		t.Fatalf("new: code=%d %s", code, h.stderr)
	}
	if code := h.run("doc", "check", "--root", root); code != 1 {
		t.Fatalf("code=%d out=%s err=%s", code, h.stdout, h.stderr)
	}
	var got checkOutput
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil || len(got.Files) != 1 || got.Files[0].Name != "history/"+newDecisionName {
		t.Fatalf("out = %s (%v)", h.stdout, err)
	}
	joined := strings.Join(got.Files[0].Problems, "\n")
	for _, want := range []string{"tags", "status", "<결정 제목>", "<왜 지금 이 결정이 필요했는지>", "<정한 것>"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q:\n%s", want, joined)
		}
	}
	for _, not := range []string{"<!--", "date 값", "author 값", "project 값"} {
		if strings.Contains(joined, not) {
			t.Errorf("problems must not mention %q:\n%s", not, joined)
		}
	}
}

func TestDocCheckPicksNewAndModifiedFiles(t *testing.T) {
	root := checkRepo(t)
	writeFiles(t, root, map[string]string{
		"history/" + freshName:     decisionTemplate,            // untracked
		"history/" + goodNewName:   filledDecision,              // untracked
		"history/" + checkedName:   decisionTemplate + "\n수정\n", // modified
		"archive/" + otherDirName:  decisionTemplate,            // untracked in archive
		"history/_decision.md":     decisionTemplate + "\n",     // template: never checked
		"history/notes.md":         "이름이 기록 형식이 아닌 파일",          // name problem
		"history/sub/" + freshName: decisionTemplate,            // not a direct child
		"decisions/" + freshName:   decisionTemplate,            // legacy folder
	})
	h := newHarness(t, nil, false)
	if code := h.run("doc", "check", "--root", root); code != 1 {
		t.Fatalf("code=%d", code)
	}
	var got checkOutput
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range got.Files {
		names = append(names, f.Name)
	}
	want := []string{"archive/" + otherDirName, "history/" + checkedName, "history/" + freshName, "history/notes.md"}
	if !equalStrings(names, want) || got.Checked != 5 {
		t.Errorf("files = %v checked=%d, want %v checked=5", names, got.Checked, want)
	}
}

func TestDocCheckStagedFileAndNothingToCheck(t *testing.T) {
	root := checkRepo(t)
	h := newHarness(t, nil, false)
	if code := h.run("doc", "check", "--root", root); code != 0 {
		t.Errorf("nothing changed: code=%d out=%s", code, h.stdout)
	}
	writeFiles(t, root, map[string]string{"history/" + freshName: decisionTemplate})
	gitRun(t, root, "add", "history")
	if code := h.run("doc", "check", "--root", root); code != 1 || !strings.Contains(h.stdout.String(), freshName) {
		t.Errorf("staged new file: code=%d out=%s", code, h.stdout)
	}
	gitRun(t, root, "rm", "-q", "-f", "history/"+freshName)
	if code := h.run("doc", "check", "--root", root); code != 0 {
		t.Errorf("deleted file: code=%d out=%s", code, h.stdout)
	}
}

func TestDocCheckNamedFiles(t *testing.T) {
	root := checkRepo(t)
	writeFiles(t, root, map[string]string{
		"history/" + goodNewName: filledDecision,
		"history/" + freshName:   strings.Replace(filledDecision, "## 배경\n\n이유.\n\n", "", 1),
	})
	h := newHarness(t, nil, false)
	// The committed file is checked when named; the unnamed files are not.
	if code := h.run("doc", "check", "--root", root, filepath.Join(root, "history", checkedName)); code != 1 || !strings.Contains(h.stdout.String(), checkedName) || strings.Contains(h.stdout.String(), freshName) {
		t.Errorf("named committed: code=%d out=%s", code, h.stdout)
	}
	if code := h.run("doc", "check", "--root", root, filepath.Join(root, "history", goodNewName)); code != 0 {
		t.Errorf("named good: code=%d out=%s", code, h.stdout)
	}
	// Missing heading.
	code := h.run("doc", "check", "--root", root, filepath.Join(root, "history", freshName))
	var got checkOutput
	if err := json.Unmarshal(h.stdout.Bytes(), &got); code != 1 || err != nil || len(got.Files) != 1 || len(got.Files[0].Problems) != 1 || !strings.Contains(got.Files[0].Problems[0], "## 배경") {
		t.Errorf("missing heading: code=%d out=%s", code, h.stdout)
	}
	// A missing file is a problem of that file; the rest is still checked.
	if code := h.run("doc", "check", "--root", root, filepath.Join(root, "history", "nope.md"), filepath.Join(root, "history", goodNewName)); code != 1 || !strings.Contains(h.stdout.String(), "nope.md") {
		t.Errorf("missing file: code=%d out=%s", code, h.stdout)
	}
}

func TestDocCheckHeadingsOutOfOrder(t *testing.T) {
	root := checkRepo(t)
	swapped := strings.Replace(filledDecision, "## 배경\n\n이유.\n\n## 결정\n\n- 큐를 쓴다.\n", "## 결정\n\n- 큐를 쓴다.\n\n## 배경\n\n이유.\n", 1)
	writeFiles(t, root, map[string]string{"history/" + goodNewName: swapped})
	h := newHarness(t, nil, false)
	if code := h.run("doc", "check", "--root", root); code != 1 || !strings.Contains(h.stdout.String(), "순서") {
		t.Errorf("code=%d out=%s", code, h.stdout)
	}
}

func TestDocCheckIgnoresRemnantsInCodeBlocks(t *testing.T) {
	root := checkRepo(t)
	body := strings.Replace(filledDecision, "이유.", "서식은 이렇다.\n\n```md\n<!-- 파일명: x. 이 주석은 지운다. -->\n<왜 지금 이 결정이 필요했는지>\n```\n", 1)
	writeFiles(t, root, map[string]string{"history/" + goodNewName: body})
	h := newHarness(t, nil, false)
	if code := h.run("doc", "check", "--root", root); code != 0 {
		t.Errorf("code=%d out=%s", code, h.stdout)
	}
}

func TestDocCheckWithoutATemplateSaysSo(t *testing.T) {
	root := checkRepo(t)
	gitRun(t, root, "rm", "-q", "history/_decision.md")
	gitRun(t, root, "commit", "-q", "-m", "no template")
	writeFiles(t, root, map[string]string{
		"history/" + goodNewName: strings.Replace(filledDecision, "status: 채택\n", "", 1),
		"history/" + freshName:   filledDecision + "\n<!-- 메모 -->\n",
	})
	h := newHarness(t, nil, false)
	if code := h.run("doc", "check", "--root", root); code != 1 {
		t.Fatalf("code=%d out=%s", code, h.stdout)
	}
	var got checkOutput
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// The key check is skipped, the comment check is not; the note appears once.
	if len(got.Files) != 1 || got.Files[0].Name != "history/"+freshName || len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "_decision.md") {
		t.Errorf("out = %s", h.stdout)
	}
}

func TestDocCheckTextOutputOnTerminal(t *testing.T) {
	root := checkRepo(t)
	writeFiles(t, root, map[string]string{"history/" + freshName: decisionTemplate, "history/" + goodNewName: filledDecision})
	h := newHarness(t, nil, true)
	if code := h.run("doc", "check", "--root", root); code != 1 {
		t.Fatalf("code=%d", code)
	}
	out := h.stdout.String()
	if strings.Contains(out, `"files"`) || !strings.Contains(out, "history/"+freshName+"\n  - ") || strings.Contains(out, goodNewName) || !strings.Contains(out, "2개 검사, 1개에 문제가 있습니다") {
		t.Errorf("text output:\n%s", out)
	}
	h2 := newHarness(t, nil, true)
	if code := h2.run("doc", "check", "--root", root, filepath.Join(root, "history", goodNewName)); code != 0 || !strings.Contains(h2.stdout.String(), "1개 검사, 문제가 없습니다") {
		t.Errorf("clean text output: code=%d\n%s", code, h2.stdout)
	}
}

func TestDocCheckUsageErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	h := newHarness(t, nil, false)
	if code := h.run("doc", "check"); code != 2 || !strings.Contains(h.stderr.String(), "--root") {
		t.Errorf("outside a git repository: code=%d %s", code, h.stderr)
	}
}
