package eval

// Success targets for the slice.
const (
	TargetOverviewSufficiency = 0.80
	TargetContextRatio        = 0.25
	TargetRecall              = 1.00
)

// TurnMetric is the per-query record (also written as JSONL when a writer is given).
type TurnMetric struct {
	Turn                 int    `json:"turn"`
	Query                string `json:"query"`
	Met                  bool   `json:"met"`                    // expectation satisfied within MaxDrills
	SufficientAtOverview bool   `json:"sufficient_at_overview"` // satisfied with zero drills
	Drills               int    `json:"drills_to_raw"`
	OverviewTokens       int    `json:"overview_tokens"`
	RawTokensConsumed    int    `json:"raw_tokens_consumed"`
	BaselineTokens       int    `json:"baseline_tokens"`
	ExpectedFound        bool   `json:"expected_found"`
	ExpectedRank         int    `json:"expected_rank"`
	ForbidOK             bool   `json:"forbid_ok"`
}

// Report is the run-level result.
type Report struct {
	Scenario            string       `json:"scenario"`
	EmbModel            string       `json:"emb_model"`
	RecallTurns         int          `json:"recall_turns"`
	OverviewSufficiency float64      `json:"overview_sufficiency"` // (a)
	ContextRatio        float64      `json:"context_ratio"`        // (b)
	ConstraintSurvival  string       `json:"constraint_survival"`  // (c): "pass"|"fail"|"n/a"
	RecallAtTopK        float64      `json:"recall_at_topk"`       // (d)
	MeanRank            float64      `json:"mean_rank"`            // (d)
	Metrics             []TurnMetric `json:"-"`
}

// Pass reports whether the run met the slice's success targets.
func (r *Report) Pass() bool {
	return r.OverviewSufficiency >= TargetOverviewSufficiency &&
		r.ContextRatio <= TargetContextRatio &&
		r.RecallAtTopK >= TargetRecall &&
		r.ConstraintSurvival != "fail"
}
