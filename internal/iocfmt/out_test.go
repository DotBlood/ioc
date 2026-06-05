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

	clean := QueryOut(core.NewID(), []core.Hit{authored}, "mock-bow")
	_, hasFlag := clean["untrusted_content"]
	require.False(t, hasFlag, "no untrusted_content when all hits authored")

	mixed := QueryOut(core.NewID(), []core.Hit{authored, ingested}, "mock-bow")
	require.Equal(t, true, mixed["untrusted_content"])
}
