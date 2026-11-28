package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store/pg"
)

const (
	maxNeighborHops         = 2
	maxNeighborResults      = 64
	defaultMaxContentRunes  = 4096
	defaultMaxSummaryRunes  = 320
	maxSupersedesChainDepth = 32
	defaultReadyLimit       = 20
	maxReadyLimit           = 100
)

// Neighbors returns bidirectional confirmed neighbors reachable from
// (sourceType, sourceID) within maxHops (clamped to [1, 2], capped at 64 rows)
// using a cycle-safe UNION recursive CTE.
func (s *Store) Neighbors(ctx context.Context, sourceType model.GraphNodeType, sourceID int64, maxHops int) ([]pg.NeighborSummary, error) {
	return s.neighborsWithOptions(ctx, sourceType, sourceID, maxHops, maxNeighborResults, false, 0)
}

func (s *Store) neighborsWithOptions(
	ctx context.Context,
	sourceType model.GraphNodeType,
	sourceID int64,
	maxHops int,
	maxResults int,
	includeContent bool,
	maxContentRunes int,
) ([]pg.NeighborSummary, error) {
	if _, ok := model.ParseGraphNodeType(string(sourceType)); !ok || sourceID <= 0 {
		return []pg.NeighborSummary{}, nil
	}
	if maxHops < 1 {
		maxHops = 1
	}
	if maxHops > maxNeighborHops {
		maxHops = maxNeighborHops
	}
	if maxResults <= 0 || maxResults > maxNeighborResults {
		maxResults = maxNeighborResults
	}
	if includeContent && maxContentRunes <= 0 {
		maxContentRunes = defaultMaxContentRunes
	}

	// Bidirectional cycle-safe UNION recursive CTE over confirmed resolved edges.
	// The visited array prevents revisiting any node (including the seed) even on
	// cyclic related/about graphs, while UNION deduplicates identical frontiers.
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE walk(edge_id, direction, kind, origin, status, node_type, node_id, hop, visited) AS (
			SELECT e.id, 'outbound'::text, e.kind, e.origin, e.status,
			       e.target_type, e.target_id, 1,
			       ARRAY[$1::text || ':' || ($2::bigint)::text, e.target_type || ':' || e.target_id::text]
			FROM graph_edge e
			WHERE e.status = 'confirmed'
			  AND e.source_type = $1
			  AND e.source_id = $2::bigint
			  AND e.target_type IS NOT NULL
			  AND e.target_id IS NOT NULL
			UNION
			SELECT e.id, 'inbound'::text, e.kind, e.origin, e.status,
			       e.source_type, e.source_id, 1,
			       ARRAY[$1::text || ':' || ($2::bigint)::text, e.source_type || ':' || e.source_id::text]
			FROM graph_edge e
			WHERE e.status = 'confirmed'
			  AND e.target_type = $1
			  AND e.target_id = $2::bigint
			UNION
			SELECT nxt.edge_id, nxt.direction, nxt.kind, nxt.origin, nxt.status,
			       nxt.node_type, nxt.node_id, w.hop + 1,
			       w.visited || (nxt.node_type || ':' || nxt.node_id::text)
			FROM walk w
			JOIN LATERAL (
				SELECT e.id AS edge_id, 'outbound'::text AS direction, e.kind, e.origin, e.status,
				       e.target_type AS node_type, e.target_id AS node_id
				FROM graph_edge e
				WHERE e.status = 'confirmed'
				  AND e.source_type = w.node_type
				  AND e.source_id = w.node_id
				  AND e.target_type IS NOT NULL
				  AND e.target_id IS NOT NULL
				UNION ALL
				SELECT e.id AS edge_id, 'inbound'::text AS direction, e.kind, e.origin, e.status,
				       e.source_type AS node_type, e.source_id AS node_id
				FROM graph_edge e
				WHERE e.status = 'confirmed'
				  AND e.target_type = w.node_type
				  AND e.target_id = w.node_id
			) nxt ON TRUE
			WHERE w.hop < $3
			  AND NOT ((nxt.node_type || ':' || nxt.node_id::text) = ANY(w.visited))
		),
		dedup AS (
			SELECT DISTINCT ON (edge_id, direction, node_type, node_id)
			       edge_id, direction, kind, origin, status, node_type, node_id, hop
			FROM walk
			ORDER BY edge_id, direction, node_type, node_id, hop ASC
		)
		SELECT d.edge_id, d.direction, d.kind, d.origin, d.status, d.node_type, d.node_id, d.hop,
		       COALESCE(
		           CASE d.node_type
		               WHEN 'issue'  THEN i.key
		               WHEN 'spec'   THEN sd.slug
		               WHEN 'record' THEN rd.name
		               WHEN 'epic'   THEN ep.key
		           END, ''
		       ) AS key_or_name,
		       COALESCE(
		           CASE d.node_type
		               WHEN 'issue'  THEN i.title
		               WHEN 'spec'   THEN sd.title
		               WHEN 'record' THEN CASE WHEN rd.title <> '' THEN rd.title ELSE rd.name END
		               WHEN 'epic'   THEN ep.title
		           END, ''
		       ) AS title,
		       COALESCE(
		           CASE d.node_type
		               WHEN 'issue'  THEN i.status
		               WHEN 'spec'   THEN CASE WHEN sd.archived THEN 'archived' ELSE 'active' END
		               WHEN 'record' THEN CASE WHEN rd.archived THEN 'archived' ELSE 'active' END
		               WHEN 'epic'   THEN ep.status
		           END, ''
		       ) AS node_status,
		       COALESCE(na.summary, '') AS norm_summary,
		       COALESCE(
		           CASE d.node_type
		               WHEN 'issue'  THEN i.body
		               WHEN 'spec'   THEN sr.content
		               WHEN 'record' THEN rd.content
		               WHEN 'epic'   THEN ep.description
		           END, ''
		       ) AS raw_content
		FROM dedup d
		LEFT JOIN issue i ON d.node_type = 'issue' AND d.node_id = i.id
		LEFT JOIN spec_document sd ON d.node_type = 'spec' AND d.node_id = sd.id
		LEFT JOIN spec_revision sr ON d.node_type = 'spec' AND sr.id = sd.current_revision_id
		LEFT JOIN record_document rd ON d.node_type = 'record' AND d.node_id = rd.id
		LEFT JOIN epic ep ON d.node_type = 'epic' AND d.node_id = ep.id
		LEFT JOIN LATERAL (
			SELECT summary
			FROM normalized_artifact
			WHERE source_type = d.node_type AND source_id = d.node_id
			ORDER BY id DESC
			LIMIT 1
		) na ON d.node_type IN ('issue', 'spec', 'record')
		ORDER BY d.hop ASC, d.edge_id ASC
		LIMIT $4`,
		string(sourceType), sourceID, maxHops, maxResults,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]pg.NeighborSummary, 0)
	for rows.Next() {
		var (
			ns          pg.NeighborSummary
			kind        string
			origin      string
			status      string
			nodeType    string
			normSummary string
			rawContent  string
		)
		if err := rows.Scan(
			&ns.EdgeID, &ns.Direction, &kind, &origin, &status, &nodeType, &ns.NodeID, &ns.Hop,
			&ns.KeyOrName, &ns.Title, &ns.NodeStatus, &normSummary, &rawContent,
		); err != nil {
			return nil, err
		}
		ns.Kind = model.EdgeKind(kind)
		ns.Origin = model.EdgeOrigin(origin)
		ns.Status = model.EdgeStatus(status)
		ns.NodeType = model.GraphNodeType(nodeType)
		ns.Summary = buildNeighborSummary(normSummary, rawContent, ns.Title)
		if includeContent {
			c := truncateNeighborRunes(strings.TrimSpace(rawContent), maxContentRunes)
			ns.Content = &c
		}
		out = append(out, ns)
	}
	return out, rows.Err()
}

// Expand1Hop implements pg.GraphStore by loading bidirectional confirmed
// neighborhood summaries (and optional truncated content) for each seed node.
func (s *Store) Expand1Hop(ctx context.Context, seeds []pg.NodeRef, opts pg.ExpandOptions) (map[pg.NodeRef][]pg.NeighborSummary, error) {
	out := make(map[pg.NodeRef][]pg.NeighborSummary, len(seeds))
	if len(seeds) == 0 {
		return out, nil
	}
	maxHops := opts.MaxHops
	if maxHops < 1 {
		maxHops = 1
	}
	if maxHops > maxNeighborHops {
		maxHops = maxNeighborHops
	}
	maxPerSeed := opts.MaxPerSeed
	if maxPerSeed <= 0 || maxPerSeed > maxNeighborResults {
		maxPerSeed = maxNeighborResults
	}
	maxContentRunes := opts.MaxContentRunes
	if maxContentRunes <= 0 {
		maxContentRunes = defaultMaxContentRunes
	}

	for _, seed := range seeds {
		if _, exists := out[seed]; exists {
			continue
		}
		neighbors, err := s.neighborsWithOptions(ctx, seed.Type, seed.ID, maxHops, maxPerSeed, opts.IncludeContent, maxContentRunes)
		if err != nil {
			return nil, err
		}
		if neighbors == nil {
			neighbors = []pg.NeighborSummary{}
		}
		out[seed] = neighbors
	}
	return out, nil
}

// LatestSuperseding follows confirmed supersedes chains from a superseded node
// to its latest terminal replacement using a cycle-safe UNION recursive CTE.
// Returns (nil, nil) if the node is not superseded by any confirmed edge.
func (s *Store) LatestSuperseding(ctx context.Context, sourceType model.GraphNodeType, sourceID int64) (*pg.NeighborSummary, error) {
	if _, ok := model.ParseGraphNodeType(string(sourceType)); !ok || sourceID <= 0 {
		return nil, nil
	}
	row := s.pool.QueryRow(ctx, `
		WITH RECURSIVE chain(node_type, node_id, edge_id, origin, status, depth, visited) AS (
			SELECT e.source_type, e.source_id, e.id, e.origin, e.status, 1,
			       ARRAY[$1::text || ':' || ($2::bigint)::text, e.source_type || ':' || e.source_id::text]
			FROM graph_edge e
			WHERE e.kind = 'supersedes'
			  AND e.status = 'confirmed'
			  AND e.target_type = $1
			  AND e.target_id = $2::bigint
			UNION
			SELECT e.source_type, e.source_id, e.id, e.origin, e.status, c.depth + 1,
			       c.visited || (e.source_type || ':' || e.source_id::text)
			FROM graph_edge e
			JOIN chain c ON e.target_type = c.node_type AND e.target_id = c.node_id
			WHERE e.kind = 'supersedes'
			  AND e.status = 'confirmed'
			  AND c.depth < $3
			  AND NOT ((e.source_type || ':' || e.source_id::text) = ANY(c.visited))
		),
		ranked AS (
			SELECT c.node_type, c.node_id, c.edge_id, c.origin, c.status, c.depth,
			       NOT EXISTS (
			           SELECT 1
			           FROM graph_edge newer
			           WHERE newer.kind = 'supersedes'
			             AND newer.status = 'confirmed'
			             AND newer.target_type = c.node_type
			             AND newer.target_id = c.node_id
			             AND NOT ((newer.source_type || ':' || newer.source_id::text) = ANY(c.visited))
			       ) AS is_terminal
			FROM chain c
			ORDER BY is_terminal DESC, c.depth DESC, c.node_id DESC
			LIMIT 1
		)
		SELECT r.edge_id, r.origin, r.status, r.node_type, r.node_id,
		       COALESCE(
		           CASE r.node_type
		               WHEN 'issue'  THEN i.key
		               WHEN 'spec'   THEN sd.slug
		               WHEN 'record' THEN rd.name
		               WHEN 'epic'   THEN ep.key
		           END, ''
		       ) AS key_or_name,
		       COALESCE(
		           CASE r.node_type
		               WHEN 'issue'  THEN i.title
		               WHEN 'spec'   THEN sd.title
		               WHEN 'record' THEN CASE WHEN rd.title <> '' THEN rd.title ELSE rd.name END
		               WHEN 'epic'   THEN ep.title
		           END, ''
		       ) AS title,
		       COALESCE(
		           CASE r.node_type
		               WHEN 'issue'  THEN i.status
		               WHEN 'spec'   THEN CASE WHEN sd.archived THEN 'archived' ELSE 'active' END
		               WHEN 'record' THEN CASE WHEN rd.archived THEN 'archived' ELSE 'active' END
		               WHEN 'epic'   THEN ep.status
		           END, ''
		       ) AS node_status,
		       COALESCE(na.summary, '') AS norm_summary,
		       COALESCE(
		           CASE r.node_type
		               WHEN 'issue'  THEN i.body
		               WHEN 'spec'   THEN sr.content
		               WHEN 'record' THEN rd.content
		               WHEN 'epic'   THEN ep.description
		           END, ''
		       ) AS raw_content
		FROM ranked r
		LEFT JOIN issue i ON r.node_type = 'issue' AND r.node_id = i.id
		LEFT JOIN spec_document sd ON r.node_type = 'spec' AND r.node_id = sd.id
		LEFT JOIN spec_revision sr ON r.node_type = 'spec' AND sr.id = sd.current_revision_id
		LEFT JOIN record_document rd ON r.node_type = 'record' AND r.node_id = rd.id
		LEFT JOIN epic ep ON r.node_type = 'epic' AND r.node_id = ep.id
		LEFT JOIN LATERAL (
			SELECT summary
			FROM normalized_artifact
			WHERE source_type = r.node_type AND source_id = r.node_id
			ORDER BY id DESC
			LIMIT 1
		) na ON r.node_type IN ('issue', 'spec', 'record')`,
		string(sourceType), sourceID, maxSupersedesChainDepth,
	)

	var (
		ns          pg.NeighborSummary
		origin      string
		status      string
		nodeType    string
		normSummary string
		rawContent  string
	)
	err := row.Scan(
		&ns.EdgeID, &origin, &status, &nodeType, &ns.NodeID,
		&ns.KeyOrName, &ns.Title, &ns.NodeStatus, &normSummary, &rawContent,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ns.Direction = "inbound"
	ns.Kind = model.EdgeKindSupersedes
	ns.Origin = model.EdgeOrigin(origin)
	ns.Status = model.EdgeStatus(status)
	ns.NodeType = model.GraphNodeType(nodeType)
	ns.Summary = buildNeighborSummary(normSummary, rawContent, ns.Title)
	return &ns, nil
}

// ListReadyIssues implements pg.GraphStore by resolving projectKey and
// returning the deterministic executable issue queue.
func (s *Store) ListReadyIssues(ctx context.Context, projectKey string, filter pg.ReadyFilter) ([]pg.ReadyIssue, error) {
	_, items, _, err := s.ReadyIssuesByProjectKey(ctx, projectKey, filter)
	return items, err
}

// ReadyIssuesByProjectKey resolves projectKey and returns the normalized key,
// paginated ready issues, and total matching count.
func (s *Store) ReadyIssuesByProjectKey(ctx context.Context, projectKey string, filter pg.ReadyFilter) (string, []pg.ReadyIssue, int, error) {
	normKey := model.NormalizeProjectKey(projectKey)
	var projectID int64
	err := s.pool.QueryRow(ctx, `SELECT id FROM project WHERE key = $1 AND status <> 'deleted'`, normKey).Scan(&projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, 0, pg.ErrNotFound
	}
	if err != nil {
		return "", nil, 0, err
	}
	items, total, err := s.ReadyIssues(ctx, projectID, filter)
	if err != nil {
		return "", nil, 0, err
	}
	return normKey, items, total, nil
}

// ReadyIssues returns backlog issues in projectID that have zero unresolved
// confirmed depends_on predecessors (target.status NOT IN ('done', 'cancelled')),
// ordered deterministically by priority (P0->P3), active milestone first,
// created_at ASC, and id ASC, together with the total count before pagination.
func (s *Store) ReadyIssues(ctx context.Context, projectID int64, opts pg.ReadyFilter) ([]pg.ReadyIssue, int, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultReadyLimit
	}
	if limit > maxReadyLimit {
		limit = maxReadyLimit
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}

	args := []any{projectID}
	conds := []string{
		"i.project_id = $1",
		"i.status = 'backlog'",
		`NOT EXISTS (
			SELECT 1
			FROM graph_edge e
			JOIN issue dep ON dep.id = e.target_id
			WHERE e.source_type = 'issue'
			  AND e.source_id = i.id
			  AND e.kind = 'depends_on'
			  AND e.status = 'confirmed'
			  AND e.target_type = 'issue'
			  AND dep.status NOT IN ('done', 'cancelled')
		)`,
	}
	if !opts.IncludeClaimed {
		conds = append(conds, "ic.id IS NULL")
	}
	if strings.TrimSpace(opts.EpicKey) != "" {
		args = append(args, strings.ToUpper(strings.TrimSpace(opts.EpicKey)))
		conds = append(conds, fmt.Sprintf("ep.key = $%d", len(args)))
	}
	if opts.MilestoneID != nil {
		args = append(args, *opts.MilestoneID)
		conds = append(conds, fmt.Sprintf("i.milestone_id = $%d", len(args)))
	}
	if strings.TrimSpace(opts.Label) != "" {
		args = append(args, strings.TrimSpace(opts.Label))
		conds = append(conds, fmt.Sprintf("EXISTS (SELECT 1 FROM issue_label il WHERE il.issue_id = i.id AND il.label = $%d)", len(args)))
	}

	fromClause := `
		FROM issue i
		LEFT JOIN epic ep ON ep.id = i.epic_id
		LEFT JOIN milestone ms ON ms.id = i.milestone_id
		LEFT JOIN issue_claim ic ON ic.issue_id = i.id AND ic.released_at IS NULL`
	whereClause := " WHERE " + strings.Join(conds, " AND ")

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*)`+fromClause+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	limitArg := fmt.Sprintf("$%d", len(args)-1)
	offsetArg := fmt.Sprintf("$%d", len(args))

	query := `
		SELECT i.id, i.key, i.title, i.type, i.status, i.priority,
		       ep.key, i.milestone_id,
		       CASE WHEN ic.id IS NOT NULL THEN ic.actor_id || '/' || ic.session_id ELSE NULL END AS claimed_by,
		       (
		           SELECT COUNT(*)
		           FROM graph_edge e
		           JOIN issue dep ON dep.id = e.target_id
		           WHERE e.source_type = 'issue'
		             AND e.source_id = i.id
		             AND e.kind = 'depends_on'
		             AND e.status = 'confirmed'
		             AND e.target_type = 'issue'
		             AND dep.status IN ('done', 'cancelled')
		       )::int AS unblocked_dependencies` +
		fromClause + whereClause + `
		ORDER BY CASE i.priority
		             WHEN 'P0' THEN 0
		             WHEN 'P1' THEN 1
		             WHEN 'P2' THEN 2
		             WHEN 'P3' THEN 3
		             ELSE 4
		         END ASC,
		         CASE WHEN ms.status = 'active' THEN 0 ELSE 1 END ASC,
		         i.created_at ASC,
		         i.id ASC
		LIMIT ` + limitArg + ` OFFSET ` + offsetArg

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := make([]pg.ReadyIssue, 0)
	for rows.Next() {
		var (
			it     pg.ReadyIssue
			typ    string
			status string
			prio   string
		)
		if err := rows.Scan(
			&it.ID, &it.Key, &it.Title, &typ, &status, &prio,
			&it.EpicKey, &it.MilestoneID, &it.ClaimedBy, &it.UnblockedDependencies,
		); err != nil {
			return nil, 0, err
		}
		it.Type = model.IssueType(typ)
		it.Status = model.IssueStatus(status)
		it.Priority = model.Priority(prio)
		items = append(items, it)
	}
	return items, total, rows.Err()
}

// ResolveNodeProjectID looks up the owning project_id for a resolved domain node.
func (s *Store) ResolveNodeProjectID(ctx context.Context, node pg.NodeRef) (int64, error) {
	var q string
	switch node.Type {
	case model.GraphNodeIssue:
		q = `SELECT project_id FROM issue WHERE id = $1`
	case model.GraphNodeSpec:
		q = `SELECT project_id FROM spec_document WHERE id = $1`
	case model.GraphNodeRecord:
		q = `SELECT project_id FROM record_document WHERE id = $1`
	case model.GraphNodeEpic:
		q = `SELECT project_id FROM epic WHERE id = $1`
	default:
		return 0, pg.ErrNotFound
	}
	var projectID int64
	err := s.pool.QueryRow(ctx, q, node.ID).Scan(&projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, pg.ErrNotFound
	}
	return projectID, err
}

func buildNeighborSummary(normSummary, rawContent, title string) string {
	s := strings.TrimSpace(normSummary)
	if s == "" {
		s = strings.Join(strings.Fields(strings.TrimSpace(rawContent)), " ")
	}
	if s == "" {
		s = strings.TrimSpace(title)
	}
	return truncateNeighborRunes(s, defaultMaxSummaryRunes)
}

func truncateNeighborRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return strings.TrimSpace(string(r[:maxRunes])) + "..."
}
