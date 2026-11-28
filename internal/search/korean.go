// Package search implements the single-call hybrid context retrieval engine
// combining Korean particle preprocessing, PGroonga full-text search, pgvector
// HNSW similarity search with query embedding caching, RRF fusion, reranking
// with timeout fallback, confidence cutoff, and bidirectional graph assembly.
package search

import (
	"strings"
	"unicode"
)

type batchimRule uint8

const (
	batchimAny batchimRule = iota
	batchimRequired
	batchimForbidden
)

type koreanParticle struct {
	suffix string
	rule   batchimRule
}

// koreanParticles lists common Korean postpositions ordered longest-first so
// compound particles (e.g. 에서는, 으로는) are matched before single-syllable
// suffixes. Batchim rules prevent false stripping of plain Korean nouns such
// as 사과 or 아이.
var koreanParticles = []koreanParticle{
	// 3-syllable particles
	{"에서는", batchimAny},
	{"에서도", batchimAny},
	{"에서만", batchimAny},
	{"으로는", batchimRequired},
	{"으로도", batchimRequired},
	{"으로만", batchimRequired},
	{"까지는", batchimAny},
	{"까지도", batchimAny},
	{"부터는", batchimAny},
	{"부터도", batchimAny},
	{"처럼은", batchimAny},
	{"보다는", batchimAny},
	{"에게는", batchimAny},
	{"에게도", batchimAny},
	{"한테는", batchimAny},
	{"이라고", batchimRequired},
	{"이라는", batchimRequired},
	{"이라면", batchimRequired},
	// 2-syllable particles
	{"에서", batchimAny},
	{"으로", batchimRequired},
	{"까지", batchimAny},
	{"부터", batchimAny},
	{"처럼", batchimAny},
	{"보다", batchimAny},
	{"에게", batchimAny},
	{"한테", batchimAny},
	{"께서", batchimAny},
	{"하고", batchimAny},
	{"이나", batchimRequired},
	{"라도", batchimAny},
	{"마저", batchimAny},
	{"조차", batchimAny},
	{"밖에", batchimAny},
	{"와는", batchimForbidden},
	{"과는", batchimRequired},
	{"에는", batchimAny},
	{"에도", batchimAny},
	{"에만", batchimAny},
	{"로는", batchimAny},
	{"로도", batchimAny},
	{"로만", batchimAny},
	// 1-syllable particles
	{"은", batchimRequired},
	{"는", batchimForbidden},
	{"이", batchimRequired},
	{"가", batchimForbidden},
	{"을", batchimRequired},
	{"를", batchimForbidden},
	{"과", batchimRequired},
	{"와", batchimForbidden},
	{"의", batchimAny},
	{"에", batchimAny},
	{"로", batchimAny},
	{"도", batchimAny},
	{"만", batchimAny},
}

// NormalizeQuery trims leading/trailing whitespace and collapses internal
// whitespace runs into single spaces for deterministic cache keying.
func NormalizeQuery(q string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(q)), " ")
}

// StripKoreanParticle removes a trailing Korean postposition from token while
// preserving at least one Hangul syllable in a Hangul stem and leaving pure
// ASCII tokens and identifiers completely untouched.
func StripKoreanParticle(token string) string {
	tok := trimOuterPunct(token)
	if tok == "" || isPureASCII(tok) {
		return tok
	}

	for _, p := range koreanParticles {
		if !strings.HasSuffix(tok, p.suffix) {
			continue
		}
		stem := tok[:len(tok)-len(p.suffix)]
		if stem == "" {
			continue
		}
		stemRunes := []rune(stem)
		last := stemRunes[len(stemRunes)-1]

		if isHangulSyllable(last) {
			if !matchesBatchim(last, p.rule) {
				continue
			}
			// Preserve at least 1 Hangul syllable in the stem.
			return stem
		}

		// Allow stripping a Hangul particle attached to an ASCII/alphanumeric
		// identifier (e.g. "API를", "PGroonga에서"), keeping >= 2 characters.
		if isASCIIAlphaNum(last) && len(stemRunes) >= 2 && hasOnlyIdentifierRunes(stemRunes) {
			return stem
		}
	}
	return tok
}

// ExpandQueryTerms splits query into cleaned tokens paired with their optional
// particle-stripped stems.
type ExpandedTerm struct {
	Original string
	Stem     string
}

// ExpandQueryTerms tokenizes query and computes particle-stripped stems for
// each token, preserving order and skipping duplicate terms.
func ExpandQueryTerms(query string) []ExpandedTerm {
	fields := strings.Fields(NormalizeQuery(query))
	if len(fields) == 0 {
		return nil
	}
	out := make([]ExpandedTerm, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		orig := trimOuterPunct(f)
		if orig == "" {
			continue
		}
		if _, dup := seen[orig]; dup {
			continue
		}
		seen[orig] = struct{}{}
		stem := StripKoreanParticle(orig)
		if stem == orig {
			stem = ""
		}
		out = append(out, ExpandedTerm{Original: orig, Stem: stem})
	}
	return out
}

// BuildPGroongaQuery constructs a PGroonga &@~ query expression where each
// token with a stripped Korean particle expands to ("original" OR "stem") and
// all token groups are combined with implicit AND.
func BuildPGroongaQuery(query string) string {
	terms := ExpandQueryTerms(query)
	if len(terms) == 0 {
		return ""
	}
	groups := make([]string, 0, len(terms))
	for _, t := range terms {
		groups = append(groups, formatPGroongaTermGroup(t))
	}
	return strings.Join(groups, " ")
}

// BuildPGroongaRelaxedQuery builds a PGroonga &@~ OR query across expanded
// token groups as a recall backfill when a multi-term AND query returns few
// candidates.
func BuildPGroongaRelaxedQuery(query string) string {
	terms := ExpandQueryTerms(query)
	if len(terms) == 0 {
		return ""
	}
	groups := make([]string, 0, len(terms))
	for _, t := range terms {
		groups = append(groups, formatPGroongaTermGroup(t))
	}
	return strings.Join(groups, " OR ")
}

func formatPGroongaTermGroup(t ExpandedTerm) string {
	origQ := quotePGroongaTerm(t.Original)
	if t.Stem == "" || t.Stem == t.Original {
		return origQ
	}
	return "(" + origQ + " OR " + quotePGroongaTerm(t.Stem) + ")"
}

// quotePGroongaTerm wraps a literal search token in double quotes with Groonga
// escape sequences so operators like OR, -, +, (, ), :, * cannot break syntax.
func quotePGroongaTerm(term string) string {
	var b strings.Builder
	b.Grow(len(term) + 2)
	b.WriteByte('"')
	for i := 0; i < len(term); i++ {
		ch := term[i]
		if ch == '\\' || ch == '"' {
			b.WriteByte('\\')
		}
		b.WriteByte(ch)
	}
	b.WriteByte('"')
	return b.String()
}

func trimOuterPunct(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		if unicode.IsSpace(r) {
			return true
		}
		switch r {
		case ',', '.', '?', '!', '"', '\'', '(', ')', '[', ']', '{', '}', '<', '>', ';', ':', '·', '…', '“', '”', '‘', '’', '「', '」', '『', '』':
			return true
		default:
			return false
		}
	})
}

func isPureASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func isHangulSyllable(r rune) bool {
	return r >= 0xAC00 && r <= 0xD7A3
}

func matchesBatchim(r rune, rule batchimRule) bool {
	if rule == batchimAny {
		return true
	}
	hasBatchim := (r-0xAC00)%28 != 0
	if rule == batchimRequired {
		return hasBatchim
	}
	return !hasBatchim
}

func isASCIIAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func hasOnlyIdentifierRunes(runes []rune) bool {
	for _, r := range runes {
		if isASCIIAlphaNum(r) || r == '_' || r == '-' || r == '.' || r == '/' || r == ':' {
			continue
		}
		return false
	}
	return true
}
