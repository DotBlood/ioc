package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/DotBlood/ioc/internal/core"
)

// Relate creates an author-declared directed edge from → to of the given kind
// (post-hoc; Push creates edges at write time via PushRequest.Relations). Edges are
// declared by the caller — IOC never infers them. Idempotent: re-relating the same
// (from, to, kind) is a no-op overwrite.
func (e *Engine) Relate(_ context.Context, from, to core.ID, kind core.RelationKind) error {
	if from == to {
		return fmt.Errorf("engine: relate: %w: an artifact cannot relate to itself", core.ErrInvalidInput)
	}
	if kind == "" {
		return fmt.Errorf("engine: relate: %w: empty relation kind", core.ErrInvalidInput)
	}
	if _, err := e.meta.GetArtifact(from); err != nil {
		return fmt.Errorf("engine: relate: from %s: %w", from, err)
	}
	if _, err := e.meta.GetArtifact(to); err != nil {
		return fmt.Errorf("engine: relate: to %s: %w", to, err)
	}
	return e.meta.PutEdge(core.Edge{From: from, To: to, Kind: kind, CreatedAt: time.Now()})
}

// Related walks the edge graph from an artifact and returns the connected artifacts
// as overview hits. dir selects edge direction (DirOut = this artifact's targets;
// DirIn = artifacts that point AT it — e.g. "what depends on X"; DirBoth = either).
// kinds filters by relation kind (empty = all). depth bounds the BFS (default 1).
// Superseded artifacts are excluded (current view); the start artifact is never
// included. A visited set bounds cyclic edge graphs.
func (e *Engine) Related(ctx context.Context, artifact core.ID, kinds []core.RelationKind, dir core.EdgeDir, depth int) ([]core.Hit, error) {
	if _, err := e.meta.GetArtifact(artifact); err != nil {
		return nil, fmt.Errorf("engine: related: %s: %w", artifact, err)
	}
	if depth <= 0 {
		depth = 1
	}
	kindOK := func(k core.RelationKind) bool {
		if len(kinds) == 0 {
			return true
		}
		for _, want := range kinds {
			if want == k {
				return true
			}
		}
		return false
	}

	visited := map[core.ID]bool{artifact: true}
	frontier := []core.ID{artifact}
	var order []core.ID // discovered neighbours, in BFS order
	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []core.ID
		for _, cur := range frontier {
			var edges []core.Edge
			if dir == core.DirOut || dir == core.DirBoth {
				out, err := e.meta.EdgesFrom(cur)
				if err != nil {
					return nil, err
				}
				edges = append(edges, out...)
			}
			if dir == core.DirIn || dir == core.DirBoth {
				in, err := e.meta.EdgesTo(cur)
				if err != nil {
					return nil, err
				}
				edges = append(edges, in...)
			}
			for _, ed := range edges {
				if !kindOK(ed.Kind) {
					continue
				}
				nb := ed.To // edge collected via EdgesFrom(cur): cur==From
				if ed.From != cur {
					nb = ed.From // collected via EdgesTo(cur): cur==To
				}
				if visited[nb] {
					continue
				}
				visited[nb] = true
				order = append(order, nb)
				next = append(next, nb)
			}
		}
		frontier = next
	}

	hits := make([]core.Hit, 0, len(order))
	for _, id := range order {
		a, err := e.meta.GetArtifact(id)
		if err != nil {
			continue // edge to a deleted artifact — skip rather than fail
		}
		if !a.SupersededBy.IsZero() {
			continue // current view only
		}
		h, err := e.buildHit(ctx, a, 0, core.DetailOverview)
		if err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, nil
}

// validateRelationTargets checks that every edge spec is well-formed and its target
// exists, BEFORE the pushing artifact is written (fail-fast, like Supersedes).
func (e *Engine) validateRelationTargets(specs []core.EdgeSpec) error {
	for _, s := range specs {
		if s.Kind == "" {
			return fmt.Errorf("%w: relation kind is empty", core.ErrInvalidInput)
		}
		if _, err := e.meta.GetArtifact(s.Target); err != nil {
			return fmt.Errorf("relation target %s: %w", s.Target, err)
		}
	}
	return nil
}

// createPushEdges writes the declared edges from a freshly pushed artifact. Targets
// were validated in validateRelationTargets; a fresh ID can never equal a target.
func (e *Engine) createPushEdges(from core.ID, specs []core.EdgeSpec) error {
	for _, s := range specs {
		if err := e.meta.PutEdge(core.Edge{From: from, To: s.Target, Kind: s.Kind, CreatedAt: time.Now()}); err != nil {
			return err
		}
	}
	return nil
}
