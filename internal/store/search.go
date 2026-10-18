package store

import (
	"fmt"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// BuildMatch turns a user query into an FTS5 MATCH expression. The whole
// input wrapped in double quotes is one phrase; otherwise each whitespace
// separated term becomes a quoted prefix query and terms are ANDed. User
// text is always quoted so FTS5 syntax characters cannot cause errors.
// A blank query yields "" meaning "no search".
func BuildMatch(q string) (string, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", nil
	}
	if len(q) >= 2 && strings.HasPrefix(q, `"`) && strings.HasSuffix(q, `"`) {
		inner := strings.TrimSpace(q[1 : len(q)-1])
		if inner == "" {
			return "", nil
		}
		return quote(inner), nil
	}
	terms := strings.Fields(q)
	if len(terms) > model.MaxSearchTerms {
		return "", fmt.Errorf("search query has %d terms, at most %d allowed", len(terms), model.MaxSearchTerms)
	}
	parts := make([]string, len(terms))
	for i, t := range terms {
		parts[i] = quote(t) + " *"
	}
	return strings.Join(parts, " "), nil
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
