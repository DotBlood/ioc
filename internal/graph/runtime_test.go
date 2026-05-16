package graph

import (
	"context"
	"fmt"
	"testing"

	"github.com/DotBlood/ioc/internal/model"
)

// ============================================================
// Bloom filter unit tests
// ============================================================

func TestBloomFilter_AddHas(t *testing.T) {
	bf := newBloomFilter(100, 0.01)
	id := model.NewID()
	bf.Add(id)
	if !bf.Has(id) {
		t.Error("added element should be found")
	}
}

func TestBloomFilter_NotAdded(t *testing.T) {
	bf := newBloomFilter(100, 0.01)
	if bf.Has(model.NewID()) {
		t.Error("non-added element should not be found (very unlikely false positive)")
	}
}

func TestBloomFilter_NoFalseNegatives(t *testing.T) {
	bf := newBloomFilter(10_000, 0.01)
	added := make([]model.ID, 1000)
	for i := range added {
		added[i] = model.NewID()
		bf.Add(added[i])
	}
	for _, id := range added {
		if !bf.Has(id) {
			t.Errorf("false negative: added element %s not found", id)
		}
	}
}

func TestBloomFilter_FalsePositiveRate(t *testing.T) {
	bf := newBloomFilter(10_000, 0.015)
	added := make(map[model.ID]bool, 10_000)
	for i := 0; i < 10_000; i++ {
		id := model.NewID()
		added[id] = true
		bf.Add(id)
	}
	falsePositives := 0
	trials := 10_000
	for i := 0; i < trials; i++ {
		id := model.NewID()
		if bf.Has(id) && !added[id] {
			falsePositives++
		}
	}
	fpr := float64(falsePositives) / float64(trials)
	expected := 0.015
	if fpr > expected*3 {
		t.Errorf("FPR too high: %.4f (expected < %.4f)", fpr, expected*3)
	}
}

func TestBloomFilter_Reset(t *testing.T) {
	bf := newBloomFilter(100, 0.01)
	id := model.NewID()
	bf.Add(id)
	bf.Reset()
	if bf.Has(id) {
		t.Error("element should not be found after reset")
	}
}

func TestBloomFilter_ZeroConfig(t *testing.T) {
	t.Run("zero items uses minimum", func(t *testing.T) {
		bf := newBloomFilter(0, 0.01)
		if bf.size < 64 {
			t.Error("bloom filter with zero expected items should have minimum size")
		}
	})
	t.Run("zero fpr uses default", func(t *testing.T) {
		bf := newBloomFilter(100, 0)
		if bf.hashes < 1 {
			t.Error("bloom filter with zero fpr should have at least 1 hash")
		}
	})
}

// ============================================================
// Property index integration tests
// ============================================================

func TestPropertyIndex_Add(t *testing.T) {
	ctx := context.Background()
	g := NewStatefulGraph()

	art := &model.Artifact{
		ArtifactID: model.NewID(),
		NodeType:   model.NodeTypeArtifact,
		Scope:      "test:scope",
	}
	if err := g.AddNode(ctx, art); err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	ids := g.PropertyIndexFind("scope", "test:scope")
	if len(ids) != 1 {
		t.Fatalf("expected 1 result, got %d", len(ids))
	}
	if ids[0] != art.ArtifactID {
		t.Errorf("wrong ID returned")
	}
}

func TestPropertyIndex_Remove(t *testing.T) {
	ctx := context.Background()
	g := NewStatefulGraph()

	art := &model.Artifact{
		ArtifactID: model.NewID(),
		NodeType:   model.NodeTypeArtifact,
		Scope:      "removable",
	}
	g.AddNode(ctx, art)
	g.RemoveNode(ctx, art.ArtifactID)

	ids := g.PropertyIndexFind("scope", "removable")
	if len(ids) != 0 {
		t.Errorf("expected 0 results after remove, got %d", len(ids))
	}
}

func TestPropertyIndex_MultipleValues(t *testing.T) {
	ctx := context.Background()
	g := NewStatefulGraph()

	for i := 0; i < 5; i++ {
		art := &model.Artifact{
			ArtifactID: model.NewID(),
			NodeType:   model.NodeTypeArtifact,
			Scope:      "shared:scope",
		}
		g.AddNode(ctx, art)
	}

	ids := g.PropertyIndexFind("scope", "shared:scope")
	if len(ids) != 5 {
		t.Errorf("expected 5 results, got %d", len(ids))
	}
}

func TestPropertyIndex_Keys(t *testing.T) {
	ctx := context.Background()
	g := NewStatefulGraph()

	scopes := []string{"s1", "s2", "s3"}
	for _, s := range scopes {
		g.AddNode(ctx, &model.Artifact{
			ArtifactID: model.NewID(),
			NodeType:   model.NodeTypeArtifact,
			Scope:      model.ScopeID(s),
		})
	}

	keys := g.PropertyIndexKeys("scope")
	if len(keys) != 3 {
		t.Errorf("expected 3 keys, got %d", len(keys))
	}
}

func TestPropertyIndex_HighCardinality(t *testing.T) {
	ctx := context.Background()
	g := NewStatefulGraph()

	const n = 1000
	for i := 0; i < n; i++ {
		g.AddNode(ctx, &model.Artifact{
			ArtifactID: model.NewID(),
			NodeType:   model.NodeTypeArtifact,
			Scope:      model.ScopeID(fmt.Sprintf("scope:%d", i)),
		})
	}

	keys := g.PropertyIndexKeys("scope")
	if len(keys) != n {
		t.Errorf("expected %d keys, got %d", n, len(keys))
	}

	for i := 0; i < n; i++ {
		ids := g.PropertyIndexFind("scope", fmt.Sprintf("scope:%d", i))
		if len(ids) != 1 {
			t.Errorf("scope:%d: expected 1 result, got %d", i, len(ids))
		}
	}
}

// ============================================================
// ProbablyHas integration tests
// ============================================================

func TestProbablyHas_Added(t *testing.T) {
	ctx := context.Background()
	g := NewStatefulGraph()

	art := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}
	g.AddNode(ctx, art)

	if !g.ProbablyHas(ctx, art.ArtifactID) {
		t.Error("ProbablyHas should return true for added node")
	}
}

func TestProbablyHas_NotAdded(t *testing.T) {
	ctx := context.Background()
	g := NewStatefulGraph()

	if g.ProbablyHas(ctx, model.NewID()) {
		t.Error("ProbablyHas should return false for non-added node")
	}
}

func TestProbablyHas_AfterRemove(t *testing.T) {
	ctx := context.Background()
	g := NewStatefulGraph()

	art := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}
	g.AddNode(ctx, art)
	g.RemoveNode(ctx, art.ArtifactID)

	// Bloom filter does NOT support deletion, so ProbablyHas may still return true.
	// This is expected — bloom is a hint, not source of truth.
	_ = g.ProbablyHas(ctx, art.ArtifactID)
}

// ============================================================
// RebuildRuntimeState integration tests
// ============================================================

func TestRebuildRuntimeState(t *testing.T) {
	g := NewStatefulGraph()

	n1 := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact, Scope: "s1"}
	n2 := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact, Scope: "s2"}
	g.nodes[n1.ArtifactID] = n1
	g.nodes[n2.ArtifactID] = n2

	g.RebuildRuntimeState()

	if !g.ProbablyHas(context.Background(), n1.ArtifactID) {
		t.Error("ProbablyHas should find n1 after rebuild")
	}
	if !g.ProbablyHas(context.Background(), n2.ArtifactID) {
		t.Error("ProbablyHas should find n2 after rebuild")
	}
	if g.ProbablyHas(context.Background(), model.NewID()) {
		t.Error("ProbablyHas should not find non-existent ID")
	}

	ids := g.PropertyIndexFind("scope", "s1")
	if len(ids) != 1 || ids[0] != n1.ArtifactID {
		t.Errorf("PropertyIndexFind after rebuild: expected [%v], got %v", n1.ArtifactID, ids)
	}
}

// ============================================================
// NodesByType via property index
// ============================================================

func TestNodesByTypeViaPropertyIndex(t *testing.T) {
	ctx := context.Background()
	g := NewStatefulGraph()

	w := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeWorktree}
	s := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeSession}
	a := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}
	for _, n := range []*model.Artifact{w, s, a} {
		g.AddNode(ctx, n)
	}

	arts, _ := g.NodesByType(ctx, model.NodeTypeArtifact)
	if len(arts) != 1 || arts[0] != a.ArtifactID {
		t.Errorf("NodesByType(artifact) = %v, want [%v]", arts, a.ArtifactID)
	}

	sessions, _ := g.NodesByType(ctx, model.NodeTypeSession)
	if len(sessions) != 1 || sessions[0] != s.ArtifactID {
		t.Errorf("NodesByType(session) = %v, want [%v]", sessions, s.ArtifactID)
	}
}

// ============================================================
// Bloom parameter validation
// ============================================================

func TestBloomFilterSize(t *testing.T) {
	bf := newBloomFilter(1_000_000, 0.015)
	expectedMB := float64(bf.size) / 8 / 1024 / 1024
	if expectedMB < 1 || expectedMB > 10 {
		t.Logf("bloom size: %.2f MB, hashes: %d", expectedMB, bf.hashes)
	}
	if bf.hashes < 3 || bf.hashes > 20 {
		t.Errorf("unexpected hash count: %d (expected 3-20)", bf.hashes)
	}
}

// ============================================================
// Edge: property index should not affect edge operations
// ============================================================

func TestPropertyIndex_EdgesUnchanged(t *testing.T) {
	ctx := context.Background()
	g := NewStatefulGraph()

	n1 := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}
	n2 := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}
	g.AddNode(ctx, n1)
	g.AddNode(ctx, n2)

	e := &model.Edge{
		EdgeID: model.NewID(), Type: model.EdgeLineage,
		Source: n2.ArtifactID, Target: n1.ArtifactID, Valid: true,
	}
	g.AddEdge(ctx, e)

	out, _ := g.EdgesOut(ctx, n2.ArtifactID, model.EdgeLineage)
	if len(out) != 1 {
		t.Errorf("expected 1 edge, got %d", len(out))
	}
}
