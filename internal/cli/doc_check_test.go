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

// checkConfig has two folders of general documents, a folder of imported ones,
// and the decision template.
const checkConfig = `{"project": "CLI",
  "documents": [
    {"glob": "history/*.md", "name": "base"},
    {"glob": "archive/*.md", "name": "base", "archived": true},
    {"glob": "imported/**/*.md", "name": "path", "imported": true}],
  "new": {"template": "history/_{kind}.md", "output": "history"}}`

// checkRepo is a git repository with the config, the decision template and one
// committed record that has a remnant; the committed file is not checked unless
// named.
func checkRepo(t *testing.T) string {
	t.Helper()
	root := gitRepo(t)
	writeFiles(t, root, map[string]string{
		".agb.json":              checkConfig,
		"history/_decision.md":   decisionTemplate,
		"history/" + checkedName: decisionTemplate,
		"README.md":              "x",
	})
	gitRun(t, root, "add", ".agb.json", "history", "README.md")
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

func TestDocCheckPicksNewAndModifiedFilesThatARuleNames(t *testing.T) {
	root := checkRepo(t)
	writeFiles(t, root, map[string]string{
		"history/" + freshName:     decisionTemplate,            // untracked
		"history/" + goodNewName:   filledDecision,              // untracked
		"history/" + checkedName:   decisionTemplate + "\n수정\n", // modified
		"archive/" + otherDirName:  decisionTemplate,            // untracked, a rule of its own
		"history/_decision.md":     decisionTemplate + "\n",     // template: never checked
		"history/notes.md":         "이름이 기록 형식이 아닌 파일",          // name problem
		"history/sub/" + freshName: decisionTemplate,            // no rule: not a direct child
		"other/" + freshName:       decisionTemplate,            // no rule
		"imported/x/old.md":        "# 가져온 문서\n",                // an imported rule is not checked
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

func TestDocCheckFindsWhatTheServerNoLongerChecks(t *testing.T) {
	root := checkRepo(t)
	// The guidance comment of a template and a title that is missing: the server takes both.
	writeFiles(t, root, map[string]string{
		"history/" + goodNewName: strings.Replace(filledDecision, "# 큐를 고른다\n", "# 큐를 고른다\n\n<!-- 파일명: x. 이 주석은 지운다. -->\n", 1),
		"history/" + freshName:   strings.Replace(filledDecision, "title: 큐를 고른다\n", "", 1),
	})
	h := newHarness(t, nil, false)
	if code := h.run("doc", "check", "--root", root); code != 1 {
		t.Fatalf("code=%d out=%s", code, h.stdout)
	}
	out := h.stdout.String()
	for _, want := range []string{"HTML 주석이 남아 있습니다", "title"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
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
	// The config names a template for the kind, and it is missing: every file of the kind has that problem.
	// The key check is skipped, the comment check is not.
	if len(got.Files) != 2 || len(got.Notes) != 0 {
		t.Fatalf("out = %s", h.stdout)
	}
	for _, f := range got.Files {
		if !strings.Contains(strings.Join(f.Problems, " "), "종류 decision 의 서식(history/_decision.md)이 없습니다") {
			t.Errorf("%s lacks the missing template problem: %v", f.Name, f.Problems)
		}
	}
	for _, f := range got.Files {
		if want := map[string]int{"history/" + freshName: 2, "history/" + goodNewName: 1}[f.Name]; len(f.Problems) != want {
			t.Errorf("%s: problems = %v, want %d (the missing template, and the comment for the fresh file)", f.Name, f.Problems, want)
		}
	}

	// A config without new has no template at all; that is noted once.
	writeFiles(t, root, map[string]string{".agb.json": `{"project": "CLI", "documents": [{"glob": "history/*.md", "name": "base"}]}`})
	if code := h.run("doc", "check", "--root", root); code != 1 {
		t.Fatalf("no new: code=%d out=%s", code, h.stdout)
	}
	got = checkOutput{}
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil || len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "new") || strings.Contains(h.stdout.String(), "서식(") {
		t.Errorf("no new: out = %s (%v)", h.stdout, err)
	}
}

const relDecision = `---
kind: decision
date: 2026-10-17 12:00
project: agent-board
title: 관계가 있는 결정
tags: [queue]
author: claude
status: 채택
supersedes: 2026-10-10-1000-decision-old.md
related: [AGT/2026-10-11-1000-report-other.md, 2026-10-12-1000-report-local.md]
issue: AGB-7
---

# 관계가 있는 결정

## 배경

이유.

## 결정

- 큐를 쓴다.
`

func TestDocCheckAsksTheServerForTheRelations(t *testing.T) {
	root := checkRepo(t)
	writeFiles(t, root, map[string]string{
		"history/" + goodNewName:                  relDecision,
		"history/2026-10-12-1000-report-local.md": strings.Replace(filledDecision, "kind: decision", "kind: report", 1),
	})
	srv := newFakeServer(t)
	srv.on("GET", "/v1/projects/CLI/documents/2026-10-10-1000-decision-old.md", canned{body: serverDoc("CLI", "2026-10-10-1000-decision-old.md", "history", "x")})
	srv.on("GET", "/v1/projects/AGT/documents/2026-10-11-1000-report-other.md", canned{body: serverDoc("AGT", "2026-10-11-1000-report-other.md", "history", "x")})
	srv.on("GET", "/v1/issues/AGB-7", canned{body: issueJSON})
	h := newHarness(t, srv, false)
	if code := h.run("doc", "check", "--root", root, filepath.Join(root, "history", goodNewName)); code != 0 {
		t.Fatalf("every target exists: code=%d out=%s err=%s", code, h.stdout, h.stderr)
	}
	// A target in another project is looked for under its own name first, then as PROJECT/name.
	var paths []string
	for _, r := range srv.reqs {
		paths = append(paths, r.Path)
	}
	joined := strings.Join(paths, " ")
	for _, want := range []string{"/v1/projects/CLI/documents/2026-10-10-1000-decision-old.md", "/v1/projects/AGT/documents/2026-10-11-1000-report-other.md", "/v1/issues/AGB-7"} {
		if !strings.Contains(joined, want) {
			t.Errorf("requests lack %s: %v", want, paths)
		}
	}
	if strings.Contains(joined, "report-local") {
		t.Errorf("a document of the repository is not asked for: %v", paths)
	}

	// Targets the server lacks are problems of the file.
	empty := newFakeServer(t)
	h = newHarness(t, empty, false)
	if code := h.run("doc", "check", "--root", root, filepath.Join(root, "history", goodNewName)); code != 1 {
		t.Fatalf("missing targets: code=%d out=%s", code, h.stdout)
	}
	for _, want := range []string{"supersedes 의 문서 2026-10-10-1000-decision-old.md", "related 의 문서 AGT/2026-10-11-1000-report-other.md", "issue 의 이슈 AGB-7"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, h.stdout)
		}
	}
}

func TestDocCheckSkipsTheRelationsWhenTheServerIsOutOfReach(t *testing.T) {
	root := checkRepo(t)
	writeFiles(t, root, map[string]string{"history/" + goodNewName: relDecision})
	// No server configured at all.
	h := newHarness(t, nil, false)
	if code := h.run("doc", "check", "--root", root, filepath.Join(root, "history", goodNewName)); code != 0 {
		t.Fatalf("no server: code=%d out=%s", code, h.stdout)
	}
	var got checkOutput
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil || len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "서버") {
		t.Errorf("no server: out = %s (%v)", h.stdout, err)
	}
	// A server that fails: the check stops asking and says so.
	srv := newFakeServer(t)
	srv.on("GET", "/v1/projects/CLI/documents/2026-10-10-1000-decision-old.md", canned{status: 500, body: `{"error":{"code":"internal","message":"서버 오류"}}`})
	h = newHarness(t, srv, false)
	if code := h.run("doc", "check", "--root", root, filepath.Join(root, "history", goodNewName)); code != 0 {
		t.Fatalf("failing server: code=%d out=%s", code, h.stdout)
	}
	got = checkOutput{}
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil || len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "멈췄습니다") || len(srv.reqs) != 1 {
		t.Errorf("failing server: out = %s (%v), requests %v", h.stdout, err, srv.paths())
	}
}

func TestDocCheckRefusesWhatTheServerWouldRefuseInRelations(t *testing.T) {
	root := checkRepo(t)
	sentence := strings.Replace(relDecision, "supersedes: 2026-10-10-1000-decision-old.md", "supersedes: 2026-10-10-1000-decision-old.md의 결론 중 일부", 1)
	writeFiles(t, root, map[string]string{"history/" + goodNewName: sentence, "history/" + freshName: strings.Replace(relDecision, "supersedes: 2026-10-10-1000-decision-old.md", "supersedes: None", 1)})
	h := newHarness(t, nil, false)
	if code := h.run("doc", "check", "--root", root); code != 1 {
		t.Fatalf("code=%d out=%s", code, h.stdout)
	}
	if out := h.stdout.String(); !strings.Contains(out, "related로 씁니다") || !strings.Contains(out, `None`) {
		t.Errorf("output:\n%s", out)
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
	bare := t.TempDir()
	if code := h.run("doc", "check", "--root", bare); code != 2 || !strings.Contains(h.stderr.String(), "agb init") {
		t.Errorf("without a config: code=%d %s", code, h.stderr)
	}
}
