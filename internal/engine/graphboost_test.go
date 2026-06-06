package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
)

func rankOf(hits []core.Hit, id core.ID) int {
	for i, h := range hits {
		if h.Artifact == id {
			return i
		}
	}
	return -1
}

func scoresByID(hits []core.Hit) map[core.ID]float64 {
	m := make(map[core.ID]float64, len(hits))
	for _, h := range hits {
		m[h.Artifact] = h.Score
	}
	return m
}

// seedCorpus pushes n distinct insights into a fresh scope and returns the engine,
// the scope, and the baseline (GraphBoost=0) hits for a fixed query.
func seedCorpus(t *testing.T, n int) (*Engine, core.ID, []core.Hit) {
	t.Helper()
	ctx := context.Background()
	e := openEng(t)
	s, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	summaries := []string{
		"alpha retrieval cosine ranking decision",
		"beta storage bbolt metadata layout",
		"gamma runtime daemon framed protocol",
		"delta embedding bge small vectors",
		"epsilon ingestion chunk language boundaries",
	}
	for i := 0; i < n; i++ {
		pushInsight(t, e, s.ID, summaries[i])
	}
	_, base, err := e.Query(ctx, core.Query{Scope: s.ID, Text: "retrieval ranking", TopK: n})
	require.NoError(t, err)
	require.Len(t, base, n)
	return e, s.ID, base
}

// GraphBoost lifts a low-cosine candidate that is edge-connected to the top hit, and
// leaves Hit.Score as cosine (reorder-only).
func TestGraphBoost_LiftsConnectedCandidate(t *testing.T) {
	ctx := context.Background()
	e, scope, base := seedCorpus(t, 5)
	top := base[0].Artifact      // highest cosine
	low := base[len(base)-1].Artifact // lowest cosine
	baseScores := scoresByID(base)

	// low depends_on top → low is structurally tied to the strongest hit.
	require.NoError(t, e.Relate(ctx, low, top, core.RelDependsOn))

	_, boosted, err := e.Query(ctx, core.Query{Scope: scope, Text: "retrieval ranking", TopK: 5, GraphBoost: 0.5})
	require.NoError(t, err)

	require.Less(t, rankOf(boosted, low), len(base)-1, "the connected low-cosine artifact should rise")

	// Hit.Score stays cosine: every boosted hit's Score equals its baseline cosine.
	for _, h := range boosted {
		require.InDelta(t, baseScores[h.Artifact], h.Score, 1e-12, "Score must remain cosine under graph-boost")
	}
}

// GraphBoost is a no-op when no edges connect the candidate set (same order as off).
func TestGraphBoost_NoEdgesIsNoop(t *testing.T) {
	ctx := context.Background()
	e, scope, base := seedCorpus(t, 5)

	_, boosted, err := e.Query(ctx, core.Query{Scope: scope, Text: "retrieval ranking", TopK: 5, GraphBoost: 0.5})
	require.NoError(t, err)
	require.Len(t, boosted, len(base))
	for i := range base {
		require.Equal(t, base[i].Artifact, boosted[i].Artifact, "no edges → order identical to GraphBoost=0")
	}
}

// GraphBoost=0 is exactly the baseline (the proven default is untouched).
func TestGraphBoost_OffEqualsBaseline(t *testing.T) {
	ctx := context.Background()
	e, scope, base := seedCorpus(t, 5)
	// Add an edge that WOULD reorder under boost, but query with GraphBoost=0.
	require.NoError(t, e.Relate(ctx, base[4].Artifact, base[0].Artifact, core.RelDependsOn))

	_, off, err := e.Query(ctx, core.Query{Scope: scope, Text: "retrieval ranking", TopK: 5, GraphBoost: 0})
	require.NoError(t, err)
	for i := range base {
		require.Equal(t, base[i].Artifact, off[i].Artifact, "GraphBoost=0 must equal the baseline order")
	}
}

// An unconnected artifact is not lifted by the boost — only edge-linked ones move.
func TestGraphBoost_UnconnectedNotLifted(t *testing.T) {
	ctx := context.Background()
	e, scope, base := seedCorpus(t, 5)
	top, low := base[0].Artifact, base[4].Artifact
	mid := base[2].Artifact // unconnected, middle of the pack

	require.NoError(t, e.Relate(ctx, low, top, core.RelDependsOn))
	_, boosted, err := e.Query(ctx, core.Query{Scope: scope, Text: "retrieval ranking", TopK: 5, GraphBoost: 0.5})
	require.NoError(t, err)

	// The connected low artifact outranks the unconnected middle one after boosting.
	require.Less(t, rankOf(boosted, low), rankOf(boosted, mid),
		"the edge-connected artifact should outrank an unconnected one of higher cosine")
}
