package retrieval

import (
	"context"
	"errors"
	"strings"

	"github.com/DotBlood/ioc/internal/embedding"
)

// Engine is the retrieval coordinator.
// v0.1: supports hybrid search (vector + text) with pluggable fusion.
//
// TODO(v0.2):
//   - Coarse/fine hierarchy retrieval (scope → artifact)
//   - Metadata scoring (scope priority, recency, lineage)
//   - Temporal filtering via TimeMachine
type Engine struct {
	embedder Embedder
	vector   RetrievalIndex
	text     TextIndex
	fusion   Fusion
}

// Embedder is the minimal text embedding interface for retrieval.
type Embedder interface {
	Embed(ctx context.Context, texts []string) (*embedding.Batch, error)
}

// pipelineResult carries results and collected errors from a full query pipeline.
type pipelineResult struct {
	vectorResults []SearchResult
	textResults   []TextResult
	results       []SearchResult
	errors        []error
}

// NewEngine creates a new retrieval engine.
// If text index or fusion are nil, text search is disabled (vector-only mode).
func NewEngine(embedder Embedder, vector RetrievalIndex, text TextIndex, fusion Fusion) *Engine {
	return &Engine{
		embedder: embedder,
		vector:   vector,
		text:     text,
		fusion:   fusion,
	}
}

// Query performs hybrid search: vector + text → fusion.
//
// If all sources fail, returns joined errors.
// If at least one source succeeds, returns partial results (errors logged but not returned).
func (e *Engine) Query(ctx context.Context, query string, opts QueryOptions) ([]SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, embedding.ErrInvalidInput
	}

	pr := e.runPipeline(ctx, query, opts)
	if len(pr.errors) > 0 && len(pr.results) == 0 {
		return nil, errors.Join(pr.errors...)
	}
	return pr.results, nil
}

// runPipeline is the single shared execution path.
// Both Query() and Trace() use this — identical SearchResult ordering for identical inputs.
func (e *Engine) runPipeline(ctx context.Context, query string, opts QueryOptions) pipelineResult {
	var pr pipelineResult
	var err error

	// Stage 1: vector search.
	pr.vectorResults, err = e.runVectorStage(ctx, query, opts)
	if err != nil {
		pr.errors = append(pr.errors, err)
	}

	// Stage 2: text search.
	pr.textResults, err = e.runTextStage(ctx, query, opts)
	if err != nil {
		pr.errors = append(pr.errors, err)
	}

	// Stage 3: fusion.
	pr.results = e.runFusionStage(pr.vectorResults, pr.textResults, opts)

	return pr
}

// runVectorStage embeds the query and searches the vector index.
func (e *Engine) runVectorStage(ctx context.Context, query string, opts QueryOptions) ([]SearchResult, error) {
	if e.embedder == nil || e.vector == nil {
		return nil, nil
	}
	batch, err := e.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(batch.Vectors) == 0 {
		return nil, nil
	}
	return e.vector.Search(ctx, batch.Vectors[0].Data, SearchOptions{TopK: opts.VectorTopK})
}

// runTextStage searches the text index.
func (e *Engine) runTextStage(ctx context.Context, query string, opts QueryOptions) ([]TextResult, error) {
	if e.text == nil {
		return nil, nil
	}
	return e.text.Search(ctx, query, TextSearchOptions{TopK: opts.TextTopK})
}

// runFusionStage merges vector and text results.
func (e *Engine) runFusionStage(vector []SearchResult, text []TextResult, opts QueryOptions) []SearchResult {
	if e.fusion != nil {
		return e.fusion.Merge(vector, text, FusionOptions{TopK: opts.TopK})
	}
	return fallbackMerge(vector, text, opts.TopK)
}

// fallbackMerge combines results when no Fusion is configured.
func fallbackMerge(vector []SearchResult, text []TextResult, topK int) []SearchResult {
	if len(vector) > 0 {
		limit := topK
		if len(vector) < limit {
			limit = len(vector)
		}
		return vector[:limit]
	}
	if len(text) > 0 {
		limit := topK
		if len(text) < limit {
			limit = len(text)
		}
		result := make([]SearchResult, limit)
		for i := 0; i < limit; i++ {
			result[i] = SearchResult{ID: text[i].ID, Score: text[i].Score}
		}
		return result
	}
	return nil
}
