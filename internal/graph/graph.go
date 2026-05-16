package graph

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math"
	"sync"

	"github.com/DotBlood/ioc/internal/model"
)

// ============================================================
// RuntimeConfig
// ============================================================

// RuntimeConfig controls ephemeral acceleration structures.
type RuntimeConfig struct {
	ExpectedArtifacts int
	BloomFPR          float64
}

// DefaultRuntimeConfig returns recommended defaults for v0.1.
func DefaultRuntimeConfig() RuntimeConfig {
	return RuntimeConfig{
		ExpectedArtifacts: 1_000_000,
		BloomFPR:          0.015,
	}
}

// ============================================================
// RuntimeIndexes
// ============================================================

// RuntimeIndexes holds materialized runtime structures.
// Эти структуры НЕ персистятся — они перестраиваются при загрузке.
// Persisted truth = nodes map, edges map, adjacency lists.
type RuntimeIndexes struct {
	Bloom       *bloomFilter
	PropertyIdx propertyIndex
}

// ============================================================
// StatefulGraph
// ============================================================

// StatefulGraph is the physical in-memory graph engine.
// It stores nodes, edges, adjacency lists, and indexes — nothing more.
// All cognition semantics (scope, branch, lifecycle) live in knowledge/.
type StatefulGraph struct {
	mu sync.RWMutex

	// Persistent truth (canonical state, serialized to disk).
	nodes   map[model.ID]*model.Artifact
	edges   map[model.EdgeID]*model.Edge
	adjOut  map[model.ID]map[model.EdgeType][]model.ID
	adjIn   map[model.ID]map[model.EdgeType][]model.ID

	// Materialized runtime structures (rebuilt on load, not persisted).
	runtime     RuntimeIndexes
	runtimeCfg  RuntimeConfig

	// Scope-level freeze locks for archive isolation.
	scopeLocks map[model.ScopeID]*scopeLock
}

// NewStatefulGraph creates an empty physical graph with default config.
func NewStatefulGraph() *StatefulGraph {
	return NewStatefulGraphWithConfig(DefaultRuntimeConfig())
}

// NewStatefulGraphWithConfig creates an empty physical graph with the given config.
func NewStatefulGraphWithConfig(cfg RuntimeConfig) *StatefulGraph {
	g := &StatefulGraph{
		nodes:       make(map[model.ID]*model.Artifact),
		edges:       make(map[model.EdgeID]*model.Edge),
		adjOut:      make(map[model.ID]map[model.EdgeType][]model.ID),
		adjIn:       make(map[model.ID]map[model.EdgeType][]model.ID),
		runtimeCfg:  cfg,
		scopeLocks:  make(map[model.ScopeID]*scopeLock),
	}
	g.RebuildRuntimeState()
	return g
}

// ============================================================
// Install snapshot (load from disk without double indexing)
// ============================================================

// installSnapshot directly sets canonical maps WITHOUT runtime indexing.
// Call RebuildRuntimeState() after to materialize runtime structures.
func (g *StatefulGraph) installSnapshot(nodes map[model.ID]*model.Artifact, edges map[model.EdgeID]*model.Edge, adjOut map[model.ID]map[model.EdgeType][]model.ID, adjIn map[model.ID]map[model.EdgeType][]model.ID) {
	g.nodes = nodes
	g.edges = edges
	g.adjOut = adjOut
	g.adjIn = adjIn
}

// RebuildRuntimeState rebuilds all materialized runtime structures in one pass.
func (g *StatefulGraph) RebuildRuntimeState() {
	bf := newBloomFilter(g.runtimeCfg.ExpectedArtifacts, g.runtimeCfg.BloomFPR)
	idx := make(propertyIndex)
	for id, art := range g.nodes {
		bf.Add(id)
		idx.add(id, indexableProps(art)...)
	}
	g.runtime = RuntimeIndexes{
		Bloom:       bf,
		PropertyIdx: idx,
	}
}

// ============================================================
// Node CRUD
// ============================================================

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

// AddNode inserts a node into the graph. Updates runtime structures.
func (g *StatefulGraph) AddNode(_ context.Context, n *model.Artifact) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.nodes[n.ArtifactID]; exists {
		return model.ErrDuplicate
	}
	g.nodes[n.ArtifactID] = n
	g.runtime.Bloom.Add(n.ArtifactID)
	g.runtime.PropertyIdx.add(n.ArtifactID, indexableProps(n)...)
	return nil
}

// RemoveNode removes a node from the graph.
// Runtime bloom filter is NOT updated (FPR may grow slightly — acceptable for v0.1).
func (g *StatefulGraph) RemoveNode(_ context.Context, id model.ID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes[id]
	if !ok {
		return model.ErrNotFound
	}
	delete(g.nodes, id)
	g.runtime.PropertyIdx.remove(id, indexableProps(n)...)
	return nil
}

// ProbablyHas returns true if the ID probably exists in the graph.
// This is an optimization hint — false positives are possible.
// Guarantee: false means the ID definitely does not exist.
func (g *StatefulGraph) ProbablyHas(_ context.Context, id model.ID) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.runtime.Bloom.Has(id)
}

// ============================================================
// Edge CRUD
// ============================================================

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

// ============================================================
// Traversal
// ============================================================

// BFS performs a simple breadth-first traversal from start node.
// Traverses both outgoing and incoming edges.
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
			for _, targets := range g.adjOut[current] {
				for _, target := range targets {
					if !visited[target] {
						visited[target] = true
						queue = append(queue, target)
					}
				}
			}
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
		for _, targets := range g.adjOut[id] {
			for _, target := range targets {
				dfs(target, depth+1)
			}
		}
		for _, sources := range g.adjIn[id] {
			for _, source := range sources {
				dfs(source, depth+1)
			}
		}
	}
	dfs(start, 0)
	return order, nil
}

// ============================================================
// Index lookups
// ============================================================

// NodesByType returns all nodes of a given type.
// Rebuilt from canonical state during RebuildRuntimeState.
func (g *StatefulGraph) NodesByType(_ context.Context, nodeType model.NodeType) ([]model.ID, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.runtime.PropertyIdx.find("node_type", fmt.Sprint(nodeType)), nil
}

// PropertyIndexFind returns node IDs matching a property key-value pair.
func (g *StatefulGraph) PropertyIndexFind(prop, value string) []model.ID {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.runtime.PropertyIdx.find(prop, value)
}

// PropertyIndexKeys returns all distinct values for a given property.
func (g *StatefulGraph) PropertyIndexKeys(prop string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.runtime.PropertyIdx.keys(prop)
}

// ============================================================
// Snapshot
// ============================================================

// Snapshot serializes the full graph state to a byte slice.
func (g *StatefulGraph) Snapshot() ([]byte, error) {
	return nil, model.ErrNotImplemented
}

// ============================================================
// Property index helpers
// ============================================================

type indexedProp struct {
	name  string
	value string
}

func indexableProps(n *model.Artifact) []indexedProp {
	return []indexedProp{
		{name: "scope", value: string(n.Scope)},
		{name: "node_type", value: fmt.Sprint(n.NodeType)},
		{name: "content_hash", value: n.ContentHash.String()},
	}
}

// propertyIndex uses map[ID]struct{} for O(1) delete and dedup.
type propertyIndex map[string]map[string]map[model.ID]struct{}

func (idx propertyIndex) add(id model.ID, props ...indexedProp) {
	for _, p := range props {
		if idx[p.name] == nil {
			idx[p.name] = make(map[string]map[model.ID]struct{})
		}
		if idx[p.name][p.value] == nil {
			idx[p.name][p.value] = make(map[model.ID]struct{})
		}
		idx[p.name][p.value][id] = struct{}{}
	}
}

func (idx propertyIndex) remove(id model.ID, props ...indexedProp) {
	for _, p := range props {
		delete(idx[p.name][p.value], id)
		if len(idx[p.name][p.value]) == 0 {
			delete(idx[p.name], p.value)
		}
	}
}

func (idx propertyIndex) find(prop, value string) []model.ID {
	ids := idx[prop][value]
	if len(ids) == 0 {
		return nil
	}
	result := make([]model.ID, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	return result
}

func (idx propertyIndex) keys(prop string) []string {
	vals := idx[prop]
	if len(vals) == 0 {
		return nil
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	return keys
}

// ============================================================
// Bloom filter
// ============================================================

// bloomFilter is a standard Bloom filter (no delete support).
// Disposable runtime structure — not persisted.
type bloomFilter struct {
	bits   []uint64
	hashes int    // k
	size   uint64 // m (aligned to 64)
	count  int
}

func newBloomFilter(expectedItems int, fpr float64) *bloomFilter {
	if expectedItems <= 0 {
		expectedItems = 1
	}
	if fpr <= 0 || fpr >= 1 {
		fpr = 0.015
	}
	ln2 := math.Ln2
	m := -float64(expectedItems) * math.Log(fpr) / (ln2 * ln2)
	mAligned := (uint64(m) + 63) & ^uint64(63)
	if mAligned < 64 {
		mAligned = 64
	}
	k := int(float64(mAligned) / float64(expectedItems) * ln2)
	if k < 1 {
		k = 1
	}
	return &bloomFilter{
		bits:   make([]uint64, mAligned/64),
		hashes: k,
		size:   mAligned,
	}
}

// forEachKey applies fn to each of k bit positions.
// No allocations on hot path.
func (f *bloomFilter) forEachKey(data []byte, fn func(uint64)) {
	h1, h2 := fnv128(data)
	for i := 0; i < f.hashes; i++ {
		fn((h1 + uint64(i)*h2) % f.size)
	}
}

// Add inserts an element into the filter.
func (f *bloomFilter) Add(id model.ID) {
	f.forEachKey([]byte(id.String()), func(k uint64) {
		f.bits[k/64] |= 1 << (k % 64)
	})
	f.count++
}

// Has returns true if the element MAY BE in the set.
// false = definitely not present (no false negatives).
func (f *bloomFilter) Has(id model.ID) bool {
	result := true
	f.forEachKey([]byte(id.String()), func(k uint64) {
		if f.bits[k/64]&(1<<(k%64)) == 0 {
			result = false
		}
	})
	return result
}

// Reset clears the filter. Must be followed by re-adding all elements.
func (f *bloomFilter) Reset() {
	for i := range f.bits {
		f.bits[i] = 0
	}
	f.count = 0
}

func fnv128(data []byte) (uint64, uint64) {
	// Use FNV-1a 128-bit, split into two 64-bit values for double hashing.
	h := fnv.New128a()
	h.Write(data)
	sum := h.Sum(nil)
	return byteOrder.Uint64(sum[0:8]), byteOrder.Uint64(sum[8:16])
}

var byteOrder = binary.LittleEndian

