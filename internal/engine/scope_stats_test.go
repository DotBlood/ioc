package engine

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
)

func clusterSizes(s core.ScopeStats) []int {
	sizes := make([]int, 0, len(s.Clusters))
	for _, c := range s.Clusters {
		sizes = append(sizes, len(c))
	}
	sort.Ints(sizes)
	return sizes
}

func TestScopeStats_TwoTopicsRecommendsSplit(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(64))
	require.NoError(t, err)
	defer e.Close()

	// Two disjoint vocabularies → mock-bow embeds them near-orthogonally; within a
	// topic the summaries share most words → high cosine.
	s, _ := pushN(t, e,
		"garden soil plant water root",
		"garden soil plant water leaf",
		"garden soil plant water stem",
		"network socket packet protocol port",
		"network socket packet protocol byte",
		"network socket packet protocol frame",
	)

	// Explicit tau (mock ConfidenceFloor is 0); minArtifacts=4 so 6 clears the gate.
	st, err := e.ScopeStats(ctx, s.ID, 0.5, 4)
	require.NoError(t, err)
	require.Equal(t, 6, st.ArtifactCount)
	require.Equal(t, 6, st.EmbeddedCount)
	require.Equal(t, 2, st.ClusterCount, "two disjoint topics must form two clusters")
	require.Equal(t, []int{3, 3}, clusterSizes(st))
	require.Equal(t, "split", st.Recommendation)
	require.Greater(t, st.Dispersion, 0.0)
}

func TestScopeStats_SingleTopicOK(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(64))
	require.NoError(t, err)
	defer e.Close()

	s, _ := pushN(t, e,
		"garden soil plant water root",
		"garden soil plant water leaf",
		"garden soil plant water stem",
		"garden soil plant water seed",
		"garden soil plant water bloom",
	)
	st, err := e.ScopeStats(ctx, s.ID, 0.5, 4)
	require.NoError(t, err)
	require.Equal(t, 1, st.ClusterCount, "one cohesive topic must form a single cluster")
	require.Equal(t, "ok", st.Recommendation)
}

func TestScopeStats_TooSmall(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(64))
	require.NoError(t, err)
	defer e.Close()

	s, _ := pushN(t, e, "alpha one two", "beta three four")
	// Default min_artifacts (8) gates a 2-artifact scope regardless of clustering.
	st, err := e.ScopeStats(ctx, s.ID, 0.5, 0)
	require.NoError(t, err)
	require.Equal(t, "too_small", st.Recommendation)
}

func TestScopeStats_UnknownScope(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(64))
	require.NoError(t, err)
	defer e.Close()

	_, err = e.ScopeStats(ctx, core.ID{}, 0.5, 4)
	require.Error(t, err)
	require.True(t, errors.Is(err, core.ErrNotFound))
}
