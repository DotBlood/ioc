package knowledge

import (
	"context"
	"fmt"
	"time"

	"github.com/DotBlood/ioc/internal/model"
)

// HistoricalState is an internal replay scratch structure.
// NOT part of public model — use model.HistoricalScope for external consumers.
type HistoricalState struct {
	ArtifactIDs    []model.ID
	EdgeIDs        []model.EdgeID
	ProjectionRefs map[model.ID]model.ProjectionKey // ArtifactID → latest ProjectionKey
}

// TimeMachine reconstructs historical scope state from anchors.
// Does NOT depend on current graph state — only on persisted anchors and store.
type TimeMachine struct {
	anchorStore   TimeAnchorStore
	artifactStore TimeArtifactStore
	edgeStore     TimeEdgeStore
}

// TimeAnchorStore is the minimal anchor interface for time-machine.
type TimeAnchorStore interface {
	LatestFullAnchorBefore(ctx context.Context, scopeID model.ScopeID, at time.Time) (*model.Anchor, error)
	LatestAnchorsAfter(ctx context.Context, scopeID model.ScopeID, after, to time.Time) ([]model.Anchor, error)
}

// TimeArtifactStore is the minimal artifact interface for time-machine.
type TimeArtifactStore interface {
	LoadArtifactsByIDs(ctx context.Context, ids []model.ID) ([]*model.Artifact, []model.ID, error)
	ListProjectionKeys(ctx context.Context, artifactID model.ID) ([]model.ProjectionKey, error)
	LoadProjection(ctx context.Context, key model.ProjectionKey) (*model.ArtifactProjection, error)
}

// TimeEdgeStore is the minimal edge interface for time-machine.
type TimeEdgeStore interface {
	LoadEdgesByIDs(ctx context.Context, ids []model.EdgeID) ([]*model.Edge, error)
}

// NewTimeMachine creates a new time machine.
func NewTimeMachine(
	anchorStore TimeAnchorStore,
	artifactStore TimeArtifactStore,
	edgeStore TimeEdgeStore,
) *TimeMachine {
	return &TimeMachine{
		anchorStore:   anchorStore,
		artifactStore: artifactStore,
		edgeStore:     edgeStore,
	}
}

// ScopeStateAt reconstructs the scope state as it existed at time T.
//
// Algorithm:
//  1. Find the latest FULL anchor with CreatedAt <= T (reconstruction starts from full base).
//  2. Apply diff anchors after the full anchor but before T.
//     Diff anchors are replayed in Revision order (deterministic).
//  3. Load artifacts (batch).
//  4. Load edges, filter by temporal validity.
//  5. For each artifact, load the projection valid at T (MVCC semantics).
func (tm *TimeMachine) ScopeStateAt(ctx context.Context, scopeID model.ScopeID, at time.Time) (*model.HistoricalScope, error) {
	// 1. Latest full anchor at or before T.
	fullAnchor, err := tm.anchorStore.LatestFullAnchorBefore(ctx, scopeID, at)
	if err != nil {
		return nil, fmt.Errorf("time-machine: %w", err)
	}

	// 2. Initialize state from full anchor.
	state := anchorToHistoricalState(fullAnchor)

	// 3. Apply diff anchors after full anchor but before T.
	//    Diff anchors are replayed in Revision order (LatestAnchorsAfter guarantees sorting).
	//    Invariant: applying the same diff anchor twice is invalid and non-idempotent.
	diffs, err := tm.anchorStore.LatestAnchorsAfter(ctx, scopeID, fullAnchor.CreatedAt, at)
	if err != nil {
		return nil, fmt.Errorf("time-machine: list diffs: %w", err)
	}
	for _, diff := range diffs {
		if diff.Kind == model.AnchorDiff {
			applyDiff(state, &diff)
		}
	}

	// 4. Load artifacts (batch, one bbolt View).
	artifacts, missing, err := tm.artifactStore.LoadArtifactsByIDs(ctx, state.ArtifactIDs)
	if err != nil {
		return nil, fmt.Errorf("time-machine: load artifacts: %w", err)
	}

	// 5. Load edges, filter by temporal validity.
	edges, err := tm.edgeStore.LoadEdgesByIDs(ctx, state.EdgeIDs)
	if err != nil {
		return nil, fmt.Errorf("time-machine: load edges: %w", err)
	}
	filteredEdges := filterEdgesAt(edges, at)

	// 6. Load projections valid at T (MVCC semantics).
	var projections []*model.ArtifactProjection
	var projErrors []string
	for artID := range state.ProjectionRefs {
		proj, err := tm.ProjectionAt(ctx, artID, at)
		if err != nil {
			projErrors = append(projErrors, fmt.Sprintf("artifact %s: %v", artID, err))
			continue
		}
		projections = append(projections, proj)
	}

	scope := &model.HistoricalScope{
		ScopeID:     scopeID,
		At:          at,
		Artifacts:   artifacts,
		Projections: projections,
		Edges:       filteredEdges,
		FromAnchor:  fullAnchor.AnchorID,
		MissingIDs:  missing,
		Errors:      projErrors,
	}
	return scope, nil
}

// ProjectionAt returns the latest projection valid at time T.
//
// Invariant: RevisionNumber is monotonically increasing per artifact.
// Selection: latest valid revision at T (MVCC semantics).
// TODO(v0.2): store projection revisions sorted by ValidFrom descending
// to allow early-stop lookup for ProjectionAt().
func (tm *TimeMachine) ProjectionAt(ctx context.Context, artifactID model.ID, at time.Time) (*model.ArtifactProjection, error) {
	keys, err := tm.artifactStore.ListProjectionKeys(ctx, artifactID)
	if err != nil {
		return nil, err
	}
	var best *model.ArtifactProjection
	for _, key := range keys {
		proj, err := tm.artifactStore.LoadProjection(ctx, key)
		if err != nil {
			continue
		}
		if !model.ProjectionValidAt(proj, at) {
			continue
		}
		if best == nil || proj.Revision > best.Revision {
			best = proj
		}
	}
	if best == nil {
		return nil, model.ErrNoHistoricalState
	}
	return best, nil
}

// EdgesAt returns edges for a specific node+type valid at time T.
//
// TODO(v0.2): build node-scoped edge index for partial temporal reconstruction.
// Current implementation reconstructs full scope edge state, which is O(all scope edges).
func (tm *TimeMachine) EdgesAt(ctx context.Context, scopeID model.ScopeID, nodeID model.ID, edgeType model.EdgeType, at time.Time) ([]*model.Edge, error) {
	fullAnchor, err := tm.anchorStore.LatestFullAnchorBefore(ctx, scopeID, at)
	if err != nil {
		return nil, err
	}

	state := &HistoricalState{
		EdgeIDs: cloneEdgeIDs(fullAnchor.EdgeRefs),
	}
	diffs, err := tm.anchorStore.LatestAnchorsAfter(ctx, scopeID, fullAnchor.CreatedAt, at)
	if err != nil {
		return nil, err
	}
	for _, diff := range diffs {
		if diff.Kind == model.AnchorDiff {
			applyDiff(state, &diff)
		}
	}

	edges, err := tm.edgeStore.LoadEdgesByIDs(ctx, state.EdgeIDs)
	if err != nil {
		return nil, err
	}

	var result []*model.Edge
	for _, e := range edges {
		if e.Source == nodeID && e.Type == edgeType && model.EdgeValidAt(e, at) {
			result = append(result, e)
		}
	}
	return result, nil
}

// ============================================================
// Internal helpers
// ============================================================

// anchorToHistoricalState creates a HistoricalState from a full anchor.
func anchorToHistoricalState(a *model.Anchor) *HistoricalState {
	refs := make(map[model.ID]model.ProjectionKey, len(a.ProjectionRefs))
	for _, ref := range a.ProjectionRefs {
		refs[ref.ArtifactID] = ref
	}
	return &HistoricalState{
		ArtifactIDs:    cloneIDs(a.ArtifactRefs),
		EdgeIDs:        cloneEdgeIDs(a.EdgeRefs),
		ProjectionRefs: refs,
	}
}

// applyDiff applies a diff anchor to an existing HistoricalState.
// Diff anchors have add+remove semantics:
//   - ArtifactRefs/EdgeRefs: artifacts/edges present in this state.
//   - RemovedArtifactIDs/RemovedEdgeIDs: artifacts/edges removed since parent.
//   - ProjectionRefs: newer revision replaces older for the same ArtifactID.
//
// Invariant: applying the same diff anchor twice is invalid and non-idempotent.
// Replay pipeline guarantees unique revision ordering.
func applyDiff(state *HistoricalState, diff *model.Anchor) {
	// Remove artifacts.
	removedSet := make(map[model.ID]bool, len(diff.RemovedArtifactIDs))
	for _, id := range diff.RemovedArtifactIDs {
		removedSet[id] = true
	}
	filtered := make([]model.ID, 0, len(state.ArtifactIDs))
	for _, id := range state.ArtifactIDs {
		if !removedSet[id] {
			filtered = append(filtered, id)
		}
	}
	state.ArtifactIDs = append(filtered, diff.ArtifactRefs...)

	// Remove edges.
	removedEdgeSet := make(map[model.EdgeID]bool, len(diff.RemovedEdgeIDs))
	for _, id := range diff.RemovedEdgeIDs {
		removedEdgeSet[id] = true
	}
	filteredEdges := make([]model.EdgeID, 0, len(state.EdgeIDs))
	for _, id := range state.EdgeIDs {
		if !removedEdgeSet[id] {
			filteredEdges = append(filteredEdges, id)
		}
	}
	state.EdgeIDs = append(filteredEdges, diff.EdgeRefs...)

	// Update projection refs: newer revision replaces older for same ArtifactID.
	// Invariant: only one projection revision per artifact may exist in reconstructed state.
	for _, ref := range diff.ProjectionRefs {
		state.ProjectionRefs[ref.ArtifactID] = ref
	}
}

// filterEdgesAt filters edges by temporal validity at time T.
func filterEdgesAt(edges []*model.Edge, at time.Time) []*model.Edge {
	var result []*model.Edge
	for _, e := range edges {
		if model.EdgeValidAt(e, at) {
			result = append(result, e)
		}
	}
	return result
}

func cloneIDs(src []model.ID) []model.ID {
	result := make([]model.ID, len(src))
	copy(result, src)
	return result
}

func cloneEdgeIDs(src []model.EdgeID) []model.EdgeID {
	result := make([]model.EdgeID, len(src))
	copy(result, src)
	return result
}
