package engine

import (
	"context"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
)

func newKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	_, err := rand.Read(k)
	require.NoError(t, err)
	return k
}

// An encrypted store round-trips end-to-end and persists across reopen with the
// same key; the sentinel matrix fails closed on key/format mismatch.
func TestEngine_Encryption(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	key := newKey(t)

	// Create + write through an encrypted store.
	e, err := Open(ctx, dir, embed.NewMockEmbedder(16), WithEncryptionKey(key))
	require.NoError(t, err)
	wt, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	a, err := e.Push(ctx, core.PushRequest{Scope: wt.ID, Summary: "encrypted insight", Content: []byte("cold raw body")})
	require.NoError(t, err)
	_, hits, err := e.Query(ctx, core.Query{Scope: wt.ID, Text: "encrypted insight", TopK: 5})
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	drill, err := e.Drill(ctx, a.ID, core.DetailRaw)
	require.NoError(t, err)
	require.Equal(t, "cold raw body", string(drill.Content))
	require.NoError(t, e.Close())

	// Reopen with the SAME key → everything readable.
	e2, err := Open(ctx, dir, embed.NewMockEmbedder(16), WithEncryptionKey(key))
	require.NoError(t, err)
	got, err := e2.GetScope(ctx, wt.ID)
	require.NoError(t, err)
	require.Equal(t, "root", got.Title)
	require.NoError(t, e2.Close())

	// Reopen with a WRONG key → reads fail (Query surfaces a decrypt error).
	e3, err := Open(ctx, dir, embed.NewMockEmbedder(16), WithEncryptionKey(newKey(t)))
	require.NoError(t, err) // sentinel says encrypted + a key is present → opens
	_, _, err = e3.Query(ctx, core.Query{Scope: wt.ID, Text: "x", TopK: 5})
	require.Error(t, err, "wrong key must surface as a read error, not garbage")
	_ = e3.Close()

	// Reopen encrypted store with NO key → refused at Open.
	_, err = Open(ctx, dir, embed.NewMockEmbedder(16))
	require.Error(t, err)
	require.ErrorIs(t, err, core.ErrInvalidInput)
}

// A non-empty plaintext store opened WITH a key is refused (no in-place migration);
// the plaintext-no-key path is unchanged.
func TestEngine_PlaintextWithKeyRefused(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	e, err := Open(ctx, dir, embed.NewMockEmbedder(16)) // plaintext
	require.NoError(t, err)
	_, err = e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	require.NoError(t, e.Close())

	_, err = Open(ctx, dir, embed.NewMockEmbedder(16), WithEncryptionKey(newKey(t)))
	require.Error(t, err)
	require.ErrorIs(t, err, core.ErrInvalidInput)

	// Plaintext reopen with no key → still works.
	e2, err := Open(ctx, dir, embed.NewMockEmbedder(16))
	require.NoError(t, err)
	require.NoError(t, e2.Close())
}
