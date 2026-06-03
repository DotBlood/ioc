package engine

import (
	"context"
	"sort"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/search"
)

// rerankTop reorders the top-n candidates by a cross-encoder over their summaries.
// It only changes ORDER — callers keep cosine as the displayed Hit.Score, so the
// confidence floor (weak_match) and MinScore stay meaningful.
func (e *Engine) rerankTop(ctx context.Context, query string, ordered []search.Result, byID map[string]core.Artifact, n int) ([]search.Result, error) {
	if n <= 0 {
		n = 20
	}
	if n > len(ordered) {
		n = len(ordered)
	}
	cand := ordered[:n]
	docs := make([]string, len(cand))
	for i, r := range cand {
		docs[i] = byID[r.ID].Summary
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
