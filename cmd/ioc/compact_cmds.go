package main

import (
	"context"
	"flag"
)

// compactCmd reclaims orphaned embeddings (those whose artifact or scope was
// deleted or re-chunked). It writes a fresh embedding file holding only live
// vectors and atomically switches the store to it. When a daemon owns -dir,
// the compact runs under the daemon's write lock (via openService routing);
// otherwise it opens the store directly. -embed must match the embedder the
// store was built with (dims/model guard).
func compactCmd(args []string) error {
	fs := flag.NewFlagSet("compact", flag.ExitOnError)
	dir, em := commonFlags(fs)
	_ = fs.Parse(args)
	svc, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer func() { _ = svc.Close() }()
	st, err := svc.Compact(context.Background())
	if err != nil {
		return err
	}
	return printJSON(st)
}
