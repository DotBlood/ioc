package cluster

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// ── Cosine ────────────────────────────────────────────────────────────────────

func TestCosine(t *testing.T) {
	const eps = 1e-6

	tests := []struct {
		name string
		a, b []float32
		want float64
	}{
		{
			name: "identical unit vectors",
			a:    []float32{1, 0, 0},
			b:    []float32{1, 0, 0},
			want: 1.0,
		},
		{
			name: "orthogonal unit vectors",
			a:    []float32{1, 0},
			b:    []float32{0, 1},
			want: 0.0,
		},
		{
			name: "opposite unit vectors",
			a:    []float32{1, 0},
			b:    []float32{-1, 0},
			want: -1.0,
		},
		{
			name: "non-normalized parallel vectors — proves normalization",
			// [2,0] and [5,0] point in the same direction; cosine must be 1.0
			// even though neither is a unit vector.
			a:    []float32{2, 0},
			b:    []float32{5, 0},
			want: 1.0,
		},
		{
			name: "length mismatch → 0",
			a:    []float32{1, 0},
			b:    []float32{1, 0, 0},
			want: 0.0,
		},
		{
			name: "empty slices → 0",
			a:    []float32{},
			b:    []float32{},
			want: 0.0,
		},
		{
			name: "zero vector a → 0",
			a:    []float32{0, 0},
			b:    []float32{1, 0},
			want: 0.0,
		},
		{
			name: "zero vector b → 0",
			a:    []float32{1, 0},
			b:    []float32{0, 0},
			want: 0.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Cosine(tc.a, tc.b)
			require.InDelta(t, tc.want, got, eps)
		})
	}
}

// ── MeanPairwiseCosine ────────────────────────────────────────────────────────

func TestMeanPairwiseCosine(t *testing.T) {
	const eps = 1e-6

	t.Run("empty → 0", func(t *testing.T) {
		require.Equal(t, 0.0, MeanPairwiseCosine(nil))
	})

	t.Run("single vector → 0", func(t *testing.T) {
		require.Equal(t, 0.0, MeanPairwiseCosine([][]float32{{1, 0}}))
	})

	t.Run("two identical unit vectors → 1.0", func(t *testing.T) {
		vecs := [][]float32{{1, 0}, {1, 0}}
		require.InDelta(t, 1.0, MeanPairwiseCosine(vecs), eps)
	})

	t.Run("known three-vector set", func(t *testing.T) {
		// vecs: [1,0], [0,1], [1,1]/sqrt(2)
		// pairs:
		//   (0,1): cos([1,0],[0,1])           = 0.0
		//   (0,2): cos([1,0],[1,1]/sqrt(2))   = 1/sqrt(2) ≈ 0.7071068
		//   (1,2): cos([0,1],[1,1]/sqrt(2))   = 1/sqrt(2) ≈ 0.7071068
		// mean = (0 + 1/√2 + 1/√2) / 3 = (2/√2)/3 = √2/3 ≈ 0.4714045
		inv := float32(1.0 / math.Sqrt2)
		vecs := [][]float32{
			{1, 0},
			{0, 1},
			{inv, inv},
		}
		want := math.Sqrt2 / 3.0
		require.InDelta(t, want, MeanPairwiseCosine(vecs), eps)
	})
}

// ── ThresholdComponents ───────────────────────────────────────────────────────

func TestThresholdComponents(t *testing.T) {
	const eps = 1e-6

	t.Run("empty → len 0 slice", func(t *testing.T) {
		out := ThresholdComponents(nil, 0.9)
		require.Len(t, out, 0)
	})

	t.Run("single vector → [[0]]", func(t *testing.T) {
		out := ThresholdComponents([][]float32{{1, 0}}, 0.9)
		require.Equal(t, [][]int{{0}}, out)
	})

	t.Run("two near-parallel vectors — below tau → one component", func(t *testing.T) {
		// cos([1,0],[0.99,0.141]) ≈ 0.99 — tau 0.98 is below that.
		a := []float32{1, 0}
		b := []float32{0.99, 0.141} // ~cos(8°) ≈ 0.990
		tau := 0.98
		got := Cosine(a, b)
		require.Greater(t, got, tau, "pre-condition: cosine must be above tau")
		out := ThresholdComponents([][]float32{a, b}, tau)
		require.Equal(t, [][]int{{0, 1}}, out)
	})

	t.Run("two near-parallel vectors — above tau → two singletons", func(t *testing.T) {
		a := []float32{1, 0}
		b := []float32{0.99, 0.141}
		tau := 0.995 // above their cosine
		got := Cosine(a, b)
		require.Less(t, got, tau, "pre-condition: cosine must be below tau")
		out := ThresholdComponents([][]float32{a, b}, tau)
		require.Equal(t, [][]int{{0}, {1}}, out)
	})

	t.Run("two clear topics, tau=0.8 → two components with right membership", func(t *testing.T) {
		// Topic A: vectors near [1,0]
		//   vA0 = [1, 0]        vA1 = [0.99, 0.01]  cos ≈ 0.9999
		// Topic B: vectors near [0,1]
		//   vB0 = [0, 1]        vB1 = [0.01, 0.99]  cos ≈ 0.9999
		// Cross-topic (A0,B0): cos = 0  → well below 0.8
		vA0 := []float32{1, 0}
		vA1 := []float32{0.99, 0.01}
		vB0 := []float32{0, 1}
		vB1 := []float32{0.01, 0.99}
		vecs := [][]float32{vA0, vA1, vB0, vB1} // indices 0,1,2,3
		tau := 0.8

		// Verify pre-conditions.
		require.Greater(t, Cosine(vA0, vA1), tau, "intra-A cosine must exceed tau")
		require.Greater(t, Cosine(vB0, vB1), tau, "intra-B cosine must exceed tau")
		require.Less(t, Cosine(vA0, vB0), tau, "cross-topic cosine must be below tau")

		out := ThresholdComponents(vecs, tau)
		require.Len(t, out, 2)
		require.Equal(t, [][]int{{0, 1}, {2, 3}}, out)
	})

	t.Run("transitivity via single-linkage: A-B and B-C connected, A-C not → one component", func(t *testing.T) {
		// 2-D unit vectors at angles 0°, 30°, 70°.
		//   A = [cos 0°,  sin 0°]  = [1,       0      ]
		//   B = [cos 30°, sin 30°] = [√3/2,    1/2    ] ≈ [0.866025, 0.5]
		//   C = [cos 70°, sin 70°]                       ≈ [0.342020, 0.939693]
		//
		// Cosines (angle between the vectors):
		//   cos(A,B) = cos(30°) ≈ 0.866025   ← above tau=0.75
		//   cos(B,C) = cos(40°) ≈ 0.766044   ← above tau=0.75
		//   cos(A,C) = cos(70°) ≈ 0.342020   ← below tau=0.75
		//
		// So with tau=0.75: A–B edge ✓, B–C edge ✓, A–C edge ✗.
		// Connected components via union-find: {A,B,C} → [[0,1,2]].
		A := []float32{1, 0}
		B := []float32{float32(math.Cos(math.Pi / 6)), float32(math.Sin(math.Pi / 6))}           // 30°
		C := []float32{float32(math.Cos(7 * math.Pi / 18)), float32(math.Sin(7 * math.Pi / 18))} // 70°
		tau := 0.75

		cosAB := Cosine(A, B)
		cosBC := Cosine(B, C)
		cosAC := Cosine(A, C)
		t.Logf("cos(A,B)=%.6f  cos(B,C)=%.6f  cos(A,C)=%.6f  tau=%.2f", cosAB, cosBC, cosAC, tau)

		require.Greater(t, cosAB, tau, "pre-condition: cos(A,B) must be >= tau")
		require.Greater(t, cosBC, tau, "pre-condition: cos(B,C) must be >= tau")
		require.Less(t, cosAC, tau, "pre-condition: cos(A,C) must be < tau")
		require.InDelta(t, math.Cos(math.Pi/6), cosAB, eps)      // cos(30°)
		require.InDelta(t, math.Cos(40*math.Pi/180), cosBC, eps) // cos(40°)
		require.InDelta(t, math.Cos(70*math.Pi/180), cosAC, eps) // cos(70°)

		out := ThresholdComponents([][]float32{A, B, C}, tau)
		require.Equal(t, [][]int{{0, 1, 2}}, out, "transitivity: A–B–C must form one component")
	})

	t.Run("determinism: indices within component and component order are sorted", func(t *testing.T) {
		// Three isolated unit-basis vectors — each is its own component.
		// Regardless of internal map iteration order the output must be [[0],[1],[2]].
		vecs := [][]float32{
			{1, 0, 0},
			{0, 1, 0},
			{0, 0, 1},
		}
		out := ThresholdComponents(vecs, 0.5)
		require.Equal(t, [][]int{{0}, {1}, {2}}, out)
	})
}
