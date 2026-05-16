package graph

import (
	"context"

	"github.com/DotBlood/ioc/internal/model"
)

// StoreWriter is the interface the physical graph engine exposes
// for writing data. knowledge/ and pipelines use this interface.
type StoreWriter interface {
	AddNode(ctx context.Context, n *model.Artifact) error
	RemoveNode(ctx context.Context, id model.ID) error
	AddEdge(ctx context.Context, e *model.Edge) error
}

// StoreReader is the interface the physical graph engine exposes
// for reading data.
type StoreReader interface {
	Node(ctx context.Context, id model.ID) (*model.Artifact, error)
	Edge(ctx context.Context, id model.EdgeID) (*model.Edge, error)
	EdgesOut(ctx context.Context, sourceID model.ID, edgeType model.EdgeType) ([]model.Edge, error)
	EdgesIn(ctx context.Context, targetID model.ID, edgeType model.EdgeType) ([]model.Edge, error)
	BFS(ctx context.Context, start model.ID, maxDepth int) ([]model.ID, error)
	DFS(ctx context.Context, start model.ID, maxDepth int) ([]model.ID, error)
	NodesByType(ctx context.Context, nodeType model.NodeType) ([]model.ID, error)
	Snapshot() ([]byte, error)
}

// ScopeFreezer manages write-freeze for a scope during snapshot/archive.
type ScopeFreezer interface {
	FreezeScope(scopeID model.ScopeID) error
	UnfreezeScope(scopeID model.ScopeID)
}

// SnapshotInstaller installs a fully-resolved graph state into the engine.
// Used by knowledge/ during restore.
type SnapshotInstaller interface {
	InstallSnapshot(nodes map[model.ID]*model.Artifact, edges map[model.EdgeID]*model.Edge)
	RebuildRuntimeState()
}

// Compile-time check: *StatefulGraph implements all interfaces.
var _ StoreReader = (*StatefulGraph)(nil)
var _ StoreWriter = (*StatefulGraph)(nil)
var _ ScopeFreezer = (*StatefulGraph)(nil)
var _ SnapshotInstaller = (*StatefulGraph)(nil)
