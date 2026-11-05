package cli

import (
	"regexp"
	"strings"
)

// followUpHeadings are the words that mark a level-2 section of a record
// document as work left open.
var followUpHeadings = []string{"남은 것", "미확인", "후속"}

// listItem matches a top-level bullet or numbered list item.
var listItem = regexp.MustCompile(`^(?:[-*+]|\d+[.)])\s+(.*)$`)

// followUp is one open section of a record document with its items.
type followUp struct {
	Section string   `json:"section"`
	Items   []string `json:"items"`
}

// followUps returns the open sections of a record document: level-2 sections
// whose heading names work left open, with their top-level list items, or the
// text as one item when the section has no list. Sections that say only 없음
// are left out, and so are code blocks.
func followUps(content string) []followUp {
	var out []followUp
	var cur *followUp
	var para []string
	inCode := false
	flush := func() {
		if cur == nil {
			return
		}
		if len(cur.Items) == 0 && len(para) > 0 {
			cur.Items = []string{strings.Join(para, " ")}
		}
		var items []string
		for _, it := range cur.Items {
			if strings.TrimRight(it, ".") != "없음" {
				items = append(items, it)
			}
		}
		if len(items) > 0 {
			out = append(out, followUp{Section: cur.Section, Items: items})
		}
		cur, para = nil, nil
	}
	for _, line := range strings.Split(stripFrontMatter(content), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		if heading, ok := strings.CutPrefix(line, "## "); ok {
			flush()
			heading = strings.TrimSpace(heading)
			for _, w := range followUpHeadings {
				if strings.Contains(heading, w) {
					cur = &followUp{Section: heading}
					break
				}
			}
			continue
		}
		if strings.HasPrefix(line, "# ") {
			flush()
			continue
		}
		if cur == nil {
			continue
		}
		if m := listItem.FindStringSubmatch(line); m != nil {
			cur.Items = append(cur.Items, strings.TrimSpace(m[1]))
		} else if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(t, "#") {
			para = append(para, t)
		}
	}
	flush()
	return out
}

// stripFrontMatter drops a leading "---" block.
func stripFrontMatter(content string) string {
	if !strings.HasPrefix(content, "---") {
		return content
	}
	rest := content[3:]
	if i := strings.Index(rest, "\n---"); i >= 0 {
		rest = rest[i+4:]
		return rest
	}
	return content
}
