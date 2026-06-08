// Package cluster is a no-LLM topic-clustering primitive used by
// engine.ScopeStats / `ioc scope-advise` to advise when a scope holds
// multiple topics (see docs/SCOPE_POLICY.md).
//
// Threshold-based connected components are chosen for being deterministic and
// parameter-light: one tau controls "how similar is close enough", there is no
// k to guess and no random seeding. Vectors are grouped by single-linkage over
// all O(n²) pairs — fine at slice scale (tens to low hundreds of artifacts per
// scope). Cosine similarity is computed from raw (non-normalized) float32
// vectors, matching the contract of engine embeddings.
//
// Leaf package: imports only stdlib (math, sort).
package cluster

import (
	"math"
	"sort"
)

// Cosine returns the cosine similarity between a and b, computed in float64.
//
// It returns 0 when: len(a) != len(b), either slice is empty, or either
// vector has zero norm (avoids NaN). Vectors need not be pre-normalized.
func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// MeanPairwiseCosine returns the average cosine similarity over all unordered
// pairs (i < j) in vecs. Returns 0 when len(vecs) < 2.
func MeanPairwiseCosine(vecs [][]float32) float64 {
	n := len(vecs)
	if n < 2 {
		return 0
	}
	var sum float64
	var count int
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			sum += Cosine(vecs[i], vecs[j])
			count++
		}
	}
	return sum / float64(count)
}

// ThresholdComponents groups vector indices 0..n-1 into connected components
// using the undirected graph where i–j has an edge iff
// Cosine(vecs[i], vecs[j]) >= tau.
//
// Determinism guarantees:
//   - each component's indices are sorted ascending;
//   - components are ordered by their smallest index ascending.
//
// Edge cases:
//   - len(vecs)==0 → empty (len 0) slice.
//   - single vector → [[0]].
//   - isolated vector (no edge to any other) → its own singleton component.
//   - zero-norm vector has Cosine 0 with everything; unless tau <= 0 it forms
//     a singleton (no special-casing needed).
func ThresholdComponents(vecs [][]float32, tau float64) [][]int {
	n := len(vecs)
	if n == 0 {
		return [][]int{}
	}

	// Union-Find with path compression and union-by-rank.
	parent := make([]int, n)
	rank := make([]int, n)
	for i := range parent {
		parent[i] = i
	}

	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x]) // path compression
		}
		return parent[x]
	}

	union := func(x, y int) {
		rx, ry := find(x), find(y)
		if rx == ry {
			return
		}
		switch {
		case rank[rx] < rank[ry]:
			parent[rx] = ry
		case rank[rx] > rank[ry]:
			parent[ry] = rx
		default:
			parent[ry] = rx
			rank[rx]++
		}
	}

	// Build edges for all pairs with cosine >= tau.
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if Cosine(vecs[i], vecs[j]) >= tau {
				union(i, j)
			}
		}
	}

	// Collect indices by root.
	groups := make(map[int][]int, n)
	for i := 0; i < n; i++ {
		r := find(i)
		groups[r] = append(groups[r], i)
	}

	// Gather roots and sort them by their smallest member (which, because we
	// iterated 0..n-1 in order, is already the smallest index in each group).
	roots := make([]int, 0, len(groups))
	for r := range groups {
		roots = append(roots, r)
	}
	sort.Slice(roots, func(a, b int) bool {
		// Each group's indices are already in ascending order because we added
		// them in order; groups[r][0] is the smallest index in the component.
		return groups[roots[a]][0] < groups[roots[b]][0]
	})

	out := make([][]int, len(roots))
	for i, r := range roots {
		comp := groups[r]
		sort.Ints(comp) // guarantee sorted (should already be, but be explicit)
		out[i] = comp
	}
	return out
}
