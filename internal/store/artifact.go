package store

import (
	"context"
	"fmt"

	"github.com/DotBlood/ioc/internal/model"
)

// ArtifactStore provides a high-level API for artifacts, projections,
// and revision DAGs, backed by DiskStore.
type ArtifactStore struct {
	disk *DiskStore
}

// NewArtifactStore creates a new ArtifactStore backed by the given DiskStore.
func NewArtifactStore(disk *DiskStore) *ArtifactStore {
	return &ArtifactStore{disk: disk}
}

// ============================================================
// Artifact
// ============================================================

// SaveArtifact persists an artifact. Returns ErrDuplicate if already exists.
func (s *ArtifactStore) SaveArtifact(ctx context.Context, art *model.Artifact) error {
	if err := s.disk.SaveNode(art); err != nil {
		return fmt.Errorf("artifact store: save: %w", err)
	}
	// Update type index.
	if err := s.disk.addToTypeIndex(art.NodeType, art.ArtifactID); err != nil {
		return fmt.Errorf("artifact store: update type index: %w", err)
	}
	return nil
}

// LoadArtifact retrieves an artifact by ID.
func (s *ArtifactStore) LoadArtifact(ctx context.Context, id model.ID) (*model.Artifact, error) {
	return s.disk.LoadNode(id)
}

// DeleteArtifact removes an artifact.
func (s *ArtifactStore) DeleteArtifact(ctx context.Context, id model.ID) error {
	// Load first to get NodeType for index cleanup.
	art, err := s.disk.LoadNode(id)
	if err != nil {
		return err
	}
	if err := s.disk.removeFromTypeIndex(art.NodeType, id); err != nil {
		return fmt.Errorf("artifact store: remove type index: %w", err)
	}
	return s.disk.DeleteNode(id)
}

// ListArtifactsByType returns all artifact IDs of the given type.
func (s *ArtifactStore) ListArtifactsByType(ctx context.Context, nodeType model.NodeType) ([]model.ID, error) {
	return s.disk.listTypeIndex(nodeType)
}

// SaveArtifactWithProjection atomically saves an artifact and its initial projection.
func (s *ArtifactStore) SaveArtifactWithProjection(ctx context.Context, art *model.Artifact, proj *model.ArtifactProjection) error {
	if err := s.SaveArtifact(ctx, art); err != nil {
		return err
	}
	return s.SaveProjection(ctx, proj)
}

// ============================================================
// Projection
// ============================================================

// SaveProjection persists a projection.
func (s *ArtifactStore) SaveProjection(ctx context.Context, proj *model.ArtifactProjection) error {
	return s.disk.SaveProjection(proj)
}

// LoadProjection retrieves a projection by key.
func (s *ArtifactStore) LoadProjection(ctx context.Context, key model.ProjectionKey) (*model.ArtifactProjection, error) {
	return s.disk.LoadProjection(key)
}

// ListProjections returns all projection keys for an artifact.
func (s *ArtifactStore) ListProjections(ctx context.Context, artifactID model.ID) ([]model.ProjectionKey, error) {
	return s.disk.ListProjectionKeys(artifactID)
}

// LatestProjection returns the projection with the highest revision number.
func (s *ArtifactStore) LatestProjection(ctx context.Context, artifactID model.ID) (*model.ArtifactProjection, error) {
	keys, err := s.disk.ListProjectionKeys(artifactID)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("artifact %s: %w", artifactID, model.ErrNotFound)
	}

	var latest model.ProjectionKey
	for _, k := range keys {
		if k.Revision > latest.Revision {
			latest = k
		}
	}
	return s.disk.LoadProjection(latest)
}

// ============================================================
// Revision DAG
// ============================================================

// SaveRevisionDAG persists a revision DAG for an artifact.
func (s *ArtifactStore) SaveRevisionDAG(ctx context.Context, artifactID model.ID, dag *RevisionDAG) error {
	return s.disk.saveRevisionDAG(artifactID, dag)
}

// LoadRevisionDAG retrieves a revision DAG for an artifact.
func (s *ArtifactStore) LoadRevisionDAG(ctx context.Context, artifactID model.ID) (*RevisionDAG, error) {
	return s.disk.loadRevisionDAG(artifactID)
}
