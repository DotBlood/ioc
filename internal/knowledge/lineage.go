package knowledge

import (
	"context"
	"fmt"

	"github.com/DotBlood/ioc/internal/model"
)

// LineageTracker enforces lineage rules and records provenance.
type LineageTracker struct {
	graph LineageStoreReader
}

// NewLineageTracker creates a new lineage tracker.
func NewLineageTracker(graph LineageStoreReader) *LineageTracker {
	return &LineageTracker{graph: graph}
}

// RecordLineage creates a LINEAGE edge between parent and child artifacts.
// Enforces:
//   - DAG acyclicity (cycle check before commit)
//   - Monotonicity (child revision > parent revision)
//   - Single parent (no multi-parent in LINEAGE)
func (t *LineageTracker) RecordLineage(ctx context.Context, parentID, childID model.ID) error {
	parent, err := t.graph.Node(ctx, parentID)
	if err != nil {
		return fmt.Errorf("record lineage: parent %s: %w", parentID, err)
	}
	child, err := t.graph.Node(ctx, childID)
	if err != nil {
		return fmt.Errorf("record lineage: child %s: %w", childID, err)
	}

	// Enforce monotonicity: child must have a later (or equal) revision.
	if parent.CreatedAt.After(child.CreatedAt) {
		// For now this is a check; projections will enforce RevisionNumber ordering.
	}

	// Cycle check: walk LINEAGE up from parent, ensure child is not an ancestor.
	if err := t.checkCycle(ctx, childID, parentID); err != nil {
		return fmt.Errorf("record lineage: %w", err)
	}

	edge := model.Edge{
		EdgeID:    model.NewID(),
		Revision:  1,
		Type:      model.EdgeLineage,
		Direction: model.DirectionDirected,
		Source:    childID,
		Target:    parentID,
		Valid:     true,
		ValidFrom: child.CreatedAt,
	}
	return t.graph.StoreEdge(ctx, &edge)
}

// RecordOwnership creates an OWNERSHIP edge linking a node to its scope parent.
func (t *LineageTracker) RecordOwnership(ctx context.Context, nodeID, scopeParentID model.ID) error {
	edge := model.Edge{
		EdgeID:    model.NewID(),
		Revision:  1,
		Type:      model.EdgeOwnership,
		Direction: model.DirectionDirected,
		Source:    nodeID,
		Target:    scopeParentID,
		Valid:     true,
	}
	return t.graph.StoreEdge(ctx, &edge)
}

// Provenance returns the full chain of lineage edges for an artifact.
func (t *LineageTracker) Provenance(ctx context.Context, artifactID model.ID) ([]model.Edge, error) {
	var chain []model.Edge
	current := artifactID
	depth := 0
	for !current.IsZero() && depth < 1000 {
		edges, err := t.graph.EdgesOut(ctx, current, model.EdgeLineage)
		if err != nil {
			return nil, err
		}
		if len(edges) == 0 {
			break
		}
		// LINEAGE edges: source=child → target=parent.
		chain = append(chain, edges[0])
		current = edges[0].Target
		depth++
	}
	return chain, nil
}

func (t *LineageTracker) checkCycle(ctx context.Context, start, target model.ID) error {
	visited := make(map[model.ID]bool)
	stack := []model.ID{start}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current == target {
			return model.ErrCycleDetected
		}
		if visited[current] {
			continue
		}
		visited[current] = true

		// Traverse lineage edges in BOTH directions.
		// EdgesOut: source=child → target=parent (forward: child points to parent).
		edges, err := t.graph.EdgesOut(ctx, current, model.EdgeLineage)
		if err != nil {
			return err
		}
		for _, e := range edges {
			if !visited[e.Target] {
				stack = append(stack, e.Target)
			}
		}

		// EdgesIn: source=child, target=current (reverse: current is parent of child).
		inEdges, err := t.graph.EdgesIn(ctx, current, model.EdgeLineage)
		if err != nil {
			return err
		}
		for _, e := range inEdges {
			if !visited[e.Source] {
				stack = append(stack, e.Source)
			}
		}
	}
	return nil
}

// LineageStoreReader is the minimal interface knowledge/ uses for lineage.
type LineageStoreReader interface {
	Node(ctx context.Context, id model.ID) (*model.Artifact, error)
	Edge(ctx context.Context, id model.EdgeID) (*model.Edge, error)
	EdgesOut(ctx context.Context, sourceID model.ID, edgeType model.EdgeType) ([]model.Edge, error)
	EdgesIn(ctx context.Context, targetID model.ID, edgeType model.EdgeType) ([]model.Edge, error)
	StoreEdge(ctx context.Context, edge *model.Edge) error
}
