// Package graph manages directed semantic and dependency edges between domain
// nodes (issues, specs, records, epics) and extracts graph relationships from
// source content without mutating source domain tables.
package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store/pg"
)

const maxUpsertUniqueRetries = 2

// ErrCycle is returned when adding a depends_on or supersedes edge would create
// a directed cycle (including a self-loop).
var ErrCycle = errors.New("cycle")

// Store persists and queries graph_edge records on PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

// Compile-time check that Store implements pg.GraphStore.
var _ pg.GraphStore = (*Store)(nil)

// NewStore returns a graph store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// UpsertExplicitEdge idempotently records a confirmed explicit edge
// (origin='explicit', status='confirmed'). Cycles on depends_on and supersedes
// are rejected with ErrCycle.
func (s *Store) UpsertExplicitEdge(ctx context.Context, in pg.UpsertEdgeInput) (model.Edge, error) {
	if err := validateEdgeInput(in); err != nil {
		return model.Edge{}, err
	}
	if isSelfLoop(in) {
		return model.Edge{}, ErrCycle
	}

	var lastErr error
	for attempt := 0; attempt <= maxUpsertUniqueRetries; attempt++ {
		edge, err := s.upsertExplicitEdgeOnce(ctx, in)
		if err == nil {
			return edge, nil
		}
		lastErr = err
		if !isUniqueViolation(err) || ctx.Err() != nil {
			return model.Edge{}, err
		}
	}
	return model.Edge{}, lastErr
}

func (s *Store) upsertExplicitEdgeOnce(ctx context.Context, in pg.UpsertEdgeInput) (model.Edge, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.Edge{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if requiresCycleCheck(in.Kind) && in.Target != nil {
		if err := lockEdgeKindTx(ctx, tx, in.Kind); err != nil {
			return model.Edge{}, err
		}
		cyclic, err := wouldCreateCycleTx(ctx, tx, in.Source, *in.Target, in.Kind)
		if err != nil {
			return model.Edge{}, err
		}
		if cyclic {
			return model.Edge{}, ErrCycle
		}
	}

	edge, err := upsertExplicitEdgeTx(ctx, tx, in)
	if err != nil {
		return model.Edge{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Edge{}, err
	}
	return edge, nil
}

// UpsertInferredEdge persists an inferred graph edge following the review
// policy:
//   - about and related edges are immediately confirmed (origin='inferred', status='confirmed').
//   - supersedes edges are always stored as candidate (origin='inferred', status='candidate')
//     and never auto-confirmed.
//   - Any existing pair already in confirmed or rejected status is preserved
//     without being overwritten.
func (s *Store) UpsertInferredEdge(ctx context.Context, in pg.UpsertEdgeInput) (model.Edge, error) {
	if err := validateEdgeInput(in); err != nil {
		return model.Edge{}, err
	}
	if isSelfLoop(in) {
		return model.Edge{}, ErrCycle
	}

	var lastErr error
	for attempt := 0; attempt <= maxUpsertUniqueRetries; attempt++ {
		edge, err := s.upsertInferredEdgeOnce(ctx, in)
		if err == nil {
			return edge, nil
		}
		lastErr = err
		if !isUniqueViolation(err) || ctx.Err() != nil {
			return model.Edge{}, err
		}
	}
	return model.Edge{}, lastErr
}

func (s *Store) upsertInferredEdgeOnce(ctx context.Context, in pg.UpsertEdgeInput) (model.Edge, error) {
	targetStatus := model.EdgeStatusConfirmed
	if in.Kind == model.EdgeKindSupersedes {
		targetStatus = model.EdgeStatusCandidate
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.Edge{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if requiresCycleCheck(in.Kind) && in.Target != nil {
		if err := lockEdgeKindTx(ctx, tx, in.Kind); err != nil {
			return model.Edge{}, err
		}
		cyclic, err := wouldCreateCycleTx(ctx, tx, in.Source, *in.Target, in.Kind)
		if err != nil {
			return model.Edge{}, err
		}
		if cyclic {
			return model.Edge{}, ErrCycle
		}
	}

	// Check for an existing edge on the same pair, prioritizing confirmed/rejected
	// decisions so prior human or explicit decisions are never overwritten.
	existing, found, err := findExistingEdgeTx(ctx, tx, in)
	if err != nil {
		return model.Edge{}, err
	}
	if found {
		switch existing.Status {
		case model.EdgeStatusConfirmed, model.EdgeStatusRejected:
			if err := tx.Commit(ctx); err != nil {
				return model.Edge{}, err
			}
			return existing, nil
		case model.EdgeStatusCandidate:
			// Refresh confidence/evidence on an existing candidate edge.
			updated, err := updateCandidateMetadataTx(ctx, tx, existing.ID, in.Confidence, in.Evidence)
			if err != nil {
				return model.Edge{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return model.Edge{}, err
			}
			return updated, nil
		}
	}

	edge, err := insertEdgeTx(ctx, tx, in, model.EdgeOriginInferred, targetStatus)
	if err != nil {
		return model.Edge{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Edge{}, err
	}
	return edge, nil
}

// ListCandidatesForSource returns all pending candidate supersedes edges
// originating from (sourceType, sourceID), resolved with target display names.
func (s *Store) ListCandidatesForSource(ctx context.Context, sourceType model.GraphNodeType, sourceID int64) ([]pg.SupersedesCandidate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.id,
		       COALESCE(e.target_type, ''),
		       COALESCE(e.target_id, 0),
		       COALESCE(
		           CASE e.target_type
		               WHEN 'issue'  THEN i.key
		               WHEN 'spec'   THEN sp.slug
		               WHEN 'record' THEN rd.name
		               WHEN 'epic'   THEN ep.key
		               ELSE e.unresolved_target_text
		           END,
		           e.unresolved_target_text,
		           ''
		       ) AS target_name,
		       e.confidence,
		       e.evidence
		FROM graph_edge e
		LEFT JOIN issue i ON e.target_type = 'issue' AND e.target_id = i.id
		LEFT JOIN spec_document sp ON e.target_type = 'spec' AND e.target_id = sp.id
		LEFT JOIN record_document rd ON e.target_type = 'record' AND e.target_id = rd.id
		LEFT JOIN epic ep ON e.target_type = 'epic' AND e.target_id = ep.id
		WHERE e.source_type = $1
		  AND e.source_id = $2
		  AND e.kind = 'supersedes'
		  AND e.status = 'candidate'
		ORDER BY e.confidence DESC NULLS LAST, e.id ASC`,
		string(sourceType), sourceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]pg.SupersedesCandidate, 0)
	for rows.Next() {
		var c pg.SupersedesCandidate
		if err := rows.Scan(&c.EdgeID, &c.TargetType, &c.TargetID, &c.TargetName, &c.Confidence, &c.Evidence); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListEdgesForSource returns all edges originating from (sourceType, sourceID).
func (s *Store) ListEdgesForSource(ctx context.Context, sourceType model.GraphNodeType, sourceID int64) ([]model.Edge, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.project_id, p.key, e.source_type, e.source_id,
		       e.target_type, e.target_id, e.unresolved_target_text,
		       e.kind, e.origin, e.status, e.confidence, e.evidence,
		       e.decided_by, e.decided_at, e.created_at
		FROM graph_edge e
		JOIN project p ON p.id = e.project_id
		WHERE e.source_type = $1 AND e.source_id = $2
		ORDER BY e.id ASC`,
		string(sourceType), sourceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Edge
	for rows.Next() {
		edge, err := scanEdge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, edge)
	}
	return out, rows.Err()
}

// ListCandidates lists candidate edges matching filter in reverse chronological order.
func (s *Store) ListCandidates(ctx context.Context, filter pg.CandidateFilter) ([]pg.EdgeCandidate, error) {
	items, _, err := s.ListCandidatesPaged(ctx, filter)
	return items, err
}

// ListCandidatesPaged lists candidate edges matching filter together with the
// total matching count before pagination.
func (s *Store) ListCandidatesPaged(ctx context.Context, filter pg.CandidateFilter) ([]pg.EdgeCandidate, int, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	args := []any{}
	conds := []string{"e.status = 'candidate'"}
	if strings.TrimSpace(filter.Project) != "" {
		args = append(args, model.NormalizeProjectKey(filter.Project))
		conds = append(conds, fmt.Sprintf("p.key = $%d", len(args)))
	}
	if filter.Kind != "" {
		args = append(args, string(filter.Kind))
		conds = append(conds, fmt.Sprintf("e.kind = $%d", len(args)))
	}
	if filter.SourceType != "" {
		args = append(args, string(filter.SourceType))
		conds = append(conds, fmt.Sprintf("e.source_type = $%d", len(args)))
	}
	if filter.SourceID > 0 {
		args = append(args, filter.SourceID)
		conds = append(conds, fmt.Sprintf("e.source_id = $%d", len(args)))
	}

	whereClause := strings.Join(conds, " AND ")
	var total int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM graph_edge e
		JOIN project p ON p.id = e.project_id
		WHERE `+whereClause, args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	limitArg := fmt.Sprintf("$%d", len(args)-1)
	offsetArg := fmt.Sprintf("$%d", len(args))

	query := `
		SELECT e.id, e.project_id, p.key, e.source_type, e.source_id,
		       e.target_type, e.target_id, e.unresolved_target_text,
		       e.kind, e.origin, e.status, e.confidence, e.evidence,
		       e.decided_by, e.decided_at, e.created_at,
		       COALESCE(
		           CASE e.source_type
		               WHEN 'issue'  THEN si.key
		               WHEN 'spec'   THEN ssp.slug
		               WHEN 'record' THEN srd.name
		               WHEN 'epic'   THEN sep.key
		           END,
		           ''
		       ) AS source_name,
		       COALESCE(
		           CASE e.target_type
		               WHEN 'issue'  THEN ti.key
		               WHEN 'spec'   THEN tsp.slug
		               WHEN 'record' THEN trd.name
		               WHEN 'epic'   THEN tep.key
		               ELSE e.unresolved_target_text
		           END,
		           e.unresolved_target_text,
		           ''
		       ) AS target_name
		FROM graph_edge e
		JOIN project p ON p.id = e.project_id
		LEFT JOIN issue si ON e.source_type = 'issue' AND e.source_id = si.id
		LEFT JOIN spec_document ssp ON e.source_type = 'spec' AND e.source_id = ssp.id
		LEFT JOIN record_document srd ON e.source_type = 'record' AND e.source_id = srd.id
		LEFT JOIN epic sep ON e.source_type = 'epic' AND e.source_id = sep.id
		LEFT JOIN issue ti ON e.target_type = 'issue' AND e.target_id = ti.id
		LEFT JOIN spec_document tsp ON e.target_type = 'spec' AND e.target_id = tsp.id
		LEFT JOIN record_document trd ON e.target_type = 'record' AND e.target_id = trd.id
		LEFT JOIN epic tep ON e.target_type = 'epic' AND e.target_id = tep.id
		WHERE ` + whereClause + `
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT ` + limitArg + ` OFFSET ` + offsetArg

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]pg.EdgeCandidate, 0)
	for rows.Next() {
		var (
			ec         pg.EdgeCandidate
			sourceType string
			targetType *string
			kind, orig string
			status     string
			decidedAt  *time.Time
			createdAt  time.Time
		)
		if err := rows.Scan(
			&ec.ID, &ec.ProjectID, &ec.Project, &sourceType, &ec.SourceID,
			&targetType, &ec.TargetID, &ec.UnresolvedTargetText,
			&kind, &orig, &status, &ec.Confidence, &ec.Evidence,
			&ec.DecidedBy, &decidedAt, &createdAt,
			&ec.SourceKeyOrName, &ec.TargetKeyOrName,
		); err != nil {
			return nil, 0, err
		}
		ec.SourceType = model.GraphNodeType(sourceType)
		if targetType != nil {
			tt := model.GraphNodeType(*targetType)
			ec.TargetType = &tt
		}
		ec.Kind = model.EdgeKind(kind)
		ec.Origin = model.EdgeOrigin(orig)
		ec.Status = model.EdgeStatus(status)
		ec.DecidedAt = toModelTimePtr(decidedAt)
		ec.CreatedAt = model.NewTime(createdAt)
		out = append(out, ec)
	}
	return out, total, rows.Err()
}

// RemoveEdge transitions an existing edge to status='removed'. Removing an
// already-removed edge is idempotent and returns the current row.
func (s *Store) RemoveEdge(ctx context.Context, actorID string, edgeID int64) (model.Edge, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.Edge{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cur, err := getEdgeForUpdateTx(ctx, tx, edgeID)
	if err != nil {
		return model.Edge{}, err
	}
	if cur.Status == model.EdgeStatusRemoved {
		if err := tx.Commit(ctx); err != nil {
			return model.Edge{}, err
		}
		return cur, nil
	}

	var decidedBy *string
	if strings.TrimSpace(actorID) != "" {
		a := strings.TrimSpace(actorID)
		decidedBy = &a
	}
	row := tx.QueryRow(ctx, `
		UPDATE graph_edge e
		SET status = 'removed',
		    decided_by = $2,
		    decided_at = now()
		FROM project p
		WHERE e.id = $1 AND p.id = e.project_id
		RETURNING e.id, e.project_id, p.key, e.source_type, e.source_id,
		          e.target_type, e.target_id, e.unresolved_target_text,
		          e.kind, e.origin, e.status, e.confidence, e.evidence,
		          e.decided_by, e.decided_at, e.created_at`,
		edgeID, decidedBy,
	)
	updated, err := scanEdge(row)
	if err != nil {
		return model.Edge{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Edge{}, err
	}
	return updated, nil
}

// ConfirmEdge transitions a candidate edge to confirmed, returning
// pg.EdgeAlreadyDecidedError if the edge is no longer in candidate state.
func (s *Store) ConfirmEdge(ctx context.Context, actorID string, edgeID int64) (model.Edge, error) {
	return s.decideEdge(ctx, actorID, edgeID, model.EdgeStatusConfirmed)
}

// RejectEdge transitions a candidate edge to rejected, returning
// pg.EdgeAlreadyDecidedError if the edge is no longer in candidate state.
func (s *Store) RejectEdge(ctx context.Context, actorID string, edgeID int64) (model.Edge, error) {
	return s.decideEdge(ctx, actorID, edgeID, model.EdgeStatusRejected)
}

func (s *Store) decideEdge(ctx context.Context, actorID string, edgeID int64, targetStatus model.EdgeStatus) (model.Edge, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.Edge{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Read edge kind without locking first so advisory lock acquisition order
	// (lockEdgeKindTx -> FOR UPDATE OF e) matches UpsertExplicitEdge and
	// UpsertInferredEdge and avoids AB-BA deadlocks.
	var kindRaw string
	if err := tx.QueryRow(ctx, `SELECT kind FROM graph_edge WHERE id = $1`, edgeID).Scan(&kindRaw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.Edge{}, pg.ErrNotFound
		}
		return model.Edge{}, err
	}
	edgeKind := model.EdgeKind(kindRaw)
	if requiresCycleCheck(edgeKind) {
		if err := lockEdgeKindTx(ctx, tx, edgeKind); err != nil {
			return model.Edge{}, err
		}
	}

	cur, err := getEdgeForUpdateTx(ctx, tx, edgeID)
	if err != nil {
		return model.Edge{}, err
	}
	if cur.Status != model.EdgeStatusCandidate {
		return model.Edge{}, &pg.EdgeAlreadyDecidedError{EdgeID: edgeID, Status: cur.Status}
	}
	if targetStatus == model.EdgeStatusConfirmed && requiresCycleCheck(cur.Kind) && cur.TargetType != nil && cur.TargetID != nil {
		cyclic, err := wouldCreateCycleTx(ctx, tx,
			pg.NodeRef{Type: cur.SourceType, ID: cur.SourceID},
			pg.NodeRef{Type: *cur.TargetType, ID: *cur.TargetID},
			cur.Kind,
		)
		if err != nil {
			return model.Edge{}, err
		}
		if cyclic {
			return model.Edge{}, ErrCycle
		}
	}

	var decidedBy *string
	if strings.TrimSpace(actorID) != "" {
		a := strings.TrimSpace(actorID)
		decidedBy = &a
	}
	row := tx.QueryRow(ctx, `
		UPDATE graph_edge e
		SET status = $2,
		    decided_by = $3,
		    decided_at = now()
		FROM project p
		WHERE e.id = $1 AND p.id = e.project_id
		RETURNING e.id, e.project_id, p.key, e.source_type, e.source_id,
		          e.target_type, e.target_id, e.unresolved_target_text,
		          e.kind, e.origin, e.status, e.confidence, e.evidence,
		          e.decided_by, e.decided_at, e.created_at`,
		edgeID, string(targetStatus), decidedBy,
	)
	updated, err := scanEdge(row)
	if err != nil {
		return model.Edge{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return model.Edge{}, err
	}
	return updated, nil
}

func validateEdgeInput(in pg.UpsertEdgeInput) error {
	if _, ok := model.ParseGraphNodeType(string(in.Source.Type)); !ok || in.Source.ID <= 0 {
		return fmt.Errorf("invalid source node (%q, %d)", in.Source.Type, in.Source.ID)
	}
	if _, ok := model.ParseEdgeKind(string(in.Kind)); !ok {
		return fmt.Errorf("invalid edge kind %q", in.Kind)
	}
	hasTarget := in.Target != nil
	hasUnresolved := in.UnresolvedTargetText != nil && strings.TrimSpace(*in.UnresolvedTargetText) != ""
	if hasTarget == hasUnresolved {
		return errors.New("edge must specify either target or unresolved_target_text, not both")
	}
	if hasTarget {
		if _, ok := model.ParseGraphNodeType(string(in.Target.Type)); !ok || in.Target.ID <= 0 {
			return fmt.Errorf("invalid target node (%q, %d)", in.Target.Type, in.Target.ID)
		}
	}
	if in.Confidence != nil && (*in.Confidence < 0 || *in.Confidence > 1) {
		return fmt.Errorf("confidence %v out of [0, 1] range", *in.Confidence)
	}
	return nil
}

func isSelfLoop(in pg.UpsertEdgeInput) bool {
	return in.Target != nil && in.Source.Type == in.Target.Type && in.Source.ID == in.Target.ID
}

func requiresCycleCheck(kind model.EdgeKind) bool {
	return kind == model.EdgeKindDependsOn || kind == model.EdgeKindSupersedes
}

func lockEdgeKindTx(ctx context.Context, tx pgx.Tx, kind model.EdgeKind) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('graph_edge_cycle:' || $1))`, string(kind))
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// wouldCreateCycleTx returns true if adding source -> target of the given kind
// would create a directed cycle among confirmed edges of the same kind.
// Uses UNION for global node deduplication so traversal stays O(V+E) on DAGs
// with many shared paths.
func wouldCreateCycleTx(ctx context.Context, tx pgx.Tx, source, target pg.NodeRef, kind model.EdgeKind) (bool, error) {
	if source.Type == target.Type && source.ID == target.ID {
		return true, nil
	}
	var reachable bool
	err := tx.QueryRow(ctx, `
		WITH RECURSIVE reach(node_type, node_id) AS (
			SELECT $1::text, $2::bigint
			UNION
			SELECT e.target_type, e.target_id
			FROM graph_edge e
			JOIN reach r ON e.source_type = r.node_type AND e.source_id = r.node_id
			WHERE e.kind = $3
			  AND e.status = 'confirmed'
			  AND e.target_type IS NOT NULL
			  AND e.target_id IS NOT NULL
		)
		SELECT EXISTS (
			SELECT 1 FROM reach WHERE node_type = $4 AND node_id = $5
		)`,
		string(target.Type), target.ID, string(kind), string(source.Type), source.ID,
	).Scan(&reachable)
	return reachable, err
}

func findExistingEdgeTx(ctx context.Context, tx pgx.Tx, in pg.UpsertEdgeInput) (model.Edge, bool, error) {
	var row pgx.Row
	if in.Target != nil {
		row = tx.QueryRow(ctx, `
			SELECT e.id, e.project_id, p.key, e.source_type, e.source_id,
			       e.target_type, e.target_id, e.unresolved_target_text,
			       e.kind, e.origin, e.status, e.confidence, e.evidence,
			       e.decided_by, e.decided_at, e.created_at
			FROM graph_edge e
			JOIN project p ON p.id = e.project_id
			WHERE e.source_type = $1
			  AND e.source_id = $2
			  AND e.target_type = $3
			  AND e.target_id = $4
			  AND e.kind = $5
			  AND e.status IN ('confirmed', 'rejected', 'candidate')
			ORDER BY CASE e.status
			             WHEN 'confirmed' THEN 1
			             WHEN 'rejected'  THEN 2
			             WHEN 'candidate' THEN 3
			             ELSE 4
			         END,
			         e.id DESC
			LIMIT 1
			FOR UPDATE OF e`,
			string(in.Source.Type), in.Source.ID, string(in.Target.Type), in.Target.ID, string(in.Kind),
		)
	} else {
		unresolved := strings.TrimSpace(*in.UnresolvedTargetText)
		row = tx.QueryRow(ctx, `
			SELECT e.id, e.project_id, p.key, e.source_type, e.source_id,
			       e.target_type, e.target_id, e.unresolved_target_text,
			       e.kind, e.origin, e.status, e.confidence, e.evidence,
			       e.decided_by, e.decided_at, e.created_at
			FROM graph_edge e
			JOIN project p ON p.id = e.project_id
			WHERE e.source_type = $1
			  AND e.source_id = $2
			  AND e.unresolved_target_text = $3
			  AND e.kind = $4
			  AND e.status IN ('confirmed', 'rejected', 'candidate')
			ORDER BY CASE e.status
			             WHEN 'confirmed' THEN 1
			             WHEN 'rejected'  THEN 2
			             WHEN 'candidate' THEN 3
			             ELSE 4
			         END,
			         e.id DESC
			LIMIT 1
			FOR UPDATE OF e`,
			string(in.Source.Type), in.Source.ID, unresolved, string(in.Kind),
		)
	}
	edge, err := scanEdge(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Edge{}, false, nil
	}
	if err != nil {
		return model.Edge{}, false, err
	}
	return edge, true, nil
}

func upsertExplicitEdgeTx(ctx context.Context, tx pgx.Tx, in pg.UpsertEdgeInput) (model.Edge, error) {
	// Look for an existing live (confirmed/candidate) row first so explicit writes
	// promote any candidate row to explicit+confirmed idempotently.
	existing, found, err := findLiveEdgeTx(ctx, tx, in)
	if err != nil {
		return model.Edge{}, err
	}
	if found {
		row := tx.QueryRow(ctx, `
			UPDATE graph_edge e
			SET origin = 'explicit',
			    status = 'confirmed',
			    confidence = COALESCE($2, e.confidence),
			    evidence = COALESCE($3, e.evidence)
			FROM project p
			WHERE e.id = $1 AND p.id = e.project_id
			RETURNING e.id, e.project_id, p.key, e.source_type, e.source_id,
			          e.target_type, e.target_id, e.unresolved_target_text,
			          e.kind, e.origin, e.status, e.confidence, e.evidence,
			          e.decided_by, e.decided_at, e.created_at`,
			existing.ID, in.Confidence, in.Evidence,
		)
		return scanEdge(row)
	}
	return insertEdgeTx(ctx, tx, in, model.EdgeOriginExplicit, model.EdgeStatusConfirmed)
}

func findLiveEdgeTx(ctx context.Context, tx pgx.Tx, in pg.UpsertEdgeInput) (model.Edge, bool, error) {
	var row pgx.Row
	if in.Target != nil {
		row = tx.QueryRow(ctx, `
			SELECT e.id, e.project_id, p.key, e.source_type, e.source_id,
			       e.target_type, e.target_id, e.unresolved_target_text,
			       e.kind, e.origin, e.status, e.confidence, e.evidence,
			       e.decided_by, e.decided_at, e.created_at
			FROM graph_edge e
			JOIN project p ON p.id = e.project_id
			WHERE e.source_type = $1
			  AND e.source_id = $2
			  AND e.target_type = $3
			  AND e.target_id = $4
			  AND e.kind = $5
			  AND e.status IN ('confirmed', 'candidate')
			LIMIT 1
			FOR UPDATE OF e`,
			string(in.Source.Type), in.Source.ID, string(in.Target.Type), in.Target.ID, string(in.Kind),
		)
	} else {
		unresolved := strings.TrimSpace(*in.UnresolvedTargetText)
		row = tx.QueryRow(ctx, `
			SELECT e.id, e.project_id, p.key, e.source_type, e.source_id,
			       e.target_type, e.target_id, e.unresolved_target_text,
			       e.kind, e.origin, e.status, e.confidence, e.evidence,
			       e.decided_by, e.decided_at, e.created_at
			FROM graph_edge e
			JOIN project p ON p.id = e.project_id
			WHERE e.source_type = $1
			  AND e.source_id = $2
			  AND e.unresolved_target_text = $3
			  AND e.kind = $4
			  AND e.status IN ('confirmed', 'candidate')
			LIMIT 1
			FOR UPDATE OF e`,
			string(in.Source.Type), in.Source.ID, unresolved, string(in.Kind),
		)
	}
	edge, err := scanEdge(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Edge{}, false, nil
	}
	if err != nil {
		return model.Edge{}, false, err
	}
	return edge, true, nil
}

func updateCandidateMetadataTx(ctx context.Context, tx pgx.Tx, edgeID int64, confidence *float64, evidence *string) (model.Edge, error) {
	row := tx.QueryRow(ctx, `
		UPDATE graph_edge e
		SET confidence = COALESCE($2, e.confidence),
		    evidence = COALESCE($3, e.evidence)
		FROM project p
		WHERE e.id = $1 AND p.id = e.project_id
		RETURNING e.id, e.project_id, p.key, e.source_type, e.source_id,
		          e.target_type, e.target_id, e.unresolved_target_text,
		          e.kind, e.origin, e.status, e.confidence, e.evidence,
		          e.decided_by, e.decided_at, e.created_at`,
		edgeID, confidence, evidence,
	)
	return scanEdge(row)
}

func insertEdgeTx(ctx context.Context, tx pgx.Tx, in pg.UpsertEdgeInput, origin model.EdgeOrigin, status model.EdgeStatus) (model.Edge, error) {
	var targetType *string
	var targetID *int64
	var unresolved *string
	if in.Target != nil {
		tt := string(in.Target.Type)
		tid := in.Target.ID
		targetType = &tt
		targetID = &tid
	} else if in.UnresolvedTargetText != nil {
		u := strings.TrimSpace(*in.UnresolvedTargetText)
		unresolved = &u
	}

	row := tx.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO graph_edge (
				project_id, source_type, source_id, target_type, target_id,
				unresolved_target_text, kind, origin, status, confidence, evidence
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING id, project_id, source_type, source_id, target_type, target_id,
			          unresolved_target_text, kind, origin, status, confidence, evidence,
			          decided_by, decided_at, created_at
		)
		SELECT ins.id, ins.project_id, p.key, ins.source_type, ins.source_id,
		       ins.target_type, ins.target_id, ins.unresolved_target_text,
		       ins.kind, ins.origin, ins.status, ins.confidence, ins.evidence,
		       ins.decided_by, ins.decided_at, ins.created_at
		FROM ins
		JOIN project p ON p.id = ins.project_id`,
		in.ProjectID, string(in.Source.Type), in.Source.ID,
		targetType, targetID, unresolved,
		string(in.Kind), string(origin), string(status),
		in.Confidence, in.Evidence,
	)
	return scanEdge(row)
}

func getEdgeForUpdateTx(ctx context.Context, tx pgx.Tx, edgeID int64) (model.Edge, error) {
	row := tx.QueryRow(ctx, `
		SELECT e.id, e.project_id, p.key, e.source_type, e.source_id,
		       e.target_type, e.target_id, e.unresolved_target_text,
		       e.kind, e.origin, e.status, e.confidence, e.evidence,
		       e.decided_by, e.decided_at, e.created_at
		FROM graph_edge e
		JOIN project p ON p.id = e.project_id
		WHERE e.id = $1
		FOR UPDATE OF e`,
		edgeID,
	)
	edge, err := scanEdge(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Edge{}, pg.ErrNotFound
	}
	return edge, err
}

type edgeScanner interface {
	Scan(dest ...any) error
}

func scanEdge(row edgeScanner) (model.Edge, error) {
	var (
		e          model.Edge
		sourceType string
		targetType *string
		kind       string
		origin     string
		status     string
		decidedAt  *time.Time
		createdAt  time.Time
	)
	if err := row.Scan(
		&e.ID, &e.ProjectID, &e.Project, &sourceType, &e.SourceID,
		&targetType, &e.TargetID, &e.UnresolvedTargetText,
		&kind, &origin, &status, &e.Confidence, &e.Evidence,
		&e.DecidedBy, &decidedAt, &createdAt,
	); err != nil {
		return model.Edge{}, err
	}
	e.SourceType = model.GraphNodeType(sourceType)
	if targetType != nil {
		tt := model.GraphNodeType(*targetType)
		e.TargetType = &tt
	}
	e.Kind = model.EdgeKind(kind)
	e.Origin = model.EdgeOrigin(origin)
	e.Status = model.EdgeStatus(status)
	e.DecidedAt = toModelTimePtr(decidedAt)
	e.CreatedAt = model.NewTime(createdAt)
	return e, nil
}

func toModelTimePtr(t *time.Time) *model.Time {
	if t == nil {
		return nil
	}
	mt := model.NewTime(*t)
	return &mt
}
