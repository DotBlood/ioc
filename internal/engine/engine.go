// Package engine wires the storage, embedding, and search layers into the
// IOC public API. It is the only package that depends on all the others.
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/storage"
)

// Engine is the embeddable IOC runtime. The external LLM writes summaries via
// Push and reads via Query/Drill; IOC stores, embeds, and serves them.
type Engine struct {
	dir      string
	meta     *storage.Meta
	cas      *storage.CAS
	emb      *storage.EmbeddingStore // opened lazily once the embedding dim is known
	embedder embed.Embedder
	reranker embed.Reranker // optional cross-encoder for last-mile rerank (nil = off)
}

// Option configures an Engine at Open time.
type Option func(*Engine)

// WithReranker attaches a cross-encoder reranker (used when Query.Rerank is set).
func WithReranker(r embed.Reranker) Option { return func(e *Engine) { e.reranker = r } }

// Open opens (creating if needed) an IOC repository at dir, using embedder for
// summary embeddings. Options can attach extras like a reranker.
func Open(_ context.Context, dir string, embedder embed.Embedder, opts ...Option) (*Engine, error) {
	if embedder == nil {
		return nil, fmt.Errorf("engine: nil embedder")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("engine: mkdir: %w", err)
	}
	meta, err := storage.OpenMeta(filepath.Join(dir, "meta.db"))
	if err != nil {
		return nil, err
	}
	e := &Engine{
		dir:      dir,
		meta:     meta,
		cas:      storage.NewCAS(filepath.Join(dir, "cas")),
		embedder: embedder,
	}
	for _, opt := range opts {
		opt(e)
	}
	// Open the embedding store eagerly if the dimension is already known
	// (persisted from a prior run, or fixed by the embedder).
	if v, ok := meta.GetConfig("emb_dims"); ok {
		if dims, err := strconv.Atoi(v); err == nil {
			if err := e.openEmb(dims); err != nil {
				meta.Close()
				return nil, err
			}
		}
	} else if d := embedder.Dims(); d > 0 {
		if err := e.openEmb(d); err != nil {
			meta.Close()
			return nil, err
		}
	}
	return e, nil
}

// Close releases resources.
func (e *Engine) Close() error {
	if e.emb != nil {
		if err := e.emb.Close(); err != nil {
			return err
		}
	}
	return e.meta.Close()
}

func (e *Engine) openEmb(dims int) error {
	es, err := storage.OpenEmbeddingStore(filepath.Join(e.dir, "emb.dat"), dims)
	if err != nil {
		return err
	}
	e.emb = es
	return e.meta.PutConfig("emb_dims", strconv.Itoa(dims))
}

func (e *Engine) ensureEmb(dims int) error {
	if e.emb == nil {
		return e.openEmb(dims)
	}
	if e.emb.Dims() != dims {
		return fmt.Errorf("engine: embedding dim mismatch: store=%d new=%d", e.emb.Dims(), dims)
	}
	return nil
}

// embedText embeds a single document/passage into a vector.
func (e *Engine) embedText(ctx context.Context, text string) ([]float32, error) {
	return one(e.embedder.Embed(ctx, []string{text}))
}

// embedQuery embeds a single search query (instruction-prefixed for bge).
func (e *Engine) embedQuery(ctx context.Context, text string) ([]float32, error) {
	return one(e.embedder.EmbedQuery(ctx, []string{text}))
}

func one(batch [][]float32, err error) ([]float32, error) {
	if err != nil {
		return nil, err
	}
	if len(batch) != 1 {
		return nil, fmt.Errorf("engine: embedder returned %d vectors for 1 text", len(batch))
	}
	return batch[0], nil
}

// storeSummaryEmbedding embeds summary text and appends it to the vector store.
func (e *Engine) storeSummaryEmbedding(ctx context.Context, summary string) (core.EmbeddingRef, error) {
	vec, err := e.embedText(ctx, summary)
	if err != nil {
		return 0, err
	}
	if err := e.ensureEmb(len(vec)); err != nil {
		return 0, err
	}
	return e.emb.Put(vec)
}

// CreateScope creates a scope under parent (zero parent => root).
func (e *Engine) CreateScope(_ context.Context, parent core.ID, role core.Role, title string) (core.Scope, error) {
	if !parent.IsZero() {
		if _, err := e.meta.GetScope(parent); err != nil {
			return core.Scope{}, fmt.Errorf("engine: parent scope: %w", err)
		}
	}
	s := core.Scope{
		ID:        core.NewID(),
		Parent:    parent,
		Role:      role,
		Title:     title,
		Version:   1,
		CreatedAt: time.Now(),
	}
	if err := e.meta.PutScope(s); err != nil {
		return core.Scope{}, err
	}
	return s, nil
}

// Push writes content + a summary into IOC. The summary is REQUIRED and is the
// text that gets embedded; full content (if any) goes to CAS.
func (e *Engine) Push(ctx context.Context, r core.PushRequest) (core.Artifact, error) {
	if r.Summary == "" {
		return core.Artifact{}, fmt.Errorf("engine: push: %w: empty summary", core.ErrInvalidInput)
	}
	if _, err := e.meta.GetScope(r.Scope); err != nil {
		return core.Artifact{}, fmt.Errorf("engine: push: scope: %w", err)
	}
	tier := r.Tier
	if tier == 0 {
		tier = core.TierWorkspace
	}
	kind := r.Kind
	if kind == 0 {
		kind = core.KindInsight
	}

	var content core.ContentHash
	if r.Content != nil {
		h, err := e.cas.StoreBytes(ctx, r.Content)
		if err != nil {
			return core.Artifact{}, err
		}
		content = h
	}
	ref, err := e.storeSummaryEmbedding(ctx, r.Summary)
	if err != nil {
		return core.Artifact{}, err
	}
	a := core.Artifact{
		ID:          core.NewID(),
		Scope:       r.Scope,
		Kind:        kind,
		Tier:        tier,
		Summary:     r.Summary,
		EmbRef:      ref,
		Content:     content,
		DerivedFrom: r.DerivedFrom,
		Published:   r.Publish,
		CreatedAt:   time.Now(),
	}
	if err := e.meta.PutArtifact(a); err != nil {
		return core.Artifact{}, err
	}
	return a, nil
}

// Trace returns the recorded context a query saw.
func (e *Engine) Trace(_ context.Context, queryID core.ID) (core.TraceRecord, error) {
	return e.meta.GetTrace(queryID)
}

// EmbModel returns the embedder's model identifier.
func (e *Engine) EmbModel() string { return e.embedder.Model() }
