package ingest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/DotBlood/ioc/internal/core"
)

// IngestRootEnv names the environment variable that pins the ingest sandbox root.
const IngestRootEnv = "IOC_INGEST_ROOT"

// ingestRootConfigKey is the meta-config key holding a persisted sandbox root
// (set via `ioc config set-ingest-root`). The env var overrides it.
const ingestRootConfigKey = "ingest_root"

// absRoot makes a root path absolute (best-effort), falling back to Clean.
func absRoot(v string) string {
	if abs, err := filepath.Abs(v); err == nil {
		return abs
	}
	return filepath.Clean(v)
}

// IngestRoot returns the directory ingest is confined to from env (IOC_INGEST_ROOT)
// or the process working directory — for callers without a store. Prefer
// ResolveIngestRoot when a store is available (it also honors the persisted config).
func IngestRoot() string {
	if v := strings.TrimSpace(os.Getenv(IngestRootEnv)); v != "" {
		return absRoot(v)
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// configReader is the slice of the store ResolveIngestRoot needs.
type configReader interface {
	Config(key string) (string, bool)
}

// ResolveIngestRoot returns the ingest sandbox root with precedence:
// env IOC_INGEST_ROOT > store config "ingest_root" > process CWD. Resolved by the
// owner that launched the process (operator sets policy), NOT chosen per request,
// so a model cannot widen its own sandbox. The result is absolute (best-effort).
func ResolveIngestRoot(e configReader) string {
	if v := strings.TrimSpace(os.Getenv(IngestRootEnv)); v != "" {
		return absRoot(v)
	}
	if e != nil {
		if v, ok := e.Config(ingestRootConfigKey); ok {
			if v = strings.TrimSpace(v); v != "" {
				return absRoot(v)
			}
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// Contain verifies that target is inside root and returns target's cleaned,
// symlink-resolved absolute path. It rejects path-traversal ("..") and any target
// that resolves outside root (a different volume, an absolute escape, or a symlink
// pointing out). This is the V1 fix: ioc_ingest takes a model-chosen path, so an
// unconfined ingest could pull ~/.ssh or .env and read it back via query/drill.
func Contain(root, target string) (string, error) {
	at, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("ingest: %w: bad path %q: %v", core.ErrInvalidInput, target, err)
	}
	// Resolve symlinks so a symlinked target (or root) pointing outside the sandbox
	// is judged by its real location. The target must exist to be ingested; a
	// resolve error (missing/dangling) is a hard reject.
	rt, err := filepath.EvalSymlinks(at)
	if err != nil {
		return "", fmt.Errorf("ingest: %w: cannot resolve %q: %v", core.ErrInvalidInput, target, err)
	}
	ar, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("ingest: %w: bad root %q: %v", core.ErrInvalidInput, root, err)
	}
	rr, err := filepath.EvalSymlinks(ar)
	if err != nil {
		rr = filepath.Clean(ar) // root may legitimately not be a symlink-resolvable path; use cleaned abs
	}

	a, b := rr, rt
	if runtime.GOOS == "windows" {
		// Windows filesystems are case-insensitive; fold case so a case-different
		// but identical path is NOT mistaken for an escape (avoids a false reject).
		a, b = strings.ToLower(rr), strings.ToLower(rt)
	}
	rel, err := filepath.Rel(a, b)
	if err != nil {
		return "", fmt.Errorf("ingest: %w: %q is outside the ingest root %q", core.ErrInvalidInput, target, root)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("ingest: %w: %q escapes the ingest root %q (set %s or run `ioc config set-ingest-root` to widen)", core.ErrInvalidInput, target, root, IngestRootEnv)
	}
	return rt, nil
}
