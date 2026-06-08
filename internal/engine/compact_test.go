package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
)

// pushN creates a session scope under a fresh worktree and pushes len(summaries)
// artifacts, returning the scope and the artifact IDs in order.
func pushN(t *testing.T, e *Engine, summaries ...string) (core.Scope, []core.ID) {
	t.Helper()
	ctx := context.Background()
	wt, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "wt")
	require.NoError(t, err)
	s, err := e.CreateScope(ctx, wt.ID, core.RoleSession, "s")
	require.NoError(t, err)
	ids := make([]core.ID, 0, len(summaries))
	for _, sum := range summaries {
		a, perr := e.Push(ctx, core.PushRequest{Scope: s.ID, Kind: core.KindInsight, Summary: sum})
		require.NoError(t, perr)
		require.False(t, a.EmbRef.IsZero())
		ids = append(ids, a.ID)
	}
	return s, ids
}

func TestCompact_NoopWhenDense(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(16))
	require.NoError(t, err)
	defer e.Close()

	pushN(t, e, "alpha one", "beta two", "gamma three")

	st, err := e.Compact(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, st.OldRecords)
	require.Equal(t, 3, st.LiveRecords)
	require.Equal(t, 0, st.Reclaimed)
	// No generation file was created; the live file is still the default.
	require.Equal(t, defaultEmbFile, e.currentEmbFile())
}

func TestCompact_ReclaimsOrphansAndPreservesRetrieval(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	e, err := Open(ctx, dir, embed.NewMockEmbedder(16))
	require.NoError(t, err)
	defer e.Close()

	// refs 1..5
	s, ids := pushN(t, e, "apple pie", "banana bread", "cherry tart", "date cake", "elder wine")

	// Delete refs 2 (banana) and 4 (date) → orphans. Live = {1,3,5}.
	require.NoError(t, e.DeleteArtifact(ctx, ids[1]))
	require.NoError(t, e.DeleteArtifact(ctx, ids[3]))

	st, err := e.Compact(ctx)
	require.NoError(t, err)
	require.Equal(t, 5, st.OldRecords)
	require.Equal(t, 3, st.LiveRecords)
	require.Equal(t, 2, st.Reclaimed)
	require.Less(t, st.NewBytes, st.OldBytes, "compacted file must be smaller")
	require.Greater(t, st.ReclaimedBytes, int64(0))

	// emb_file flipped to a generation file; old emb.dat removed.
	require.Equal(t, "emb.1.dat", e.currentEmbFile())
	_, statErr := os.Stat(filepath.Join(dir, defaultEmbFile))
	require.True(t, os.IsNotExist(statErr), "old emb.dat should be gone")
	require.Equal(t, 3, e.emb.Len(), "live store holds exactly the live vectors")

	// Retrieval integrity: each surviving summary still retrieves its own
	// artifact as a strong hit (refs were correctly remapped to the moved vectors).
	for _, want := range []string{"apple pie", "cherry tart", "elder wine"} {
		_, hits, qerr := e.Query(ctx, core.Query{Scope: s.ID, Text: want, Detail: core.DetailOverview, TopK: 3})
		require.NoError(t, qerr)
		_, ok := hitWith(hits, want)
		require.Truef(t, ok, "post-compact query for %q must find it", want)
	}
	// Deleted summaries must not reappear.
	_, hits, qerr := e.Query(ctx, core.Query{Scope: s.ID, Text: "banana bread", Detail: core.DetailOverview, TopK: 5})
	require.NoError(t, qerr)
	_, ok := hitWith(hits, "banana bread")
	require.False(t, ok, "deleted artifact must not resurface")
}

func TestCompact_RemapsRollupRef(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(16))
	require.NoError(t, err)
	defer e.Close()

	s, ids := pushN(t, e, "one fish", "two fish", "red fish")
	// Give the scope a rollup (allocates an embedding ref after the artifacts).
	require.NoError(t, e.RollupScope(ctx, s.ID, "a scope about fish"))
	before, err := e.meta.GetScope(s.ID)
	require.NoError(t, err)
	require.False(t, before.RollupEmbRef.IsZero())

	// Create an orphan so compaction actually runs and shifts refs.
	require.NoError(t, e.DeleteArtifact(ctx, ids[0]))

	_, err = e.Compact(ctx)
	require.NoError(t, err)

	after, err := e.meta.GetScope(s.ID)
	require.NoError(t, err)
	require.NotEqual(t, before.RollupEmbRef, after.RollupEmbRef, "rollup ref should be remapped")
	// The remapped rollup ref resolves in the new store.
	_, gerr := e.emb.Get(after.RollupEmbRef)
	require.NoError(t, gerr)
}

func TestCompact_IdempotentAndGenerationIncrements(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	e, err := Open(ctx, dir, embed.NewMockEmbedder(16))
	require.NoError(t, err)
	defer e.Close()

	s, ids := pushN(t, e, "aa", "bb", "cc", "dd")
	require.NoError(t, e.DeleteArtifact(ctx, ids[0]))

	st1, err := e.Compact(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, st1.Reclaimed)
	require.Equal(t, "emb.1.dat", e.currentEmbFile())

	// Re-compacting a dense store is a no-op.
	st2, err := e.Compact(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, st2.Reclaimed)
	require.Equal(t, "emb.1.dat", e.currentEmbFile())

	// New orphan → next compaction rotates to emb.2.dat and drops emb.1.dat.
	require.NoError(t, e.DeleteArtifact(ctx, ids[1]))
	st3, err := e.Compact(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, st3.Reclaimed)
	require.Equal(t, "emb.2.dat", e.currentEmbFile())
	_, statErr := os.Stat(filepath.Join(dir, "emb.1.dat"))
	require.True(t, os.IsNotExist(statErr), "previous generation file should be gone")
	_ = s
}

func TestCompact_EncryptedStoreRoundTrips(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	dir := t.TempDir()
	e, err := Open(ctx, dir, embed.NewMockEmbedder(16), WithEncryptionKey(key))
	require.NoError(t, err)

	s, ids := pushN(t, e, "secret alpha", "secret beta", "secret gamma")
	require.NoError(t, e.DeleteArtifact(ctx, ids[1])) // orphan the middle ref

	st, err := e.Compact(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, st.Reclaimed)
	require.Equal(t, 2, e.emb.Len())

	// Surviving encrypted vectors must still decrypt + retrieve under their
	// remapped refs (Get opens with the old AAD, Put re-sealed under the new ref).
	for _, want := range []string{"secret alpha", "secret gamma"} {
		_, hits, qerr := e.Query(ctx, core.Query{Scope: s.ID, Text: want, Detail: core.DetailOverview, TopK: 3})
		require.NoError(t, qerr)
		_, ok := hitWith(hits, want)
		require.Truef(t, ok, "encrypted post-compact query for %q must find it", want)
	}
	require.NoError(t, e.Close())

	// Reopen with the key: the compacted encrypted file reads back cleanly.
	e2, err := Open(ctx, dir, embed.NewMockEmbedder(16), WithEncryptionKey(key))
	require.NoError(t, err)
	defer e2.Close()
	require.Equal(t, 2, e2.emb.Len())
	_, hits, qerr := e2.Query(ctx, core.Query{Scope: s.ID, Text: "secret gamma", Detail: core.DetailOverview, TopK: 3})
	require.NoError(t, qerr)
	_, ok := hitWith(hits, "secret gamma")
	require.True(t, ok, "reopened encrypted compacted store must retrieve")
}

func TestCompact_OrphanFileSweptOnOpen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	e, err := Open(ctx, dir, embed.NewMockEmbedder(16))
	require.NoError(t, err)
	pushN(t, e, "x marks", "y axis")
	require.NoError(t, e.Close())

	// Simulate a crashed-compaction leftover: a stray generation file.
	stray := filepath.Join(dir, "emb.99.dat")
	require.NoError(t, os.WriteFile(stray, []byte("garbage"), 0o600))

	e2, err := Open(ctx, dir, embed.NewMockEmbedder(16))
	require.NoError(t, err)
	defer e2.Close()
	_, statErr := os.Stat(stray)
	require.True(t, os.IsNotExist(statErr), "orphan emb.99.dat should be swept on Open")
	// The live store is intact.
	require.Equal(t, 2, e2.emb.Len())
}
