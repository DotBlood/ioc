package search

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// BM25 is a minimal in-memory Okapi BM25 index over short texts (summaries),
// keyed by string id. Dependency-free; sized for a per-query visible set.
type BM25 struct {
	k1, b  float64
	ids    []string
	docs   [][]string // tokenized
	docLen []int
	df     map[string]int
	avgLen float64
	totalN int
}

// NewBM25 creates an empty index with standard parameters (k1=1.2, b=0.75).
func NewBM25() *BM25 {
	return &BM25{k1: 1.2, b: 0.75, df: map[string]int{}}
}

// Add indexes a document's text under an id.
func (idx *BM25) Add(id, text string) {
	toks := bmTokenize(text)
	idx.ids = append(idx.ids, id)
	idx.docs = append(idx.docs, toks)
	idx.docLen = append(idx.docLen, len(toks))
	idx.totalN++
	seen := map[string]bool{}
	for _, t := range toks {
		if !seen[t] {
			idx.df[t]++
			seen[t] = true
		}
	}
}

// Len returns the number of indexed documents.
func (idx *BM25) Len() int { return idx.totalN }

// Search returns the top-K documents by BM25 score for the query.
func (idx *BM25) Search(query string, topK int) []Result {
	if idx.totalN == 0 || topK <= 0 {
		return nil
	}
	if idx.avgLen == 0 {
		total := 0
		for _, l := range idx.docLen {
			total += l
		}
		idx.avgLen = float64(total) / float64(idx.totalN)
	}
	qToks := bmTokenize(query)
	results := make([]Result, 0, idx.totalN)
	for i, id := range idx.ids {
		results = append(results, Result{ID: id, Score: idx.score(qToks, i)})
	}
	sort.SliceStable(results, func(a, b int) bool {
		if results[a].Score != results[b].Score {
			return results[a].Score > results[b].Score
		}
		return results[a].ID < results[b].ID
	})
	if len(results) > topK {
		results = results[:topK]
	}
	return results
}

func (idx *BM25) score(qToks []string, doc int) float64 {
	tf := map[string]int{}
	for _, t := range idx.docs[doc] {
		tf[t]++
	}
	dl := float64(idx.docLen[doc])
	var score float64
	for _, q := range qToks {
		f := float64(tf[q])
		if f == 0 {
			continue
		}
		df := float64(idx.df[q])
		idf := math.Log(1 + (float64(idx.totalN)-df+0.5)/(df+0.5))
		score += idf * (f * (idx.k1 + 1)) / (f + idx.k1*(1-idx.b+idx.b*dl/idx.avgLen))
	}
	return score
}

func bmTokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// RRF fuses ranked result lists via Reciprocal Rank Fusion: score = sum 1/(k+rank).
// Returns ids ordered by fused score descending (ties by id ascending).
func RRF(k int, lists ...[]Result) []Result {
	if k <= 0 {
		k = 60
	}
	fused := map[string]float64{}
	for _, list := range lists {
		for rank, r := range list {
			fused[r.ID] += 1.0 / float64(k+rank+1)
		}
	}
	out := make([]Result, 0, len(fused))
	for id, s := range fused {
		out = append(out, Result{ID: id, Score: s})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		return out[a].ID < out[b].ID
	})
	return out
}
