// Package config centralises IOC's PROCESS-level configuration.
//
// It is distinct from the per-store config bucket in internal/storage, which
// holds runtime state (root-scope mapping, etc.) inside a bolt database. This
// package owns the human-facing knobs that control how a process starts: which
// data directory to open, which embedder to call, encryption keys, TLS paths,
// and so on.
//
// # Precedence model
//
//	flag > env > config-file > default
//
// This package resolves the inner three layers — env > file > default. The
// CALLER applies flag overrides on top after flag.Parse, because flag defaults
// interact with the way cobra / the standard flag package work and we do not
// want a dependency on flag wiring here. Concretely: Load returns a Config with
// all env and file overrides applied; the caller then writes flag-parsed values
// into the same struct (or into the flag defaults before calling Load).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Env-var names for every Config field.  Deep packages (crypto, runtime TLS,
// ingest sandbox, embedder) read some of these directly from the environment;
// applyEnvBridge forwards config-file values into the environment so those
// packages see the file setting even when the user didn't export the variable.
const (
	EnvDir               = "IOC_DIR"
	EnvEmbed             = "IOC_EMBED"
	EnvAllowRemoteEmbed  = "IOC_ALLOW_REMOTE_EMBED"
	EnvIngestRoot        = "IOC_INGEST_ROOT"
	EnvEncryptionKey     = "IOC_ENCRYPTION_KEY"
	EnvEncryptionKeyfile = "IOC_ENCRYPTION_KEYFILE"
	EnvRuntimeTLS        = "IOC_RUNTIME_TLS"
	EnvRuntimeTLSCert    = "IOC_RUNTIME_TLS_CERT"
	EnvRuntimeTLSKey     = "IOC_RUNTIME_TLS_KEY"
	EnvRuntimeTLSCA      = "IOC_RUNTIME_TLS_CA"
	EnvQueryInstruction  = "IOC_QUERY_INSTRUCTION"
	EnvLogLevel          = "IOC_LOG_LEVEL"
	EnvLogFormat         = "IOC_LOG_FORMAT"

	// EnvConfigPath names the env var that overrides the config-file search path.
	EnvConfigPath = "IOC_CONFIG"
)

// Config holds every process-level setting IOC cares about.  JSON tags drive
// the ioc.json file format.  Boolean fields are false by default (opt-in).
type Config struct {
	Dir               string `json:"dir"`                // IOC_DIR
	Embed             string `json:"embed"`              // IOC_EMBED
	AllowRemoteEmbed  bool   `json:"allow_remote_embed"` // IOC_ALLOW_REMOTE_EMBED
	IngestRoot        string `json:"ingest_root"`        // IOC_INGEST_ROOT
	EncryptionKey     string `json:"encryption_key"`     // IOC_ENCRYPTION_KEY
	EncryptionKeyfile string `json:"encryption_keyfile"` // IOC_ENCRYPTION_KEYFILE
	RuntimeTLS        bool   `json:"runtime_tls"`        // IOC_RUNTIME_TLS
	RuntimeTLSCert    string `json:"runtime_tls_cert"`   // IOC_RUNTIME_TLS_CERT
	RuntimeTLSKey     string `json:"runtime_tls_key"`    // IOC_RUNTIME_TLS_KEY
	RuntimeTLSCA      string `json:"runtime_tls_ca"`     // IOC_RUNTIME_TLS_CA
	QueryInstruction  string `json:"query_instruction"`  // IOC_QUERY_INSTRUCTION
	LogLevel          string `json:"log_level"`          // IOC_LOG_LEVEL  (debug|info|warn|error)
	LogFormat         string `json:"log_format"`         // IOC_LOG_FORMAT (text|json)
}

// Default returns a Config whose values are the built-in defaults.
// Every binary may apply its own Dir/Embed defaults on top; this layer
// only sets the human-observable log knobs to sensible values.
func Default() Config {
	return Config{
		LogLevel:  "info",
		LogFormat: "text",
	}
}

// DefaultPath returns the config-file path that Load should use when the
// caller has not explicitly supplied one.  Preference order:
//
//  1. IOC_CONFIG env var (any non-empty value is returned as-is).
//  2. "ioc.json" in the current working directory, if the file exists.
//  3. "" — meaning no file layer.
func DefaultPath() string {
	if p := os.Getenv(EnvConfigPath); p != "" {
		return p
	}
	if _, err := os.Stat("ioc.json"); err == nil {
		return "ioc.json"
	}
	return ""
}

// Load builds the resolved Config by layering: default → file → env.
//
//   - path == ""  → file layer is skipped entirely (not an error).
//   - file absent → also silently skipped (os.IsNotExist).
//   - file present but unreadable, or contains invalid JSON → error returned.
//
// After merging, applyEnvBridge writes config-file values back into the
// process environment for deep packages that read env vars directly.
func Load(path string) (Config, error) {
	cfg := Default()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				// Missing file is not an error; env/default resolution continues.
			} else {
				return Config{}, fmt.Errorf("config: load %s: %w", path, err)
			}
		} else {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return Config{}, fmt.Errorf("config: load %s: %w", path, err)
			}
		}
	}

	cfg.mergeEnv()

	if err := cfg.applyEnvBridge(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// mergeEnv overrides each Config field when the corresponding env var is
// present and non-empty.  String fields are overridden directly; bool fields
// use envBool, which only overrides when the value is recognisably true or
// false (not just present).
func (c *Config) mergeEnv() {
	if v := os.Getenv(EnvDir); v != "" {
		c.Dir = v
	}
	if v := os.Getenv(EnvEmbed); v != "" {
		c.Embed = v
	}
	if b, ok := envBool(EnvAllowRemoteEmbed); ok {
		c.AllowRemoteEmbed = b
	}
	if v := os.Getenv(EnvIngestRoot); v != "" {
		c.IngestRoot = v
	}
	if v := os.Getenv(EnvEncryptionKey); v != "" {
		c.EncryptionKey = v
	}
	if v := os.Getenv(EnvEncryptionKeyfile); v != "" {
		c.EncryptionKeyfile = v
	}
	if b, ok := envBool(EnvRuntimeTLS); ok {
		c.RuntimeTLS = b
	}
	if v := os.Getenv(EnvRuntimeTLSCert); v != "" {
		c.RuntimeTLSCert = v
	}
	if v := os.Getenv(EnvRuntimeTLSKey); v != "" {
		c.RuntimeTLSKey = v
	}
	if v := os.Getenv(EnvRuntimeTLSCA); v != "" {
		c.RuntimeTLSCA = v
	}
	if v := os.Getenv(EnvQueryInstruction); v != "" {
		c.QueryInstruction = v
	}
	if v := os.Getenv(EnvLogLevel); v != "" {
		c.LogLevel = v
	}
	if v := os.Getenv(EnvLogFormat); v != "" {
		c.LogFormat = v
	}
}

// envBool looks up name using LookupEnv.  If the var is unset or empty the
// second return value is false (caller should leave the existing field value
// untouched).  If it is set and non-empty the second return is true, and the
// first return is true when the value is one of "1", "true", or "yes"
// (case-insensitive, trimmed), false otherwise.
func envBool(name string) (value bool, present bool) {
	raw, ok := os.LookupEnv(name)
	if !ok || raw == "" {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true, true
	default:
		return false, true
	}
}

// applyEnvBridge writes config-file values into the process environment for
// settings that deep packages (crypto, runtime TLS, ingest sandbox, embedder)
// read directly via os.Getenv.  It only acts when the env var is currently
// unset/empty AND the Config field has a non-empty / true value, so an
// explicit env var always takes precedence.
func (c Config) applyEnvBridge() error {
	if err := setEnvIfUnset(EnvIngestRoot, c.IngestRoot); err != nil {
		return fmt.Errorf("config: bridge %s: %w", EnvIngestRoot, err)
	}
	if err := setEnvIfUnset(EnvEncryptionKey, c.EncryptionKey); err != nil {
		return fmt.Errorf("config: bridge %s: %w", EnvEncryptionKey, err)
	}
	if err := setEnvIfUnset(EnvEncryptionKeyfile, c.EncryptionKeyfile); err != nil {
		return fmt.Errorf("config: bridge %s: %w", EnvEncryptionKeyfile, err)
	}
	if err := setEnvIfUnset(EnvQueryInstruction, c.QueryInstruction); err != nil {
		return fmt.Errorf("config: bridge %s: %w", EnvQueryInstruction, err)
	}
	if c.AllowRemoteEmbed {
		if err := setEnvIfUnset(EnvAllowRemoteEmbed, "1"); err != nil {
			return fmt.Errorf("config: bridge %s: %w", EnvAllowRemoteEmbed, err)
		}
	}
	if c.RuntimeTLS {
		if err := setEnvIfUnset(EnvRuntimeTLS, "1"); err != nil {
			return fmt.Errorf("config: bridge %s: %w", EnvRuntimeTLS, err)
		}
	}
	if err := setEnvIfUnset(EnvRuntimeTLSCert, c.RuntimeTLSCert); err != nil {
		return fmt.Errorf("config: bridge %s: %w", EnvRuntimeTLSCert, err)
	}
	if err := setEnvIfUnset(EnvRuntimeTLSKey, c.RuntimeTLSKey); err != nil {
		return fmt.Errorf("config: bridge %s: %w", EnvRuntimeTLSKey, err)
	}
	if err := setEnvIfUnset(EnvRuntimeTLSCA, c.RuntimeTLSCA); err != nil {
		return fmt.Errorf("config: bridge %s: %w", EnvRuntimeTLSCA, err)
	}
	return nil
}

// setEnvIfUnset sets name=val only when val is non-empty AND the env var is
// not already set to a non-empty value.  This ensures the explicit env var
// always wins over a config-file value forwarded by applyEnvBridge.
func setEnvIfUnset(name, val string) error {
	if val == "" {
		return nil
	}
	if os.Getenv(name) != "" {
		return nil
	}
	return os.Setenv(name, val)
}
