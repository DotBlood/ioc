package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/engine"
	"github.com/DotBlood/ioc/internal/iocfmt"
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

	resolveScope := func(name string) (core.ID, error) {
		if name == "" || name == "root" || name == "-" {
			return core.NilID, nil
		}
		id, ok := scopes[name]
		if !ok {
			return core.NilID, fmt.Errorf("unknown scope %q", name)
		}
		return id, nil
	}

	for i, t := range sc.Turns {
		switch t.Op {
		case "create_scope":
			parent, err := resolveScope(t.Parent)
			if err != nil {
				return nil, fmt.Errorf("turn %d: %w", i, err)
			}
			s, err := e.CreateScope(ctx, parent, iocfmt.ParseRole(t.Role), t.Title)
			if err != nil {
				return nil, fmt.Errorf("turn %d create_scope: %w", i, err)
			}
			scopes[t.ID] = s.ID

		case "push":
			scope, err := resolveScope(t.Scope)
			if err != nil {
				return nil, fmt.Errorf("turn %d: %w", i, err)
			}
			var content []byte
			if t.Content != "" {
				content = []byte(t.Content)
			}
			a, err := e.Push(ctx, core.PushRequest{
				Scope:   scope,
				Kind:    iocfmt.ParseKind(t.Kind),
				Summary: t.Summary,
				Content: content,
				Publish: t.Publish,
			})
			if err != nil {
				return nil, fmt.Errorf("turn %d push: %w", i, err)
			}
			if t.As != "" {
				arts[t.As] = a.ID
			}

		case "publish":
			id, ok := arts[t.Ref]
			if !ok {
				return nil, fmt.Errorf("turn %d publish: unknown artifact %q", i, t.Ref)
			}
			if err := e.Publish(ctx, id); err != nil {
				return nil, fmt.Errorf("turn %d publish: %w", i, err)
			}

		case "fork":
			src, err := resolveScope(t.Scope)
			if err != nil {
				return nil, fmt.Errorf("turn %d: %w", i, err)
			}
			ns, err := e.Fork(ctx, src, t.Title)
			if err != nil {
				return nil, fmt.Errorf("turn %d fork: %w", i, err)
			}
			scopes[t.ID] = ns.ID

		case "consolidate":
			scope, err := resolveScope(t.Scope)
			if err != nil {
				return nil, fmt.Errorf("turn %d: %w", i, err)
			}
			if _, err := e.Consolidate(ctx, scope, t.Summary); err != nil {
				return nil, fmt.Errorf("turn %d consolidate: %w", i, err)
			}

		case "crossversion":
			scope, err := resolveScope(t.Scope)
			if err != nil {
				return nil, fmt.Errorf("turn %d: %w", i, err)
			}
			ns, err := e.CrossVersion(ctx, scope, core.Seed{Constraints: t.Constraints, Lessons: t.Lessons})
			if err != nil {
				return nil, fmt.Errorf("turn %d crossversion: %w", i, err)
			}
			scopes[t.ID] = ns.ID

		case "rollup":
			scope, err := resolveScope(t.Scope)
			if err != nil {
				return nil, fmt.Errorf("turn %d: %w", i, err)
			}
			if err := e.RollupScope(ctx, scope, t.Summary); err != nil {
				return nil, fmt.Errorf("turn %d rollup: %w", i, err)
			}

		case "query":
			scope, err := resolveScope(t.Scope)
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
				fmt.Fprintln(traceW, string(line))
			}

		default:
			return nil, fmt.Errorf("turn %d: unknown op %q", i, t.Op)
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
