package eval

import (
	"context"
	"fmt"
	"sort"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/engine"
)

// Quantile returns the q-quantile (0<=q<=1) of xs via linear interpolation on a sorted
// copy (xs is not mutated). Empty xs returns 0.
func Quantile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	switch {
	case q <= 0:
		return s[0]
	case q >= 1:
		return s[len(s)-1]
	}
	pos := q * float64(len(s)-1)
	lo := int(pos)
	if lo+1 >= len(s) {
		return s[lo]
	}
	frac := pos - float64(lo)
	return s[lo]*(1-frac) + s[lo+1]*frac
}

// FloorFromScores derives a confidence floor from the top scores of KNOWN-RELEVANT probe
// queries: the (1−coverage) quantile, i.e. the score that keeps `coverage` of the
// relevant probes at/above the floor (split-conformal calibration, arXiv:2511.17908).
// Higher coverage → lower floor (fewer relevant probes excluded). This replaces a
// hardcoded, non-portable constant with a per-embedder value derived from data
// (absolute cosine floors do not transfer across embedders — arXiv:2403.05440).
// coverage is clamped to [0,1].
func FloorFromScores(relevantTops []float64, coverage float64) float64 {
	switch {
	case coverage < 0:
		coverage = 0
	case coverage > 1:
		coverage = 1
	}
	return Quantile(relevantTops, 1-coverage)
}

// CalibrateReport holds the result of a calibration run. It reports the derived
// confidence floor alongside the raw evidence — relevant-probe top scores, absent-probe
// top scores, and a separation check — so the caller can judge quality before writing.
type CalibrateReport struct {
	Model        string    // embedder model identifier (e.EmbModel())
	Floor        float64   // derived confidence floor (FloorFromScores at coverage)
	Coverage     float64   // fraction of relevant probes at or above Floor
	RelevantN    int       // number of questions with non-empty GoldRefs (relevant probes)
	AbsentN      int       // number of questions with empty GoldRefs (absent probes)
	MaxAbsentTop float64   // highest top-hit score among absent probes (0 if none)
	RelevantTops []float64 // top-hit scores for each relevant question (len == RelevantN)
	AbsentTops   []float64 // top-hit scores for each absent question (len == AbsentN)
}

// CalibrateRun builds the probe corpus from spec.Build, then for every question in
// spec.Questions it runs a simple vector query and records the top-hit cosine score.
// Questions with non-empty GoldRefs are treated as RELEVANT probes; questions with
// empty GoldRefs are treated as ABSENT probes (off-topic, should not score high).
// The derived floor is the (1−coverage) quantile of the relevant-probe top scores —
// i.e. the value that keeps `coverage` fraction of relevant probes at or above the
// floor. No printing is done inside this function; the caller decides how to present
// and persist the report.
//
// It replicates the build-turn loop used by WallRun (copy, not refactor) so that
// WallRun's own corpus-construction logic is never affected.
func CalibrateRun(ctx context.Context, e *engine.Engine, spec *WallSpec, coverage float64) (CalibrateReport, error) {
	topK := spec.TopK
	if topK <= 0 {
		topK = 5
	}

	// Build the probe corpus — exact copy of WallRun's build loop.
	scopes := map[string]core.ID{}
	arts := map[string]core.ID{}
	for i, t := range spec.Build {
		handled, err := applyStructureTurn(ctx, e, i, t, scopes, arts)
		if err != nil {
			return CalibrateReport{}, err
		}
		if !handled {
			return CalibrateReport{}, fmt.Errorf("calibrate: build turn %d: %q is not allowed in a calibrate build (questions are separate)", i, t.Op)
		}
	}

	var relevantTops, absentTops []float64

	for _, q := range spec.Questions {
		scope, err := resolveScopeName(scopes, q.Scope)
		if err != nil {
			return CalibrateReport{}, fmt.Errorf("calibrate: question %q: %w", q.ID, err)
		}
		// Plain vector query — calibration measures the raw cosine signal before any
		// mode selection or reranking, which is what ConfidenceFloor guards.
		_, hits, err := e.Query(ctx, core.Query{
			Scope: scope,
			Text:  q.Question,
			TopK:  topK,
			// Mode, Hierarchical, etc. left at zero-values (ModeVector, flat) — the
			// floor is a cosine gate; calibrate against the mode-agnostic signal.
		})
		if err != nil {
			return CalibrateReport{}, fmt.Errorf("calibrate: question %q: %w", q.ID, err)
		}

		var topScore float64
		if len(hits) > 0 {
			topScore = hits[0].Score
		}

		if len(q.GoldRefs) > 0 {
			relevantTops = append(relevantTops, topScore)
		} else {
			absentTops = append(absentTops, topScore)
		}
	}

	floor := FloorFromScores(relevantTops, coverage)

	var maxAbsentTop float64
	for _, s := range absentTops {
		if s > maxAbsentTop {
			maxAbsentTop = s
		}
	}

	return CalibrateReport{
		Model:        e.EmbModel(),
		Floor:        floor,
		Coverage:     coverage,
		RelevantN:    len(relevantTops),
		AbsentN:      len(absentTops),
		MaxAbsentTop: maxAbsentTop,
		RelevantTops: relevantTops,
		AbsentTops:   absentTops,
	}, nil
}
