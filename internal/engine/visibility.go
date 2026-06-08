package engine

import "github.com/DotBlood/ioc/internal/core"

// visibleArtifacts implements the bottom-up visibility axis:
//   - all artifacts in the viewpoint scope,
//   - all artifacts in ancestor scopes (drill-up lineage),
//   - PUBLISHED artifacts in sibling scopes (the blackboard).
//
// tier != 0 filters to that tier. Unless includeArchived is set, sibling scopes
// that are Archived (superseded versions, e.g. the vN scope a CrossVersion left
// behind) are skipped — otherwise old-version conclusions compete on equal cosine
// footing with the current version (see docs/SUPERSESSION.md). The viewpoint and
// its ancestors are always kept (you explicitly stand there / drill up your lineage).
func (e *Engine) visibleArtifacts(scope core.ID, tier core.Tier, includeArchived bool) ([]core.Artifact, error) {
	seen := make(map[core.ID]bool)
	var out []core.Artifact
	add := func(a core.Artifact, requirePublished bool) {
		if seen[a.ID] {
			return
		}
		if requirePublished && !a.Published {
			return
		}
		if tier != 0 && a.Tier != tier {
			return
		}
		seen[a.ID] = true
		out = append(out, a)
	}

	// own scope
	own, err := e.meta.ArtifactsInScope(scope)
	if err != nil {
		return nil, err
	}
	for _, a := range own {
		add(a, false)
	}

	// ancestors
	ancs, err := e.ancestorsOf(scope)
	if err != nil {
		return nil, err
	}
	for _, anc := range ancs {
		arts, err := e.meta.ArtifactsInScope(anc.ID)
		if err != nil {
			return nil, err
		}
		for _, a := range arts {
			add(a, false)
		}
	}

	// siblings (published only)
	sibs, err := e.siblingsOf(scope)
	if err != nil {
		return nil, err
	}
	for _, s := range sibs {
		if s.Archived && !includeArchived {
			continue // superseded version: excluded from the current view
		}
		arts, err := e.meta.ArtifactsInScope(s.ID)
		if err != nil {
			return nil, err
		}
		for _, a := range arts {
			add(a, true)
		}
	}
	return out, nil
}

// collapsedCandidates implements RAPTOR's "collapsed tree" for IOC: the visible set
// (own + ancestors + published siblings) UNION every artifact in EVERY descendant
// scope, searched in one flat pass. Unlike flat retrieval it descends into child
// scopes (fixing the descendant-blindness `visibleArtifacts` has); unlike
// hierarchical it does NOT route coarse→fine, so it can never drop the correct scope.
// Descendant artifacts are NOT required to be published (you own your subtree) —
// matching coarseToFineCandidates; archived (superseded-version) descendant scopes are
// skipped unless includeArchived; tier filters as usual.
func (e *Engine) collapsedCandidates(scope core.ID, tier core.Tier, includeArchived bool) ([]core.Artifact, error) {
	out, err := e.visibleArtifacts(scope, tier, includeArchived)
	if err != nil {
		return nil, err
	}
	seen := make(map[core.ID]bool, len(out))
	for _, a := range out {
		seen[a.ID] = true
	}
	descs, err := e.descendantScopes(scope)
	if err != nil {
		return nil, err
	}
	for _, sc := range descs {
		if sc.Archived && !includeArchived {
			continue // superseded version scope: not in the current view
		}
		arts, err := e.meta.ArtifactsInScope(sc.ID)
		if err != nil {
			return nil, err
		}
		for _, a := range arts {
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

func (e *Engine) ancestorsOf(scope core.ID) ([]core.Scope, error) {
	var out []core.Scope
	s, err := e.meta.GetScope(scope)
	if err != nil {
		return nil, err
	}
	// visited guards against a corrupt parent chain (self-parent or an N-cycle),
	// which would otherwise loop forever. Seed with the start scope.
	visited := map[core.ID]bool{scope: true}
	cur := s.Parent
	for !cur.IsZero() {
		if visited[cur] {
			break // cycle in the parent chain — stop, return what we have
		}
		visited[cur] = true
		p, err := e.meta.GetScope(cur)
		if err != nil {
			break
		}
		out = append(out, p)
		cur = p.Parent
	}
	return out, nil
}

func (e *Engine) siblingsOf(scope core.ID) ([]core.Scope, error) {
	s, err := e.meta.GetScope(scope)
	if err != nil {
		return nil, err
	}
	kids, err := e.meta.ChildScopes(s.Parent)
	if err != nil {
		return nil, err
	}
	out := kids[:0]
	for _, k := range kids {
		if k.ID != scope {
			out = append(out, k)
		}
	}
	return out, nil
}
