package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
)

// Reranker re-scores (query, passage) pairs with a cross-encoder — far more
// accurate than bi-encoder cosine, used as a last-mile rerank over top-N hits.
type Reranker interface {
	// Rerank returns one relevance score per passage (higher = more relevant).
	Rerank(ctx context.Context, query string, passages []string) ([]float64, error)
	Model() string
}

// HTTPReranker calls the external /rerank endpoint (py CrossEncoder).
type HTTPReranker struct {
	client  *http.Client
	baseURL string
	model   atomic.Value // string
}

// NewHTTPReranker dials the same endpoint shape as NewHTTPEmbedder.
func NewHTTPReranker(endpoint string) *HTTPReranker {
	c, base := dialClient(endpoint)
	return &HTTPReranker{client: c, baseURL: base}
}

type rerankRequest struct {
	Query    string   `json:"query"`
	Passages []string `json:"passages"`
}

type rerankResponse struct {
	Scores []float64 `json:"scores"`
	Model  string    `json:"model"`
}

// Rerank scores each passage against the query.
func (r *HTTPReranker) Rerank(ctx context.Context, query string, passages []string) ([]float64, error) {
	if len(passages) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(rerankRequest{Query: query, Passages: passages})
	if err != nil {
		return nil, fmt.Errorf("reranker: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("reranker: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reranker: do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reranker: server returned %d", resp.StatusCode)
	}
	var data rerankResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("reranker: decode: %w", err)
	}
	if len(data.Scores) != len(passages) {
		return nil, fmt.Errorf("reranker: got %d scores for %d passages", len(data.Scores), len(passages))
	}
	r.model.Store(data.Model)
	return data.Scores, nil
}

// Model returns the reranker model id ("" until the first Rerank).
func (r *HTTPReranker) Model() string {
	if v := r.model.Load(); v != nil {
		return v.(string)
	}
	return ""
}
