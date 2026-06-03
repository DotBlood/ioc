package engine

import (
	"context"
	"fmt"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/search"
)

// RollupScope attaches an LLM-authored summary (and its embedding) that
// represents the scope's contents. Hierarchical retrieval ranks these rollups
// to pick which scopes to search — the "coarse" stage of coarse→fine.
func (e *Engine) RollupScope(ctx context.Context, scope core.ID, summary string) error {
	if summary == "" {
		return fmt.Errorf("engine: rollup: %w: empty summary", core.ErrInvalidInput)
	}
	s, err := e.meta.GetScope(scope)
	if err != nil {
		return err
	}
	ref, err := e.storeSummaryEmbedding(ctx, summary)
	if err != nil {
		return err
	}
	s.RollupSummary = summary
	s.RollupEmbRef = ref
	return e.meta.PutScope(s)
}

// descendantScopes returns all scopes under root (children, recursively).
func (e *Engine) descendantScopes(root core.ID) ([]core.Scope, error) {
	var out []core.Scope
	queue := []core.ID{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		kids, err := e.meta.ChildScopes(cur)
		if err != nil {
			return nil, err
		}
		for _, k := range kids {
			out = append(out, k)
			queue = append(queue, k.ID)
		}
	}
	return out, nil
}

// coarseToFineCandidates implements the coarse stage: rank descendant-scope
// rollups against the query vector, keep the top coarseK scopes, and return the
// artifacts within them (plus the viewpoint's own). Falls back to the flat
// visible set if no scope has a rollup.
func (e *Engine) coarseToFineCandidates(viewpoint core.ID, qvec []float32, coarseK int, tier core.Tier) ([]core.Artifact, error) {
	if coarseK <= 0 {
		coarseK = 6 // sweet spot at ~18 clusters: narrow enough to cut cross-scope noise
	}
	descs, err := e.descendantScopes(viewpoint)
	if err != nil {
		return nil, err
	}
	rset := search.New()
	for _, sc := range descs {
		if sc.RollupEmbRef.IsZero() || e.emb == nil {
			continue
		}
		vec, err := e.emb.Get(sc.RollupEmbRef)
		if err != nil {
			continue
		}
		rset.Add(sc.ID.String(), vec)
	}
	if rset.Len() == 0 {
		return e.visibleArtifacts(viewpoint, tier) // no rollups → flat
	}

	selected := map[core.ID]bool{viewpoint: true}
	for _, r := range rset.Search(qvec, coarseK) {
		if id, err := core.ParseID(r.ID); err == nil {
			selected[id] = true
		}
	}

	seen := map[core.ID]bool{}
	var out []core.Artifact
	for sid := range selected {
		as, err := e.meta.ArtifactsInScope(sid)
		if err != nil {
			return nil, err
		}
		for _, a := range as {
			if seen[a.ID] {
				continue
			}
			if tier != 0 && a.Tier != tier {
				continue
			}
			seen[a.ID] = true
			out = append(out, a)
		}
	}
	return out, nil
}
