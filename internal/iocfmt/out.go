package iocfmt

import "github.com/DotBlood/ioc/internal/core"

// ScopeOut shapes a Scope for JSON output.
func ScopeOut(s core.Scope) map[string]any {
	out := map[string]any{
		"id":       s.ID.String(),
		"role":     s.Role.String(),
		"title":    s.Title,
		"version":  s.Version,
		"archived": s.Archived,
	}
	if !s.Parent.IsZero() {
		out["parent"] = s.Parent.String()
	}
	if !s.ForkedFrom.IsZero() {
		out["forked_from"] = s.ForkedFrom.String()
	}
	return out
}

// ArtifactOut shapes an Artifact for JSON output.
func ArtifactOut(a core.Artifact) map[string]any {
	return map[string]any{
		"id":        a.ID.String(),
		"scope":     a.Scope.String(),
		"kind":      a.Kind.String(),
		"tier":      a.Tier.String(),
		"published": a.Published,
	}
}

// HitOut shapes a Hit; raw content (if present) is rendered as a string.
func HitOut(h core.Hit) map[string]any {
	out := map[string]any{
		"artifact":   h.Artifact.String(),
		"scope_path": h.ScopePath,
		"kind":       h.Kind.String(),
		"tier":       h.Tier.String(),
		"summary":    h.Summary,
		"score":      h.Score,
	}
	if h.RerankScore != nil {
		out["rerank_score"] = *h.RerankScore
	}
	if !h.SupersededBy.IsZero() {
		out["superseded_by"] = h.SupersededBy.String()
	}
	if p := h.Meta["path"]; p != "" {
		out["path"] = p
	}
	if l := h.Meta["lines"]; l != "" {
		out["lines"] = l
	}
	if len(h.Content) > 0 {
		out["content"] = string(h.Content)
	}
	return out
}

// HitsOut shapes a hit slice.
func HitsOut(hits []core.Hit) []map[string]any {
	out := make([]map[string]any, len(hits))
	for i, h := range hits {
		out[i] = HitOut(h)
	}
	return out
}

// QueryOut builds a query response. top_score and margin (top1-top2) are a
// relative signal, and weak_match flags "no specific match" — all read from the
// signal that ACTUALLY ordered the hits: the cross-encoder rerank score (vs
// core.RerankFloor) when the query was reranked, else cosine (vs the per-embedder
// core.ConfidenceFloor). Mixing them — e.g. cosine margin over rerank-ordered
// hits — produces negative margins and false confidence (the H3 bug), so the
// ranked_by field names which signal is in force.
func QueryOut(queryID core.ID, hits []core.Hit, model string) map[string]any {
	reranked := len(hits) > 0 && hits[0].RerankScore != nil

	var top, margin float64
	score := func(h core.Hit) float64 {
		if reranked && h.RerankScore != nil {
			return *h.RerankScore
		}
		return h.Score
	}
	if len(hits) > 0 {
		top = score(hits[0])
	}
	// Only a margin between two hits ranked by the SAME signal is meaningful.
	// Caveat: the rerank margin is on sigmoid-normalized scores, which saturate near
	// 1 — two very confident hits can show a tiny margin even when the cross-encoder
	// clearly prefers one. Treat rerank margin as a soft separation hint; weak_match
	// (an absolute floor) is the actionable signal.
	if len(hits) > 1 && (!reranked || hits[1].RerankScore != nil) {
		margin = score(hits[0]) - score(hits[1])
	}

	floor := core.ConfidenceFloor(model)
	rankedBy := "cosine"
	if reranked {
		floor = core.RerankFloor(model)
		rankedBy = "rerank"
	}

	return map[string]any{
		"query_id":   queryID.String(),
		"ranked_by":  rankedBy,
		"weak_match": len(hits) == 0 || top < floor,
		"top_score":  top,
		"margin":     margin,
		"hits":       HitsOut(hits),
	}
}
