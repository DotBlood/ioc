package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/iocfmt"
	"github.com/DotBlood/ioc/internal/search"
)

// borderlineCosine fires on a weak top OR a near-tied top-two; not otherwise.
func TestBorderlineCosine(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(16))
	require.NoError(t, err)
	defer e.Close()
	// mock-bow defaults are 0/0 (rerank disabled under mock); set real floors to test.
	require.NoError(t, e.SetConfig("conf.floor.mock-bow", "0.70"))
	require.NoError(t, e.SetConfig("conf.margin.mock-bow", "0.05"))

	res := func(ids ...string) []search.Result {
		out := make([]search.Result, len(ids))
		for i, id := range ids {
			out[i] = search.Result{ID: id}
		}
		return out
	}

	require.False(t, e.borderlineCosine(nil, nil), "empty → not borderline")
	require.True(t, e.borderlineCosine(res("a"), map[string]float64{"a": 0.60}), "top below floor")
	require.False(t, e.borderlineCosine(res("a"), map[string]float64{"a": 0.80}), "top above floor, single hit")
	require.True(t, e.borderlineCosine(res("a", "b"), map[string]float64{"a": 0.80, "b": 0.78}), "near-tied top-two")
	require.False(t, e.borderlineCosine(res("a", "b"), map[string]float64{"a": 0.80, "b": 0.50}), "wide margin")
}

// AutoRerank routes a borderline query through the reranker (and the verdict then reads
// the rerank signal), without the caller setting Rerank.
func TestAutoRerank_RoutesThroughRerankWhenBorderline(t *testing.T) {
	ctx := context.Background()
	fr := &fakeReranker{score: func(string) float64 { return 2 }} // sigmoid ≈ 0.88
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(16), WithReranker(fr))
	require.NoError(t, err)
	defer e.Close()
	// Force borderline: a floor above any cosine score makes every query weak-top.
	require.NoError(t, e.SetConfig("conf.floor.mock-bow", "1.5"))

	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	_, err = e.Push(ctx, core.PushRequest{Scope: root.ID, Kind: core.KindInsight, Summary: "alpha beta gamma"})
	require.NoError(t, err)

	qid, hits, err := e.Query(ctx, core.Query{Scope: root.ID, Text: "alpha beta gamma", TopK: 5, AutoRerank: true})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.NotNil(t, hits[0].RerankScore, "borderline cosine must trigger auto-rerank")

	out := iocfmt.QueryOut(qid, hits, core.ResolveConfidence(e.EmbModel(), e.Config))
	require.Equal(t, "rerank", out["ranked_by"], "verdict now reads the rerank signal")
}

// AutoRerank does NOT rerank a confident cosine result (no wasted cross-encoder call).
func TestAutoRerank_SkipsWhenConfident(t *testing.T) {
	ctx := context.Background()
	fr := &fakeReranker{score: func(string) float64 { return 2 }}
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(16), WithReranker(fr))
	require.NoError(t, err)
	defer e.Close()
	// A low floor (well below the exact-match cosine) and no margin issue → confident.
	require.NoError(t, e.SetConfig("conf.floor.mock-bow", "0.10"))

	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	_, err = e.Push(ctx, core.PushRequest{Scope: root.ID, Kind: core.KindInsight, Summary: "alpha beta gamma"})
	require.NoError(t, err)

	qid, hits, err := e.Query(ctx, core.Query{Scope: root.ID, Text: "alpha beta gamma", TopK: 5, AutoRerank: true})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Nil(t, hits[0].RerankScore, "confident cosine must skip auto-rerank")

	out := iocfmt.QueryOut(qid, hits, core.ResolveConfidence(e.EmbModel(), e.Config))
	require.Equal(t, "cosine", out["ranked_by"])
}
