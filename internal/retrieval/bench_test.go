package retrieval

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/DotBlood/ioc/internal/embedding"
	"github.com/DotBlood/ioc/internal/model"
)

func newDeterministicRNG() *rand.Rand {
	return rand.New(rand.NewPCG(1, 2))
}

func BenchmarkRetrieval_Coarse_10KVectors(b *testing.B) {
	ctx := context.Background()
	rng := newDeterministicRNG()
	dim := 384
	n := 10000

	idx := NewBruteForceIndex(dim)
	entries := make([]IndexEntry, n)
	for i := range n {
		vec := make([]float32, dim)
		for j := range dim {
			vec[j] = rng.Float32()
		}
		entries[i] = IndexEntry{ID: model.NewID(), Vector: vec}
	}
	err := idx.Upsert(ctx, entries)
	if err != nil {
		b.Fatal(err)
	}

	query := make([]float32, dim)
	for j := range dim {
		query[j] = rng.Float32()
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		results, err := idx.Search(ctx, query, SearchOptions{TopK: 10})
		if err != nil {
			b.Fatal(err)
		}
		if len(results) != 10 {
			b.Fatalf("got %d results, want 10", len(results))
		}
	}
}

func BenchmarkRetrieval_Full_1000Candidates(b *testing.B) {
	ctx := context.Background()
	rng := newDeterministicRNG()
	dim := 384
	n := 1000

	emb := embedding.NewMockEmbedder(embedding.DefaultMockConfig())
	vecIdx := NewBruteForceIndex(dim)
	textIdx := NewBM25Index()
	fusion := NewRRF(60)

	vectorEntries := make([]IndexEntry, n)
	textDocs := make([]TextDocument, n)
	for i := range n {
		id := model.NewID()
		vec := make([]float32, dim)
		for j := range dim {
			vec[j] = rng.Float32()
		}
		vectorEntries[i] = IndexEntry{ID: id, Vector: vec}
		textDocs[i] = TextDocument{ID: id, Content: fmt.Sprintf("benchmark document number %d for retrieval performance testing with varied vocabulary and content", i)}
	}

	err := vecIdx.Upsert(ctx, vectorEntries)
	if err != nil {
		b.Fatal(err)
	}
	err = textIdx.Index(ctx, textDocs)
	if err != nil {
		b.Fatal(err)
	}

	query := "benchmark query for retrieval performance testing"
	queryVec := make([]float32, dim)
	for j := range dim {
		queryVec[j] = rng.Float32()
	}

	engine := NewEngine(emb, vecIdx, textIdx, fusion)

	b.Run("Full", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			results, err := engine.Query(ctx, query, QueryOptions{TopK: 50, VectorTopK: 100, TextTopK: 100})
			if err != nil {
				b.Fatal(err)
			}
			if len(results) == 0 {
				b.Fatal("expected at least one result")
			}
		}
	})

	b.Run("Full_NoEmbed", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			vresults, err := vecIdx.Search(ctx, queryVec, SearchOptions{TopK: 100})
			if err != nil {
				b.Fatal(err)
			}
			tresults, err := textIdx.Search(ctx, query, TextSearchOptions{TopK: 100})
			if err != nil {
				b.Fatal(err)
			}
			results := fusion.Merge(vresults, tresults, FusionOptions{TopK: 50})
			if len(results) == 0 {
				b.Fatal("expected at least one result")
			}
		}
	})

	b.Run("OnlyVector", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			results, err := vecIdx.Search(ctx, queryVec, SearchOptions{TopK: 50})
			if err != nil {
				b.Fatal(err)
			}
			if len(results) == 0 {
				b.Fatal("expected at least one result")
			}
		}
	})
}
