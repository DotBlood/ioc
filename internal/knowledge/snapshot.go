package knowledge

import (
	"context"
	"fmt"
	"time"

	"github.com/DotBlood/ioc/internal/model"
)

// AnchorCreator builds structural snapshots (anchors) of scope state.
type AnchorCreator struct {
	graph       SnapshotGraphReader
	anchorStore AnchorStoreWriter
}

// NewAnchorCreator creates a new AnchorCreator.
func NewAnchorCreator(graph SnapshotGraphReader, anchorStore AnchorStoreWriter) *AnchorCreator {
	return &AnchorCreator{
		graph:       graph,
		anchorStore: anchorStore,
	}
}

const defaultAnchorInterval = 10

// CreateSnapshot builds a structural anchor for scopeID at the current state.
// Automatically chooses Full vs Diff based on anchor history.
func (c *AnchorCreator) CreateSnapshot(ctx context.Context, scopeID model.ScopeID) (*model.Anchor, error) {
	existingAnchors, err := c.anchorStore.List(ctx, scopeID)
	if err != nil {
		return nil, fmt.Errorf("create snapshot: list anchors: %w", err)
	}
	isFull := shouldCreateFull(existingAnchors)
	anchor, err := c.createAnchor(ctx, scopeID, isFull, existingAnchors)
	if err != nil {
		return nil, err
	}
	if err := c.anchorStore.Create(ctx, anchor); err != nil {
		return nil, fmt.Errorf("create snapshot: store: %w", err)
	}
	return anchor, nil
}

func (c *AnchorCreator) createAnchor(ctx context.Context, scopeID model.ScopeID, isFull bool, existing []model.Anchor) (*model.Anchor, error) {
	// Collect artifact IDs belonging to this scope via ownership edges.
	artifactIDs, err := c.collectArtifactIDs(ctx, scopeID)
	if err != nil {
		return nil, fmt.Errorf("create snapshot: collect artifacts: %w", err)
	}

	// Collect projections for each artifact (latest revision).
	projectionRefs, err := c.collectProjectionRefs(ctx, artifactIDs)
	if err != nil {
		return nil, fmt.Errorf("create snapshot: collect projections: %w", err)
	}

	// Collect valid edge IDs in this scope.
	edgeIDs, err := c.collectEdgeIDs(ctx, scopeID)
	if err != nil {
		return nil, fmt.Errorf("create snapshot: collect edges: %w", err)
	}

	anchor := &model.Anchor{
		AnchorID:          model.AnchorID(model.NewID()),
		ScopeID:           scopeID,
		CreatedAt:         time.Now(),
		ProjectionRefs:    projectionRefs,
		EdgeRefs:          edgeIDs,
		ArtifactCount:     len(artifactIDs),
		EdgeCount:         len(edgeIDs),
		ProjectionCount:   len(projectionRefs),
	}

	if isFull {
		anchor.Kind = model.AnchorFull
		anchor.ArtifactRefs = artifactIDs
	} else {
		anchor.Kind = model.AnchorDiff
		lastFull := findLastFull(existing)
		prevRefs := existing[len(existing)-1].ArtifactRefs

		// Compute added/removed artifacts since last full anchor.
		added, removed := diffArtifactSets(lastFull.ArtifactRefs, artifactIDs)
		anchor.ArtifactRefs = added
		anchor.RemovedArtifactIDs = removed

		// Compute added/removed edges since last full.
		addedEdges, removedEdges := diffEdgeSets(lastFull.EdgeRefs, edgeIDs)
		anchor.EdgeRefs = addedEdges
		anchor.RemovedEdgeIDs = removedEdges

		// Link to parent (the immediate previous anchor, full or diff).
		anchor.ParentAnchor = existing[len(existing)-1].AnchorID
		_ = prevRefs
	}

	return anchor, nil
}

func (c *AnchorCreator) collectArtifactIDs(ctx context.Context, scopeID model.ScopeID) ([]model.ID, error) {
	// Walk scope via OWNERSHIP edges to find all artifacts.
	// For v0.1: use the graph node type index to find artifacts in scope.
	ids, err := c.graph.NodesByType(ctx, model.NodeTypeArtifact)
	if err != nil {
		return nil, err
	}

	// Filter by scope prefix.
	var inScope []model.ID
	for _, id := range ids {
		art, err := c.graph.Node(ctx, id)
		if err != nil {
			continue
		}
		if string(art.Scope) == string(scopeID) {
			inScope = append(inScope, id)
		}
	}
	return inScope, nil
}

func (c *AnchorCreator) collectProjectionRefs(ctx context.Context, artifactIDs []model.ID) ([]model.ProjectionKey, error) {
	var refs []model.ProjectionKey
	for _, artID := range artifactIDs {
		// For v0.1: collect latest projection keys.
		// In full version, would resolve from RevisionManager.
		refs = append(refs, model.ProjectionKey{ArtifactID: artID, Revision: 0})
	}
	return refs, nil
}

func (c *AnchorCreator) collectEdgeIDs(ctx context.Context, scopeID model.ScopeID) ([]model.EdgeID, error) {
	artifacts, err := c.collectArtifactIDs(ctx, scopeID)
	if err != nil {
		return nil, err
	}

	var edgeIDs []model.EdgeID
	for _, artID := range artifacts {
		// Collect outgoing LINEAGE and RETRIEVAL edges.
		for _, edgeType := range []model.EdgeType{model.EdgeLineage, model.EdgeOwnership, model.EdgeRetrieval} {
			edges, err := c.graph.EdgesOut(ctx, artID, edgeType)
			if err != nil {
				continue
			}
			for _, e := range edges {
				edgeIDs = append(edgeIDs, e.EdgeID)
			}
		}
	}
	return edgeIDs, nil
}

// shouldCreateFull returns true if a full anchor is needed (first or interval reached).
func shouldCreateFull(existing []model.Anchor) bool {
	if len(existing) == 0 {
		return true
	}
	lastFull := findLastFullIndex(existing)
	return (len(existing) - lastFull) >= defaultAnchorInterval
}

func findLastFull(anchors []model.Anchor) model.Anchor {
	for i := len(anchors) - 1; i >= 0; i-- {
		if anchors[i].Kind == model.AnchorFull {
			return anchors[i]
		}
	}
	return model.Anchor{}
}

func findLastFullIndex(anchors []model.Anchor) int {
	for i := len(anchors) - 1; i >= 0; i-- {
		if anchors[i].Kind == model.AnchorFull {
			return i
		}
	}
	return -1
}

func diffArtifactSets(base, current []model.ID) (added, removed []model.ID) {
	baseSet := make(map[model.ID]bool, len(base))
	for _, id := range base {
		baseSet[id] = true
	}
	currentSet := make(map[model.ID]bool, len(current))
	for _, id := range current {
		currentSet[id] = true
	}
	for _, id := range current {
		if !baseSet[id] {
			added = append(added, id)
		}
	}
	for _, id := range base {
		if !currentSet[id] {
			removed = append(removed, id)
		}
	}
	return
}

func diffEdgeSets(base, current []model.EdgeID) (added, removed []model.EdgeID) {
	baseSet := make(map[model.EdgeID]bool, len(base))
	for _, id := range base {
		baseSet[id] = true
	}
	currentSet := make(map[model.EdgeID]bool, len(current))
	for _, id := range current {
		currentSet[id] = true
	}
	for _, id := range current {
		if !baseSet[id] {
			added = append(added, id)
		}
	}
	for _, id := range base {
		if !currentSet[id] {
			removed = append(removed, id)
		}
	}
	return
}

// SnapshotGraphReader is the minimal interface knowledge/ needs for snapshot creation.
type SnapshotGraphReader interface {
	Node(ctx context.Context, id model.ID) (*model.Artifact, error)
	EdgesOut(ctx context.Context, sourceID model.ID, edgeType model.EdgeType) ([]model.Edge, error)
	NodesByType(ctx context.Context, nodeType model.NodeType) ([]model.ID, error)
}

// AnchorStoreWriter is the minimal interface for storing anchors.
type AnchorStoreWriter interface {
	List(ctx context.Context, scopeID model.ScopeID) ([]model.Anchor, error)
	Create(ctx context.Context, anchor *model.Anchor) error
}
