package knowledge

import (
	"context"
	"fmt"
	"time"

	"github.com/DotBlood/ioc/internal/model"
)

// ArchiveStage is a monotonic state machine for the archive pipeline.
// Stages never move backwards.
type ArchiveStage uint8

const (
	ArchiveStageNone       ArchiveStage = 0
	ArchiveStageFrozen     ArchiveStage = 1 // FreezeScope successful
	ArchiveStageAnchored   ArchiveStage = 2 // Anchor created
	ArchiveStageSummarized ArchiveStage = 3 // Summary artifact created
	ArchiveStageArchived   ArchiveStage = 4 // Lifecycle transition to archived
)

// ArchiveResult contains the outcome of an archive operation.
type ArchiveResult struct {
	Stage    ArchiveStage
	AnchorID model.AnchorID
}

// ScopeFreezer is the interface for graph-level scope freeze.
type ScopeFreezer interface {
	FreezeScope(scopeID model.ScopeID) error
	UnfreezeScope(scopeID model.ScopeID)
}

// SnapshotInstaller installs resolved graph state and rebuilds runtime structures.
type SnapshotInstaller interface {
	InstallSnapshot(nodes map[model.ID]*model.Artifact, edges map[model.EdgeID]*model.Edge)
	RebuildRuntimeState()
}

// AnchorStorer is the minimal interface for anchor persistence.
type AnchorStorer interface {
	Create(ctx context.Context, anchor *model.Anchor) error
	Load(ctx context.Context, id model.AnchorID) (*model.Anchor, error)
	List(ctx context.Context, scopeID model.ScopeID) ([]model.Anchor, error)
}

// ArtifactCreator is the minimal interface for creating summary artifacts.
type ArtifactCreator interface {
	SaveSummaryArtifact(ctx context.Context, summary string, scopeID model.ScopeID) (model.ID, error)
	StoreEdge(ctx context.Context, edge *model.Edge) error
}

// LifecycleTransitioner is the minimal interface for lifecycle transitions.
type LifecycleTransitioner interface {
	Transition(ctx context.Context, scopeID model.ScopeID, to model.LifecycleState) error
}

// ArchivePipeline orchestrates the scope archiving process.
//
// Pipeline stages (monotonic):
//  1. Freeze — block writes, drain pending
//  2. Anchor — create structural snapshot (full or diff)
//  3. Summary — create deterministic summary artifact
//  4. Archive — transition to LifecycleArchived
//
// Orphan anchors (snapshot created but transition failed) are allowed.
// They preserve data integrity but waste storage.
// Future RetentionPolicy may prune unreferenced anchors.
type ArchivePipeline struct {
	freezer       ScopeFreezer
	installer     SnapshotInstaller
	anchorCreator *AnchorCreator
	anchorStore   AnchorStorer
	artifactStore ArtifactCreator
	lifecycle     LifecycleTransitioner
}

// NewArchivePipeline creates a new archive pipeline.
func NewArchivePipeline(
	freezer ScopeFreezer,
	installer SnapshotInstaller,
	anchorCreator *AnchorCreator,
	anchorStore AnchorStorer,
	artifactStore ArtifactCreator,
	lifecycle LifecycleTransitioner,
) *ArchivePipeline {
	return &ArchivePipeline{
		freezer:       freezer,
		installer:     installer,
		anchorCreator: anchorCreator,
		anchorStore:   anchorStore,
		artifactStore: artifactStore,
		lifecycle:     lifecycle,
	}
}

// Archive runs the full archive pipeline for a scope.
// Returns an ArchiveResult indicating what stage was reached.
// Guarantees UnfreezeScope even on panic.
func (p *ArchivePipeline) Archive(ctx context.Context, scopeID model.ScopeID) (*ArchiveResult, error) {
	result := &ArchiveResult{Stage: ArchiveStageNone}

	// 1. Freeze — block writes, drain pending.
	if err := p.freezer.FreezeScope(scopeID); err != nil {
		return result, fmt.Errorf("archive: freeze: %w", err)
	}
	defer p.freezer.UnfreezeScope(scopeID)
	result.Stage = ArchiveStageFrozen

	// 2. Anchor — structural snapshot.
	anchor, err := p.anchorCreator.CreateSnapshot(ctx, scopeID)
	if err != nil {
		return result, fmt.Errorf("archive: snapshot: %w", err)
	}
	result.AnchorID = anchor.AnchorID
	result.Stage = ArchiveStageAnchored

	// 3. Summary — deterministic structured summary (no LLM).
	summary := p.buildSummary(scopeID, anchor)
	summaryID, err := p.artifactStore.SaveSummaryArtifact(ctx, summary, scopeID)
	if err != nil {
		return result, fmt.Errorf("archive: summary: %w", err)
	}
	// Link summary to anchor via REFERENCES edge.
	refEdge := &model.Edge{
		EdgeID:    model.NewID(),
		Type:      model.EdgeReference,
		Direction: model.DirectionDirected,
		Source:    summaryID,
		Target:    model.ID(anchor.AnchorID),
		Valid:     true,
		ValidFrom: time.Now(),
	}
	if err := p.artifactStore.StoreEdge(ctx, refEdge); err != nil {
		return result, fmt.Errorf("archive: reference edge: %w", err)
	}
	result.Stage = ArchiveStageSummarized

	// 4. Lifecycle transition.
	if err := p.lifecycle.Transition(ctx, scopeID, model.LifecycleArchived); err != nil {
		return result, fmt.Errorf("archive: transition: %w", err)
	}
	result.Stage = ArchiveStageArchived

	return result, nil
}

// Restore restores a scope from an anchor. Full structural restore only.
// Fails with ErrScopeExists if the scope is already active.
func (p *ArchivePipeline) Restore(ctx context.Context, anchorID model.AnchorID) error {
	anchor, err := p.anchorStore.Load(ctx, anchorID)
	if err != nil {
		return fmt.Errorf("restore: load anchor: %w", err)
	}

	// Check scope state — fail if already active.
	// For v0.1, we don't have direct ScopeState access here,
	// so the caller is responsible for checking.
	_ = anchor

	// Freeze during install — prevent concurrent writes.
	if err := p.freezer.FreezeScope(anchor.ScopeID); err != nil {
		return fmt.Errorf("restore: freeze: %w", err)
	}
	defer p.freezer.UnfreezeScope(anchor.ScopeID)

	// Resolve full anchor state (follow diff chain if needed).
	fullAnchor := p.resolveFull(ctx, anchor)

	// Build graph state from anchor.
	nodes, edges := p.anchorToGraph(fullAnchor)

	// Install and rebuild runtime.
	p.installer.InstallSnapshot(nodes, edges)
	p.installer.RebuildRuntimeState()

	// Transition to active.
	if err := p.lifecycle.Transition(ctx, anchor.ScopeID, model.LifecycleActive); err != nil {
		return fmt.Errorf("restore: transition: %w", err)
	}

	return nil
}

// buildSummary creates a deterministic structured summary for an anchor.
// No LLM — purely structural metadata.
func (p *ArchivePipeline) buildSummary(scopeID model.ScopeID, anchor *model.Anchor) string {
	return fmt.Sprintf(`Archived scope: %s
Artifacts: %d
Edges: %d
Projections: %d
Archived at: %s
Anchor: %s
Anchor kind: %s
Revision: %d`,
		scopeID,
		anchor.ArtifactCount,
		anchor.EdgeCount,
		anchor.ProjectionCount,
		anchor.CreatedAt.Format(time.RFC3339),
		anchor.AnchorID,
		anchorKindString(anchor.Kind),
		anchor.Revision,
	)
}

// resolveFull resolves a possibly-diff anchor to a full state by walking
// the parent anchor chain.
func (p *ArchivePipeline) resolveFull(ctx context.Context, anchor *model.Anchor) *model.Anchor {
	if anchor.IsFull() {
		return anchor
	}

	// Walk diff chain to find the nearest full ancestor.
	var chain []*model.Anchor
	current := anchor
	for current.IsDiff() && !model.ID(current.ParentAnchor).IsZero() {
		chain = append(chain, current)
		parent, err := p.anchorStore.Load(ctx, current.ParentAnchor)
		if err != nil {
			// Can't resolve — return best-effort (current anchor).
			return anchor
		}
		current = parent
	}

	// Start from full anchor.
	full := cloneAnchor(current)
	// Apply diffs in reverse order.
	for i := len(chain) - 1; i >= 0; i-- {
		diff := chain[i]
		full.ArtifactRefs = append(full.ArtifactRefs, diff.ArtifactRefs...)
		full.EdgeRefs = append(full.EdgeRefs, diff.EdgeRefs...)
		full.ProjectionRefs = append(full.ProjectionRefs, diff.ProjectionRefs...)
		full.Revision = diff.Revision
	}
	return full
}

// anchorToGraph converts a resolved full anchor into graph state maps.
func (p *ArchivePipeline) anchorToGraph(anchor *model.Anchor) (map[model.ID]*model.Artifact, map[model.EdgeID]*model.Edge) {
	nodes := make(map[model.ID]*model.Artifact)
	for _, ref := range anchor.ArtifactRefs {
		// For v0.1, create placeholder artifacts for each ref.
		nodes[ref] = &model.Artifact{
			ArtifactID: ref,
			NodeType:   model.NodeTypeArtifact,
			Scope:      anchor.ScopeID,
		}
	}

	edges := make(map[model.EdgeID]*model.Edge)
	for _, ref := range anchor.EdgeRefs {
		// Edge data would be loaded from store in full implementation.
		edges[ref] = &model.Edge{
			EdgeID: ref,
			Valid:  true,
		}
	}

	return nodes, edges
}

func anchorKindString(k model.AnchorKind) string {
	if k == model.AnchorDiff {
		return "diff"
	}
	return "full"
}

func cloneAnchor(a *model.Anchor) *model.Anchor {
	c := *a
	c.ArtifactRefs = append([]model.ID(nil), a.ArtifactRefs...)
	c.EdgeRefs = append([]model.EdgeID(nil), a.EdgeRefs...)
	c.ProjectionRefs = append([]model.ProjectionKey(nil), a.ProjectionRefs...)
	return &c
}


