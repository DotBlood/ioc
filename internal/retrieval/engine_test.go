package retrieval

import (
	"context"
	"strings"
	"testing"

	"github.com/DotBlood/ioc/internal/embedding"
	"github.com/DotBlood/ioc/internal/model"
)

// testEmbedder is a simple deterministic embedder for tests.
type testEmbedder struct {
	dims int
}

func (e *testEmbedder) Embed(_ context.Context, texts []string) (*embedding.Batch, error) {
	if len(texts) == 0 {
		return nil, embedding.ErrInvalidInput
	}
	if strings.TrimSpace(texts[0]) == "" {
		return nil, embedding.ErrInvalidInput
	}
	// Simple deterministic embedding: each text gets a normalized unit vector
	// where position depends on the first character's hash-like derivation.
	vec := make([]float32, e.dims)
	for i := range vec {
		if i < len(texts[0]) {
			vec[i] = 1.0 / float32(len(texts[0]))
		}
	}
	// Normalize.
	var sumSq float64
	for _, v := range vec {
		sumSq += float64(v) * float64(v)
	}
	if sumSq > 0 {
		inv := float32(1.0 / sqrt(float64(sumSq)))
		for i := range vec {
			vec[i] *= inv
		}
	}
	return &embedding.Batch{
		Vectors: []embedding.Vector{{Data: vec}},
	}, nil
}

func sqrt(f float64) float64 {
	// avoid math import for simple test
	if f <= 0 {
		return 0
	}
	s := f
	for i := 0; i < 10; i++ {
		s = (s + f/s) / 2
	}
	return s
}

func TestEngine_Query(t *testing.T) {
	idx := NewBruteForceIndex(4)
	emb := &testEmbedder{dims: 4}

	// Add some index entries.
	idx.Upsert(context.Background(), []IndexEntry{
		{ID: model.NewID(), Vector: []float32{1, 0, 0, 0}},
		{ID: model.NewID(), Vector: []float32{0, 1, 0, 0}},
	})

	engine := NewEngine(emb, idx)
	results, err := engine.Query(context.Background(), "test query", SearchOptions{TopK: 2})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestEngine_EmptyIndex(t *testing.T) {
	idx := NewBruteForceIndex(4)
	emb := &testEmbedder{dims: 4}

	engine := NewEngine(emb, idx)
	results, err := engine.Query(context.Background(), "test", SearchOptions{TopK: 2})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestEngine_EmptyQuery(t *testing.T) {
	idx := NewBruteForceIndex(4)
	emb := &testEmbedder{dims: 4}

	engine := NewEngine(emb, idx)
	_, err := engine.Query(context.Background(), "", SearchOptions{TopK: 2})
	if err == nil {
		t.Error("expected error for empty query")
	}

	_, err = engine.Query(context.Background(), "   ", SearchOptions{TopK: 2})
	if err == nil {
		t.Error("expected error for whitespace-only query")
	}
}

func TestEngine_SearchOrdering(t *testing.T) {
	idx := NewBruteForceIndex(2)
	emb := &testEmbedder{dims: 2}

	idA := model.NewID()
	idB := model.NewID()

	// Ensure deterministic ordering: if scores equal, lexically earlier ID wins.
	// We create IDs where idA < idB lexically for the test.
	for idA.String() >= idB.String() {
		idA = model.NewID()
		idB = model.NewID()
	}

	idx.Upsert(context.Background(), []IndexEntry{
		{ID: idA, Vector: []float32{1, 0}},
		{ID: idB, Vector: []float32{1, 0}}, // same vector → tie-break by ID
	})

	engine := NewEngine(emb, idx)
	results, _ := engine.Query(context.Background(), "a", SearchOptions{TopK: 2})
	if len(results) != 2 {
		t.Fatalf("expected 2 results")
	}
	if results[0].ID.String() != idA.String() {
		t.Errorf("tie-break: expected %v first, got %v", idA, results[0].ID)
	}
}
