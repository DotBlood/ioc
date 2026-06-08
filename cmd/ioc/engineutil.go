package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/DotBlood/ioc/internal/config"
	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/engine"
	"github.com/DotBlood/ioc/internal/logging"
	"github.com/DotBlood/ioc/internal/runtime"
)

const defaultDataDir = ".ioc/data"

// appConfig holds the resolved process configuration. main() overwrites it at
// startup after calling config.Load; the default ensures pre-flag code paths
// see sensible values.
var appConfig = config.Default()

// firstNonEmpty returns a if non-empty, else b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// allowRemoteEmbed reports whether non-loopback embedder endpoints are permitted
// (V2). Config already resolved env+file; plaintext-remote is always refused
// (https required) by the embed package.
func allowRemoteEmbed() bool {
	return appConfig.AllowRemoteEmbed
}

func buildEmbedder(endpoint string) (embed.Embedder, error) {
	if endpoint == "" {
		return embed.NewMockEmbedder(384), nil
	}
	return embed.NewHTTPEmbedder(endpoint, allowRemoteEmbed())
}

// rerankOpts attaches a cross-encoder reranker on any REAL endpoint, so both explicit
// rerank (-rerank) and borderline AutoRerank are available without a second flag (R5);
// the reranker is only invoked when a query asks for it, and construction does not dial.
// The mock embedder (endpoint=="") gets none, so mock/offline paths stay pure-cosine. The
// rerank bool is retained for call-site readability but no longer gates attachment.
func rerankOpts(rerank bool, endpoint string) ([]engine.Option, error) {
	_ = rerank
	if endpoint != "" {
		rr, err := embed.NewHTTPReranker(endpoint, allowRemoteEmbed())
		if err != nil {
			return nil, err
		}
		return []engine.Option{engine.WithReranker(rr)}, nil
	}
	return nil, nil
}

// openEngineEmbedded opens the store DIRECTLY (no daemon) — for the daemon
// itself (serve) and throwaway eval runs (run-scenario), which need to own the
// concrete engine rather than a client.
func openEngineEmbedded(dir, endpoint string, rerank bool) (*engine.Engine, error) {
	emb, err := buildEmbedder(endpoint)
	if err != nil {
		return nil, err
	}
	opts, err := rerankOpts(rerank, endpoint)
	if err != nil {
		return nil, err
	}
	return engine.Open(context.Background(), dir, emb, opts...)
}

// openService returns a daemon client when one owns dir, else an embedded
// engine. Memory commands use this so they transparently route through a
// running daemon (and never hit the "dir busy" lock).
func openService(dir, endpoint string, rerank bool) (runtime.Service, error) {
	emb, err := buildEmbedder(endpoint)
	if err != nil {
		return nil, err
	}
	opts, err := rerankOpts(rerank, endpoint)
	if err != nil {
		return nil, err
	}
	return runtime.Open(dir, emb, opts...)
}

// commonFlags registers -dir, -embed, and the three side-effecting global flags
// (-config, -log-level, -log-format) on a flag set.  It returns (dir, embed)
// exactly as before so all call sites stay unchanged.
func commonFlags(fs *flag.FlagSet) (*string, *string) {
	dir := fs.String("dir", firstNonEmpty(appConfig.Dir, defaultDataDir), "persistent data directory")
	em := fs.String("embed", appConfig.Embed, "embedder endpoint (empty=mock; http://host:port or unix:/path)")

	fs.Func("config", "path to ioc.json (reloads process config)", func(p string) error {
		c, err := config.Load(p)
		if err != nil {
			return err
		}
		appConfig = c
		logging.Init(c.LogLevel, c.LogFormat)
		return nil
	})
	fs.Func("log-level", "debug|info|warn|error", func(s string) error {
		logging.SetLevel(s)
		return nil
	})
	fs.Func("log-format", "text|json", func(s string) error {
		logging.SetFormat(s)
		return nil
	})

	return dir, em
}

func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

// readContent resolves push content: a file, stdin ("-"), inline, or none.
func readContent(inline, file string) ([]byte, error) {
	switch {
	case file != "":
		return os.ReadFile(file)
	case inline == "-":
		return io.ReadAll(os.Stdin)
	case inline != "":
		return []byte(inline), nil
	default:
		return nil, nil
	}
}
