package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCAS_RoundtripAndDedup(t *testing.T) {
	ctx := context.Background()
	c := NewCAS(t.TempDir())

	data := []byte("the wooden head splits under load")
	h1, err := c.StoreBytes(ctx, data)
	require.NoError(t, err)

	got, err := c.Load(ctx, h1)
	require.NoError(t, err)
	require.Equal(t, data, got)

	ok, err := c.Has(ctx, h1)
	require.NoError(t, err)
	require.True(t, ok)

	// Dedup: storing identical content yields the same hash.
	h2, err := c.StoreBytes(ctx, data)
	require.NoError(t, err)
	require.Equal(t, h1, h2)
}

func TestEmbeddingStore_PutGetPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "emb.dat")

	s, err := OpenEmbeddingStore(path, 4)
	require.NoError(t, err)

	r1, err := s.Put([]float32{1, 0, 0, 0})
	require.NoError(t, err)
	r2, err := s.Put([]float32{0, 1, 0, 0})
	require.NoError(t, err)
	require.NotEqual(t, r1, r2)
	require.Equal(t, 2, s.Len())

	v1, err := s.Get(r1)
	require.NoError(t, err)
	require.Equal(t, []float32{1, 0, 0, 0}, v1)

	require.NoError(t, s.Close())

	// Reopen and verify persistence.
	s2, err := OpenEmbeddingStore(path, 4)
	require.NoError(t, err)
	defer s2.Close()
	require.Equal(t, 2, s2.Len())
	v2, err := s2.Get(r2)
	require.NoError(t, err)
	require.Equal(t, []float32{0, 1, 0, 0}, v2)
}
