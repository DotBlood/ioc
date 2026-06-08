package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
)

func mustBox(t *testing.T) *Box {
	t.Helper()
	b, err := NewBox(testKey(t))
	require.NoError(t, err)
	return b
}

// CAS: encrypted round-trip, dedup preserved, wrong key fails, and the on-disk
// bytes are NOT the plaintext.
func TestCAS_Encrypted(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "cas")
	box := mustBox(t)
	c := NewCAS(root, box)

	data := []byte("proprietary reasoning that must not sit in plaintext")
	h, err := c.StoreBytes(ctx, data)
	require.NoError(t, err)
	h2, err := c.StoreBytes(ctx, data)
	require.NoError(t, err)
	require.Equal(t, h, h2, "same plaintext dedups under encryption")
	require.True(t, c.has(h))

	got, err := c.Load(ctx, h)
	require.NoError(t, err)
	require.Equal(t, data, got)

	// A different key cannot read it.
	wrong := NewCAS(root, mustBox(t))
	_, err = wrong.Load(ctx, h)
	require.Error(t, err)

	// No-key reader gets ciphertext through zstd → decode error (not the plaintext).
	plain := NewCAS(root, nil)
	_, err = plain.Load(ctx, h)
	require.Error(t, err)
}

// EmbeddingStore: encrypted Put/Get round-trips across reopen; flag/key mismatch
// on reopen is a clear error; wrong key fails Get (no panic, no garbage).
func TestEmbStore_Encrypted(t *testing.T) {
	p := filepath.Join(t.TempDir(), "emb.dat")
	box := mustBox(t)

	s, err := OpenEmbeddingStore(p, 4, box)
	require.NoError(t, err)
	ref, err := s.Put([]float32{1, 2, 3, 4})
	require.NoError(t, err)
	got, err := s.Get(ref)
	require.NoError(t, err)
	require.Equal(t, []float32{1, 2, 3, 4}, got)
	require.NoError(t, s.Close())

	// Reopen with the same key → persists.
	s2, err := OpenEmbeddingStore(p, 4, box)
	require.NoError(t, err)
	got2, err := s2.Get(ref)
	require.NoError(t, err)
	require.Equal(t, []float32{1, 2, 3, 4}, got2)
	require.NoError(t, s2.Close())

	// Reopen encrypted file with NO key → error.
	_, err = OpenEmbeddingStore(p, 4, nil)
	require.Error(t, err)

	// Reopen with a WRONG key → opens (header is plaintext) but Get fails cleanly.
	s3, err := OpenEmbeddingStore(p, 4, mustBox(t))
	require.NoError(t, err)
	_, err = s3.Get(ref)
	require.Error(t, err)
	require.NoError(t, s3.Close())
}

// A plaintext emb file opened WITH a key is refused (no mixing).
func TestEmbStore_PlaintextFileWithKeyRefused(t *testing.T) {
	p := filepath.Join(t.TempDir(), "emb.dat")
	s, err := OpenEmbeddingStore(p, 4, nil)
	require.NoError(t, err)
	_, _ = s.Put([]float32{1, 2, 3, 4})
	require.NoError(t, s.Close())

	_, err = OpenEmbeddingStore(p, 4, mustBox(t))
	require.Error(t, err)
}

// Meta: encrypted values round-trip (Put/Get + ForEach); wrong key fails reads;
// the always-plaintext "enc" sentinel is readable without the key.
func TestMeta_Encrypted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.db")
	box := mustBox(t)

	m, err := OpenMeta(path, box)
	require.NoError(t, err)
	require.NoError(t, m.PutConfig("enc", "aes256gcm")) // plaintext sentinel
	sc := core.Scope{ID: core.NewID(), Role: core.RoleWorktree, Title: "secret-title"}
	require.NoError(t, m.PutScope(sc))
	require.NoError(t, m.PutConfig("emb_model", "BAAI/bge-small-en-v1.5"))
	require.NoError(t, m.Close())

	// Reopen same key → values decrypt.
	m2, err := OpenMeta(path, box)
	require.NoError(t, err)
	got, err := m2.GetScope(sc.ID)
	require.NoError(t, err)
	require.Equal(t, "secret-title", got.Title)
	all, err := m2.ListScopes()
	require.NoError(t, err)
	require.Len(t, all, 1)
	model, ok := m2.GetConfig("emb_model")
	require.True(t, ok)
	require.Equal(t, "BAAI/bge-small-en-v1.5", model)
	// Sentinel readable without decryption.
	enc, ok := m2.GetConfig("enc")
	require.True(t, ok)
	require.Equal(t, "aes256gcm", enc)
	require.NoError(t, m2.Close())

	// Wrong key → encrypted reads fail; the plaintext sentinel still reads.
	m3, err := OpenMeta(path, mustBox(t))
	require.NoError(t, err)
	_, err = m3.GetScope(sc.ID)
	require.Error(t, err)
	enc2, ok := m3.GetConfig("enc")
	require.True(t, ok)
	require.Equal(t, "aes256gcm", enc2)
	_, ok = m3.GetConfig("emb_model") // encrypted value, wrong key → not ok
	require.False(t, ok)
	require.NoError(t, m3.Close())
}
