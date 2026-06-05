package storage

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// At-rest confidentiality (V4): the store's files are owner-only. chmod is a
// no-op on Windows, so the mode assertions only run on POSIX (the CI matrix
// includes ubuntu).
func TestStorageFileModes0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not enforced on Windows")
	}
	dir := t.TempDir()

	// meta.db
	m, err := OpenMeta(filepath.Join(dir, "meta.db"))
	require.NoError(t, err)
	require.NoError(t, m.Close())
	requireMode(t, filepath.Join(dir, "meta.db"), 0o600)

	// emb.dat
	es, err := OpenEmbeddingStore(filepath.Join(dir, "emb.dat"), 16)
	require.NoError(t, err)
	require.NoError(t, es.Close())
	requireMode(t, filepath.Join(dir, "emb.dat"), 0o600)

	// CAS object + its dirs
	cas := NewCAS(filepath.Join(dir, "cas"))
	if _, err := cas.StoreBytes(context.Background(), []byte("secret reasoning")); err != nil {
		t.Fatalf("store: %v", err)
	}
	var obj string
	_ = filepath.WalkDir(filepath.Join(dir, "cas"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			obj = p
		}
		return nil
	})
	require.NotEmpty(t, obj, "expected a CAS object file")
	requireMode(t, obj, 0o600)
	requireMode(t, filepath.Dir(obj), 0o700)
}

// Pre-existing world-readable files are tightened on open (migration).
func TestStorageMigratesExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not enforced on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.db")

	m, err := OpenMeta(path)
	require.NoError(t, err)
	require.NoError(t, m.Close())
	require.NoError(t, os.Chmod(path, 0o644)) // simulate an older build's loose mode
	requireMode(t, path, 0o644)

	m2, err := OpenMeta(path)
	require.NoError(t, err)
	require.NoError(t, m2.Close())
	requireMode(t, path, 0o600)
}

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equalf(t, want, fi.Mode().Perm(), "%s mode = %o, want %o", path, fi.Mode().Perm(), want)
}
