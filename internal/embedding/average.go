package embedding

import (
	"errors"
	"math"
)

// WeightedAverage computes the length-weighted centroid of embedding vectors.
//
// Formula: E = Σ(w_i × v_i) / Σ(w_i)
//
//	where w_i = token count of child i
//	      v_i = embedding vector of child i
//
// Output is normalized to unit length (MUST for cosine retrieval stability).
//
// Numerical stability:
//   - accumulation uses float64 internally
//   - final vector is float32 for storage efficiency
//
// Invariant: MUST be deterministic.
// Same vectors + same weights + same ordering → identical result.
//
// Embeddings containing NaN or Inf are invalid and skipped (ErrCorruptedEmbedding).
func WeightedAverage(vectors [][]float32, weights []int) ([]float32, error) {
	if len(vectors) == 0 {
		return nil, ErrInvalidInput
	}
	if len(vectors) != len(weights) {
		return nil, errors.New("embedding: vector/weight count mismatch")
	}

	// Validate all vectors before any allocation.
	dim := len(vectors[0])
	if dim == 0 {
		return nil, errors.New("embedding: empty vector")
	}
	for _, vec := range vectors {
		if len(vec) != dim {
			return nil, ErrDimensionMismatch
		}
		for _, v := range vec {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return nil, ErrCorruptedEmbedding
			}
		}
	}
	for _, w := range weights {
		if w < 0 {
			return nil, errors.New("embedding: negative weight")
		}
	}

	// Allocation only after successful validation.
	result := make([]float64, dim)
	var totalWeight int

	for i, vec := range vectors {
		w := weights[i]
		for j := range vec {
			result[j] += float64(vec[j]) * float64(w)
		}
		totalWeight += w
	}

	if totalWeight == 0 {
		out := make([]float32, dim)
		return out, nil
	}

	invTotal := 1.0 / float64(totalWeight)
	out := make([]float32, dim)
	for j := range result {
		out[j] = float32(result[j] * invTotal)
	}

	normalize(out)
	return out, nil
}

// normalize normalizes v to unit length in-place.
//
// Uses float64 accumulation for numerical stability.
// Final vector remains float32 for storage efficiency.
//
// Without normalization on deep aggregation chains: ANN distance drift,
// cross-level vector space inconsistency, floating-point error accumulation.
func normalize(v []float32) {
	var sumSq float64
	for _, x := range v {
		sumSq += float64(x) * float64(x)
	}
	if sumSq == 0 {
		return
	}
	invNorm := float32(1.0 / math.Sqrt(sumSq))
	for i := range v {
		v[i] *= invNorm
	}
}
