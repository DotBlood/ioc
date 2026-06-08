package engine

import (
	"context"
	"math"
	"sort"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/search"
)

// maxRankTextChars bounds the text fed to BM25 / the cross-encoder for a single
// artifact. Document chunks are already ~1500 chars; this caps pathological
// content (and matches the cross-encoder's ~512-token input window in practice).
const maxRankTextChars = 2000

// rankText is the text an artifact should be ranked by lexically (BM25) and by
// the cross-encoder (rerank). For a document the embedded/searchable text is the
// chunk CONTENT in CAS — its Summary is only a "path:lines — first line" label —
// so ranking on the Summary scores the wrong text. For reasoning/insight the
// Summary IS the embedded text, so it is the right rank text. On any CAS error
// it falls back to the Summary (degraded, not broken).
func (e *Engine) rankText(ctx context.Context, a core.Artifact) string {
	if a.Kind == core.KindDocument && !a.Content.IsZero() {
		if data, err := e.cas.Load(ctx, a.Content); err == nil {
			return truncRunes(string(data), maxRankTextChars)
		}
	}
	return truncRunes(a.Summary, maxRankTextChars)
}

func truncRunes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// sigmoid maps a cross-encoder logit to (0,1) so rerank scores are comparable
// across queries and against a fixed relevance floor (see core.RerankFloor).
func sigmoid(x float64) float64 { return 1 / (1 + math.Exp(-x)) }

// borderlineCosine reports whether the cosine result is weak enough to warrant an
// AutoRerank: the top cosine is below the per-embedder confidence floor (no confident
// match), OR the top-two cosine margin is below the margin floor (near-duplicate cluster
// — cosine cannot tell which, if any, actually answers). These are exactly the regions
// where the bi-encoder fails to separate present from absent (R5); the cross-encoder
// does. Confident, well-separated cosine results return false and skip rerank. Floors are
// resolved per-embedder from config (calibration) or defaults via core.ResolveConfidence.
func (e *Engine) borderlineCosine(ordered []search.Result, cosineByID map[string]float64) bool {
	if len(ordered) == 0 {
		return false // nothing to rerank; the result is "empty" regardless
	}
	conf := core.ResolveConfidence(e.EmbModel(), e.Config)
	top := cosineByID[ordered[0].ID]
	if top < conf.Floor {
		return true
	}
	if len(ordered) > 1 {
		if top-cosineByID[ordered[1].ID] < conf.MarginFloor {
			return true
		}
	}
	return false
}

// rerankTop reorders the top-n candidates by a cross-encoder over their rank
// text (CONTENT for documents, Summary otherwise — see rankText). It returns the
// reordered candidates with each Result.Score set to the RAW logit; the caller
// sigmoid-normalizes for display/confidence. Order is unchanged by the monotone
// sigmoid. Only ORDER changes here — Hit.Score stays cosine.
func (e *Engine) rerankTop(ctx context.Context, query string, ordered []search.Result, byID map[string]core.Artifact, n int) ([]search.Result, error) {
	if n <= 0 {
		n = 50
	}
	if n > len(ordered) {
		n = len(ordered)
	}
	cand := ordered[:n]
	docs := make([]string, len(cand))
	for i, r := range cand {
		docs[i] = e.rankText(ctx, byID[r.ID])
	}
	scores, err := e.reranker.Rerank(ctx, query, docs)
	if err != nil {
		return nil, err
	}
	out := make([]search.Result, len(cand))
	for i, r := range cand {
		out[i] = search.Result{ID: r.ID, Score: scores[i]}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
