package knowledge

import (
	"context"
	"fmt"

	"github.com/DotBlood/ioc/internal/model"
)

// ScopeResolver resolves and validates scope hierarchy.
// It answers: what scopes exist, what is their relationship,
// what is the effective retrieval scope for a given session.
type ScopeResolver struct {
	graph GraphStoreReader
}

// NewScopeResolver creates a new scope resolver.
func NewScopeResolver(graph GraphStoreReader) *ScopeResolver {
	return &ScopeResolver{graph: graph}
}

// ResolveScope returns the effective retrieval scope for a given session.
// This includes the session itself plus any parent scopes it inherits from.
func (r *ScopeResolver) ResolveScope(ctx context.Context, sessionID model.ID) (model.ScopeID, error) {
	// Traverse ownership edges up from session to worktree.
	scopeIDs, err := r.collectScopeChain(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("resolve scope: %w", err)
	}
	if len(scopeIDs) == 0 {
		return "", fmt.Errorf("resolve scope: session %s has no scope chain", sessionID)
	}
	// Return the full path as a hierarchical ScopeID.
	var path string
	for _, id := range scopeIDs {
		if path != "" {
			path += ":"
		}
		path += id.String()
	}
	return model.ScopeID(path), nil
}

// IsVisibleFrom checks whether an artifact in sourceScope is visible
// when retrieved from targetScope. This enforces context isolation:
// sessions do not see sibling sessions by default.
func (r *ScopeResolver) IsVisibleFrom(ctx context.Context, sourceScope, targetScope model.ScopeID) (bool, error) {
	source := string(sourceScope)
	target := string(targetScope)

	// Exact match.
	if source == target {
		return true, nil
	}

	// Target is child of source (target works inside source scope).
	for len(target) >= len(source) {
		if target == source {
			return true, nil
		}
		// Move up one level.
		idx := lastColon(target)
		if idx < 0 {
			break
		}
		target = target[:idx]
	}

	return false, nil
}

func (r *ScopeResolver) collectScopeChain(ctx context.Context, nodeID model.ID) ([]model.ID, error) {
	// Walk OWNERSHIP edges upward from node to Worktree root.
	var chain []model.ID
	current := nodeID
	depth := 0
	for !current.IsZero() && depth < 100 {
		chain = append([]model.ID{current}, chain...)
		edges, err := r.graph.EdgesIn(ctx, current, model.EdgeOwnership)
		if err != nil {
			return nil, err
		}
		if len(edges) == 0 {
			break
		}
		current = edges[0].Source
		depth++
	}
	return chain, nil
}

func lastColon(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}

// GraphStoreReader is the minimal interface knowledge/ uses from physical graph.
type GraphStoreReader interface {
	Node(ctx context.Context, id model.ID) (*model.Artifact, error)
	EdgesIn(ctx context.Context, targetID model.ID, edgeType model.EdgeType) ([]model.Edge, error)
	EdgesOut(ctx context.Context, sourceID model.ID, edgeType model.EdgeType) ([]model.Edge, error)
	ActiveHeads(ctx context.Context) (map[model.BranchName]map[model.ID]model.RevisionNumber, error)
}
