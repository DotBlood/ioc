package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/DotBlood/ioc/internal/eval"
)

// calibrate derives a per-embedder confidence floor from a probe spec and
// optionally writes it into the store config so that ResolveConfidence picks it
// up automatically (R4b). The floor is the (1−coverage) quantile of the
// relevant-probe top cosine scores — the lowest score that keeps `coverage`
// fraction of known-relevant queries above the floor.
func calibrate(args []string) error {
	fs := flag.NewFlagSet("calibrate", flag.ExitOnError)
	dir := fs.String("dir", filepath.Join(os.TempDir(), "ioc-calibrate"), "run data directory (reset each run)")
	em := fs.String("embed", "", "embedder endpoint (empty=mock)")
	probe := fs.String("probe", "", "probe spec JSON file (required)")
	coverage := fs.Float64("coverage", 0.9, "fraction of relevant probes the floor must cover (0..1)")
	rerank := fs.Bool("rerank", false, "calibrate the cross-encoder rerank floor (conf.rerank.<model>) instead of the cosine floor; needs a real -embed with a /rerank endpoint")
	apply := fs.String("apply", "", "auto-apply: live store dir to persist the derived floor into (e.g. your IOC_DIR / MCP data dir). Routes through a daemon if one owns it. Empty = print only.")
	_ = fs.Parse(args)

	if *probe == "" {
		return fmt.Errorf("calibrate: -probe <spec.json> is required")
	}

	spec, err := eval.LoadWallSpec(*probe)
	if err != nil {
		return fmt.Errorf("calibrate: load probe spec: %w", err)
	}

	if err := os.RemoveAll(*dir); err != nil {
		return fmt.Errorf("calibrate: reset dir: %w", err)
	}
	e, err := openEngineEmbedded(*dir, *em, *rerank)
	if err != nil {
		return fmt.Errorf("calibrate: open engine: %w", err)
	}
	defer e.Close()

	rep, err := eval.CalibrateRun(context.Background(), e, spec, eval.CalibrateOpts{Coverage: *coverage, Rerank: *rerank})
	if err != nil {
		return fmt.Errorf("calibrate: run: %w", err)
	}

	signal := "cosine"
	if rep.Rerank {
		signal = "rerank"
	}
	fmt.Printf("model:          %s\n", rep.Model)
	fmt.Printf("signal:         %s\n", signal)
	fmt.Printf("calibrated floor: %.4f\n", rep.Floor)
	fmt.Printf("coverage:       %.2f\n", rep.Coverage)
	fmt.Printf("relevant probes: %d  (top scores: %v)\n", rep.RelevantN, rep.RelevantTops)
	fmt.Printf("absent probes:  %d  (top scores: %v)\n", rep.AbsentN, rep.AbsentTops)
	fmt.Printf("max absent top: %.4f\n", rep.MaxAbsentTop)

	ab := rep.Abstention
	fmt.Printf("abstention @ floor (%s): present %d (weak %d) absent %d (weak %d) — FPR %.2f (fakes answered) FNR %.2f (reals suppressed)\n",
		ab.RankedBy, ab.PresentN, ab.PresentWeak, ab.AbsentN, ab.AbsentWeak, ab.FalsePosRate, ab.FalseNegRate)

	if rep.Floor <= rep.MaxAbsentTop {
		fmt.Fprintln(os.Stderr, "WARNING: floor does not separate absent probes — add more/stronger probes")
	}

	// Auto-apply: persist the derived floor straight into the LIVE store named by -apply
	// (the calibration corpus lives in the throwaway -dir, which is reset each run, so the
	// floor must be written elsewhere). openService routes the write through a daemon when
	// one owns the live dir; otherwise it opens the store directly (which fails if another
	// process — e.g. the MCP server — holds the lock, so stop it or run a daemon).
	key := "conf.floor." + rep.Model
	if rep.Rerank {
		key = "conf.rerank." + rep.Model
	}
	val := strconv.FormatFloat(rep.Floor, 'f', 4, 64)
	if *apply == "" {
		fmt.Printf("(not persisted — pass -apply <live-store-dir> to auto-apply; would set %s = %s)\n", key, val)
		return nil
	}
	le, err := openService(*apply, *em, false)
	if err != nil {
		return fmt.Errorf("calibrate: open apply store %q: %w", *apply, err)
	}
	defer le.Close()
	// Guard against applying a floor under the wrong model key — but only when the live
	// store's model is actually known. A freshly opened HTTP embedder reports "" until its
	// first call, so an empty model means "unknown", not "mismatch" (trust the same -embed).
	if lm := le.EmbModel(); lm != "" && lm != rep.Model {
		return fmt.Errorf("calibrate: apply store embedder %q != calibration embedder %q — the floor is per-model; use the same -embed", lm, rep.Model)
	}
	if err := le.SetConfig(key, val); err != nil {
		return fmt.Errorf("calibrate: persist to apply store: %w", err)
	}
	fmt.Printf("applied to %s: %s = %s\n", *apply, key, val)
	return nil
}
