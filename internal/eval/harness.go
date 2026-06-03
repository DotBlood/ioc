package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/engine"
)

// Success targets for the slice.
const (
	TargetOverviewSufficiency = 0.80
	TargetContextRatio        = 0.25
	TargetRecall              = 1.00
)

// TurnMetric is the per-query record (also written as JSONL when a writer is given).
type TurnMetric struct {
	Turn                 int     `json:"turn"`
	Query                string  `json:"query"`
	Met                  bool    `json:"met"`                    // expectation satisfied within MaxDrills
	SufficientAtOverview bool    `json:"sufficient_at_overview"` // satisfied with zero drills
	Drills               int     `json:"drills_to_raw"`
	OverviewTokens       int     `json:"overview_tokens"`
	RawTokensConsumed    int     `json:"raw_tokens_consumed"`
	BaselineTokens       int     `json:"baseline_tokens"`
	ExpectedFound        bool    `json:"expected_found"`
	ExpectedRank         int     `json:"expected_rank"`
	ForbidOK             bool    `json:"forbid_ok"`
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

// Run plays a scenario against the engine. If traceW is non-nil, each query
// turn's TurnMetric is written to it as one JSON line.
func Run(ctx context.Context, e *engine.Engine, sc *Scenario, traceW io.Writer) (*Report, error) {
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
			s, err := e.CreateScope(ctx, parent, parseRole(t.Role), t.Title)
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
				Kind:    parseKind(t.Kind),
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

		case "query":
			scope, err := resolveScope(t.Scope)
			if err != nil {
				return nil, fmt.Errorf("turn %d: %w", i, err)
			}
			m, err := evalQuery(ctx, e, i, sc.TopK, scope, t, arts)
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

func evalQuery(ctx context.Context, e *engine.Engine, turnIdx, topK int, scope core.ID, t Turn, arts map[string]core.ID) (TurnMetric, error) {
	m := TurnMetric{Turn: turnIdx, Query: t.Text, ForbidOK: true, Met: true}

	hits, err := e.Query(ctx, core.Query{Scope: scope, Text: t.Text, Detail: core.DetailOverview, TopK: topK})
	if err != nil {
		return m, err
	}
	for _, h := range hits {
		m.OverviewTokens += tokens(h.Summary)
	}

	exp := t.Expect
	if exp == nil {
		return m, nil
	}

	// target presence + rank in the overview.
	var targetID core.ID
	hasTarget := exp.MustContain != ""
	if hasTarget {
		id, ok := arts[exp.MustContain]
		if !ok {
			return m, fmt.Errorf("expect.mustContain: unknown artifact %q", exp.MustContain)
		}
		targetID = id
	}
	found, rank := scanHits(hits, hasTarget, targetID, exp.MustMentionAny)
	m.ExpectedFound = found
	m.ExpectedRank = rank

	// forbid check on the cheap overview surface (repeat-mistake guard).
	m.ForbidOK = !summariesMentionAny(hits, exp.ForbidMention)

	targetOK := !hasTarget || hitsContainID(hits, targetID)
	mentionOK := len(exp.MustMentionAny) == 0 || summariesMentionAny(hits, exp.MustMentionAny)
	overviewSatisfied := targetOK && mentionOK && m.ForbidOK
	m.SufficientAtOverview = overviewSatisfied

	if overviewSatisfied {
		m.Met = true
	} else if exp.MaxDrills > 0 {
		// Drill into top hits to recover the expectation from raw content.
		for _, h := range hits {
			if m.Drills >= exp.MaxDrills {
				break
			}
			d, err := e.Drill(ctx, h.Artifact, core.DetailRaw)
			if err != nil {
				return m, err
			}
			m.Drills++
			m.RawTokensConsumed += tokens(string(d.Content))
			if len(exp.MustMentionAny) > 0 && containsAny(string(d.Content), exp.MustMentionAny) {
				mentionOK = true
			}
			if targetOK && mentionOK && m.ForbidOK {
				break
			}
		}
		m.Met = targetOK && mentionOK && m.ForbidOK
	} else {
		m.Met = false
	}

	// context baseline: tokens an agent would carry WITHOUT IOC.
	for _, name := range exp.RawBaseline {
		id, ok := arts[name]
		if !ok {
			return m, fmt.Errorf("expect.rawBaseline: unknown artifact %q", name)
		}
		m.BaselineTokens += baselineTokens(ctx, e, id)
	}
	return m, nil
}

func baselineTokens(ctx context.Context, e *engine.Engine, id core.ID) int {
	d, err := e.Drill(ctx, id, core.DetailRaw)
	if err != nil {
		return 0
	}
	if len(d.Content) > 0 {
		return tokens(string(d.Content))
	}
	return tokens(d.Summary)
}

func scanHits(hits []core.Hit, hasTarget bool, target core.ID, mentions []string) (found bool, rank int) {
	for i, h := range hits {
		match := false
		if hasTarget && h.Artifact == target {
			match = true
		}
		if !hasTarget && len(mentions) > 0 && containsAny(h.Summary, mentions) {
			match = true
		}
		if match {
			return true, i + 1
		}
	}
	return false, 0
}

func hitsContainID(hits []core.Hit, id core.ID) bool {
	for _, h := range hits {
		if h.Artifact == id {
			return true
		}
	}
	return false
}

func summariesMentionAny(hits []core.Hit, terms []string) bool {
	for _, h := range hits {
		if containsAny(h.Summary, terms) {
			return true
		}
	}
	return false
}

func containsAny(text string, terms []string) bool {
	low := strings.ToLower(text)
	for _, term := range terms {
		if term != "" && strings.Contains(low, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func tokens(s string) int { return len(strings.Fields(s)) }

func parseRole(s string) core.Role {
	switch strings.ToLower(s) {
	case "worktree":
		return core.RoleWorktree
	case "workspace":
		return core.RoleWorkspace
	default:
		return core.RoleSession
	}
}

func parseKind(s string) core.ArtifactKind {
	switch strings.ToLower(s) {
	case "answer":
		return core.KindAnswer
	case "summary":
		return core.KindSummary
	case "document":
		return core.KindDocument
	case "reasoning":
		return core.KindReasoning
	case "seed":
		return core.KindSeed
	default:
		return core.KindInsight
	}
}
