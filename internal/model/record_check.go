package model

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// CheckRecord returns the problems of a record document file, in Korean, one
// per line of the report. name is the file name and content the file as it
// lies on disk. template is the content of the template of the kind the name
// carries; nil skips the checks that need it (the front matter keys and the
// headings). It finds what the server refuses and what it cannot see: a
// template remnant that is no front matter value, such as a placeholder in the
// body, is kept for ever once uploaded.
func CheckRecord(name, content string, template *string) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	kind, nameOK := ParseDocumentName(name)
	if !nameOK {
		add("파일명이 yyyy-mm-dd-HHMM-<종류>-<slug>.md 형식이 아닙니다. 종류는 %s여야 하며 other일 수 없습니다.", DocumentKindFormat)
	}
	entries, fmOK := FrontMatterEntries(content)
	body := content
	if !fmOK {
		add("맨 앞에 ---로 시작해 ---로 닫는 YAML 프런트매터가 없습니다.")
	} else {
		body = afterFrontMatter(content)
		meta, _ := ParseFrontMatter(content)
		if nameOK && meta.Kind != string(kind) {
			add("프런트매터의 kind(%q)가 파일명의 종류(%s)와 다릅니다.", meta.Kind, kind)
		}
		have := map[string]bool{}
		for _, en := range entries {
			have[en.Key] = true
			if IsPlaceholder(en.Value) {
				add("프런트매터의 %s 값이 서식의 자리표시자(%s)입니다.", en.Key, en.Value)
			}
		}
		if meta.Title == nil && (have["title"] || template == nil) {
			add("프런트매터의 title이 필요합니다. 서버는 기록 문서의 title을 필수로 받습니다.")
		}
		for _, rel := range []struct {
			key   string
			items []string
		}{{"supersedes", meta.Supersedes}, {"related", meta.Related}} {
			for _, item := range rel.items {
				if isPlaceholderScalar(strings.TrimSpace(item)) || isNoRelation(item) || IsDocumentName(item) {
					continue
				}
				hint := ""
				if rel.key == "supersedes" {
					hint = " 문서의 일부만 대체하면 related로 씁니다."
				}
				add("프런트매터의 %s 값 %q이 문서 이름(.md로 끝나는 한 단어)이 아닙니다.%s", rel.key, item, hint)
			}
		}
		if template != nil {
			tmplEntries, _ := FrontMatterEntries(*template)
			for _, en := range tmplEntries {
				if !have[en.Key] {
					add("프런트매터에 %s 키가 없습니다.", en.Key)
				}
			}
		}
	}
	if template != nil {
		for _, p := range headingProblems(recordHeadings(*template), recordHeadings(content)) {
			add("%s", p)
		}
	}

	masked := maskFences(body)
	line := 1 + strings.Count(content[:len(content)-len(body)], "\n")
	for _, loc := range htmlComment.FindAllStringIndex(masked, -1) {
		add("HTML 주석이 남아 있습니다(%d번째 줄): %s", line+strings.Count(masked[:loc[0]], "\n"), excerpt(masked[loc[0]:loc[1]]))
	}
	// Comments and code spans are not read for placeholders; their newlines stay
	// so that the line numbers hold.
	text := htmlComment.ReplaceAllStringFunc(masked, blank)
	text = inlineCode.ReplaceAllStringFunc(text, blank)
	for _, loc := range anglePlaceholder.FindAllStringSubmatchIndex(text, -1) {
		inside := text[loc[2]:loc[3]]
		if isBodyPlaceholder(inside) {
			add("자리표시자가 남아 있습니다(%d번째 줄): <%s>", line+strings.Count(text[:loc[0]], "\n"), inside)
		}
	}
	return problems
}

var (
	inlineCode       = regexp.MustCompile("`[^`\n]*`")
	anglePlaceholder = regexp.MustCompile(`<([^<>\n]+)>`)
	headingLine      = regexp.MustCompile(`(?m)^## .*$`)
)

// isBodyPlaceholder reports whether the text between angle brackets is a
// template placeholder: it holds whitespace or a non-ASCII character, and does
// not start or end with whitespace, which would make it a comparison, and
// holds no ASCII digit. A template describes what to write and names no
// number, while a statute notation such as "<신설 2021.11.30>" carries a date.
func isBodyPlaceholder(inside string) bool {
	if strings.TrimSpace(inside) != inside || strings.ContainsAny(inside, "0123456789") {
		return false
	}
	return strings.IndexFunc(inside, func(r rune) bool { return r > unicode.MaxASCII || unicode.IsSpace(r) }) >= 0
}

// afterFrontMatter returns what follows the closing line of the front matter.
func afterFrontMatter(content string) string {
	_, rest, _ := strings.Cut(content, "\n")
	for more := true; more; {
		var line string
		line, rest, more = strings.Cut(rest, "\n")
		if isFence(line) {
			return rest
		}
	}
	return ""
}

// blank replaces every character but the line breaks with a space.
func blank(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		return ' '
	}, s)
}

// maskFences blanks the lines of fenced code blocks, the fence lines included.
// A block ends at a fence line of the same character and at least the same
// length, or at the end of the text.
func maskFences(text string) string {
	lines := strings.Split(text, "\n")
	var char byte
	var length int
	for i, l := range lines {
		t := strings.TrimLeft(l, " ")
		n := 0
		if t != "" && (t[0] == '`' || t[0] == '~') {
			for n < len(t) && t[n] == t[0] {
				n++
			}
		}
		switch {
		case char == 0 && n >= 3:
			char, length = t[0], n
			lines[i] = ""
		case char != 0 && n >= length && t[0] == char && strings.TrimSpace(t[n:]) == "":
			char = 0
			lines[i] = ""
		case char != 0:
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

// recordHeadings lists the "## " headings of a document outside fenced code
// blocks, in order.
func recordHeadings(content string) []string {
	var out []string
	for _, h := range headingLine.FindAllString(maskFences(afterFrontMatterOr(content)), -1) {
		out = append(out, strings.TrimSpace(h))
	}
	return out
}

func afterFrontMatterOr(content string) string {
	if _, ok := FrontMatterEntries(content); ok {
		return afterFrontMatter(content)
	}
	return content
}

// headingProblems reports the headings of want that have lacks, or has in
// another order than want. The headings that stay in order are the longest
// common subsequence of the two lists, so one moved heading is reported once.
// Extra headings in have are allowed.
func headingProblems(want, have []string) []string {
	// lcs[i][j] is the length of the common subsequence of want[i:] and have[j:].
	lcs := make([][]int, len(want)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(have)+1)
	}
	for i := len(want) - 1; i >= 0; i-- {
		for j := len(have) - 1; j >= 0; j-- {
			if want[i] == have[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	inOrder := map[int]bool{}
	for i, j := 0, 0; i < len(want) && j < len(have); {
		switch {
		case want[i] == have[j]:
			inOrder[i] = true
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			i++
		default:
			j++
		}
	}
	var problems []string
	for i, h := range want {
		switch {
		case inOrder[i]:
		case contains(have, h):
			problems = append(problems, fmt.Sprintf("%s 절이 서식과 다른 순서에 있습니다.", h))
		default:
			problems = append(problems, fmt.Sprintf("%s 절이 없습니다.", h))
		}
	}
	return problems
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// excerpt is the start of a comment on one line.
func excerpt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// StripHTMLComments removes the HTML comments of text that lie outside fenced
// code blocks. A comment that fills its own lines goes with its line break and
// the blank line after it, so the text around it closes up.
func StripHTMLComments(text string) string {
	masked := maskFences(text)
	matches := htmlComment.FindAllStringIndex(masked, -1)
	if len(matches) == 0 {
		return text
	}
	// Fenced lines are emptied in the masked text, so offsets differ line by line.
	// Every other line is the same in both texts.
	starts := func(s string) []int {
		out := []int{0}
		for i := 0; i < len(s); i++ {
			if s[i] == '\n' {
				out = append(out, i+1)
			}
		}
		return out
	}
	mStarts, oStarts := starts(masked), starts(text)
	toOriginal := func(off int, end bool) int {
		probe := off
		if end {
			probe = off - 1
		}
		line := sort.Search(len(mStarts), func(i int) bool { return mStarts[i] > probe }) - 1
		col := off - mStarts[line]
		return oStarts[line] + col
	}
	lineBreak := func(at int) int {
		switch {
		case strings.HasPrefix(text[at:], "\r\n"):
			return 2
		case strings.HasPrefix(text[at:], "\n"):
			return 1
		}
		return 0
	}
	var b strings.Builder
	prev := 0
	for _, m := range matches {
		s, e := toOriginal(m[0], false), toOriginal(m[1], true)
		if s < prev {
			continue
		}
		if s == 0 || text[s-1] == '\n' {
			if n := lineBreak(e); n > 0 {
				e += n
				e += lineBreak(e)
			}
		}
		b.WriteString(text[prev:s])
		prev = e
	}
	b.WriteString(text[prev:])
	return b.String()
}
