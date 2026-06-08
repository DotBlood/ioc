package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
)

func hitWith(hits []core.Hit, substr string) (core.Hit, bool) {
	for _, h := range hits {
		if strings.Contains(strings.ToLower(h.Summary), strings.ToLower(substr)) {
			return h, true
		}
	}
	return core.Hit{}, false
}

func TestEngine_FullLoop(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(384))
	require.NoError(t, err)
	defer e.Close()

	// Recursive scopes: worktree > workspace > session.
	wt, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "garden-tools")
	require.NoError(t, err)
	ws, err := e.CreateScope(ctx, wt.ID, core.RoleWorkspace, "tool-design")
	require.NoError(t, err)
	s1, err := e.CreateScope(ctx, ws.ID, core.RoleSession, "stick")
	require.NoError(t, err)

	// Push an insight with full content, published.
	a, err := e.Push(ctx, core.PushRequest{
		Scope:   s1.ID,
		Kind:    core.KindInsight,
		Summary: "the wooden tool head splits under load",
		Content: []byte("Detailed test log: under 5kg load the wooden head fractured at the neck."),
		Publish: true,
	})
	require.NoError(t, err)
	require.False(t, a.EmbRef.IsZero())
	require.False(t, a.Content.IsZero())

	// Query at overview from s1: the insight should be visible (own scope).
	_, hits, err := e.Query(ctx, core.Query{Scope: s1.ID, Text: "what material should the head be", Detail: core.DetailOverview, TopK: 5})
	require.NoError(t, err)
	h, ok := hitWith(hits, "wooden tool head splits")
	require.True(t, ok, "expected the wooden-head insight in overview")
	require.Empty(t, h.Content, "overview must not carry raw content")

	// Drill to raw: content from CAS.
	raw, err := e.Drill(ctx, h.Artifact, core.DetailRaw)
	require.NoError(t, err)
	require.Contains(t, string(raw.Content), "fractured at the neck")

	// Sibling visibility: a second session sees s1's PUBLISHED artifact.
	s2, err := e.CreateScope(ctx, ws.ID, core.RoleSession, "digging-stick")
	require.NoError(t, err)
	sib, err := e.SiblingOverview(ctx, s2.ID)
	require.NoError(t, err)
	_, ok = hitWith(sib, "wooden tool head splits")
	require.True(t, ok, "sibling should see published artifact")

	// Fork keeps both; the fork gets a copy of published artifacts.
	f, err := e.Fork(ctx, s1.ID, "hoe")
	require.NoError(t, err)
	require.Equal(t, s1.ID, f.ForkedFrom)
	forkArts, err := e.meta.ArtifactsInScope(f.ID)
	require.NoError(t, err)
	require.Len(t, forkArts, 1)
	require.Equal(t, "the wooden tool head splits under load", forkArts[0].Summary)

	// Consolidate: a worktree-tier summary promoted to the parent (ws).
	cons, err := e.Consolidate(ctx, s1.ID, "decision: the head must be metal", nil)
	require.NoError(t, err)
	require.Equal(t, core.TierWorktree, cons.Tier)
	require.Equal(t, ws.ID, cons.Scope)

	// Version boundary: archive ws, open v2 seeded with the constraint.
	ns, err := e.CrossVersion(ctx, ws.ID, core.Seed{
		Constraints: "the head must be metal, never wood",
		Lessons:     "a wooden head splits under load",
	})
	require.NoError(t, err)
	require.Equal(t, 2, ns.Version)
	require.False(t, ns.SeedFrom.IsZero())

	archived, err := e.meta.GetScope(ws.ID)
	require.NoError(t, err)
	require.True(t, archived.Archived)

	// In v2, the seed constraint surfaces and mentions metal.
	_, v2hits, err := e.Query(ctx, core.Query{Scope: ns.ID, Text: "what material for the head", Detail: core.DetailOverview, TopK: 5})
	require.NoError(t, err)
	_, ok = hitWith(v2hits, "metal")
	require.True(t, ok, "seed constraint should surface in v2")
}
