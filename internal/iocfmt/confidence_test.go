package iocfmt

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
)

// TestQueryOut_FloorMiss: one hit below ConfidenceFloor — classified as floor_miss.
func TestQueryOut_FloorMiss(t *testing.T) {
	hits := []core.Hit{{Artifact: core.NewID(), Score: 0.50}}
	out := QueryOut(core.NewID(), hits, core.DefaultConfidence(bgeSmall))
	require.Equal(t, "floor_miss", out["confidence"].(string))
	require.Equal(t, true, out["weak_match"].(bool))
}

// TestQueryOut_MarginAmbiguous: two hits above floor but within MarginFloor — margin_ambiguous.
func TestQueryOut_MarginAmbiguous(t *testing.T) {
	hits := []core.Hit{
		{Artifact: core.NewID(), Score: 0.80},
		{Artifact: core.NewID(), Score: 0.78},
	}
	out := QueryOut(core.NewID(), hits, core.DefaultConfidence(bgeSmall))
	require.Equal(t, "margin_ambiguous", out["confidence"].(string))
	require.Equal(t, true, out["weak_match"].(bool))
}

// TestQueryOut_Ok: two hits, top above floor, margin above MarginFloor — ok.
func TestQueryOut_Ok(t *testing.T) {
	hits := []core.Hit{
		{Artifact: core.NewID(), Score: 0.82},
		{Artifact: core.NewID(), Score: 0.70},
	}
	out := QueryOut(core.NewID(), hits, core.DefaultConfidence(bgeSmall))
	require.Equal(t, "ok", out["confidence"].(string))
	require.Equal(t, false, out["weak_match"].(bool))
}

// TestQueryOut_Empty: no hits — classified as empty.
func TestQueryOut_Empty(t *testing.T) {
	out := QueryOut(core.NewID(), nil, core.DefaultConfidence(bgeSmall))
	require.Equal(t, "empty", out["confidence"].(string))
	require.Equal(t, true, out["weak_match"].(bool))
}

// TestQueryOut_RerankNoMarginGate: reranked path; margin gate is cosine-only, so a
// tiny rerank margin does NOT trigger margin_ambiguous — confidence is "ok" as long
// as top >= RerankFloor.
func TestQueryOut_RerankNoMarginGate(t *testing.T) {
	rs0 := 0.90
	rs1 := 0.89
	hits := []core.Hit{
		{Artifact: core.NewID(), Score: 0.4, RerankScore: &rs0},
		{Artifact: core.NewID(), Score: 0.4, RerankScore: &rs1},
	}
	out := QueryOut(core.NewID(), hits, core.DefaultConfidence(bgeSmall))
	require.Equal(t, "ok", out["confidence"].(string))
	require.Equal(t, false, out["weak_match"].(bool))
	require.Equal(t, "rerank", out["ranked_by"].(string))
}

// TestQueryOut_FloorMissBeatsMargin: floor_miss takes priority over margin_ambiguous
// even when the margin would also qualify as ambiguous.
func TestQueryOut_FloorMissBeatsMargin(t *testing.T) {
	hits := []core.Hit{
		{Artifact: core.NewID(), Score: 0.50},
		{Artifact: core.NewID(), Score: 0.49},
	}
	out := QueryOut(core.NewID(), hits, core.DefaultConfidence(bgeSmall))
	require.Equal(t, "floor_miss", out["confidence"].(string))
	require.NotEqual(t, "margin_ambiguous", out["confidence"].(string))
	require.Equal(t, true, out["weak_match"].(bool))
}
