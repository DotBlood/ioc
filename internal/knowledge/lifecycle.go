package knowledge

import (
	"context"
	"fmt"

	"github.com/DotBlood/ioc/internal/model"
)

// LifecycleManager enforces lifecycle transitions and invariant rules.
type LifecycleManager struct {
	graph LifecycleStoreWriter
}

// NewLifecycleManager creates a new lifecycle manager.
func NewLifecycleManager(graph LifecycleStoreWriter) *LifecycleManager {
	return &LifecycleManager{graph: graph}
}

// Transition attempts a lifecycle transition for a scope.
// Returns an error if the transition is invalid per the lifecycle model.
func (m *LifecycleManager) Transition(ctx context.Context, scopeID model.ScopeID, to model.LifecycleState) error {
	state, err := m.graph.ScopeState(ctx, scopeID)
	if err != nil {
		return fmt.Errorf("lifecycle transition: %w", err)
	}
	if !model.IsValidTransition(state.State, to) {
		return fmt.Errorf("%w: %s → %s", model.ErrInvalidTransition, state.State, to)
	}
	return m.graph.SetScopeState(ctx, scopeID, to)
}

// TransitionIfValid performs a transition only if it is valid, returning
// false (no error) if the transition is not applicable.
func (m *LifecycleManager) TransitionIfValid(ctx context.Context, scopeID model.ScopeID, to model.LifecycleState) (bool, error) {
	state, err := m.graph.ScopeState(ctx, scopeID)
	if err != nil {
		return false, err
	}
	if !model.IsValidTransition(state.State, to) {
		return false, nil
	}
	if err := m.graph.SetScopeState(ctx, scopeID, to); err != nil {
		return false, err
	}
	return true, nil
}

// EnforceInvariants checks all system invariants for a given scope.
// Called after every lifecycle transition to ensure consistency.
func (m *LifecycleManager) EnforceInvariants(ctx context.Context, scopeID model.ScopeID) []InvariantViolation {
	var violations []InvariantViolation

	// I1: the scope node exists.
	_, err := m.graph.ScopeState(ctx, scopeID)
	if err != nil {
		violations = append(violations, InvariantViolation{Invariant: "I1", Message: "scope state not found"})
	}

	// I3: scope containment — all children have consistent scope IDs.
	children, err := m.graph.ScopeChildren(ctx, scopeID)
	if err == nil {
		for _, child := range children {
			if !isChildScope(string(scopeID), string(child)) {
				violations = append(violations, InvariantViolation{
					Invariant: "I3",
					Message:   fmt.Sprintf("child %s has mismatched scope prefix", child),
				})
			}
		}
	}

	return violations
}

// InvariantViolation records a system invariant breach.
type InvariantViolation struct {
	Invariant string
	Message   string
}

func (v InvariantViolation) Error() string {
	return fmt.Sprintf("invariant %s: %s", v.Invariant, v.Message)
}

func isChildScope(parent, child string) bool {
	return len(child) > len(parent) && child[:len(parent)] == parent
}

// LifecycleStoreWriter is the minimal interface knowledge/ uses for lifecycle operations.
type LifecycleStoreWriter interface {
	ScopeState(ctx context.Context, scopeID model.ScopeID) (*model.ScopeState, error)
	SetScopeState(ctx context.Context, scopeID model.ScopeID, state model.LifecycleState) error
	ScopeChildren(ctx context.Context, scopeID model.ScopeID) ([]model.ScopeID, error)
}
