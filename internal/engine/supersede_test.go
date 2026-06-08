package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
)

func hitsContainID(hits []core.Hit, id core.ID) bool {
	for _, h := range hits {
		if h.Artifact == id {
			return true
		}
	}
	return false
}

func summariesMentionAny(hits []core.Hit, terms []string) bool {
	for _, h := range hits {
		for _, t := range terms {
			if strings.Contains(strings.ToLower(h.Summary), strings.ToLower(t)) {
				return true
			}
		}
	}
	return false
}

// Push with Supersedes: the prior artifact is marked superseded, excluded from the
// default current view, and surfaced again only with IncludeSuperseded — carrying
// the back-link. The new artifact records the lineage in DerivedFrom.
func TestSupersede_PushReplacesPrior(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(64))
	require.NoError(t, err)
	defer e.Close()

	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)

	old, err := e.Push(ctx, core.PushRequest{Scope: root.ID, Kind: core.KindInsight,
		Summary: "the wooden tool head is fine under load"})
	require.NoError(t, err)
	neu, err := e.Push(ctx, core.PushRequest{Scope: root.ID, Kind: core.KindInsight,
		Summary:    "the tool head must be metal, the wooden head fails under load",
		Supersedes: []core.ID{old.ID}})
	require.NoError(t, err)

	// New artifact links the superseded one in its lineage.
	require.Contains(t, neu.DerivedFrom, old.ID)

	// Default query: only the current (new) artifact; the superseded one is gone.
	_, hits, err := e.Query(ctx, core.Query{Scope: root.ID, Text: "what material for the tool head", TopK: 5})
	require.NoError(t, err)
	require.True(t, hitsContainID(hits, neu.ID), "current artifact must be retrievable")
	require.False(t, hitsContainID(hits, old.ID), "superseded artifact must be excluded by default")

	// IncludeSuperseded surfaces history with the back-link.
	_, all, err := e.Query(ctx, core.Query{Scope: root.ID, Text: "what material for the tool head", TopK: 5, IncludeSuperseded: true})
	require.NoError(t, err)
	require.True(t, hitsContainID(all, old.ID), "history view must surface the superseded artifact")
	for _, h := range all {
		if h.Artifact == old.ID {
			require.Equal(t, neu.ID, h.SupersededBy, "superseded hit must carry the replacing id")
		}
	}
}

// The standalone Supersede method (post-hoc) has the same retrieval effect, and
// validates its inputs.
func TestSupersede_Method(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(64))
	require.NoError(t, err)
	defer e.Close()

	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	a, err := e.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "use sqlite for storage"})
	require.NoError(t, err)
	b, err := e.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "use postgres for storage, sqlite was rejected"})
	require.NoError(t, err)

	require.NoError(t, e.Supersede(ctx, a.ID, b.ID))

	_, hits, err := e.Query(ctx, core.Query{Scope: root.ID, Text: "which storage engine", TopK: 5})
	require.NoError(t, err)
	require.False(t, hitsContainID(hits, a.ID), "superseded artifact excluded after Supersede")
	require.True(t, hitsContainID(hits, b.ID))

	// Validation.
	require.Error(t, e.Supersede(ctx, a.ID, a.ID), "self-supersession rejected")
	require.Error(t, e.Supersede(ctx, core.NewID(), b.ID), "unknown old rejected")
	require.Error(t, e.Supersede(ctx, a.ID, core.NewID()), "unknown replacement rejected")
}

// Push validates superseded targets before writing; a bad id fails the push
// without half-applying.
func TestSupersede_PushRejectsUnknownTarget(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(64))
	require.NoError(t, err)
	defer e.Close()

	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	_, err = e.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "x", Supersedes: []core.ID{core.NewID()}})
	require.Error(t, err)
}

// Consolidate with supersedes atomically retires the artifacts it folds in: they
// leave the default current view and the consolidated summary records the lineage.
func TestConsolidate_BatchSupersedes(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(16))
	require.NoError(t, err)
	defer e.Close()

	wt, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "wt")
	require.NoError(t, err)
	ws, err := e.CreateScope(ctx, wt.ID, core.RoleWorkspace, "ws")
	require.NoError(t, err)

	a1, err := e.Push(ctx, core.PushRequest{Scope: ws.ID, Summary: "finding one about the gizmo", Publish: true})
	require.NoError(t, err)
	a2, err := e.Push(ctx, core.PushRequest{Scope: ws.ID, Summary: "finding two about the gizmo", Publish: true})
	require.NoError(t, err)

	cons, err := e.Consolidate(ctx, ws.ID, "consolidated: the gizmo conclusion", []core.ID{a1.ID, a2.ID})
	require.NoError(t, err)
	require.Contains(t, cons.DerivedFrom, a1.ID)
	require.Contains(t, cons.DerivedFrom, a2.ID)

	// The folded-in findings are superseded by the consolidated summary.
	for _, id := range []core.ID{a1.ID, a2.ID} {
		got, err := e.meta.GetArtifact(id)
		require.NoError(t, err)
		require.Equal(t, cons.ID, got.SupersededBy)
	}

	// Default query from the workspace no longer surfaces the retired findings.
	_, hits, err := e.Query(ctx, core.Query{Scope: ws.ID, Text: "gizmo finding", TopK: 5})
	require.NoError(t, err)
	require.False(t, hitsContainID(hits, a1.ID))
	require.False(t, hitsContainID(hits, a2.ID))

	// A bad supersedes id aborts before writing anything.
	_, err = e.Consolidate(ctx, ws.ID, "x", []core.ID{core.NewID()})
	require.Error(t, err)
}

// Retrieval honors Scope.Archived: after a CrossVersion, the archived old-version
// scope's published artifacts no longer compete from the new version's viewpoint
// (unless IncludeSuperseded). This is the version-boundary reset.
func TestSupersede_ArchivedScopeExcluded(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(64))
	require.NoError(t, err)
	defer e.Close()

	wt, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "project")
	require.NoError(t, err)
	v1, err := e.CreateScope(ctx, wt.ID, core.RoleWorkspace, "approach")
	require.NoError(t, err)
	_, err = e.Push(ctx, core.PushRequest{Scope: v1.ID, Summary: "the chosen approach is a knowledge graph engine", Publish: true})
	require.NoError(t, err)

	// Cross a version boundary: v1 is archived, v2 is its sibling.
	v2, err := e.CrossVersion(ctx, v1.ID, core.Seed{})
	require.NoError(t, err)

	// From v2, the archived v1's conclusion must NOT surface by default.
	_, hits, err := e.Query(ctx, core.Query{Scope: v2.ID, Text: "what approach did we choose", TopK: 5})
	require.NoError(t, err)
	require.False(t, summariesMentionAny(hits, []string{"knowledge graph"}), "archived version's artifact must be excluded by default")

	// With IncludeSuperseded, the archived version is visible again (history).
	_, all, err := e.Query(ctx, core.Query{Scope: v2.ID, Text: "what approach did we choose", TopK: 5, IncludeSuperseded: true})
	require.NoError(t, err)
	require.True(t, summariesMentionAny(all, []string{"knowledge graph"}), "history view must surface the archived version")
}
