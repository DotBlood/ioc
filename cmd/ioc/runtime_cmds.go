package main

import (
	"flag"
	"fmt"
	"os"

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
	// runtime.json exists; confirm the daemon is actually reachable.
	alive := false
	if c, derr := runtime.Dial(dir); derr == nil {
		alive = true
		_ = c.Close()
	}
	return printJSON(map[string]any{
		"running":     alive,
		"stale":       !alive, // file present but no daemon answering
		"pid":         info.PID,
		"addr":        info.Addr,
		"embed_model": info.EmbedModel,
		"started_at":  info.StartedAt,
		"data_dir":    info.DataDir,
	})
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
