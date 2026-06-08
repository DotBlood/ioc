package embed

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// MockEmbedder is a deterministic, offline embedder using the hashing trick
// (a random-projection bag-of-words). Texts that share tokens get higher cosine
// similarity, so retrieval is meaningful enough to validate the pipeline in CI.
//
// IMPORTANT: this is a lexical proxy, NOT true semantics. Real "wall" numbers
// must be gathered with a real embedder (see HTTPEmbedder). With MockEmbedder
// the eval validates the plumbing, not summary quality.
type MockEmbedder struct {
	dims int
}

// NewMockEmbedder creates a deterministic embedder with the given dimension.
func NewMockEmbedder(dims int) *MockEmbedder {
	if dims <= 0 {
		dims = 384
	}
	return &MockEmbedder{dims: dims}
}

func (m *MockEmbedder) Dims() int     { return m.dims }
func (m *MockEmbedder) Model() string { return "mock-bow" }

// EmbedQuery is identical to Embed for the mock (no instruction tuning).
func (m *MockEmbedder) EmbedQuery(ctx context.Context, texts []string) ([][]float32, error) {
	return m.Embed(ctx, texts)
}

// Embed produces one normalized vector per text via hashed bag-of-words.
func (m *MockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, ErrEmptyInput
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = m.vector(t)
	}
	return out, nil
}

func (m *MockEmbedder) vector(text string) []float32 {
	vec := make([]float32, m.dims)
	tokens := tokenize(text)
	if len(tokens) == 0 {
		// Fallback: project the whole string to one slot so empty/symbol-only
		// texts still get a stable non-zero vector.
		h := hashString(text)
		vec[h%uint64(m.dims)] = 1
		return vec
	}
	for _, tok := range tokens {
		h := hashString(tok)
		idx := h % uint64(m.dims)
		if h&(1<<63) != 0 {
			vec[idx] -= 1
		} else {
			vec[idx] += 1
		}
	}
	normalize(vec)
	return vec
}

func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func hashString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func normalize(vec []float32) {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	if sum == 0 {
		return
	}
	inv := float32(1.0 / math.Sqrt(sum))
	for i := range vec {
		vec[i] *= inv
	}
}
