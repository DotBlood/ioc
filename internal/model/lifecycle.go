package model

import "time"

// LifecycleState represents the current state of a scope.
type LifecycleState uint8

const (
	LifecycleDraft    LifecycleState = 1
	LifecycleActive   LifecycleState = 2
	LifecycleArchived LifecycleState = 3
	LifecycleDetached LifecycleState = 4
	LifecycleDeleted  LifecycleState = 5
)

func (s LifecycleState) String() string {
	switch s {
	case LifecycleDraft:
		return "draft"
	case LifecycleActive:
		return "active"
	case LifecycleArchived:
		return "archived"
	case LifecycleDetached:
		return "detached"
	case LifecycleDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

// LifecycleTransition represents a valid state transition.
type LifecycleTransition struct {
	From LifecycleState
	To   LifecycleState
}

// ValidTransitions returns all valid state transitions.
func ValidTransitions() []LifecycleTransition {
	return []LifecycleTransition{
		{From: LifecycleDraft, To: LifecycleActive},
		{From: LifecycleActive, To: LifecycleArchived},
		{From: LifecycleActive, To: LifecycleDetached},
		{From: LifecycleArchived, To: LifecycleActive},
		{From: LifecycleArchived, To: LifecycleDeleted},
		{From: LifecycleDetached, To: LifecycleActive},
		{From: LifecycleDetached, To: LifecycleDeleted},
	}
}

// IsValidTransition checks if a transition is valid.
func IsValidTransition(from, to LifecycleState) bool {
	for _, t := range ValidTransitions() {
		if t.From == from && t.To == to {
			return true
		}
	}
	return false
}

// ScopeState holds the lifecycle state and metadata for a scope.
type ScopeState struct {
	ScopeID    ScopeID
	Type       NodeType
	State      LifecycleState
	ParentID   ID
	CreatedAt  time.Time
	ArchivedAt *time.Time
}

// NodeReference stores a reference with its resolution state.
type NodeReference struct {
	SourceID ID
	TargetID ID
	EdgeID   EdgeID
	State    ReferenceState
}
