package engine

import (
	"context"
	"fmt"
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
// cosine similarity, regardless of fusion order; MinScore is a cosine pre-gate on
// candidates (applied before rerank, not a post-rerank drop).
// When q.Rerank is set, Hit.RerankScore carries the (sigmoid-normalized)
// cross-encoder score — the signal that actually ordered the hits, and the one
// confidence (weak_match/margin) should read. Visibility is bottom-up (see
// visibleArtifacts).
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
	// Defensive: a query vector whose dimension differs from the store means a
	// wrong embedder slipped past the Open/model guards; cosine would silently
	// return 0 for every artifact, so fail loudly instead.
	if e.emb != nil && len(qvec) != e.emb.Dims() {
		return core.NilID, nil, fmt.Errorf("engine: query: %w: query is %d-dim but store is %d-dim", core.ErrInvalidInput, len(qvec), e.emb.Dims())
	}

	var arts []core.Artifact
	if q.Hierarchical {
		arts, err = e.coarseToFineCandidates(q.Scope, qvec, q.CoarseK, q.Tier, q.IncludeSuperseded)
	} else {
		arts, err = e.visibleArtifacts(q.Scope, q.Tier, q.IncludeSuperseded)
	}
	if err != nil {
		return core.NilID, nil, err
	}

	// Currency: drop superseded artifacts from the candidate set so the current
	// distilled truth is what competes (the agent gets the up-to-date conclusion,
	// not a stale-but-similar one). IncludeSuperseded opts into the full history.
	if !q.IncludeSuperseded {
		kept := arts[:0]
		for _, a := range arts {
			if a.SupersededBy.IsZero() {
				kept = append(kept, a)
			}
		}
		arts = kept
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
		// BM25 ranks the document CONTENT, not the "path:lines — first line"
		// label (rankText loads CAS for documents); only build it in hybrid mode
		// so vector-only queries pay no decompression cost. Deliberate cost: in
		// hybrid this decompresses every visible document chunk per query (O(N)
		// CAS loads) — acceptable for hybrid (opt-in, and label-only BM25 is simply
		// wrong for documents); a lexical-text cache is the lever if it ever bites.
		if q.Mode == core.ModeHybrid {
			bm.Add(a.ID.String(), e.rankText(ctx, a))
		}
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

	// MinScore is a COSINE gate, applied here on the candidate set — before rerank,
	// not as a post-rerank drop. Applying it after rerank would let the cross-encoder
	// promote a low-cosine artifact to the top and then silently delete it by cosine
	// (the same cross-signal incoherence H3 fixes for weak_match/margin). As a
	// pre-gate the semantics stay "remove low-cosine candidates," and rerank only
	// reorders what survives.
	if q.MinScore > 0 {
		kept := ordered[:0]
		for _, r := range ordered {
			if cosineByID[r.ID] >= q.MinScore {
				kept = append(kept, r)
			}
		}
		ordered = kept
	}

	// Optional cross-encoder rerank of the top candidates over their CONTENT
	// (rerankTop → rankText). It reorders; Hit.Score stays cosine, but the
	// (sigmoid-normalized) rerank score is carried into each Hit so confidence
	// (weak_match/margin) reads the signal that actually ordered the results.
	// On rerank error, keep the existing order rather than fail.
	//
	// Known, deliberate limit: rerank only sees the top RerankN cosine candidates,
	// so an artifact that cosine ranks below that window is never promoted (inherent
	// to retrieve-then-rerank; widen RerankN to trade compute for recall). NaN/Inf
	// guarding on reranker output is deferred to the V8 input-validation hardening.
	rerankByID := map[string]float64(nil)
	if q.Rerank && e.reranker != nil {
		if reranked, rerr := e.rerankTop(ctx, q.Text, ordered, byID, q.RerankN); rerr == nil {
			ordered = reranked
			rerankByID = make(map[string]float64, len(reranked))
			for _, r := range reranked {
				rerankByID[r.ID] = sigmoid(r.Score)
			}
		}
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
		h, err := e.buildHit(ctx, a, score, q.Detail)
		if err != nil {
			return core.NilID, nil, err
		}
		if rs, ok := rerankByID[r.ID]; ok {
			h.RerankScore = &rs
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
		Artifact:     a.ID,
		Scope:        a.Scope,
		ScopePath:    e.scopePath(a.Scope),
		Kind:         a.Kind,
		Tier:         a.Tier,
		Summary:      a.Summary,
		Score:        score,
		Meta:         a.Meta,
		SupersededBy: a.SupersededBy,
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
	visited := map[core.ID]bool{} // bound a corrupt/cyclic parent chain
	cur := scope
	for !cur.IsZero() {
		if visited[cur] {
			break
		}
		visited[cur] = true
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
