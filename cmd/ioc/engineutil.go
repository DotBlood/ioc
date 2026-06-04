package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/engine"
	"github.com/DotBlood/ioc/internal/runtime"
)

const defaultDataDir = ".ioc/data"

func buildEmbedder(endpoint string) embed.Embedder {
	if endpoint == "" {
		return embed.NewMockEmbedder(384)
	}
	return embed.NewHTTPEmbedder(endpoint)
}

// rerankOpts attaches a reranker when requested against a real endpoint.
func rerankOpts(rerank bool, endpoint string) []engine.Option {
	if rerank && endpoint != "" {
		return []engine.Option{engine.WithReranker(embed.NewHTTPReranker(endpoint))}
	}
	return nil
}

// openEngineEmbedded opens the store DIRECTLY (no daemon) — for the daemon
// itself (serve) and throwaway eval runs (run-scenario), which need to own the
// concrete engine rather than a client.
func openEngineEmbedded(dir, endpoint string, rerank bool) (*engine.Engine, error) {
	return engine.Open(context.Background(), dir, buildEmbedder(endpoint), rerankOpts(rerank, endpoint)...)
}

// openService returns a daemon client when one owns dir, else an embedded
// engine. Memory commands use this so they transparently route through a
// running daemon (and never hit the "dir busy" lock).
func openService(dir, endpoint string, rerank bool) (runtime.Service, error) {
	return runtime.Open(dir, buildEmbedder(endpoint), rerankOpts(rerank, endpoint)...)
}

// commonFlags registers -dir and -embed on a flag set.
func commonFlags(fs *flag.FlagSet) (*string, *string) {
	dir := fs.String("dir", defaultDataDir, "persistent data directory")
	em := fs.String("embed", "", "embedder endpoint (empty=mock; http://host:port or unix:/path)")
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
