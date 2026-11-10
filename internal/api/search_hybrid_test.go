package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/embed"
	"github.com/wsk0715/agent-board/internal/model"
	"github.com/wsk0715/agent-board/internal/store"
)

// conceptEmbedder gives a text one dimension per concept, counting the words of
// the concept, so that texts about the same thing are near without sharing a
// word. It fails while fail is set and blocks until its context ends while
// hang is set. calls counts every call.
type conceptEmbedder struct {
	fail    bool
	hang    bool
	calls   int
	queries []string
}

var concepts = [][]string{{"잠금", "락", "lock"}, {"큐", "queue"}, {"배포", "release"}}

func (c *conceptEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	c.calls++
	if c.hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if c.fail {
		return nil, errors.New("embeddings API returned 503")
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		t = strings.ToLower(t)
		v := make([]float32, len(concepts)+1)
		for d, words := range concepts {
			for _, w := range words {
				v[d] += float32(strings.Count(t, w))
			}
		}
		v[len(concepts)] = 0.001
		out[i] = v
	}
	if len(texts) == 1 {
		c.queries = append(c.queries, texts[0])
	}
	return out, nil
}

// hybridEnv is an environment with the search by meaning on, holding two
// projects with records about locks and one about queues. Everything is
// indexed already.
func hybridEnv(t *testing.T) (*env, *conceptEmbedder) {
	t.Helper()
	emb := &conceptEmbedder{}
	index := &embed.Index{}
	e := newEnvWith(t, testActors, func(o *Options) {
		o.Semantic = &embed.Searcher{Embedder: emb, Prefix: "Query: ", Index: index, Timeout: 300 * time.Millisecond}
	})
	e.createProject(t, "AGB", "agent-board")
	e.createProject(t, "OTH", "other")
	e.mustPutDocument(t, "AGB", apiDocLock, "history", apiDocText("incident", "db lock contention between writers", "title: writers"))
	e.mustPutDocument(t, "AGB", apiDocPriority, "history", apiDocText("decision", "queue order is computed by the server", "title: queue"))
	e.createIssue(t, `{"project":"AGB","title":"잠금 조사"}`)
	e.createIssue(t, `{"project":"OTH","title":"lock in another project"}`)
	e.createIssue(t, `{"project":"AGB","title":"lock of a deleted issue","status":"deleted"}`)
	r := &embed.Runner{Store: e.st, Embedder: emb, Model: "m", Interval: 1, Index: index, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	emb.queries = nil
	return e, emb
}

func (e *env) searchHits(t *testing.T, target string) searchList {
	t.Helper()
	rec := e.do(t, "GET", target, "", nil)
	var l searchList
	decodeJSON(t, rec, &l)
	if rec.Code != 200 {
		t.Fatalf("%s: %d %s", target, rec.Code, rec.Body.String())
	}
	return l
}

func hitKeys(l searchList) []string {
	var keys []string
	for _, h := range l.Items {
		keys = append(keys, string(h.Type)+":"+h.Key)
	}
	return keys
}

// TestHybridSearchFindsWhatKeywordsMiss searches with a word that no record
// contains. The records about locks come up by meaning, each with the text of
// its best chunk as the snippet; the deleted issue and the document about
// queues, which is below the similarity floor, stay out.
func TestHybridSearchFindsWhatKeywordsMiss(t *testing.T) {
	e, emb := hybridEnv(t)
	l := e.searchHits(t, "/v1/search?q=락")
	if l.Total != 3 || len(l.Items) != 3 {
		t.Fatalf("hits = %v", hitKeys(l))
	}
	seen := map[string]model.SearchHit{}
	for _, h := range l.Items {
		seen[string(h.Type)+":"+h.Key] = h
	}
	doc, ok := seen["document:"+apiDocLock]
	if !ok || doc.Snippet == "" || strings.ContainsAny(doc.Snippet, "[]") || doc.Title == nil || *doc.Title != "writers" || doc.Kind == nil || *doc.Kind != "incident" {
		t.Errorf("semantic only document = %+v", doc)
	}
	if _, ok := seen["document:"+apiDocPriority]; ok {
		t.Errorf("the document about queues is below the floor: %v", hitKeys(l))
	}
	if len(emb.queries) != 1 || emb.queries[0] != "Query: 락" {
		t.Errorf("embedded queries = %q", emb.queries)
	}
}

// TestHybridSearchRanksWhatBothListsFindFirst keeps the keyword snippet of a
// record that the keyword search also finds.
func TestHybridSearchRanksWhatBothListsFindFirst(t *testing.T) {
	e, _ := hybridEnv(t)
	l := e.searchHits(t, "/v1/search?q=잠금")
	if l.Total != 3 || l.Items[0].Type != model.SearchIssue || l.Items[0].Project != "AGB" || !strings.Contains(l.Items[0].Snippet, "[잠금]") {
		t.Errorf("hits = %v, first %+v", hitKeys(l), l.Items[0])
	}
}

func TestHybridSearchFallsBackToKeywordsWhenTheQueryCannotBeEmbedded(t *testing.T) {
	e, emb := hybridEnv(t)
	emb.fail = true
	l := e.searchHits(t, "/v1/search?q=잠금")
	if l.Total != 1 || len(l.Items) != 1 || !strings.Contains(l.Items[0].Snippet, "[잠금]") {
		t.Errorf("fallback hits = %v", hitKeys(l))
	}
	if log := e.log.String(); !strings.Contains(log, `"level":"WARN"`) || !strings.Contains(log, "503") {
		t.Errorf("log lacks the warning: %s", log)
	}
}

func TestHybridSearchAppliesTheFiltersToSemanticHits(t *testing.T) {
	e, _ := hybridEnv(t)
	if l := e.searchHits(t, "/v1/search?q=락&project=AGB"); l.Total != 2 {
		t.Errorf("project AGB = %v", hitKeys(l))
	}
	if l := e.searchHits(t, "/v1/search?q=락&type=document"); l.Total != 1 || l.Items[0].Type != model.SearchDocument {
		t.Errorf("type document = %v", hitKeys(l))
	}
	if l := e.searchHits(t, "/v1/search?q=락&project=OTH&type=document"); l.Total != 0 || l.Items == nil {
		t.Errorf("OTH documents = %+v", l)
	}
	if l := e.searchHits(t, "/v1/search?q=락&type=comment"); l.Total != 0 {
		t.Errorf("comments = %v", hitKeys(l))
	}
}

// The status and state filters, and the default of the state, apply to the hits
// found by meaning like to the others.
func TestHybridSearchAppliesStatusAndState(t *testing.T) {
	e, _ := hybridEnv(t)
	if rec := e.do(t, "POST", documentPath("AGB", apiDocLock)+"/archive", "", nil); rec.Code != 200 {
		t.Fatalf("archive: %d %s", rec.Code, rec.Body.String())
	}
	if l := e.searchHits(t, "/v1/search?q=락"); l.Total != 2 {
		t.Errorf("default hides the archived document: %v", hitKeys(l))
	}
	if l := e.searchHits(t, "/v1/search?q=락&type=document&state=archived"); l.Total != 1 || l.Items[0].Key != apiDocLock || *l.Items[0].State != "archived" {
		t.Errorf("state archived = %v", hitKeys(l))
	}
	if l := e.searchHits(t, "/v1/search?q=락&state=all"); l.Total != 3 {
		t.Errorf("state all = %v", hitKeys(l))
	}
	if l := e.searchHits(t, "/v1/search?q=락&status=done&state=all"); l.Total != 1 || l.Items[0].Type != model.SearchDocument || l.Items[0].Key != apiDocLock {
		t.Errorf("status done keeps the documents only: %v", hitKeys(l))
	}
	if l := e.searchHits(t, "/v1/search?q=락&status=open&type=issue"); l.Total != 2 || l.Items[0].Status == nil || *l.Items[0].Status != "backlog" {
		t.Errorf("status open = %v", hitKeys(l))
	}
}

// TestHybridSearchPagesAfterFusion applies limit and offset to the fused list
// and counts the fused items.
func TestHybridSearchPagesAfterFusion(t *testing.T) {
	e, _ := hybridEnv(t)
	all := e.searchHits(t, "/v1/search?q=락")
	var got []string
	for off := 0; off < 3; off++ {
		l := e.searchHits(t, "/v1/search?q=락&limit=1&offset="+string(rune('0'+off)))
		if l.Total != 3 || len(l.Items) != 1 {
			t.Fatalf("page %d = %+v", off, l)
		}
		got = append(got, hitKeys(l)...)
	}
	if strings.Join(got, ",") != strings.Join(hitKeys(all), ",") {
		t.Errorf("pages %v differ from the list %v", got, hitKeys(all))
	}
}

func TestFuseRanksByReciprocalRank(t *testing.T) {
	hit := func(key string) model.SearchHit {
		return model.SearchHit{Type: model.SearchIssue, Project: "AGB", Key: key}
	}
	kw := []model.SearchHit{hit("AGB-1"), hit("AGB-2")}
	kw[0].Snippet = "[kw]"
	sem := []model.SearchHit{hit("AGB-3"), hit("AGB-2"), hit("AGB-1")}
	sem[2].Snippet = "semantic"
	got := fuse(kw, sem)
	// AGB-1: 1/61 + 1/63, AGB-2: 1/62 + 1/62, AGB-3: 1/61.
	var keys []string
	for _, h := range got {
		keys = append(keys, h.Key)
	}
	if strings.Join(keys, ",") != "AGB-1,AGB-2,AGB-3" || got[0].Snippet != "[kw]" {
		t.Errorf("fused = %+v", got)
	}
	// Equal scores order by type and key.
	got = fuse([]model.SearchHit{hit("AGB-9")}, []model.SearchHit{hit("AGB-8")})
	if len(got) != 2 || got[0].Key != "AGB-8" {
		t.Errorf("tie = %+v", got)
	}
}

// A question longer than the keyword search takes is ranked by meaning alone.
// Without the search by meaning it is refused as before.
func TestHybridSearchTakesLongQuestions(t *testing.T) {
	const long = "/v1/search?q=a+b+c+d+e+f+g+h+i+j+잠금"
	e, emb := hybridEnv(t)
	if rec := e.do(t, "GET", long, "", nil); rec.Code != 200 {
		t.Fatalf("long question: %d %s", rec.Code, rec.Body.String())
	}
	emb.fail = true
	if rec := e.do(t, "GET", long, "", nil); rec.Code != 400 {
		t.Errorf("long question without meaning: %d %s", rec.Code, rec.Body.String())
	}
	if rec := newEnv(t).do(t, "GET", long, "", nil); rec.Code != 400 {
		t.Errorf("long question on a keyword server: %d %s", rec.Code, rec.Body.String())
	}
}

// crowdedEnv holds more issues than fusionPool that are nearer to the query
// "lock" than the one issue the filters ask for, which is far from it. The
// nearer issues are in project ABC and closed.
func crowdedEnv(t *testing.T) *env {
	t.Helper()
	emb := &conceptEmbedder{}
	index := &embed.Index{}
	e := newEnvWith(t, testActors, func(o *Options) {
		o.Semantic = &embed.Searcher{Embedder: emb, Prefix: "Query: ", Index: index}
	})
	e.createProject(t, "ABC", "crowd")
	e.createProject(t, "XYZ", "target")
	for i := 0; i < fusionPool+5; i++ {
		e.createIssue(t, fmt.Sprintf(`{"project":"ABC","title":"lock %d","status":"done"}`, i))
	}
	e.createIssue(t, `{"project":"XYZ","title":"잠금 큐","status":"backlog"}`)
	r := &embed.Runner{Store: e.st, Embedder: emb, Model: "m", Interval: 1, Index: index, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e
}

// The filters choose the sources before the nearest ones are taken: nearer
// sources that a filter excludes do not fill the pool.
func TestHybridSearchFiltersBeforeRanking(t *testing.T) {
	e := crowdedEnv(t)
	for _, target := range []string{
		"/v1/search?q=lock&project=XYZ&type=issue",
		"/v1/search?q=lock&status=open&type=issue",
	} {
		l := e.searchHits(t, target)
		if l.Total != 1 || l.Items[0].Project != "XYZ" {
			t.Errorf("%s = %v", target, hitKeys(l))
		}
	}
}

// A hit found by meaning carries its cosine similarity to the query; a hit that
// only the keyword search found has none.
func TestHybridSearchReportsTheSemanticScore(t *testing.T) {
	e, _ := hybridEnv(t)
	l := e.searchHits(t, "/v1/search?q=락&type=issue&project=AGB")
	if len(l.Items) != 1 || l.Items[0].SemanticScore == nil || *l.Items[0].SemanticScore < 0.99 || *l.Items[0].SemanticScore > 1.0001 {
		t.Fatalf("score of a semantic hit = %+v", l.Items)
	}
	rec := e.do(t, "GET", "/v1/search?q=락&type=document&project=AGB", "", nil)
	if !strings.Contains(rec.Body.String(), `"semantic_score":`) {
		t.Errorf("body lacks semantic_score: %s", rec.Body.String())
	}
	e.createIssue(t, `{"project":"AGB","title":"새 잠금 이슈"}`)
	l = e.searchHits(t, "/v1/search?q=새+잠금&type=issue&project=AGB")
	found := false
	for _, h := range l.Items {
		if h.Title != nil && *h.Title == "새 잠금 이슈" {
			found = true
			if h.SemanticScore != nil {
				t.Errorf("a keyword only hit has score %v", *h.SemanticScore)
			}
		}
	}
	if !found {
		t.Errorf("the issue created after indexing is missing: %v", hitKeys(l))
	}
}

func TestAboveFloorKeepsScoresAtTheFloor(t *testing.T) {
	refs := []store.SourceText{{ID: 1, Score: 0.9}, {ID: 2, Score: 0.7}, {ID: 3, Score: 0.6999}, {ID: 4, Score: 0.2}}
	var ids []int64
	for _, r := range aboveFloor(refs, 0.7) {
		ids = append(ids, r.ID)
	}
	if !slices.Equal(ids, []int64{1, 2}) {
		t.Errorf("kept = %v", ids)
	}
	if got := aboveFloor(refs, 0); len(got) != 4 {
		t.Errorf("floor 0 kept %d of 4", len(got))
	}
	if got := aboveFloor(refs, 0.95); len(got) != 0 {
		t.Errorf("floor above all kept %d", len(got))
	}
}

// The envelope says whether the search by meaning took part: ok when it did,
// failed when the query could not be embedded and the keywords answered alone,
// disabled on a server without it.
func TestSearchReportsTheStateOfTheSearchByMeaning(t *testing.T) {
	e, emb := hybridEnv(t)
	if l := e.searchHits(t, "/v1/search?q=잠금"); l.Semantic != "ok" {
		t.Errorf("semantic = %q, want ok", l.Semantic)
	}
	if l := e.searchHits(t, "/v1/search?q=lock&type=comment"); l.Semantic != "ok" || l.Total != 0 {
		t.Errorf("an empty answer: semantic = %q, total %d", l.Semantic, l.Total)
	}
	emb.fail = true
	if l := e.searchHits(t, "/v1/search?q=잠금"); l.Semantic != "failed" {
		t.Errorf("semantic = %q, want failed", l.Semantic)
	}
	plain := newEnv(t)
	plain.createProject(t, "XYZ", "plain")
	plain.createIssue(t, `{"project":"XYZ","title":"잠금 조사"}`)
	if l := plain.searchHits(t, "/v1/search?q=잠금"); l.Semantic != "disabled" || l.Total != 1 {
		t.Errorf("semantic = %q, total %d, want disabled", l.Semantic, l.Total)
	}
}

// A hit that both lists found keeps its keyword snippet and gets the semantic
// score.
func TestHybridSearchScoresWhatBothListsFound(t *testing.T) {
	e, _ := hybridEnv(t)
	l := e.searchHits(t, "/v1/search?q=잠금&type=issue&project=AGB")
	if len(l.Items) != 1 || !strings.Contains(l.Items[0].Snippet, "[잠금]") || l.Items[0].SemanticScore == nil {
		t.Errorf("hit found by both lists = %+v", l.Items)
	}
}

// A query that is not embedded within the time limit is tried once; the
// keywords then answer alone and the envelope says failed.
func TestSearchAnswersWithKeywordsWhenTheEmbeddingIsTooSlow(t *testing.T) {
	e, emb := hybridEnv(t)
	emb.hang = true
	emb.calls = 0
	l := e.searchHits(t, "/v1/search?q=잠금")
	if l.Semantic != "failed" || l.Total != 1 {
		t.Errorf("semantic = %q, total %d, want failed with the one keyword hit", l.Semantic, l.Total)
	}
	if emb.calls != 1 {
		t.Errorf("embedding calls = %d, want 1", emb.calls)
	}
}
