package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/engine"
)

// Run plays a scenario against the engine. If traceW is non-nil, each query
// turn's TurnMetric is written to it as one JSON line. mode/hierarchical/coarseK
// configure how query turns retrieve.
func Run(ctx context.Context, e *engine.Engine, sc *Scenario, traceW io.Writer, mode core.QueryMode, hierarchical bool, coarseK int, rerank bool) (*Report, error) {
	scopes := map[string]core.ID{}
	arts := map[string]core.ID{}

	rep := &Report{Scenario: sc.Name}
	var ovSuffCount, foundCount, rankSum, forbidTurns, forbidPass int
	var ioTokens, baseTokens int

	for i, t := range sc.Turns {
		handled, err := applyStructureTurn(ctx, e, i, t, scopes, arts)
		if err != nil {
			return nil, err
		}
		if handled {
			continue // create_scope/push/publish/fork/consolidate/crossversion/rollup
		}

		// The only unhandled op is "query" — score it.
		scope, err := resolveScopeName(scopes, t.Scope)
		if err != nil {
			return nil, fmt.Errorf("turn %d: %w", i, err)
		}
		m, err := evalQuery(ctx, e, i, sc.TopK, scope, t, arts, mode, hierarchical, coarseK, rerank)
		if err != nil {
			return nil, fmt.Errorf("turn %d query: %w", i, err)
		}
		rep.Metrics = append(rep.Metrics, m)
		rep.RecallTurns++
		if m.SufficientAtOverview {
			ovSuffCount++
		}
		if m.ExpectedFound {
			foundCount++
			rankSum += m.ExpectedRank
		}
		ioTokens += m.OverviewTokens + m.RawTokensConsumed
		baseTokens += m.BaselineTokens
		if t.Expect != nil && len(t.Expect.ForbidMention) > 0 {
			forbidTurns++
			if m.ForbidOK && m.Met {
				forbidPass++
			}
		}
		if traceW != nil {
			line, _ := json.Marshal(m)
			_, _ = fmt.Fprintln(traceW, string(line))
		}
	}

	rep.EmbModel = e.EmbModel()
	if rep.RecallTurns > 0 {
		rep.OverviewSufficiency = float64(ovSuffCount) / float64(rep.RecallTurns)
		rep.RecallAtTopK = float64(foundCount) / float64(rep.RecallTurns)
	}
	if foundCount > 0 {
		rep.MeanRank = float64(rankSum) / float64(foundCount)
	}
	if baseTokens > 0 {
		rep.ContextRatio = float64(ioTokens) / float64(baseTokens)
	}
	switch {
	case forbidTurns == 0:
		rep.ConstraintSurvival = "n/a"
	case forbidPass == forbidTurns:
		rep.ConstraintSurvival = "pass"
	default:
		rep.ConstraintSurvival = "fail"
	}
	return rep, nil
}
