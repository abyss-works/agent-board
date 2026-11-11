package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

// preCommitInput is the JSON a PreToolUse hook gets on standard input when
// tool is about to run command in cwd.
func preCommitInput(t *testing.T, cwd, tool, command string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"session_id": "s", "hook_event_name": "PreToolUse", "cwd": cwd,
		"tool_name": tool, "tool_input": map[string]any{"command": command}, "tool_use_id": "toolu_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// runPreCommit runs the pre-commit hook with stdin and requires silence on
// standard error and exit code 0, which hold whatever happens.
func runPreCommit(t *testing.T, h *harness, stdin string, args ...string) string {
	t.Helper()
	h.app.Stdin = strings.NewReader(stdin)
	if code := h.run(append([]string{"hook", "pre-commit"}, args...)...); code != 0 || h.stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, h.stderr)
	}
	return h.stdout.String()
}

// denyReason requires out to be the one JSON object with which a PreToolUse
// hook denies a tool call, and returns its reason.
func denyReason(t *testing.T, out string) string {
	t.Helper()
	var got struct {
		HookSpecificOutput *struct {
			HookEventName            string `json:"hookEventName"`
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil || got.HookSpecificOutput == nil {
		t.Fatalf("stdout is not a denial: %q (%v)", out, err)
	}
	if dec.More() {
		t.Fatalf("stdout holds more than one JSON value: %q", out)
	}
	o := got.HookSpecificOutput
	if o.HookEventName != "PreToolUse" || o.PermissionDecision != "deny" || o.PermissionDecisionReason == "" {
		t.Fatalf("denial = %+v", *o)
	}
	return o.PermissionDecisionReason
}

// badRepo is a repository with a config and one new record that is still the
// unfilled template.
func badRepo(t *testing.T) string {
	t.Helper()
	root := checkRepo(t)
	writeFiles(t, root, map[string]string{"history/" + freshName: decisionTemplate})
	return root
}

// cleanRepo is a repository with a config and one new record that is filled.
func cleanRepo(t *testing.T) string {
	t.Helper()
	root := checkRepo(t)
	writeFiles(t, root, map[string]string{"history/" + goodNewName: filledDecision})
	return root
}

func TestHookPreCommitDeniesACommitWhenARecordHasProblems(t *testing.T) {
	root := badRepo(t)
	h := newHarness(t, nil, false)
	if code := h.run("doc", "check", "--root", root); code != 1 {
		t.Fatalf("doc check: code=%d err=%s", code, h.stderr)
	}
	var checked checkOutput
	if err := json.Unmarshal(h.stdout.Bytes(), &checked); err != nil || len(checked.Files) != 1 || len(checked.Files[0].Problems) == 0 {
		t.Fatalf("doc check: %s (%v)", h.stdout, err)
	}

	reason := denyReason(t, runPreCommit(t, h, preCommitInput(t, root, "Bash", "git commit -m x")))
	// The reason names the file and holds every problem as agb doc check words it.
	if !strings.Contains(reason, "history/"+freshName) {
		t.Errorf("reason lacks the file name:\n%s", reason)
	}
	for _, p := range checked.Files[0].Problems {
		if !strings.Contains(reason, p) {
			t.Errorf("reason lacks the problem %q:\n%s", p, reason)
		}
	}
	for _, want := range []string{"다시 커밋", "agb doc check"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason lacks %q:\n%s", want, reason)
		}
	}
}

func TestHookPreCommitPassesACommitWhenTheRecordsAreFine(t *testing.T) {
	h := newHarness(t, nil, false)
	for name, root := range map[string]string{"a filled record": cleanRepo(t), "nothing changed": checkRepo(t)} {
		if out := runPreCommit(t, h, preCommitInput(t, root, "Bash", "git commit -m x")); out != "" {
			t.Errorf("%s: stdout = %q", name, out)
		}
	}
}

func TestGitCommitDir(t *testing.T) {
	cwd := t.TempDir()
	abs := filepath.ToSlash(t.TempDir())
	sub := filepath.Join(cwd, "sub")
	commits := map[string]string{
		"git commit -m x":                   cwd,
		"git commit":                        cwd,
		"  git commit --amend":              cwd,
		"git -C sub commit":                 sub,
		"X=1 git commit":                    cwd,
		"X=1 Y=2 git -C sub commit -m x":    sub,
		"git add a && git commit -m x":      cwd,
		"git add a; git commit -m x":        cwd,
		"git add a\ngit commit -m x":        cwd,
		"false || git commit":               cwd,
		"true | git commit":                 cwd,
		"cd sub && git commit":              sub,
		"cd sub && git add a && git commit": sub,
		"cd sub && git -C deep commit":      filepath.Join(sub, "deep"),
		"git -c user.name=x commit":         cwd,
		"git -c a=b -C sub -c c=d commit":   sub,
		`git -C "my dir" commit -m "a; b"`:  filepath.Join(cwd, "my dir"),
		`git -C 'my dir' commit`:            filepath.Join(cwd, "my dir"),
		`git -C my\ dir commit`:             filepath.Join(cwd, "my dir"),
		`git commit -m "it's"`:              cwd,
		"git -C " + abs + " commit":         filepath.FromSlash(abs),
		`cd "` + abs + `" && git commit`:    filepath.FromSlash(abs),
		"git commit -m \"$(cat <<'EOF'\nfix: a \"quoted\" word; and more\nEOF\n)\"": cwd,
	}
	for command, want := range commits {
		if dir, ok := gitCommitDir(command, cwd); !ok || dir != want {
			t.Errorf("%q: dir = %q, %v, want %q", command, dir, ok, want)
		}
	}
	for _, command := range []string{
		"", " ", "ls", "git", "git status", "git -C", "git -C sub", "git -C sub status",
		"git commit-tree abc", "git log --grep commit", "git log -- commit", "git show commit",
		`echo "git commit"`, "echo git commit", `echo 'a; git commit -m x'`, `echo "a && git commit -m x"`,
		"gitk commit", "mygit commit", "X=1", "X=1 ls", "cd sub", "cd sub && git status",
		"git stash && echo commit", "git -c commit", "git -C commit",
	} {
		if dir, ok := gitCommitDir(command, cwd); ok {
			t.Errorf("%q counts as a commit in %q", command, dir)
		}
	}
}

func TestHookPreCommitChecksOnlyCommits(t *testing.T) {
	root := badRepo(t)
	h := newHarness(t, nil, false)
	for _, command := range []string{"git status", "git commit-tree abc", "git log --grep commit", `echo "git commit"`, "ls"} {
		if out := runPreCommit(t, h, preCommitInput(t, root, "Bash", command)); out != "" {
			t.Errorf("%q: stdout = %q", command, out)
		}
	}
	for _, command := range []string{"git commit -m x", "git -C history commit", "X=1 git commit", "git add a && git commit -m x", "cd history && git commit"} {
		if out := runPreCommit(t, h, preCommitInput(t, root, "Bash", command)); out == "" {
			t.Errorf("%q: not checked", command)
		} else {
			denyReason(t, out)
		}
	}
}

func TestHookPreCommitChecksTheRepositoryOfTheCommand(t *testing.T) {
	bad, clean := badRepo(t), cleanRepo(t)
	h := newHarness(t, nil, false)
	quoted := func(dir string) string { return `"` + filepath.ToSlash(dir) + `"` }
	for _, form := range []func(dir string) string{
		func(dir string) string { return "git -C " + quoted(dir) + " commit -m x" },
		func(dir string) string { return "cd " + quoted(dir) + " && git commit -m x" },
		func(dir string) string { return "cd " + quoted(filepath.Join(dir, "history")) + " && git commit -m x" },
	} {
		if out := runPreCommit(t, h, preCommitInput(t, clean, "Bash", form(bad))); out == "" {
			t.Errorf("%s: the repository of the command is not checked", form(bad))
		} else if reason := denyReason(t, out); !strings.Contains(reason, freshName) {
			t.Errorf("%s: reason = %s", form(bad), reason)
		}
		if out := runPreCommit(t, h, preCommitInput(t, bad, "Bash", form(clean))); out != "" {
			t.Errorf("%s: the repository of cwd is checked: %q", form(clean), out)
		}
	}
	// A path that is not absolute is seen from cwd.
	if out := runPreCommit(t, h, preCommitInput(t, filepath.Dir(bad), "Bash", "git -C "+filepath.Base(bad)+" commit")); out == "" {
		t.Errorf("a relative path is not seen from cwd")
	}
}

// relRepo is a repository whose one new record is filled and names documents
// and an issue, so only the server can find a problem in it.
func relRepo(t *testing.T) string {
	t.Helper()
	root := checkRepo(t)
	writeFiles(t, root, map[string]string{"history/" + goodNewName: relDecision})
	return root
}

func TestHookPreCommitDeniesRelationsTheServerLacks(t *testing.T) {
	h, srv := newHookActorHarness(t, Config{HookActors: map[string]string{"claude": "reviewer"}})
	root := relRepo(t)
	reason := denyReason(t, runPreCommit(t, h, preCommitInput(t, root, "Bash", "git commit -m x"), "--tool", "claude"))
	if !strings.Contains(reason, "supersedes 의 문서 2026-10-10-1000-decision-old.md 가 서버에 없습니다.") {
		t.Errorf("reason = %s", reason)
	}
	if len(srv.reqs) == 0 {
		t.Fatalf("the server was not asked")
	}
	// The hook only reads, as the hook actor of the tool and in the session of the input.
	for _, r := range srv.reqs {
		if r.Method != "GET" || r.Header.Get("X-Actor") != "reviewer" || r.Header.Get("X-Session-Id") != "s" {
			t.Errorf("request = %s %s as %q in %q", r.Method, r.Path, r.Header.Get("X-Actor"), r.Header.Get("X-Session-Id"))
		}
	}
}

func TestHookPreCommitPassesWhenItCannotCheck(t *testing.T) {
	commit := func(root string) func(t *testing.T) string {
		return func(t *testing.T) string { return preCommitInput(t, root, "Bash", "git commit -m x") }
	}
	cases := map[string]struct {
		setup func(t *testing.T, h *harness, srv *fakeServer)
		stdin func(t *testing.T) string
	}{
		"empty stdin":          {stdin: func(t *testing.T) string { return "" }},
		"broken JSON on stdin": {stdin: func(t *testing.T) string { return `{"tool_name":"Bash","tool_input":{"command":"git commit"},"cwd":` }},
		"not JSON on stdin":    {stdin: func(t *testing.T) string { return "git commit" }},
		"another tool":         {stdin: func(t *testing.T) string { return preCommitInput(t, badRepo(t), "Write", "git commit -m x") }},
		"no tool name":         {stdin: func(t *testing.T) string { return preCommitInput(t, badRepo(t), "", "git commit -m x") }},
		"no command": {stdin: func(t *testing.T) string {
			b, _ := json.Marshal(map[string]any{"cwd": badRepo(t), "tool_name": "Bash", "tool_input": map[string]any{}})
			return string(b)
		}},
		"a command that is not a string": {stdin: func(t *testing.T) string {
			b, _ := json.Marshal(map[string]any{"cwd": badRepo(t), "tool_name": "Bash", "tool_input": map[string]any{"command": 1}})
			return string(b)
		}},
		"no .agb.json": {stdin: func(t *testing.T) string {
			root := gitRepo(t)
			writeFiles(t, root, map[string]string{"history/" + freshName: decisionTemplate})
			return commit(root)(t)
		}},
		"broken .agb.json": {stdin: func(t *testing.T) string {
			root := badRepo(t)
			writeFiles(t, root, map[string]string{".agb.json": `{"project":`})
			return commit(root)(t)
		}},
		"not a git repository": {stdin: func(t *testing.T) string {
			root := t.TempDir()
			writeFiles(t, root, map[string]string{".agb.json": checkConfig, "history/_decision.md": decisionTemplate, "history/" + freshName: decisionTemplate})
			return commit(root)(t)
		}},
		// agb doc check takes the config of the top level of the repository, so
		// the records of a repository around the one of the commit are not its
		// business.
		"a repository without a config inside one with a bad record": {stdin: func(t *testing.T) string {
			inner := filepath.Join(badRepo(t), "inner")
			if err := os.Mkdir(inner, 0o755); err != nil {
				t.Fatal(err)
			}
			gitRun(t, inner, "init", "-q")
			return commit(inner)(t)
		}},
		"cwd does not exist": {stdin: func(t *testing.T) string { return commit(filepath.Join(t.TempDir(), "gone"))(t) }},
		"the folder of the command does not exist": {stdin: func(t *testing.T) string {
			return preCommitInput(t, badRepo(t), "Bash", "git -C gone commit")
		}},
		"no token":           {stdin: commit(relRepo(t)), setup: func(t *testing.T, h *harness, srv *fakeServer) { delete(h.env, "AGB_TOKEN") }},
		"no actor":           {stdin: commit(relRepo(t)), setup: func(t *testing.T, h *harness, srv *fakeServer) { delete(h.env, "AGB_ACTOR") }},
		"no url":             {stdin: commit(relRepo(t)), setup: func(t *testing.T, h *harness, srv *fakeServer) { delete(h.env, "AGB_URL") }},
		"server unreachable": {stdin: commit(relRepo(t)), setup: func(t *testing.T, h *harness, srv *fakeServer) { srv.srv.Close() }},
		"server says 500": {stdin: commit(relRepo(t)), setup: func(t *testing.T, h *harness, srv *fakeServer) {
			srv.on("GET", "/v1/projects/CLI/documents/2026-10-10-1000-decision-old.md", canned{status: 500, body: `oops`})
		}},
		"server says 401": {stdin: commit(relRepo(t)), setup: func(t *testing.T, h *harness, srv *fakeServer) {
			srv.on("GET", "/v1/projects/CLI/documents/2026-10-10-1000-decision-old.md", canned{status: 401, body: errJSON})
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h, srv := newMsgHarness(t, false)
			if c.setup != nil {
				c.setup(t, h, srv)
			}
			if out := runPreCommit(t, h, c.stdin(t), "--tool", "claude"); out != "" {
				t.Errorf("stdout = %q", out)
			}
		})
	}
}

// What the files alone show is a problem whether the server answers or not.
func TestHookPreCommitDeniesFormProblemsWithoutTheServer(t *testing.T) {
	root := relRepo(t)
	writeFiles(t, root, map[string]string{"history/" + freshName: decisionTemplate})
	for name, setup := range map[string]func(h *harness, srv *fakeServer){
		"server unreachable": func(h *harness, srv *fakeServer) { srv.srv.Close() },
		"no token":           func(h *harness, srv *fakeServer) { delete(h.env, "AGB_TOKEN") },
		"server says 500": func(h *harness, srv *fakeServer) {
			srv.on("GET", "/v1/projects/CLI/documents/2026-10-10-1000-decision-old.md", canned{status: 500, body: `oops`})
		},
	} {
		h, srv := newMsgHarness(t, false)
		setup(h, srv)
		reason := denyReason(t, runPreCommit(t, h, preCommitInput(t, root, "Bash", "git commit -m x")))
		if !strings.Contains(reason, freshName) || strings.Contains(reason, goodNewName) {
			t.Errorf("%s: reason = %s", name, reason)
		}
	}
}

func TestHookPreCommitGivesUpOnASlowServer(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer slow.Close()
	defer close(release)

	old := hookQueryTimeout
	hookQueryTimeout = 50 * time.Millisecond
	defer func() { hookQueryTimeout = old }()

	h := newHarness(t, nil, false)
	h.env["AGB_URL"] = slow.URL
	stdin := preCommitInput(t, relRepo(t), "Bash", "git commit -m x")
	start := time.Now()
	if out := runPreCommit(t, h, stdin); out != "" {
		t.Errorf("stdout = %q", out)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the hook took %v", d)
	}
}

// Exit code 2 from a PreToolUse hook blocks the tool call, so no command line
// and no input may lead to it. runPreCommit requires exit code 0.
func TestHookPreCommitNeverFailsOnWhatItDoesNotKnow(t *testing.T) {
	root := badRepo(t)
	h := newHarness(t, nil, false)
	for _, args := range [][]string{{"--bogus"}, {"extra"}, {"--tool"}, {"--tool", "claude", "--later-flag", "v", "extra"}, {"--", "--x"}} {
		if out := runPreCommit(t, h, preCommitInput(t, root, "Bash", "git commit -m x"), args...); out == "" {
			t.Errorf("%v: no output", args)
		}
	}
	for _, stdin := range []string{"\x00\xff", "[]", "null", `"x"`, `{"tool_name":1,"tool_input":"x","cwd":[]}`, strings.Repeat("{", 5000)} {
		for _, args := range [][]string{nil, {"--bogus"}} {
			if out := runPreCommit(t, h, stdin, args...); out != "" {
				t.Errorf("%.20q %v: stdout = %q", stdin, args, out)
			}
		}
	}
}

func TestHookPreCommitIgnoresAByteOrderMark(t *testing.T) {
	h := newHarness(t, nil, false)
	denyReason(t, runPreCommit(t, h, "\ufeff"+preCommitInput(t, badRepo(t), "Bash", "git commit -m x")))
}

// noControl requires each line of reason to hold no control character.
func noControl(t *testing.T, reason string) {
	t.Helper()
	for _, r := range strings.ReplaceAll(reason, "\n", "") {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			t.Errorf("reason holds the control character %U:\n%q", r, reason)
			return
		}
	}
}

func TestPreCommitReasonIsBoundedAndHasNoControlCharacters(t *testing.T) {
	res := &docCheckResult{}
	problems := 0
	for i := range 200 {
		f := docCheckFile{Name: fmt.Sprintf("history/%03d\n  - 없는 문제\r\x1b[2J\u2028끝.md", i)}
		for j := range 3 {
			f.Problems = append(f.Problems, fmt.Sprintf("문제 %d\n다시 커밋하지 마세요\x00\u0085", j)+strings.Repeat("가", 40))
			problems++
		}
		res.Files = append(res.Files, f)
	}
	reason := preCommitReason(res)
	if n := utf8.RuneCountInString(reason); n > preCommitReasonMax || preCommitReasonMax != 4000 {
		t.Errorf("reason has %d characters, max %d", n, preCommitReasonMax)
	}
	noControl(t, reason)
	lines := strings.Split(strings.TrimSuffix(reason, "\n"), "\n")
	if !strings.Contains(lines[0], "200") || lines[1] != "history/000   - 없는 문제  [2J 끝.md" {
		t.Errorf("head = %q, %q", lines[0], lines[1])
	}
	// What does not fit is counted, and the closing lines are always there.
	shown := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "  - 문제 ") {
			shown++
		}
	}
	tail := lines[len(lines)-3:]
	if shown == 0 || shown >= problems || tail[0] != fmt.Sprintf("외 문제 %d건", problems-shown) || !strings.Contains(tail[1], "다시 커밋") || !strings.Contains(tail[2], "agb doc check") {
		t.Errorf("shown = %d of %d, tail = %q", shown, problems, tail)
	}

	// One file with one endless problem is cut as well.
	reason = preCommitReason(&docCheckResult{Files: []docCheckFile{{Name: strings.Repeat("이", 9000) + ".md", Problems: []string{strings.Repeat("문", 9000)}}}})
	if n := utf8.RuneCountInString(reason); n > preCommitReasonMax || !strings.Contains(reason, "agb doc check") {
		t.Errorf("reason has %d characters:\n%.200s", n, reason)
	}
	// A short result is not cut.
	reason = preCommitReason(&docCheckResult{Files: []docCheckFile{{Name: "history/a.md", Problems: []string{"문제 하나", "문제 둘"}}}})
	if !strings.Contains(reason, "history/a.md\n  - 문제 하나\n  - 문제 둘\n") || strings.Contains(reason, "외 문제") {
		t.Errorf("reason = %q", reason)
	}
}

// The denial of a repository with many bad records stays within the limit.
func TestHookPreCommitCutsALongDenial(t *testing.T) {
	root := checkRepo(t)
	files := map[string]string{}
	for i := range 60 {
		files[fmt.Sprintf("history/2026-10-17-13%02d-decision-n%d.md", i, i)] = decisionTemplate
	}
	writeFiles(t, root, files)
	h := newHarness(t, nil, false)
	out := runPreCommit(t, h, preCommitInput(t, root, "Bash", "git commit -m x"))
	reason := denyReason(t, out)
	if n := utf8.RuneCountInString(reason); n > 4000 || !strings.Contains(reason, "외 문제 ") {
		t.Errorf("reason has %d characters:\n%s", n, reason)
	}
	noControl(t, reason)
	if !bytes.HasSuffix([]byte(out), []byte("}\n")) || strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Errorf("stdout is not one line of JSON: %q", out)
	}
}
