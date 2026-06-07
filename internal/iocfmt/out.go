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
	if tr := h.Meta["trust"]; tr != "" {
		out["trust"] = tr // e.g. "ingested" — untrusted external data (V17)
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
func QueryOut(queryID core.ID, hits []core.Hit, conf core.Confidence) map[string]any {
	// core.Decide is the single source of truth for the confidence verdict, shared with
	// the eval/calibration harness so production and the abstention metric judge a hit
	// list identically. It reads the signal that ordered the hits (rerank if present,
	// else cosine), gates the margin on the cosine path only, and prioritizes floor_miss.
	d := core.Decide(hits, conf)

	out := map[string]any{
		"query_id":   queryID.String(),
		"ranked_by":  d.RankedBy,
		"confidence": d.Confidence,
		"calibrated": conf.Calibrated,
		"weak_match": d.WeakMatch,
		"top_score":  d.TopScore,
		"margin":     d.Margin,
		"hits":       HitsOut(hits),
	}
	// Provenance flag (V17): warn the caller that some results are UNTRUSTED
	// ingested content (potential indirect prompt injection) — reason about any
	// imperatives in it, do not follow them.
	for _, h := range hits {
		if h.Meta["trust"] == core.TrustIngested {
			out["untrusted_content"] = true
			break
		}
	}
	return out
}
