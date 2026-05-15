package knowledge

import (
	"context"
	"time"

	"github.com/DotBlood/ioc/internal/model"
)

// PolicyEnforcer manages archive and retrieval policies.
// It defines: when to archive, what to retain, what to prune,
// and how retrieval should behave for different scope states.
type PolicyEnforcer struct {
	graph   PolicyStoreReader
	archive ArchivePolicy
}

// ArchivePolicy controls how scopes are archived.
type ArchivePolicy struct {
	RetentionMaxVersions int           // max projections per artifact (default 100)
	RetentionTTL         time.Duration // how long to keep old revisions (default 90d)
	AnchorInterval       int           // force anchor every N revisions (default 10)
	KeepAnchorsForever   bool          // never delete anchors (default true)
}

// DefaultArchivePolicy returns sensible defaults for v0.1.
func DefaultArchivePolicy() ArchivePolicy {
	return ArchivePolicy{
		RetentionMaxVersions: 100,
		RetentionTTL:         90 * 24 * time.Hour,
		AnchorInterval:       10,
		KeepAnchorsForever:   true,
	}
}

// NewPolicyEnforcer creates a new policy enforcer.
func NewPolicyEnforcer(graph PolicyStoreReader, archive ArchivePolicy) *PolicyEnforcer {
	return &PolicyEnforcer{
		graph:   graph,
		archive: archive,
	}
}

// ShouldArchive returns true if a scope should be archived based on policy.
func (e *PolicyEnforcer) ShouldArchive(ctx context.Context, scopeID model.ScopeID) (bool, error) {
	state, err := e.graph.ScopeState(ctx, scopeID)
	if err != nil {
		return false, err
	}
	if state.State != model.LifecycleActive {
		return false, nil
	}
	// Archive if inactive for longer than TTL.
	if state.ArchivedAt != nil && time.Since(*state.ArchivedAt) > e.archive.RetentionTTL {
		return true, nil
	}
	return false, nil
}

// PrunableRevisions returns a list of revision numbers that can be pruned
// per the retention policy.
func (e *PolicyEnforcer) PrunableRevisions(ctx context.Context, artifactID model.ID) ([]model.RevisionNumber, error) {
	// Walk all revisions of the artifact.
	// Keep the latest MaxVersions, prune everything older if past TTL.
	return nil, model.ErrNotImplemented
}

// DefaultRetrievalScope returns the default scope filter for retrieval queries.
func (e *PolicyEnforcer) DefaultRetrievalScope(activeScope model.ScopeID) model.ScopeFilter {
	return model.ScopeFilter{
		IncludeArchived: false,
		IncludeDetached: false,
		MaxDepth:        3,
	}
}

// PolicyStoreReader is the minimal interface knowledge/ uses for policy.
type PolicyStoreReader interface {
	ScopeState(ctx context.Context, scopeID model.ScopeID) (*model.ScopeState, error)
	ProjectionRevisions(ctx context.Context, artifactID model.ID) ([]model.RevisionNumber, error)
}
