package retrieval

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/DotBlood/ioc/internal/embedding"
	"github.com/DotBlood/ioc/internal/model"
)

// Trace performs a query with full trace collection.
//
// Trace collection MUST NOT affect retrieval semantics.
// Query() and Trace() over identical inputs MUST return identical SearchResult ordering.
//
// Stage order is deterministic:
//   1. vector_search — query embedding + vector index search
//   2. text_search   — BM25 keyword search
//   3. fusion        — RRF merge of vector + text results
func (e *Engine) Trace(ctx context.Context, query string, opts QueryOptions) ([]SearchResult, *model.RetrievalTrace, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil, embedding.ErrInvalidInput
	}

	trace := &model.RetrievalTrace{
		QueryHash: newContentHash(query),
	}
	start := time.Now()

	// Stage 1: vector_search.
	stageStart := time.Now()
	vectorResults, vecErr := e.runVectorStage(ctx, query, opts)
	if vecErr != nil {
		trace.Errors = append(trace.Errors, fmt.Sprintf("vector_search: %v", vecErr))
	}
	trace.Stages = append(trace.Stages, model.RetrievalStage{
		Name: "vector_search", InputSize: 1, OutputSize: len(vectorResults),
		Duration: time.Since(stageStart),
	})

	// Stage 2: text_search.
	stageStart = time.Now()
	textResults, txtErr := e.runTextStage(ctx, query, opts)
	if txtErr != nil {
		trace.Errors = append(trace.Errors, fmt.Sprintf("text_search: %v", txtErr))
	}
	trace.Stages = append(trace.Stages, model.RetrievalStage{
		Name: "text_search", InputSize: 1, OutputSize: len(textResults),
		Duration: time.Since(stageStart),
	})

	// Stage 3: fusion.
	fusionInputSize := len(vectorResults) + len(textResults)
	stageStart = time.Now()
	results := e.runFusionStage(vectorResults, textResults, opts)
	trace.Stages = append(trace.Stages, model.RetrievalStage{
		Name: "fusion", InputSize: fusionInputSize, OutputSize: len(results),
		Duration: time.Since(stageStart),
	})

	// FinalSelection preserves the final fused ranking order.
	// The order matches the returned []SearchResult exactly.
	for _, r := range results {
		trace.FinalSelection = append(trace.FinalSelection, r.ID)
	}
	trace.Duration = time.Since(start)

	return results, trace, nil
}

// newContentHash creates a deterministic content hash from a query string.
func newContentHash(query string) model.ContentHash {
	h := sha256.Sum256([]byte(query))
	return model.ContentHash(h)
}
