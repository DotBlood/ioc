package iocfmt

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
)

// HitOut surfaces ingested provenance; QueryOut raises untrusted_content when any
// hit is ingested, and omits it when all hits are authored (V17).
func TestOut_TrustSurfacing(t *testing.T) {
	authored := core.Hit{Artifact: core.NewID(), Summary: "authored", Score: 0.9}
	ingested := core.Hit{Artifact: core.NewID(), Summary: "from a file", Score: 0.8,
		Meta: map[string]string{"trust": core.TrustIngested, "path": "f.go"}}

	require.Nil(t, HitOut(authored)["trust"])
	require.Equal(t, core.TrustIngested, HitOut(ingested)["trust"])

	clean := QueryOut(core.NewID(), []core.Hit{authored}, core.DefaultConfidence("mock-bow"))
	_, hasFlag := clean["untrusted_content"]
	require.False(t, hasFlag, "no untrusted_content when all hits authored")

	mixed := QueryOut(core.NewID(), []core.Hit{authored, ingested}, core.DefaultConfidence("mock-bow"))
	require.Equal(t, true, mixed["untrusted_content"])
}

// bgeSmall is an embedder model whose ConfidenceFloor is a non-zero ~0.68, so the
// cosine weak_match threshold is exercised (mock-bow's floor is 0 and never flags).
const bgeSmall = "BAAI/bge-small-en-v1.5"

// QueryOut on the COSINE path: weak_match is an absolute floor on the cosine top
// score, margin is top1-top2, and ranked_by names the cosine signal. (The rerank
// path is covered in engine/rerank_test.go.)
func TestQueryOut_CosinePath(t *testing.T) {
	floor := core.ConfidenceFloor(bgeSmall) // ~0.68

	t.Run("no hits → weak, zeroed", func(t *testing.T) {
		out := QueryOut(core.NewID(), nil, core.DefaultConfidence(bgeSmall))
		require.Equal(t, true, out["weak_match"])
		require.Equal(t, "cosine", out["ranked_by"])
		require.Equal(t, 0.0, out["top_score"])
		require.Equal(t, 0.0, out["margin"])
		require.Empty(t, out["hits"])
	})

	t.Run("single strong hit → not weak, margin 0", func(t *testing.T) {
		hits := []core.Hit{{Artifact: core.NewID(), Score: 0.82}}
		out := QueryOut(core.NewID(), hits, core.DefaultConfidence(bgeSmall))
		require.Equal(t, false, out["weak_match"])
		require.InDelta(t, 0.82, out["top_score"].(float64), 1e-9)
		require.Equal(t, 0.0, out["margin"], "margin needs ≥2 hits")
		require.Equal(t, "cosine", out["ranked_by"])
	})

	t.Run("single hit below floor → weak", func(t *testing.T) {
		hits := []core.Hit{{Artifact: core.NewID(), Score: floor - 0.05}}
		out := QueryOut(core.NewID(), hits, core.DefaultConfidence(bgeSmall))
		require.Equal(t, true, out["weak_match"])
	})

	t.Run("two hits → margin = top1-top2", func(t *testing.T) {
		hits := []core.Hit{
			{Artifact: core.NewID(), Score: 0.81},
			{Artifact: core.NewID(), Score: 0.66},
		}
		out := QueryOut(core.NewID(), hits, core.DefaultConfidence(bgeSmall))
		require.Equal(t, false, out["weak_match"])
		require.InDelta(t, 0.81, out["top_score"].(float64), 1e-9)
		require.InDelta(t, 0.15, out["margin"].(float64), 1e-9)
	})
}

// The floor is per-embedder: the SAME top score is weak under a high floor and
// strong under a low one — guards against a hardcoded threshold.
func TestQueryOut_FloorIsPerEmbedder(t *testing.T) {
	hits := []core.Hit{{Artifact: core.NewID(), Score: 0.60}}
	// bge-small floor ~0.68 → 0.60 is weak; default floor 0.5 → 0.60 is strong.
	require.Greater(t, core.ConfidenceFloor(bgeSmall), 0.60)
	require.Less(t, core.ConfidenceFloor("some-other-model"), 0.60)

	weak := QueryOut(core.NewID(), hits, core.DefaultConfidence(bgeSmall))
	strong := QueryOut(core.NewID(), hits, core.DefaultConfidence("some-other-model"))
	require.Equal(t, true, weak["weak_match"])
	require.Equal(t, false, strong["weak_match"])
}

// ScopeOut / ArtifactOut / HitOut emit optional fields only when present.
func TestShapeOut_OptionalFields(t *testing.T) {
	t.Run("ScopeOut omits zero parent/forked_from", func(t *testing.T) {
		root := ScopeOut(core.Scope{ID: core.NewID(), Role: core.RoleWorktree, Title: "root", Version: 1})
		_, hasParent := root["parent"]
		_, hasFork := root["forked_from"]
		require.False(t, hasParent)
		require.False(t, hasFork)

		child := ScopeOut(core.Scope{ID: core.NewID(), Parent: core.NewID(), ForkedFrom: core.NewID(), Role: core.RoleSession, Title: "c", Version: 2})
		require.NotEmpty(t, child["parent"])
		require.NotEmpty(t, child["forked_from"])
		require.Equal(t, 2, child["version"])
	})

	t.Run("ArtifactOut core fields", func(t *testing.T) {
		a := ArtifactOut(core.Artifact{ID: core.NewID(), Scope: core.NewID(), Kind: core.KindInsight, Tier: core.TierWorktree, Published: true})
		require.Equal(t, true, a["published"])
		require.NotEmpty(t, a["id"])
		require.NotEmpty(t, a["scope"])
	})

	t.Run("HitOut optional fields", func(t *testing.T) {
		plain := HitOut(core.Hit{Artifact: core.NewID(), Summary: "s", Score: 0.7})
		for _, k := range []string{"rerank_score", "superseded_by", "path", "lines", "trust", "content"} {
			_, has := plain[k]
			require.False(t, has, "omit %q when absent", k)
		}

		rr := 0.91
		rich := HitOut(core.Hit{
			Artifact: core.NewID(), Summary: "s", Score: 0.7, RerankScore: &rr,
			SupersededBy: core.NewID(), Content: []byte("raw"),
			Meta: map[string]string{"path": "f.go", "lines": "1-9"},
		})
		require.InDelta(t, 0.91, rich["rerank_score"].(float64), 1e-9)
		require.NotEmpty(t, rich["superseded_by"])
		require.Equal(t, "f.go", rich["path"])
		require.Equal(t, "1-9", rich["lines"])
		require.Equal(t, "raw", rich["content"])
	})
}
