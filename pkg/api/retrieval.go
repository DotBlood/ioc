package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/DotBlood/ioc/internal/model"
	"github.com/DotBlood/ioc/internal/retrieval"
)

// Query performs hybrid search (dense + sparse + fusion).
// Returns up to topK results sorted by relevance (descending).
// Empty query returns ErrInvalidInput.
// topK is clamped to 1000.
func (r *Runtime) Query(ctx context.Context, query string, topK int) ([]QueryResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query: %w", ErrInvalidInput)
	}
	if topK <= 0 {
		return nil, fmt.Errorf("query: topK must be positive: %w", ErrInvalidInput)
	}
	if topK > 1000 {
		topK = 1000
	}

	engine, err := r.retrievalEngine(ctx)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}

	results, err := engine.Query(ctx, query, retrieval.QueryOptions{
		TopK:       topK,
		VectorTopK: 50,
		TextTopK:   50,
	})
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}

	return r.convertResults(ctx, results), nil
}

// Trace performs hybrid search with full pipeline trace.
// Returns results and per-stage diagnostics.
// Stage kind values: "dense_search", "sparse_search", "rerank".
func (r *Runtime) Trace(ctx context.Context, query string, topK int) (*TraceResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("trace: %w", ErrInvalidInput)
	}
	if topK <= 0 {
		return nil, fmt.Errorf("trace: topK must be positive: %w", ErrInvalidInput)
	}
	if topK > 1000 {
		topK = 1000
	}

	engine, err := r.retrievalEngine(ctx)
	if err != nil {
		return nil, fmt.Errorf("trace: %w", err)
	}

	results, t, err := engine.Trace(ctx, query, retrieval.QueryOptions{
		TopK:       topK,
		VectorTopK: 50,
		TextTopK:   50,
	})
	if err != nil {
		return nil, fmt.Errorf("trace: %w", err)
	}

	tr := &TraceResult{
		Results:  r.convertResults(ctx, results),
		Errors:   t.Errors,
		Duration: t.Duration,
	}
	for _, s := range t.Stages {
		tr.Stages = append(tr.Stages, TraceStage{
			Kind:       mapStageName(s.Name),
			InputSize:  s.InputSize,
			OutputSize: s.OutputSize,
			Duration:   s.Duration,
		})
	}
	return tr, nil
}

func (r *Runtime) convertResults(ctx context.Context, results []retrieval.SearchResult) []QueryResult {
	out := make([]QueryResult, 0, len(results))
	for _, res := range results {
		summary := r.loadSummary(ctx, res.ID)
		out = append(out, QueryResult{
			ArtifactID: res.ID.String(),
			Score:      res.Score,
			Summary:    summary,
		})
	}
	return out
}

func (r *Runtime) loadSummary(ctx context.Context, id model.ID) string {
	proj, err := r.artifactStore().LatestProjection(ctx, id)
	if err != nil {
		return ""
	}
	return proj.Summary
}

// mapStageName maps internal retrieval stage names to public names.
func mapStageName(internal string) string {
	switch internal {
	case "vector_search":
		return "dense_search"
	case "text_search":
		return "sparse_search"
	case "fusion":
		return "rerank"
	default:
		return internal
	}
}
