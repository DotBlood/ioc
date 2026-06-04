package main

import (
	"context"
	"flag"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/iocfmt"
)

func push(args []string) error {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "scope ID")
	kind := fs.String("kind", "insight", "answer|insight|summary|document|reasoning|seed")
	summary := fs.String("summary", "", "mini-summary (required)")
	content := fs.String("content", "", "inline full content ('-' = stdin)")
	contentFile := fs.String("content-file", "", "file with full content")
	pub := fs.Bool("publish", false, "publish to siblings")
	_ = fs.Parse(args)

	scopeID, err := iocfmt.ParseScopeID(*scope)
	if err != nil {
		return err
	}
	body, err := readContent(*content, *contentFile)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()

	a, err := e.Push(context.Background(), core.PushRequest{
		Scope:   scopeID,
		Kind:    iocfmt.ParseKind(*kind),
		Summary: *summary,
		Content: body,
		Publish: *pub,
	})
	if err != nil {
		return err
	}
	return printJSON(iocfmt.ArtifactOut(a))
}

func drill(args []string) error {
	fs := flag.NewFlagSet("drill", flag.ExitOnError)
	dir, em := commonFlags(fs)
	artifact := fs.String("artifact", "", "artifact ID")
	detail := fs.String("detail", "raw", "entry|raw")
	_ = fs.Parse(args)

	id, err := core.ParseID(*artifact)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()

	h, err := e.Drill(context.Background(), id, iocfmt.ParseDetail(*detail))
	if err != nil {
		return err
	}
	return printJSON(iocfmt.HitOut(h))
}

func publish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	dir, em := commonFlags(fs)
	artifact := fs.String("artifact", "", "artifact ID")
	_ = fs.Parse(args)
	id, err := core.ParseID(*artifact)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()
	if err := e.Publish(context.Background(), id); err != nil {
		return err
	}
	return printJSON(map[string]any{"published": id.String()})
}
