package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// Response bounds (V8). A hostile/buggy embedder endpoint (see V2) could stream an
// unbounded body (OOM) or return NaN/Inf components that poison cosine ranking, so
// responses are size-capped before decode and every vector is validated finite.
const (
	maxEmbedRespBytes  = 64 << 20 // /embed and /rerank response cap
	maxHealthRespBytes = 64 << 10 // /health is tiny
)

// validateVectors rejects a response whose vectors aren't all dims-long and finite.
func validateVectors(vecs [][]float32, dims int) error {
	for i, v := range vecs {
		if len(v) != dims {
			return fmt.Errorf("http embedder: vector %d has %d components, want %d", i, len(v), dims)
		}
		for j, c := range v {
			if math.IsNaN(float64(c)) || math.IsInf(float64(c), 0) {
				return fmt.Errorf("http embedder: vector %d component %d is non-finite", i, j)
			}
		}
	}
	return nil
}

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

// noRedirect refuses HTTP redirects: the endpoint policy (validateEndpoint) vets the
// CONFIGURED URL, but the default client would follow up to 10 redirects without
// re-checking — letting a validated https endpoint redirect to http:// (downgrade) or
// another host and exfiltrate all POSTed text. Returning ErrUseLastResponse hands the
// 3xx back as-is, so the caller's `StatusCode != 200` check rejects it. No legitimate
// embedder/reranker uses redirects.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// dialClient builds an HTTP client + base URL for a TCP URL or Unix socket endpoint.
func dialClient(endpoint string) (*http.Client, string) {
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return &http.Client{Timeout: 120 * time.Second, CheckRedirect: noRedirect}, strings.TrimRight(endpoint, "/")
	}
	sock := strings.TrimPrefix(endpoint, "unix:")
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
		Timeout:       120 * time.Second,
		CheckRedirect: noRedirect,
	}, "http://unix"
}

// NewHTTPEmbedder builds an embedder for a TCP URL or a Unix socket endpoint. It
// enforces the endpoint policy (V2): a non-loopback target needs https and
// allowRemote, plaintext-remote is always refused. NOTE: never log embedded content.
func NewHTTPEmbedder(endpoint string, allowRemote bool) (*HTTPEmbedder, error) {
	if err := validateEndpoint(endpoint, allowRemote); err != nil {
		return nil, err
	}
	c, base := dialClient(endpoint)
	return &HTTPEmbedder{client: c, baseURL: base, queryInstr: defaultQueryInstruction()}, nil
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("http embedder: health: server returned %d", resp.StatusCode)
	}
	var h healthResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxHealthRespBytes)).Decode(&h); err != nil {
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http embedder: server returned %d", resp.StatusCode)
	}

	var data embedResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxEmbedRespBytes)).Decode(&data); err != nil {
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
	// Per-vector length + finiteness (a NaN/Inf component would corrupt cosine).
	if err := validateVectors(data.Vectors, data.Dimension); err != nil {
		return nil, err
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
