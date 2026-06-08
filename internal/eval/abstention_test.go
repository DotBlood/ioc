package eval

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
)

const bgeSmall = "BAAI/bge-small-en-v1.5" // Floor 0.68, MarginFloor 0.05, RerankFloor 0.5

// A well-separated cosine set: present above the floor, absent below it → no errors.
func TestAbstention_CosineSeparates(t *testing.T) {
	conf := core.DefaultConfidence(bgeSmall)
	present := []Probe{{NumHits: 1, Top1: 0.80}, {NumHits: 2, Top1: 0.85, Top2: 0.70}}
	absent := []Probe{{NumHits: 1, Top1: 0.50}, {NumHits: 1, Top1: 0.60}}
	r := Abstention(present, absent, conf)
	require.Equal(t, "cosine", r.RankedBy)
	require.Equal(t, 0.0, r.FalseNegRate, "both present hits clear the floor")
	require.Equal(t, 0.0, r.FalsePosRate, "both absent hits are below the floor → flagged weak")
}

// The measured failure: absent (fake) questions clear the cosine floor (~0.70-0.73 on
// shared vocabulary), so none are flagged weak → FalsePosRate 1.0. This is the error
// class the rerank floor must fix.
func TestAbstention_CosineFloorGameable(t *testing.T) {
	conf := core.DefaultConfidence(bgeSmall)
	absent := []Probe{{NumHits: 1, Top1: 0.72}, {NumHits: 1, Top1: 0.70}}
	r := Abstention(nil, absent, conf)
	require.Equal(t, 1.0, r.FalsePosRate, "fakes above the cosine floor are not abstained")
}

// With a CALIBRATED rerank floor, the same separation (present ~0.73, absent ~0.55 on the
// cross-encoder) is cleanly caught: FPR and FNR both 0.
func TestAbstention_RerankFloorSeparates(t *testing.T) {
	conf := core.DefaultConfidence(bgeSmall)
	conf.RerankFloor = 0.62 // calibrated (default 0.5 would let 0.55 through)
	present := []Probe{{NumHits: 1, Top1: 0.73, Reranked: true}}
	absent := []Probe{{NumHits: 1, Top1: 0.55, Reranked: true}}
	r := Abstention(present, absent, conf)
	require.Equal(t, "rerank", r.RankedBy)
	require.Equal(t, 0.0, r.FalseNegRate, "present rerank score above the calibrated floor")
	require.Equal(t, 0.0, r.FalsePosRate, "absent rerank score below the calibrated floor → weak")
}
