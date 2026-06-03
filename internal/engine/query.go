package engine

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/search"
)

const defaultTopK = 5

// Query runs progressive-disclosure retrieval from a viewpoint scope and returns
// the trace's query ID alongside the hits (use it with Trace). Mode selects
// hybrid (vector+BM25 via RRF, default) or vector-only. Hit.Score is always the
// cosine similarity (the confidence signal), regardless of fusion order; MinScore
// drops hits below it. Visibility is bottom-up (see visibleArtifacts).
func (e *Engine) Query(ctx context.Context, q core.Query) (core.ID, []core.Hit, error) {
	if _, err := e.meta.GetScope(q.Scope); err != nil {
		return core.NilID, nil, err
	}
	if q.Detail == 0 {
		q.Detail = core.DetailOverview
	}
	topK := q.TopK
	if topK <= 0 {
		topK = defaultTopK
	}

	qvec, err := e.embedQuery(ctx, q.Text)
	if err != nil {
		return core.NilID, nil, err
	}

	var arts []core.Artifact
	if q.Hierarchical {
		arts, err = e.coarseToFineCandidates(q.Scope, qvec, q.CoarseK, q.Tier)
	} else {
		arts, err = e.visibleArtifacts(q.Scope, q.Tier)
	}
	if err != nil {
		return core.NilID, nil, err
	}

	// Kind filter (e.g. only documents/files, or only reasonings).
	if len(q.Kinds) > 0 {
		kept := arts[:0]
		for _, a := range arts {
			if core.MatchesKinds(q.Kinds, a.Kind) {
				kept = append(kept, a)
			}
		}
		arts = kept
	}

	set := search.New()
	bm := search.NewBM25()
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
		bm.Add(a.ID.String(), a.Summary)
		byID[a.ID.String()] = a
	}

	// Cosine ranking — also the displayed/confidence score for every hit.
	vecRanked := set.Search(qvec, set.Len())
	cosineByID := make(map[string]float64, len(vecRanked))
	for _, r := range vecRanked {
		cosineByID[r.ID] = r.Score
	}

	// Ordering: vector (cosine) by default; hybrid opt-in fuses cosine + BM25 via RRF.
	ordered := vecRanked
	if q.Mode == core.ModeHybrid {
		ordered = search.RRF(60, vecRanked, bm.Search(q.Text, bm.Len()))
	}

	hits := make([]core.Hit, 0, topK)
	for _, r := range ordered {
		if len(hits) >= topK {
			break
		}
		a, ok := byID[r.ID]
		if !ok {
			continue
		}
		score := cosineByID[r.ID]
		if q.MinScore > 0 && score < q.MinScore {
			continue
		}
		h, err := e.buildHit(ctx, a, score, q.Detail)
		if err != nil {
			return core.NilID, nil, err
		}
		hits = append(hits, h)
	}

	queryID := core.NewID()
	_ = e.meta.PutTrace(core.TraceRecord{
		QueryID:    queryID,
		Scope:      q.Scope,
		Text:       q.Text,
		EmbModel:   e.embedder.Model(),
		VisibleSet: visible,
		Hits:       hits,
		CreatedAt:  time.Now(),
	})
	return queryID, hits, nil
}

// RecentTraces returns up to n most recent query traces, newest first.
func (e *Engine) RecentTraces(n int) ([]core.TraceRecord, error) {
	all, err := e.meta.ListTraces()
	if err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].QueryID.String() > all[j].QueryID.String() })
	if n > 0 && len(all) > n {
		all = all[:n]
	}
	return all, nil
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
