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

// QueryOut builds a query response: weak_match uses a per-embedder confidence
// floor; top_score and margin (top1-top2) are a relative signal.
func QueryOut(queryID core.ID, hits []core.Hit, model string) map[string]any {
	var top, margin float64
	if len(hits) > 0 {
		top = hits[0].Score
	}
	if len(hits) > 1 {
		margin = hits[0].Score - hits[1].Score
	}
	return map[string]any{
		"query_id":   queryID.String(),
		"weak_match": len(hits) == 0 || top < core.ConfidenceFloor(model),
		"top_score":  top,
		"margin":     margin,
		"hits":       HitsOut(hits),
	}
}
