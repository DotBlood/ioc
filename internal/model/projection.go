package model

import "time"

// ArtifactProjection contains mutable metadata for an Artifact.
// Each mutation creates a new revision.
type ArtifactProjection struct {
	ArtifactID   ID             // FK → Artifact.ArtifactID
	Revision     RevisionNumber // monotonic per ArtifactID
	Summary      string
	EmbeddingRef EmbeddingRefID // FK → EmbeddingStore (zero = not computed)
	Keywords     []string
	ModelVersion string    // embedding model name + version
	Readiness    Readiness // current readiness level
	ValidFrom    time.Time
	ValidTo      *time.Time // nil = currently valid
	GeneratedAt  time.Time
}

// EmbeddingRefID references an embedding in the EmbeddingStore.
type EmbeddingRefID uint64

const NilEmbeddingRef EmbeddingRefID = 0

// ProjectionKey returns the composite key for this projection.
func (p *ArtifactProjection) ProjectionKey() ProjectionKey {
	return ProjectionKey{
		ArtifactID: p.ArtifactID,
		Revision:   p.Revision,
	}
}

// IsValidAt checks if this projection was valid at time t.
func (p *ArtifactProjection) IsValidAt(t time.Time) bool {
	if t.Before(p.ValidFrom) {
		return false
	}
	if p.ValidTo != nil && t.After(*p.ValidTo) {
		return false
	}
	return true
}
