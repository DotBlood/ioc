package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/iocfmt"
)

// fakeReranker records the passages it was asked to score and scores each by a
// caller-supplied function (raw cross-encoder logits). It lets a test prove (a)
// rerank sees document CONTENT, not the label, and (b) the rerank score drives
// ordering + confidence.
type fakeReranker struct {
	got   []string
	score func(passage string) float64
}

func (f *fakeReranker) Rerank(_ context.Context, _ string, passages []string) ([]float64, error) {
	f.got = append(f.got[:0], passages...)
	out := make([]float64, len(passages))
	for i, p := range passages {
		out[i] = f.score(p)
	}
	return out, nil
}

func (f *fakeReranker) Model() string { return "fake-reranker" }

// H3: the reranker (and BM25) must score a document's CONTENT, not its
// "path:lines — first line" label — otherwise it reorders on the wrong text.
func TestRerank_ScoresContentNotLabel(t *testing.T) {
	ctx := context.Background()
	fr := &fakeReranker{score: func(string) float64 { return 1 }}
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(16), WithReranker(fr))
	require.NoError(t, err)
	defer e.Close()

	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)

	_, err = e.Push(ctx, core.PushRequest{
		Scope:   root.ID,
		Kind:    core.KindDocument,
		Summary: "pkg/foo.go:1-20 — package foo", // label only
		Content: []byte("the quick brown fox jumps over the lazy dog"),
	})
	require.NoError(t, err)

	_, _, err = e.Query(ctx, core.Query{Scope: root.ID, Text: "fox", TopK: 5, Rerank: true})
	require.NoError(t, err)

	require.Len(t, fr.got, 1)
	require.Equal(t, "the quick brown fox jumps over the lazy dog", fr.got[0],
		"reranker must score document CONTENT, not the path:lines label")
	require.NotContains(t, fr.got[0], "pkg/foo.go")
}

// H3: when reranked, hits are ordered by the cross-encoder, each Hit carries the
// sigmoid-normalized rerank score, and confidence (weak_match/margin/ranked_by)
// reads THAT signal — not cosine (which would give a negative margin).
func TestRerank_ConfidenceUsesRerankSignal(t *testing.T) {
	ctx := context.Background()
	// Score by a keyword in the summary so the test controls the rerank order
	// independently of cosine. "win" → strong relevance, "lose" → weak.
	fr := &fakeReranker{score: func(p string) float64 {
		if strings.Contains(p, "win") {
			return 5 // sigmoid ≈ 0.993
		}
		return -5 // sigmoid ≈ 0.007
	}}
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(16), WithReranker(fr))
	require.NoError(t, err)
	defer e.Close()

	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)

	// "lose" shares more query terms (would rank first by cosine); "win" is the
	// one the reranker prefers — so rerank must flip the order.
	_, err = e.Push(ctx, core.PushRequest{Scope: root.ID, Kind: core.KindInsight, Summary: "lose alpha beta gamma reranker"})
	require.NoError(t, err)
	_, err = e.Push(ctx, core.PushRequest{Scope: root.ID, Kind: core.KindInsight, Summary: "win delta"})
	require.NoError(t, err)

	qid, hits, err := e.Query(ctx, core.Query{Scope: root.ID, Text: "alpha beta gamma reranker", TopK: 5, Rerank: true})
	require.NoError(t, err)
	require.Len(t, hits, 2)

	// Ordered by rerank: "win" first despite weaker cosine.
	require.Contains(t, hits[0].Summary, "win")
	require.Contains(t, hits[1].Summary, "lose")

	// Each hit carries the sigmoid-normalized rerank score; cosine Score untouched.
	require.NotNil(t, hits[0].RerankScore)
	require.NotNil(t, hits[1].RerankScore)
	require.InDelta(t, 0.9933, *hits[0].RerankScore, 0.01)
	require.InDelta(t, 0.0067, *hits[1].RerankScore, 0.01)

	out := iocfmt.QueryOut(qid, hits, e.EmbModel())
	require.Equal(t, "rerank", out["ranked_by"])
	// Margin uses the rerank signal → strictly positive (cosine would be negative).
	require.Greater(t, out["margin"].(float64), 0.0)
	require.InDelta(t, *hits[0].RerankScore, out["top_score"].(float64), 1e-9)
	require.False(t, out["weak_match"].(bool), "top rerank score 0.993 is above the 0.5 floor")
}

// H3: a confident cosine top hit that the reranker judges irrelevant (logit < 0)
// must be flagged weak_match — the rerank floor governs once reranked.
func TestRerank_WeakWhenRerankerRejects(t *testing.T) {
	ctx := context.Background()
	fr := &fakeReranker{score: func(string) float64 { return -2 }} // sigmoid ≈ 0.12 < 0.5
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(16), WithReranker(fr))
	require.NoError(t, err)
	defer e.Close()

	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	_, err = e.Push(ctx, core.PushRequest{Scope: root.ID, Kind: core.KindInsight, Summary: "alpha beta gamma"})
	require.NoError(t, err)

	qid, hits, err := e.Query(ctx, core.Query{Scope: root.ID, Text: "alpha beta gamma", TopK: 5, Rerank: true})
	require.NoError(t, err)
	require.Len(t, hits, 1)

	out := iocfmt.QueryOut(qid, hits, e.EmbModel())
	require.Equal(t, "rerank", out["ranked_by"])
	require.True(t, out["weak_match"].(bool), "reranker rejected the only hit → weak_match")
}

// H3: MinScore is a cosine PRE-gate — a candidate below it is excluded before
// rerank, so the cross-encoder cannot resurrect it (and there is no incoherent
// post-rerank cosine drop). A high-cosine candidate the reranker dislikes still
// survives the gate (the gate is cosine, not rerank).
func TestRerank_MinScoreIsCosinePreGate(t *testing.T) {
	ctx := context.Background()
	// Reranker loves "weak" (would pull it to the top) and hates "strong".
	fr := &fakeReranker{score: func(p string) float64 {
		if strings.Contains(p, "weak") {
			return 9
		}
		return -9
	}}
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(16), WithReranker(fr))
	require.NoError(t, err)
	defer e.Close()

	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	// Two artifacts: one shares all query terms (high cosine), one shares none.
	_, err = e.Push(ctx, core.PushRequest{Scope: root.ID, Kind: core.KindInsight, Summary: "strong alpha beta gamma delta"})
	require.NoError(t, err)
	_, err = e.Push(ctx, core.PushRequest{Scope: root.ID, Kind: core.KindInsight, Summary: "weak unrelated terms"})
	require.NoError(t, err)

	// Establish the cosine of each so we can set a gate strictly between them.
	_, base, err := e.Query(ctx, core.Query{Scope: root.ID, Text: "alpha beta gamma delta", TopK: 5})
	require.NoError(t, err)
	require.Len(t, base, 2)
	byKey := map[string]float64{}
	for _, h := range base {
		if strings.Contains(h.Summary, "strong") {
			byKey["strong"] = h.Score
		} else {
			byKey["weak"] = h.Score
		}
	}
	require.Greater(t, byKey["strong"], byKey["weak"], "the term-sharing artifact must have higher cosine")
	gate := (byKey["strong"] + byKey["weak"]) / 2

	// With the gate, "weak" is excluded as a candidate; even though the reranker
	// scores it +9 it can never be promoted — only "strong" survives and is shown.
	_, hits, err := e.Query(ctx, core.Query{Scope: root.ID, Text: "alpha beta gamma delta", TopK: 5, MinScore: gate, Rerank: true})
	require.NoError(t, err)
	require.Len(t, hits, 1, "low-cosine candidate must be gated out before rerank")
	require.Contains(t, hits[0].Summary, "strong")
}

// Without rerank, confidence stays on cosine and ranked_by reflects that.
func TestQueryOut_CosinePathUnchanged(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(16))
	require.NoError(t, err)
	defer e.Close()

	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	_, err = e.Push(ctx, core.PushRequest{Scope: root.ID, Kind: core.KindInsight, Summary: "alpha beta gamma"})
	require.NoError(t, err)

	qid, hits, err := e.Query(ctx, core.Query{Scope: root.ID, Text: "alpha beta gamma", TopK: 5})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Nil(t, hits[0].RerankScore)

	out := iocfmt.QueryOut(qid, hits, e.EmbModel())
	require.Equal(t, "cosine", out["ranked_by"])
}
