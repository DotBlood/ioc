package search

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- Set (brute-force cosine over normalized vectors == dot product) ---

func TestSet_Search_OrdersByScoreThenID(t *testing.T) {
	s := New()
	// Normalized 2-D unit vectors at known angles to the query (1,0):
	//   a = (1,0)        cos 1.0
	//   b = (0,1)        cos 0.0
	//   c = (0.6,0.8)    cos 0.6
	s.Add("a", []float32{1, 0})
	s.Add("b", []float32{0, 1})
	s.Add("c", []float32{0.6, 0.8})

	got := s.Search([]float32{1, 0}, 5)
	require.Len(t, got, 3)
	require.Equal(t, []string{"a", "c", "b"}, []string{got[0].ID, got[1].ID, got[2].ID})
	require.InDelta(t, 1.0, got[0].Score, 1e-6)
	require.InDelta(t, 0.6, got[1].Score, 1e-6)
	require.InDelta(t, 0.0, got[2].Score, 1e-6)
}

func TestSet_Search_TieBreakByIDAscending(t *testing.T) {
	s := New()
	// Three identical vectors → identical scores → deterministic id-ascending order.
	s.Add("c", []float32{1, 0})
	s.Add("a", []float32{1, 0})
	s.Add("b", []float32{1, 0})
	got := s.Search([]float32{1, 0}, 3)
	require.Equal(t, []string{"a", "b", "c"}, []string{got[0].ID, got[1].ID, got[2].ID})
}

func TestSet_Search_TopKEdges(t *testing.T) {
	s := New()
	s.Add("a", []float32{1, 0})
	s.Add("b", []float32{0, 1})

	require.Nil(t, s.Search([]float32{1, 0}, 0), "topK<=0 returns nil")
	require.Nil(t, s.Search([]float32{1, 0}, -1), "negative topK returns nil")

	require.Len(t, s.Search([]float32{1, 0}, 1), 1, "topK<Len truncates")
	require.Len(t, s.Search([]float32{1, 0}, 99), 2, "topK>Len returns all")
}

func TestSet_Search_DimensionMismatchScoresZero(t *testing.T) {
	s := New()
	s.Add("short", []float32{1})        // wrong dim vs a 2-D query
	s.Add("ok", []float32{0.6, 0.8})    // matches query dim
	got := s.Search([]float32{1, 0}, 5) // dot() returns 0 on length mismatch
	require.Equal(t, "ok", got[0].ID)
	for _, r := range got {
		if r.ID == "short" {
			require.Equal(t, 0.0, r.Score)
		}
	}
}

func TestSet_Len(t *testing.T) {
	s := New()
	require.Equal(t, 0, s.Len())
	s.Add("a", []float32{1, 0})
	require.Equal(t, 1, s.Len())
}

// --- BM25 (Okapi, k1=1.2 b=0.75) ---

func TestBM25_Defaults(t *testing.T) {
	idx := NewBM25()
	require.Equal(t, 1.2, idx.k1)
	require.Equal(t, 0.75, idx.b)
	require.Equal(t, 0, idx.Len())
}

func TestBM25_EmptyOrNonPositiveTopK(t *testing.T) {
	idx := NewBM25()
	require.Nil(t, idx.Search("anything", 5), "empty index returns nil")
	idx.Add("a", "alpha beta")
	require.Nil(t, idx.Search("alpha", 0), "topK<=0 returns nil")
}

func TestBM25_MatchOutranksNonMatch(t *testing.T) {
	idx := NewBM25()
	idx.Add("hit", "reranker cross encoder precision")
	idx.Add("miss", "completely unrelated storage durability")
	got := idx.Search("cross encoder reranker", 5)
	require.Len(t, got, 2)
	require.Equal(t, "hit", got[0].ID)
	require.Greater(t, got[0].Score, got[1].Score)
	require.Equal(t, 0.0, got[1].Score, "a doc sharing no query term scores 0")
}

func TestBM25_RarerTermScoresHigher(t *testing.T) {
	idx := NewBM25()
	// "common" appears in every doc (low IDF); "rare" appears in one (high IDF).
	idx.Add("d1", "common rare")
	idx.Add("d2", "common filler")
	idx.Add("d3", "common filler")
	idx.Add("d4", "common filler")
	// Query both terms; d1 (has the rare term) must win on IDF.
	got := idx.Search("common rare", 4)
	require.Equal(t, "d1", got[0].ID)

	// IDF of the rare term must exceed IDF of the common one (positive, monotone in df).
	idfRare := math.Log(1 + (4-1+0.5)/(1+0.5))
	idfCommon := math.Log(1 + (4-4+0.5)/(4+0.5))
	require.Greater(t, idfRare, idfCommon)
	require.Greater(t, idfCommon, 0.0, "smoothed IDF stays positive even for an all-docs term")
}

func TestBM25_TermFrequencySaturates(t *testing.T) {
	idx := NewBM25()
	// Same length docs (pad to equal token count) isolating term frequency of "x".
	// k1=1.2 means marginal gain per extra occurrence diminishes.
	idx.Add("one", "x p p p p p p p")   // tf(x)=1, len 8
	idx.Add("two", "x x p p p p p p")   // tf(x)=2, len 8
	idx.Add("three", "x x x p p p p p") // tf(x)=3, len 8
	got := idx.Search("x", 3)
	byID := map[string]float64{}
	for _, r := range got {
		byID[r.ID] = r.Score
	}
	require.Greater(t, byID["two"], byID["one"], "more occurrences score higher")
	require.Greater(t, byID["three"], byID["two"])
	gain12 := byID["two"] - byID["one"]
	gain23 := byID["three"] - byID["two"]
	require.Greater(t, gain12, gain23, "marginal gain diminishes (k1 saturation)")
}

func TestBM25_AvgLenLazyAndStable(t *testing.T) {
	idx := NewBM25()
	idx.Add("a", "alpha beta gamma")
	idx.Add("b", "alpha")
	require.Equal(t, 0.0, idx.avgLen, "avgLen not computed until first Search")
	first := idx.Search("alpha", 2)
	require.InDelta(t, 2.0, idx.avgLen, 1e-9, "avgLen = (3+1)/2 computed on first Search")
	second := idx.Search("alpha", 2)
	require.Equal(t, first, second, "scores stable across repeated searches")
}

func TestBM25_TieBreakByIDAscending(t *testing.T) {
	idx := NewBM25()
	// Identical content → identical scores → id-ascending order.
	idx.Add("c", "alpha beta")
	idx.Add("a", "alpha beta")
	idx.Add("b", "alpha beta")
	got := idx.Search("alpha", 3)
	require.Equal(t, []string{"a", "b", "c"}, []string{got[0].ID, got[1].ID, got[2].ID})
}

func TestBMTokenize(t *testing.T) {
	require.Equal(t, []string{"hello", "world"}, bmTokenize("Hello, World!"))
	require.Equal(t, []string{"path", "lines", "go", "func"}, bmTokenize("path:lines.go — func"))
	require.Equal(t, []string{"a1", "b2"}, bmTokenize("a1 b2"), "digits are kept")
	require.Empty(t, bmTokenize("  --  "), "punctuation-only yields no tokens")
}

// --- RRF (Reciprocal Rank Fusion) ---

func TestRRF_FormulaAndDefaultK(t *testing.T) {
	// One list, single id at rank 0: score = 1/(k+0+1). Default k=60 when k<=0.
	out := RRF(0, []Result{{ID: "a"}})
	require.Len(t, out, 1)
	require.InDelta(t, 1.0/61.0, out[0].Score, 1e-12)

	outExplicit := RRF(60, []Result{{ID: "a"}})
	require.Equal(t, out, outExplicit, "k<=0 defaults to 60")
}

func TestRRF_FusesAcrossLists(t *testing.T) {
	// "b" appears in BOTH lists (rank 0 and rank 1) → highest fused score.
	listA := []Result{{ID: "a"}, {ID: "b"}} // a@0, b@1
	listB := []Result{{ID: "b"}, {ID: "c"}} // b@0, c@1
	out := RRF(60, listA, listB)

	score := map[string]float64{}
	for _, r := range out {
		score[r.ID] = r.Score
	}
	require.InDelta(t, 1.0/61.0+1.0/62.0, score["b"], 1e-12, "b fused from both lists")
	require.InDelta(t, 1.0/61.0, score["a"], 1e-12)
	require.InDelta(t, 1.0/62.0, score["c"], 1e-12)
	require.Equal(t, "b", out[0].ID, "b ranks first on fused score")
}

func TestRRF_TieBreakByIDAscending(t *testing.T) {
	// Each id appears once at rank 0 in its own list → equal fused score → id order.
	out := RRF(60, []Result{{ID: "c"}}, []Result{{ID: "a"}}, []Result{{ID: "b"}})
	require.Equal(t, []string{"a", "b", "c"}, []string{out[0].ID, out[1].ID, out[2].ID})
}

func TestRRF_EmptyInputs(t *testing.T) {
	require.Empty(t, RRF(60))
	require.Empty(t, RRF(60, []Result{}, []Result{}))
}
