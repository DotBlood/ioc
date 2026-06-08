package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/DotBlood/ioc/internal/config"
	"github.com/DotBlood/ioc/internal/logging"
	"github.com/DotBlood/ioc/internal/runtime"
)

// serveCmd runs the runtime daemon: it becomes the single owner of -dir and
// serves clients (CLI, MCP, sub-agents) over the framed-JSON protocol described
// in <dir>/runtime.json. Blocks until SIGINT/SIGTERM, then shuts down cleanly.
func serveCmd(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dir, em := commonFlags(fs)
	_ = fs.Parse(args)

	// If a daemon already owns this dir, say so clearly instead of leaking the
	// raw bbolt lock error.
	if _, derr := runtime.Dial(*dir); derr == nil {
		return fmt.Errorf("a runtime daemon already owns %s — stop it with `ioc runtime stop -dir %s`", *dir, *dir)
	}

	// rerank=true so Query rerank works through the daemon when a real embedder
	// endpoint is configured (no-op for the mock).
	e, err := openEngineEmbedded(*dir, *em, true)
	if err != nil {
		return fmt.Errorf("%w (if a daemon is running here, run `ioc runtime stop -dir %s`)", err, *dir)
	}
	var srv *runtime.Server
	if runtime.TLSEnabled() {
		cfg, terr := runtime.ServerTLSConfig()
		if terr != nil {
			return terr
		}
		srv = runtime.NewServerTLS(e, *dir, cfg)
		slog.Info("runtime mTLS enabled")
	} else {
		srv = runtime.NewServer(e, *dir)
	}

	go func() {
		<-srv.Ready()
		slog.Info("runtime listening", "addr", srv.Addr(), "dir", *dir, "embed_model", e.EmbModel())
	}()

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigc
		slog.Info("runtime shutting down")
		_ = srv.Stop()
	}()

	// SIGHUP (where the platform has it — see reload_*.go) re-reads ioc.json and
	// re-applies the log level/format WITHOUT reopening the store, so an operator
	// can change verbosity on a long-lived daemon. The store/embedder are NOT
	// reloaded (a zero-downtime restart is out of scope; stop+start for those).
	if reloadSigs := reloadSignals(); len(reloadSigs) > 0 {
		hupc := make(chan os.Signal, 1)
		signal.Notify(hupc, reloadSigs...)
		// Process-lifetime goroutine: it is intentionally not torn down — the daemon
		// exits right after srv.Serve() returns, taking this with it.
		go func() {
			for range hupc {
				cfg, rerr := config.Load(config.DefaultPath())
				if rerr != nil {
					slog.Warn("reload: config load failed", "err", rerr)
					continue
				}
				logging.Init(cfg.LogLevel, cfg.LogFormat)
				slog.Info("reloaded config", "log_level", cfg.LogLevel, "log_format", cfg.LogFormat)
			}
		}()
	}

	return srv.Serve()
}
