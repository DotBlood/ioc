package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// bgeQueryInstruction is bge-v1.5's recommended retrieval prefix for the QUERY
// side (passages are encoded without it). Override via IOC_QUERY_INSTRUCTION.
const bgeQueryInstruction = "Represent this sentence for searching relevant passages: "

func defaultQueryInstruction() string {
	if v := os.Getenv("IOC_QUERY_INSTRUCTION"); v != "" {
		return v
	}
	return bgeQueryInstruction
}

// HTTPEmbedder talks to an external embedding service (py/embed_server.py).
//
// The endpoint may be either:
//   - a TCP base URL:   "http://127.0.0.1:8088"   (works on Windows)
//   - a Unix socket:    "unix:/tmp/ioc/embedder.sock"  or  "/tmp/ioc/embedder.sock"
//
// The dimension is runtime-discovered on the first successful response and then fixed.
// NOTE: this is an EMBEDDING model, not a reasoning LLM. IOC never calls an LLM.
type HTTPEmbedder struct {
	client     *http.Client
	baseURL    string
	queryInstr string
	model      atomic.Value // string
	dims       atomic.Int32
}

// NewHTTPEmbedder builds an embedder for a TCP URL or a Unix socket endpoint.
func NewHTTPEmbedder(endpoint string) *HTTPEmbedder {
	e := &HTTPEmbedder{queryInstr: defaultQueryInstruction()}
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		e.client = &http.Client{Timeout: 120 * time.Second}
		e.baseURL = strings.TrimRight(endpoint, "/")
		return e
	}
	// Unix socket: "unix:/path" or bare "/path".
	sock := strings.TrimPrefix(endpoint, "unix:")
	e.client = &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
		Timeout: 120 * time.Second,
	}
	e.baseURL = "http://unix"
	return e
}

// EmbedQuery prepends the retrieval query instruction to each text, then embeds.
func (e *HTTPEmbedder) EmbedQuery(ctx context.Context, texts []string) ([][]float32, error) {
	if e.queryInstr == "" {
		return e.Embed(ctx, texts)
	}
	pref := make([]string, len(texts))
	for i, t := range texts {
		pref[i] = e.queryInstr + t
	}
	return e.Embed(ctx, pref)
}

type embedRequest struct {
	Texts []string `json:"texts"`
}

type embedResponse struct {
	Dimension int         `json:"dimension"`
	Vectors   [][]float32 `json:"vectors"`
	Model     string      `json:"model"`
}

type healthResponse struct {
	Status    string `json:"status"`
	Model     string `json:"model"`
	Dimension int    `json:"dimension"`
}

// Health pings GET /health and records model + dims.
func (e *HTTPEmbedder) Health(ctx context.Context) (model string, dims int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.baseURL+"/health", nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("http embedder: health: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("http embedder: health: server returned %d", resp.StatusCode)
	}
	var h healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return "", 0, fmt.Errorf("http embedder: health decode: %w", err)
	}
	if h.Dimension > 0 {
		e.dims.Store(int32(h.Dimension))
	}
	if h.Model != "" {
		e.model.Store(h.Model)
	}
	return h.Model, h.Dimension, nil
}

// Embed returns one normalized vector per text from the external service.
func (e *HTTPEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, ErrEmptyInput
	}
	body, err := json.Marshal(embedRequest{Texts: texts})
	if err != nil {
		return nil, fmt.Errorf("http embedder: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http embedder: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http embedder: do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http embedder: server returned %d", resp.StatusCode)
	}

	var data embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("http embedder: decode: %w", err)
	}
	if e.dims.Load() == 0 {
		e.dims.Store(int32(data.Dimension))
	} else if e.dims.Load() != int32(data.Dimension) {
		return nil, fmt.Errorf("http embedder: dims changed from %d to %d", e.dims.Load(), data.Dimension)
	}
	if len(data.Vectors) != len(texts) {
		return nil, fmt.Errorf("http embedder: got %d vectors for %d texts", len(data.Vectors), len(texts))
	}
	e.model.Store(data.Model)
	return data.Vectors, nil
}

// Dims returns the runtime-discovered dimension (0 until first Health/Embed).
func (e *HTTPEmbedder) Dims() int { return int(e.dims.Load()) }

// Model returns the model id ("" until first Health/Embed).
func (e *HTTPEmbedder) Model() string {
	if v := e.model.Load(); v != nil {
		return v.(string)
	}
	return ""
}
