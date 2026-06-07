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
	write := fs.Bool("write", true, "persist the derived floor to the store config")
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

	if *write {
		key := "conf.floor." + rep.Model
		if rep.Rerank {
			key = "conf.rerank." + rep.Model
		}
		val := strconv.FormatFloat(rep.Floor, 'f', 4, 64)
		if err := e.SetConfig(key, val); err != nil {
			return fmt.Errorf("calibrate: write config: %w", err)
		}
		fmt.Printf("written to config: %s = %s\n", key, val)
	} else {
		fmt.Println("(dry run — floor not written to config; use -write=true to persist)")
	}

	return nil
}
