package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"

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

	given, err := iocfmt.ParseScopeID(*scope)
	if err != nil {
		return err
	}
	rootScope, err := ingest.RootScope(ctx, e, root, given, *title)
	if err != nil {
		return err
	}

	st, err := ingest.Ingest(ctx, e, root, rootScope, ingest.Options{MaxChars: *maxchars, Overlap: *overlap})
	if err != nil {
		return err
	}
	out := st.JSON()
	out["root_scope"] = rootScope.String()
	return printJSON(out)
}
