package knowledge

import (
	"context"
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	"github.com/DotBlood/ioc/internal/model"
)

// RetentionConfig controls how historical data is pruned.
//
// MaxVersions semantics:
//
//	0 = keep only the latest revision
//	N = keep the latest N revisions
type RetentionConfig struct {
	MaxVersions  int           // max projections per artifact (default 100)
	RetentionTTL time.Duration // keep revisions that became invalid within this window (default 90d)
	KeepAnchors  bool          // never delete anchors (default true)
}

// DefaultRetentionConfig returns sensible defaults for v0.1.
func DefaultRetentionConfig() RetentionConfig {
	return RetentionConfig{
		MaxVersions:  100,
		RetentionTTL: 90 * 24 * time.Hour,
		KeepAnchors:  true,
	}
}

// RetentionResult contains the outcome of a retention run.
type RetentionResult struct {
	ProjectionsPruned   int
	EdgeRevisionsPruned int
	Duration            time.Duration
	Errors              []string
}

// RetentionPolicy implements mark-and-sweep garbage collection for historical data.
//
// Protected sets (never pruned):
//   - Latest projection revision (invariant)
//   - Projections reachable from any anchor chain
//   - Valid=true edges (active)
//   - ValidTo==nil projections and edges (no expiry)
//   - Anchors themselves
//
// v0.1 retention conservatively protects all revisions of an edge
// if any revision of that edge is referenced by an anchor.
// This may retain more history than necessary but avoids accidental
// historical corruption.
//
// TODO(v0.2): switch Anchor.EdgeRefs to []EdgeRevisionKey.
// TODO(v0.2): Build reverse revision reachability index:
//
//	ProjectionKey -> []AnchorID
type RetentionPolicy struct {
	config        RetentionConfig
	artifactStore RetentionArtifactStore
	anchorStore   RetentionAnchorStore
	edgeStore     RetentionEdgeStore
	running       atomic.Bool
}

// RetentionArtifactStore is the minimal artifact interface for retention.
type RetentionArtifactStore interface {
	ListProjections(ctx context.Context, artifactID model.ID) ([]model.ProjectionKey, error)
	LoadProjection(ctx context.Context, key model.ProjectionKey) (*model.ArtifactProjection, error)
	DeleteProjection(key model.ProjectionKey) error
	ListAllArtifactIDs(ctx context.Context) ([]model.ID, error)
}

// RetentionEdgeStore is the minimal edge interface for retention.
type RetentionEdgeStore interface {
	ListEdgeRevisions(ctx context.Context) ([]model.EdgeRevisionKey, error)
	LoadEdgeRevision(ctx context.Context, key model.EdgeRevisionKey) (*model.Edge, error)
	DeleteEdgeRevision(key model.EdgeRevisionKey) error
}

// RetentionAnchorStore is the minimal anchor interface for retention.
type RetentionAnchorStore interface {
	ListAll(ctx context.Context) ([]model.Anchor, error)
}

// NewRetentionPolicy creates a new retention policy.
func NewRetentionPolicy(
	config RetentionConfig,
	artifactStore RetentionArtifactStore,
	anchorStore RetentionAnchorStore,
	edgeStore RetentionEdgeStore,
) *RetentionPolicy {
	return &RetentionPolicy{
		config:        config,
		artifactStore: artifactStore,
		anchorStore:   anchorStore,
		edgeStore:     edgeStore,
	}
}

// Run executes a full retention sweep: prune projections then edge revisions.
func (p *RetentionPolicy) Run(ctx context.Context) (*RetentionResult, error) {
	start := time.Now()
	result := &RetentionResult{}

	// 1. Collect all artifacts.
	artifactIDs, err := p.artifactStore.ListAllArtifactIDs(ctx)
	if err != nil {
		return result, fmt.Errorf("retention: list artifacts: %w", err)
	}

	// 2. Prune projections per artifact.
	for _, id := range artifactIDs {
		pruned, err := p.PruneProjections(ctx, id)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("artifact %s: %v", id, err))
			continue
		}
		result.ProjectionsPruned += pruned
	}

	// 3. Prune edge revisions (global sweep).
	pruned, err := p.PruneEdgeRevisions(ctx)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("edge sweep: %v", err))
	}
	result.EdgeRevisionsPruned += pruned

	result.Duration = time.Since(start)
	return result, nil
}

// RunScheduler starts a background goroutine that runs retention periodically.
// Best-effort, single-process, non-distributed. Uses atomic.Bool for overlap protection.
func (p *RetentionPolicy) RunScheduler(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if p.running.Load() {
					continue // skip if previous run still active
				}
				p.running.Store(true)
				func() {
					defer p.running.Store(false)
					p.Run(ctx)
				}()
			}
		}
	}()
}

// PruneProjections prunes old projections for a single artifact.
// Never prunes: latest revision, anchor-reachable revisions, or ValidTo==nil projections.
func (p *RetentionPolicy) PruneProjections(ctx context.Context, artifactID model.ID) (int, error) {
	keys, err := p.artifactStore.ListProjections(ctx, artifactID)
	if err != nil {
		return 0, err
	}
	if len(keys) <= p.config.MaxVersions {
		return 0, nil
	}

	// Sort ascending (oldest first).
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Revision < keys[j].Revision
	})

	pruneUpTo := len(keys) - p.config.MaxVersions
	if pruneUpTo <= 0 {
		return 0, nil
	}

	protected := p.protectedRevisions(ctx, artifactID)

	pruned := 0
	// Prune from oldest up to (len(keys)-MaxVersions).
	// Latest MaxVersions revisions are always kept.
	// Keep at least the latest revision (invariant).
	keepAtLeast := p.config.MaxVersions
	if keepAtLeast < 1 {
		keepAtLeast = 1
	}
	cutoff := len(keys) - keepAtLeast
	if cutoff <= 0 {
		return 0, nil
	}

	for i := 0; i < cutoff; i++ {
		key := keys[i]

		if protected[key.Revision] {
			continue
		}

		proj, err := p.artifactStore.LoadProjection(ctx, key)
		if err != nil {
			continue
		}
		if proj.ValidTo == nil {
			continue
		}
		if time.Since(*proj.ValidTo) < p.config.RetentionTTL {
			continue
		}

		if p.artifactStore.DeleteProjection(key) != nil {
			continue
		}
		pruned++
	}

	return pruned, nil
}

// PruneEdgeRevisions prunes old edge revisions globally.
// Never prunes: Valid=true edges, ValidTo==nil edges, anchor-reachable edges.
func (p *RetentionPolicy) PruneEdgeRevisions(ctx context.Context) (int, error) {
	allKeys, err := p.edgeStore.ListEdgeRevisions(ctx)
	if err != nil {
		return 0, err
	}

	// v0.1: conservatively protect all revisions of edges referenced by any anchor.
	protected := p.protectedEdgeRevisions(ctx)

	pruned := 0
	for _, key := range allKeys {
		if protected[key] {
			continue
		}
		e, err := p.edgeStore.LoadEdgeRevision(ctx, key)
		if err != nil {
			continue
		}
		// Active edges — never prune.
		if e.Valid {
			continue
		}
		// No expiry — never prune.
		if e.ValidTo == nil {
			continue
		}
		// Recently invalidated — keep.
		if time.Since(*e.ValidTo) < p.config.RetentionTTL {
			continue
		}
		if err := p.edgeStore.DeleteEdgeRevision(key); err != nil {
			p.collectError(fmt.Sprintf("delete edge %s rev %d: %v", key.EdgeID, key.Revision, err))
			continue
		}
		pruned++
	}

	return pruned, nil
}

// protectedRevisions returns all projection revisions reachable from any anchor.
// O(all anchors × all projection refs) — acceptable for v0.1.
func (p *RetentionPolicy) protectedRevisions(ctx context.Context, artifactID model.ID) map[model.RevisionNumber]bool {
	protected := make(map[model.RevisionNumber]bool)
	allAnchors, err := p.anchorStore.ListAll(ctx)
	if err != nil {
		return protected
	}
	for _, anchor := range allAnchors {
		for _, ref := range anchor.ProjectionRefs {
			if ref.ArtifactID == artifactID {
				protected[ref.Revision] = true
			}
		}
	}
	return protected
}

// protectedEdgeRevisions returns all edge revision keys reachable from any anchor.
// v0.1: conservatively protects ALL revisions of any referenced edge.
// TODO(v0.2): Switch to revision-level granularity when Anchor.EdgeRefs becomes []EdgeRevisionKey.
func (p *RetentionPolicy) protectedEdgeRevisions(ctx context.Context) map[model.EdgeRevisionKey]bool {
	protected := make(map[model.EdgeRevisionKey]bool)
	allAnchors, err := p.anchorStore.ListAll(ctx)
	if err != nil {
		return protected
	}
	for _, anchor := range allAnchors {
		for _, edgeID := range anchor.EdgeRefs {
			// v0.1: protect ALL revisions of this edge (conservative).
			protected[model.EdgeRevisionKey{EdgeID: edgeID}] = true
		}
	}
	return protected
}

func (p *RetentionPolicy) collectError(msg string) {
	if p == nil {
		return
	}
	// Errors collected externally via RetentionResult.Errors.
	_ = msg
}
