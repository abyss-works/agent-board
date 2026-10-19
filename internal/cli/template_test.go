package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/model"
)

// templatesBody is what the server sends: the real templates.
func templatesBody(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"items": model.Templates})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func newTemplateHarness(t *testing.T, tty bool) (*harness, *fakeServer) {
	t.Helper()
	srv := newFakeServer(t)
	srv.on("GET", "/v1/templates", canned{body: templatesBody(t)})
	srv.on("POST", "/v1/issues", canned{status: 201, body: issueJSON})
	return newHarness(t, srv, tty), srv
}

func TestTemplateListShowsTypesRulesAndHeadings(t *testing.T) {
	h, srv := newTemplateHarness(t, true)
	if code := h.run("template", "list"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	if srv.last().Path != "/v1/templates" {
		t.Errorf("request = %+v", srv.last())
	}
	out := h.stdout.String()
	for _, tpl := range model.Templates {
		for _, want := range []string{string(tpl.Type), tpl.TitleRule, strings.Join(tpl.Headings, ", ")} {
			if !strings.Contains(out, want) {
				t.Errorf("table lacks %q:\n%s", want, out)
			}
		}
	}
	if strings.Contains(out, "왜 필요한지") {
		t.Errorf("the list leaves the body to template show:\n%s", out)
	}
	hj := newHarness(t, srv, false)
	if code := hj.run("template", "list"); code != 0 || hj.stdout.String() != templatesBody(t)+"\n" {
		t.Errorf("json: code=%d out=%q", code, hj.stdout.String())
	}
}

func TestTemplateShowPrintsTheBodyForPiping(t *testing.T) {
	for _, tty := range []bool{true, false} {
		h, _ := newTemplateHarness(t, tty)
		for _, tpl := range model.Templates {
			if code := h.run("template", "show", string(tpl.Type)); code != 0 {
				t.Fatalf("%s: code=%d stderr=%s", tpl.Type, code, h.stderr)
			}
			// The body exactly, also on a pipe where other commands print JSON.
			if h.stdout.String() != tpl.Body {
				t.Errorf("tty=%v %s: stdout = %q, want the template body", tty, tpl.Type, h.stdout.String())
			}
		}
	}

	// --json asks for the template object.
	h, _ := newTemplateHarness(t, false)
	if code := h.run("template", "show", "bug", "--json"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var got model.Template
	if err := json.Unmarshal(h.stdout.Bytes(), &got); err != nil || got.Type != model.TypeBug || got.Body != model.Templates[1].Body {
		t.Errorf("--json output = %q (%v)", h.stdout.String(), err)
	}

	// An unknown type or a missing one is a usage error.
	for _, args := range [][]string{{"template", "show", "task"}, {"template", "show"}, {"template", "show", "bug", "feature"}} {
		if code := h.run(args...); code != 2 {
			t.Errorf("%v: code=%d", args, code)
		}
	}
	if code := h.run("template", "show", "task"); !strings.Contains(h.stderr.String(), "feature") || !strings.Contains(h.stderr.String(), "observation") {
		t.Errorf("the error lists the types: code=%d stderr=%s", code, h.stderr)
	}
}

// The output of template show goes straight into issue create --body-file -.
func TestTemplateShowFeedsIssueCreate(t *testing.T) {
	h, srv := newTemplateHarness(t, false)
	if code := h.run("template", "show", "idea"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	h.app.Stdin = strings.NewReader(h.stdout.String())
	if code := h.run("issue", "create", "-p", "CLI", "-t", "아이디어", "--type", "idea", "--body-file", "-"); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, h.stderr)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(srv.last().Body), &sent); err != nil || sent["body"] != model.Templates[3].Body {
		t.Errorf("body sent = %v (%v)", sent["body"], err)
	}
	for _, args := range [][]string{{"issue", "create", "--help"}, {"issue", "update", "--help"}} {
		if code := h.run(args...); code != 0 || !strings.Contains(h.stdout.String(), "agb template show <type>") {
			t.Errorf("%v help does not point to the templates: code=%d out=%s", args, code, h.stdout)
		}
	}
	if code := h.run("template", "--help"); code != 0 || !strings.Contains(h.stdout.String(), "show") {
		t.Errorf("help: code=%d out=%s", code, h.stdout)
	}
	if code := h.run("--help"); code != 0 || !strings.Contains(h.stdout.String(), "template") {
		t.Errorf("top-level help: code=%d", code)
	}
}
