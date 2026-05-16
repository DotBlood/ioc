package pipeline

import (
	"context"

	"github.com/DotBlood/ioc/internal/knowledge"
	"github.com/DotBlood/ioc/internal/model"
)

// ArchivePipeline wraps knowledge.ArchivePipeline to provide a stable
// operational boundary for CLI and API consumers.
//
// This wrapper is a composition root for archive dependencies and a
// future extension point for pre-archive validation, retention hooks,
// and telemetry. Archive semantics belong to knowledge/archive.go.
type ArchivePipeline struct {
	inner *knowledge.ArchivePipeline
}

// NewArchivePipeline creates an ArchivePipeline that delegates to the
// knowledge-layer archive pipeline.
func NewArchivePipeline(
	freezer knowledge.ScopeFreezer,
	installer knowledge.SnapshotInstaller,
	anchorCreator *knowledge.AnchorCreator,
	anchorStore knowledge.AnchorStorer,
	artifactCreator knowledge.ArtifactCreator,
	lifecycle knowledge.LifecycleTransitioner,
) *ArchivePipeline {
	return &ArchivePipeline{
		inner: knowledge.NewArchivePipeline(
			freezer, installer, anchorCreator, anchorStore, artifactCreator, lifecycle,
		),
	}
}

// Archive runs the full archive pipeline for a scope.
func (p *ArchivePipeline) Archive(ctx context.Context, scopeID model.ScopeID) (*model.AnchorID, error) {
	result, err := p.inner.Archive(ctx, scopeID)
	if err != nil {
		return nil, err
	}
	return &result.AnchorID, nil
}

// Restore restores a scope from an anchor.
func (p *ArchivePipeline) Restore(ctx context.Context, anchorID model.AnchorID) error {
	return p.inner.Restore(ctx, anchorID)
}
