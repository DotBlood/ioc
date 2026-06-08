package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
)

// openSchema opens an engine for schema tests using the mock embedder. Callers
// are responsible for calling Close.
func openSchema(t *testing.T, dir string) *Engine {
	t.Helper()
	e, err := Open(context.Background(), dir, embed.NewMockEmbedder(16))
	require.NoError(t, err)
	return e
}

// TestSchema_NewStoreStamped verifies that opening a fresh directory results in
// schema_version == "1" being persisted so a subsequent reopen recognises it.
func TestSchema_NewStoreStamped(t *testing.T) {
	dir := t.TempDir()

	// First open: brand-new store should be stamped.
	e := openSchema(t, dir)
	v, ok := e.meta.GetConfig(cfgSchemaVersion)
	require.True(t, ok, "schema_version must be set after first open")
	require.Equal(t, "1", v)
	require.NoError(t, e.Close())

	// Second open: reopen must succeed and leave the version unchanged.
	e2 := openSchema(t, dir)
	v2, ok2 := e2.meta.GetConfig(cfgSchemaVersion)
	require.True(t, ok2)
	require.Equal(t, "1", v2)
	require.NoError(t, e2.Close())
}

// TestSchema_ReopenWithDataStable verifies that a store that already has a scope
// and artifact reopens cleanly and retains schema_version == "1".
func TestSchema_ReopenWithDataStable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Write some data.
	e := openSchema(t, dir)
	wt, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	require.NoError(t, err)
	_, err = e.Push(ctx, core.PushRequest{Scope: wt.ID, Summary: "schema stability check"})
	require.NoError(t, err)
	require.NoError(t, e.Close())

	// Reopen: must succeed with version still 1.
	e2 := openSchema(t, dir)
	v, ok := e2.meta.GetConfig(cfgSchemaVersion)
	require.True(t, ok)
	require.Equal(t, "1", v)
	// Data survived.
	scopes, err := e2.ListScopes(ctx)
	require.NoError(t, err)
	require.Len(t, scopes, 1)
	require.Equal(t, "root", scopes[0].Title)
	require.NoError(t, e2.Close())
}

// TestSchema_NewerStoreRefused verifies that a store whose schema_version is
// greater than CurrentSchemaVersion is refused at Open with ErrInvalidInput.
func TestSchema_NewerStoreRefused(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Create a valid store, then forge a future version.
	e := openSchema(t, dir)
	require.NoError(t, e.meta.PutConfig(cfgSchemaVersion, "2"))
	require.NoError(t, e.Close())

	_, err := Open(ctx, dir, embed.NewMockEmbedder(16))
	require.Error(t, err, "store with newer schema version must be refused")
	require.True(t, errors.Is(err, core.ErrInvalidInput), "error must wrap ErrInvalidInput, got: %v", err)
	require.Contains(t, err.Error(), "newer than this build")
}

// TestSchema_LegacyStoreStampedWithoutWipe simulates a pre-versioning store (no
// schema_version key, non-empty data) and verifies that:
//  1. Open succeeds and stamps version "1".
//  2. All pre-existing data is intact (no wipe).
//
// Because storage.Meta does not expose a general DeleteConfig, this test drives
// checkSchemaVersion directly on a manually-constructed engine whose store has
// had the schema_version key removed via DeleteConfig.
func TestSchema_LegacyStoreStampedWithoutWipe(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Write a scope and an artifact to make the store non-empty.
	e := openSchema(t, dir)
	wt, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "legacy-root")
	require.NoError(t, err)
	_, err = e.Push(ctx, core.PushRequest{Scope: wt.ID, Summary: "pre-versioning artifact"})
	require.NoError(t, err)

	// Remove schema_version to simulate a pre-versioning store.
	require.NoError(t, e.meta.DeleteConfig(cfgSchemaVersion))

	// Confirm it is gone.
	_, ok := e.meta.GetConfig(cfgSchemaVersion)
	require.False(t, ok, "schema_version must be absent to simulate legacy store")

	// Confirm the store is non-empty (IsEmpty must return false).
	require.False(t, e.meta.IsEmpty(), "store with a scope+artifact must not be empty")

	// Drive checkSchemaVersion directly (same-package access).
	err = e.checkSchemaVersion()
	require.NoError(t, err, "legacy store must be stamped without error")

	// Version is now stamped.
	v, ok := e.meta.GetConfig(cfgSchemaVersion)
	require.True(t, ok)
	require.Equal(t, "1", v)

	// Pre-existing data survived — no wipe.
	scopes, err := e.ListScopes(ctx)
	require.NoError(t, err)
	require.Len(t, scopes, 1)
	require.Equal(t, "legacy-root", scopes[0].Title)

	require.NoError(t, e.Close())
}

// TestSchema_CorruptVersion verifies that a non-integer schema_version causes
// Open to return an error wrapping ErrInvalidInput and containing the word
// "corrupt".
func TestSchema_CorruptVersion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	e := openSchema(t, dir)
	require.NoError(t, e.meta.PutConfig(cfgSchemaVersion, "abc"))
	require.NoError(t, e.Close())

	_, err := Open(ctx, dir, embed.NewMockEmbedder(16))
	require.Error(t, err)
	require.True(t, errors.Is(err, core.ErrInvalidInput), "corrupt version must wrap ErrInvalidInput, got: %v", err)
	require.Contains(t, err.Error(), "corrupt schema_version")
}

// TestSchema_MigrationExecutes verifies the migration registry mechanism by
// temporarily installing a test migration (to: 2) and asserting that:
//  1. The migration function is called.
//  2. schema_version is stamped to "2" after migrate returns.
//
// CurrentSchemaVersion is NOT changed — this tests the plumbing only.
func TestSchema_MigrationExecutes(t *testing.T) {
	dir := t.TempDir()

	e := openSchema(t, dir)
	defer e.Close()

	// Install a temporary migration that records execution via a config marker.
	const markerKey = "test_migration_ran"
	saved := migrations
	defer func() { migrations = saved }()
	migrations = []migration{
		{
			to: 2,
			run: func(eng *Engine) error {
				return eng.meta.PutConfig(markerKey, "yes")
			},
		},
	}

	// Call migrate(1→2) directly; version 1 is the baseline after openSchema.
	err := e.migrate(1, 2)
	require.NoError(t, err)

	// Migration function fired.
	marker, ok := e.meta.GetConfig(markerKey)
	require.True(t, ok, "migration must have written the marker key")
	require.Equal(t, "yes", marker)

	// schema_version was bumped to "2" by migrate.
	v, ok := e.meta.GetConfig(cfgSchemaVersion)
	require.True(t, ok)
	require.Equal(t, "2", v)
}
