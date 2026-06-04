package main

import (
	"context"
	"flag"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/iocfmt"
)

func createScope(args []string) error {
	fs := flag.NewFlagSet("create-scope", flag.ExitOnError)
	dir, em := commonFlags(fs)
	parent := fs.String("parent", "", "parent scope ID (empty=root)")
	role := fs.String("role", "session", "worktree|workspace|session")
	title := fs.String("title", "", "scope title")
	_ = fs.Parse(args)

	parentID, err := iocfmt.ParseScopeID(*parent)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()
	s, err := e.CreateScope(context.Background(), parentID, iocfmt.ParseRole(*role), *title)
	if err != nil {
		return err
	}
	return printJSON(iocfmt.ScopeOut(s))
}

func fork(args []string) error {
	fs := flag.NewFlagSet("fork", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "source scope ID")
	title := fs.String("title", "", "new scope title")
	_ = fs.Parse(args)
	scopeID, err := iocfmt.ParseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()
	s, err := e.Fork(context.Background(), scopeID, *title)
	if err != nil {
		return err
	}
	return printJSON(iocfmt.ScopeOut(s))
}

func consolidate(args []string) error {
	fs := flag.NewFlagSet("consolidate", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "scope ID")
	summary := fs.String("summary", "", "consolidated summary")
	_ = fs.Parse(args)
	scopeID, err := iocfmt.ParseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()
	a, err := e.Consolidate(context.Background(), scopeID, *summary)
	if err != nil {
		return err
	}
	return printJSON(iocfmt.ArtifactOut(a))
}

func crossversion(args []string) error {
	fs := flag.NewFlagSet("crossversion", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "scope ID")
	constraints := fs.String("constraints", "", "carried-forward constraints (one per line)")
	lessons := fs.String("lessons", "", "carried-forward lessons (one per line)")
	_ = fs.Parse(args)
	scopeID, err := iocfmt.ParseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()
	s, err := e.CrossVersion(context.Background(), scopeID, core.Seed{Constraints: *constraints, Lessons: *lessons})
	if err != nil {
		return err
	}
	return printJSON(iocfmt.ScopeOut(s))
}

func rollupCmd(args []string) error {
	fs := flag.NewFlagSet("rollup", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "scope ID")
	summary := fs.String("summary", "", "rollup summary representing the scope")
	_ = fs.Parse(args)
	scopeID, err := iocfmt.ParseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()
	if err := e.RollupScope(context.Background(), scopeID, *summary); err != nil {
		return err
	}
	return printJSON(map[string]any{"rolled_up": scopeID.String()})
}

func siblings(args []string) error {
	fs := flag.NewFlagSet("siblings", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "scope ID")
	_ = fs.Parse(args)
	scopeID, err := iocfmt.ParseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()
	hits, err := e.SiblingOverview(context.Background(), scopeID)
	if err != nil {
		return err
	}
	return printJSON(iocfmt.HitsOut(hits))
}

func ancestors(args []string) error {
	fs := flag.NewFlagSet("ancestors", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "scope ID")
	_ = fs.Parse(args)
	scopeID, err := iocfmt.ParseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()
	scs, err := e.Ancestors(context.Background(), scopeID)
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(scs))
	for i, s := range scs {
		out[i] = iocfmt.ScopeOut(s)
	}
	return printJSON(out)
}
