package retrieval

import (
	"context"
	"strings"

	"github.com/DotBlood/ioc/internal/embedding"
)

// Engine is the top-level retrieval coordinator.
// v0.1: minimal — embed query, index search, return results.
//
// TODO(v0.2):
//   - Coarse/fine hierarchy retrieval (scope → artifact)
//   - Metadata scoring (scope priority, recency, lineage)
//   - Reranking with exact cosine
//   - Temporal filtering via TimeMachine
//   - Scope filtering (include archived, etc.)
//   - Token budget assembly via ContextAssembler
type Engine struct {
	embedder Embedder
	index    RetrievalIndex
}

// Embedder is the minimal text embedding interface for retrieval.
type Embedder interface {
	Embed(ctx context.Context, texts []string) (*embedding.Batch, error)
}

// NewEngine creates a new retrieval engine.
func NewEngine(embedder Embedder, index RetrievalIndex) *Engine {
	return &Engine{
		embedder: embedder,
		index:    index,
	}
}

// Query embeds the query text and searches the index.
// Returns top-K results sorted by Score descending (index ordering).
//
// TODO(v0.2): Accept model.RetrievalOpts for scope/temporal/archived filters.
func (e *Engine) Query(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, embedding.ErrInvalidInput
	}

	batch, err := e.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}

	results, err := e.index.Search(ctx, batch.Vectors[0].Data, opts)
	if err != nil {
		return nil, err
	}

	return results, nil
}
