package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/DotBlood/ioc/internal/runtime"
)

// serveCmd runs the runtime daemon: it becomes the single owner of -dir and
// serves clients (CLI, MCP, sub-agents) over the framed-JSON protocol described
// in <dir>/runtime.json. Blocks until SIGINT/SIGTERM, then shuts down cleanly.
func serveCmd(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dir, em := commonFlags(fs)
	_ = fs.Parse(args)

	// rerank=true so Query rerank works through the daemon when a real embedder
	// endpoint is configured (no-op for the mock).
	e, err := openEngineRerank(*dir, *em, true)
	if err != nil {
		return err
	}
	srv := runtime.NewServer(e, *dir)

	go func() {
		<-srv.Ready()
		fmt.Fprintf(os.Stderr, "ioc runtime: listening on %s (dir=%s, embed=%q)\n", srv.Addr(), *dir, e.EmbModel())
	}()

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigc
		fmt.Fprintln(os.Stderr, "ioc runtime: shutting down…")
		_ = srv.Stop()
	}()

	return srv.Serve()
}
