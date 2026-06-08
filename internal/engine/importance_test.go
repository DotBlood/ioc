package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
)

// TestImportance_LiftsCanonicalOverWorkspace verifies that a TierWorktree artifact
// outranks a TierWorkspace artifact of EQUAL cosine when ImportanceWeight > 0, and
// that Hit.Score stays cosine (blendImportance only reorders).
func TestImportance_LiftsCanonicalOverWorkspace(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	s, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)

	// Two artifacts with the exact same summary text → identical mock vector → identical
	// cosine. The only distinguishing signal is the Tier.
	const twin = "embedder choice bge small decision"
	ws, err := e.Push(ctx, core.PushRequest{
		Scope:   s.ID,
		Kind:    core.KindInsight,
		Summary: twin,
		Tier:    core.TierWorkspace,
	})
	require.NoError(t, err)
	wt, err := e.Push(ctx, core.PushRequest{
		Scope:   s.ID,
		Kind:    core.KindInsight,
		Summary: twin,
		Tier:    core.TierWorktree,
	})
	require.NoError(t, err)

	// A couple of distinct distractors to give the search something to rank.
	pushInsight(t, e, s.ID, "runtime daemon framed protocol loopback token")
	pushInsight(t, e, s.ID, "ingestion chunk language semantic boundaries")

	baseQ := core.Query{Scope: s.ID, Text: twin, TopK: 5}

	// Baseline (ImportanceWeight=0): capture cosine scores by artifact ID.
	_, base, err := e.Query(ctx, baseQ)
	require.NoError(t, err)
	baseScores := scoresByID(base)

	// Importance-on query.
	importanceQ := core.Query{Scope: s.ID, Text: twin, TopK: 5, ImportanceWeight: 0.3}
	_, boosted, err := e.Query(ctx, importanceQ)
	require.NoError(t, err)

	// The canonical (worktree) artifact must now rank above the workspace one.
	require.Less(t, rankOf(boosted, wt.ID), rankOf(boosted, ws.ID),
		"TierWorktree artifact must outrank TierWorkspace artifact of equal cosine when ImportanceWeight > 0")

	// Hit.Score must stay cosine — blendImportance is reorder-only.
	for _, h := range boosted {
		if baseline, ok := baseScores[h.Artifact]; ok {
			require.InDelta(t, baseline, h.Score, 1e-12,
				"Hit.Score must remain the cosine value under importance boost (artifact %s)", h.Artifact)
		}
	}
}

// TestImportance_OffIsNoop verifies that ImportanceWeight=0 produces the exact same
// artifact order as the default query (no ImportanceWeight set), i.e. pure cosine.
func TestImportance_OffIsNoop(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	s, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)

	const twin = "embedder choice bge small decision"
	_, err = e.Push(ctx, core.PushRequest{Scope: s.ID, Kind: core.KindInsight, Summary: twin, Tier: core.TierWorkspace})
	require.NoError(t, err)
	_, err = e.Push(ctx, core.PushRequest{Scope: s.ID, Kind: core.KindInsight, Summary: twin, Tier: core.TierWorktree})
	require.NoError(t, err)
	pushInsight(t, e, s.ID, "runtime daemon framed protocol loopback token")
	pushInsight(t, e, s.ID, "ingestion chunk language semantic boundaries")

	_, base, err := e.Query(ctx, core.Query{Scope: s.ID, Text: twin, TopK: 5})
	require.NoError(t, err)

	_, off, err := e.Query(ctx, core.Query{Scope: s.ID, Text: twin, TopK: 5, ImportanceWeight: 0})
	require.NoError(t, err)

	require.Len(t, off, len(base))
	for i := range base {
		require.Equal(t, base[i].Artifact, off[i].Artifact,
			"ImportanceWeight=0 must produce the same order as the default (pure cosine) at position %d", i)
	}
}

// TestImportance_UniformTierNoop verifies that when all candidates share the same Tier,
// importance is constant for every candidate — a uniform additive shift that preserves
// cosine order — so the result must be identical to ImportanceWeight=0.
func TestImportance_UniformTierNoop(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	s, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)

	// Three distinct workspace artifacts (uniform tier). pushInsight omits Tier, which
	// causes engine.Push to default it to TierWorkspace — so all three land as workspace.

	q := core.Query{Scope: s.ID, Text: "retrieval ranking storage runtime", TopK: 3}

	_, base, err := e.Query(ctx, q)
	require.NoError(t, err)

	q.ImportanceWeight = 0.5
	_, weighted, err := e.Query(ctx, q)
	require.NoError(t, err)

	require.Len(t, weighted, len(base))
	for i := range base {
		require.Equal(t, base[i].Artifact, weighted[i].Artifact,
			"uniform Tier → importance constant → order must be identical to ImportanceWeight=0 at position %d", i)
	}
}
