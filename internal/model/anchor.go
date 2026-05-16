package model

import "time"

// AnchorID is a stable ULID for a structural snapshot.
type AnchorID ID

// AnchorKind distinguishes full snapshots from incremental diffs.
type AnchorKind uint8

const (
	AnchorFull AnchorKind = 0 // complete snapshot of scope state
	AnchorDiff AnchorKind = 1 // incremental changes since parent anchor
)

// Anchor is a structural snapshot of a scope.
//
// Invariants:
//   - Kind == Full:  ArtifactRefs + EdgeRefs + ProjectionRefs = COMPLETE snapshot
//   - Kind == Diff:  ArtifactRefs + EdgeRefs = added only,
//                    RemovedArtifactIDs + RemovedEdgeIDs = removed only,
//                    ProjectionRefs = changed only
//   - Kind == Diff  => ParentAnchor != zero (must reference a previous anchor)
//   - Kind == Full  => ParentAnchor == zero
type Anchor struct {
	AnchorID     AnchorID
	Kind         AnchorKind
	ScopeID      ScopeID
	CreatedAt    time.Time
	Revision     RevisionNumber
	ParentAnchor AnchorID // zero for full anchors

	// Full: complete list of artifact IDs in scope.
	// Diff: newly added artifacts since parent.
	ArtifactRefs []ID

	// Full: valid edge IDs at snapshot time.
	// Diff: newly added edges since parent.
	EdgeRefs []EdgeID

	// Full: snapshot of all projections.
	// Diff: only changed projections.
	ProjectionRefs []ProjectionKey

	// Diff only: artifacts and edges removed since parent.
	RemovedArtifactIDs []ID
	RemovedEdgeIDs     []EdgeID

	// Reference to separately stored embeddings manifest.
	EmbeddingManifestRef ContentHash

	ArtifactCount   int
	EdgeCount       int
	ProjectionCount int
}

// IsFull returns true if this is a full (non-diff) anchor.
func (a *Anchor) IsFull() bool { return a.Kind == AnchorFull }

// IsDiff returns true if this is an incremental diff anchor.
func (a *Anchor) IsDiff() bool { return a.Kind == AnchorDiff }
