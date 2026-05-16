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

// SearchOptions controls vector search behavior.
type SearchOptions struct {
	TopK     int     // must be > 0
	MinScore float64 // cosine threshold [-1, 1]. For normalized embeddings practical range [0, 1].
	// 0 = no minimum.
}

// SearchResult is a single vector retrieval result.
// Higher Score = more similar.
type SearchResult struct {
	ID    model.ID
	Score float64 // cosine similarity
}

// TextIndex is a lexical (keyword-based) search index.
type TextIndex interface {
	// Search returns top-K results for a text query.
	Search(ctx context.Context, query string, opts TextSearchOptions) ([]TextResult, error)

	// Index adds or updates documents. Full rebuild on each call (v0.1).
	Index(ctx context.Context, docs []TextDocument) error

	// Delete removes documents by ID. Idempotent.
	Delete(ctx context.Context, ids []model.ID) error

	// Count returns the number of indexed documents.
	Count() int
}

// TextDocument is a document for lexical indexing.
type TextDocument struct {
	ID      model.ID
	Content string // raw text for BM25 indexing
}

// TextSearchOptions controls text search behavior.
type TextSearchOptions struct {
	TopK int // must be > 0
}

// TextResult is a single text search result.
type TextResult struct {
	ID    model.ID
	Score float64 // BM25 score (higher = more relevant)
}

// Fusion merges results from multiple search sources.
// Implementations must be deterministic.
type Fusion interface {
	Merge(vector []SearchResult, text []TextResult, opts FusionOptions) []SearchResult
}

// FusionOptions controls merge behavior.
type FusionOptions struct {
	TopK int // final top-K after fusion
}

// QueryOptions configures a hybrid query.
type QueryOptions struct {
	TopK       int // final result count
	VectorTopK int // candidate count from vector search
	TextTopK   int // candidate count from text search
}
