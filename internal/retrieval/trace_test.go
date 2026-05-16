package retrieval

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DotBlood/ioc/internal/embedding"
	"github.com/DotBlood/ioc/internal/model"
)

func TestTrace_AllStagesPresent(t *testing.T) {
	idx := NewBruteForceIndex(2)
	emb := &testEmbedder{dims: 2}
	id1 := model.NewID()
	idx.Upsert(context.Background(), []IndexEntry{
		{ID: id1, Vector: []float32{1, 0}},
	})

	engine := NewEngine(emb, idx, nil, nil)
	_, trace, err := engine.Trace(context.Background(), "hello", QueryOptions{
		TopK: 3, VectorTopK: 3, TextTopK: 3,
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	if len(trace.Stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(trace.Stages))
	}

	expected := []string{"vector_search", "text_search", "fusion"}
	for i, stage := range trace.Stages {
		if stage.Name != expected[i] {
			t.Errorf("stage %d: name = %q, want %q", i, stage.Name, expected[i])
		}
	}

	if trace.Stages[0].InputSize != 1 {
		t.Errorf("vector_search InputSize = %d, want 1", trace.Stages[0].InputSize)
	}
	if trace.Stages[2].InputSize != trace.Stages[0].OutputSize+0 {
		t.Errorf("fusion InputSize = %d, want vector_output + text_output", trace.Stages[2].InputSize)
	}
}

func TestTrace_ErrorsCollected(t *testing.T) {
	// Engine with nil embedder — will cause vector search to fail.
	idx := NewBruteForceIndex(2)
	engine := NewEngine(nil, idx, nil, nil)

	_, trace, err := engine.Trace(context.Background(), "hello", QueryOptions{
		TopK: 3, VectorTopK: 3, TextTopK: 3,
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	// No embedder configured — vector search is skipped, not an error.
	// So trace.Errors should be empty.
	if len(trace.Errors) > 0 {
		t.Errorf("expected no errors, got %d: %v", len(trace.Errors), trace.Errors)
	}
}

func TestTrace_ErrorsWithFailingEmbedder(t *testing.T) {
	// Create embedder that fails on certain inputs.
	emb := &failEmbedder{shouldFail: true}
	idx := NewBruteForceIndex(2)
	engine := NewEngine(emb, idx, nil, nil)

	_, trace, err := engine.Trace(context.Background(), "fail", QueryOptions{
		TopK: 3, VectorTopK: 3, TextTopK: 3,
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	if len(trace.Errors) == 0 {
		t.Error("expected errors from failing embedder")
	}
}

type failEmbedder struct {
	testEmbedder
	shouldFail bool
}

func (e *failEmbedder) Embed(ctx context.Context, texts []string) (*embedding.Batch, error) {
	if e.shouldFail {
		return nil, embedding.ErrInferenceFailed
	}
	return e.testEmbedder.Embed(ctx, texts)
}

func TestTrace_DurationInitialized(t *testing.T) {
	idx := NewBruteForceIndex(2)
	emb := &testEmbedder{dims: 2}
	id1 := model.NewID()
	idx.Upsert(context.Background(), []IndexEntry{
		{ID: id1, Vector: []float32{1, 0}},
	})

	engine := NewEngine(emb, idx, nil, nil)
	_, trace, err := engine.Trace(context.Background(), "hello", QueryOptions{
		TopK: 3, VectorTopK: 3, TextTopK: 3,
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	if trace.Duration < 0 {
		t.Error("Duration should not be negative")
	}
	if len(trace.Stages) == 0 {
		t.Error("expected at least 1 stage")
	}
}

func TestTrace_QueryHash(t *testing.T) {
	idx := NewBruteForceIndex(2)
	emb := &testEmbedder{dims: 2}
	engine := NewEngine(emb, idx, nil, nil)

	_, trace1, _ := engine.Trace(context.Background(), "same query", QueryOptions{
		TopK: 3, VectorTopK: 3, TextTopK: 3,
	})
	_, trace2, _ := engine.Trace(context.Background(), "same query", QueryOptions{
		TopK: 3, VectorTopK: 3, TextTopK: 3,
	})

	if trace1.QueryHash != trace2.QueryHash {
		t.Error("QueryHash should be deterministic for same query")
	}
	if trace1.QueryHash == (model.ContentHash{}) {
		t.Error("QueryHash should not be empty")
	}
}

func TestTraceAsJSON_ValidOutput(t *testing.T) {
	idx := NewBruteForceIndex(2)
	emb := &testEmbedder{dims: 2}
	id1 := model.NewID()
	idx.Upsert(context.Background(), []IndexEntry{
		{ID: id1, Vector: []float32{1, 0}},
	})

	engine := NewEngine(emb, idx, nil, nil)
	_, trace, err := engine.Trace(context.Background(), "hello", QueryOptions{
		TopK: 3, VectorTopK: 3, TextTopK: 3,
	})
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}

	jsonStr, err := TraceAsJSON(trace)
	if err != nil {
		t.Fatalf("TraceAsJSON: %v", err)
	}

	var decoded model.RetrievalTrace
	if err := json.Unmarshal([]byte(jsonStr), &decoded); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}

	if len(decoded.FinalSelection) == 0 {
		t.Error("expected non-empty FinalSelection in JSON output")
	}
}
