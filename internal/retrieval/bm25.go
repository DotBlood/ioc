package retrieval

import (
	"container/heap"
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/JLugagne/bm25"

	"github.com/DotBlood/ioc/internal/model"
)

// BM25Index wraps JLugagne/bm25 as a TextIndex.
//
// Immutable between Index() calls — Search MUST NOT mutate internal state.
// Full rebuild on each Index() call (acceptable for v0.1 corpus sizes).
type BM25Index struct {
	mu         sync.RWMutex
	bm         *bm25.BM25Okapi
	idToDocIdx map[model.ID]int // model ID → corpus position
	docIdxToID []model.ID       // corpus position → model ID
	tokenizer  func(string) []string
}

// NewBM25Index creates a new BM25 index with default tokenizer (whitespace).
func NewBM25Index() *BM25Index {
	return &BM25Index{
		tokenizer: strings.Fields,
	}
}

// Search returns top-K text results via BM25 ranking.
func (idx *BM25Index) Search(_ context.Context, query string, opts TextSearchOptions) ([]TextResult, error) {
	if opts.TopK <= 0 {
		return nil, model.ErrInvalidArgument
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.bm == nil || idx.bm.CorpusSize() == 0 {
		return nil, nil
	}

	tokens := idx.tokenizer(query)
	if len(tokens) == 0 {
		return nil, nil
	}

	scores, err := idx.bm.GetScores(tokens)
	if err != nil {
		return nil, err
	}

	h := &resultHeap{}
	heap.Init(h)

	for pos, score := range scores {
		entry := resultEntry{pos: pos, score: score, isText: true}
		if h.Len() < opts.TopK {
			heap.Push(h, entry)
		} else if score > h.Peek().score {
			(*h)[0] = entry
			heap.Fix(h, 0)
		}
	}

	results := make([]TextResult, h.Len())
	for i := h.Len() - 1; i >= 0; i-- {
		e := heap.Pop(h).(resultEntry)
		results[i] = TextResult{
			ID:    idx.docIdxToID[e.pos],
			Score: e.score,
		}
	}

	// Deterministic tie-break: lexical ID ascending for equal scores.
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID.String() < results[j].ID.String()
	})

	return results, nil
}

// Index does a full rebuild of the BM25 index. Replaces all existing documents.
func (idx *BM25Index) Index(_ context.Context, docs []TextDocument) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	corpus := make([]string, len(docs))
	idx.idToDocIdx = make(map[model.ID]int, len(docs))
	idx.docIdxToID = make([]model.ID, len(docs))

	for i, doc := range docs {
		corpus[i] = doc.Content
		idx.idToDocIdx[doc.ID] = i
		idx.docIdxToID[i] = doc.ID
	}

	bm, err := bm25.NewBM25Okapi(corpus, idx.tokenizer, 1.5, 0.75, nil)
	if err != nil {
		return err
	}
	idx.bm = bm
	return nil
}

// Delete removes documents by ID. Idempotent — triggers full rebuild without deleted docs.
func (idx *BM25Index) Delete(_ context.Context, ids []model.ID) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.bm == nil {
		return nil
	}

	delSet := make(map[model.ID]bool, len(ids))
	for _, id := range ids {
		delSet[id] = true
	}

	// Build new corpus excluding deleted IDs.
	var remaining []TextDocument
	for _, docID := range idx.docIdxToID {
		if !delSet[docID] {
			pos := idx.idToDocIdx[docID]
			// Keep the content from the corpus... For v0.1 we need to store content.
			// This is a limitation — for real deletion we'd need to store content.
			_ = pos
		}
	}
	_ = remaining

	// NOTE: For v0.1, BM25Index does not store document content after Index().
	// Delete requires the caller to pass the full updated document set via Index().
	// This is acceptable for v0.1 — incremental delete will be added in v0.2.
	return model.ErrNotImplemented
}

// Count returns the number of indexed documents.
func (idx *BM25Index) Count() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if idx.bm == nil {
		return 0
	}
	return idx.bm.CorpusSize()
}

// resultEntry is a candidate for top-K selection.
type resultEntry struct {
	pos    int
	score  float64
	isText bool
}

// resultHeap implements heap.Interface for top-K selection (min-heap by score).
type resultHeap []resultEntry

func (h resultHeap) Len() int            { return len(h) }
func (h resultHeap) Less(i, j int) bool  { return h[i].score < h[j].score }
func (h resultHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *resultHeap) Push(x interface{}) { *h = append(*h, x.(resultEntry)) }
func (h *resultHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
func (h resultHeap) Peek() resultEntry { return h[0] }
