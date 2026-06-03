package embed

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func cosine(a, b []float32) float64 {
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot // inputs are normalized
}

func TestMockEmbedder_DeterministicAndLexical(t *testing.T) {
	m := NewMockEmbedder(384)
	ctx := context.Background()

	v, err := m.Embed(ctx, []string{
		"the tool head should be metal",
		"the tool head must be made of metal",
		"completely unrelated banana topic",
	})
	require.NoError(t, err)
	require.Len(t, v, 3)
	require.Len(t, v[0], 384)

	// Deterministic: same text -> same vector.
	again, err := m.Embed(ctx, []string{"the tool head should be metal"})
	require.NoError(t, err)
	require.Equal(t, v[0], again[0])

	// Lexical proxy: shared-token texts are closer than unrelated ones.
	require.Greater(t, cosine(v[0], v[1]), cosine(v[0], v[2]))
}

func TestMockEmbedder_EmptyInput(t *testing.T) {
	_, err := NewMockEmbedder(384).Embed(context.Background(), nil)
	require.ErrorIs(t, err, ErrEmptyInput)
}
