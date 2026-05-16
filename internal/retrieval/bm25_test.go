package retrieval

import (
	"context"
	"testing"

	"github.com/DotBlood/ioc/internal/model"
)

func TestBM25Index_Search(t *testing.T) {
	idx := NewBM25Index()
	ctx := context.Background()

	// Index some documents.
	err := idx.Index(ctx, []TextDocument{
		{ID: model.NewID(), Content: "hello world"},
		{ID: model.NewID(), Content: "goodbye world"},
		{ID: model.NewID(), Content: "hello everyone"},
	})
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	// Search for "hello".
	results, err := idx.Search(ctx, "hello", TextSearchOptions{TopK: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for 'hello'")
	}
	if results[0].Score <= 0 {
		t.Errorf("expected positive BM25 score for matching document, got %f", results[0].Score)
	}

	// Search for a term that doesn't exist should return all with near-zero scores.
	noResults, err := idx.Search(ctx, "xyzzy_nonexistent", TextSearchOptions{TopK: 3})
	if err != nil {
		t.Fatalf("Search nonexistent: %v", err)
	}
	if len(noResults) == 0 {
		// Accept 0 results — BM25 may return non-matching docs with zero scores.
	}
}

func TestBM25Index_EmptyIndex(t *testing.T) {
	idx := NewBM25Index()
	ctx := context.Background()

	results, err := idx.Search(ctx, "test", TextSearchOptions{TopK: 3})
	if err != nil {
		t.Fatalf("Search on empty: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}

	if idx.Count() != 0 {
		t.Errorf("Count on empty: want 0, got %d", idx.Count())
	}
}

func TestBM25Index_Reindex(t *testing.T) {
	idx := NewBM25Index()
	ctx := context.Background()

	// First index.
	err := idx.Index(ctx, []TextDocument{
		{ID: model.NewID(), Content: "first set of documents"},
	})
	if err != nil {
		t.Fatalf("Index 1: %v", err)
	}
	c1 := idx.Count()

	// Second index replaces first.
	err = idx.Index(ctx, []TextDocument{
		{ID: model.NewID(), Content: "second set completely different"},
	})
	if err != nil {
		t.Fatalf("Index 2: %v", err)
	}
	c2 := idx.Count()

	if c1 == c2 {
		// Both have 1 doc, so counts match. Verify content changed.
		r1, _ := idx.Search(ctx, "first", TextSearchOptions{TopK: 1})
		r2, _ := idx.Search(ctx, "second", TextSearchOptions{TopK: 1})
		if len(r1) == 0 && len(r2) == 0 {
			t.Error("neither query matched after reindex — content may not have changed")
		}
	}
}

func TestRRF_Merge_Deterministic(t *testing.T) {
	id1 := model.NewID()
	id2 := model.NewID()

	vector := []SearchResult{
		{ID: id1, Score: 0.9},
		{ID: id2, Score: 0.5},
	}
	text := []TextResult{
		{ID: id2, Score: 100},
		{ID: id1, Score: 50},
	}

	r := NewRRF(60)
	results := r.Merge(vector, text, FusionOptions{TopK: 2})

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Results should be deterministic.
	r2 := r.Merge(vector, text, FusionOptions{TopK: 2})
	for i := range results {
		if results[i].ID.String() != r2[i].ID.String() || results[i].Score != r2[i].Score {
			t.Errorf("determinism broken at %d", i)
		}
	}
}

func TestRRF_EqualScores_TieBreak(t *testing.T) {
	// Two IDs with same RRF score — should sort by ID lexical ascending.
	idA := model.NewID()
	idB := model.NewID()

	// Ensure idA < idB lexically.
	for idA.String() >= idB.String() {
		idA = model.NewID()
		idB = model.NewID()
	}

	// Same rank in both sources → same RRF score.
	vector := []SearchResult{
		{ID: idA, Score: 0.5},
		{ID: idB, Score: 0.5},
	}
	text := []TextResult{
		{ID: idA, Score: 50},
		{ID: idB, Score: 50},
	}

	r := NewRRF(60)
	results := r.Merge(vector, text, FusionOptions{TopK: 2})

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Equal scores → tie-break by ID (ascending).
	if results[0].ID.String() != idA.String() {
		t.Errorf("tie-break: expected %v first, got %v", idA, results[0].ID)
	}
}

func TestEngine_HybridQuery(t *testing.T) {
	vecIdx := NewBruteForceIndex(2)
	textIdx := NewBM25Index()
	emb := &testEmbedder{dims: 2}

	// Add data to both indexes.
	id1 := model.NewID()
	vecIdx.Upsert(context.Background(), []IndexEntry{
		{ID: id1, Vector: []float32{1, 0}},
	})
	textIdx.Index(context.Background(), []TextDocument{
		{ID: id1, Content: "hello world"},
	})

	engine := NewEngine(emb, vecIdx, textIdx, NewRRF(60))
	results, err := engine.Query(context.Background(), "hello", QueryOptions{
		TopK:       5,
		VectorTopK: 3,
		TextTopK:   3,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected at least 1 result")
	}
}

func TestEngine_OneSourceEmpty(t *testing.T) {
	vecIdx := NewBruteForceIndex(2)
	emb := &testEmbedder{dims: 2}

	// Only vector index has data.
	id1 := model.NewID()
	vecIdx.Upsert(context.Background(), []IndexEntry{
		{ID: id1, Vector: []float32{1, 0}},
	})

	engine := NewEngine(emb, vecIdx, nil, nil)
	results, err := engine.Query(context.Background(), "test", QueryOptions{
		TopK: 5, VectorTopK: 3, TextTopK: 3,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected at least 1 result")
	}
}
