package knowledge

import (
	"context"
	"fmt"

	"github.com/DotBlood/ioc/internal/model"
)

// RevisionManager handles Revision DAG semantics: branching, head resolution,
// state derivation (active/superseded/archived).
type RevisionManager struct {
	graph RevisionStoreReader
}

// NewRevisionManager creates a new revision manager.
func NewRevisionManager(graph RevisionStoreReader) *RevisionManager {
	return &RevisionManager{graph: graph}
}

// ResolveRevision resolves a RevisionRef to a concrete ArtifactProjection.
//   - ref.Revision = 0, ref.Branch = "" → latest active in default ("main") branch
//   - ref.Branch = "experiment"        → latest in branch "experiment"
//   - ref.Revision = 42                → exact revision 42 (any branch)
//   - ref.Filter = RevFilterAncestor   → nearest ancestor reachable via LINEAGE
func (m *RevisionManager) ResolveRevision(ctx context.Context, ref model.RevisionRef) (*model.ArtifactProjection, error) {
	switch ref.Filter {
	case model.RevFilterLatest:
		return m.resolveLatest(ctx, ref.StableID, ref.Branch)
	case model.RevFilterPinned:
		return m.resolvePinned(ctx, ref.StableID, ref.Revision)
	case model.RevFilterBranchLocal:
		return m.resolveBranchLocal(ctx, ref.StableID, ref.Branch)
	case model.RevFilterAncestor:
		return m.resolveAncestor(ctx, ref.StableID)
	default:
		return m.resolveLatest(ctx, ref.StableID, ref.Branch)
	}
}

// ResolveForRetrieval resolves the effective projections for retrieval.
// Returns all projections that match the scope filter and time filter.
func (m *RevisionManager) ResolveForRetrieval(ctx context.Context, artifactID model.ID, opts model.TraversalOpts) ([]*model.ArtifactProjection, error) {
	heads, err := m.graph.ActiveHeads(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve for retrieval: %w", err)
	}

	var result []*model.ArtifactProjection
	for branch, revisions := range heads {
		rev, ok := revisions[artifactID]
		if !ok {
			continue
		}
		proj, err := m.graph.Projection(ctx, model.ProjectionKey{ArtifactID: artifactID, Revision: rev})
		if err != nil {
			continue
		}
		result = append(result, proj)
		_ = branch // could use branch for scope filter later
	}
	return result, nil
}

// IsActive returns true if the projection is the latest active head in its branch.
// State is derived from DAG topology, not mutable flags.
func (m *RevisionManager) IsActive(ctx context.Context, proj *model.ArtifactProjection) (bool, error) {
	heads, err := m.graph.ActiveHeads(ctx)
	if err != nil {
		return false, err
	}
	for _, branchHeads := range heads {
		head, ok := branchHeads[proj.ArtifactID]
		if ok && head == proj.Revision {
			return true, nil
		}
	}
	return false, nil
}

// IsSuperseded returns true if there is a newer revision in the same lineage.
func (m *RevisionManager) IsSuperseded(ctx context.Context, proj *model.ArtifactProjection) (bool, error) {
	revs, err := m.graph.RevisionChildren(ctx, proj.ArtifactID, proj.Revision)
	if err != nil {
		return false, err
	}
	return len(revs) > 0, nil
}

func (m *RevisionManager) resolveLatest(ctx context.Context, artifactID model.ID, branch model.BranchName) (*model.ArtifactProjection, error) {
	heads, err := m.graph.ActiveHeads(ctx)
	if err != nil {
		return nil, err
	}
	branchHeads, ok := heads[branch]
	if !ok {
		return nil, fmt.Errorf("branch %q not found", branch)
	}
	rev, ok := branchHeads[artifactID]
	if !ok {
		return nil, fmt.Errorf("artifact %s not found in branch %q", artifactID, branch)
	}
	return m.graph.Projection(ctx, model.ProjectionKey{ArtifactID: artifactID, Revision: rev})
}

func (m *RevisionManager) resolvePinned(ctx context.Context, artifactID model.ID, rev model.RevisionNumber) (*model.ArtifactProjection, error) {
	return m.graph.Projection(ctx, model.ProjectionKey{ArtifactID: artifactID, Revision: rev})
}

func (m *RevisionManager) resolveBranchLocal(ctx context.Context, artifactID model.ID, branch model.BranchName) (*model.ArtifactProjection, error) {
	heads, err := m.graph.ActiveHeads(ctx)
	if err != nil {
		return nil, err
	}
	branchHeads, ok := heads[branch]
	if !ok {
		return nil, fmt.Errorf("branch %q not found", branch)
	}
	rev, ok := branchHeads[artifactID]
	if !ok {
		return nil, fmt.Errorf("artifact %s has no revision in branch %q", artifactID, branch)
	}
	return m.graph.Projection(ctx, model.ProjectionKey{ArtifactID: artifactID, Revision: rev})
}

func (m *RevisionManager) resolveAncestor(ctx context.Context, artifactID model.ID) (*model.ArtifactProjection, error) {
	heads, err := m.graph.ActiveHeads(ctx)
	if err != nil {
		return nil, err
	}
	// Walk all branch heads, find the oldest reachable ancestor.
	var oldestRev model.RevisionNumber
	var oldestProj *model.ArtifactProjection
	for _, branchHeads := range heads {
		rev, ok := branchHeads[artifactID]
		if !ok {
			continue
		}
		proj, err := m.graph.Projection(ctx, model.ProjectionKey{ArtifactID: artifactID, Revision: rev})
		if err != nil {
			continue
		}
		if oldestProj == nil || rev < oldestRev {
			oldestRev = rev
			oldestProj = proj
		}
	}
	if oldestProj == nil {
		return nil, fmt.Errorf("no revision found for artifact %s", artifactID)
	}
	return oldestProj, nil
}

// RevisionStoreReader is the minimal interface knowledge/ uses from physical graph for revisions.
type RevisionStoreReader interface {
	Projection(ctx context.Context, key model.ProjectionKey) (*model.ArtifactProjection, error)
	ActiveHeads(ctx context.Context) (map[model.BranchName]map[model.ID]model.RevisionNumber, error)
	RevisionChildren(ctx context.Context, artifactID model.ID, rev model.RevisionNumber) ([]model.RevisionNumber, error)
	StoreProjection(ctx context.Context, proj *model.ArtifactProjection) error
}
