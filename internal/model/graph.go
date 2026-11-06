package model

import (
	"regexp"
	"strings"
)

// GraphEdgeType is the kind of link between two graph nodes.
type GraphEdgeType string

const (
	EdgeParent     GraphEdgeType = "parent"     // child issue → parent issue
	EdgeDependsOn  GraphEdgeType = "depends_on" // issue → issue it waits for
	EdgeRef        GraphEdgeType = "ref"        // issue → issue or document it refers to
	EdgeAbout      GraphEdgeType = "about"      // document → issue its front matter names
	EdgeSupersedes GraphEdgeType = "supersedes" // document → document it replaces
	EdgeRelated    GraphEdgeType = "related"    // document → document its front matter relates
)

// GraphNode is an issue or a record document. ID is "issue:<KEY>" or
// "doc:<PROJECT>/<name>". Status is set for an issue only. Refs are the
// issue's references that are not nodes, such as commit: and url:.
type GraphNode struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Project string   `json:"project"`
	Key     string   `json:"key"`
	Title   *string  `json:"title"`
	Kind    string   `json:"kind"`
	Status  *string  `json:"status"`
	Refs    []string `json:"refs"`
}

// GraphEdge is a directed link between the nodes with the given IDs.
type GraphEdge struct {
	From string        `json:"from"`
	To   string        `json:"to"`
	Type GraphEdgeType `json:"type"`
}

// Graph is the part of the decision graph around a node. The first node is
// the one asked for.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// IssueNodeID and DocumentNodeID build node IDs.
func IssueNodeID(key string) string { return "issue:" + key }

func DocumentNodeID(project, name string) string { return "doc:" + project + "/" + name }

// ParseNodeID splits a node ID. For an issue, project is empty and key is
// the issue key; for a document, key is the document name, which may hold
// slashes itself.
func ParseNodeID(id string) (typ, project, key string, ok bool) {
	switch {
	case strings.HasPrefix(id, "issue:"):
		key = id[len("issue:"):]
		return "issue", "", key, issueKeyRE.MatchString(key)
	case strings.HasPrefix(id, "doc:"):
		project, key, ok = strings.Cut(id[len("doc:"):], "/")
		return "document", project, key, ok && ValidProjectKey(project) && key != ""
	}
	return "", "", "", false
}

var (
	linkKeyRE  = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,9}-[1-9][0-9]*\b`)
	linkFileRE = regexp.MustCompile(`[^\s\[\],'"` + "`" + `]+\.md`)
	linkFMKeys = map[string]bool{"issue": true, "issues": true, "supersedes": true, "related": true}
)

// FrontMatterLinks reads the link fields of a record document's front
// matter: issue keys from issue or issues, and file names from supersedes
// and related. Values may be lists or carry prose; only issue keys and *.md
// names are taken. A document without a closed front matter block has none.
func FrontMatterLinks(content string) (issues, supersedes, related []string) {
	first, rest, found := strings.Cut(content, "\n")
	if !found || !isFence(first) {
		return nil, nil, nil
	}
	for more := true; more; {
		var line string
		line, rest, more = strings.Cut(rest, "\n")
		if isFence(line) {
			return issues, supersedes, related
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || !linkFMKeys[strings.TrimSpace(key)] || key != strings.TrimLeft(key, " \t") {
			continue
		}
		switch strings.TrimSpace(key) {
		case "issue", "issues":
			issues = append(issues, linkKeyRE.FindAllString(value, -1)...)
		case "supersedes":
			supersedes = append(supersedes, linkFileRE.FindAllString(value, -1)...)
		case "related":
			related = append(related, linkFileRE.FindAllString(value, -1)...)
		}
	}
	return nil, nil, nil
}
