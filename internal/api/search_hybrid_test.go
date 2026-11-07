package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/wsk0715/agent-board/internal/embed"
	"github.com/wsk0715/agent-board/internal/model"
)

// conceptEmbedder gives a text one dimension per concept, counting the words of
// the concept, so that texts about the same thing are near without sharing a
// word. It fails while fail is set.
type conceptEmbedder struct {
	fail    bool
	queries []string
}

var concepts = [][]string{{"잠금", "락", "lock"}, {"큐", "queue"}, {"배포", "release"}}

func (c *conceptEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
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
		o.Semantic = &embed.Searcher{Embedder: emb, Prefix: "Query: ", Index: index}
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
// its best chunk as the snippet, and the deleted issue stays out.
func TestHybridSearchFindsWhatKeywordsMiss(t *testing.T) {
	e, emb := hybridEnv(t)
	l := e.searchHits(t, "/v1/search?q=락")
	if l.Total != 4 || len(l.Items) != 4 {
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
	// There is no similarity threshold: the document about queues comes last.
	if last := l.Items[3]; last.Key != apiDocPriority {
		t.Errorf("last hit = %+v", last)
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
	if l.Total != 4 || l.Items[0].Type != model.SearchIssue || l.Items[0].Project != "AGB" || !strings.Contains(l.Items[0].Snippet, "[잠금]") {
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
	if l := e.searchHits(t, "/v1/search?q=락&project=AGB"); l.Total != 3 {
		t.Errorf("project AGB = %v", hitKeys(l))
	}
	if l := e.searchHits(t, "/v1/search?q=락&type=document"); l.Total != 2 || l.Items[0].Type != model.SearchDocument {
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
	if l := e.searchHits(t, "/v1/search?q=락"); l.Total != 3 {
		t.Errorf("default hides the archived document: %v", hitKeys(l))
	}
	if l := e.searchHits(t, "/v1/search?q=락&type=document&state=archived"); l.Total != 1 || l.Items[0].Key != apiDocLock || *l.Items[0].State != "archived" {
		t.Errorf("state archived = %v", hitKeys(l))
	}
	if l := e.searchHits(t, "/v1/search?q=락&state=all"); l.Total != 4 {
		t.Errorf("state all = %v", hitKeys(l))
	}
	if l := e.searchHits(t, "/v1/search?q=락&status=done"); l.Total != 1 || l.Items[0].Type != model.SearchDocument || l.Items[0].Key != apiDocPriority {
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
	for off := 0; off < 4; off++ {
		l := e.searchHits(t, "/v1/search?q=락&limit=1&offset="+string(rune('0'+off)))
		if l.Total != 4 || len(l.Items) != 1 {
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
