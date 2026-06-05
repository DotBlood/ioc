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

func (e *Engine) ancestorsOf(scope core.ID) ([]core.Scope, error) {
	var out []core.Scope
	s, err := e.meta.GetScope(scope)
	if err != nil {
		return nil, err
	}
	cur := s.Parent
	for !cur.IsZero() {
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
