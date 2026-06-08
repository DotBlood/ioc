package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
)

// renamedEmbedder reports a different model id than the wrapped embedder (same
// dims) — to simulate two embedders that share a dimension (e.g. the 384-dim
// mock vs bge-small).
type renamedEmbedder struct {
	embed.Embedder
	model string
}

func (r renamedEmbedder) Model() string { return r.model }

// lazyModelEmbedder reports "" until its first Embed/EmbedQuery, like an HTTP
// embedder whose model is unknown until the first response.
type lazyModelEmbedder struct {
	embed.Embedder
	model string
	used  bool
}

func (l *lazyModelEmbedder) Embed(ctx context.Context, t []string) ([][]float32, error) {
	l.used = true
	return l.Embedder.Embed(ctx, t)
}
func (l *lazyModelEmbedder) EmbedQuery(ctx context.Context, t []string) ([][]float32, error) {
	l.used = true
	return l.Embedder.EmbedQuery(ctx, t)
}
func (l *lazyModelEmbedder) Model() string {
	if !l.used {
		return ""
	}
	return l.model
}

func seedStore(t *testing.T, dir string) {
	t.Helper()
	ctx := context.Background()
	e, err := Open(ctx, dir, embed.NewMockEmbedder(384)) // stores emb_dims=384, emb_model=mock-bow
	require.NoError(t, err)
	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	_, err = e.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "seed"})
	require.NoError(t, err)
	require.NoError(t, e.Close())
}

// H2: a different-model embedder with the SAME dimension must be refused (the
// dim check alone would miss it).
func TestModelGuard_RenamedRefusedAtOpen(t *testing.T) {
	dir := t.TempDir()
	seedStore(t, dir)
	_, err := Open(context.Background(), dir, renamedEmbedder{embed.NewMockEmbedder(384), "some-other-model"})
	require.Error(t, err, "opening with a different model at same dims must be refused")
	require.ErrorIs(t, err, core.ErrInvalidInput)
}

// Same embedder reopens fine.
func TestModelGuard_SameModelOK(t *testing.T) {
	dir := t.TempDir()
	seedStore(t, dir)
	e, err := Open(context.Background(), dir, embed.NewMockEmbedder(384))
	require.NoError(t, err)
	_ = e.Close()
}

// Different dimension is refused at Open.
func TestModelGuard_DimMismatchRefusedAtOpen(t *testing.T) {
	dir := t.TempDir()
	seedStore(t, dir)
	_, err := Open(context.Background(), dir, embed.NewMockEmbedder(32))
	require.Error(t, err)
	require.ErrorIs(t, err, core.ErrInvalidInput)
}

// An HTTP-style embedder whose model is unknown at Open passes Open but is caught
// on first use (reconcileModel) when its model disagrees with the stored one.
func TestModelGuard_LazyModelCaughtOnFirstUse(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	seedStore(t, dir) // emb_model=mock-bow

	lazy := &lazyModelEmbedder{Embedder: embed.NewMockEmbedder(384), model: "lazy-other"}
	e, err := Open(ctx, dir, lazy) // model "" at open → passes the eager check
	require.NoError(t, err)
	defer e.Close()

	root, err := e.ListScopes(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, root)
	// First embed reveals the model; reconcileModel must refuse.
	_, _, err = e.Query(ctx, core.Query{Scope: root[0].ID, Text: "anything", TopK: 3})
	require.Error(t, err, "lazy model mismatch must be caught on first query")
	require.ErrorIs(t, err, core.ErrInvalidInput)
}
