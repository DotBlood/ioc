package eval

import (
	"fmt"
	"strings"
)

// String renders a human-readable PASS/FAIL table for the run.
func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Scenario: %s   (embedder: %s)\n", r.Scenario, r.EmbModel)
	fmt.Fprintf(&b, "Recall turns: %d\n", r.RecallTurns)
	fmt.Fprintln(&b, strings.Repeat("-", 64))
	fmt.Fprintf(&b, "%-34s %10s %8s  %s\n", "metric", "value", "target", "result")
	fmt.Fprintln(&b, strings.Repeat("-", 64))

	row := func(name, val, target string, ok bool) {
		res := "PASS"
		if !ok {
			res = "FAIL"
		}
		fmt.Fprintf(&b, "%-34s %10s %8s  %s\n", name, val, target, res)
	}

	row("(a) overview-sufficiency",
		fmt.Sprintf("%.2f", r.OverviewSufficiency),
		fmt.Sprintf(">=%.2f", TargetOverviewSufficiency),
		r.OverviewSufficiency >= TargetOverviewSufficiency)
	row("(b) context ratio (IOC/raw)",
		fmt.Sprintf("%.2f", r.ContextRatio),
		fmt.Sprintf("<=%.2f", TargetContextRatio),
		r.ContextRatio <= TargetContextRatio)
	cOK := r.ConstraintSurvival != "fail"
	row("(c) constraint survival", r.ConstraintSurvival, "pass", cOK)
	row("(d) recall@topK",
		fmt.Sprintf("%.2f", r.RecallAtTopK),
		fmt.Sprintf(">=%.2f", TargetRecall),
		r.RecallAtTopK >= TargetRecall)

	fmt.Fprintf(&b, "%-34s %10s\n", "    mean rank (found)", fmt.Sprintf("%.2f", r.MeanRank))
	fmt.Fprintln(&b, strings.Repeat("-", 64))
	overall := "PASS"
	if !r.Pass() {
		overall = "FAIL"
	}
	fmt.Fprintf(&b, "OVERALL: %s\n", overall)
	if r.EmbModel == "mock-bow" {
		fmt.Fprintln(&b, "\nNOTE: mock-bow embedder is a lexical proxy. These numbers validate the")
		fmt.Fprintln(&b, "pipeline, NOT the real 'wall'. Re-run with the real embedder for wall numbers.")
	}
	return b.String()
}
