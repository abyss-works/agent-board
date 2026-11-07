package model

import "strings"

// GraphEdgeType is the kind of link between two graph nodes.
type GraphEdgeType string

const (
	EdgeParent     GraphEdgeType = "parent"     // child issue → parent issue
	EdgeDependsOn  GraphEdgeType = "depends_on" // issue → issue it waits for
	EdgeRef        GraphEdgeType = "ref"        // issue → issue or document it refers to
	EdgeAbout      GraphEdgeType = "about"      // document → issue it is about
	EdgeSupersedes GraphEdgeType = "supersedes" // document → document it replaces
	EdgeRelated    GraphEdgeType = "related"    // document → related document
	// EdgeSimilar links documents close in meaning by their embeddings. It is
	// inferred, not written by anyone, and carries a score.
	EdgeSimilar GraphEdgeType = "similar"
)

// GraphNode is an issue or a record document. ID is "issue:<KEY>" or
// "doc:<PROJECT>/<name>". Status is set for an issue and State for a document.
// Refs are the issue's references that are not nodes, such as commit: and
// url:. Folded counts the superseded and archived documents next to the node
// that the graph leaves out; it is nil when there are none.
type GraphNode struct {
	ID      string       `json:"id"`
	Type    string       `json:"type"`
	Project string       `json:"project"`
	Key     string       `json:"key"`
	Title   *string      `json:"title"`
	Kind    string       `json:"kind"`
	Status  *string      `json:"status"`
	State   *string      `json:"state"`
	Refs    []string     `json:"refs"`
	Folded  *GraphFolded `json:"folded"`
}

// GraphFolded counts left out documents by state.
type GraphFolded struct {
	Superseded int `json:"superseded"`
	Archived   int `json:"archived"`
}

// GraphEdge is a directed link between the nodes with the given IDs.
type GraphEdge struct {
	From string        `json:"from"`
	To   string        `json:"to"`
	Type GraphEdgeType `json:"type"`
	// Score is the cosine similarity of a similar edge; other edges have none.
	Score *float64 `json:"score,omitempty"`
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
