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
		"zeta security token frame cap auth",
		"eta visibility published siblings blackboard",
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

// v2: the boost is anchored to the query's STRONG hits (seed set), not to global
// degree. A hub with several edges to NON-seed candidates must NOT be lifted, while a
// dependent of the top hit IS — this is the fix for v1's centrality bias.
func TestGraphBoost_AnchoredNotCentrality(t *testing.T) {
	old := graphSeedK
	graphSeedK = 1 // only the single top-cosine hit counts as a seed
	defer func() { graphSeedK = old }()

	ctx := context.Background()
	e, scope, base := seedCorpus(t, 7)
	top := base[0].Artifact // the only seed
	d := base[6].Artifact   // lowest cosine; we connect it to the seed
	h := base[5].Artifact   // a hub; we connect it to three NON-seed mids

	require.NoError(t, e.Relate(ctx, d, top, core.RelDependsOn))
	require.NoError(t, e.Relate(ctx, h, base[2].Artifact, core.RelDependsOn))
	require.NoError(t, e.Relate(ctx, h, base[3].Artifact, core.RelDependsOn))
	require.NoError(t, e.Relate(ctx, h, base[4].Artifact, core.RelDependsOn))

	_, boosted, err := e.Query(ctx, core.Query{Scope: scope, Text: "retrieval ranking", TopK: 7, GraphBoost: 0.5})
	require.NoError(t, err)
	require.Less(t, rankOf(boosted, d), rankOf(boosted, h),
		"a dependent of the query's strong hit must outrank a high-degree hub connected only to non-seeds")
}

// TestGraphBoost_MultiHopPPR proves that PPR with 2 steps reaches a 2-hop dependent
// that a single-hop walk cannot — the essential multi-hop property of the PPR
// implementation.
func TestGraphBoost_MultiHopPPR(t *testing.T) {
	oldK := graphSeedK
	graphSeedK = 1 // only the single top-cosine hit (base[0]) seeds the restart
	defer func() { graphSeedK = oldK }()

	ctx := context.Background()
	e, scope, base := seedCorpus(t, 7)

	// Build a 2-hop chain anchored at the seed:
	//   far → mid → top(seed)
	// The seed is base[0] (highest cosine for "retrieval ranking").
	top := base[0].Artifact // seed
	mid := base[5].Artifact // 1 hop from seed
	far := base[6].Artifact // 2 hops from seed (lowest cosine, furthest from query)
	ctrl := base[4].Artifact // unconnected control node; no edges

	require.NoError(t, e.Relate(ctx, mid, top, core.RelDependsOn)) // mid is 1 hop from seed
	require.NoError(t, e.Relate(ctx, far, mid, core.RelDependsOn)) // far is 2 hops from seed

	// --- 2-step walk: PPR can reach far ---
	// graphPPRSteps is already 2 (default); no need to override.
	_, boosted, err := e.Query(ctx, core.Query{
		Scope:      scope,
		Text:       "retrieval ranking",
		TopK:       7,
		GraphBoost: 0.5,
	})
	require.NoError(t, err)
	require.Less(t, rankOf(boosted, far), rankOf(boosted, ctrl),
		"PPR (2 steps) must lift a 2-hop dependent above an unconnected node")

	// --- 1-step walk: PPR cannot reach far ---
	// With a single walk step, mass flows from the seed to mid (1 hop) but CANNOT
	// reach far (2 hops). Both far and ctrl get zero PPR mass and fall back to their
	// baseline cosine order, where ctrl (base[4]) outranks far (base[6]) because ctrl
	// has higher query cosine.
	oldSteps := graphPPRSteps
	graphPPRSteps = 1
	defer func() { graphPPRSteps = oldSteps }()

	_, boosted1, err := e.Query(ctx, core.Query{
		Scope:      scope,
		Text:       "retrieval ranking",
		TopK:       7,
		GraphBoost: 0.5,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, rankOf(boosted1, far), rankOf(boosted1, ctrl),
		"with a 1-step walk, the 2-hop far node must NOT be lifted above the unconnected ctrl")
}

// TestGraphBoost_SynonymDensifies proves that GraphSynonym > 0 adds ephemeral links
// between candidates whose stored vectors are highly similar (cosine ≥ threshold), and
// that this lifts a candidate whose query cosine is lower than a non-synonym peer.
//
// Corpus design (verified against the MockEmbedder with 384-dim FNV bag-of-words):
//
//	X = "retrieval ranking cosine bge small"        stored-vec ← query-cosine 0.6325 (seed)
//	B = "retrieval ranking token small extra"       stored-vec ← query-cosine 0.6325 (ties X)
//	Z = "retrieval ranking cosine bge small search" stored-vec ← query-cosine 0.5774
//
//	cosine(stored_X, stored_Z) ≈ 0.913 ≥ 0.9  → synonym link X-Z fires
//	cosine(stored_X, stored_B) ≈ 0.600 < 0.9  → no synonym link X-B
//	cosine(stored_Z, stored_B) ≈ 0.548 < 0.9  → no synonym link Z-B
//
// Baseline (synonym OFF, no declared edges): X(0), B(1), Z(2) — ordered by query cosine
// then ULID ascending (X pushed first, B second, Z third → X<B<Z by ULID).
//
// With synonym ON and graphSeedK=1 (X seeds): PPR spreads mass from X to Z via the
// synonym edge. Z's blended score (0.455) overtakes B's (0.316). Order: X(0), Z(1), B(2).
//
// Assertion: Z's rank improves from 2 → 1, overtaking B.
func TestGraphBoost_SynonymDensifies(t *testing.T) {
	oldK := graphSeedK
	graphSeedK = 1 // only the single top-cosine artifact seeds the PPR restart
	defer func() { graphSeedK = oldK }()

	ctx := context.Background()
	e := openEng(t)
	s, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)

	// Push X first (lowest ULID), B second, Z third (highest ULID).
	// X and B tie on query cosine; X wins the seed tie-break by ULID.
	artX := pushInsight(t, e, s.ID, "retrieval ranking cosine bge small")
	artB := pushInsight(t, e, s.ID, "retrieval ranking token small extra")
	artZ := pushInsight(t, e, s.ID, "retrieval ranking cosine bge small search")

	// Sanity: ULID order matches push order (so X seeds on cosine+ULID tie-break).
	require.Less(t, artX.ID.String(), artB.ID.String())
	require.Less(t, artB.ID.String(), artZ.ID.String())

	// --- synonym OFF: no edges → order unchanged from cosine (X, B, Z by cosine then ULID) ---
	_, withoutSyn, err := e.Query(ctx, core.Query{
		Scope:      s.ID,
		Text:       "retrieval ranking",
		TopK:       3,
		GraphBoost: 0.5,
		// GraphSynonym: 0 (default — synonym off)
	})
	require.NoError(t, err)
	require.Len(t, withoutSyn, 3)
	require.Equal(t, artZ.ID, withoutSyn[2].Artifact,
		"without synonym, Z (lowest query cosine) should be last")

	// --- synonym ON: cosine(stored_X, stored_Z) ≈ 0.913 ≥ 0.9 adds ephemeral X-Z edge ---
	// PPR spreads seed mass from X to Z; B has no synonym link to X and gets none.
	// Z's blended score overtakes B's → Z rises from rank 2 to rank 1.
	_, withSyn, err := e.Query(ctx, core.Query{
		Scope:        s.ID,
		Text:         "retrieval ranking",
		TopK:         3,
		GraphBoost:   0.5,
		GraphSynonym: 0.9,
	})
	require.NoError(t, err)
	require.Len(t, withSyn, 3)

	require.Less(t, rankOf(withSyn, artZ.ID), rankOf(withoutSyn, artZ.ID),
		"synonym ON must lift Z (synonym of seed X) above its no-synonym rank")
	require.Less(t, rankOf(withSyn, artZ.ID), rankOf(withSyn, artB.ID),
		"with synonym ON, Z must outrank B (which has no synonym link to the seed)")
}
