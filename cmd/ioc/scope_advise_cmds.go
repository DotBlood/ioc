package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/DotBlood/ioc/internal/iocfmt"
)

// scopeAdvise runs the no-LLM scope-split advisory: it reports how many topic
// clusters the scope's artifacts form, the dispersion, and a recommendation
// (ok | split | too_small). It is read-only and changes nothing.
func scopeAdvise(args []string) error {
	fs := flag.NewFlagSet("scope-advise", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "scope ID to analyze")
	tau := fs.Float64("tau", 0, "cosine cluster threshold (0 = embedder ConfidenceFloor default)")
	minArts := fs.Int("min-artifacts", 0, "min artifacts before advising (0 = default 8)")
	_ = fs.Parse(args)
	if *scope == "" {
		return fmt.Errorf("scope-advise: -scope is required")
	}
	id, err := iocfmt.ParseScopeID(*scope)
	if err != nil {
		return fmt.Errorf("scope-advise: bad -scope: %w", err)
	}
	svc, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer func() { _ = svc.Close() }()
	st, err := svc.ScopeStats(context.Background(), id, *tau, *minArts)
	if err != nil {
		return err
	}
	return printJSON(st)
}
