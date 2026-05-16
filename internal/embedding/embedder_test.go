package embedding

import (
	"context"
	"math"
	"testing"
)

func TestMockEmbedder_Embed_SingleText(t *testing.T) {
	m := NewMockEmbedder(DefaultMockConfig())
	ctx := context.Background()

	batch, err := m.Embed(ctx, []string{"hello world"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if len(batch.Vectors) != 1 {
		t.Fatalf("expected 1 vector, got %d", len(batch.Vectors))
	}
	if batch.Dimension != 384 {
		t.Errorf("Dimension = %d, want 384", batch.Dimension)
	}
	if len(batch.Vectors[0].Data) != 384 {
		t.Errorf("vector data len = %d, want 384", len(batch.Vectors[0].Data))
	}
}

func TestMockEmbedder_Embed_MultipleTexts(t *testing.T) {
	m := NewMockEmbedder(DefaultMockConfig())
	ctx := context.Background()

	texts := []string{"first", "second", "third", "fourth", "fifth"}
	batch, err := m.Embed(ctx, texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if len(batch.Vectors) != 5 {
		t.Fatalf("expected 5 vectors, got %d", len(batch.Vectors))
	}
	for i, v := range batch.Vectors {
		if len(v.Data) != 384 {
			t.Errorf("vector %d: data len = %d, want 384", i, len(v.Data))
		}
	}
}

func TestMockEmbedder_Determinism(t *testing.T) {
	m := NewMockEmbedder(DefaultMockConfig())
	ctx := context.Background()

	batch1, _ := m.Embed(ctx, []string{"deterministic test"})
	batch2, _ := m.Embed(ctx, []string{"deterministic test"})

	if len(batch1.Vectors) != 1 || len(batch2.Vectors) != 1 {
		t.Fatal("expected 1 vector each")
	}

	v1 := batch1.Vectors[0].Data
	v2 := batch2.Vectors[0].Data

	for i := range v1 {
		if v1[i] != v2[i] {
			t.Errorf("determinism broken at index %d: %f vs %f", i, v1[i], v2[i])
			break
		}
	}
}

func TestMockEmbedder_Normalized(t *testing.T) {
	m := NewMockEmbedder(DefaultMockConfig())
	ctx := context.Background()

	texts := []string{
		"short",
		"a slightly longer sentence for testing",
		"",
		"the quick brown fox jumps over the lazy dog",
		"    spaces and special chars: !@#$%^&*()",
	}

	batch, err := m.Embed(ctx, texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	for i, vec := range batch.Vectors {
		var sumSq float64
		for _, v := range vec.Data {
			sumSq += float64(v) * float64(v)
		}
		norm := math.Sqrt(sumSq)

		// Allow small epsilon for float32 precision.
		if norm < 0.9999 || norm > 1.0001 {
			t.Errorf("vector %d (text=%q): norm = %f, want ≈1.0", i, texts[i], norm)
		}
	}
}

func TestMockEmbedder_EmptyInput(t *testing.T) {
	m := NewMockEmbedder(DefaultMockConfig())
	ctx := context.Background()

	_, err := m.Embed(ctx, nil)
	if err != ErrInvalidInput {
		t.Errorf("nil batch: want ErrInvalidInput, got %v", err)
	}

	_, err = m.Embed(ctx, []string{})
	if err != ErrInvalidInput {
		t.Errorf("empty batch: want ErrInvalidInput, got %v", err)
	}
}

func TestMockEmbedder_EmptyStringIsValid(t *testing.T) {
	m := NewMockEmbedder(DefaultMockConfig())
	ctx := context.Background()

	batch, err := m.Embed(ctx, []string{""})
	if err != nil {
		t.Fatalf("empty string: Embed returned error: %v", err)
	}
	if len(batch.Vectors) != 1 {
		t.Fatalf("empty string: expected 1 vector")
	}
	if len(batch.Vectors[0].Data) != 384 {
		t.Errorf("empty string: vector data len = %d, want 384", len(batch.Vectors[0].Data))
	}

	// Whitespace-only should also be valid.
	batch2, err := m.Embed(ctx, []string{"   ", "\t", "\n"})
	if err != nil {
		t.Fatalf("whitespace batch: Embed returned error: %v", err)
	}
	if len(batch2.Vectors) != 3 {
		t.Fatalf("whitespace batch: expected 3 vectors, got %d", len(batch2.Vectors))
	}
}

func TestMockEmbedder_DifferentTexts_DifferentVectors(t *testing.T) {
	m := NewMockEmbedder(DefaultMockConfig())
	ctx := context.Background()

	batch, err := m.Embed(ctx, []string{"abc", "xyz"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	v1 := batch.Vectors[0].Data
	v2 := batch.Vectors[1].Data

	same := true
	for i := range v1 {
		if v1[i] != v2[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("different texts produced identical vectors")
	}
}

func TestMockEmbedder_ModelName(t *testing.T) {
	cfg := DefaultMockConfig()
	cfg.ModelName = "test-model-v2"
	m := NewMockEmbedder(cfg)

	if m.Model() != "test-model-v2" {
		t.Errorf("Model() = %q, want %q", m.Model(), "test-model-v2")
	}
}

func TestMockEmbedder_DimsConstant(t *testing.T) {
	cfg := DefaultMockConfig()
	cfg.Dimension = 128
	m := NewMockEmbedder(cfg)

	if m.Dims() != 128 {
		t.Errorf("Dims() = %d, want 128", m.Dims())
	}
}

func TestMockEmbedder_ApproximateTokens(t *testing.T) {
	tests := []struct {
		text string
		min  int
	}{
		{"", 0},
		{"a", 0},
		{"hello", 1},
		{"hello world", 2},
		{"a longer text with multiple words", 6},
	}

	for _, tt := range tests {
		got := approximateTokens(tt.text)
		if got < tt.min {
			t.Errorf("approximateTokens(%q) = %d, want at least %d", tt.text, got, tt.min)
		}
	}
}
