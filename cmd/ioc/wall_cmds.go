package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DotBlood/ioc/internal/eval"
)

// runWall builds a reasoning-wall corpus in a throwaway store and emits the
// blind-judge packets + the gold sidecar. The actual answer-grounded scoring is
// done by an external blind LLM judge over packets.jsonl vs gold.jsonl (see
// docs/WALL_EXPERIMENT.md) — IOC never calls an LLM itself.
func runWall(args []string) int {
	fs := flag.NewFlagSet("wall", flag.ExitOnError)
	dir := fs.String("dir", filepath.Join(os.TempDir(), "ioc-wall"), "run data directory (reset each run)")
	em := fs.String("embed", "", "embedder endpoint (empty=mock)")
	out := fs.String("out", "", "output directory for packets.jsonl/gold.jsonl (default: <dir>)")
	specPath, rest := splitPositional(args)
	_ = fs.Parse(rest)
	if specPath == "" {
		usage()
		return 2
	}
	spec, err := eval.LoadWallSpec(specPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if err := os.RemoveAll(*dir); err != nil {
		fmt.Fprintln(os.Stderr, "error: reset dir:", err)
		return 1
	}
	// The wall corpus is reasoning-only; rerank needs no endpoint here.
	e, err := openEngineEmbedded(*dir, *em, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: open engine:", err)
		return 1
	}
	defer e.Close()

	outDir := *out
	if outDir == "" {
		outDir = *dir
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "error: out dir:", err)
		return 1
	}
	packetsPath := filepath.Join(outDir, "packets.jsonl")
	goldPath := filepath.Join(outDir, "gold.jsonl")
	pf, err := os.Create(packetsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: packets file:", err)
		return 1
	}
	defer pf.Close()
	gf, err := os.Create(goldPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: gold file:", err)
		return 1
	}
	defer gf.Close()

	rep, err := eval.WallRun(context.Background(), e, spec, pf, gf)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: wall run:", err)
		return 1
	}
	fmt.Print(rep.String())
	fmt.Printf("blind judge packets: %s\n", packetsPath)
	fmt.Printf("gold + retrieval facts: %s\n", goldPath)
	return 0
}
