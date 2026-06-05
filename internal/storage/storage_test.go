package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCAS_RoundtripAndDedup(t *testing.T) {
	ctx := context.Background()
	c := NewCAS(t.TempDir(), nil)

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

// M2: StoreBytes writes atomically (temp + rename) and leaves no temp files;
// the stored object round-trips.
func TestCAS_AtomicStoreNoTempLeftover(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	c := NewCAS(root, nil)

	data := []byte("the metal head holds under load")
	h, err := c.StoreBytes(ctx, data)
	require.NoError(t, err)
	got, err := c.Load(ctx, h)
	require.NoError(t, err)
	require.Equal(t, data, got)

	// No "tmp-*" remnants under the CAS root.
	require.NoError(t, filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), "tmp-") {
			t.Fatalf("leftover temp file: %s", p)
		}
		return nil
	}))
}

// V5: Load must refuse to decompress past the configured cap (bomb defense)
// instead of allocating unbounded memory.
func TestCAS_DecompressionBombCapped(t *testing.T) {
	ctx := context.Background()
	old := maxCASDecodedBytes
	maxCASDecodedBytes = 1 << 20 // 1 MiB cap for this test
	defer func() { maxCASDecodedBytes = old }()

	c := NewCAS(t.TempDir(), nil)
	big := make([]byte, 8<<20) // 8 MiB of zeros — compresses tiny, expands past the cap
	h, err := c.StoreBytes(ctx, big)
	require.NoError(t, err)
	_, err = c.Load(ctx, h)
	require.Error(t, err, "decompressing past the cap must error, not OOM")
}

func TestEmbeddingStore_PutGetPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "emb.dat")

	s, err := OpenEmbeddingStore(path, 4, nil)
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
	s2, err := OpenEmbeddingStore(path, 4, nil)
	require.NoError(t, err)
	defer s2.Close()
	require.Equal(t, 2, s2.Len())
	v2, err := s2.Get(r2)
	require.NoError(t, err)
	require.Equal(t, []float32{0, 1, 0, 0}, v2)
}
