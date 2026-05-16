package retrieval

import (
	"context"
	"testing"

	"github.com/DotBlood/ioc/internal/model"
)

func TestBruteForce_Search(t *testing.T) {
	idx := NewBruteForceIndex(2)

	// Insert 3 entries.
	id1 := model.NewID()
	id2 := model.NewID()
	id3 := model.NewID()
	entries := []IndexEntry{
		{ID: id1, Vector: []float32{1, 0}},  // closest to query {1, 0}
		{ID: id2, Vector: []float32{0, 1}},  // orthogonal
		{ID: id3, Vector: []float32{-1, 0}}, // opposite
	}
	if err := idx.Upsert(context.Background(), entries); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Search for {1, 0}.
	results, err := idx.Search(context.Background(), []float32{1, 0}, SearchOptions{TopK: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// First result should be id1 (score ≈ 1.0).
	if results[0].ID.String() != id1.String() {
		t.Errorf("top result = %v, want %v", results[0].ID, id1)
	}

	// Last result should be id3 (score ≈ -1.0).
	if results[2].ID.String() != id3.String() {
		t.Errorf("last result = %v, want %v", results[2].ID, id3)
	}
}

func TestBruteForce_Upsert(t *testing.T) {
	idx := NewBruteForceIndex(2)

	id1 := model.NewID()
	id2 := model.NewID()

	// Insert.
	idx.Upsert(context.Background(), []IndexEntry{
		{ID: id1, Vector: []float32{1, 0}},
	})
	if idx.Count() != 1 {
		t.Errorf("Count after insert = %d, want 1", idx.Count())
	}

	// Replace same ID with different vector.
	idx.Upsert(context.Background(), []IndexEntry{
		{ID: id1, Vector: []float32{0, 1}},
		{ID: id2, Vector: []float32{1, 1}},
	})
	if idx.Count() != 2 {
		t.Errorf("Count after upsert = %d, want 2", idx.Count())
	}

	// Search for {0, 1} → both should be found (replaced id1 matches closely).
	results, _ := idx.Search(context.Background(), []float32{0, 1}, SearchOptions{TopK: 2})
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
	// id1 was replaced to {0,1} → score should be 1.0 (perfect match).
	found := false
	for _, r := range results {
		if r.ID.String() == id1.String() {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("upserted id %v not found in results %v", id1, results)
	}
}

func TestBruteForce_Delete(t *testing.T) {
	idx := NewBruteForceIndex(2)

	id1 := model.NewID()
	id2 := model.NewID()

	idx.Upsert(context.Background(), []IndexEntry{
		{ID: id1, Vector: []float32{1, 0}},
		{ID: id2, Vector: []float32{0, 1}},
	})

	// Delete one.
	idx.Delete(context.Background(), []model.ID{id1})
	if idx.Count() != 1 {
		t.Errorf("Count after delete = %d, want 1", idx.Count())
	}

	// Delete missing — should be idempotent.
	idx.Delete(context.Background(), []model.ID{id1, model.NewID()})
	if idx.Count() != 1 {
		t.Errorf("Count after idempotent delete = %d, want 1", idx.Count())
	}
}

func TestBruteForce_DimensionMismatch(t *testing.T) {
	idx := NewBruteForceIndex(2)

	err := idx.Upsert(context.Background(), []IndexEntry{
		{ID: model.NewID(), Vector: []float32{1}}, // wrong dim (1 vs 2)
	})
	if err == nil {
		t.Error("expected error for dimension mismatch")
	}
}

func TestBruteForce_CountUnderLock(t *testing.T) {
	idx := NewBruteForceIndex(1)

	// Concurrent upsert and count.
	done := make(chan struct{})
	go func() {
		idx.Upsert(context.Background(), []IndexEntry{
			{ID: model.NewID(), Vector: []float32{1}},
		})
		close(done)
	}()

	c := idx.Count()
	<-done
	if c < 0 || c > 1 {
		t.Errorf("unexpected count: %d", c)
	}
}
