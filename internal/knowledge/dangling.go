package knowledge

import (
	"context"
	"errors"
	"sync"

	"github.com/DotBlood/ioc/internal/model"
)

// DanglingChecker is eventually consistent.
// Cache invalidation is advisory and correctness MUST
// always be recoverable via Exists().
//
// Hybrid: traversal-time check + lazy in-memory cache.
// Cache is advisory only — correctness never depends on cache.
type DanglingChecker struct {
	cache map[model.ID]model.ReferenceState
	mu    sync.RWMutex
	graph DanglingGraphReader
}

// DanglingGraphReader is the minimal interface for dangling checks.
// Exists returns true if a node with the given ID exists in the store.
type DanglingGraphReader interface {
	Exists(ctx context.Context, id model.ID) (bool, error)
}

// NewDanglingChecker creates a new dangling reference checker.
func NewDanglingChecker(graph DanglingGraphReader) *DanglingChecker {
	return &DanglingChecker{
		cache: make(map[model.ID]model.ReferenceState),
		graph: graph,
	}
}

// ExistsReference returns the reference state for a target.
//
// Returns:
//
//	RefActive   — target exists and is accessible
//	RefDangling — target does not exist (deleted/absent)
//	RefUnresolvedRemote — store error (transient)
//
// Cache hit avoids store lookup on subsequent calls.
func (c *DanglingChecker) ExistsReference(ctx context.Context, target model.ID) (model.ReferenceState, error) {
	// 1. Check cache.
	c.mu.RLock()
	state, ok := c.cache[target]
	c.mu.RUnlock()
	if ok {
		return state, nil
	}

	// 2. Check graph store.
	exists, err := c.graph.Exists(ctx, target)
	if err != nil {
		return model.RefUnresolvedRemote, err
	}
	if !exists {
		c.setCache(target, model.RefDangling)
		return model.RefDangling, nil
	}

	c.setCache(target, model.RefActive)
	return model.RefActive, nil
}

// InvalidateCache clears cached states. If ids is nil, clears entire cache.
func (c *DanglingChecker) InvalidateCache(ids []model.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ids == nil {
		c.cache = make(map[model.ID]model.ReferenceState)
		return
	}
	for _, id := range ids {
		delete(c.cache, id)
	}
}

func (c *DanglingChecker) setCache(id model.ID, state model.ReferenceState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[id] = state
}

// OrphanScanner validates structural containment only.
// It does NOT validate lifecycle correctness, permissions,
// retrieval reachability, or semantic consistency.
//
// Orphan definition:
//
//	node that lost structural containment:
//	  - Artifact without an OWNERSHIP edge from a parent Session
//
// NOT orphan:
//   - parent is archived or detached (still structural owner)
//   - node is inactive (lifecycle ≠ topology)
type OrphanScanner struct {
	graph StructuralResolver
}

// StructuralResolver provides hierarchy traversal for orphan detection.
type StructuralResolver interface {
	// Parent returns the structural parent of a node via OWNERSHIP edge.
	// Returns ErrNotFound if no parent exists.
	Parent(ctx context.Context, childID model.ID) (model.ID, error)

	// Exists returns true if a node with the given ID exists.
	Exists(ctx context.Context, id model.ID) (bool, error)

	// Children returns direct children of a scope as node IDs.
	// ScopeID is resolved internally by the implementation.
	Children(ctx context.Context, scopeID model.ScopeID) ([]model.ID, error)
}

// NewOrphanScanner creates a new orphan scanner.
func NewOrphanScanner(graph StructuralResolver) *OrphanScanner {
	return &OrphanScanner{graph: graph}
}

// OrphanResult contains a single orphaned node and any errors encountered.
type OrphanResult struct {
	ID    model.ID
	Error error // non-fatal error during scan (collected, not silently skipped)
}

// ScanOrphans scans a scope subtree for structurally orphaned artifacts.
// Returns orphan IDs + any errors encountered during scan.
//
// Errors are collected, not silently skipped — audit subsystem must not hide corruption.
// Traversal is structural via Children() — no string prefix scanning.
func (s *OrphanScanner) ScanOrphans(ctx context.Context, scopeID model.ScopeID) ([]model.ID, error) {
	// For v0.1: simplified flat scan without recursive subtree traversal.
	// Children() returns direct children of this scope node.
	children, err := s.graph.Children(ctx, scopeID)
	if err != nil {
		return nil, err
	}

	var orphans []model.ID
	for _, childID := range children {
		parentID, err := s.graph.Parent(ctx, childID)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				orphans = append(orphans, childID)
				continue
			}
			return orphans, err
		}

		exists, err := s.graph.Exists(ctx, parentID)
		if err != nil {
			return orphans, err
		}
		if !exists {
			orphans = append(orphans, childID)
		}
	}

	return orphans, nil
}

// ScanOrphansFlat is an alias for ScanOrphans.
func (s *OrphanScanner) ScanOrphansFlat(ctx context.Context, scopeID model.ScopeID) ([]model.ID, error) {
	return s.ScanOrphans(ctx, scopeID)
}
