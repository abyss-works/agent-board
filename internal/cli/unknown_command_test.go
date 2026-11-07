package cli

import (
	"strings"
	"testing"
)

func TestUnknownCommandHintsAtSkillCheck(t *testing.T) {
	h := newHarness(t, nil, false)
	if code := h.run("recall-x"); code != 2 {
		t.Fatalf("code=%d", code)
	}
	for _, w := range []string{"CLI가 스킬이나 문서보다 오래됐을 수 있습니다", "agb skill check", "references/setup.md 1절"} {
		if !strings.Contains(h.stderr.String(), w) {
			t.Errorf("stderr lacks %q:\n%s", w, h.stderr)
		}
	}
}
