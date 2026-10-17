package cli

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/wsk0715/agent-board/internal/model"
)

// jsonOutput decides between JSON and tables: flags first, then whether
// stdout is a terminal.
func (a *App) jsonOutput(g *globals) bool {
	switch {
	case g.table:
		return false
	case g.json:
		return true
	}
	return !a.StdoutIsTerminal
}

// writeJSON writes the server body as it is, ending with one newline.
func writeJSON(w io.Writer, body []byte) {
	w.Write(body)
	if len(body) == 0 || body[len(body)-1] != '\n' {
		io.WriteString(w, "\n")
	}
}

// formatTime renders a server timestamp in the given location, to the
// minute.
func formatTime(t model.Time, loc *time.Location) string {
	return t.Time().In(loc).Format("2006-01-02 15:04")
}

// formatTimePtr is formatTime for optional timestamps.
func formatTimePtr(t *model.Time, loc *time.Location) string {
	if t == nil {
		return "-"
	}
	return formatTime(*t, loc)
}

// runeWidth is 2 for East Asian wide and fullwidth runes, 0 for
// combining marks and 1 otherwise.
func runeWidth(r rune) int {
	switch {
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), r == 0x200B:
		return 0
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK radicals, punctuation
		r >= 0x3041 && r <= 0x33FF, // kana, CJK compatibility
		r >= 0x3400 && r <= 0x4DBF, // CJK extension A
		r >= 0x4E00 && r <= 0x9FFF, // CJK unified
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE4F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD: // CJK extensions B and later
		return 2
	}
	return 1
}

// displayWidth is the number of terminal cells s occupies.
func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

// truncate cuts s to at most width cells, ending with an ellipsis when
// something was cut.
func truncate(s string, width int) string {
	if width <= 0 || displayWidth(s) <= width {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := runeWidth(r)
		if used+w > width-1 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String() + "…"
}

// pad right-pads s with spaces to width cells.
func pad(s string, width int) string {
	if d := width - displayWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// table is a simple column-aligned text table. The last column absorbs
// the remaining width and is truncated when the table would overflow.
type table struct {
	header []string
	rows   [][]string
}

func (t *table) add(cells ...string) { t.rows = append(t.rows, cells) }

func (t *table) write(w io.Writer, width int) {
	cols := len(t.header)
	widths := make([]int, cols)
	all := append([][]string{t.header}, t.rows...)
	for _, row := range all {
		for i := 0; i < cols && i < len(row); i++ {
			if n := displayWidth(row[i]); n > widths[i] {
				widths[i] = n
			}
		}
	}
	const gap = 2
	last := cols - 1
	if width > 0 {
		fixed := 0
		for i := 0; i < last; i++ {
			fixed += widths[i] + gap
		}
		if room := width - fixed; room < widths[last] {
			if room < 8 {
				room = 8
			}
			widths[last] = room
		}
	}
	for _, row := range all {
		var line strings.Builder
		for i := 0; i < cols; i++ {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			cell = strings.ReplaceAll(cell, "\n", " ")
			if i == last {
				if width > 0 {
					cell = truncate(cell, widths[i])
				}
				line.WriteString(cell)
			} else {
				line.WriteString(pad(cell, widths[i]))
				line.WriteString(strings.Repeat(" ", gap))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(line.String(), " "))
	}
}

// fields prints "name: value" lines with aligned names.
func fields(w io.Writer, pairs [][2]string) {
	width := 0
	for _, p := range pairs {
		if n := displayWidth(p[0]); n > width {
			width = n
		}
	}
	for _, p := range pairs {
		fmt.Fprintf(w, "%s  %s\n", pad(p[0]+":", width+1), p[1])
	}
}

func joinOr(list []string, empty string) string {
	if len(list) == 0 {
		return empty
	}
	return strings.Join(list, ", ")
}

func deref(s *string, empty string) string {
	if s == nil {
		return empty
	}
	return *s
}
