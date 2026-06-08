package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
)

// pushInsight is a tiny helper: push a summary-only insight into scope.
func pushInsight(t *testing.T, e *Engine, scope core.ID, summary string, rels ...core.EdgeSpec) core.Artifact {
	t.Helper()
	a, err := e.Push(context.Background(), core.PushRequest{Scope: scope, Kind: core.KindInsight, Summary: summary, Relations: rels})
	require.NoError(t, err)
	return a
}

// Push with Relations creates edges; Related walks them in both directions.
func TestPush_RelationsCreateEdges(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	s, _ := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")

	a := pushInsight(t, e, s.ID, "use bbolt for metadata storage")
	// b depends on a (edge From=b To=a, kind depends_on)
	b := pushInsight(t, e, s.ID, "runtime daemon owns the bbolt store",
		core.EdgeSpec{Kind: core.RelDependsOn, Target: a.ID})

	// "what depends on a?" → edges INTO a → b.
	dependents, err := e.Related(ctx, a.ID, []core.RelationKind{core.RelDependsOn}, core.DirIn, 1)
	require.NoError(t, err)
	require.True(t, hasArtifact(dependents, b.ID), "b should be found as a dependent of a")
	require.False(t, hasArtifact(dependents, a.ID), "the start artifact is never included")

	// "what does b depend on?" → edges OUT of b → a.
	deps, err := e.Related(ctx, b.ID, []core.RelationKind{core.RelDependsOn}, core.DirOut, 1)
	require.NoError(t, err)
	require.True(t, hasArtifact(deps, a.ID))
}

func TestPush_RejectsBadRelations(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	s, _ := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")

	// Unknown target → push fails (fail-fast, like Supersedes).
	_, err := e.Push(ctx, core.PushRequest{Scope: s.ID, Kind: core.KindInsight, Summary: "x",
		Relations: []core.EdgeSpec{{Kind: core.RelDependsOn, Target: core.NewID()}}})
	require.ErrorIs(t, err, core.ErrNotFound)

	// Empty kind → invalid.
	target := pushInsight(t, e, s.ID, "target")
	_, err = e.Push(ctx, core.PushRequest{Scope: s.ID, Kind: core.KindInsight, Summary: "y",
		Relations: []core.EdgeSpec{{Kind: "", Target: target.ID}}})
	require.ErrorIs(t, err, core.ErrInvalidInput)
}

func TestRelate_PostHocAndValidation(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	s, _ := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	a := pushInsight(t, e, s.ID, "alpha")
	b := pushInsight(t, e, s.ID, "beta")

	require.NoError(t, e.Relate(ctx, b.ID, a.ID, core.RelDependsOn))
	// Idempotent: re-relating the same triple does not duplicate.
	require.NoError(t, e.Relate(ctx, b.ID, a.ID, core.RelDependsOn))
	dependents, err := e.Related(ctx, a.ID, nil, core.DirIn, 1)
	require.NoError(t, err)
	require.Len(t, dependents, 1)
	require.Equal(t, b.ID, dependents[0].Artifact)

	require.ErrorIs(t, e.Relate(ctx, a.ID, a.ID, core.RelRelatesTo), core.ErrInvalidInput, "self-edge")
	require.ErrorIs(t, e.Relate(ctx, a.ID, b.ID, ""), core.ErrInvalidInput, "empty kind")
	require.ErrorIs(t, e.Relate(ctx, a.ID, core.NewID(), core.RelRelatesTo), core.ErrNotFound, "unknown target")
	require.ErrorIs(t, e.Relate(ctx, core.NewID(), a.ID, core.RelRelatesTo), core.ErrNotFound, "unknown source")
}

func TestRelated_KindFilterAndDirection(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	s, _ := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	a := pushInsight(t, e, s.ID, "anchor")
	b := pushInsight(t, e, s.ID, "dep")     // a depends_on b
	c := pushInsight(t, e, s.ID, "related") // a relates_to c
	require.NoError(t, e.Relate(ctx, a.ID, b.ID, core.RelDependsOn))
	require.NoError(t, e.Relate(ctx, a.ID, c.ID, core.RelRelatesTo))

	// Filter by kind: only the depends_on neighbour.
	deps, err := e.Related(ctx, a.ID, []core.RelationKind{core.RelDependsOn}, core.DirOut, 1)
	require.NoError(t, err)
	require.True(t, hasArtifact(deps, b.ID))
	require.False(t, hasArtifact(deps, c.ID))

	// No kind filter: both outgoing neighbours.
	all, err := e.Related(ctx, a.ID, nil, core.DirOut, 1)
	require.NoError(t, err)
	require.Len(t, all, 2)
}

func TestRelated_DepthChain(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	s, _ := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	a := pushInsight(t, e, s.ID, "leaf")
	b := pushInsight(t, e, s.ID, "mid")
	c := pushInsight(t, e, s.ID, "top")
	// c depends_on b depends_on a (a dependency chain).
	require.NoError(t, e.Relate(ctx, c.ID, b.ID, core.RelDependsOn))
	require.NoError(t, e.Relate(ctx, b.ID, a.ID, core.RelDependsOn))

	d1, err := e.Related(ctx, c.ID, []core.RelationKind{core.RelDependsOn}, core.DirOut, 1)
	require.NoError(t, err)
	require.True(t, hasArtifact(d1, b.ID))
	require.False(t, hasArtifact(d1, a.ID), "depth 1 stops at b")

	d2, err := e.Related(ctx, c.ID, []core.RelationKind{core.RelDependsOn}, core.DirOut, 2)
	require.NoError(t, err)
	require.True(t, hasArtifact(d2, b.ID))
	require.True(t, hasArtifact(d2, a.ID), "depth 2 reaches a")
}

func TestRelated_ExcludesSuperseded(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	s, _ := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	a := pushInsight(t, e, s.ID, "anchor")
	b := pushInsight(t, e, s.ID, "old dep")
	require.NoError(t, e.Relate(ctx, a.ID, b.ID, core.RelDependsOn))

	b2 := pushInsight(t, e, s.ID, "new dep")
	require.NoError(t, e.Supersede(ctx, b.ID, b2.ID))

	deps, err := e.Related(ctx, a.ID, nil, core.DirOut, 1)
	require.NoError(t, err)
	require.False(t, hasArtifact(deps, b.ID), "superseded neighbour excluded from current view")
}

func TestDeleteArtifact_CleansEdges(t *testing.T) {
	ctx := context.Background()
	e := openEng(t)
	s, _ := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	a := pushInsight(t, e, s.ID, "anchor")
	b := pushInsight(t, e, s.ID, "dep")
	require.NoError(t, e.Relate(ctx, a.ID, b.ID, core.RelDependsOn))

	require.NoError(t, e.DeleteArtifact(ctx, b.ID))
	deps, err := e.Related(ctx, a.ID, nil, core.DirOut, 1)
	require.NoError(t, err)
	require.Empty(t, deps, "edges to a deleted artifact are removed")
}
