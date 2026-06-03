// Package embed defines the Embedder interface and its implementations.
// IOC calls an embedder (an embedding model) — never a reasoning LLM.
package embed

import (
	"context"
	"errors"
)

// Embedder turns text into fixed-dimension vectors.
type Embedder interface {
	// Embed returns one vector per input text (normalized, so cosine == dot product).
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Dims is the vector dimension.
	Dims() int
	// Model is a human-readable model identifier (recorded in traces).
	Model() string
}

// ErrEmptyInput is returned when no texts are provided.
var ErrEmptyInput = errors.New("embed: empty input")
