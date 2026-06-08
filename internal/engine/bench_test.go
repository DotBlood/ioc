package engine

import (
	"context"
	"fmt"
	"testing"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
)

// benchSummary keeps the embedded text varied so the mock bag-of-words embedder
// produces distinct vectors (a constant string would collapse the vector space).
func benchSummary(i int) string {
	return fmt.Sprintf("artifact number %d about retrieval scope memory token vector index lesson %d", i, i*7)
}

// BenchmarkPush measures the write path (embed via mock + CAS + EmbeddingStore +
// Meta) with no network embedder in the way, so it isolates engine/storage cost.
func BenchmarkPush(b *testing.B) {
	ctx := context.Background()
	e, err := Open(ctx, b.TempDir(), embed.NewMockEmbedder(384))
	if err != nil {
		b.Fatal(err)
	}
	defer e.Close()
	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "bench")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Push(ctx, core.PushRequest{
			Scope:   root.ID,
			Kind:    core.KindReasoning,
			Summary: benchSummary(i),
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkQueryParallel hammers the read path from many goroutines at once to
// exercise the concurrent-read regime (engine RWMutex: parallel reads). It both
// stresses for races/panics under load and reports aggregate parallel throughput
// against a 1000-artifact store.
func BenchmarkQueryParallel(b *testing.B) {
	ctx := context.Background()
	e, err := Open(ctx, b.TempDir(), embed.NewMockEmbedder(384))
	if err != nil {
		b.Fatal(err)
	}
	defer e.Close()
	root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "bench")
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		if _, err := e.Push(ctx, core.PushRequest{
			Scope: root.ID, Kind: core.KindReasoning, Summary: benchSummary(i),
		}); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, _, err := e.Query(ctx, core.Query{
				Scope: root.ID, Text: "retrieval scope memory vector lesson",
				Detail: core.DetailOverview, TopK: 5, Collapsed: true,
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkQuery measures the read path (collapsed default: embed query + cosine
// over the whole visible set) against a store pre-loaded with N artifacts, so the
// reported ns/op is the per-query latency at that corpus size.
func BenchmarkQuery(b *testing.B) {
	ctx := context.Background()
	for _, n := range []int{100, 1_000, 10_000} {
		e, err := Open(ctx, b.TempDir(), embed.NewMockEmbedder(384))
		if err != nil {
			b.Fatal(err)
		}
		root, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "bench")
		if err != nil {
			b.Fatal(err)
		}
		for i := 0; i < n; i++ {
			if _, err := e.Push(ctx, core.PushRequest{
				Scope:   root.ID,
				Kind:    core.KindReasoning,
				Summary: benchSummary(i),
			}); err != nil {
				b.Fatal(err)
			}
		}
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, _, err := e.Query(ctx, core.Query{
					Scope:     root.ID,
					Text:      "retrieval scope memory vector lesson",
					Detail:    core.DetailOverview,
					TopK:      5,
					Collapsed: true,
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
		e.Close()
	}
}
