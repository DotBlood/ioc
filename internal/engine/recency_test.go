package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
)

// The recency tie-breaker (opt-in) promotes a newer near-tie above an older one,
// but does NOT override a clearly-stronger cosine match; off by default it's pure
// cosine. Uses a pinned clock for determinism.
func TestRecencyTieBreaker(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(32))
	require.NoError(t, err)
	defer e.Close()

	// Pin "now" so CreatedAt ages are deterministic.
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	old := clockNow
	clockNow = func() time.Time { return now }
	defer func() { clockNow = old }()

	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)

	// Two near-identical summaries (near-tie cosine). Push order = insertion order;
	// we backdate one via meta to make it clearly older.
	older, err := e.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "the deployment uses a blue green rollout strategy alpha"})
	require.NoError(t, err)
	newer, err := e.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "the deployment uses a blue green rollout strategy beta"})
	require.NoError(t, err)

	// Backdate `older` by 60 days, keep `newer` recent.
	setCreatedAt(t, e, older.ID, now.Add(-60*24*time.Hour))
	setCreatedAt(t, e, newer.ID, now.Add(-1*24*time.Hour))

	q := core.Query{Scope: root.ID, Text: "blue green rollout strategy", TopK: 5}

	// Off by default → pure cosine (we only assert both are present; order is the
	// embedder's cosine order, unaffected by age).
	_, base, err := e.Query(ctx, q)
	require.NoError(t, err)
	require.True(t, hitsContainID(base, older.ID))
	require.True(t, hitsContainID(base, newer.ID))

	// With a short half-life, the newer near-tie should rank at or above the older.
	q.RecencyHalfLifeDays = 10
	_, withRec, err := e.Query(ctx, q)
	require.NoError(t, err)
	require.Equal(t, newer.ID, withRec[0].Artifact, "recency should lift the newer near-tie to the top")
	// Hit.Score stays cosine (recency only reorders).
	for _, h := range withRec {
		require.LessOrEqual(t, h.Score, 1.0)
	}
}

// A clearly stronger cosine match still wins despite being older (tie-breaker, not
// override).
func TestRecencyDoesNotOverrideStrongMatch(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(32))
	require.NoError(t, err)
	defer e.Close()

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	old := clockNow
	clockNow = func() time.Time { return now }
	defer func() { clockNow = old }()

	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)

	// Strong match (shares all query terms), but old.
	strong, err := e.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "postgres jsonb storage engine decision"})
	require.NoError(t, err)
	// Weak match (shares little), but brand new.
	weak, err := e.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "unrelated note about logging format"})
	require.NoError(t, err)
	setCreatedAt(t, e, strong.ID, now.Add(-365*24*time.Hour))
	setCreatedAt(t, e, weak.ID, now)

	_, hits, err := e.Query(ctx, core.Query{Scope: root.ID, Text: "postgres jsonb storage engine", TopK: 5, RecencyHalfLifeDays: 30})
	require.NoError(t, err)
	require.Equal(t, strong.ID, hits[0].Artifact, "a clearly stronger cosine match must still win over a newer weak one")
	_ = weak
}

func setCreatedAt(t *testing.T, e *Engine, id core.ID, ts time.Time) {
	t.Helper()
	a, err := e.meta.GetArtifact(id)
	require.NoError(t, err)
	a.CreatedAt = ts
	require.NoError(t, e.meta.PutArtifact(a))
}
