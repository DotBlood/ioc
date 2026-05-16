package embedding

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkMockEmbedder_100Texts(b *testing.B) {
	ctx := context.Background()
	m := NewMockEmbedder(DefaultMockConfig())

	texts := make([]string, 100)
	for i := range 100 {
		texts[i] = fmt.Sprintf("benchmark text number %d for mock embedding performance measurement with varied vocabulary", i)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		batch, err := m.Embed(ctx, texts)
		if err != nil {
			b.Fatal(err)
		}
		if len(batch.Vectors) != 100 {
			b.Fatalf("got %d vectors, want 100", len(batch.Vectors))
		}
	}
}
