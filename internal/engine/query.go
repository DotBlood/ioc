package engine

import (
	"context"
	"strings"
	"time"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/search"
)

const defaultTopK = 5

// Query runs progressive-disclosure retrieval from a viewpoint scope.
// At DetailOverview it returns summaries + scores only (cheap); DetailRaw also
// loads full content from CAS. Visibility is bottom-up (see visibleArtifacts).
func (e *Engine) Query(ctx context.Context, q core.Query) ([]core.Hit, error) {
	if _, err := e.meta.GetScope(q.Scope); err != nil {
		return nil, err
	}
	if q.Detail == 0 {
		q.Detail = core.DetailOverview
	}
	topK := q.TopK
	if topK <= 0 {
		topK = defaultTopK
	}

	qvec, err := e.embedText(ctx, q.Text)
	if err != nil {
		return nil, err
	}

	arts, err := e.visibleArtifacts(q.Scope, q.Tier)
	if err != nil {
		return nil, err
	}

	set := search.New()
	byID := make(map[string]core.Artifact, len(arts))
	visible := make([]core.ID, 0, len(arts))
	for _, a := range arts {
		visible = append(visible, a.ID)
		if a.EmbRef.IsZero() || e.emb == nil {
			continue
		}
		vec, err := e.emb.Get(a.EmbRef)
		if err != nil {
			continue
		}
		set.Add(a.ID.String(), vec)
		byID[a.ID.String()] = a
	}

	results := set.Search(qvec, topK)
	hits := make([]core.Hit, 0, len(results))
	for _, r := range results {
		a := byID[r.ID]
		h, err := e.buildHit(ctx, a, r.Score, q.Detail)
		if err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}

	// Record the trace (inspectability).
	_ = e.meta.PutTrace(core.TraceRecord{
		QueryID:    core.NewID(),
		Scope:      q.Scope,
		Text:       q.Text,
		EmbModel:   e.embedder.Model(),
		VisibleSet: visible,
		Hits:       hits,
		CreatedAt:  time.Now(),
	})
	return hits, nil
}

// Drill re-fetches a single artifact at a higher detail level (e.g. raw content).
func (e *Engine) Drill(ctx context.Context, artifactID core.ID, to core.Detail) (core.Hit, error) {
	a, err := e.meta.GetArtifact(artifactID)
	if err != nil {
		return core.Hit{}, err
	}
	return e.buildHit(ctx, a, 0, to)
}

// SiblingOverview returns published artifacts of sibling scopes as cheap hits
// (summary + embedding only) — the blackboard a scope reads.
func (e *Engine) SiblingOverview(ctx context.Context, scope core.ID) ([]core.Hit, error) {
	sibs, err := e.siblingsOf(scope)
	if err != nil {
		return nil, err
	}
	var hits []core.Hit
	for _, s := range sibs {
		arts, err := e.meta.ArtifactsInScope(s.ID)
		if err != nil {
			return nil, err
		}
		for _, a := range arts {
			if !a.Published {
				continue
			}
			h, err := e.buildHit(ctx, a, 0, core.DetailOverview)
			if err != nil {
				return nil, err
			}
			hits = append(hits, h)
		}
	}
	return hits, nil
}

// Ancestors returns the scope's ancestor chain (nearest parent first).
func (e *Engine) Ancestors(_ context.Context, scope core.ID) ([]core.Scope, error) {
	return e.ancestorsOf(scope)
}

// Publish makes an artifact visible to siblings via the parent blackboard.
func (e *Engine) Publish(_ context.Context, artifactID core.ID) error {
	a, err := e.meta.GetArtifact(artifactID)
	if err != nil {
		return err
	}
	a.Published = true
	return e.meta.PutArtifact(a)
}

// visibleArtifacts implements the bottom-up visibility axis:
//   - all artifacts in the viewpoint scope,
//   - all artifacts in ancestor scopes (drill-up lineage),
//   - PUBLISHED artifacts in sibling scopes (the blackboard).
//
// tier != 0 filters to that tier.
func (e *Engine) visibleArtifacts(scope core.ID, tier core.Tier) ([]core.Artifact, error) {
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

func (e *Engine) buildHit(ctx context.Context, a core.Artifact, score float64, detail core.Detail) (core.Hit, error) {
	h := core.Hit{
		Artifact:  a.ID,
		Scope:     a.Scope,
		ScopePath: e.scopePath(a.Scope),
		Kind:      a.Kind,
		Tier:      a.Tier,
		Summary:   a.Summary,
		Score:     score,
	}
	if detail >= core.DetailRaw && !a.Content.IsZero() {
		data, err := e.cas.Load(ctx, a.Content)
		if err != nil {
			return core.Hit{}, err
		}
		h.Content = data
	}
	return h, nil
}

func (e *Engine) scopePath(scope core.ID) string {
	var parts []string
	cur := scope
	for !cur.IsZero() {
		s, err := e.meta.GetScope(cur)
		if err != nil {
			break
		}
		label := s.Title
		if label == "" {
			label = s.Role.String()
		}
		parts = append([]string{label}, parts...)
		cur = s.Parent
	}
	return strings.Join(parts, "/")
}
