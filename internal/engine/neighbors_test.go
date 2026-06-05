package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
)

// Neighbors returns the top-k CURRENT artifacts for some text (superseded ones
// excluded), and the returned ids feed straight into a follow-up Supersedes.
func TestNeighbors(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(16))
	require.NoError(t, err)
	defer e.Close()

	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)

	old, err := e.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "use sqlite for storage"})
	require.NoError(t, err)
	cur, err := e.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "use postgres for storage, sqlite was rejected"})
	require.NoError(t, err)

	// Before any supersession both are current and can be neighbors.
	got, err := e.Neighbors(ctx, root.ID, "which storage engine", 5)
	require.NoError(t, err)
	require.True(t, hitsContainID(got, old.ID))
	require.True(t, hitsContainID(got, cur.ID))

	// The agent declares the supersession it discovered via Neighbors.
	_, err = e.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "postgres is the chosen storage engine", Supersedes: []core.ID{old.ID, cur.ID}})
	require.NoError(t, err)

	// Now Neighbors returns only the current artifact, not the superseded ones.
	got2, err := e.Neighbors(ctx, root.ID, "which storage engine", 5)
	require.NoError(t, err)
	require.False(t, hitsContainID(got2, old.ID), "superseded neighbor excluded")
	require.False(t, hitsContainID(got2, cur.ID), "superseded neighbor excluded")

	// k bounds the result.
	limited, err := e.Neighbors(ctx, root.ID, "storage", 1)
	require.NoError(t, err)
	require.LessOrEqual(t, len(limited), 1)
}
