package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
)

// Contain confines a model-chosen ingest path to the sandbox root, rejecting
// traversal and any target that resolves outside it.
func TestContain(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	sibling := filepath.Join(parent, "sibling")
	sub := filepath.Join(root, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.MkdirAll(sibling, 0o755))

	// Allowed: root itself, and a subdirectory.
	for _, ok := range []string{root, sub, filepath.Join(root, ".", "sub")} {
		got, err := Contain(root, ok)
		require.NoErrorf(t, err, "expected %q allowed", ok)
		require.NotEmpty(t, got)
	}

	// Rejected: escape via "..", an absolute path outside, and a non-existent target.
	for _, bad := range []string{
		filepath.Join(root, "..", "sibling"),
		sibling,
		filepath.Join(root, "does-not-exist"),
		parent, // the root's own parent is outside the sandbox
	} {
		_, err := Contain(root, bad)
		require.Errorf(t, err, "expected %q rejected", bad)
		require.ErrorIs(t, err, core.ErrInvalidInput)
	}
}

// A symlink inside the root that points outside it must be rejected (judged by
// its resolved location). Symlink creation may require privilege on Windows; skip
// if unavailable.
func TestContain_SymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))

	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported here: %v", err)
	}
	_, err := Contain(root, link)
	require.Error(t, err)
	require.ErrorIs(t, err, core.ErrInvalidInput)
}

// Case-only differences on a case-insensitive FS must NOT be mistaken for an
// escape (no false reject).
func TestContain_CaseInsensitive(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-fold containment only applies on case-insensitive FS")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "Root")
	sub := filepath.Join(root, "Sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	_, err := Contain(filepath.Join(parent, "root"), filepath.Join(parent, "ROOT", "sub"))
	require.NoError(t, err)
}

func TestIngestRoot_EnvAndDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(IngestRootEnv, dir)
	abs, _ := filepath.Abs(dir)
	require.Equal(t, abs, IngestRoot())

	t.Setenv(IngestRootEnv, "")
	cwd, _ := os.Getwd()
	require.Equal(t, cwd, IngestRoot())
}

// fakeConfig is a minimal configReader for ResolveIngestRoot.
type fakeConfig map[string]string

func (f fakeConfig) Config(k string) (string, bool) { v, ok := f[k]; return v, ok }

// ResolveIngestRoot precedence: env > config "ingest_root" > CWD.
func TestResolveIngestRoot(t *testing.T) {
	envDir := t.TempDir()
	cfgDir := t.TempDir()
	cwd, _ := os.Getwd()

	t.Run("env-beats-config", func(t *testing.T) {
		t.Setenv(IngestRootEnv, envDir)
		require.Equal(t, absRoot(envDir), ResolveIngestRoot(fakeConfig{"ingest_root": cfgDir}))
	})
	t.Run("config-when-no-env", func(t *testing.T) {
		t.Setenv(IngestRootEnv, "")
		require.Equal(t, absRoot(cfgDir), ResolveIngestRoot(fakeConfig{"ingest_root": cfgDir}))
	})
	t.Run("cwd-when-neither", func(t *testing.T) {
		t.Setenv(IngestRootEnv, "")
		require.Equal(t, cwd, ResolveIngestRoot(fakeConfig{}))
	})
	t.Run("empty-config-falls-through", func(t *testing.T) {
		t.Setenv(IngestRootEnv, "")
		require.Equal(t, cwd, ResolveIngestRoot(fakeConfig{"ingest_root": "  "}))
	})
	t.Run("nil-store", func(t *testing.T) {
		t.Setenv(IngestRootEnv, "")
		require.Equal(t, cwd, ResolveIngestRoot(nil))
	})
}

// Ingest honors a persisted config root (no env): a file inside it ingests, a
// sibling outside it is rejected.
func TestIngest_RespectsConfigRoot(t *testing.T) {
	t.Setenv(IngestRootEnv, "") // config path, not env
	e := openTestEngine(t)
	rootDir := t.TempDir()
	require.NoError(t, e.SetConfig(ingestRootConfigKey, rootDir))
	write(t, filepath.Join(rootDir, "a.txt"), "alpha\n")

	scope := newRoot(t, e)
	_, err := Ingest(context.Background(), e, rootDir, scope, Options{})
	require.NoError(t, err, "ingest inside the config root should succeed")

	outside := t.TempDir()
	write(t, filepath.Join(outside, "secret.txt"), "x\n")
	_, err = Ingest(context.Background(), e, outside, scope, Options{})
	require.Error(t, err, "ingest outside the config root should be rejected")
	require.ErrorIs(t, err, core.ErrInvalidInput)
}

// End-to-end: Ingest of an out-of-root tree errors and writes nothing.
func TestIngest_RejectsOutOfRoot(t *testing.T) {
	e := openTestEngine(t)
	rootDir := t.TempDir() // the allowed sandbox
	t.Setenv(IngestRootEnv, rootDir)
	outside := t.TempDir() // a different tree, outside the sandbox
	write(t, filepath.Join(outside, "secret.txt"), "ssh private key\n")

	scope := newRoot(t, e)
	_, err := Ingest(context.Background(), e, outside, scope, Options{})
	require.Error(t, err)
	require.True(t, errors.Is(err, core.ErrInvalidInput))
	require.Empty(t, docArtifacts(t, e), "out-of-root ingest must write no document chunks")
}
