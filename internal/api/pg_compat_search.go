package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
	pgstore "github.com/wsk0715/agent-board/internal/store/pg"
)

// pgCompatSearch is the unified search over issues, comments and record documents
// in PostgreSQL mode. The keyword search runs on the tables; when the server ranks by
// meaning, the hits of the context engine are fused with it.
func (h *handler) pgCompatSearch(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	f := store.SearchFilter{
		Query:        q.one("q"),
		Projects:     q.csv("project"),
		Types:        parseEnumList(q, "type", model.ParseSearchType),
		CommentKinds: parseEnumList(q, "comment_kind", model.ParseCommentKind),
		Statuses:     parseSearchStatuses(q),
		States:       parseStates(q),
	}
	for _, p := range f.Projects {
		if !model.ValidProjectKey(p) {
			q.add("project", "프로젝트 키 형식이 아닙니다.")
		}
	}
	f.Limit, f.Offset = q.limit(20, 200), q.offset()
	match, matchErr := store.BuildMatch(f.Query)
	switch {
	case strings.TrimSpace(f.Query) == "" || (matchErr == nil && match == ""):
		q.add("q", "검색어가 필요합니다.")
	case matchErr != nil && !h.opts.PGSemantic:
		q.add("q", "검색어는 최대 10항입니다.")
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	state := semanticDisabled
	if h.opts.PGSemantic {
		state = semanticFailed
		items, total, ok, err := h.pgHybridSearch(r, f, matchErr == nil)
		if err != nil {
			writeError(w, h.pgStoreError(r, err))
			return
		}
		if ok {
			writeJSON(w, http.StatusOK, searchEnvelope(items, total, f, semanticOK))
			return
		}
		if matchErr != nil {
			q.add("q", "의미 검색이 실패했고 키워드 검색은 최대 10항까지 받습니다. 핵심어 10개 이내로 다시 찾거나, 잠시 뒤 같은 질문으로 다시 시도하세요.")
			writeError(w, q.finish())
			return
		}
	}
	items, total, err := h.pgStore.CompatSearch(r.Context(), f)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, searchEnvelope(items, total, f, state))
}

func (h *handler) pgHybridSearch(r *http.Request, f store.SearchFilter, withKeywords bool) (items []model.SearchHit, total int, ok bool, err error) {
	ctx := r.Context()
	semantic, ok := h.pgSemanticHits(ctx, f)
	if !ok {
		return nil, 0, false, nil
	}
	var keyword []model.SearchHit
	if withKeywords {
		kw := f
		kw.Limit, kw.Offset = fusionPool, 0
		if keyword, _, err = h.pgStore.CompatSearch(ctx, kw); err != nil {
			return nil, 0, false, err
		}
	}
	fused := fuse(keyword, semantic)
	total = len(fused)
	fused = fused[min(f.Offset, total):]
	fused = fused[:min(f.Limit, len(fused))]
	return fused, total, true, nil
}

// pgSemanticHits asks the context engine for the sources nearest in meaning and
// turns them into search hits that obey the filter. ok is false when the engine
// could not answer.
func (h *handler) pgSemanticHits(ctx context.Context, f store.SearchFilter) ([]model.SearchHit, bool) {
	req := pgstore.ContextQueryRequest{Query: f.Query, MaxResults: pgstoreMaxContext, IncludeGlobal: boolPtr(false), IncludeSuperseded: true, IncludeArchived: true}
	if len(f.Projects) == 1 {
		req.Project = f.Projects[0]
	}
	res, err := h.contextEngine.Query(ctx, req)
	if err != nil {
		var verr *model.ValidationError
		if !errors.As(err, &verr) {
			h.opts.Logger.Warn("semantic search failed, answering with keywords only", "err", err.Error())
		}
		return nil, false
	}
	want := func(t model.SearchType) bool { return len(f.Types) == 0 || slices.Contains(f.Types, t) }
	var hits []model.SearchHit
	pool := h.pgStore.Pool()
	for _, it := range res.Items {
		if len(f.Projects) > 1 && !slices.Contains(f.Projects, it.Project) {
			continue
		}
		if it.Score < semanticFloor {
			continue
		}
		score := it.Score
		hit := model.SearchHit{Project: it.Project, Key: it.KeyOrName, Supersedes: []string{}, SupersededBy: []string{}, SemanticScore: &score,
			Snippet: strings.Join(strings.Fields(it.Summary), " ")}
		title, kind := it.Title, it.Kind
		hit.Title, hit.Kind = &title, &kind
		switch it.NodeType {
		case model.GraphNodeIssue:
			if !want(model.SearchIssue) {
				continue
			}
			var status string
			var at string
			if err := pool.QueryRow(ctx, `SELECT status, to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"') FROM issue WHERE id = $1`, it.NodeID).Scan(&status, &at); err != nil {
				continue
			}
			if status == string(model.StatusDeleted) || (len(f.Statuses) > 0 && !slices.Contains(f.Statuses, model.IssueStatus(status))) {
				continue
			}
			hit.Type, hit.Status, hit.At = model.SearchIssue, &status, at
		case model.GraphNodeType(model.IngestSourceActivity):
			if !want(model.SearchComment) {
				continue
			}
			var status, at string
			var ck *string
			var summary *string
			if err := pool.QueryRow(ctx, `SELECT i.status, a.comment_kind, a.summary, to_char(a.at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
				FROM activity a JOIN issue i ON i.id = a.issue_id WHERE a.id = $1`, it.NodeID).Scan(&status, &ck, &summary, &at); err != nil {
				continue
			}
			if status == string(model.StatusDeleted) || (len(f.Statuses) > 0 && !slices.Contains(f.Statuses, model.IssueStatus(status))) {
				continue
			}
			if len(f.CommentKinds) > 0 && (ck == nil || !slices.Contains(f.CommentKinds, model.CommentKind(*ck))) {
				continue
			}
			id := it.NodeID
			hit.Type, hit.CommentID, hit.Status, hit.Kind, hit.Title, hit.At = model.SearchComment, &id, &status, ck, summary, at
		case model.GraphNodeRecord:
			if !want(model.SearchDocument) {
				continue
			}
			var st string
			var recorded *time.Time
			var created time.Time
			if err := pool.QueryRow(ctx, `SELECT (CASE WHEN d.archived THEN 'archived'
					WHEN EXISTS (SELECT 1 FROM graph_edge sr WHERE sr.kind = 'supersedes' AND sr.status = 'confirmed' AND sr.target_type = 'record' AND sr.target_id = d.id) THEN 'superseded'
					ELSE 'current' END), d.recorded_at, d.created_at
				FROM record_document d WHERE d.id = $1`, it.NodeID).Scan(&st, &recorded, &created); err != nil {
				continue
			}
			at := model.NewTime(created).String()
			if recorded != nil {
				at = *pgstore.FormatDocDate(recorded)
			}
			if len(f.States) > 0 && !slices.Contains(f.States, model.DocumentState(st)) {
				continue
			}
			hit.Type, hit.State, hit.At = model.SearchDocument, &st, at
		default:
			continue
		}
		hits = append(hits, hit)
	}
	return hits, true
}

const pgstoreMaxContext = 10

func boolPtr(b bool) *bool { return &b }

func (h *handler) pgCompatGraph(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	node := q.one("node")
	depth := q.intRange("depth", 1, 1, 2)
	fold := true
	switch q.one("state") {
	case "", "current":
	case "all":
		fold = false
	default:
		q.add("state", "current 또는 all이어야 합니다.")
	}
	if _, _, _, ok := model.ParseNodeID(node); !ok {
		q.add("node", "issue:<KEY> 또는 doc:<PROJECT>/<문서 이름> 형식이어야 합니다.")
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	g, err := h.pgStore.CompatGraph(r.Context(), node, depth, fold, h.opts.PGSemantic)
	if err != nil {
		writeError(w, h.pgStoreError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, g)
}
