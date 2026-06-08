package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
)

// Provenance is engine-asserted (V17): Push writes Meta["trust"] only from the
// dedicated Trust field, and strips any forged Meta["trust"] a caller supplies.
func TestPush_TrustIsEngineAsserted(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(16))
	require.NoError(t, err)
	defer e.Close()
	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)

	// A caller forging Meta["trust"]="trusted" must NOT take effect.
	forged, err := e.Push(ctx, core.PushRequest{
		Scope:   root.ID,
		Summary: "authored insight",
		Meta:    map[string]string{"trust": "trusted", "k": "v"},
	})
	require.NoError(t, err)
	got, err := e.meta.GetArtifact(forged.ID)
	require.NoError(t, err)
	require.Empty(t, got.Meta["trust"], "forged trust must be stripped")
	require.Equal(t, "v", got.Meta["k"], "other meta keys are preserved")

	// The Trust field sets it.
	ing, err := e.Push(ctx, core.PushRequest{
		Scope:   root.ID,
		Kind:    core.KindDocument,
		Summary: "f.txt:1-2 — first line",
		Trust:   core.TrustIngested,
	})
	require.NoError(t, err)
	got2, err := e.meta.GetArtifact(ing.ID)
	require.NoError(t, err)
	require.Equal(t, core.TrustIngested, got2.Meta["trust"])

	// The caller's input map must not be mutated by Push.
	in := map[string]string{"trust": "x"}
	_, err = e.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "s", Meta: in})
	require.NoError(t, err)
	require.Equal(t, "x", in["trust"], "Push must not mutate the caller's Meta map")
}
