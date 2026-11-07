package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitWritesAnEmptyConfig(t *testing.T) {
	root := gitRepo(t)
	srv := newFakeServer(t)
	h := newHarness(t, srv, false)
	if code := h.run("init", "-p", "AGB", "--root", root); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	path := filepath.Join(root, ".agb.json")
	if got := strings.TrimSpace(h.stdout.String()); got != path {
		t.Errorf("stdout = %q, want %q", got, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := parseRepoConfig(raw)
	if err != nil || cfg.Project != "AGB" || cfg.AutoSync || cfg.Documents == nil || len(cfg.Documents) != 0 || cfg.New != nil {
		t.Errorf("config = %+v, %v\n%s", cfg, err, raw)
	}
	if !strings.Contains(string(raw), `"documents": []`) || !strings.HasSuffix(string(raw), "}\n") {
		t.Errorf("the file must show the empty rule list:\n%s", raw)
	}
	if len(srv.reqs) != 0 {
		t.Errorf("init must not reach the server: %v", srv.paths())
	}

	// A second init does not overwrite what is there.
	if err := os.WriteFile(path, []byte(`{"project": "AGB", "documents": [{"glob": "a/*.md", "name": "base"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := h.run("init", "-p", "OTH", "--root", root); code != 2 || !strings.Contains(h.stderr.String(), "덮어쓰지") {
		t.Errorf("second init: code=%d stderr=%s", code, h.stderr)
	}
	if again, _ := os.ReadFile(path); !strings.Contains(string(again), `"glob": "a/*.md"`) {
		t.Errorf("the file was overwritten: %s", again)
	}
}

func TestInitDefaultRootIsTheGitTopLevel(t *testing.T) {
	root := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(root, "sub", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(root, "sub", "dir"))
	h := newHarness(t, nil, false)
	if code := h.run("init", "-p", "AGB"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if _, err := os.Stat(filepath.Join(root, ".agb.json")); err != nil {
		t.Errorf("the file belongs at the top of the repository: %v", err)
	}
}

func TestInitUsageErrors(t *testing.T) {
	root := gitRepo(t)
	h := newHarness(t, nil, false)
	for label, args := range map[string][]string{
		"no project":            {"init", "--root", root},
		"a bad project":         {"init", "-p", "agb", "--root", root},
		"a stray argument":      {"init", "-p", "AGB", "--root", root, "extra"},
		"a missing root":        {"init", "-p", "AGB", "--root", filepath.Join(root, "nope")},
		"a root that is a file": {"init", "-p", "AGB", "--root", filepath.Join(root, "wip.go")},
	} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%s: code=%d stderr=%s", label, code, h.stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".agb.json")); err == nil {
		t.Errorf("a usage error left a file behind")
	}
	// Outside a repository without --root there is no top level.
	t.Chdir(t.TempDir())
	if code := h.run("init", "-p", "AGB"); code != 2 || !strings.Contains(h.stderr.String(), "git 저장소") {
		t.Errorf("outside a repository: code=%d stderr=%s", code, h.stderr)
	}
}
