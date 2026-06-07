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
	// Abstention is the FPR/FNR the DERIVED floor would produce on this probe set
	// (present vs absent), judged through the same core.Decide as production. It is the
	// number that says whether the floor actually separates present from absent.
	Abstention AbstentionReport
	// Rerank reports which floor was calibrated: false = cosine floor (conf.floor.<model>),
	// true = cross-encoder rerank floor (conf.rerank.<model>). Top scores in the report are
	// on the corresponding signal.
	Rerank bool
}

// CalibrateOpts configures a calibration run. Coverage is the fraction of relevant probes
// the floor must keep at/above it. Rerank=true calibrates the cross-encoder rerank floor
// (runs each probe reranked and reads rerank scores) instead of the cosine floor; it
// requires a reranker attached to the engine (a real /rerank endpoint).
type CalibrateOpts struct {
	Coverage float64
	Rerank   bool
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
func CalibrateRun(ctx context.Context, e *engine.Engine, spec *WallSpec, opts CalibrateOpts) (CalibrateReport, error) {
	coverage := opts.Coverage
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
	var presentProbes, absentProbes []Probe

	for _, q := range spec.Questions {
		scope, err := resolveScopeName(scopes, q.Scope)
		if err != nil {
			return CalibrateReport{}, fmt.Errorf("calibrate: question %q: %w", q.ID, err)
		}
		// Cosine pass measures the raw cosine signal (ConfidenceFloor); rerank pass runs
		// the same query reranked and reads the cross-encoder score (RerankFloor).
		_, hits, err := e.Query(ctx, core.Query{
			Scope:  scope,
			Text:   q.Question,
			TopK:   topK,
			Rerank: opts.Rerank,
			// Mode/Hierarchical left at zero-values (ModeVector, flat) — calibrate against
			// the mode-agnostic signal.
		})
		if err != nil {
			return CalibrateReport{}, fmt.Errorf("calibrate: question %q: %w", q.ID, err)
		}
		// score reads the signal being calibrated: rerank score when reranking, else cosine.
		sig := func(h core.Hit) float64 {
			if opts.Rerank && h.RerankScore != nil {
				return *h.RerankScore
			}
			return h.Score
		}
		if opts.Rerank && len(hits) > 0 && hits[0].RerankScore == nil {
			return CalibrateReport{}, fmt.Errorf("calibrate: -rerank requires a reranker attached (real -embed with a /rerank endpoint); query %q was not reranked", q.ID)
		}

		var topScore float64
		if len(hits) > 0 {
			topScore = sig(hits[0])
		}
		probe := Probe{NumHits: len(hits), Top1: topScore, Reranked: opts.Rerank}
		if len(hits) > 1 {
			probe.Top2 = sig(hits[1])
		}

		if len(q.GoldRefs) > 0 {
			relevantTops = append(relevantTops, topScore)
			presentProbes = append(presentProbes, probe)
		} else {
			absentTops = append(absentTops, topScore)
			absentProbes = append(absentProbes, probe)
		}
	}

	floor := FloorFromScores(relevantTops, coverage)

	var maxAbsentTop float64
	for _, s := range absentTops {
		if s > maxAbsentTop {
			maxAbsentTop = s
		}
	}

	// Abstention the DERIVED floor would produce on this probe set, judged through the
	// same core.Decide production uses. Apply the floor to the signal being calibrated:
	// the rerank floor for a rerank pass, else the cosine floor (MarginFloor at default).
	conf := core.DefaultConfidence(e.EmbModel())
	if opts.Rerank {
		conf.RerankFloor = floor
	} else {
		conf.Floor = floor
		conf.Calibrated = true
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
		Abstention:   Abstention(presentProbes, absentProbes, conf),
		Rerank:       opts.Rerank,
	}, nil
}
