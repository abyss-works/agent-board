//go:build e2e

package e2e

import (
	"strings"
	"sync"
	"testing"
)

type template struct {
	Type      string   `json:"type"`
	TitleRule string   `json:"title_rule"`
	Headings  []string `json:"headings"`
	Body      string   `json:"body"`
}

var (
	templatesOnce   sync.Once
	templatesByType map[string]template
)

// templateBody is the template body of an issue type, read from the server.
// Agents write every issue body to it.
func templateBody(t testing.TB, typ string) string {
	t.Helper()
	templatesOnce.Do(func() {
		l := decodeAs[list[template]](t, owner.do(t, "GET", "/v1/templates", nil), 200)
		templatesByType = map[string]template{}
		for _, tpl := range l.Items {
			templatesByType[tpl.Type] = tpl
		}
	})
	tpl, ok := templatesByType[typ]
	if !ok {
		t.Fatalf("the server has no template for %q", typ)
	}
	return tpl.Body
}

func TestTemplates(t *testing.T) {
	t.Run("the server lists six templates in type order", func(t *testing.T) {
		raw := decodeAs[struct {
			Items []template `json:"items"`
		}](t, owner.do(t, "GET", "/v1/templates", nil), 200)
		var types []string
		for _, tpl := range raw.Items {
			types = append(types, tpl.Type)
			if tpl.TitleRule == "" || len(tpl.Headings) == 0 || !strings.HasSuffix(tpl.Body, "\n") {
				t.Errorf("incomplete template %+v", tpl)
			}
			for _, h := range tpl.Headings {
				if !strings.Contains(tpl.Body, "## "+h+"\n") {
					t.Errorf("%s: heading %q is not in the body", tpl.Type, h)
				}
			}
		}
		if !equalStrings(types, []string{"feature", "bug", "design", "decision", "idea", "observation"}) {
			t.Errorf("types = %v", types)
		}
	})
}
