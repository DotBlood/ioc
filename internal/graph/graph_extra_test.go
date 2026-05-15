package graph

import (
	"context"
	"testing"

	"github.com/DotBlood/ioc/internal/model"
)

func TestStatefulGraph_PropertyIndex(t *testing.T) {
	g := NewStatefulGraph()
	ctx := context.Background()

	// Add a node then verify the index is consistent.
	id1 := model.NewID()
	g.AddNode(ctx, &model.Artifact{ArtifactID: id1, NodeType: model.NodeTypeArtifact})

	got, err := g.Node(ctx, id1)
	if err != nil {
		t.Fatalf("Node: %v", err)
	}
	if got.ArtifactID != id1 {
		t.Errorf("Node returned wrong id: got %v, want %v", got.ArtifactID, id1)
	}

	// Remove and verify.
	if err := g.RemoveNode(ctx, id1); err != nil {
		t.Fatalf("RemoveNode: %v", err)
	}
	if _, err := g.Node(ctx, id1); err != model.ErrNotFound {
		t.Errorf("Node after RemoveNode: want ErrNotFound, got %v", err)
	}

	// Type index should be updated after remove.
	arts, err := g.NodesByType(ctx, model.NodeTypeArtifact)
	if err != nil {
		t.Fatalf("NodesByType: %v", err)
	}
	if len(arts) != 0 {
		t.Errorf("NodesByType after remove: want 0, got %d", len(arts))
	}
}

func TestStatefulGraph_MultipleEdges(t *testing.T) {
	g := NewStatefulGraph()
	ctx := context.Background()

	n1 := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}
	n2 := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}
	g.AddNode(ctx, n1)
	g.AddNode(ctx, n2)

	// Add two edges of different types between same nodes.
	e1 := &model.Edge{
		EdgeID: model.NewID(), Type: model.EdgeLineage,
		Source: n2.ArtifactID, Target: n1.ArtifactID, Valid: true,
	}
	e2 := &model.Edge{
		EdgeID: model.NewID(), Type: model.EdgeReference,
		Source: n1.ArtifactID, Target: n2.ArtifactID, Valid: true,
	}

	if err := g.AddEdge(ctx, e1); err != nil {
		t.Fatalf("AddEdge e1: %v", err)
	}
	if err := g.AddEdge(ctx, e2); err != nil {
		t.Fatalf("AddEdge e2: %v", err)
	}

	out, err := g.EdgesOut(ctx, n1.ArtifactID, model.EdgeReference)
	if err != nil {
		t.Fatalf("EdgesOut: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("expected 1 reference edge out, got %d", len(out))
	}

	in, err := g.EdgesIn(ctx, n2.ArtifactID, model.EdgeReference)
	if err != nil {
		t.Fatalf("EdgesIn: %v", err)
	}
	if len(in) != 1 {
		t.Errorf("expected 1 reference edge in, got %d", len(in))
	}
}

func TestStatefulGraph_ConcurrentReads(t *testing.T) {
	g := NewStatefulGraph()
	ctx := context.Background()

	// Add 100 nodes.
	for i := 0; i < 100; i++ {
		g.AddNode(ctx, &model.Artifact{
			ArtifactID: model.NewID(),
			NodeType:   model.NodeTypeArtifact,
		})
	}

	// Concurrent reads.
	done := make(chan struct{}, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_, err := g.NodesByType(ctx, model.NodeTypeArtifact)
			if err != nil {
				t.Errorf("concurrent NodesByType: %v", err)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestStatefulGraph_EdgesInAndOutSync(t *testing.T) {
	g := NewStatefulGraph()
	ctx := context.Background()

	a := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}
	b := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}
	g.AddNode(ctx, a)
	g.AddNode(ctx, b)

	e := &model.Edge{
		EdgeID: model.NewID(), Type: model.EdgeLineage,
		Source: b.ArtifactID, Target: a.ArtifactID, Valid: true,
	}
	g.AddEdge(ctx, e)

	// EdgesOut from source should find the edge.
	out, err := g.EdgesOut(ctx, b.ArtifactID, model.EdgeLineage)
	if err != nil {
		t.Fatalf("EdgesOut: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("EdgesOut: expected 1, got %d", len(out))
	}
	if out[0].Target != a.ArtifactID {
		t.Errorf("EdgesOut target = %v, want %v", out[0].Target, a.ArtifactID)
	}

	// EdgesIn to target should find the same edge.
	in, err := g.EdgesIn(ctx, a.ArtifactID, model.EdgeLineage)
	if err != nil {
		t.Fatalf("EdgesIn: %v", err)
	}
	if len(in) != 1 {
		t.Fatalf("EdgesIn: expected 1, got %d", len(in))
	}
	if in[0].Source != b.ArtifactID {
		t.Errorf("EdgesIn source = %v, want %v", in[0].Source, b.ArtifactID)
	}
}
