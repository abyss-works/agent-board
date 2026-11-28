package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wsk0715/agent-board/internal/store/pg"
)

// Voyage AI direct adapter defaults.
const (
	VoyageProvider               = "voyage"
	DefaultVoyageBaseURL         = "https://api.voyageai.com/v1"
	DefaultVoyageEmbedModel      = "voyage-4"
	DefaultVoyageEmbedDimensions = 1024
	DefaultVoyageRerankModel     = "rerank-3-lite"
	DefaultVoyageEmbedTimeout    = 15 * time.Second
	DefaultVoyageRerankTimeout   = 5 * time.Second
	DefaultVoyageRetryBaseDelay  = 200 * time.Millisecond
	maxVoyageResponseBytes       = 64 << 20
)

// Classified adapter sentinel errors for retry and fallback decisions.
var (
	ErrInvalidConfig     = errors.New("invalid adapter configuration")
	ErrUnauthorized      = errors.New("upstream authentication failed")
	ErrRateLimited       = errors.New("upstream rate limited")
	ErrTimeout           = errors.New("upstream request timed out")
	ErrUpstream          = errors.New("upstream request failed")
	ErrDimensionMismatch = errors.New("embedding vector dimension mismatch")
)

// Compile-time checks against shared PostgreSQL adapter contracts.
var (
	_ pg.Embedder = (*VoyageEmbedder)(nil)
	_ pg.Reranker = (*VoyageReranker)(nil)
)

// APIError is a sanitized, classified error returned by external model adapters.
// Its Error string and Unwrap chain never contain raw credentials or auth headers.
type APIError struct {
	Provider   string
	Operation  string
	StatusCode int
	Retryable  bool
	Timeout    bool
	Message    string
	Sentinels  []error
}

func (e *APIError) Error() string { return e.Message }

// Unwrap exposes only safe sentinel errors so errors.Is works without leaking
// raw transport errors that may carry sensitive headers or credentials.
func (e *APIError) Unwrap() []error { return e.Sentinels }

// IsRetryable reports whether err represents a transient failure safe to retry.
func IsRetryable(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Retryable
	}
	return errors.Is(err, ErrRateLimited) || errors.Is(err, ErrTimeout)
}

// IsTimeout reports whether err represents a timeout or deadline exceeded error.
func IsTimeout(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) && ae.Timeout {
		return true
	}
	return errors.Is(err, ErrTimeout) || errors.Is(err, context.DeadlineExceeded)
}

var (
	bearerHeaderRE = regexp.MustCompile(`(?i)\bBearer\s+[^\s"',;}\]]+`)
	authHeaderRE   = regexp.MustCompile(`(?i)\bAuthorization\s*[:=]\s*[^\r\n,;}\]]+`)
)

// sanitizeText removes raw API keys, Bearer tokens, and Authorization header
// values from any message before it can reach errors or logs.
func sanitizeText(msg string, secret string) string {
	out := msg
	if secret != "" {
		out = strings.ReplaceAll(out, secret, "[redacted]")
	}
	out = authHeaderRE.ReplaceAllString(out, "Authorization: [redacted]")
	out = bearerHeaderRE.ReplaceAllString(out, "Bearer [redacted]")
	return out
}

// ValidateVector checks that vec has the expected length and contains only
// finite float32 values.
func ValidateVector(vec []float32, expectedDim int) error {
	if expectedDim <= 0 {
		return fmt.Errorf("%w: expected dimension must be positive, got %d", ErrDimensionMismatch, expectedDim)
	}
	if len(vec) != expectedDim {
		return fmt.Errorf("%w: got %d dimensions, want %d", ErrDimensionMismatch, len(vec), expectedDim)
	}
	for i, v := range vec {
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("%w: non-finite value at index %d", ErrDimensionMismatch, i)
		}
	}
	return nil
}

// FormatVector serializes a non-empty finite float32 slice as a pgvector text
// literal (e.g. "[0.1,0.2,-0.3]").
func FormatVector(vec []float32) (string, error) {
	if len(vec) == 0 {
		return "", fmt.Errorf("%w: vector is empty", ErrDimensionMismatch)
	}
	var b strings.Builder
	b.Grow(len(vec)*10 + 2)
	b.WriteByte('[')
	for i, v := range vec {
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", fmt.Errorf("%w: non-finite value at index %d", ErrDimensionMismatch, i)
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(f, 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String(), nil
}

// FormatVectorChecked validates expectedDim and serializes vec as a pgvector
// text literal.
func FormatVectorChecked(vec []float32, expectedDim int) (string, error) {
	if err := ValidateVector(vec, expectedDim); err != nil {
		return "", err
	}
	return FormatVector(vec)
}

// VoyageEmbedderConfig configures a direct Voyage AI embeddings adapter.
type VoyageEmbedderConfig struct {
	BaseURL        string
	APIKey         string
	Model          string
	Dimensions     int
	Timeout        time.Duration
	MaxRetries     int
	RetryBaseDelay time.Duration
	HTTPClient     *http.Client
}

// VoyageEmbedder calls Voyage AI's /v1/embeddings endpoint directly.
type VoyageEmbedder struct {
	baseURL        string
	apiKey         string
	model          string
	dimensions     int
	timeout        time.Duration
	maxRetries     int
	retryBaseDelay time.Duration
	httpClient     *http.Client
	configErr      error
}

// NewVoyageEmbedder builds a VoyageEmbedder with safe defaults and a trimmed
// API key kept only in memory.
func NewVoyageEmbedder(cfg VoyageEmbedderConfig) *VoyageEmbedder {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultVoyageBaseURL
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	modelName := strings.TrimSpace(cfg.Model)
	if modelName == "" {
		modelName = DefaultVoyageEmbedModel
	}
	dims := cfg.Dimensions
	if dims <= 0 {
		dims = DefaultVoyageEmbedDimensions
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultVoyageEmbedTimeout
	}
	maxRetries := cfg.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	retryDelay := cfg.RetryBaseDelay
	if retryDelay <= 0 {
		retryDelay = DefaultVoyageRetryBaseDelay
	}
	hc := hardenVoyageHTTPClient(cfg.HTTPClient, timeout)
	configErr := validateVoyageConfig(baseURL, apiKey)
	return &VoyageEmbedder{
		baseURL:        baseURL,
		apiKey:         apiKey,
		model:          modelName,
		dimensions:     dims,
		timeout:        timeout,
		maxRetries:     maxRetries,
		retryBaseDelay: retryDelay,
		httpClient:     hc,
		configErr:      configErr,
	}
}

// Provider returns the adapter provider name ("voyage").
func (e *VoyageEmbedder) Provider() string { return VoyageProvider }

// Model returns the configured Voyage embedding model name.
func (e *VoyageEmbedder) Model() string { return e.model }

// Dimensions returns the configured vector dimension (default 1024).
func (e *VoyageEmbedder) Dimensions() int { return e.dimensions }

// EmbedDocuments embeds document chunks with input_type="document".
func (e *VoyageEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, pg.Usage, error) {
	if len(texts) == 0 {
		return nil, pg.Usage{}, nil
	}
	return e.embed(ctx, texts, "document")
}

// EmbedQuery embeds a search query with input_type="query".
func (e *VoyageEmbedder) EmbedQuery(ctx context.Context, query string) ([]float32, pg.Usage, error) {
	vecs, usage, err := e.embed(ctx, []string{query}, "query")
	if err != nil {
		return nil, usage, err
	}
	if len(vecs) != 1 {
		return nil, usage, &APIError{
			Provider:  VoyageProvider,
			Operation: "embed",
			Message:   fmt.Sprintf("voyage embed returned %d vectors for 1 query", len(vecs)),
			Sentinels: []error{ErrUpstream},
		}
	}
	return vecs[0], usage, nil
}

type voyageEmbedRequest struct {
	Input           []string `json:"input"`
	Model           string   `json:"model"`
	InputType       string   `json:"input_type"`
	OutputDimension int      `json:"output_dimension"`
}

type voyageEmbedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
	Detail string `json:"detail"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (e *VoyageEmbedder) embed(ctx context.Context, texts []string, inputType string) ([][]float32, pg.Usage, error) {
	if e.configErr != nil {
		return nil, pg.Usage{}, &APIError{
			Provider:  VoyageProvider,
			Operation: "embed",
			Message:   sanitizeText("voyage embed: "+e.configErr.Error(), e.apiKey),
			Sentinels: []error{ErrInvalidConfig},
		}
	}
	if e.apiKey == "" {
		return nil, pg.Usage{}, &APIError{
			Provider:  VoyageProvider,
			Operation: "embed",
			Message:   "voyage embed: API key is not configured",
			Sentinels: []error{ErrInvalidConfig},
		}
	}

	reqBody, err := json.Marshal(voyageEmbedRequest{
		Input:           texts,
		Model:           e.model,
		InputType:       inputType,
		OutputDimension: e.dimensions,
	})
	if err != nil {
		return nil, pg.Usage{}, fmt.Errorf("encode voyage embed request: %w", err)
	}

	endpoint := e.baseURL + "/embeddings"
	start := time.Now()
	raw, err := doVoyageJSON(ctx, e.httpClient, endpoint, e.apiKey, reqBody, "embed", e.timeout, e.maxRetries, e.retryBaseDelay)
	usage := pg.Usage{Latency: time.Since(start)}
	if err != nil {
		return nil, usage, err
	}

	var out voyageEmbedResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, usage, &APIError{
			Provider:  VoyageProvider,
			Operation: "embed",
			Message:   sanitizeText("voyage embed: decode response: "+err.Error(), e.apiKey),
			Sentinels: []error{ErrUpstream},
		}
	}
	if out.Error != nil && strings.TrimSpace(out.Error.Message) != "" {
		return nil, usage, &APIError{
			Provider:  VoyageProvider,
			Operation: "embed",
			Message:   "voyage embed: " + shorten(sanitizeText(out.Error.Message, e.apiKey), 200),
			Sentinels: []error{ErrUpstream},
		}
	}
	if strings.TrimSpace(out.Detail) != "" && len(out.Data) == 0 {
		return nil, usage, &APIError{
			Provider:  VoyageProvider,
			Operation: "embed",
			Message:   "voyage embed: " + shorten(sanitizeText(out.Detail, e.apiKey), 200),
			Sentinels: []error{ErrUpstream},
		}
	}
	if len(out.Data) != len(texts) {
		return nil, usage, &APIError{
			Provider:  VoyageProvider,
			Operation: "embed",
			Message:   fmt.Sprintf("voyage embed: returned %d vectors for %d inputs", len(out.Data), len(texts)),
			Sentinels: []error{ErrUpstream},
		}
	}

	vecs := make([][]float32, len(texts))
	for _, item := range out.Data {
		if item.Index < 0 || item.Index >= len(texts) || vecs[item.Index] != nil {
			return nil, usage, &APIError{
				Provider:  VoyageProvider,
				Operation: "embed",
				Message:   fmt.Sprintf("voyage embed: unexpected index %d", item.Index),
				Sentinels: []error{ErrUpstream},
			}
		}
		if err := ValidateVector(item.Embedding, e.dimensions); err != nil {
			return nil, usage, &APIError{
				Provider:  VoyageProvider,
				Operation: "embed",
				Message:   fmt.Sprintf("voyage embed: vector[%d]: %v", item.Index, err),
				Sentinels: []error{ErrDimensionMismatch},
			}
		}
		vecs[item.Index] = item.Embedding
	}

	usage.TotalTokens = out.Usage.TotalTokens
	usage.PromptTokens = out.Usage.TotalTokens
	return vecs, usage, nil
}

// VoyageRerankerConfig configures a direct Voyage AI reranker adapter.
type VoyageRerankerConfig struct {
	BaseURL        string
	APIKey         string
	Model          string
	Timeout        time.Duration
	MaxRetries     int
	RetryBaseDelay time.Duration
	HTTPClient     *http.Client
}

// VoyageReranker calls Voyage AI's /v1/rerank endpoint directly.
type VoyageReranker struct {
	baseURL        string
	apiKey         string
	model          string
	timeout        time.Duration
	maxRetries     int
	retryBaseDelay time.Duration
	httpClient     *http.Client
	configErr      error
}

// NewVoyageReranker builds a VoyageReranker with safe defaults and a trimmed
// API key kept only in memory.
func NewVoyageReranker(cfg VoyageRerankerConfig) *VoyageReranker {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultVoyageBaseURL
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	modelName := strings.TrimSpace(cfg.Model)
	if modelName == "" {
		modelName = DefaultVoyageRerankModel
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultVoyageRerankTimeout
	}
	maxRetries := cfg.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	retryDelay := cfg.RetryBaseDelay
	if retryDelay <= 0 {
		retryDelay = DefaultVoyageRetryBaseDelay
	}
	hc := hardenVoyageHTTPClient(cfg.HTTPClient, timeout)
	configErr := validateVoyageConfig(baseURL, apiKey)
	return &VoyageReranker{
		baseURL:        baseURL,
		apiKey:         apiKey,
		model:          modelName,
		timeout:        timeout,
		maxRetries:     maxRetries,
		retryBaseDelay: retryDelay,
		httpClient:     hc,
		configErr:      configErr,
	}
}

// Provider returns the adapter provider name ("voyage").
func (r *VoyageReranker) Provider() string { return VoyageProvider }

// Model returns the configured Voyage reranker model name.
func (r *VoyageReranker) Model() string { return r.model }

type voyageRerankRequest struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	Model     string   `json:"model"`
	TopK      *int     `json:"top_k,omitempty"`
}

type voyageRerankResponse struct {
	Data []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	} `json:"data"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
	Detail string `json:"detail"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Rerank scores candidate documents against query and returns them ordered by
// descending relevance score.
func (r *VoyageReranker) Rerank(ctx context.Context, query string, docs []pg.RerankDocument, topK int) ([]pg.RerankResult, pg.Usage, error) {
	if len(docs) == 0 {
		return nil, pg.Usage{}, nil
	}
	if r.configErr != nil {
		return nil, pg.Usage{}, &APIError{
			Provider:  VoyageProvider,
			Operation: "rerank",
			Message:   sanitizeText("voyage rerank: "+r.configErr.Error(), r.apiKey),
			Sentinels: []error{ErrInvalidConfig},
		}
	}
	if r.apiKey == "" {
		return nil, pg.Usage{}, &APIError{
			Provider:  VoyageProvider,
			Operation: "rerank",
			Message:   "voyage rerank: API key is not configured",
			Sentinels: []error{ErrInvalidConfig},
		}
	}

	texts := make([]string, len(docs))
	for i, d := range docs {
		texts[i] = d.Text
	}
	payload := voyageRerankRequest{
		Query:     query,
		Documents: texts,
		Model:     r.model,
	}
	if topK > 0 {
		k := min(topK, len(docs))
		payload.TopK = &k
	}
	reqBody, err := json.Marshal(payload)
	if err != nil {
		return nil, pg.Usage{}, fmt.Errorf("encode voyage rerank request: %w", err)
	}

	endpoint := r.baseURL + "/rerank"
	start := time.Now()
	raw, err := doVoyageJSON(ctx, r.httpClient, endpoint, r.apiKey, reqBody, "rerank", r.timeout, r.maxRetries, r.retryBaseDelay)
	usage := pg.Usage{Latency: time.Since(start)}
	if err != nil {
		return nil, usage, err
	}

	var out voyageRerankResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, usage, &APIError{
			Provider:  VoyageProvider,
			Operation: "rerank",
			Message:   sanitizeText("voyage rerank: decode response: "+err.Error(), r.apiKey),
			Sentinels: []error{ErrUpstream},
		}
	}
	if out.Error != nil && strings.TrimSpace(out.Error.Message) != "" {
		return nil, usage, &APIError{
			Provider:  VoyageProvider,
			Operation: "rerank",
			Message:   "voyage rerank: " + shorten(sanitizeText(out.Error.Message, r.apiKey), 200),
			Sentinels: []error{ErrUpstream},
		}
	}
	if strings.TrimSpace(out.Detail) != "" && len(out.Data) == 0 {
		return nil, usage, &APIError{
			Provider:  VoyageProvider,
			Operation: "rerank",
			Message:   "voyage rerank: " + shorten(sanitizeText(out.Detail, r.apiKey), 200),
			Sentinels: []error{ErrUpstream},
		}
	}
	if len(out.Data) == 0 {
		return nil, usage, &APIError{
			Provider:  VoyageProvider,
			Operation: "rerank",
			Message:   "voyage rerank: upstream returned empty data for non-empty documents",
			Sentinels: []error{ErrUpstream},
		}
	}

	seen := make(map[int]bool, len(out.Data))
	results := make([]pg.RerankResult, 0, len(out.Data))
	for _, item := range out.Data {
		if item.Index < 0 || item.Index >= len(docs) || seen[item.Index] {
			return nil, usage, &APIError{
				Provider:  VoyageProvider,
				Operation: "rerank",
				Message:   fmt.Sprintf("voyage rerank: unexpected index %d", item.Index),
				Sentinels: []error{ErrUpstream},
			}
		}
		seen[item.Index] = true
		results = append(results, pg.RerankResult{
			Index: item.Index,
			ID:    docs[item.Index].ID,
			Score: item.RelevanceScore,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Index < results[j].Index
		}
		return results[i].Score > results[j].Score
	})
	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}

	usage.TotalTokens = out.Usage.TotalTokens
	usage.PromptTokens = out.Usage.TotalTokens
	return results, usage, nil
}

func disallowRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func hardenVoyageHTTPClient(hc *http.Client, timeout time.Duration) *http.Client {
	if hc == nil {
		return &http.Client{
			Timeout:       timeout,
			CheckRedirect: disallowRedirect,
		}
	}
	cloned := *hc
	cloned.CheckRedirect = disallowRedirect
	return &cloned
}

func validateVoyageConfig(baseURL, apiKey string) error {
	if strings.ContainsAny(apiKey, "\r\n\x00") {
		return errors.New("API key contains invalid control characters")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return errors.New("invalid base URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported base URL scheme %q (must be http or https)", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("base URL host is required")
	}
	if u.User != nil {
		return errors.New("base URL must not contain userinfo")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("base URL must not contain query parameters or fragments")
	}
	return nil
}

func doVoyageJSON(
	ctx context.Context,
	hc *http.Client,
	endpoint, apiKey string,
	body []byte,
	op string,
	timeout time.Duration,
	maxRetries int,
	retryBaseDelay time.Duration,
) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			shift := min(attempt-1, 6)
			delay := retryBaseDelay << shift
			if delay <= 0 {
				delay = retryBaseDelay
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, classifyTransportError(ctx.Err(), op, apiKey)
			case <-timer.C:
			}
		}

		reqCtx := ctx
		var cancel context.CancelFunc
		if timeout > 0 {
			reqCtx, cancel = context.WithTimeout(ctx, timeout)
		}

		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			if cancel != nil {
				cancel()
			}
			return nil, &APIError{
				Provider:  VoyageProvider,
				Operation: op,
				Message:   sanitizeText(fmt.Sprintf("voyage %s: build request: %v", op, err), apiKey),
				Sentinels: []error{ErrInvalidConfig},
			}
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)

		resp, err := hc.Do(req)
		if err != nil {
			if cancel != nil {
				cancel()
			}
			apiErr := classifyTransportError(err, op, apiKey)
			lastErr = apiErr
			if apiErr.Retryable && attempt < maxRetries && ctx.Err() == nil {
				continue
			}
			return nil, apiErr
		}

		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxVoyageResponseBytes))
		_ = resp.Body.Close()
		if cancel != nil {
			cancel()
		}
		if readErr != nil {
			apiErr := classifyTransportError(readErr, op, apiKey)
			lastErr = apiErr
			if apiErr.Retryable && attempt < maxRetries && ctx.Err() == nil {
				continue
			}
			return nil, apiErr
		}

		if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
			return raw, nil
		}

		apiErr := classifyStatusError(resp.StatusCode, resp.Status, raw, op, apiKey)
		lastErr = apiErr
		if apiErr.Retryable && attempt < maxRetries && ctx.Err() == nil {
			continue
		}
		return nil, apiErr
	}
	return nil, lastErr
}

func classifyTransportError(err error, op, apiKey string) *APIError {
	sanitized := sanitizeText(fmt.Sprintf("voyage %s: %v", op, err), apiKey)
	if errors.Is(err, context.Canceled) {
		return &APIError{
			Provider:  VoyageProvider,
			Operation: op,
			Retryable: false,
			Message:   sanitized,
			Sentinels: []error{context.Canceled},
		}
	}
	var netErr net.Error
	isTimeout := errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
	if isTimeout {
		return &APIError{
			Provider:  VoyageProvider,
			Operation: op,
			Retryable: true,
			Timeout:   true,
			Message:   sanitized,
			Sentinels: []error{ErrTimeout, context.DeadlineExceeded},
		}
	}
	return &APIError{
		Provider:  VoyageProvider,
		Operation: op,
		Retryable: true,
		Message:   sanitized,
		Sentinels: []error{ErrUpstream},
	}
}

func classifyStatusError(code int, status string, raw []byte, op, apiKey string) *APIError {
	msg := sanitizeText(extractVoyageErrorMessage(raw), apiKey)
	detail := sanitizeText(fmt.Sprintf("voyage %s: HTTP %s", op, status), apiKey)
	if msg != "" {
		detail += ": " + shorten(msg, 200)
	}

	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return &APIError{
			Provider:   VoyageProvider,
			Operation:  op,
			StatusCode: code,
			Retryable:  false,
			Message:    detail,
			Sentinels:  []error{ErrUnauthorized},
		}
	case code == http.StatusTooManyRequests:
		return &APIError{
			Provider:   VoyageProvider,
			Operation:  op,
			StatusCode: code,
			Retryable:  true,
			Message:    detail,
			Sentinels:  []error{ErrRateLimited},
		}
	case code == http.StatusRequestTimeout || code == http.StatusGatewayTimeout:
		return &APIError{
			Provider:   VoyageProvider,
			Operation:  op,
			StatusCode: code,
			Retryable:  true,
			Timeout:    true,
			Message:    detail,
			Sentinels:  []error{ErrTimeout, ErrUpstream},
		}
	case code >= 500:
		return &APIError{
			Provider:   VoyageProvider,
			Operation:  op,
			StatusCode: code,
			Retryable:  true,
			Message:    detail,
			Sentinels:  []error{ErrUpstream},
		}
	default:
		return &APIError{
			Provider:   VoyageProvider,
			Operation:  op,
			StatusCode: code,
			Retryable:  false,
			Message:    detail,
			Sentinels:  []error{ErrUpstream},
		}
	}
}

func extractVoyageErrorMessage(raw []byte) string {
	var parsed struct {
		Detail string `json:"detail"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err == nil {
		if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
			return strings.TrimSpace(parsed.Error.Message)
		}
		if strings.TrimSpace(parsed.Detail) != "" {
			return strings.TrimSpace(parsed.Detail)
		}
	}
	return strings.TrimSpace(string(raw))
}
