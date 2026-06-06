package engine

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/search"
)

const defaultTopK = 5

// recencyWeight is the (1−α) blend factor for the optional recency tie-breaker: the
// recency term contributes at most this fraction, cosine the rest — small enough to
// only break near-ties, not override a clearly-stronger semantic match.
const recencyWeight = 0.15

// clockNow is the time source for the recency tie-breaker; a var so tests can pin it.
var clockNow = time.Now

// graphSeedK is how many top-cosine candidates seed the graph-aware boost — the query's
// "strong hits". The PPR restart distribution puts all its mass on these (∝ their cosine)
// and zero elsewhere, so candidates are lifted for lying on a path FROM what the query
// matched, not for global degree. Package var so a test can lower it.
var graphSeedK = 5

// graphPPRSteps / graphPPRDamping tune the query-seeded Personalized PageRank: a few
// degree-normalized walk steps with teleport probability (1−damping) back to the seeds.
// Few steps keep the walk local (multi-hop, not global). Package vars so tests can pin them.
var (
	graphPPRSteps   = 2
	graphPPRDamping = 0.5
)

// blendRecency re-sorts candidates by α·cosine + (1−α)·0.5^(ageDays/halfLife). It
// only reorders — the displayed Hit.Score stays cosine (like the rerank path).
func blendRecency(ordered []search.Result, cosineByID map[string]float64, byID map[string]core.Artifact, halfLifeDays float64) []search.Result {
	now := clockNow()
	out := make([]search.Result, len(ordered))
	for i, r := range ordered {
		ageDays := now.Sub(byID[r.ID].CreatedAt).Hours() / 24
		if ageDays < 0 {
			ageDays = 0
		}
		decay := math.Pow(0.5, ageDays/halfLifeDays)
		out[i] = search.Result{ID: r.ID, Score: (1-recencyWeight)*cosineByID[r.ID] + recencyWeight*decay}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// blendGraphPPR re-sorts candidates by (1−w)·cosine + w·g, where g is a candidate's
// normalized mass from a query-seeded Personalized PageRank over the author-declared
// edges among the candidate set: the restart distribution sits on the top-graphSeedK
// cosine hits (∝ their cosine) and a few degree-normalized walk steps spread it along
// edges — so a candidate on a MULTI-HOP path from a strong hit is lifted (unlike a 1-hop
// boost), while a hub linked only to weak/unseeded candidates gets ~nothing (degree
// normalization + seed anchoring keep it query-relevant, not centrality-biased). When
// synThreshold > 0 the walk graph is densified with ephemeral, NON-persisted "synonym"
// links between candidate pairs whose vectors have cosine ≥ synThreshold (a no-LLM
// encoder signal; the persisted edge store stays author-declared only). It only REORDERS
// the candidate set (no recall change); the displayed Hit.Score stays cosine (callers
// read cosineByID, not these scores). A no-op when w<=0, fewer than 2 candidates, or no
// edges/synonym links connect the set.
func (e *Engine) blendGraphPPR(ordered []search.Result, cosineByID map[string]float64, vecByID map[string][]float32, w, synThreshold float64) ([]search.Result, error) {
	if w <= 0 || len(ordered) < 2 {
		return ordered, nil
	}
	if w > 1 {
		w = 1
	}
	// Undirected adjacency among candidates: author-declared edges (both endpoints in
	// the set) + optional ephemeral synonym links.
	adj := make(map[string][]string, len(ordered))
	addEdge := func(a, b string) {
		adj[a] = append(adj[a], b)
		adj[b] = append(adj[b], a)
	}
	edges, err := e.meta.AllEdges()
	if err != nil {
		return nil, err
	}
	for _, ed := range edges {
		from, to := ed.From.String(), ed.To.String()
		if _, ok := cosineByID[from]; !ok {
			continue
		}
		if _, ok := cosineByID[to]; !ok {
			continue
		}
		addEdge(from, to)
	}
	if synThreshold > 0 {
		// O(N²) pairwise over candidates with a stored vector — opt-in only.
		ids := make([]string, 0, len(vecByID))
		for id := range vecByID {
			ids = append(ids, id)
		}
		sort.Strings(ids) // deterministic
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				if search.Cosine(vecByID[ids[i]], vecByID[ids[j]]) >= synThreshold {
					addEdge(ids[i], ids[j])
				}
			}
		}
	}
	if len(adj) == 0 {
		return ordered, nil // nothing connects the candidate set → unchanged order
	}

	// Restart distribution: mass on the top-graphSeedK candidates by cosine, ∝ cosine.
	ids := make([]string, 0, len(cosineByID))
	for id := range cosineByID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if cosineByID[ids[i]] != cosineByID[ids[j]] {
			return cosineByID[ids[i]] > cosineByID[ids[j]]
		}
		return ids[i] < ids[j]
	})
	k := graphSeedK
	if k > len(ids) {
		k = len(ids)
	}
	restart := make(map[string]float64, k)
	var seedSum float64
	for _, id := range ids[:k] {
		if c := cosineByID[id]; c > 0 {
			restart[id] = c
			seedSum += c
		}
	}
	if seedSum <= 0 {
		return ordered, nil // no positive-cosine seed → nothing to propagate
	}
	for id := range restart {
		restart[id] /= seedSum
	}

	// Personalized PageRank: r⁰ = restart; r^{t+1} = (1−α)·restart + α·P·r^t, where
	// P[i][j] = 1/deg(j) along undirected edges (degree-normalized random walk). Teleport
	// (1−α) returns only to the seeds, so mass stays anchored to what the query matched.
	deg := make(map[string]int, len(adj))
	for id, nbrs := range adj {
		deg[id] = len(nbrs)
	}
	r := make(map[string]float64, len(restart))
	for id, m := range restart {
		r[id] = m
	}
	alpha := graphPPRDamping
	for step := 0; step < graphPPRSteps; step++ {
		next := make(map[string]float64, len(r))
		for id, m := range restart {
			next[id] = (1 - alpha) * m
		}
		for j, mj := range r {
			d := deg[j]
			if mj == 0 || d == 0 {
				continue
			}
			share := alpha * mj / float64(d)
			for _, i := range adj[j] {
				next[i] += share
			}
		}
		r = next
	}

	var gmax float64
	for _, m := range r {
		if m > gmax {
			gmax = m
		}
	}
	if gmax <= 0 {
		return ordered, nil
	}
	blended := make([]search.Result, len(ordered))
	for i, res := range ordered {
		g := r[res.ID] / gmax
		blended[i] = search.Result{ID: res.ID, Score: (1-w)*cosineByID[res.ID] + w*g}
	}
	sort.SliceStable(blended, func(i, j int) bool {
		if blended[i].Score != blended[j].Score {
			return blended[i].Score > blended[j].Score
		}
		return blended[i].ID < blended[j].ID
	})
	return blended, nil
}

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
	switch {
	case q.Hierarchical:
		arts, err = e.coarseToFineCandidates(q.Scope, qvec, q.CoarseK, q.Tier, q.IncludeSuperseded)
	case q.Collapsed:
		arts, err = e.collapsedCandidates(q.Scope, q.Tier, q.IncludeSuperseded)
	default:
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
	vecByID := make(map[string][]float32, len(arts)) // candidate vectors, for synonym links
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
		vecByID[a.ID.String()] = vec
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

	// Optional recency tie-breaker (opt-in, OFF by default): blend a small age-decay
	// term into the cosine order among current atoms. Only on the pure-cosine path
	// and only when NOT reranking (the cross-encoder already orders); see the caveat
	// on Query.RecencyHalfLifeDays. Hit.Score stays cosine — this only reorders.
	willRerank := q.Rerank && e.reranker != nil
	if q.RecencyHalfLifeDays > 0 && q.Mode == core.ModeVector && !willRerank {
		ordered = blendRecency(ordered, cosineByID, byID, q.RecencyHalfLifeDays)
	}

	// Optional graph-aware boost (opt-in, OFF by default): a query-seeded Personalized
	// PageRank over author-declared edges lifts candidates on paths FROM the strong hits
	// (the structural axis blended into the semantic order). Reorder-only — Hit.Score
	// stays cosine; a no-op when no edges connect the set; skipped while reranking (the
	// cross-encoder already orders). See Query.GraphBoost / GraphSynonym.
	if q.GraphBoost > 0 && !willRerank {
		ordered, err = e.blendGraphPPR(ordered, cosineByID, vecByID, q.GraphBoost, q.GraphSynonym)
		if err != nil {
			return core.NilID, nil, err
		}
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

// Neighbors returns the top-k most similar CURRENT artifacts visible from scope —
// the "what existing memory might this replace?" lookup an agent runs BEFORE
// pushing a new conclusion, so it can declare PushRequest.Supersedes (or pass the
// ids to Consolidate). It is a plain overview Query restricted to the current view
// (superseded/archived excluded); the returned Hit.Artifact ids feed straight into
// Supersedes. See docs/SUPERSESSION.md §4/§5.
func (e *Engine) Neighbors(ctx context.Context, scope core.ID, text string, k int) ([]core.Hit, error) {
	if k <= 0 {
		k = defaultTopK
	}
	_, hits, err := e.Query(ctx, core.Query{Scope: scope, Text: text, Detail: core.DetailOverview, TopK: k})
	return hits, err
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
