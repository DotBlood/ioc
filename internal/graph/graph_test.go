package graph

import (
	"context"
	"testing"

	"github.com/DotBlood/ioc/internal/model"
)

func TestStatefulGraph_AddNode(t *testing.T) {
	g := NewStatefulGraph()

	art := &model.Artifact{
		ArtifactID: model.NewID(),
		NodeType:   model.NodeTypeArtifact,
		Scope:      "test:scope",
	}

	if err := g.AddNode(context.Background(), art); err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	got, err := g.Node(context.Background(), art.ArtifactID)
	if err != nil {
		t.Fatalf("Node: %v", err)
	}
	if got.ArtifactID != art.ArtifactID {
		t.Errorf("Node returned wrong ID: got %v, want %v", got.ArtifactID, art.ArtifactID)
	}

	// Duplicate should fail.
	if err := g.AddNode(context.Background(), art); err != model.ErrDuplicate {
		t.Errorf("AddNode duplicate: want ErrDuplicate, got %v", err)
	}
}

func TestStatefulGraph_AddEdge(t *testing.T) {
	g := NewStatefulGraph()

	alice := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}
	bob := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}

	g.AddNode(context.Background(), alice)
	g.AddNode(context.Background(), bob)

	edge := &model.Edge{
		EdgeID:    model.NewID(),
		Type:      model.EdgeLineage,
		Direction: model.DirectionDirected,
		Source:    bob.ArtifactID,
		Target:    alice.ArtifactID,
		Valid:     true,
	}

	if err := g.AddEdge(context.Background(), edge); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	got, err := g.Edge(context.Background(), edge.EdgeID)
	if err != nil {
		t.Fatalf("Edge: %v", err)
	}
	if got.Source != bob.ArtifactID || got.Target != alice.ArtifactID {
		t.Errorf("Edge returned wrong endpoints: source=%v target=%v", got.Source, got.Target)
	}

	// Duplicate edge should fail.
	if err := g.AddEdge(context.Background(), edge); err != model.ErrDuplicate {
		t.Errorf("AddEdge duplicate: want ErrDuplicate, got %v", err)
	}
}

func TestStatefulGraph_BFS(t *testing.T) {
	g := newLinearGraph(t)

	ids := g.collectIDs(t)
	start := ids[0]

	order, err := g.BFS(context.Background(), start, 0)
	if err != nil {
		t.Fatalf("BFS: %v", err)
	}

	if len(order) != len(ids) {
		t.Errorf("BFS visited %d nodes, want %d", len(order), len(ids))
	}
}

func TestStatefulGraph_DFS(t *testing.T) {
	g := newLinearGraph(t)

	ids := g.collectIDs(t)
	start := ids[0]

	order, err := g.DFS(context.Background(), start, 0)
	if err != nil {
		t.Fatalf("DFS: %v", err)
	}

	if len(order) != len(ids) {
		t.Errorf("DFS visited %d nodes, want %d", len(order), len(ids))
	}
}

func TestStatefulGraph_NodesByType(t *testing.T) {
	g := NewStatefulGraph()

	w := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeWorktree}
	s := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeSession}
	a := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}

	g.AddNode(context.Background(), w)
	g.AddNode(context.Background(), s)
	g.AddNode(context.Background(), a)

	arts, err := g.NodesByType(context.Background(), model.NodeTypeArtifact)
	if err != nil {
		t.Fatalf("NodesByType: %v", err)
	}
	if len(arts) != 1 || arts[0] != a.ArtifactID {
		t.Errorf("NodesByType(artifact) = %v, want [%v]", arts, a.ArtifactID)
	}
}

func TestStatefulGraph_RemoveNode(t *testing.T) {
	g := NewStatefulGraph()
	art := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}
	g.AddNode(context.Background(), art)

	if err := g.RemoveNode(context.Background(), art.ArtifactID); err != nil {
		t.Fatalf("RemoveNode: %v", err)
	}

	if _, err := g.Node(context.Background(), art.ArtifactID); err != model.ErrNotFound {
		t.Errorf("Node after RemoveNode: want ErrNotFound, got %v", err)
	}
}

// newLinearGraph creates a chain: n0 → n1 → n2 with LINEAGE edges.
func newLinearGraph(t *testing.T) *StatefulGraph {
	t.Helper()
	g := NewStatefulGraph()

	n0 := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}
	n1 := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}
	n2 := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}

	for _, n := range []*model.Artifact{n0, n1, n2} {
		if err := g.AddNode(context.Background(), n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}

	g.AddEdge(context.Background(), &model.Edge{
		EdgeID: model.NewID(), Type: model.EdgeLineage,
		Source: n1.ArtifactID, Target: n0.ArtifactID, Valid: true,
	})
	g.AddEdge(context.Background(), &model.Edge{
		EdgeID: model.NewID(), Type: model.EdgeLineage,
		Source: n2.ArtifactID, Target: n1.ArtifactID, Valid: true,
	})

	return g
}

func (g *StatefulGraph) collectIDs(t *testing.T) []model.ID {
	t.Helper()
	var ids []model.ID
	arts, err := g.NodesByType(context.Background(), model.NodeTypeArtifact)
	if err != nil {
		t.Fatalf("NodesByType: %v", err)
	}
	for _, id := range arts {
		ids = append(ids, id)
	}
	return ids
}
