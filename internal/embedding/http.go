package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// HTTPEmbedder implements the Embedder interface over HTTP + Unix socket.
//
// Dimension is runtime-discovered: first successful response sets Dims().
// Subsequent responses must match — violation returns ErrDimensionMismatch.
//
// Vector count must match request text count — violation returns ErrVectorCountMismatch.
//
// TODO(v0.3): Replace JSON vectors with versioned binary float32 transport.
//
//	TODO(v0.3): protocol framing: magic bytes, version, dimension, count, payload.
type HTTPEmbedder struct {
	client  *http.Client
	socket  string
	model   string          // model name from latest successful response
	dims    atomic.Int32    // runtime-discovered, 0 until first success
	modelMu atomic.Value    // stores string, updated on each successful response
}

// NewHTTPEmbedder creates a new HTTP embedder over Unix socket.
// Timeout defaults to 30s. Socket path is configurable.
//
// Socket path precedence:
//  1. explicit path argument
//  2. ${XDG_RUNTIME_DIR}/ioc/embedder.sock
//  3. /tmp/ioc-embedder.sock (fallback)
func NewHTTPEmbedder(socketPath string) (*HTTPEmbedder, error) {
	if socketPath == "" {
		socketPath = defaultSocketPath()
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}

	return &HTTPEmbedder{
		client: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		socket: socketPath,
	}, nil
}

// EmbedRequest is the JSON request body for /embed.
type embedRequest struct {
	Texts []string `json:"texts"`
}

// embedResponse is the JSON response body from /embed.
type embedResponse struct {
	Dimension int         `json:"dimension"`
	Vectors   [][]float32 `json:"vectors"`
	Model     string      `json:"model"`
}

// Embed converts texts to embedding vectors via the external service.
//
// Model returns the model name from the latest successful response.
// Returns empty string until the first successful Embed call.
func (e *HTTPEmbedder) Embed(ctx context.Context, texts []string) (*Batch, error) {
	if len(texts) == 0 {
		return nil, ErrInvalidInput
	}

	req := embedRequest{Texts: texts}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("http embedder: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http embedder: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInferenceFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		return nil, ErrInvalidInput
	}
	if resp.StatusCode == http.StatusServiceUnavailable {
		return nil, fmt.Errorf("%w: model unavailable", ErrInferenceFailed)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: server returned %d", ErrInferenceFailed, resp.StatusCode)
	}

	var respData embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return nil, fmt.Errorf("%w: decode response: %v", ErrInferenceFailed, err)
	}

	// Validate dimension consistency.
	if e.dims.Load() == 0 {
		e.dims.Store(int32(respData.Dimension))
	} else if e.dims.Load() != int32(respData.Dimension) {
		return nil, ErrDimensionMismatch
	}

	// Validate vector count.
	if len(respData.Vectors) != len(texts) {
		return nil, ErrVectorCountMismatch
	}

	// Store model name.
	e.modelMu.Store(respData.Model)

	vectors := make([]Vector, len(respData.Vectors))
	for i, vec := range respData.Vectors {
		vectors[i] = Vector{
			Data:       vec,
			TokenCount: approximateTokens(texts[i]),
		}
	}

	return &Batch{
		Vectors:   vectors,
		Dimension: respData.Dimension,
		Model:     respData.Model,
		CreatedAt: time.Now(),
	}, nil
}

// Dims returns the runtime-discovered embedding dimension.
// Returns 0 until the first successful Embed call.
// Invariant: MUST remain constant after discovery.
func (e *HTTPEmbedder) Dims() int { return int(e.dims.Load()) }

// Model returns the model name from the latest successful response.
// Returns empty string until the first successful Embed call.
func (e *HTTPEmbedder) Model() string {
	v := e.modelMu.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}

func defaultSocketPath() string {
	runtimeDir := "/tmp"
	return fmt.Sprintf("%s/ioc-embedder.sock", runtimeDir)
}
