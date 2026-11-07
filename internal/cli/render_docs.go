package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// supersession is the replacement relations of one document, to be listed
// under a table whose rows have no room for names.
type supersession struct {
	id           string
	supersedes   []string
	supersededBy []string
}

// printSupersession lists, for each document that replaces or is replaced by
// another, the names on the other side. A document that has neither is left
// out; nothing is printed when no document has any.
func printSupersession(w io.Writer, items []supersession) {
	var lines []string
	for _, it := range items {
		if len(it.supersedes) > 0 {
			lines = append(lines, fmt.Sprintf("  %s  supersedes: %s", it.id, strings.Join(it.supersedes, ", ")))
		}
		if len(it.supersededBy) > 0 {
			lines = append(lines, fmt.Sprintf("  %s  superseded_by: %s", it.id, strings.Join(it.supersededBy, ", ")))
		}
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(w, "\n대체 관계")
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
}

// hitState is the status of an issue or of the issue of a comment, or the
// state of a document, of a search hit.
func hitState(h model.SearchHit) string {
	if h.State != nil {
		return *h.State
	}
	return deref(h.Status, "-")
}

func hitSupersession(h model.SearchHit) supersession {
	return supersession{id: h.Project + "/" + h.Key, supersedes: h.Supersedes, supersededBy: h.SupersededBy}
}

func docSupersession(d model.Document) supersession {
	return supersession{id: d.Project + "/" + d.Name, supersedes: d.Supersedes, supersededBy: d.SupersededBy}
}

// nodeState is the status of an issue node or the state of a document node.
func nodeState(n model.GraphNode) string {
	if n.State != nil {
		return *n.State
	}
	return deref(n.Status, "-")
}

// printFolded lists the nodes that have superseded or archived neighbours the
// graph left out, with their counts.
func printFolded(w io.Writer, nodes []model.GraphNode) {
	var lines []string
	for _, n := range nodes {
		if n.Folded == nil {
			continue
		}
		var parts []string
		if n.Folded.Superseded > 0 {
			parts = append(parts, fmt.Sprintf("superseded %d", n.Folded.Superseded))
		}
		if n.Folded.Archived > 0 {
			parts = append(parts, fmt.Sprintf("archived %d", n.Folded.Archived))
		}
		if len(parts) > 0 {
			lines = append(lines, fmt.Sprintf("  %s  %s", n.ID, strings.Join(parts, ", ")))
		}
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(w, "\n접힌 이웃 (--state all 로 보임)")
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// unresolvedText lists the relations of a document whose target does not exist
// yet.
func unresolvedText(refs []model.UnresolvedRef) string {
	if len(refs) == 0 {
		return "-"
	}
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = string(r.Kind) + " " + r.Target
	}
	return strings.Join(parts, ", ")
}
