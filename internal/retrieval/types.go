// Package retrieval implements the vector search and retrieval pipeline.
//
// v0.1: BruteForceIndex (linear scan). No hierarchy retrieval, no metadata scoring.
// TODO(v0.2): ANN implementations, coarse→fine hierarchy, metadata scoring, reranking.
package retrieval

import (
	"context"

	"github.com/DotBlood/ioc/internal/model"
)

// RetrievalIndex is a pluggable vector search index.
// v0.1 default implementation: BruteForceIndex (linear scan).
// TODO(v0.2): ANN implementations (HNSW, mmap-backed, quantized).
//
// Retrieval semantics MUST be identical across implementations.
// ANN affects performance only, not ranking correctness guarantees.
type RetrievalIndex interface {
	// Search returns top-K results for a query vector.
	// Results are sorted by Score descending, then ID lexical ascending (tie-break).
	Search(ctx context.Context, query []float32, opts SearchOptions) ([]SearchResult, error)

	// Upsert adds or updates entries in the index.
	Upsert(ctx context.Context, entries []IndexEntry) error

	// Delete removes entries by ID. Idempotent — missing IDs are ignored.
	Delete(ctx context.Context, ids []model.ID) error

	// Dimension returns the vector dimension.
	Dimension() int

	// Count returns the number of indexed vectors.
	Count() int
}

// IndexEntry is a single vector entry in the index.
// Index layer does NOT know about scopes, revisions, or projections.
//
// TODO(v0.2): Add VectorRef when mmap/vector-store split is stable.
type IndexEntry struct {
	ID     model.ID
	Vector []float32 // pre-normalized; dimension must match index dimension
}

// SearchOptions controls search behavior.
type SearchOptions struct {
	TopK     int     // must be > 0
	MinScore float64 // cosine threshold [-1, 1]. For normalized embeddings practical range [0, 1].
	                 // 0 = no minimum.
}

// SearchResult is a single retrieval result.
// Higher Score = more similar.
type SearchResult struct {
	ID    model.ID
	Score float64 // cosine similarity
}
