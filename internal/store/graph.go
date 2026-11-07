package store

import (
	"context"
	"database/sql"
	"path"
	"sort"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
)

// Graph returns the decision graph around a node: the nodes reachable from it
// in depth steps along edges of either direction, and the edges among them.
// The node itself comes first. A missing node, or a deleted issue, is
// ErrNotFound.
//
// ponytail: the whole graph is built from the issue and document tables on
// every call. Fine for hundreds of issues and documents; keep derived edge
// columns or an edge table when documents reach the thousands.
func (s *Store) Graph(ctx context.Context, node string, depth int) (*model.Graph, error) {
	g, err := s.buildGraph(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := g.nodes[node]; !ok {
		return nil, ErrNotFound
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
					if !seen[other] {
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
		out.Nodes = append(out.Nodes, g.nodes[id])
	}
	for _, e := range g.edges {
		if seen[e.From] && seen[e.To] {
			out.Edges = append(out.Edges, e)
		}
	}
	return out, nil
}

type graph struct {
	nodes map[string]model.GraphNode
	edges []model.GraphEdge
}

func (s *Store) buildGraph(ctx context.Context) (*graph, error) {
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

	type doc struct {
		id, project, content string
	}
	var docs []doc
	// byName finds a document by its name and, for a legacy path, its base
	// name, which is how other records cite it.
	byName := map[string][]string{}
	rows, err = s.db.QueryContext(ctx, `select p.key, d.name, d.title, d.kind, d.content from document d join project p on p.id = d.project_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n model.GraphNode
		var title sql.NullString
		var content string
		if err := rows.Scan(&n.Project, &n.Key, &title, &n.Kind, &content); err != nil {
			rows.Close()
			return nil, err
		}
		n.ID, n.Type, n.Refs = model.DocumentNodeID(n.Project, n.Key), "document", []string{}
		if title.Valid {
			n.Title = &title.String
		}
		g.nodes[n.ID] = n
		docs = append(docs, doc{n.ID, n.Project, content})
		byName[n.Key] = append(byName[n.Key], n.ID)
		if base := path.Base(n.Key); base != n.Key {
			byName[base] = append(byName[base], n.ID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// resolve picks the document a file name means: the one in the same
	// project, or else the only one anywhere. An ambiguous name links nothing.
	resolve := func(project, name string) (string, bool) {
		ids := byName[name]
		var same []string
		for _, id := range ids {
			if strings.HasPrefix(id, "doc:"+project+"/") {
				same = append(same, id)
			}
		}
		switch {
		case len(same) == 1:
			return same[0], true
		case len(same) == 0 && len(ids) == 1:
			return ids[0], true
		}
		return "", false
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

	rows, err = s.db.QueryContext(ctx, `select i.key, p.key, r.ref from issue_ref r join issue i on i.id = r.issue_id join project p on p.id = i.project_id order by i.key, r.ref`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key, project, ref string
		if err := rows.Scan(&key, &project, &ref); err != nil {
			rows.Close()
			return nil, err
		}
		from := model.IssueNodeID(key)
		kind, value, _ := strings.Cut(ref, ":")
		switch kind {
		case "issue":
			add(from, model.IssueNodeID(value), model.EdgeRef)
		case "doc":
			if to, ok := resolve(project, value); ok {
				add(from, to, model.EdgeRef)
			}
		default:
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

	for _, d := range docs {
		issues, supersedes, related := model.FrontMatterLinks(d.content)
		for _, key := range issues {
			add(d.id, model.IssueNodeID(key), model.EdgeAbout)
		}
		for _, name := range supersedes {
			if to, ok := resolve(d.project, path.Base(name)); ok {
				add(d.id, to, model.EdgeSupersedes)
			}
		}
		for _, name := range related {
			if to, ok := resolve(d.project, path.Base(name)); ok {
				add(d.id, to, model.EdgeRelated)
			}
		}
	}
	// Inferred links come last and only between documents that no written
	// link joins already.
	linked := map[[2]string]bool{}
	for _, e := range g.edges {
		linked[[2]string{e.From, e.To}] = true
		linked[[2]string{e.To, e.From}] = true
	}
	rows, err = s.db.QueryContext(ctx, `select pa.key, a.name, pb.key, b.name, sd.score from similar_document sd
		join document a on a.id = sd.document_id join project pa on pa.id = a.project_id
		join document b on b.id = sd.similar_id join project pb on pb.id = b.project_id
		order by sd.score desc`)
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
