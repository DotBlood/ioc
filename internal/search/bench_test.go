package search

import (
	"math"
	"math/rand"
	"strconv"
	"testing"
)

// randomUnitVecs builds n deterministic normalized vectors of the given dim, so
// cosine == dot (Set.Search's assumption). Seeded for run-to-run comparability.
func randomUnitVecs(n, dim int, seed int64) [][]float32 {
	rng := rand.New(rand.NewSource(seed))
	out := make([][]float32, n)
	for i := range out {
		v := make([]float32, dim)
		var ss float64
		for j := range v {
			x := float32(rng.NormFloat64())
			v[j] = x
			ss += float64(x) * float64(x)
		}
		inv := float32(1.0 / math.Sqrt(ss))
		for j := range v {
			v[j] *= inv
		}
		out[i] = v
	}
	return out
}

// BenchmarkSearch measures brute-force top-k cosine as the corpus grows. This is
// IOC's retrieval hot path (O(N·dim) dot products + an O(N log N) sort per query);
// it shows where the in-memory brute-force regime stops being cheap. dim=384 = bge-small.
func BenchmarkSearch(b *testing.B) {
	const dim, topK = 384, 5
	for _, n := range []int{100, 1_000, 10_000, 50_000, 100_000} {
		vecs := randomUnitVecs(n, dim, 1)
		s := New()
		for i, v := range vecs {
			s.Add(strconv.Itoa(i), v)
		}
		queries := randomUnitVecs(256, dim, 2)
		b.Run("n="+strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = s.Search(queries[i%len(queries)], topK)
			}
		})
	}
}
