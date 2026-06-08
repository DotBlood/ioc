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
		return fmt.Errorf("runtime: need a subcommand: status|stop|rotate|mint-read-token")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("runtime "+sub, flag.ExitOnError)
	// Default -dir from the resolved process config (env/file) so runtime control
	// commands target the SAME store that `ioc serve` opens; otherwise a config
	// `dir` would make `serve` listen on one dir while `runtime stop` aimed at
	// the hardcoded default and reported "no daemon".
	dir := fs.String("dir", firstNonEmpty(appConfig.Dir, defaultDataDir), "data directory")
	_ = fs.Parse(rest)

	switch sub {
	case "status":
		return runtimeStatus(*dir)
	case "stop":
		return runtimeStop(*dir)
	case "rotate":
		return runtimeRotate(*dir)
	case "mint-read-token":
		return runtimeMintReadToken(*dir)
	default:
		return fmt.Errorf("runtime: unknown subcommand %q (use status|stop|rotate|mint-read-token)", sub)
	}
}

// runtimeRotate regenerates the daemon's token(s); old tokens stop working and
// other live clients must re-connect (runtime.json is updated with the new token).
func runtimeRotate(dir string) error {
	c, err := runtime.Dial(dir)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	full, read, err := c.RotateToken()
	if err != nil {
		return err
	}
	out := map[string]any{"rotated": true, "full_token": full}
	if read != "" {
		out["read_token"] = read
	}
	return printJSON(out)
}

// runtimeMintReadToken mints a read-only token (read methods only), revoking any
// prior read token.
func runtimeMintReadToken(dir string) error {
	c, err := runtime.Dial(dir)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	read, err := c.MintReadToken()
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"read_token": read})
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
			if st.EmbedModel != "" { // live model name (runtime.json snapshot is empty for HTTP embedders at startup)
				out["embed_model"] = st.EmbedModel
			}
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
