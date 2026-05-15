package graph

import (
	"context"
	"sync"

	"github.com/DotBlood/ioc/internal/model"
)

// StatefulGraph is the physical in-memory graph engine.
// It stores nodes, edges, adjacency lists, and indexes — nothing more.
// All cognition semantics (scope, branch, lifecycle) live in knowledge/.
type StatefulGraph struct {
	mu sync.RWMutex

	nodes map[model.ID]*model.Artifact
	edges map[model.EdgeID]*model.Edge

	// Adjacency lists: sourceID → edgeType → targetIDs.
	adjOut map[model.ID]map[model.EdgeType][]model.ID
	adjIn  map[model.ID]map[model.EdgeType][]model.ID

	// Indexes.
	nodeTypeIdx map[model.NodeType][]model.ID
}

// NewStatefulGraph creates an empty physical graph.
func NewStatefulGraph() *StatefulGraph {
	return &StatefulGraph{
		nodes:       make(map[model.ID]*model.Artifact),
		edges:       make(map[model.EdgeID]*model.Edge),
		adjOut:      make(map[model.ID]map[model.EdgeType][]model.ID),
		adjIn:       make(map[model.ID]map[model.EdgeType][]model.ID),
		nodeTypeIdx: make(map[model.NodeType][]model.ID),
	}
}

// Node returns a node by ID.
func (g *StatefulGraph) Node(_ context.Context, id model.ID) (*model.Artifact, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	if !ok {
		return nil, model.ErrNotFound
	}
	return n, nil
}

// AddNode inserts a node into the graph.
func (g *StatefulGraph) AddNode(_ context.Context, n *model.Artifact) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.nodes[n.ArtifactID]; exists {
		return model.ErrDuplicate
	}
	g.nodes[n.ArtifactID] = n
	g.nodeTypeIdx[n.NodeType] = append(g.nodeTypeIdx[n.NodeType], n.ArtifactID)
	return nil
}

// RemoveNode marks a node as deleted (soft-delete).
func (g *StatefulGraph) RemoveNode(_ context.Context, id model.ID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes[id]
	if !ok {
		return model.ErrNotFound
	}
	// Soft-delete: just remove from active indexes.
	delete(g.nodes, id)
	g.removeFromIndex(n)
	return nil
}

// Edge returns an edge by ID.
func (g *StatefulGraph) Edge(_ context.Context, id model.EdgeID) (*model.Edge, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	e, ok := g.edges[id]
	if !ok {
		return nil, model.ErrNotFound
	}
	return e, nil
}

// AddEdge inserts an edge into the graph. Updates adjacency lists.
// Does NOT enforce any cognition semantics (scope, cycle, lifecycle).
func (g *StatefulGraph) AddEdge(_ context.Context, e *model.Edge) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.edges[e.EdgeID]; exists {
		return model.ErrDuplicate
	}
	g.edges[e.EdgeID] = e

	// Update adjacency lists.
	if g.adjOut[e.Source] == nil {
		g.adjOut[e.Source] = make(map[model.EdgeType][]model.ID)
	}
	g.adjOut[e.Source][e.Type] = append(g.adjOut[e.Source][e.Type], e.Target)

	if g.adjIn[e.Target] == nil {
		g.adjIn[e.Target] = make(map[model.EdgeType][]model.ID)
	}
	g.adjIn[e.Target][e.Type] = append(g.adjIn[e.Target][e.Type], e.Source)

	return nil
}

// EdgesOut returns all outgoing edges of a given type from a node.
func (g *StatefulGraph) EdgesOut(_ context.Context, sourceID model.ID, edgeType model.EdgeType) ([]model.Edge, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	targets, ok := g.adjOut[sourceID][edgeType]
	if !ok {
		return nil, nil
	}
	result := make([]model.Edge, 0, len(targets))
	for _, targetID := range targets {
		// Find the edge by scanning (small sets in practice).
		for _, e := range g.edges {
			if e.Source == sourceID && e.Target == targetID && e.Type == edgeType {
				result = append(result, *e)
			}
		}
	}
	return result, nil
}

// EdgesIn returns all incoming edges of a given type to a node.
func (g *StatefulGraph) EdgesIn(_ context.Context, targetID model.ID, edgeType model.EdgeType) ([]model.Edge, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	sources, ok := g.adjIn[targetID][edgeType]
	if !ok {
		return nil, nil
	}
	result := make([]model.Edge, 0, len(sources))
	for _, sourceID := range sources {
		for _, e := range g.edges {
			if e.Source == sourceID && e.Target == targetID && e.Type == edgeType {
				result = append(result, *e)
			}
		}
	}
	return result, nil
}

// BFS performs a simple breadth-first traversal from start node.
// Traverses both outgoing and incoming edges to explore the full connected component.
// Returns nodes in BFS order. This is a PURE traversal — no filters.
// Filters and cognition semantics are applied by knowledge/.
func (g *StatefulGraph) BFS(_ context.Context, start model.ID, maxDepth int) ([]model.ID, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if _, ok := g.nodes[start]; !ok {
		return nil, model.ErrNotFound
	}

	visited := make(map[model.ID]bool)
	var order []model.ID
	queue := []model.ID{start}
	visited[start] = true

	depth := 0
	for len(queue) > 0 && (maxDepth <= 0 || depth < maxDepth) {
		levelSize := len(queue)
		for i := 0; i < levelSize; i++ {
			current := queue[i]
			order = append(order, current)
			// Traverse outgoing edges.
			for _, targets := range g.adjOut[current] {
				for _, target := range targets {
					if !visited[target] {
						visited[target] = true
						queue = append(queue, target)
					}
				}
			}
			// Traverse incoming edges.
			for _, sources := range g.adjIn[current] {
				for _, source := range sources {
					if !visited[source] {
						visited[source] = true
						queue = append(queue, source)
					}
				}
			}
		}
		queue = queue[levelSize:]
		depth++
	}
	return order, nil
}

// DFS performs a simple depth-first traversal from start node.
// Traverses both outgoing and incoming edges to explore the full connected component.
func (g *StatefulGraph) DFS(_ context.Context, start model.ID, maxDepth int) ([]model.ID, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if _, ok := g.nodes[start]; !ok {
		return nil, model.ErrNotFound
	}

	visited := make(map[model.ID]bool)
	var order []model.ID
	var dfs func(id model.ID, depth int)
	dfs = func(id model.ID, depth int) {
		if visited[id] {
			return
		}
		if maxDepth > 0 && depth > maxDepth {
			return
		}
		visited[id] = true
		order = append(order, id)
		// Traverse outgoing edges.
		for _, targets := range g.adjOut[id] {
			for _, target := range targets {
				dfs(target, depth+1)
			}
		}
		// Traverse incoming edges.
		for _, sources := range g.adjIn[id] {
			for _, source := range sources {
				dfs(source, depth+1)
			}
		}
	}
	dfs(start, 0)
	return order, nil
}

// NodesByType returns all nodes of a given type.
func (g *StatefulGraph) NodesByType(_ context.Context, nodeType model.NodeType) ([]model.ID, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	ids, ok := g.nodeTypeIdx[nodeType]
	if !ok {
		return nil, nil
	}
	result := make([]model.ID, len(ids))
	copy(result, ids)
	return result, nil
}

// Snapshot serializes the full graph state to a byte slice.
func (g *StatefulGraph) Snapshot() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return nil, model.ErrNotImplemented
}

func (g *StatefulGraph) removeFromIndex(n *model.Artifact) {
	ids := g.nodeTypeIdx[n.NodeType]
	for i, id := range ids {
		if id == n.ArtifactID {
			g.nodeTypeIdx[n.NodeType] = append(ids[:i], ids[i+1:]...)
			break
		}
	}
}
