package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeJSON creates an ioc.json file in dir containing the given map, and
// returns the full path.
func writeJSON(t *testing.T, dir string, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	p := filepath.Join(dir, "ioc.json")
	require.NoError(t, os.WriteFile(p, data, 0o600))
	return p
}

// TestDefault verifies the built-in defaults.
func TestDefault(t *testing.T) {
	cfg := Default()
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, "text", cfg.LogFormat)
	require.Empty(t, cfg.Dir)
	require.Empty(t, cfg.Embed)
	require.False(t, cfg.AllowRemoteEmbed)
	require.False(t, cfg.RuntimeTLS)
}

// TestLoad_NoPath_NoEnv: Load("") with no relevant env vars returns Default().
func TestLoad_NoPath_NoEnv(t *testing.T) {
	// Unset all relevant env vars so previous test runs don't bleed in.
	for _, e := range []string{
		EnvDir, EnvEmbed, EnvAllowRemoteEmbed, EnvIngestRoot,
		EnvEncryptionKey, EnvEncryptionKeyfile, EnvRuntimeTLS,
		EnvRuntimeTLSCert, EnvRuntimeTLSKey, EnvRuntimeTLSCA,
		EnvQueryInstruction, EnvLogLevel, EnvLogFormat,
	} {
		t.Setenv(e, "")
	}

	cfg, err := Load("")
	require.NoError(t, err)
	require.Equal(t, Default(), cfg)
}

// TestLoad_FileOnly: a config file with several fields set is reflected.
func TestLoad_FileOnly(t *testing.T) {
	clearBridgeEnv(t)

	dir := t.TempDir()
	path := writeJSON(t, dir, map[string]any{
		"dir":         "/tmp/data",
		"log_level":   "debug",
		"runtime_tls": true,
	})

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "/tmp/data", cfg.Dir)
	require.Equal(t, "debug", cfg.LogLevel)
	require.True(t, cfg.RuntimeTLS)
	// Defaults for unlisted fields still apply.
	require.Equal(t, "text", cfg.LogFormat)
}

// TestLoad_EnvOverridesFile: env var wins over the config file value.
func TestLoad_EnvOverridesFile(t *testing.T) {
	clearBridgeEnv(t)
	t.Setenv(EnvDir, "/from/env")

	dir := t.TempDir()
	path := writeJSON(t, dir, map[string]any{
		"dir": "/from/file",
	})

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "/from/env", cfg.Dir)
}

// TestEnvBool_Parsing exercises all recognised truth/false values.
func TestEnvBool_Parsing(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"1", true},
		{"true", true},
		{"yes", true},
		{"TRUE", true},
		{"True", true},
		{"YES", true},
		{"0", false},
		{"no", false},
		{"false", false},
		{"off", false},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv(EnvAllowRemoteEmbed, tc.raw)
			got, present := envBool(EnvAllowRemoteEmbed)
			require.True(t, present, "var should be present")
			require.Equal(t, tc.want, got)
		})
	}
}

// TestEnvBool_AllowRemoteEmbed_ViaLoad verifies the full pipeline: Load picks
// up IOC_ALLOW_REMOTE_EMBED from the environment.
func TestEnvBool_AllowRemoteEmbed_ViaLoad(t *testing.T) {
	clearBridgeEnv(t)

	for _, raw := range []string{"1", "true", "yes", "TRUE"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv(EnvAllowRemoteEmbed, raw)
			cfg, err := Load("")
			require.NoError(t, err)
			require.True(t, cfg.AllowRemoteEmbed)
		})
	}
	for _, raw := range []string{"0", "no", "false"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv(EnvAllowRemoteEmbed, raw)
			cfg, err := Load("")
			require.NoError(t, err)
			require.False(t, cfg.AllowRemoteEmbed)
		})
	}
}

// TestApplyEnvBridge_SetsEnvWhenUnset: after Load with a file that has
// encryption_keyfile set and IOC_ENCRYPTION_KEYFILE unset, the env var must
// be populated with the file value.
func TestApplyEnvBridge_SetsEnvWhenUnset(t *testing.T) {
	clearBridgeEnv(t)
	// Ensure the target var starts unset.
	t.Setenv(EnvEncryptionKeyfile, "")

	dir := t.TempDir()
	path := writeJSON(t, dir, map[string]any{
		"encryption_keyfile": "/keys/ioc.key",
	})

	_, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "/keys/ioc.key", os.Getenv(EnvEncryptionKeyfile))
}

// TestApplyEnvBridge_DoesNotOverwriteExistingEnv: when the env var is already
// set, the bridge must not overwrite it with the file value.
func TestApplyEnvBridge_DoesNotOverwriteExistingEnv(t *testing.T) {
	clearBridgeEnv(t)
	t.Setenv(EnvEncryptionKeyfile, "/existing/key")

	dir := t.TempDir()
	path := writeJSON(t, dir, map[string]any{
		"encryption_keyfile": "/file/key",
	})

	_, err := Load(path)
	require.NoError(t, err)
	// The env var must still hold the original value.
	require.Equal(t, "/existing/key", os.Getenv(EnvEncryptionKeyfile))
}

// TestLoad_NonExistentPath: a path that does not exist is not an error.
func TestLoad_NonExistentPath(t *testing.T) {
	clearBridgeEnv(t)

	cfg, err := Load(filepath.Join(t.TempDir(), "does_not_exist.json"))
	require.NoError(t, err)
	require.Equal(t, Default(), cfg)
}

// TestLoad_MalformedJSON: a file with invalid JSON returns an error.
func TestLoad_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ioc.json")
	require.NoError(t, os.WriteFile(p, []byte(`{bad json`), 0o600))

	_, err := Load(p)
	require.Error(t, err)
	require.Contains(t, err.Error(), "config: load")
}

// TestDefaultPath covers the three resolution cases.
func TestDefaultPath(t *testing.T) {
	t.Run("IOC_CONFIG set", func(t *testing.T) {
		t.Setenv(EnvConfigPath, "/custom/ioc.json")
		require.Equal(t, "/custom/ioc.json", DefaultPath())
	})

	t.Run("ioc.json in CWD", func(t *testing.T) {
		tmp := t.TempDir()
		// Write the file so os.Stat succeeds.
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "ioc.json"), []byte("{}"), 0o600))
		t.Setenv(EnvConfigPath, "") // ensure IOC_CONFIG is clear
		t.Chdir(tmp)
		require.Equal(t, "ioc.json", DefaultPath())
	})

	t.Run("nothing present", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv(EnvConfigPath, "")
		t.Chdir(tmp)
		require.Equal(t, "", DefaultPath())
	})
}

// clearBridgeEnv unsets all env vars that applyEnvBridge might set, so tests
// that check bridge behaviour start from a clean slate.
func clearBridgeEnv(t *testing.T) {
	t.Helper()
	for _, e := range []string{
		EnvDir, EnvEmbed, EnvAllowRemoteEmbed, EnvIngestRoot,
		EnvEncryptionKey, EnvEncryptionKeyfile, EnvRuntimeTLS,
		EnvRuntimeTLSCert, EnvRuntimeTLSKey, EnvRuntimeTLSCA,
		EnvQueryInstruction, EnvLogLevel, EnvLogFormat,
	} {
		t.Setenv(e, "")
	}
}
