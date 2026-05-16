package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/DotBlood/ioc/internal/embedding"
	"github.com/DotBlood/ioc/internal/model"
	"github.com/DotBlood/ioc/internal/retrieval"
	"github.com/DotBlood/ioc/internal/store"
)

// ============================================================
// Test doubles
// ============================================================

type mockOwnershipWriter struct {
	mu       sync.Mutex
	parents  []model.ID
	children []model.ID
}

func (m *mockOwnershipWriter) AddOwnership(_ context.Context, parent, child model.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.parents = append(m.parents, parent)
	m.children = append(m.children, child)
	return nil
}

func (m *mockOwnershipWriter) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.parents)
}

type mockScopeResolver struct {
	nodeID model.ID
	err    error
}

func (m *mockScopeResolver) ResolveScope(_ context.Context, _ model.ScopeID) (model.ID, error) {
	return m.nodeID, m.err
}

// ============================================================
// Test helpers
// ============================================================

func setupPipelineTest(t *testing.T) (*IngestionPipeline, *mockOwnershipWriter, *store.CAS, *store.EmbeddingStore, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "ioc-pipeline-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	cleanup := func() {
		os.RemoveAll(dir)
	}

	cas := store.NewCAS(filepath.Join(dir, "cas"))

	ds, err := store.OpenOrCreate(filepath.Join(dir, "test.db"))
	if err != nil {
		cleanup()
		t.Fatalf("OpenOrCreate: %v", err)
	}
	artifactStore := store.NewArtifactStore(ds)

	embStore, err := store.OpenEmbeddingStore(filepath.Join(dir, "emb.bin"), 384)
	if err != nil {
		ds.Close()
		cleanup()
		t.Fatalf("OpenEmbeddingStore: %v", err)
	}

	embCfg := embedding.DefaultMockConfig()
	embCfg.Dimension = 384
	embedder := embedding.NewMockEmbedder(embCfg)

	bm25 := retrieval.NewBM25Index()

	ownership := &mockOwnershipWriter{}
	scopes := &mockScopeResolver{nodeID: model.NewID()}

	p := NewIngestionPipelineFromStore(
		cas, artifactStore, embStore, ownership, scopes, embedder, bm25,
	)

	cleanupInner := func() {
		embStore.Close()
		ds.Close()
		cleanup()
	}

	return p, ownership, cas, embStore, cleanupInner
}

func TestPipeline_IngestionRoundtrip(t *testing.T) {
	p, ownership, _, _, cleanup := setupPipelineTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("hello ioc world")
	scopeID := model.ScopeID("test:scope")
	summary := "test artifact"

	id, err := p.Process(ctx, content, scopeID, summary)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if id.IsZero() {
		t.Fatal("Process returned zero ID")
	}

	// Ownership edge was created.
	if ownership.callCount() != 1 {
		t.Errorf("ownership call count = %d, want 1", ownership.callCount())
	}

	// Artifact exists in store.
	art, err := p.artifacts.LoadArtifact(ctx, id)
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if art.Scope != scopeID {
		t.Errorf("artifact scope = %q, want %q", art.Scope, scopeID)
	}
	if art.NodeType != model.NodeTypeArtifact {
		t.Errorf("artifact type = %v, want %v", art.NodeType, model.NodeTypeArtifact)
	}

	// Projection exists with all readiness flags.
	proj, err := p.artifacts.LatestProjection(ctx, id)
	if err != nil {
		t.Fatalf("LatestProjection: %v", err)
	}
	if !proj.Readiness.Has(model.ReadinessStored) {
		t.Error("projection missing ReadinessStored")
	}
	if !proj.Readiness.Has(model.ReadinessEmbedded) {
		t.Error("projection missing ReadinessEmbedded")
	}
	if !proj.Readiness.Has(model.ReadinessIndexed) {
		t.Error("projection missing ReadinessIndexed")
	}
	if proj.Summary != summary {
		t.Errorf("projection summary = %q, want %q", proj.Summary, summary)
	}
	if proj.EmbeddingRef == 0 {
		t.Error("projection EmbeddingRef is zero")
	}
	if proj.ModelVersion == "" {
		t.Error("projection ModelVersion is empty")
	}
}

func TestPipeline_CASDedup(t *testing.T) {
	p, _, cas, _, cleanup := setupPipelineTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("deduplicated content")
	scopeID := model.ScopeID("test:dedup")

	id1, err := p.Process(ctx, content, scopeID, "first")
	if err != nil {
		t.Fatalf("Process 1: %v", err)
	}

	id2, err := p.Process(ctx, content, scopeID, "second")
	if err != nil {
		t.Fatalf("Process 2: %v", err)
	}

	// Different artifact IDs (ULIDs are unique).
	if id1 == id2 {
		t.Error("expected different artifact IDs for repeated ingestion")
	}

	// Both artifacts have the same content hash (CAS dedup).
	art1, _ := p.artifacts.LoadArtifact(ctx, id1)
	art2, _ := p.artifacts.LoadArtifact(ctx, id2)
	if art1.ContentHash != art2.ContentHash {
		t.Error("expected same ContentHash for identical content")
	}

	// CAS has the blob.
	has, err := cas.Has(ctx, art1.ContentHash)
	if err != nil {
		t.Fatalf("CAS.Has: %v", err)
	}
	if !has {
		t.Error("CAS should have the stored blob")
	}
}

func TestPipeline_EmbeddingStored(t *testing.T) {
	p, _, _, embStore, cleanup := setupPipelineTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("embeddable content")

	id, err := p.Process(ctx, content, "test:emb", "embedding test")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	// EmbeddingStore has exactly 1 entry.
	if embStore.Len() != 1 {
		t.Errorf("EmbeddingStore.Len() = %d, want 1", embStore.Len())
	}

	// Projection has a non-zero EmbeddingRef.
	proj, err := p.artifacts.LatestProjection(ctx, id)
	if err != nil {
		t.Fatalf("LatestProjection: %v", err)
	}
	if proj.EmbeddingRef == 0 {
		t.Error("projection EmbeddingRef should be non-zero")
	}

	// Can read the embedding back.
	vec, err := embStore.Get(proj.EmbeddingRef)
	if err != nil {
		t.Fatalf("EmbeddingStore.Get: %v", err)
	}
	if len(vec) != 384 {
		t.Errorf("embedding dims = %d, want 384", len(vec))
	}
}

func TestPipeline_BM25Indexed(t *testing.T) {
	p, _, _, _, cleanup := setupPipelineTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("the quick brown fox jumps over the lazy dog")
	scopeID := model.ScopeID("test:bm25")

	id, err := p.Process(ctx, content, scopeID, "bm25 test")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	// BM25 search returns the ingested artifact.
	results, err := p.bm25.Search(ctx, "fox dog", retrieval.TextSearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("BM25.Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("BM25 search returned no results")
	}

	found := false
	for _, r := range results {
		if r.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("BM25 results do not contain artifact %s: %+v", id, results)
	}
}

func TestPipeline_EmptyContent(t *testing.T) {
	p, _, _, _, cleanup := setupPipelineTest(t)
	defer cleanup()

	ctx := context.Background()
	content := []byte("")
	scopeID := model.ScopeID("test:empty")

	id, err := p.Process(ctx, content, scopeID, "")
	if err != nil {
		t.Fatalf("Process empty content: %v", err)
	}
	if id.IsZero() {
		t.Fatal("Process returned zero ID for empty content")
	}

	proj, err := p.artifacts.LatestProjection(ctx, id)
	if err != nil {
		t.Fatalf("LatestProjection: %v", err)
	}
	if !proj.Readiness.Has(model.ReadinessStored) {
		t.Error("empty projection missing ReadinessStored")
	}
	if !proj.Readiness.Has(model.ReadinessEmbedded) {
		t.Error("empty projection missing ReadinessEmbedded")
	}
	if !proj.Readiness.Has(model.ReadinessIndexed) {
		t.Error("empty projection missing ReadinessIndexed")
	}
}


