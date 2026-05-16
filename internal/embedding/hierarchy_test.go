package embedding

import (
	"context"
	"sort"
	"testing"

	"github.com/DotBlood/ioc/internal/model"
)

// mockScopeReader implements ScopeHierarchy for testing.
type mockScopeReader struct {
	children    map[model.ScopeID][]model.ScopeID
	artifacts   map[model.ScopeID][]model.ID
}

func newMockScopeReader() *mockScopeReader {
	return &mockScopeReader{
		children:  make(map[model.ScopeID][]model.ScopeID),
		artifacts: make(map[model.ScopeID][]model.ID),
	}
}

func (m *mockScopeReader) Children(_ context.Context, parent model.ScopeID) ([]model.ScopeID, error) {
	return m.children[parent], nil
}

func (m *mockScopeReader) ArtifactsInScope(_ context.Context, scopeID model.ScopeID) ([]model.ID, error) {
	return m.artifacts[scopeID], nil
}

// mockEmbeddingReader implements EmbeddingReader for testing.
type mockEmbeddingReader struct {
	embeddings map[model.EmbeddingRefID][]float32
}

func newMockEmbeddingReader() *mockEmbeddingReader {
	return &mockEmbeddingReader{
		embeddings: make(map[model.EmbeddingRefID][]float32),
	}
}

func (m *mockEmbeddingReader) Get(ref model.EmbeddingRefID) ([]float32, error) {
	v, ok := m.embeddings[ref]
	if !ok {
		return nil, model.ErrNotFound
	}
	return v, nil
}

// mockProjectionLoader implements ProjectionLoader for testing.
type mockProjectionLoader struct {
	projections map[model.ID]*model.ArtifactProjection
}

func newMockProjectionLoader() *mockProjectionLoader {
	return &mockProjectionLoader{
		projections: make(map[model.ID]*model.ArtifactProjection),
	}
}

func (m *mockProjectionLoader) LatestProjection(_ context.Context, artifactID model.ID) (*model.ArtifactProjection, error) {
	p, ok := m.projections[artifactID]
	if !ok {
		return nil, model.ErrNotFound
	}
	return p, nil
}

func TestHierarchy_ComputeSession(t *testing.T) {
	scope := newMockScopeReader()
	emb := newMockEmbeddingReader()
	proj := newMockProjectionLoader()

	// Setup: workspace → session1, session2
	scope.children["ws:test"] = []model.ScopeID{"ws:test:s1", "ws:test:s2"}

	// Session s1 has 2 artifacts.
	art1 := model.NewID()
	art2 := model.NewID()
	scope.artifacts["ws:test:s1"] = []model.ID{art1, art2}

	// Embeddings for both artifacts.
	emb.embeddings[1] = []float32{1, 0, 0, 0}
	emb.embeddings[2] = []float32{0, 1, 0, 0}

	// Projections.
	proj.projections[art1] = &model.ArtifactProjection{
		ArtifactID:   art1,
		EmbeddingRef: 1,
		Summary:      "artifact one",
	}
	proj.projections[art2] = &model.ArtifactProjection{
		ArtifactID:   art2,
		EmbeddingRef: 2,
		Summary:      "artifact two with longer text",
	}

	// Session s2 has no artifacts.
	scope.artifacts["ws:test:s2"] = nil

	h := NewHierarchy(scope, emb, proj, "test-model", 4)
	results, err := h.ComputeAggregate(context.Background(), "ws:test")
	if err != nil {
		t.Fatalf("ComputeAggregate: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 session result, got %d", len(results))
	}

	if results[0].ParentScope != "ws:test:s1" {
		t.Errorf("ParentScope = %q, want %q", results[0].ParentScope, "ws:test:s1")
	}
	if results[0].Level != LevelSession {
		t.Errorf("Level = %d, want %d", results[0].Level, LevelSession)
	}
	if results[0].ChildCount != 2 {
		t.Errorf("ChildCount = %d, want 2", results[0].ChildCount)
	}
	if results[0].Dimension != 4 {
		t.Errorf("Dimension = %d, want 4", results[0].Dimension)
	}
	if results[0].Model != "test-model" {
		t.Errorf("Model = %q, want %q", results[0].Model, "test-model")
	}
}

func TestHierarchy_Determinism(t *testing.T) {
	scope := newMockScopeReader()
	emb := newMockEmbeddingReader()
	proj := newMockProjectionLoader()

	scope.children["root"] = []model.ScopeID{"root:b", "root:a"} // deliberately out of order
	art1 := model.NewID()
	art2 := model.NewID()
	scope.artifacts["root:a"] = []model.ID{art2, art1} // deliberately out of order
	scope.artifacts["root:b"] = []model.ID{art1, art2}

	emb.embeddings[1] = []float32{1, 0}
	emb.embeddings[2] = []float32{0, 1}

	for _, art := range []model.ID{art1, art2} {
		proj.projections[art] = &model.ArtifactProjection{
			ArtifactID:   art,
			EmbeddingRef: map[model.ID]model.EmbeddingRefID{
				art1: 1,
				art2: 2,
			}[art],
			Summary: "test",
		}
	}

	h := NewHierarchy(scope, emb, proj, "m", 2)
	r1, _ := h.ComputeAggregate(context.Background(), "root")
	r2, _ := h.ComputeAggregate(context.Background(), "root")

	if len(r1) != len(r2) {
		t.Fatalf("different result counts: %d vs %d", len(r1), len(r2))
	}

	// Sort results by ParentScope for comparison (should already be sorted).
	sort.Slice(r1, func(i, j int) bool { return string(r1[i].ParentScope) < string(r1[j].ParentScope) })
	sort.Slice(r2, func(i, j int) bool { return string(r2[i].ParentScope) < string(r2[j].ParentScope) })

	for i := range r1 {
		if r1[i].ParentScope != r2[i].ParentScope {
			t.Errorf("scope mismatch at %d", i)
		}
		for j := range r1[i].Vector {
			if r1[i].Vector[j] != r2[i].Vector[j] {
				t.Errorf("determinism broken at result %d, index %d: %f vs %f",
					i, j, r1[i].Vector[j], r2[i].Vector[j])
			}
		}
	}
}

func TestHierarchy_EmptyScope(t *testing.T) {
	scope := newMockScopeReader()
	emb := newMockEmbeddingReader()
	proj := newMockProjectionLoader()

	scope.children["empty"] = []model.ScopeID{"empty:s1"}
	scope.artifacts["empty:s1"] = nil

	h := NewHierarchy(scope, emb, proj, "m", 384)
	results, err := h.ComputeAggregate(context.Background(), "empty")
	if err != nil {
		t.Fatalf("ComputeAggregate: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty scope, got %d", len(results))
	}
}
