// Package embedding provides the Embedder interface for text embedding inference.
//
// v0.1: mock implementation only.
// TODO(v0.2): external inference service over HTTP+Unix socket.
package embedding

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Embedder is the abstraction for text embedding inference.
//
// Invariant: Dims() MUST remain constant for the lifetime of the embedder instance.
// Violation would cause index corruption, mixed vector spaces, invalid cosine results.
type Embedder interface {
	// Embed converts a batch of texts into embedding vectors.
	// Returns ErrInferenceFailed if the embedder is unavailable.
	// Returns ErrInvalidInput if texts is empty.
	Embed(ctx context.Context, texts []string) (*Batch, error)

	// Dims returns the embedding dimension (fixed for this embedder instance).
	Dims() int

	// Model returns the model identifier.
	Model() string
}

// Batch is the result of a single Embed call.
type Batch struct {
	Vectors   []Vector
	Dimension int
	Model     string
	CreatedAt time.Time
}

// Vector is a single embedding vector.
// Text is NOT stored here — traceability is external.
type Vector struct {
	Data       []float32 // dims * 4 bytes
	TokenCount int       // approximate token count (for hierarchical averaging)
}

// Sentinel errors.
var (
	ErrInferenceFailed = errors.New("embedding inference failed")
	ErrInvalidInput    = errors.New("invalid input: empty text batch")
)

// InferenceError carries details about a failed inference.
type InferenceError struct {
	Text  string // which text caused the failure (if identifiable)
	Model string
	Err   error
}

func (e *InferenceError) Error() string {
	return fmt.Sprintf("embedding inference for %q (model %s): %v", e.Text, e.Model, e.Err)
}

func (e *InferenceError) Unwrap() error { return e.Err }
