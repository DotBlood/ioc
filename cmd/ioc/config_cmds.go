package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DotBlood/ioc/internal/ingest"
)

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
	default:
		return fmt.Errorf("config: unknown subcommand %q", sub)
	}
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
	defer e.Close()
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
	defer e.Close()

	root := ingest.ResolveIngestRoot(e) // runtime.Service has Config(key)(string,bool)
	source := "cwd"
	if os.Getenv(ingest.IngestRootEnv) != "" {
		source = "env"
	} else if v, ok := e.Config("ingest_root"); ok && v != "" {
		source = "config"
	}
	return printJSON(map[string]any{"ingest_root": root, "source": source})
}
