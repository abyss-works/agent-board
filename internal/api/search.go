package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

// Semantic finds the n sources nearest in meaning to a query text, nearest
// first, among the allowed sources.
type Semantic interface {
	Search(ctx context.Context, query string, n int, allowed map[store.SourceRef]struct{}) ([]store.SourceText, error)
}

const (
	// fusionPool is how many hits of each list take part in the fusion.
	fusionPool = 50
	// semanticFloor is the lowest cosine similarity at which a hit found by
	// meaning is kept.
	semanticFloor = 0
	// rrfK is the constant of the reciprocal rank fusion.
	rrfK = 60
)

func (h *handler) registerSearch(mux *http.ServeMux) {
	route(mux, "/v1/search", methods{"GET": h.search})
}

// search is the unified search over issues, comments and record documents.
func (h *handler) search(w http.ResponseWriter, r *http.Request) {
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
	// A question longer than the keyword search takes is still a query for
	// the search by meaning; only the keyword part is left out.
	match, matchErr := store.BuildMatch(f.Query)
	switch {
	case strings.TrimSpace(f.Query) == "" || (matchErr == nil && match == ""):
		q.add("q", "검색어가 필요합니다.")
	case matchErr != nil && h.opts.Semantic == nil:
		q.add("q", "검색어는 최대 10항입니다.")
	}
	if aerr := q.finish(); aerr != nil {
		writeError(w, aerr)
		return
	}
	state := semanticDisabled
	if h.opts.Semantic != nil {
		state = semanticFailed
		items, total, ok, err := h.hybridSearch(r.Context(), f, matchErr == nil)
		if err != nil {
			writeError(w, h.storeError(r, err))
			return
		}
		if ok {
			writeJSON(w, http.StatusOK, searchEnvelope(items, total, f, semanticOK))
			return
		}
		if matchErr != nil {
			q.add("q", "의미 검색을 쓸 수 없어 키워드로 찾습니다. 키워드 검색어는 최대 10항입니다.")
			writeError(w, q.finish())
			return
		}
	}
	items, total, err := h.store.Search(r.Context(), f)
	if err != nil {
		writeError(w, h.storeError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, searchEnvelope(items, total, f, state))
}

// The states of the search by meaning reported in the envelope: it took part,
// the server has none, or its query could not be embedded and the keywords
// answered alone.
const (
	semanticOK       = "ok"
	semanticDisabled = "disabled"
	semanticFailed   = "failed"
)

func searchEnvelope(items []model.SearchHit, total int, f store.SearchFilter, semantic string) map[string]any {
	env := envelope(items, total, f.Limit, f.Offset)
	env["semantic"] = semantic
	return env
}

// hybridSearch fuses the keyword ranking and the ranking by meaning. ok is
// false when the query could not be embedded; the caller then answers with the
// keyword search alone. withKeywords is false for a query the keyword search
// cannot take, which then ranks by meaning alone.
func (h *handler) hybridSearch(ctx context.Context, f store.SearchFilter, withKeywords bool) (items []model.SearchHit, total int, ok bool, err error) {
	allowed, err := h.store.AllowedSources(ctx, f)
	if err != nil {
		return nil, 0, false, err
	}
	refs, err := h.opts.Semantic.Search(ctx, f.Query, fusionPool, allowed)
	if err != nil {
		h.opts.Logger.Warn("semantic search failed, answering with keywords only", "err", err.Error())
		return nil, 0, false, nil
	}
	var keyword []model.SearchHit
	if withKeywords {
		kwFilter := f
		kwFilter.Limit, kwFilter.Offset = fusionPool, 0
		if keyword, _, err = h.store.Search(ctx, kwFilter); err != nil {
			return nil, 0, false, err
		}
	}
	semantic, err := h.store.SemanticHits(ctx, aboveFloor(refs, semanticFloor), f)
	if err != nil {
		return nil, 0, false, err
	}
	fused := fuse(keyword, semantic)
	total = len(fused)
	fused = fused[min(f.Offset, total):]
	fused = fused[:min(f.Limit, len(fused))]
	return fused, total, true, nil
}

// aboveFloor keeps the refs whose score is at least floor, in order.
func aboveFloor(refs []store.SourceText, floor float64) []store.SourceText {
	var out []store.SourceText
	for _, r := range refs {
		if r.Score >= floor {
			out = append(out, r)
		}
	}
	return out
}

// hitID identifies the source of a hit: an issue by key, a comment by id and a
// document by project and name.
func hitID(h model.SearchHit) string {
	switch h.Type {
	case model.SearchComment:
		return fmt.Sprintf("comment:%d", *h.CommentID)
	case model.SearchDocument:
		return "document:" + h.Project + "/" + h.Key
	}
	return "issue:" + h.Key
}

// fuse merges two ranked lists with reciprocal rank fusion: the score of a hit
// is the sum of 1/(rrfK+rank) over the lists that hold it. A hit in both lists
// keeps the keyword hit, which carries the marked snippet, and takes the
// semantic score of the other. Equal scores order by type and key.
func fuse(keyword, semantic []model.SearchHit) []model.SearchHit {
	score := map[string]float64{}
	byID := map[string]model.SearchHit{}
	for _, list := range [][]model.SearchHit{keyword, semantic} {
		for i, hit := range list {
			id := hitID(hit)
			score[id] += 1 / float64(rrfK+i+1)
			if _, seen := byID[id]; !seen {
				byID[id] = hit
			}
		}
	}
	for _, hit := range semantic {
		if id := hitID(hit); hit.SemanticScore != nil {
			b := byID[id]
			b.SemanticScore = hit.SemanticScore
			byID[id] = b
		}
	}
	out := make([]model.SearchHit, 0, len(byID))
	for _, hit := range byID {
		out = append(out, hit)
	}
	slices.SortFunc(out, func(a, b model.SearchHit) int {
		if sa, sb := score[hitID(a)], score[hitID(b)]; sa != sb {
			if sa > sb {
				return -1
			}
			return 1
		}
		if c := strings.Compare(string(a.Type), string(b.Type)); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
	return out
}

// parseSearchStatuses reads the status filter of the unified search: the
// statuses of the issues and, through them, of the comments. open and closed
// stand for several statuses and all for every status but deleted, which the
// search never finds and refuses to be asked for. Without a filter, and for all,
// it returns none, which selects every status.
func parseSearchStatuses(q *query) []model.IssueStatus {
	param := q.one("status")
	if param == "" {
		return nil
	}
	for _, v := range strings.Split(param, ",") {
		if v == string(model.StatusDeleted) {
			q.add("status", "deleted는 받지 않습니다. 지운 이슈와 그 댓글은 찾지 않습니다.")
			return nil
		}
	}
	statuses, err := model.ExpandIssueStatuses(param)
	if err != nil {
		q.add("status", "상태 또는 open, closed, all이어야 합니다.")
		return nil
	}
	var out []model.IssueStatus
	for _, s := range statuses {
		if s != model.StatusDeleted {
			out = append(out, s)
		}
	}
	return out
}
