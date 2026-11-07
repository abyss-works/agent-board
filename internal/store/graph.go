package store

import (
	"context"
	"sort"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// Graph returns the decision graph around a node: the nodes reachable from it
// in depth steps along edges of either direction, and the edges among them.
// The node itself comes first. A missing node, or a deleted issue, is
// ErrNotFound.
//
// With fold, a document that is superseded or archived is left out as a
// neighbor and is no way to a farther node; the nodes next to such documents
// count them by state in Folded. The node asked for is shown whatever its state.
//
// ponytail: the whole graph is built from the issue, document and relation
// tables on every call. Fine for hundreds of issues and documents; keep derived
// edge columns or an edge table when documents reach the thousands.
func (s *Store) Graph(ctx context.Context, node string, depth int, similarModel string, fold bool) (*model.Graph, error) {
	g, err := s.buildGraph(ctx, similarModel)
	if err != nil {
		return nil, err
	}
	if _, ok := g.nodes[node]; !ok {
		return nil, ErrNotFound
	}
	// folded reports a document that fold leaves out.
	folded := func(id string) bool {
		if !fold || id == node {
			return false
		}
		n := g.nodes[id]
		return n.State != nil && *n.State != string(model.StateCurrent)
	}
	adj := map[string][]int{}
	for n, e := range g.edges {
		adj[e.From] = append(adj[e.From], n)
		adj[e.To] = append(adj[e.To], n)
	}
	seen := map[string]bool{node: true}
	order := []string{node}
	frontier := []string{node}
	for step := 0; step < depth; step++ {
		var next []string
		for _, id := range frontier {
			for _, n := range adj[id] {
				for _, other := range []string{g.edges[n].From, g.edges[n].To} {
					if !seen[other] && !folded(other) {
						seen[other] = true
						order = append(order, other)
						next = append(next, other)
					}
				}
			}
		}
		frontier = next
	}
	out := &model.Graph{Nodes: []model.GraphNode{}, Edges: []model.GraphEdge{}}
	for _, id := range order {
		n := g.nodes[id]
		if fold {
			n.Folded = foldedAround(g, adj, id, seen)
		}
		out.Nodes = append(out.Nodes, n)
	}
	for _, e := range g.edges {
		if seen[e.From] && seen[e.To] {
			out.Edges = append(out.Edges, e)
		}
	}
	return out, nil
}

// foldedAround counts, by state, the documents next to the node that are not in
// the graph shown. It is nil when there are none.
func foldedAround(g *graph, adj map[string][]int, id string, shown map[string]bool) *model.GraphFolded {
	var f model.GraphFolded
	counted := map[string]bool{}
	for _, n := range adj[id] {
		for _, other := range []string{g.edges[n].From, g.edges[n].To} {
			o := g.nodes[other]
			if other == id || shown[other] || counted[other] || o.State == nil {
				continue
			}
			counted[other] = true
			switch model.DocumentState(*o.State) {
			case model.StateSuperseded:
				f.Superseded++
			case model.StateArchived:
				f.Archived++
			}
		}
	}
	if f.Superseded+f.Archived == 0 {
		return nil
	}
	return &f
}

type graph struct {
	nodes map[string]model.GraphNode
	edges []model.GraphEdge
}

func (s *Store) buildGraph(ctx context.Context, similarModel string) (*graph, error) {
	g := &graph{nodes: map[string]model.GraphNode{}}
	rows, err := s.db.QueryContext(ctx, `select i.key, p.key, i.title, i.type, i.status from issue i join project p on p.id = i.project_id where i.status <> 'deleted'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n model.GraphNode
		var title, status string
		if err := rows.Scan(&n.Key, &n.Project, &title, &n.Kind, &status); err != nil {
			rows.Close()
			return nil, err
		}
		n.ID, n.Type, n.Title, n.Status, n.Refs = model.IssueNodeID(n.Key), "issue", &title, &status, []string{}
		g.nodes[n.ID] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.QueryContext(ctx, `select p.key, d.name, d.title, d.kind, (`+documentState+`) from document d join project p on p.id = d.project_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n model.GraphNode
		var state string
		var title *string
		if err := rows.Scan(&n.Project, &n.Key, &title, &n.Kind, &state); err != nil {
			rows.Close()
			return nil, err
		}
		n.ID, n.Type, n.Title, n.State, n.Refs = model.DocumentNodeID(n.Project, n.Key), "document", title, &state, []string{}
		g.nodes[n.ID] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	add := func(from, to string, typ model.GraphEdgeType) {
		_, a := g.nodes[from]
		_, b := g.nodes[to]
		if a && b && from != to {
			g.edges = append(g.edges, model.GraphEdge{From: from, To: to, Type: typ})
		}
	}

	pairs := func(q string, typ model.GraphEdgeType) error {
		rows, err := s.db.QueryContext(ctx, q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var from, to string
			if err := rows.Scan(&from, &to); err != nil {
				return err
			}
			add(model.IssueNodeID(from), model.IssueNodeID(to), typ)
		}
		return rows.Err()
	}
	if err := pairs(`select i.key, par.key from issue i join issue par on par.id = i.parent_id`, model.EdgeParent); err != nil {
		return nil, err
	}
	if err := pairs(`select a.key, b.key from issue_dependency d join issue a on a.id = d.issue_id join issue b on b.id = d.depends_on_id`, model.EdgeDependsOn); err != nil {
		return nil, err
	}

	// The references of an issue: those that resolved to an issue or a
	// document are edges, the others that are no node, such as a commit, stay
	// on the node. A reference that did not resolve links nothing.
	rows, err = s.db.QueryContext(ctx, `select i.key, r.ref, ti.key, tp.key, td.name
		from issue_ref r join issue i on i.id = r.issue_id
		left join issue ti on ti.id = r.target_issue_id
		left join document td on td.id = r.target_document_id left join project tp on tp.id = td.project_id
		order by i.key, r.ref`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key, ref string
		var issueKey, project, docName *string
		if err := rows.Scan(&key, &ref, &issueKey, &project, &docName); err != nil {
			rows.Close()
			return nil, err
		}
		from := model.IssueNodeID(key)
		kind, _, _ := strings.Cut(ref, ":")
		switch {
		case issueKey != nil:
			add(from, model.IssueNodeID(*issueKey), model.EdgeRef)
		case docName != nil:
			add(from, model.DocumentNodeID(*project, *docName), model.EdgeRef)
		case kind != "issue" && kind != "doc":
			if n, ok := g.nodes[from]; ok {
				n.Refs = append(n.Refs, ref)
				g.nodes[from] = n
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// The relations of a document that resolved. A relation kept as text links nothing.
	rows, err = s.db.QueryContext(ctx, `select sp.key, sd.name, r.kind, tp.key, td.name, ti.key
		from document_relation r
		join document sd on sd.id = r.document_id join project sp on sp.id = sd.project_id
		left join document td on td.id = r.target_document_id left join project tp on tp.id = td.project_id
		left join issue ti on ti.id = r.target_issue_id
		where r.target_text is null order by r.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var project, name, kind string
		var targetProject, targetDoc, targetIssue *string
		if err := rows.Scan(&project, &name, &kind, &targetProject, &targetDoc, &targetIssue); err != nil {
			rows.Close()
			return nil, err
		}
		from := model.DocumentNodeID(project, name)
		if targetIssue != nil {
			add(from, model.IssueNodeID(*targetIssue), model.EdgeAbout)
		} else {
			add(from, model.DocumentNodeID(*targetProject, *targetDoc), model.GraphEdgeType(kind))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Inferred links come last and only between documents that no written
	// link joins already. Only pairs found with the model in use count; an
	// empty model (search by meaning off) matches none, so stored pairs left
	// from an earlier model or key stay hidden.
	linked := map[[2]string]bool{}
	for _, e := range g.edges {
		linked[[2]string{e.From, e.To}] = true
		linked[[2]string{e.To, e.From}] = true
	}
	rows, err = s.db.QueryContext(ctx, `select pa.key, a.name, pb.key, b.name, sd.score from similar_document sd
		join document a on a.id = sd.document_id join project pa on pa.id = a.project_id
		join document b on b.id = sd.similar_id join project pb on pb.id = b.project_id
		where sd.model = ? order by sd.score desc`, similarModel)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var pa, na, pb, nb string
		var score float64
		if err := rows.Scan(&pa, &na, &pb, &nb, &score); err != nil {
			rows.Close()
			return nil, err
		}
		from, to := model.DocumentNodeID(pa, na), model.DocumentNodeID(pb, nb)
		if linked[[2]string{from, to}] {
			continue
		}
		linked[[2]string{from, to}] = true
		linked[[2]string{to, from}] = true
		g.edges = append(g.edges, model.GraphEdge{From: from, To: to, Type: model.EdgeSimilar, Score: &score})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(g.edges, func(a, b int) bool {
		x, y := g.edges[a], g.edges[b]
		if x.From != y.From {
			return x.From < y.From
		}
		if x.Type != y.Type {
			return x.Type < y.Type
		}
		return x.To < y.To
	})
	return g, nil
}
