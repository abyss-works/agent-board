package embed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/store/pg"
)

const testVoyageSecret = "pa-dummy-secret-token-9876543210"

func makeVector(dim int, val float32) []float32 {
	out := make([]float32, dim)
	for i := range out {
		out[i] = val + float32(i%7)*0.001
	}
	return out
}

func TestVoyageEmbedderDocumentsAndQuery(t *testing.T) {
	var requests []struct {
		Auth            string
		Path            string
		Model           string
		InputType       string
		OutputDimension int
		Input           []string
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body voyageEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests = append(requests, struct {
			Auth            string
			Path            string
			Model           string
			InputType       string
			OutputDimension int
			Input           []string
		}{
			Auth:            r.Header.Get("Authorization"),
			Path:            r.URL.Path,
			Model:           body.Model,
			InputType:       body.InputType,
			OutputDimension: body.OutputDimension,
			Input:           body.Input,
		})

		type item struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		data := make([]item, len(body.Input))
		// Return in reverse index order to verify index alignment.
		for i := range body.Input {
			idx := len(body.Input) - 1 - i
			data[i] = item{Index: idx, Embedding: makeVector(1024, float32(idx+1))}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   data,
			"usage":  map[string]int{"total_tokens": 42},
		})
	}))
	defer srv.Close()

	// Include surrounding whitespace and CRLF to verify in-memory strip.
	emb := NewVoyageEmbedder(VoyageEmbedderConfig{
		BaseURL: srv.URL + "/v1/",
		APIKey:  "  " + testVoyageSecret + "\r\n",
	})

	if emb.Provider() != "voyage" || emb.Model() != "voyage-4" || emb.Dimensions() != 1024 {
		t.Fatalf("defaults = (%s, %s, %d)", emb.Provider(), emb.Model(), emb.Dimensions())
	}

	vecs, usage, err := emb.EmbedDocuments(context.Background(), []string{"doc alpha", "doc beta"})
	if err != nil {
		t.Fatalf("EmbedDocuments: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 1024 || len(vecs[1]) != 1024 {
		t.Fatalf("unexpected vectors shape: %d", len(vecs))
	}
	if vecs[0][0] != 1 || vecs[1][0] != 2 {
		t.Fatalf("vectors not reordered by index: got [%v, %v]", vecs[0][0], vecs[1][0])
	}
	if usage.TotalTokens != 42 {
		t.Fatalf("TotalTokens = %d, want 42", usage.TotalTokens)
	}

	qVec, qUsage, err := emb.EmbedQuery(context.Background(), "search query")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if len(qVec) != 1024 || qUsage.TotalTokens != 42 {
		t.Fatalf("unexpected query result len=%d tokens=%d", len(qVec), qUsage.TotalTokens)
	}

	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	if requests[0].Auth != "Bearer "+testVoyageSecret {
		t.Fatalf("stripped auth header mismatch: %q", requests[0].Auth)
	}
	if requests[0].Path != "/v1/embeddings" || requests[0].InputType != "document" || requests[0].OutputDimension != 1024 {
		t.Fatalf("doc request mismatch: %+v", requests[0])
	}
	if requests[1].Path != "/v1/embeddings" || requests[1].InputType != "query" || requests[1].OutputDimension != 1024 {
		t.Fatalf("query request mismatch: %+v", requests[1])
	}
}

func TestVoyageEmbedderDimensionValidation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 0, "embedding": makeVector(512, 0.5)},
			},
		})
	}))
	defer srv.Close()

	emb := NewVoyageEmbedder(VoyageEmbedderConfig{
		BaseURL: srv.URL,
		APIKey:  testVoyageSecret,
	})
	_, _, err := emb.EmbedQuery(context.Background(), "mismatch")
	if !errors.Is(err, ErrDimensionMismatch) {
		t.Fatalf("err = %v, want ErrDimensionMismatch", err)
	}
}

func TestVoyageEmbedderRetryAndSanitization(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"detail":"rate limited for Bearer ` + testVoyageSecret + `"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"index": 0, "embedding": makeVector(1024, 0.25)}},
			"usage": map[string]int{"total_tokens": 9},
		})
	}))
	defer srv.Close()

	emb := NewVoyageEmbedder(VoyageEmbedderConfig{
		BaseURL:        srv.URL,
		APIKey:         testVoyageSecret,
		MaxRetries:     2,
		RetryBaseDelay: 5 * time.Millisecond,
	})
	vec, _, err := emb.EmbedQuery(context.Background(), "retryable")
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if len(vec) != 1024 || attempts.Load() != 2 {
		t.Fatalf("len=%d attempts=%d", len(vec), attempts.Load())
	}

	// Verify 401 and transport errors never leak API key or Authorization header.
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `{"detail":"bad header Authorization: Bearer %s and raw %s"}`, testVoyageSecret, testVoyageSecret)
	}))
	defer errSrv.Close()

	badEmb := NewVoyageEmbedder(VoyageEmbedderConfig{
		BaseURL: errSrv.URL,
		APIKey:  testVoyageSecret,
	})
	_, _, err = badEmb.EmbedQuery(context.Background(), "auth fail")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(err.Error(), testVoyageSecret) {
		t.Fatalf("error leaked API key: %s", err.Error())
	}
	if strings.Contains(err.Error(), "Bearer "+testVoyageSecret) {
		t.Fatalf("error leaked Bearer header: %s", err.Error())
	}

	// Simulate transport error whose message embeds both the key and an auth header.
	leakClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("dial failed with Authorization: %s raw=%s", req.Header.Get("Authorization"), testVoyageSecret)
		}),
	}
	leakEmb := NewVoyageEmbedder(VoyageEmbedderConfig{
		BaseURL:    "http://127.0.0.1:1",
		APIKey:     testVoyageSecret,
		HTTPClient: leakClient,
		MaxRetries: 0,
	})
	_, _, err = leakEmb.EmbedQuery(context.Background(), "transport fail")
	if err == nil {
		t.Fatal("expected transport error")
	}
	if strings.Contains(err.Error(), testVoyageSecret) {
		t.Fatalf("transport error leaked API key: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("expected redaction marker in error: %s", err.Error())
	}
}

func TestVoyageRerankerSuccessTimeoutAndSanitization(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rerank" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+testVoyageSecret {
			t.Errorf("unexpected auth header %q", r.Header.Get("Authorization"))
		}
		var req voyageRerankRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode rerank: %v", err)
		}
		if req.Model != DefaultVoyageRerankModel || req.TopK == nil || *req.TopK != 2 {
			t.Errorf("unexpected rerank req: %+v", req)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 2, "relevance_score": 0.91},
				{"index": 0, "relevance_score": 0.64},
			},
			"usage": map[string]int{"total_tokens": 18},
		})
	}))
	defer srv.Close()

	rr := NewVoyageReranker(VoyageRerankerConfig{
		BaseURL: srv.URL,
		APIKey:  "\t" + testVoyageSecret + "\n",
	})
	docs := []pg.RerankDocument{
		{ID: "d0", Text: "first"},
		{ID: "d1", Text: "second"},
		{ID: "d2", Text: "third"},
	}
	res, usage, err := rr.Rerank(context.Background(), "query", docs, 2)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(res) != 2 || res[0].ID != "d2" || res[0].Score != 0.91 || res[1].ID != "d0" || usage.TotalTokens != 18 {
		t.Fatalf("unexpected rerank results: %+v usage=%+v", res, usage)
	}

	// Timeout classification test.
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(500 * time.Millisecond):
		case <-r.Context().Done():
		}
	}))
	defer slowSrv.Close()

	slowRR := NewVoyageReranker(VoyageRerankerConfig{
		BaseURL: slowSrv.URL,
		APIKey:  testVoyageSecret,
		Timeout: 40 * time.Millisecond,
	})
	_, _, err = slowRR.Rerank(context.Background(), "query", docs, 2)
	if !IsTimeout(err) || !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if strings.Contains(err.Error(), testVoyageSecret) {
		t.Fatalf("timeout error leaked API key: %s", err.Error())
	}
}

func TestFormatVectorAndValidation(t *testing.T) {
	lit, err := FormatVector([]float32{0.5, -1.25, 0})
	if err != nil {
		t.Fatalf("FormatVector: %v", err)
	}
	if lit != "[0.5,-1.25,0]" {
		t.Fatalf("FormatVector = %q, want [0.5,-1.25,0]", lit)
	}

	if _, err := FormatVector(nil); !errors.Is(err, ErrDimensionMismatch) {
		t.Fatalf("FormatVector(nil) err = %v, want ErrDimensionMismatch", err)
	}
	if _, err := FormatVector([]float32{float32(math.NaN())}); !errors.Is(err, ErrDimensionMismatch) {
		t.Fatalf("FormatVector(NaN) err = %v, want ErrDimensionMismatch", err)
	}
	if _, err := FormatVectorChecked(makeVector(1024, 0.1), 1024); err != nil {
		t.Fatalf("FormatVectorChecked(1024): %v", err)
	}
	if _, err := FormatVectorChecked(makeVector(512, 0.1), 1024); !errors.Is(err, ErrDimensionMismatch) {
		t.Fatalf("FormatVectorChecked(512, 1024) err = %v, want ErrDimensionMismatch", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
