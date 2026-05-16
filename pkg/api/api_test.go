package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DotBlood/ioc/internal/store"
	"github.com/stretchr/testify/require"
)

func initRepo(t *testing.T, dir string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "db"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "cas"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "emb"), 0755))

	disk, err := store.OpenOrCreate(filepath.Join(dir, "db", "ioc.db"))
	require.NoError(t, err)

	emb, err := store.OpenEmbeddingStore(filepath.Join(dir, "emb", "default.emb"), 384)
	require.NoError(t, err)

	require.NoError(t, disk.SetMeta("ioc_version", "0.1.0"))

	disk.Close()
	emb.Close()
}

func TestOpenClose(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(context.Background(), Config{RootDir: dir})
	require.NoError(t, err)
	require.NotNil(t, rt)
	require.NoError(t, rt.Close())
}

func TestOpen_NotInitialized(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(context.Background(), Config{RootDir: dir})
	require.ErrorIs(t, err, ErrNotInitialized)
}

func TestCreateScope_Worktree(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	info, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)
	require.NotEmpty(t, info.ScopeID)
	require.Equal(t, "worktree", info.Type)
	require.Equal(t, "draft", info.State)
	require.Empty(t, info.ParentID)
	require.False(t, info.CreatedAt.IsZero())
}

func TestCreateScope_WorktreeWithParent_Rejected(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	_, err = rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree", ParentID: "01ABCDEF"})
	require.ErrorIs(t, err, ErrInvalidInput)
}

func TestCreateScope_ExplicitParent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	ws, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "workspace", ParentID: wt.ScopeID})
	require.NoError(t, err)
	require.Equal(t, "workspace", ws.Type)
	require.Equal(t, wt.ScopeID, ws.ParentID)

	sess, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "session", ParentID: ws.ScopeID})
	require.NoError(t, err)
	require.Equal(t, "session", sess.Type)
	require.Equal(t, ws.ScopeID, sess.ParentID)
}

func TestCreateScope_InvalidType(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	_, err = rt.CreateScope(ctx, CreateScopeRequest{Type: "invalid"})
	require.ErrorIs(t, err, ErrInvalidInput)
}

func TestCreateScope_WrongParent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	// session cannot have worktree parent.
	_, err = rt.CreateScope(ctx, CreateScopeRequest{Type: "session", ParentID: wt.ScopeID})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session parent must be workspace")
}

func TestListScopes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	scopes, err := rt.ListScopes(ctx)
	require.NoError(t, err)
	require.Empty(t, scopes)

	_, err = rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	scopes, err = rt.ListScopes(ctx)
	require.NoError(t, err)
	require.Len(t, scopes, 1)
	require.Equal(t, "worktree", scopes[0].Type)
}

func TestAddArtifactAndQuery(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	// Query before any artifacts — empty.
	results, err := rt.Query(ctx, "hello", 10)
	require.NoError(t, err)
	require.Empty(t, results)

	// Add artifact.
	artifactID, err := rt.AddArtifact(ctx, wt.ScopeID, []byte("hello world"), "test greeting")
	require.NoError(t, err)
	require.NotEmpty(t, artifactID)

	// Query after add — should find it.
	results, err = rt.Query(ctx, "hello", 10)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	require.Equal(t, artifactID, results[0].ArtifactID)
	require.Equal(t, "test greeting", results[0].Summary)
	require.Positive(t, results[0].Score)
}

func TestTrace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	_, err = rt.AddArtifact(ctx, wt.ScopeID, []byte("trace test content"), "trace test")
	require.NoError(t, err)

	tr, err := rt.Trace(ctx, "trace", 10)
	require.NoError(t, err)
	require.NotNil(t, tr)
	require.NotEmpty(t, tr.Results)
	require.NotEmpty(t, tr.Stages)

	// Verify stage names are the public (not internal) versions.
	stageNames := make(map[string]bool)
	for _, s := range tr.Stages {
		stageNames[s.Kind] = true
	}
	require.Contains(t, stageNames, "dense_search")
	require.Contains(t, stageNames, "sparse_search")
	require.Contains(t, stageNames, "rerank")
}

func TestQuery_EmptyInput(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	_, err = rt.Query(ctx, "", 10)
	require.ErrorIs(t, err, ErrInvalidInput)

	_, err = rt.Query(ctx, "test", 0)
	require.ErrorIs(t, err, ErrInvalidInput)
}

func TestArchiveRestoreRoundtrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	_, err = rt.AddArtifact(ctx, wt.ScopeID, []byte("content to archive"), "archive test")
	require.NoError(t, err)

	// Query before archive.
	results, err := rt.Query(ctx, "archive", 10)
	require.NoError(t, err)
	require.NotEmpty(t, results)

	// Archive.
	anchorID, err := rt.ArchiveScope(ctx, wt.ScopeID)
	require.NoError(t, err)
	require.NotEmpty(t, anchorID)

	// Restore.
	err = rt.RestoreScope(ctx, anchorID)
	require.NoError(t, err)
}

func TestAddArtifact_EmptyContent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	_, err = rt.AddArtifact(ctx, wt.ScopeID, []byte{}, "empty")
	require.ErrorIs(t, err, ErrInvalidInput)
}

func TestAddArtifact_NonexistentScope(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	_, err = rt.AddArtifact(ctx, "nonexistent", []byte("hello"), "test")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestArchiveScope_Nonexistent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	_, err = rt.ArchiveScope(ctx, "nonexistent")
	require.ErrorIs(t, err, ErrNotFound)
}
