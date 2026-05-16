package model

import "time"

// NodeType identifies the type of a graph node.
type NodeType uint8

const (
	NodeTypeWorktree  NodeType = 1
	NodeTypeWorkspace NodeType = 2
	NodeTypeSession   NodeType = 3
	NodeTypeArtifact  NodeType = 4
	NodeTypeSummary   NodeType = 5 // archive summary artifact
)

func (t NodeType) String() string {
	switch t {
	case NodeTypeWorktree:
		return "worktree"
	case NodeTypeWorkspace:
		return "workspace"
	case NodeTypeSession:
		return "session"
	case NodeTypeArtifact:
		return "artifact"
	case NodeTypeSummary:
		return "summary"
	default:
		return "unknown"
	}
}

// LineageRef describes a connection between artifact versions.
type LineageRef struct {
	Parent   ID // предыдущая версия (zero if first)
	Children []ID
}

// Artifact is an immutable piece of content with lineage tracking.
// Metadata lives in ArtifactProjection, not here.
type Artifact struct {
	ArtifactID  ID
	NodeType    NodeType
	Scope       ScopeID
	Lineage     LineageRef
	ContentHash ContentHash
	CreatedAt   time.Time
}

// ArtifactLineageRef returns the parent ID. Zero if no parent.
func (a *Artifact) ArtifactLineageRef() ID {
	return a.Lineage.Parent
}

// HasParent returns true if this artifact has a parent version.
func (a *Artifact) HasParent() bool {
	return !a.Lineage.Parent.IsZero()
}
