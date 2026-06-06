package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
)

// Collapsed descends into a child scope WITHOUT needing a rollup — its advantage over
// hierarchical (which routes via rollups and can drop the scope). Flat is blind here.
func TestQuery_Collapsed_DescendsIntoChild(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	wt, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	ws, err := e.CreateScope(ctx, wt.ID, core.RoleWorkspace, "decisions")
	require.NoError(t, err)
	child, err := e.CreateScope(ctx, ws.ID, core.RoleSession, "retrieval")
	require.NoError(t, err)

	const text = "alpha target decision about caching"
	a, err := e.Push(ctx, core.PushRequest{Scope: child.ID, Kind: core.KindInsight, Summary: text})
	require.NoError(t, err)

	// Flat: the child is a descendant (not sibling/ancestor) and the artifact is
	// unpublished → invisible.
	_, flat, err := e.Query(ctx, core.Query{Scope: ws.ID, Text: text, TopK: 5})
	require.NoError(t, err)
	require.False(t, hasArtifact(flat, a.ID), "flat must not see an unpublished child artifact")

	// Collapsed: descends into ALL descendants in one pass — and needs NO rollup.
	_, coll, err := e.Query(ctx, core.Query{Scope: ws.ID, Text: text, TopK: 5, Collapsed: true})
	require.NoError(t, err)
	require.True(t, hasArtifact(coll, a.ID), "collapsed must reach the child artifact without a rollup")
}

// With no descendant scopes, collapsed is identical to flat (no regression, no-op).
func TestCollapsed_EqualsFlatWhenNoDescendants(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	wt, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	ws, err := e.CreateScope(ctx, wt.ID, core.RoleWorkspace, "leaf")
	require.NoError(t, err)
	for _, s := range []string{"retrieval cosine ranking", "storage bbolt layout", "runtime daemon protocol"} {
		_, err := e.Push(ctx, core.PushRequest{Scope: ws.ID, Kind: core.KindInsight, Summary: s})
		require.NoError(t, err)
	}
	_, flat, err := e.Query(ctx, core.Query{Scope: ws.ID, Text: "ranking", TopK: 5})
	require.NoError(t, err)
	_, coll, err := e.Query(ctx, core.Query{Scope: ws.ID, Text: "ranking", TopK: 5, Collapsed: true})
	require.NoError(t, err)
	require.Equal(t, len(flat), len(coll))
	for i := range flat {
		require.Equal(t, flat[i].Artifact, coll[i].Artifact, "no descendants → collapsed == flat order")
	}
}

// Collapsed preserves the current view: superseded artifacts and artifacts in an
// archived (superseded-version) descendant scope are excluded by default, and
// IncludeSuperseded brings the archived ones back.
func TestCollapsed_ExcludesArchivedAndSuperseded(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	wt, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	ws, err := e.CreateScope(ctx, wt.ID, core.RoleWorkspace, "decisions")
	require.NoError(t, err)
	live, err := e.CreateScope(ctx, ws.ID, core.RoleSession, "live")
	require.NoError(t, err)
	archived, err := e.CreateScope(ctx, ws.ID, core.RoleSession, "old-version")
	require.NoError(t, err)

	// A superseded chain inside the live child.
	const text = "embedder choice bge small decision"
	old, err := e.Push(ctx, core.PushRequest{Scope: live.ID, Kind: core.KindInsight, Summary: text})
	require.NoError(t, err)
	cur, err := e.Push(ctx, core.PushRequest{Scope: live.ID, Kind: core.KindInsight, Summary: text + " confirmed", Supersedes: []core.ID{old.ID}})
	require.NoError(t, err)

	// An artifact in a scope we mark archived (as CrossVersion would).
	stale, err := e.Push(ctx, core.PushRequest{Scope: archived.ID, Kind: core.KindInsight, Summary: text + " v0"})
	require.NoError(t, err)
	sc, err := e.meta.GetScope(archived.ID)
	require.NoError(t, err)
	sc.Archived = true
	require.NoError(t, e.meta.PutScope(sc))

	_, coll, err := e.Query(ctx, core.Query{Scope: ws.ID, Text: text, TopK: 10, Collapsed: true})
	require.NoError(t, err)
	require.True(t, hasArtifact(coll, cur.ID), "current artifact present")
	require.False(t, hasArtifact(coll, old.ID), "superseded artifact excluded from the current view")
	require.False(t, hasArtifact(coll, stale.ID), "artifact in an archived descendant scope excluded")

	_, hist, err := e.Query(ctx, core.Query{Scope: ws.ID, Text: text, TopK: 10, Collapsed: true, IncludeSuperseded: true})
	require.NoError(t, err)
	require.True(t, hasArtifact(hist, stale.ID), "IncludeSuperseded surfaces the archived-scope artifact")
}
