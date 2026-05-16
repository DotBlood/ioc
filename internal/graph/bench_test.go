package graph

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/DotBlood/ioc/internal/model"
)

func newDeterministicRNG() *rand.Rand {
	return rand.New(rand.NewPCG(1, 2))
}

func BenchmarkGraphTraversal_100KNodes(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping 100K node benchmark in short mode")
	}

	b.Run("BFS/Chain_100K", func(b *testing.B) {
		ctx := context.Background()
		count := 100000

		g := NewStatefulGraph()
		nodes := make([]model.ID, count)
		for i := range count {
			id := model.NewID()
			nodes[i] = id
			err := g.AddNode(ctx, &model.Artifact{ArtifactID: id, NodeType: model.NodeTypeArtifact})
			if err != nil {
				b.Fatal(err)
			}
		}
		for i := range count - 1 {
			err := g.AddEdge(ctx, &model.Edge{
				EdgeID:    model.NewID(),
				Type:      model.EdgeLineage,
				Direction: model.DirectionDirected,
				Source:    nodes[i],
				Target:    nodes[i+1],
				Valid:     true,
			})
			if err != nil {
				b.Fatal(err)
			}
		}

		root := nodes[0]
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			visited, err := g.BFS(ctx, root, 0)
			if err != nil {
				b.Fatal(err)
			}
			if len(visited) != count {
				b.Fatalf("visited %d nodes, want %d", len(visited), count)
			}
		}
	})

	b.Run("BFS/Star_100K", func(b *testing.B) {
		ctx := context.Background()
		count := 100000

		g := NewStatefulGraph()
		root := model.NewID()
		err := g.AddNode(ctx, &model.Artifact{ArtifactID: root, NodeType: model.NodeTypeArtifact})
		if err != nil {
			b.Fatal(err)
		}
		for range count - 1 {
			child := model.NewID()
			err := g.AddNode(ctx, &model.Artifact{ArtifactID: child, NodeType: model.NodeTypeArtifact})
			if err != nil {
				b.Fatal(err)
			}
			err = g.AddEdge(ctx, &model.Edge{
				EdgeID:    model.NewID(),
				Type:      model.EdgeLineage,
				Direction: model.DirectionDirected,
				Source:    root,
				Target:    child,
				Valid:     true,
			})
			if err != nil {
				b.Fatal(err)
			}
		}

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			visited, err := g.BFS(ctx, root, 0)
			if err != nil {
				b.Fatal(err)
			}
			if len(visited) != count {
				b.Fatalf("visited %d nodes, want %d", len(visited), count)
			}
		}
	})
}

func BenchmarkGraphTraversal_DeepDepth(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping deep traversal benchmark in short mode")
	}

	rng := newDeterministicRNG()
	n := 10000

	b.Run(fmt.Sprintf("DFS/Unlimited_%d", n), func(b *testing.B) {
		ctx := context.Background()

		g := NewStatefulGraph()
		nodes := make([]model.ID, n)
		for i := range n {
			id := model.NewID()
			nodes[i] = id
			err := g.AddNode(ctx, &model.Artifact{ArtifactID: id, NodeType: model.NodeTypeArtifact})
			if err != nil {
				b.Fatal(err)
			}
		}
		for i := range n - 1 {
			err := g.AddEdge(ctx, &model.Edge{
				EdgeID:    model.NewID(),
				Type:      model.EdgeLineage,
				Direction: model.DirectionDirected,
				Source:    nodes[i],
				Target:    nodes[i+1],
				Valid:     true,
			})
			if err != nil {
				b.Fatal(err)
			}
		}

		root := nodes[0]
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			visited, err := g.DFS(ctx, root, 0)
			if err != nil {
				b.Fatal(err)
			}
			if len(visited) != n {
				b.Fatalf("visited %d nodes, want %d", len(visited), n)
			}
		}
		_ = rng
	})

	b.Run(fmt.Sprintf("DFS/Depth100_%d", n), func(b *testing.B) {
		ctx := context.Background()

		g := NewStatefulGraph()
		nodes := make([]model.ID, n)
		for i := range n {
			id := model.NewID()
			nodes[i] = id
			err := g.AddNode(ctx, &model.Artifact{ArtifactID: id, NodeType: model.NodeTypeArtifact})
			if err != nil {
				b.Fatal(err)
			}
		}
		for i := range n - 1 {
			err := g.AddEdge(ctx, &model.Edge{
				EdgeID:    model.NewID(),
				Type:      model.EdgeLineage,
				Direction: model.DirectionDirected,
				Source:    nodes[i],
				Target:    nodes[i+1],
				Valid:     true,
			})
			if err != nil {
				b.Fatal(err)
			}
		}

		root := nodes[0]
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			visited, err := g.DFS(ctx, root, 100)
			if err != nil {
				b.Fatal(err)
			}
			if len(visited) != 101 {
				b.Fatalf("visited %d nodes, want 101 (start + 100 depth)", len(visited))
			}
		}
	})

	b.Run(fmt.Sprintf("BFS/Unlimited_%d", n), func(b *testing.B) {
		ctx := context.Background()

		g := NewStatefulGraph()
		nodes := make([]model.ID, n)
		for i := range n {
			id := model.NewID()
			nodes[i] = id
			err := g.AddNode(ctx, &model.Artifact{ArtifactID: id, NodeType: model.NodeTypeArtifact})
			if err != nil {
				b.Fatal(err)
			}
		}
		for i := range n - 1 {
			err := g.AddEdge(ctx, &model.Edge{
				EdgeID:    model.NewID(),
				Type:      model.EdgeLineage,
				Direction: model.DirectionDirected,
				Source:    nodes[i],
				Target:    nodes[i+1],
				Valid:     true,
			})
			if err != nil {
				b.Fatal(err)
			}
		}

		root := nodes[0]
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			visited, err := g.BFS(ctx, root, 0)
			if err != nil {
				b.Fatal(err)
			}
			if len(visited) != n {
				b.Fatalf("visited %d nodes, want %d", len(visited), n)
			}
		}
	})
}
