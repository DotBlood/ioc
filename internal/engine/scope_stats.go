package engine

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/DotBlood/ioc/internal/cluster"
	"github.com/DotBlood/ioc/internal/core"
)

// Config keys for the (opt-in) scope-split advisory thresholds. All have built-in
// defaults; setting them tunes the advice without any code change.
const (
	cfgSplitTau        = "scope.split.tau"              // cosine edge threshold for clustering
	cfgSplitMinArts    = "scope.split.min_artifacts"    // gate: too few to advise below this
	cfgSplitMinCluster = "scope.split.min_cluster_size" // a component smaller than this is an outlier, not a topic

	defaultSplitMinArts    = 8
	defaultSplitMinCluster = 2
)

// ScopeStats computes the NO-LLM, advisory split signal for a scope (CLI
// `ioc scope-advise`). It reports how many topic clusters the scope's artifacts
// form (deterministic connected components at cosine ≥ tau over their summary
// embeddings), the dispersion, and a recommendation. It is READ-ONLY and changes
// no behavior — the agent decides whether to fork/consolidate. See docs/SCOPE_POLICY.md.
//
// tau <= 0 resolves to scope.split.tau, else the embedder's calibrated cosine
// ConfidenceFloor (the "same match" threshold). minArtifacts <= 0 resolves to
// scope.split.min_artifacts, else the built-in default. NOTE: ConfidenceFloor is
// 0 for the mock embedder, so a meaningful clustering on the mock requires an
// explicit tau; the default is a starting point pending per-embedder calibration
// (docs/SCOPE_POLICY.md §10).
func (e *Engine) ScopeStats(_ context.Context, scope core.ID, tau float64, minArtifacts int) (core.ScopeStats, error) {
	if _, err := e.meta.GetScope(scope); err != nil {
		return core.ScopeStats{}, err // core.ErrNotFound for an unknown scope
	}
	arts, err := e.meta.ArtifactsInScope(scope)
	if err != nil {
		return core.ScopeStats{}, err
	}

	stats := core.ScopeStats{Scope: scope, ArtifactCount: len(arts)}
	stats.Tau = e.resolveSplitTau(tau)
	minArts := e.resolveSplitInt(cfgSplitMinArts, minArtifacts, defaultSplitMinArts)
	minCluster := e.resolveSplitInt(cfgSplitMinCluster, 0, defaultSplitMinCluster)

	// Deterministic order (by ID) so cluster membership/output is stable.
	sort.Slice(arts, func(i, j int) bool { return arts[i].ID.String() < arts[j].ID.String() })

	// Gather embeddings for artifacts that have one.
	vecs := make([][]float32, 0, len(arts))
	ids := make([]core.ID, 0, len(arts))
	for _, a := range arts {
		if a.EmbRef == 0 || e.emb == nil {
			continue
		}
		v, gerr := e.emb.Get(a.EmbRef)
		if gerr != nil {
			return core.ScopeStats{}, fmt.Errorf("engine: scope stats: embedding for %s: %w", a.ID, gerr)
		}
		vecs = append(vecs, v)
		ids = append(ids, a.ID)
	}
	stats.EmbeddedCount = len(vecs)
	// Dispersion only means something with at least one pair; for 0–1 embedded
	// artifacts leave it 0 (rather than 1−0=1.0, which would read as "maximally
	// diverse" for a degenerate set).
	if len(vecs) >= 2 {
		stats.Dispersion = 1 - cluster.MeanPairwiseCosine(vecs)
	}

	comps := cluster.ThresholdComponents(vecs, stats.Tau)
	stats.ClusterCount = len(comps)
	bigClusters := 0
	for _, comp := range comps {
		grp := make([]core.ID, 0, len(comp))
		for _, idx := range comp {
			grp = append(grp, ids[idx])
		}
		stats.Clusters = append(stats.Clusters, grp)
		if len(comp) >= minCluster {
			bigClusters++
		}
	}

	switch {
	case stats.EmbeddedCount < minArts:
		stats.Recommendation = "too_small"
		stats.Reason = fmt.Sprintf("only %d embedded artifacts (< %d) — too few to advise on splitting", stats.EmbeddedCount, minArts)
	case bigClusters >= 2:
		stats.Recommendation = "split"
		stats.Reason = fmt.Sprintf("%d artifacts form %d topic clusters (%d of size ≥ %d) at tau=%.2f — consider forking a sub-scope per cluster so descendant-aware retrieval can engage", stats.EmbeddedCount, stats.ClusterCount, bigClusters, minCluster, stats.Tau)
	default:
		stats.Recommendation = "ok"
		stats.Reason = fmt.Sprintf("%d artifacts form %d cluster(s) at tau=%.2f — the scope is topically cohesive", stats.EmbeddedCount, stats.ClusterCount, stats.Tau)
	}
	return stats, nil
}

// resolveSplitTau picks the clustering threshold: explicit override > config >
// the embedder's calibrated cosine ConfidenceFloor.
func (e *Engine) resolveSplitTau(override float64) float64 {
	if override > 0 {
		return override
	}
	if v, ok := e.meta.GetConfig(cfgSplitTau); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
		}
	}
	model := e.embedder.Model()
	if m, ok := e.meta.GetConfig("emb_model"); ok && m != "" {
		model = m // the persisted model is authoritative (HTTP embedders report "" until first call)
	}
	return core.ResolveConfidence(model, e.meta.GetConfig).Floor
}

// resolveSplitInt picks an int threshold: explicit override > config key > default.
func (e *Engine) resolveSplitInt(key string, override, def int) int {
	if override > 0 {
		return override
	}
	if v, ok := e.meta.GetConfig(key); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
