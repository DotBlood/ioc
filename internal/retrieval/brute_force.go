package retrieval

import (
	"container/heap"
	"context"
	"sort"
	"sync"

	"github.com/DotBlood/ioc/internal/model"
)

// BruteForceIndex performs linear scan over all indexed vectors.
// O(N × dim) per search. Acceptable for ~50K vectors at 384 dims in v0.1.
type BruteForceIndex struct {
	mu      sync.RWMutex
	dims    int
	entries map[model.ID]IndexEntry
}

// NewBruteForceIndex creates a new brute-force index with the given dimension.
func NewBruteForceIndex(dims int) *BruteForceIndex {
	return &BruteForceIndex{
		dims:    dims,
		entries: make(map[model.ID]IndexEntry),
	}
}

// Search returns top-K results via linear scan.
// Results sorted by Score descending, then ID lexical ascending (tie-break).
func (idx *BruteForceIndex) Search(_ context.Context, query []float32, opts SearchOptions) ([]SearchResult, error) {
	if opts.TopK <= 0 {
		return nil, model.ErrInvalidArgument
	}
	if len(query) != idx.dims {
		return nil, model.ErrDimensionMismatch
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	h := &scoreHeap{}
	heap.Init(h)

	for _, entry := range idx.entries {
		score, err := dotProduct(query, entry.Vector)
		if err != nil {
			continue
		}
		if opts.MinScore > 0 && score < opts.MinScore {
			continue
		}

		if h.Len() < opts.TopK {
			heap.Push(h, SearchResult{ID: entry.ID, Score: score})
		} else if score > h.Peek().Score {
			(*h)[0] = SearchResult{ID: entry.ID, Score: score}
			heap.Fix(h, 0)
		}
	}

	results := make([]SearchResult, h.Len())
	for i := h.Len() - 1; i >= 0; i-- {
		results[i] = heap.Pop(h).(SearchResult)
	}

	// Tie-break: ID lexical ascending for equal scores.
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID.String() < results[j].ID.String()
	})

	return results, nil
}

// Upsert adds or updates entries. Replaces existing by ID.
// Returns ErrDimensionMismatch if any entry has wrong dimension.
func (idx *BruteForceIndex) Upsert(_ context.Context, entries []IndexEntry) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	for _, e := range entries {
		if len(e.Vector) != idx.dims {
			return model.ErrDimensionMismatch
		}
		idx.entries[e.ID] = e
	}
	return nil
}

// Delete removes entries by ID. Idempotent — missing IDs are ignored.
func (idx *BruteForceIndex) Delete(_ context.Context, ids []model.ID) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	for _, id := range ids {
		delete(idx.entries, id)
	}
	return nil
}

// Dimension returns the configured vector dimension.
func (idx *BruteForceIndex) Dimension() int { return idx.dims }

// Count returns the number of indexed vectors.
func (idx *BruteForceIndex) Count() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.entries)
}

// dotProduct computes the dot product of two vectors.
// For normalized vectors, dot product = cosine similarity.
func dotProduct(a, b []float32) (float64, error) {
	if len(a) != len(b) {
		return 0, model.ErrDimensionMismatch
	}
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum, nil
}

// scoreHeap implements heap.Interface for top-K selection (min-heap).
type scoreHeap []SearchResult

func (h scoreHeap) Len() int            { return len(h) }
func (h scoreHeap) Less(i, j int) bool  { return h[i].Score < h[j].Score }
func (h scoreHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *scoreHeap) Push(x interface{}) { *h = append(*h, x.(SearchResult)) }
func (h *scoreHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
func (h scoreHeap) Peek() SearchResult { return h[0] }
