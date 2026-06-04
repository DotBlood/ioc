package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/ingest"
	"github.com/DotBlood/ioc/internal/iocfmt"
)

// ingestCmd synchronizes a directory tree into nested scopes, writing each text
// file as KindDocument chunks (mechanical, no LLM). Idempotent: re-running
// reuses the remembered root scope for this path and reconciles in place. Path
// is positional.
func ingestCmd(args []string) error {
	pos, rest := splitPositional(args)
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "root scope ID to ingest under (empty=create a worktree)")
	title := fs.String("title", "", "title for the created root scope (default: base of path)")
	maxchars := fs.Int("maxchars", ingest.DefaultMaxChars, "chunk window size in chars")
	overlap := fs.Int("overlap", ingest.DefaultOverlap, "chunk overlap in chars")
	_ = fs.Parse(rest)

	if pos == "" {
		return fmt.Errorf("ingest: missing <path>")
	}
	root := filepath.Clean(pos)

	e, err := openEngine(*dir, *em)
	if err != nil {
		return err
	}
	defer e.Close()
	ctx := context.Background()

	rootScope, err := iocfmt.ParseScopeID(*scope)
	if err != nil {
		return err
	}

	// Remember which root scope a given absolute path was ingested under, so a
	// later `ioc ingest <path>` (no -scope) reuses it and re-syncs in place.
	abs, _ := filepath.Abs(root)
	pathKey := "ingest:" + filepath.ToSlash(abs)

	if rootScope.IsZero() {
		if v, ok := e.Config(pathKey); ok {
			if id, perr := core.ParseID(v); perr == nil {
				if _, gerr := e.GetScope(ctx, id); gerr == nil {
					rootScope = id // reuse remembered root that still exists
				}
			}
		}
	}
	if rootScope.IsZero() {
		t := *title
		if t == "" {
			t = filepath.Base(root)
		}
		s, cErr := e.CreateScope(ctx, core.NilID, core.RoleWorktree, t)
		if cErr != nil {
			return cErr
		}
		rootScope = s.ID
	}
	if err := e.SetConfig(pathKey, rootScope.String()); err != nil {
		return err
	}

	st, err := ingest.Ingest(ctx, e, root, rootScope, ingest.Options{MaxChars: *maxchars, Overlap: *overlap})
	if err != nil {
		return err
	}
	return printJSON(map[string]any{
		"root_scope":      rootScope.String(),
		"files_added":     st.FilesAdded,
		"files_updated":   st.FilesUpdated,
		"files_unchanged": st.FilesUnchanged,
		"files_removed":   st.FilesRemoved,
		"chunks_added":    st.ChunksAdded,
		"chunks_removed":  st.ChunksRemoved,
		"scopes_created":  st.ScopesCreated,
		"scopes_removed":  st.ScopesRemoved,
	})
}
