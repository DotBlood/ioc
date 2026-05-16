package embedding

import (
	"context"
	"math"
	"time"
)

// MockConfig controls MockEmbedder behavior.
type MockConfig struct {
	Dimension int    // default 384
	ModelName string // default "mock-v1"
	Seed      int64  // default 42
}

// DefaultMockConfig returns sensible defaults for testing.
func DefaultMockConfig() MockConfig {
	return MockConfig{
		Dimension: 384,
		ModelName: "mock-v1",
		Seed:      42,
	}
}

// MockEmbedder is a deterministic fake embedder for testing.
//
// Properties:
//   - Deterministic: same input → same vector (xxhash → splitmix64)
//   - Normalized: each vector has unit length
//   - Dimension-fixed: always returns Dims() == config.Dimension
//   - Stable across runs: seed-based, no global math/rand
type MockEmbedder struct {
	config MockConfig
}

// NewMockEmbedder creates a new MockEmbedder.
func NewMockEmbedder(config MockConfig) *MockEmbedder {
	return &MockEmbedder{config: config}
}

// Embed converts texts to deterministic mock embedding vectors.
// Empty batch → ErrInvalidInput. Empty string → valid embedding.
func (m *MockEmbedder) Embed(_ context.Context, texts []string) (*Batch, error) {
	if len(texts) == 0 {
		return nil, ErrInvalidInput
	}

	vectors := make([]Vector, len(texts))
	for i, text := range texts {
		vec := m.generateVector(text)
		vectors[i] = Vector{
			Data:       vec,
			TokenCount: approximateTokens(text),
		}
	}

	return &Batch{
		Vectors:   vectors,
		Dimension: m.config.Dimension,
		Model:     m.config.ModelName,
		CreatedAt: time.Now(),
	}, nil
}

// Dims returns the embedding dimension.
// Invariant: MUST remain constant for lifetime.
func (m *MockEmbedder) Dims() int { return m.config.Dimension }

// Model returns the model identifier.
func (m *MockEmbedder) Model() string { return m.config.ModelName }

// generateVector produces a deterministic normalized vector from a text input.
// Algorithm: xxhash(text) → seed splitmix64 → generate random values → normalize.
func (m *MockEmbedder) generateVector(text string) []float32 {
	seed := hashString(text, uint64(m.config.Seed))
	rng := newSplitmix64(seed)

	vec := make([]float32, m.config.Dimension)
	for i := range vec {
		// Generate float32 in [-1, 1]
		vec[i] = float32(rng.Uint64()>>40)*twoMinus32 - 1.0
	}

	// Normalize to unit length.
	var sumSq float64
	for _, v := range vec {
		sumSq += float64(v) * float64(v)
	}
	norm := float32(math.Sqrt(sumSq))
	if norm > 0 {
		for i := range vec {
			vec[i] /= norm
		}
	}

	return vec
}

// hashString computes a 64-bit hash of a string using FNV-1a with a seed.
func hashString(s string, seed uint64) uint64 {
	h := seed
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211 // FNV-1a prime
	}
	return h
}

// approximateTokens returns a rough token count for a text.
func approximateTokens(text string) int {
	if text == "" {
		return 0
	}
	// Very rough: ~4 chars per token on average.
	return len(text) / 4
}

// ============================================================
// splitmix64 — deterministic, lock-free, fast PRNG
// ============================================================

type splitmix64 struct {
	state uint64
}

func newSplitmix64(seed uint64) *splitmix64 {
	return &splitmix64{state: seed}
}

func (s *splitmix64) Uint64() uint64 {
	s.state += 0x9e3779b97f4a7c15
	z := s.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// twoMinus32 is 2^-32 for float32 generation.
const twoMinus32 = 1.0 / (1 << 32)
