package ingest

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
)

// V9: a run exceeding MaxFiles stops at a file boundary, reports ErrIngestLimit +
// LimitHit, and still committed the files it did process (no mid-file partial).
func TestIngest_MaxFiles(t *testing.T) {
	e := openTestEngine(t)
	dir := ingestRootDir(t)
	for i := 0; i < 5; i++ {
		write(t, filepath.Join(dir, fmt.Sprintf("f%d.txt", i)), "some content line\n")
	}
	root := newRoot(t, e)

	st, err := Ingest(context.Background(), e, dir, root, Options{MaxFiles: 3})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrIngestLimit))
	require.Equal(t, "max_files", st.LimitHit)
	// Stopped at the boundary: at most MaxFiles files were added, and each is whole.
	require.LessOrEqual(t, st.FilesAdded, 3)
	require.Greater(t, len(docArtifacts(t, e)), 0, "files processed before the cap are committed")
}

// V9: a directory deeper than MaxDepth is rejected.
func TestIngest_MaxDepth(t *testing.T) {
	e := openTestEngine(t)
	dir := ingestRootDir(t)
	write(t, filepath.Join(dir, "a", "b", "c", "deep.txt"), "deep content\n")
	root := newRoot(t, e)

	st, err := Ingest(context.Background(), e, dir, root, Options{MaxDepth: 2})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrIngestLimit))
	require.Equal(t, "max_depth", st.LimitHit)
}

// V9: a normal tree well under every cap ingests fully (regression — caps must not
// fire in the common case).
func TestIngest_UnderCapsFullIngest(t *testing.T) {
	e := openTestEngine(t)
	dir := ingestRootDir(t)
	write(t, filepath.Join(dir, "a.txt"), "alpha\n")
	write(t, filepath.Join(dir, "sub", "b.txt"), "bravo content\n")
	root := newRoot(t, e)

	st, err := Ingest(context.Background(), e, dir, root, Options{}) // all defaults
	require.NoError(t, err)
	require.Empty(t, st.LimitHit)
	require.Equal(t, 2, st.FilesAdded)
}

// V9: the in-memory index is bounded.
func TestIngest_MaxIndexArtifacts(t *testing.T) {
	e := openTestEngine(t)
	dir := ingestRootDir(t)
	// Seed a few artifacts so the store's artifact count exceeds the tiny cap.
	root := newRoot(t, e)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, err := e.Push(ctx, core.PushRequest{Scope: root, Summary: fmt.Sprintf("seed %d", i)})
		require.NoError(t, err)
	}
	write(t, filepath.Join(dir, "a.txt"), "alpha\n")

	st, err := Ingest(ctx, e, dir, root, Options{MaxIndexArtifacts: 1})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrIngestLimit))
	require.Equal(t, "max_index_artifacts", st.LimitHit)
}
