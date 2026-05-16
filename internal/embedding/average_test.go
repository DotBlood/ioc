package embedding

import (
	"math"
	"testing"
)

func TestWeightedAverage_EqualWeights(t *testing.T) {
	v1 := []float32{1, 0, 0}
	v2 := []float32{0, 1, 0}
	vectors := [][]float32{v1, v2}
	weights := []int{1, 1}

	centroid, err := WeightedAverage(vectors, weights)
	if err != nil {
		t.Fatalf("WeightedAverage: %v", err)
	}

	// Equal weights → plain mean → (0.5, 0.5, 0), then normalized.
	// Normalized: each component ≈ 0.707
	if len(centroid) != 3 {
		t.Fatalf("expected dim 3, got %d", len(centroid))
	}

	// Check normalized.
	var sumSq float64
	for _, v := range centroid {
		sumSq += float64(v) * float64(v)
	}
	norm := math.Sqrt(sumSq)
	if norm < 0.999 || norm > 1.001 {
		t.Errorf("norm = %f, want ≈1.0", norm)
	}

	// Both components should be positive after normalize.
	if centroid[0] <= 0 || centroid[1] <= 0 {
		t.Errorf("expected positive components, got %v", centroid)
	}
}

func TestWeightedAverage_DifferentWeights(t *testing.T) {
	v1 := []float32{1, 0}
	v2 := []float32{0, 2}
	vectors := [][]float32{v1, v2}
	weights := []int{3, 1} // v1 weighted 3x

	centroid, err := WeightedAverage(vectors, weights)
	if err != nil {
		t.Fatalf("WeightedAverage: %v", err)
	}

	// Before normalize: sum = (3*1+1*0, 3*0+1*2)/4 = (0.75, 0.5)
	// After normalize: should be unit length with first component dominant.
	var sumSq float64
	for _, v := range centroid {
		sumSq += float64(v) * float64(v)
	}
	norm := math.Sqrt(sumSq)
	if norm < 0.999 || norm > 1.001 {
		t.Errorf("norm = %f, want ≈1.0", norm)
	}

	// First component should dominate (weighted 3:1).
	if centroid[0] < centroid[1] {
		t.Errorf("first component %f should dominate second %f", centroid[0], centroid[1])
	}
}

func TestWeightedAverage_Normalized(t *testing.T) {
	tests := [][]float32{
		{1, 0, 0, 0},
		{0.5, 0.5, 0.5, 0.5},
		{3, -1, 2, -4},
		{0.01, 0.02, -0.01, 0.03},
	}

	for _, v := range tests {
		vectors := [][]float32{v}
		weights := []int{1}
		centroid, err := WeightedAverage(vectors, weights)
		if err != nil {
			t.Fatalf("WeightedAverage(%v): %v", v, err)
		}

		var sumSq float64
		for _, x := range centroid {
			sumSq += float64(x) * float64(x)
		}
		norm := math.Sqrt(sumSq)
		if norm < 0.999 || norm > 1.001 {
			t.Errorf("norm = %f for input %v, want ≈1.0", norm, v)
		}
	}
}

func TestWeightedAverage_AllZeroWeights(t *testing.T) {
	v := []float32{1, 2, 3}
	vectors := [][]float32{v}
	weights := []int{0}

	centroid, err := WeightedAverage(vectors, weights)
	if err != nil {
		t.Fatalf("WeightedAverage: %v", err)
	}

	for _, x := range centroid {
		if x != 0 {
			t.Errorf("expected zero vector, got %v", centroid)
			break
		}
	}
}

func TestWeightedAverage_EmptyInput(t *testing.T) {
	_, err := WeightedAverage(nil, nil)
	if err == nil {
		t.Error("expected error for nil vectors")
	}

	_, err = WeightedAverage([][]float32{}, []int{})
	if err == nil {
		t.Error("expected error for empty vectors")
	}
}

func TestWeightedAverage_MismatchedCounts(t *testing.T) {
	vectors := [][]float32{{1, 2}}
	weights := []int{1, 2} // 1 vector but 2 weights
	_, err := WeightedAverage(vectors, weights)
	if err == nil {
		t.Error("expected error for mismatched counts")
	}
}

func TestWeightedAverage_NegativeWeight(t *testing.T) {
	vectors := [][]float32{{1, 2}}
	weights := []int{-1}
	_, err := WeightedAverage(vectors, weights)
	if err == nil {
		t.Error("expected error for negative weight")
	}
}

func TestWeightedAverage_EmptyVector(t *testing.T) {
	vectors := [][]float32{{}}
	weights := []int{1}
	_, err := WeightedAverage(vectors, weights)
	if err == nil {
		t.Error("expected error for empty vector")
	}
}

func TestWeightedAverage_NanVector(t *testing.T) {
	vectors := [][]float32{{float32(math.NaN()), 1, 2}}
	weights := []int{1}
	_, err := WeightedAverage(vectors, weights)
	if err != ErrCorruptedEmbedding {
		t.Errorf("want ErrCorruptedEmbedding for NaN, got %v", err)
	}
}

func TestWeightedAverage_InfVector(t *testing.T) {
	vectors := [][]float32{{float32(math.Inf(1)), 1, 2}}
	weights := []int{1}
	_, err := WeightedAverage(vectors, weights)
	if err != ErrCorruptedEmbedding {
		t.Errorf("want ErrCorruptedEmbedding for Inf, got %v", err)
	}
}

func TestWeightedAverage_DimensionMismatchEarly(t *testing.T) {
	vectors := [][]float32{
		{1, 2, 3},
		{4, 5}, // wrong dim
	}
	weights := []int{1, 1}
	_, err := WeightedAverage(vectors, weights)
	if err != ErrDimensionMismatch {
		t.Errorf("want ErrDimensionMismatch, got %v", err)
	}
}

func TestWeightedAverage_Determinism(t *testing.T) {
	vectors := [][]float32{
		{0.1, 0.2, 0.3},
		{0.4, 0.5, 0.6},
		{0.7, 0.8, 0.9},
	}
	weights := []int{5, 3, 2}

	r1, _ := WeightedAverage(vectors, weights)
	r2, _ := WeightedAverage(vectors, weights)

	for i := range r1 {
		if r1[i] != r2[i] {
			t.Errorf("determinism broken at index %d: %f vs %f", i, r1[i], r2[i])
			break
		}
	}
}
