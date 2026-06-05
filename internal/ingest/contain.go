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

// IngestRoot returns the directory ingest is confined to: IOC_INGEST_ROOT if set,
// else the process working directory. Resolved once per process by the owner that
// launched it (the daemon/MCP operator sets policy) — NOT chosen per request, so a
// model cannot widen its own sandbox. The returned path is absolute (best-effort).
func IngestRoot() string {
	if v := strings.TrimSpace(os.Getenv(IngestRootEnv)); v != "" {
		if abs, err := filepath.Abs(v); err == nil {
			return abs
		}
		return filepath.Clean(v)
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
		return "", fmt.Errorf("ingest: %w: %q escapes the ingest root %q (set %s to widen)", core.ErrInvalidInput, target, root, IngestRootEnv)
	}
	return rt, nil
}
