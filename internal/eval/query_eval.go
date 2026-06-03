package eval

import (
	"context"
	"fmt"
	"strings"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/engine"
)

// evalQuery runs one query turn and scores it against the turn's Expectation.
func evalQuery(ctx context.Context, e *engine.Engine, turnIdx, topK int, scope core.ID, t Turn, arts map[string]core.ID, mode core.QueryMode, hierarchical bool, coarseK int) (TurnMetric, error) {
	m := TurnMetric{Turn: turnIdx, Query: t.Text, ForbidOK: true, Met: true}

	_, hits, err := e.Query(ctx, core.Query{
		Scope: scope, Text: t.Text, Detail: core.DetailOverview, TopK: topK,
		Mode: mode, Hierarchical: hierarchical, CoarseK: coarseK,
	})
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

	switch {
	case overviewSatisfied:
		m.Met = true
	case exp.MaxDrills > 0:
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
	default:
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
