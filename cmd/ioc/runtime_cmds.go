package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/DotBlood/ioc/internal/runtime"
)

// runtimeCmd manages the runtime daemon: `ioc runtime status` and
// `ioc runtime stop`. The daemon itself is started with `ioc serve`.
func runtimeCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("runtime: need a subcommand: status|stop")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("runtime "+sub, flag.ExitOnError)
	dir := fs.String("dir", defaultDataDir, "data directory")
	_ = fs.Parse(rest)

	switch sub {
	case "status":
		return runtimeStatus(*dir)
	case "stop":
		return runtimeStop(*dir)
	default:
		return fmt.Errorf("runtime: unknown subcommand %q (use status|stop)", sub)
	}
}

func runtimeStatus(dir string) error {
	info, err := runtime.Info(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return printJSON(map[string]any{"running": false, "dir": dir})
		}
		return err
	}
	// runtime.json exists; confirm the daemon is actually reachable and pull
	// live stats (connections, request count).
	out := map[string]any{
		"running":     false,
		"stale":       true, // file present but no daemon answering
		"pid":         info.PID,
		"addr":        info.Addr,
		"embed_model": info.EmbedModel,
		"started_at":  info.StartedAt,
		"data_dir":    info.DataDir,
	}
	if c, derr := runtime.Dial(dir); derr == nil {
		out["running"], out["stale"] = true, false
		if st, serr := c.Stats(); serr == nil {
			out["conns"] = st.Conns
			out["requests"] = st.Requests
		}
		_ = c.Close()
		if t, perr := time.Parse(time.RFC3339, info.StartedAt); perr == nil {
			out["uptime_sec"] = int(time.Since(t).Seconds())
		}
	}
	return printJSON(out)
}

func runtimeStop(dir string) error {
	if err := runtime.Shutdown(dir); err != nil {
		if os.IsNotExist(err) {
			return printJSON(map[string]any{"stopped": false, "reason": "no daemon", "dir": dir})
		}
		return err
	}
	return printJSON(map[string]any{"stopped": true, "dir": dir})
}
