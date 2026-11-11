package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Embedder turns texts into embedding vectors, one per text and in order. The
// responseID is the "id" of the response and is empty when the endpoint sends
// none.
type Embedder interface {
	Embed(ctx context.Context, texts []string) (vecs [][]float32, responseID string, err error)
}

// Client calls an OpenAI compatible embeddings endpoint.
type Client struct {
	url, key, model string
	http            *http.Client
}

// NewClient returns a client for the endpoint at url, which is the full URL of
// the embeddings resource.
func NewClient(url, key, model string) *Client {
	return &Client{url: url, key: key, model: model, http: &http.Client{Timeout: 60 * time.Second}}
}

// Embed returns the vectors of texts in the order of texts and the "id" of the
// response, which is empty when the endpoint sends none.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, string, error) {
	body, err := json.Marshal(map[string]any{"model": c.model, "input": texts})
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, "", c.redact(err.Error(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", c.redact(err.Error(), err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, "", c.redact(err.Error(), err)
	}
	var out struct {
		ID   string `json:"id"`
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	jsonErr := json.Unmarshal(raw, &out)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(raw))
		if jsonErr == nil && out.Error != nil {
			msg = out.Error.Message
		}
		return nil, "", c.redact(fmt.Sprintf("embeddings API returned %s: %s", resp.Status, shorten(msg, 200)), nil)
	}
	if jsonErr != nil {
		return nil, "", c.redact("decode embeddings response: "+jsonErr.Error(), nil)
	}
	if out.Error != nil {
		return nil, "", c.redact("embeddings API error: "+shorten(out.Error.Message, 200), nil)
	}
	if len(out.Data) != len(texts) {
		return nil, "", fmt.Errorf("embeddings API returned %d vectors for %d inputs", len(out.Data), len(texts))
	}
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(texts) || vecs[d.Index] != nil {
			return nil, "", fmt.Errorf("embeddings API returned an unexpected index %d", d.Index)
		}
		if len(d.Embedding) == 0 {
			return nil, "", fmt.Errorf("embeddings API returned an empty vector for index %d", d.Index)
		}
		vecs[d.Index] = d.Embedding
	}
	return vecs, out.ID, nil
}

// redact returns msg with the API key removed as an error that wraps cause, so
// callers can still test it with errors.Is. cause may be nil.
func (c *Client) redact(msg string, cause error) error {
	return redacted{strings.ReplaceAll(msg, c.key, "[redacted]"), cause}
}

// redacted reports a message free of the API key. Only the message is shown;
// cause is kept for errors.Is and errors.As.
type redacted struct {
	msg   string
	cause error
}

func (e redacted) Error() string { return e.msg }
func (e redacted) Unwrap() error { return e.cause }

func shorten(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}
