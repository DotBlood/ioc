package model

import "time"

// EdgeType identifies the semantic type of a relationship.
type EdgeType uint8

const (
	// System edges (immutable, DAG, cycles forbidden).
	EdgeLineage   EdgeType = 1
	EdgeOwnership EdgeType = 2

	// Retrieval edges (mutable revisions, cycles allowed).
	EdgeRetrieval EdgeType = 3

	// Knowledge edges (mutable revisions, explicit semantics).
	EdgeReference   EdgeType = 4
	EdgeCitation    EdgeType = 5
	EdgeDerivedFrom EdgeType = 6
	EdgeRelatedTo   EdgeType = 7

	// Sequence edges (immutable, DAG).
	EdgeTemporal EdgeType = 8
)

func (t EdgeType) String() string {
	switch t {
	case EdgeLineage:
		return "lineage"
	case EdgeOwnership:
		return "ownership"
	case EdgeRetrieval:
		return "retrieval"
	case EdgeReference:
		return "reference"
	case EdgeCitation:
		return "citation"
	case EdgeDerivedFrom:
		return "derived_from"
	case EdgeRelatedTo:
		return "related_to"
	case EdgeTemporal:
		return "temporal"
	default:
		return "unknown"
	}
}

// EdgeDirection specifies the direction semantics of an edge.
type EdgeDirection uint8

const (
	DirectionDirected   EdgeDirection = 1
	DirectionUndirected EdgeDirection = 2
	DirectionSymmetric  EdgeDirection = 3
)

func (d EdgeDirection) String() string {
	switch d {
	case DirectionDirected:
		return "directed"
	case DirectionUndirected:
		return "undirected"
	case DirectionSymmetric:
		return "symmetric"
	default:
		return "unknown"
	}
}

// CyclePolicy controls whether cycles are allowed for an edge type.
type CyclePolicy uint8

const (
	CyclesForbidden CyclePolicy = 1
	CyclesAllowed   CyclePolicy = 2
)

// ReferenceState indicates the state of a cross-reference target.
type ReferenceState uint8

const (
	RefActive           ReferenceState = 1
	RefDangling         ReferenceState = 2
	RefArchived         ReferenceState = 3
	RefDeleted          ReferenceState = 4
	RefUnresolvedRemote ReferenceState = 5
)

// Edge is a relationship between two graph nodes.
type Edge struct {
	EdgeID    EdgeID
	Revision  RevisionNumber
	Type      EdgeType
	Direction EdgeDirection
	Source    ID
	Target    ID
	Valid     bool
	ValidFrom time.Time
	ValidTo   *time.Time
	Metadata  map[string]any
}

// IsValidAt checks if this edge was valid at time t.
func (e *Edge) IsValidAt(t time.Time) bool {
	if !e.Valid {
		return false
	}
	if t.Before(e.ValidFrom) {
		return false
	}
	if e.ValidTo != nil && t.After(*e.ValidTo) {
		return false
	}
	return true
}

// TargetKind identifies the type of entity an edge points to.
type TargetKind uint8

const (
	TargetArtifact TargetKind = 1
	TargetAnchor   TargetKind = 2
)

// EdgeTarget is a generalized reference target.
// TODO(v0.2): Replace string ID with strongly typed target identifiers.
type EdgeTarget struct {
	Kind TargetKind
	ID   string
}

// EdgeRevisionKey identifies a specific revision of an edge.
type EdgeRevisionKey struct {
	EdgeID   EdgeID
	Revision RevisionNumber
}

// EdgeConstraints specifies what is allowed for a given edge type.
type EdgeConstraints struct {
	Type            EdgeType
	Direction       EdgeDirection
	CyclePolicy     CyclePolicy
	SourceNodeTypes []NodeType
	TargetNodeTypes []NodeType
	CrossScope      bool
	CrossWorktree   bool
}

// DefaultEdgeConstraints returns the default constraints for an edge type.
func DefaultEdgeConstraints(t EdgeType) EdgeConstraints {
	switch t {
	case EdgeLineage:
		return EdgeConstraints{
			Type: t, Direction: DirectionDirected,
			CyclePolicy: CyclesForbidden, CrossScope: false, CrossWorktree: false,
			SourceNodeTypes: []NodeType{NodeTypeArtifact},
			TargetNodeTypes: []NodeType{NodeTypeArtifact},
		}
	case EdgeOwnership:
		return EdgeConstraints{
			Type: t, Direction: DirectionDirected,
			CyclePolicy: CyclesForbidden, CrossScope: false, CrossWorktree: false,
		}
	case EdgeRetrieval:
		return EdgeConstraints{
			Type: t, Direction: DirectionDirected,
			CyclePolicy: CyclesAllowed, CrossScope: true, CrossWorktree: true,
		}
	case EdgeReference:
		return EdgeConstraints{
			Type: t, Direction: DirectionUndirected,
			CyclePolicy: CyclesAllowed, CrossScope: true, CrossWorktree: true,
		}
	case EdgeCitation:
		return EdgeConstraints{
			Type: t, Direction: DirectionDirected,
			CyclePolicy: CyclesAllowed, CrossScope: true, CrossWorktree: true,
		}
	case EdgeDerivedFrom:
		return EdgeConstraints{
			Type: t, Direction: DirectionDirected,
			CyclePolicy: CyclesForbidden, CrossScope: true, CrossWorktree: false,
		}
	case EdgeRelatedTo:
		return EdgeConstraints{
			Type: t, Direction: DirectionUndirected,
			CyclePolicy: CyclesAllowed, CrossScope: true, CrossWorktree: true,
		}
	case EdgeTemporal:
		return EdgeConstraints{
			Type: t, Direction: DirectionDirected,
			CyclePolicy: CyclesForbidden, CrossScope: true, CrossWorktree: false,
		}
	default:
		return EdgeConstraints{Type: t, Direction: DirectionDirected, CyclePolicy: CyclesForbidden}
	}
}
