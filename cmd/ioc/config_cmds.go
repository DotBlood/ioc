package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DotBlood/ioc/internal/ingest"
)

// configSetAllowed gates which config keys `config set` may write. Only operator-tunable
// values are allowed; SAFETY-CRITICAL keys are intentionally NOT writable here — `enc`
// (the at-rest encryption sentinel) and `emb_model`/`emb_dims` (which gate the
// embedder-mismatch guard that prevents querying across incompatible vector spaces). Use
// `config set-ingest-root` for ingest_root (it validates the directory).
func configSetAllowed(key string) bool {
	if key == "ingest_root" {
		return true
	}
	return strings.HasPrefix(key, "conf.floor.") ||
		strings.HasPrefix(key, "conf.margin.") ||
		strings.HasPrefix(key, "conf.rerank.")
}

// configCmd manages persisted store config. Subcommands:
//
//	set-ingest-root <path>   pin the ingest sandbox root for this store
//	get-ingest-root          show the effective ingest root and its source
func configCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("config: missing subcommand (set-ingest-root|get-ingest-root)")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "set-ingest-root":
		return setIngestRoot(rest)
	case "get-ingest-root":
		return getIngestRoot(rest)
	case "set":
		return configSet(rest)
	case "get":
		return configGet(rest)
	default:
		return fmt.Errorf("config: unknown subcommand %q", sub)
	}
}

// configSet persists an arbitrary store config key — notably the calibrated confidence
// floors (conf.floor.<model> / conf.rerank.<model> / conf.margin.<model>) that
// `ioc calibrate` prints, since calibrate runs in a throwaway dir and cannot write them
// into a live store itself. Usage: config set <key> <value> [-dir d] [-embed e].
func configSet(args []string) error {
	fs := flag.NewFlagSet("config set", flag.ExitOnError)
	dir, em := commonFlags(fs)
	if len(args) < 2 {
		return fmt.Errorf("config set: usage: config set <key> <value> [-dir d] [-embed e]")
	}
	key, val := args[0], args[1]
	_ = fs.Parse(args[2:])
	if key == "" {
		return fmt.Errorf("config set: empty key")
	}
	if !configSetAllowed(key) {
		return fmt.Errorf("config set: key %q is not settable here (allowed: conf.floor.* / conf.margin.* / conf.rerank.* / ingest_root); safety-critical keys (enc, emb_model, emb_dims) are protected", key)
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer func() { _ = e.Close() }()
	if err := e.SetConfig(key, val); err != nil {
		return err
	}
	return printJSON(map[string]any{"key": key, "value": val})
}

// configGet reads a store config key. Usage: config get <key> [-dir d] [-embed e].
func configGet(args []string) error {
	fs := flag.NewFlagSet("config get", flag.ExitOnError)
	dir, em := commonFlags(fs)
	if len(args) < 1 {
		return fmt.Errorf("config get: usage: config get <key> [-dir d] [-embed e]")
	}
	key := args[0]
	_ = fs.Parse(args[1:])
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer func() { _ = e.Close() }()
	v, ok := e.Config(key)
	return printJSON(map[string]any{"key": key, "value": v, "found": ok})
}

func setIngestRoot(args []string) error {
	pos, rest := splitPositional(args)
	fs := flag.NewFlagSet("config set-ingest-root", flag.ExitOnError)
	dir, em := commonFlags(fs)
	_ = fs.Parse(rest)
	if pos == "" {
		return fmt.Errorf("config set-ingest-root: missing <path>")
	}
	abs, err := filepath.Abs(filepath.Clean(pos))
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("config set-ingest-root: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("config set-ingest-root: %q is not a directory", abs)
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer func() { _ = e.Close() }()
	if err := e.SetConfig("ingest_root", abs); err != nil {
		return err
	}
	return printJSON(map[string]any{"ingest_root": abs})
}

func getIngestRoot(args []string) error {
	fs := flag.NewFlagSet("config get-ingest-root", flag.ExitOnError)
	dir, em := commonFlags(fs)
	_ = fs.Parse(args)
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer func() { _ = e.Close() }()

	root := ingest.ResolveIngestRoot(e) // runtime.Service has Config(key)(string,bool)
	source := "cwd"
	if os.Getenv(ingest.IngestRootEnv) != "" {
		source = "env"
	} else if v, ok := e.Config("ingest_root"); ok && v != "" {
		source = "config"
	}
	return printJSON(map[string]any{"ingest_root": root, "source": source})
}
