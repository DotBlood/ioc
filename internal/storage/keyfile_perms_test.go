package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// N5: an at-rest keyfile that is group/world-readable is refused (POSIX). A 0600 keyfile
// loads fine. Windows enforces no POSIX modes, so the check (and this test) is skipped.
func TestLoadKey_RefusesLooseKeyfilePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("k", keyLen)), 0o600))
	t.Setenv(EncryptionKeyEnv, "") // force the keyfile branch
	t.Setenv(EncryptionKeyfileEnv, path)

	k, err := LoadKey()
	require.NoError(t, err, "0600 keyfile must load")
	require.Len(t, k, keyLen)

	require.NoError(t, os.Chmod(path, 0o644)) // group/world-readable
	if _, err := LoadKey(); err == nil {
		t.Fatal("expected an error: a group/world-readable keyfile must be refused (N5)")
	}
}
