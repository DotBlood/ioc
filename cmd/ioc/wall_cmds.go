package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DotBlood/ioc/internal/eval"
	"github.com/DotBlood/ioc/internal/iocfmt"
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
	mode := fs.String("mode", "vector", "retrieval mode: vector|hybrid|hierarchical")
	coarseK := fs.Int("coarsek", 0, "hierarchical coarse stage: # scopes to keep (0=engine default)")
	rerank := fs.Bool("rerank", false, "cross-encoder rerank the top candidates (needs a real -embed)")
	graphBoost := fs.Float64("graph-boost", 0, "opt-in graph-aware boost weight 0..1 (0=off)")
	importanceWeight := fs.Float64("importance-weight", 0, "opt-in author-declared importance weight 0..1 (0=off)")
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
	e, err := openEngineEmbedded(*dir, *em, *rerank)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: open engine:", err)
		return 1
	}
	defer func() { _ = e.Close() }()

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
	defer func() { _ = pf.Close() }()
	gf, err := os.Create(goldPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: gold file:", err)
		return 1
	}
	defer func() { _ = gf.Close() }()

	qm, hier, coll := iocfmt.ParseModeSpec(*mode)
	rep, err := eval.WallRun(context.Background(), e, spec, pf, gf, qm, hier, coll, *coarseK, *rerank, *graphBoost, *importanceWeight)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: wall run:", err)
		return 1
	}
	fmt.Printf("config: mode=%s coarsek=%d rerank=%v graph-boost=%g importance-weight=%g\n", *mode, *coarseK, *rerank, *graphBoost, *importanceWeight)
	fmt.Print(rep.String())
	fmt.Printf("blind judge packets: %s\n", packetsPath)
	fmt.Printf("gold + retrieval facts: %s\n", goldPath)
	return 0
}
