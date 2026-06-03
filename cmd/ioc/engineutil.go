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
)

const defaultDataDir = ".ioc-data"

func buildEmbedder(endpoint string) embed.Embedder {
	if endpoint == "" {
		return embed.NewMockEmbedder(384)
	}
	return embed.NewHTTPEmbedder(endpoint)
}

func openEngine(dir, endpoint string) (*engine.Engine, error) {
	return engine.Open(context.Background(), dir, buildEmbedder(endpoint))
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
