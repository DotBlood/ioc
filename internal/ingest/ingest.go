package ingest

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"

	"github.com/DotBlood/ioc/internal/core"
)

// MaxFileBytes caps the size of a file we will ingest; larger files are skipped.
const MaxFileBytes = 512 * 1024

// skipDirs are directory names never descended into.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	"bin": true, "dist": true, "build": true,
	".venv": true, "venv": true, "__pycache__": true,
	".idea": true, ".vscode": true,
}

// Default resource caps (V9): generous enough that normal repos never hit them,
// but bounded so a pathologically large/deep tree cannot exhaust memory or create
// a runaway number of scopes. 0 in Options means "use the default" (never unlimited).
const (
	DefaultMaxFiles          = 50_000
	DefaultMaxChunks         = 500_000
	DefaultMaxDepth          = 64
	DefaultMaxIndexArtifacts = 1_000_000
)

// ErrIngestLimit is returned when an ingest run exceeds a configured resource cap.
// It wraps ErrInvalidInput. The run stops at a FILE boundary (no mid-file partial),
// commits what completed, sets Stats.LimitHit, and reports this error — so the
// truncation is explicit, never silent, and a re-run with a higher cap converges.
var ErrIngestLimit = fmt.Errorf("%w: ingest resource limit exceeded", core.ErrInvalidInput)

// Options tunes an ingest run. Zero values fall back to the Default* caps.
type Options struct {
	MaxChars int // chunk window size in chars (0 => DefaultMaxChars)
	Overlap  int // chunk overlap in chars (<0 => DefaultOverlap)

	MaxFiles          int // max text files processed per run (0 => DefaultMaxFiles)
	MaxChunks         int // max chunks pushed per run (0 => DefaultMaxChunks)
	MaxDepth          int // max directory nesting under root (0 => DefaultMaxDepth)
	MaxIndexArtifacts int // refuse to build the in-memory index above this (0 => default)
}

// Stats summarizes a reconcile run.
type Stats struct {
	FilesAdded     int `json:"files_added"`
	FilesUpdated   int `json:"files_updated"`
	FilesUnchanged int `json:"files_unchanged"`
	FilesRemoved   int `json:"files_removed"`
	ChunksAdded    int `json:"chunks_added"`
	ChunksRemoved  int `json:"chunks_removed"`
	ScopesCreated  int `json:"scopes_created"`
	ScopesRemoved  int `json:"scopes_removed"`
	// LimitHit names the resource cap that stopped the run early (V9), e.g.
	// "max_files"; empty when the run completed. Surfaced so truncation is visible.
	LimitHit string `json:"limit_hit,omitempty"`
}

// Ingest synchronizes the store under rootScope to match the file tree at root:
// new files are added, changed files re-chunked, vanished files' chunks removed,
// and emptied directory scopes pruned. It is IDEMPOTENT — re-running with no
// filesystem changes is a no-op (no new scopes, chunks, or embeddings), because
// each text file carries a content+params signature (Meta["sig"]) and directory
// scopes are reused by (parent, title) rather than recreated.
func Ingest(ctx context.Context, e Store, root string, rootScope core.ID, opt Options) (Stats, error) {
	// Containment (V1): refuse to ingest outside the configured sandbox root, so a
	// model-chosen path cannot pull arbitrary local files (secrets) into memory.
	// Root precedence: env > persisted store config > CWD (ResolveIngestRoot).
	contained, err := Contain(ResolveIngestRoot(e), root)
	if err != nil {
		return Stats{}, err
	}
	root = contained
	if opt.MaxChars <= 0 {
		opt.MaxChars = DefaultMaxChars
	}
	if opt.Overlap < 0 || opt.Overlap >= opt.MaxChars {
		opt.Overlap = DefaultOverlap
	}
	if opt.MaxFiles <= 0 {
		opt.MaxFiles = DefaultMaxFiles
	}
	if opt.MaxChunks <= 0 {
		opt.MaxChunks = DefaultMaxChunks
	}
	if opt.MaxDepth <= 0 {
		opt.MaxDepth = DefaultMaxDepth
	}
	if opt.MaxIndexArtifacts <= 0 {
		opt.MaxIndexArtifacts = DefaultMaxIndexArtifacts
	}
	r := &reconciler{
		ctx: ctx, e: e, root: filepath.Clean(root), rootScope: rootScope, opt: opt,
		dirScope:  map[string]core.ID{".": rootScope},
		dirFiles:  map[string][]string{},
		dirty:     map[string]bool{},
		wantPaths: map[string]bool{},
	}
	if err := r.buildIndex(); err != nil {
		return r.st, err
	}
	if err := r.walk(); err != nil {
		return r.st, err
	}
	if err := r.removeVanished(); err != nil {
		return r.st, err
	}
	if err := r.pruneEmpty(); err != nil {
		return r.st, err
	}
	if err := r.rollups(); err != nil {
		return r.st, err
	}
	return r.st, nil
}

// isBinary reports whether data looks non-text: a NUL byte in the first 8KB.
func isBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}
