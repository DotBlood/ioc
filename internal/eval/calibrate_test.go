package eval

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/engine"
)

// TestQuantile checks boundary values and interpolation on a known slice.
func TestQuantile(t *testing.T) {
	xs := []float64{0, 1, 2, 3, 4}
	require.Equal(t, 0.0, Quantile(xs, 0))
	require.Equal(t, 4.0, Quantile(xs, 1))
	require.InDelta(t, 2.0, Quantile(xs, 0.5), 1e-9)
	// Linear interpolation: q=0.25 → pos=1.0 → s[1] = 1.0 exactly
	require.InDelta(t, 1.0, Quantile(xs, 0.25), 1e-9)
	// Empty slice
	require.Equal(t, 0.0, Quantile(nil, 0.5))
	// Single element
	require.Equal(t, 7.0, Quantile([]float64{7}, 0.3))
}

// TestFloorFromScores checks that FloorFromScores returns the (1-coverage) quantile.
func TestFloorFromScores(t *testing.T) {
	// [0.5, 0.6, 0.7, 0.8, 0.9, 1.0] — 6 elements
	// coverage=0.9 → (1-0.9)=0.1 quantile → pos = 0.1*5 = 0.5 → interp s[0]..s[1]
	// = 0.5*(0.5) + 0.6*(0.5) = 0.55
	scores := []float64{0.5, 0.6, 0.7, 0.8, 0.9, 1.0}
	got := FloorFromScores(scores, 0.9)
	require.InDelta(t, 0.55, got, 1e-9)

	// coverage=1.0 → quantile(0) → minimum = 0.5
	require.InDelta(t, 0.5, FloorFromScores(scores, 1.0), 1e-9)

	// coverage=0.0 → quantile(1) → maximum = 1.0
	require.InDelta(t, 1.0, FloorFromScores(scores, 0.0), 1e-9)

	// Clamping: coverage>1 treated as 1
	require.InDelta(t, 0.5, FloorFromScores(scores, 1.5), 1e-9)
	// Clamping: coverage<0 treated as 0
	require.InDelta(t, 1.0, FloorFromScores(scores, -0.5), 1e-9)
}

// TestCalibrateRun_Smoke verifies that CalibrateRun returns a valid report against a
// mock-embedder engine with a tiny in-code WallSpec (no file I/O needed).
func TestCalibrateRun_Smoke(t *testing.T) {
	ctx := context.Background()
	e, err := engine.Open(ctx, t.TempDir(), embed.NewMockEmbedder(64))
	require.NoError(t, err)
	defer e.Close()

	spec := &WallSpec{
		Name: "cal-smoke",
		TopK: 5,
		Build: []Turn{
			{Op: "create_scope", ID: "wt", Parent: "root", Role: "worktree", Title: "test"},
			{Op: "push", Scope: "wt", As: "A1", Kind: "reasoning",
				Summary: "embeddings are stored in an append-only file with a count header and fsync for durability"},
			{Op: "push", Scope: "wt", As: "A2", Kind: "reasoning",
				Summary: "a long-lived daemon owns the store and serializes writes via a read-write lock"},
			{Op: "push", Scope: "wt", As: "A3", Kind: "reasoning",
				Summary: "IOC never calls a language model; an external agent authors the summary text"},
		},
		Questions: []WallQuestion{
			// Relevant — GoldRefs non-empty
			{ID: "q-durability", Scope: "wt", GoldRefs: []string{"A1"},
				Question: "how are embeddings made durable on disk",
				Gold:     "append-only file, count header, fsync"},
			{ID: "q-daemon", Scope: "wt", GoldRefs: []string{"A2"},
				Question: "how does the daemon handle concurrent access to the store",
				Gold:     "read-write lock; daemon is single owner"},
			{ID: "q-no-llm", Scope: "wt", GoldRefs: []string{"A3"},
				Question: "does IOC call a language model",
				Gold:     "no, external agent authors the summary"},
			// Absent — empty GoldRefs
			{ID: "q-absent", Scope: "wt", GoldRefs: []string{},
				Question: "what is the company vacation and sick-leave policy",
				Gold:     "INSUFFICIENT"},
		},
	}

	rep, err := CalibrateRun(ctx, e, spec, CalibrateOpts{Coverage: 0.9})
	require.NoError(t, err)
	require.Equal(t, 3, rep.RelevantN)
	require.Equal(t, 1, rep.AbsentN)
	require.GreaterOrEqual(t, rep.Floor, 0.0)
	require.LessOrEqual(t, rep.Floor, 1.0)
	require.Equal(t, 0.9, rep.Coverage)
	require.Len(t, rep.RelevantTops, 3)
	require.Len(t, rep.AbsentTops, 1)
	require.NotEmpty(t, rep.Model)
}

// TestCalibrateRun_ConfigRoundTrip verifies that the floor written by the caller
// via e.SetConfig is picked up by core.ResolveConfidence with Calibrated=true.
func TestCalibrateRun_ConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	e, err := engine.Open(ctx, t.TempDir(), embed.NewMockEmbedder(64))
	require.NoError(t, err)
	defer e.Close()

	// Write a known floor value for a synthetic model name.
	const model = "mock-bow"
	const wantFloor = 0.42
	err = e.SetConfig("conf.floor."+model, "0.42")
	require.NoError(t, err)

	// ResolveConfidence must return that floor and mark it as calibrated.
	conf := core.ResolveConfidence(model, e.Config)
	require.InDelta(t, wantFloor, conf.Floor, 1e-9)
	require.True(t, conf.Calibrated, "floor read from config must set Calibrated=true")

	// A model with no config entry must return a default (non-calibrated) floor.
	confDefault := core.ResolveConfidence("unknown-model-xyz", e.Config)
	require.False(t, confDefault.Calibrated)
}
