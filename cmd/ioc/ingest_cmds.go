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

// ingestCmd mirrors a directory tree into nested scopes and writes each text
// file as KindDocument chunks (mechanical, no LLM). Path is positional.
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

	st, err := ingest.Ingest(ctx, e, root, rootScope, ingest.Options{MaxChars: *maxchars, Overlap: *overlap})
	if err != nil {
		return err
	}
	return printJSON(map[string]any{
		"root_scope": rootScope.String(),
		"files":      st.Files,
		"chunks":     st.Chunks,
		"skipped":    st.Skipped,
		"scopes":     st.Scopes,
	})
}
