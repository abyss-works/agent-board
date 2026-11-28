package pg

import (
	"context"
	"sort"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

// CompatGraph returns the decision graph around a node ("issue:<KEY>" or
// "doc:<PROJECT>/<name>") the way the classic API shows it: parent and depends_on
// links between issues, the references of issues, and the confirmed relations of
// record documents.
func (s *Store) CompatGraph(ctx context.Context, node string, depth int, fold, similar bool) (*model.Graph, error) {
	nodes := map[string]model.GraphNode{}
	var edges []model.GraphEdge
	add := func(from, to string, typ model.GraphEdgeType) {
		_, a := nodes[from]
		_, b := nodes[to]
		if a && b && from != to {
			edges = append(edges, model.GraphEdge{From: from, To: to, Type: typ})
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT i.key, p.key, i.title, i.type, i.status FROM issue i JOIN project p ON p.id = i.project_id WHERE i.status <> 'deleted'`)
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
		nodes[n.ID] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.pool.Query(ctx, `SELECT p.key, d.name, NULLIF(d.title, ''), d.kind, (`+docStateExpr+`) FROM record_document d JOIN project p ON p.id = d.project_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n model.GraphNode
		var state string
		if err := rows.Scan(&n.Project, &n.Key, &n.Title, &n.Kind, &state); err != nil {
			rows.Close()
			return nil, err
		}
		n.ID, n.Type, n.State, n.Refs = model.DocumentNodeID(n.Project, n.Key), "document", &state, []string{}
		nodes[n.ID] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	pairs := func(q string, typ model.GraphEdgeType) error {
		rs, err := s.pool.Query(ctx, q)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var from, to string
			if err := rs.Scan(&from, &to); err != nil {
				return err
			}
			add(model.IssueNodeID(from), model.IssueNodeID(to), typ)
		}
		return rs.Err()
	}
	if err := pairs(`SELECT i.key, e.source_issue_key FROM issue i JOIN epic e ON e.id = i.epic_id WHERE e.source_issue_key IS NOT NULL AND e.source_issue_key <> i.key`, model.EdgeParent); err != nil {
		return nil, err
	}
	if err := pairs(`SELECT a.key, b.key FROM graph_edge ge JOIN issue a ON a.id = ge.source_id JOIN issue b ON b.id = ge.target_id
		WHERE ge.kind = 'depends_on' AND ge.status = 'confirmed' AND ge.source_type = 'issue' AND ge.target_type = 'issue'`, model.EdgeDependsOn); err != nil {
		return nil, err
	}

	rows, err = s.pool.Query(ctx, `SELECT i.key, r.ref, ti.key, tp.key, td.name
		FROM issue_ref r JOIN issue i ON i.id = r.issue_id
		LEFT JOIN issue ti ON ti.id = r.target_issue_id
		LEFT JOIN record_document td ON td.id = r.target_record_id LEFT JOIN project tp ON tp.id = td.project_id
		ORDER BY i.key, r.ref`)
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
			if n, ok := nodes[from]; ok {
				n.Refs = append(n.Refs, ref)
				nodes[from] = n
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.pool.Query(ctx, `SELECT sp.key, sd.name, ge.kind, tp.key, td.name, ti.key, ge.origin, ge.confidence
		FROM graph_edge ge
		JOIN record_document sd ON sd.id = ge.source_id JOIN project sp ON sp.id = sd.project_id
		LEFT JOIN record_document td ON ge.target_type = 'record' AND td.id = ge.target_id LEFT JOIN project tp ON tp.id = td.project_id
		LEFT JOIN issue ti ON ge.target_type = 'issue' AND ti.id = ge.target_id
		WHERE ge.source_type = 'record' AND ge.status = 'confirmed' AND ge.unresolved_target_text IS NULL AND ge.kind IN ('supersedes', 'related', 'about')
		ORDER BY ge.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var project, name, kind, origin string
		var targetProject, targetDoc, targetIssue *string
		var confidence *float64
		if err := rows.Scan(&project, &name, &kind, &targetProject, &targetDoc, &targetIssue, &origin, &confidence); err != nil {
			rows.Close()
			return nil, err
		}
		from := model.DocumentNodeID(project, name)
		switch {
		case targetIssue != nil:
			add(from, model.IssueNodeID(*targetIssue), model.EdgeAbout)
		case targetDoc != nil && targetProject != nil && kind == "related" && origin == "inferred" && !similar:
			// A link nobody wrote is shown only when the server ranks by meaning.
		case targetDoc != nil && targetProject != nil && kind == "related" && origin == "inferred":
			// It shows as a similar link with its score.
			to := model.DocumentNodeID(*targetProject, *targetDoc)
			_, a := nodes[from]
			_, b := nodes[to]
			if a && b && from != to {
				edges = append(edges, model.GraphEdge{From: from, To: to, Type: model.EdgeSimilar, Score: confidence})
			}
		case targetDoc != nil && targetProject != nil:
			add(from, model.DocumentNodeID(*targetProject, *targetDoc), model.GraphEdgeType(kind))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(edges, func(a, b int) bool {
		x, y := edges[a], edges[b]
		if x.From != y.From {
			return x.From < y.From
		}
		if x.Type != y.Type {
			return x.Type < y.Type
		}
		return x.To < y.To
	})
	g, err := store.WalkGraph(nodes, edges, node, depth, fold)
	if err != nil {
		if err == store.ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return g, nil
}
