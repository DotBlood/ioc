package pipeline

import (
	"context"
	"testing"

	"github.com/DotBlood/ioc/internal/knowledge"
	"github.com/DotBlood/ioc/internal/model"
)

// ============================================================
// Test doubles for archive dependencies
// ============================================================

type stubFreezer struct{}

func (s *stubFreezer) FreezeScope(_ model.ScopeID) error { return nil }
func (s *stubFreezer) UnfreezeScope(_ model.ScopeID)     {}

type stubInstaller struct{}

func (s *stubInstaller) InstallSnapshot(_ map[model.ID]*model.Artifact, _ map[model.EdgeID]*model.Edge) {
}
func (s *stubInstaller) RebuildRuntimeState() {}

type stubGraphReader struct{}

func (s *stubGraphReader) Node(_ context.Context, _ model.ID) (*model.Artifact, error) {
	return nil, model.ErrNotFound
}
func (s *stubGraphReader) EdgesOut(_ context.Context, _ model.ID, _ model.EdgeType) ([]model.Edge, error) {
	return nil, nil
}
func (s *stubGraphReader) NodesByType(_ context.Context, _ model.NodeType) ([]model.ID, error) {
	return nil, nil
}

type stubAnchorStoreWriter struct{}

func (s *stubAnchorStoreWriter) List(_ context.Context, _ model.ScopeID) ([]model.Anchor, error) {
	return nil, nil
}
func (s *stubAnchorStoreWriter) Create(_ context.Context, _ *model.Anchor) error {
	return nil
}

type stubAnchorStorer struct{}

func (s *stubAnchorStorer) Create(_ context.Context, _ *model.Anchor) error {
	return nil
}
func (s *stubAnchorStorer) Load(_ context.Context, _ model.AnchorID) (*model.Anchor, error) {
	return nil, model.ErrNotFound
}
func (s *stubAnchorStorer) List(_ context.Context, _ model.ScopeID) ([]model.Anchor, error) {
	return nil, nil
}

type stubArtifactCreator struct{}

func (s *stubArtifactCreator) SaveSummaryArtifact(_ context.Context, _ string, _ model.ScopeID) (model.ID, error) {
	return model.NewID(), nil
}
func (s *stubArtifactCreator) StoreEdge(_ context.Context, _ *model.Edge) error {
	return nil
}

type stubLifecycleTransitioner struct{}

func (s *stubLifecycleTransitioner) Transition(_ context.Context, _ model.ScopeID, _ model.LifecycleState) error {
	return nil
}

// ============================================================
// Tests
// ============================================================

func TestArchivePipeline_ArchiveEmptyScope(t *testing.T) {
	freezer := &stubFreezer{}
	installer := &stubInstaller{}
	graph := &stubGraphReader{}
	anchorStoreWriter := &stubAnchorStoreWriter{}
	anchorStorer := &stubAnchorStorer{}
	artCreator := &stubArtifactCreator{}
	lifecycle := &stubLifecycleTransitioner{}

	anchorCreator := knowledge.NewAnchorCreator(graph, anchorStoreWriter)
	p := NewArchivePipeline(freezer, installer, anchorCreator, anchorStorer, artCreator, lifecycle)

	anchorID, err := p.Archive(context.Background(), "test:empty:scope")
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if anchorID == nil {
		t.Fatal("Archive returned nil AnchorID")
	}
	if *anchorID == model.AnchorID(model.NilID) {
		t.Error("Archive returned zero AnchorID")
	}
}

func TestArchivePipeline_RestoreNotFound(t *testing.T) {
	freezer := &stubFreezer{}
	installer := &stubInstaller{}
	graph := &stubGraphReader{}
	anchorStoreWriter := &stubAnchorStoreWriter{}
	anchorStorer := &stubAnchorStorer{}
	artCreator := &stubArtifactCreator{}
	lifecycle := &stubLifecycleTransitioner{}

	anchorCreator := knowledge.NewAnchorCreator(graph, anchorStoreWriter)
	p := NewArchivePipeline(freezer, installer, anchorCreator, anchorStorer, artCreator, lifecycle)

	err := p.Restore(context.Background(), model.AnchorID(model.NewID()))
	if err == nil {
		t.Fatal("expected Restore to fail for unknown anchor")
	}
}
