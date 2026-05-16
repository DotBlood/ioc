package retrieval

import (
	"sort"

	"github.com/DotBlood/ioc/internal/model"
)

// RRF implements Reciprocal Rank Fusion.
//
//	score(doc) = Σ 1/(k + rank(doc, source))
//
// Default k = 60 (proven effective in TREC experiments).
type RRF struct {
	K float64 // rank constant (default 60)
}

// NewRRF creates an RRF fusion with the given k parameter.
func NewRRF(k float64) *RRF {
	return &RRF{K: k}
}

// Merge combines vector + text results into a single ranked list using RRF.
//
// Primary sort: Score descending.
// Secondary sort: ID lexical ascending (deterministic tie-break).
func (r *RRF) Merge(vector []SearchResult, text []TextResult, opts FusionOptions) []SearchResult {
	vectorRank := buildRankMap(vector)
	textRank := buildTextRankMap(text)
	allIDs := collectAllIDsIDs(vector, text)

	type scoredEntry struct {
		ID    model.ID
		Score float64
	}
	entries := make([]scoredEntry, 0, len(allIDs))

	for _, id := range allIDs {
		score := 0.0
		if vr, ok := vectorRank[id]; ok {
			score += 1.0 / (r.K + float64(vr))
		}
		if tr, ok := textRank[id]; ok {
			score += 1.0 / (r.K + float64(tr))
		}
		entries = append(entries, scoredEntry{ID: id, Score: score})
	}

	// Primary sort by Score descending.
	// Secondary sort by ID lexical ascending (deterministic tie-break).
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Score != entries[j].Score {
			return entries[i].Score > entries[j].Score
		}
		return entries[i].ID.String() < entries[j].ID.String()
	})

	limit := opts.TopK
	if len(entries) < limit {
		limit = len(entries)
	}
	results := make([]SearchResult, limit)
	for i := 0; i < limit; i++ {
		results[i] = SearchResult{
			ID:    entries[i].ID,
			Score: entries[i].Score,
		}
	}
	return results
}

func buildRankMap(results []SearchResult) map[model.ID]int {
	m := make(map[model.ID]int, len(results))
	for i, r := range results {
		m[r.ID] = i + 1 // 1-indexed rank
	}
	return m
}

func buildTextRankMap(results []TextResult) map[model.ID]int {
	m := make(map[model.ID]int, len(results))
	for i, r := range results {
		m[r.ID] = i + 1
	}
	return m
}

func collectAllIDsIDs(vector []SearchResult, text []TextResult) []model.ID {
	seen := make(map[model.ID]bool)
	var ids []model.ID
	for _, r := range vector {
		if !seen[r.ID] {
			seen[r.ID] = true
			ids = append(ids, r.ID)
		}
	}
	for _, r := range text {
		if !seen[r.ID] {
			seen[r.ID] = true
			ids = append(ids, r.ID)
		}
	}
	return ids
}
