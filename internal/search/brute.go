// Package search is a leaf package: brute-force cosine search over a set of
// vectors keyed by string id. It has no dependency on core, storage, or engine.
package search

import "sort"

// Result is one search hit.
type Result struct {
	ID    string
	Score float64
}

// Set is an in-memory vector set for brute-force cosine search.
// Vectors are assumed normalized, so cosine similarity == dot product.
type Set struct {
	ids  []string
	vecs [][]float32
}

// New creates an empty set.
func New() *Set { return &Set{} }

// Add inserts a vector under an id.
func (s *Set) Add(id string, vec []float32) {
	s.ids = append(s.ids, id)
	s.vecs = append(s.vecs, vec)
}

// Len returns the number of vectors.
func (s *Set) Len() int { return len(s.ids) }

// Search returns the top-K results by descending score; ties broken by id ascending.
func (s *Set) Search(query []float32, topK int) []Result {
	if topK <= 0 {
		return nil
	}
	results := make([]Result, 0, len(s.ids))
	for i, vec := range s.vecs {
		results = append(results, Result{ID: s.ids[i], Score: dot(query, vec)})
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})
	if len(results) > topK {
		results = results[:topK]
	}
	return results
}

func dot(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

// Cosine returns the cosine similarity of two vectors. Vectors here are assumed
// normalized (the embedder returns unit vectors), so this is the dot product; a
// dimension mismatch scores 0 (consistent with Set.Search).
func Cosine(a, b []float32) float64 { return dot(a, b) }
