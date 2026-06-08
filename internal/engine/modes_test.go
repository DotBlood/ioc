package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
)

// openEng is a small helper: a fresh engine on a temp dir with the deterministic
// mock embedder (so retrieval order is reproducible without the real model).
func openEng(t *testing.T) *Engine {
	t.Helper()
	e, err := Open(context.Background(), t.TempDir(), embed.NewMockEmbedder(384))
	require.NoError(t, err)
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func hasArtifact(hits []core.Hit, id core.ID) bool {
	for _, h := range hits {
		if h.Artifact == id {
			return true
		}
	}
	return false
}

// ModeHybrid must keep Hit.Score as the COSINE similarity (not the RRF fused score)
// and leave RerankScore unset — the displayed/confidence score is always cosine,
// even though hybrid orders by RRF(cosine, BM25). Guards the H3 coherence invariant.
func TestQuery_ModeHybrid_ScoreStaysCosine(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	s, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)

	a, err := e.Push(ctx, core.PushRequest{Scope: s.ID, Kind: core.KindInsight, Summary: "reranker cross encoder precision fix"})
	require.NoError(t, err)
	_, err = e.Push(ctx, core.PushRequest{Scope: s.ID, Kind: core.KindInsight, Summary: "storage durability append only embeddings"})
	require.NoError(t, err)

	q := core.Query{Scope: s.ID, Text: "cross encoder reranker", Detail: core.DetailOverview, TopK: 5}

	_, vecHits, err := e.Query(ctx, q)
	require.NoError(t, err)
	qh := q
	qh.Mode = core.ModeHybrid
	_, hybHits, err := e.Query(ctx, qh)
	require.NoError(t, err)

	// The displayed score for the same artifact must be identical (cosine) in both
	// modes, and hybrid must not set a rerank score.
	cosVec := scoreOf(vecHits, a.ID)
	cosHyb := scoreOf(hybHits, a.ID)
	require.NotNil(t, cosVec)
	require.NotNil(t, cosHyb)
	require.InDelta(t, *cosVec, *cosHyb, 1e-12, "hybrid Hit.Score must equal the cosine score")
	for _, h := range hybHits {
		require.Nil(t, h.RerankScore, "hybrid mode must not set a rerank score")
	}
}

func scoreOf(hits []core.Hit, id core.ID) *float64 {
	for _, h := range hits {
		if h.Artifact == id {
			s := h.Score
			return &s
		}
	}
	return nil
}

// Hierarchical retrieval descends into child scopes via coarse routing: an
// UNPUBLISHED artifact in a child session is invisible to a flat query from the
// parent workspace (bottom-up visibility), but a hierarchical query reaches it
// because coarse→fine pulls artifacts directly from the selected descendant scope.
func TestQuery_Hierarchical_DescendsIntoChild(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	wt, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	ws, err := e.CreateScope(ctx, wt.ID, core.RoleWorkspace, "decisions")
	require.NoError(t, err)
	child, err := e.CreateScope(ctx, ws.ID, core.RoleSession, "retrieval")
	require.NoError(t, err)

	// Unpublished artifact lives only in the child session.
	const text = "alpha target decision about caching"
	a, err := e.Push(ctx, core.PushRequest{Scope: child.ID, Kind: core.KindInsight, Summary: text})
	require.NoError(t, err)
	// Rollup the child so it is a coarse candidate; identical text → identical mock
	// vector → it routes to the top of the coarse stage.
	require.NoError(t, e.RollupScope(ctx, child.ID, text))

	// Flat query from the workspace: child is a descendant, not a sibling/ancestor,
	// and the artifact is unpublished → NOT visible.
	_, flat, err := e.Query(ctx, core.Query{Scope: ws.ID, Text: text, TopK: 5})
	require.NoError(t, err)
	require.False(t, hasArtifact(flat, a.ID), "flat query must not see an unpublished child artifact")

	// Hierarchical query from the workspace: coarse→fine descends into the child.
	_, hier, err := e.Query(ctx, core.Query{Scope: ws.ID, Text: text, TopK: 5, Hierarchical: true})
	require.NoError(t, err)
	require.True(t, hasArtifact(hier, a.ID), "hierarchical query must reach the child artifact")
}

// With NO rollups anywhere, hierarchical falls back to the flat visible set: it
// behaves exactly like a non-hierarchical query (finds own/ancestor artifacts,
// still misses an unpublished child).
func TestQuery_Hierarchical_FallsBackWhenNoRollups(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	wt, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	ws, err := e.CreateScope(ctx, wt.ID, core.RoleWorkspace, "decisions")
	require.NoError(t, err)
	child, err := e.CreateScope(ctx, ws.ID, core.RoleSession, "retrieval")
	require.NoError(t, err)

	own, err := e.Push(ctx, core.PushRequest{Scope: ws.ID, Kind: core.KindInsight, Summary: "workspace level decision kept here"})
	require.NoError(t, err)
	childArt, err := e.Push(ctx, core.PushRequest{Scope: child.ID, Kind: core.KindInsight, Summary: "child session unpublished note"})
	require.NoError(t, err)

	// No RollupScope called → coarseToFineCandidates sees rset.Len()==0 → flat.
	_, hier, err := e.Query(ctx, core.Query{Scope: ws.ID, Text: "decision", TopK: 5, Hierarchical: true})
	require.NoError(t, err)
	require.True(t, hasArtifact(hier, own.ID), "fallback flat must still find the workspace's own artifact")
	require.False(t, hasArtifact(hier, childArt.ID), "fallback flat must not descend into the unpublished child")
}

// Ancestors returns the parent chain nearest-first.
func TestAncestors_NearestFirst(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	wt, _ := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "wt")
	ws, _ := e.CreateScope(ctx, wt.ID, core.RoleWorkspace, "ws")
	s, _ := e.CreateScope(ctx, ws.ID, core.RoleSession, "s")

	ancs, err := e.Ancestors(ctx, s.ID)
	require.NoError(t, err)
	require.Len(t, ancs, 2)
	require.Equal(t, ws.ID, ancs[0].ID, "nearest parent first")
	require.Equal(t, wt.ID, ancs[1].ID)

	root, err := e.Ancestors(ctx, wt.ID)
	require.NoError(t, err)
	require.Empty(t, root, "a root scope has no ancestors")
}

// Publish flips visibility: an unpublished artifact is invisible to siblings; after
// Publish it appears in a sibling's overview.
func TestPublish_TogglesSiblingVisibility(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	wt, _ := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "wt")
	s1, _ := e.CreateScope(ctx, wt.ID, core.RoleSession, "s1")
	s2, _ := e.CreateScope(ctx, wt.ID, core.RoleSession, "s2")

	a, err := e.Push(ctx, core.PushRequest{Scope: s1.ID, Kind: core.KindInsight, Summary: "private until published"})
	require.NoError(t, err)

	before, err := e.SiblingOverview(ctx, s2.ID)
	require.NoError(t, err)
	require.False(t, hasArtifact(before, a.ID), "unpublished artifact invisible to siblings")

	require.NoError(t, e.Publish(ctx, a.ID))
	after, err := e.SiblingOverview(ctx, s2.ID)
	require.NoError(t, err)
	require.True(t, hasArtifact(after, a.ID), "published artifact visible to siblings")
}

// DeleteArtifact removes the record from listings and the search view.
func TestDeleteArtifact(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	s, _ := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	a, err := e.Push(ctx, core.PushRequest{Scope: s.ID, Kind: core.KindInsight, Summary: "to be deleted soon"})
	require.NoError(t, err)

	require.NoError(t, e.DeleteArtifact(ctx, a.ID))
	all, err := e.ListArtifacts(ctx)
	require.NoError(t, err)
	require.False(t, hasArtifactList(all, a.ID), "deleted artifact gone from ListArtifacts")

	_, hits, err := e.Query(ctx, core.Query{Scope: s.ID, Text: "deleted", TopK: 5})
	require.NoError(t, err)
	require.False(t, hasArtifact(hits, a.ID), "deleted artifact gone from search view")
}

func hasArtifactList(arts []core.Artifact, id core.ID) bool {
	for _, a := range arts {
		if a.ID == id {
			return true
		}
	}
	return false
}

// DeleteScope refuses while the scope still holds artifacts or child scopes, and
// succeeds once emptied — a guard against accidental data loss.
func TestDeleteScope_GuardsThenSucceeds(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	wt, _ := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "wt")
	child, _ := e.CreateScope(ctx, wt.ID, core.RoleSession, "child")

	// Parent with a child → refused.
	require.ErrorIs(t, e.DeleteScope(ctx, wt.ID), core.ErrInvalidInput)

	a, err := e.Push(ctx, core.PushRequest{Scope: child.ID, Kind: core.KindInsight, Summary: "blocks deletion"})
	require.NoError(t, err)
	// Scope with an artifact → refused.
	require.ErrorIs(t, e.DeleteScope(ctx, child.ID), core.ErrInvalidInput)

	// Empty it, then both delete (child first, then parent).
	require.NoError(t, e.DeleteArtifact(ctx, a.ID))
	require.NoError(t, e.DeleteScope(ctx, child.ID))
	require.NoError(t, e.DeleteScope(ctx, wt.ID))

	_, err = e.GetScope(ctx, wt.ID)
	require.ErrorIs(t, err, core.ErrNotFound)
}

// RecentTraces returns query traces newest-first (ULID-ordered), honoring n.
func TestRecentTraces_NewestFirst(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	s, _ := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	_, err := e.Push(ctx, core.PushRequest{Scope: s.ID, Kind: core.KindInsight, Summary: "alpha"})
	require.NoError(t, err)

	var qids []core.ID
	for i := 0; i < 3; i++ {
		qid, _, err := e.Query(ctx, core.Query{Scope: s.ID, Text: "alpha", TopK: 3})
		require.NoError(t, err)
		qids = append(qids, qid)
	}

	recent, err := e.RecentTraces(2)
	require.NoError(t, err)
	require.Len(t, recent, 2, "n limits the result")
	require.Equal(t, qids[2], recent[0].QueryID, "most recent query first")
	require.Equal(t, qids[1], recent[1].QueryID)

	all, err := e.RecentTraces(0)
	require.NoError(t, err)
	require.Len(t, all, 3, "n<=0 returns all")
}

// ListScopes / GetScope round-trip the scope set.
func TestListAndGetScope(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	wt, _ := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "wt")
	ws, _ := e.CreateScope(ctx, wt.ID, core.RoleWorkspace, "ws")

	scopes, err := e.ListScopes(ctx)
	require.NoError(t, err)
	require.Len(t, scopes, 2)

	got, err := e.GetScope(ctx, ws.ID)
	require.NoError(t, err)
	require.Equal(t, "ws", got.Title)
	require.Equal(t, wt.ID, got.Parent)

	_, err = e.GetScope(ctx, core.NewID())
	require.ErrorIs(t, err, core.ErrNotFound)
}
