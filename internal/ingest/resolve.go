package ingest

import (
	"context"
	"path/filepath"

	"github.com/DotBlood/ioc/internal/core"
)

// RootScope resolves the worktree scope to ingest under, so both the CLI and the
// MCP tool share one policy. If given is non-zero it is used as-is. Otherwise the
// abspath->root-scope mapping remembered in the meta config bucket is reused
// (when that scope still exists); failing that, a new worktree is created. The
// mapping is always (re)written so a later ingest of the same path re-syncs in
// place. title (when creating) defaults to the base name of root.
func RootScope(ctx context.Context, e Store, root string, given core.ID, title string) (core.ID, error) {
	// Containment (V1): reject an out-of-sandbox path BEFORE creating a scope or
	// writing the abspath→scope mapping, so a rejected ingest leaves no trace.
	abs, err := Contain(IngestRoot(), root)
	if err != nil {
		return core.NilID, err
	}
	key := "ingest:" + filepath.ToSlash(abs)

	rootScope := given
	if rootScope.IsZero() {
		if v, ok := e.Config(key); ok {
			if id, err := core.ParseID(v); err == nil {
				if _, err := e.GetScope(ctx, id); err == nil {
					rootScope = id // reuse remembered root that still exists
				}
			}
		}
	}
	if rootScope.IsZero() {
		if title == "" {
			title = filepath.Base(filepath.Clean(root))
		}
		s, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, title)
		if err != nil {
			return core.NilID, err
		}
		rootScope = s.ID
	}
	if err := e.SetConfig(key, rootScope.String()); err != nil {
		return core.NilID, err
	}
	return rootScope, nil
}

// JSON renders the reconcile stats as a map for tool/CLI output. Callers add
// "root_scope".
func (s Stats) JSON() map[string]any {
	return map[string]any{
		"files_added":     s.FilesAdded,
		"files_updated":   s.FilesUpdated,
		"files_unchanged": s.FilesUnchanged,
		"files_removed":   s.FilesRemoved,
		"chunks_added":    s.ChunksAdded,
		"chunks_removed":  s.ChunksRemoved,
		"scopes_created":  s.ScopesCreated,
		"scopes_removed":  s.ScopesRemoved,
	}
}
