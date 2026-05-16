package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/DotBlood/ioc/internal/embedding"
	"github.com/DotBlood/ioc/internal/model"
	"github.com/DotBlood/ioc/internal/retrieval"
	"github.com/DotBlood/ioc/internal/store"
)

// ============================================================
// Capability interfaces
// ============================================================

// CASWriter provides content-addressed storage for raw content.
type CASWriter interface {
	Store(ctx context.Context, r io.Reader) (model.ContentHash, error)
	Open(ctx context.Context, hash model.ContentHash) (io.ReadCloser, error)
}

// ArtifactWriter provides read/write access to artifacts and projections.
type ArtifactWriter interface {
	SaveArtifact(ctx context.Context, art *model.Artifact) error
	SaveProjection(ctx context.Context, proj *model.ArtifactProjection) error
	LoadProjection(ctx context.Context, key model.ProjectionKey) (*model.ArtifactProjection, error)
	LatestProjection(ctx context.Context, artifactID model.ID) (*model.ArtifactProjection, error)
	ListArtifactsByType(ctx context.Context, nodeType model.NodeType) ([]model.ID, error)
	LoadArtifact(ctx context.Context, id model.ID) (*model.Artifact, error)
}

// EmbeddingWriter persists embedding vectors to durable storage.
type EmbeddingWriter interface {
	SaveEmbedding(ctx context.Context, artifactID model.ID, vec embedding.Vector) (model.EmbeddingRefID, error)
}

// OwnershipWriter attaches an artifact to the scope graph via an ownership edge.
type OwnershipWriter interface {
	AddOwnership(ctx context.Context, parent model.ID, child model.ID) error
}

// ScopeResolver maps a logical scopeID to a graph node ID.
type ScopeResolver interface {
	ResolveScope(ctx context.Context, scopeID model.ScopeID) (model.ID, error)
}

// ============================================================
// EmbeddingStore adapter
// ============================================================

type embeddingStoreAdapter struct {
	inner *store.EmbeddingStore
}

func (a *embeddingStoreAdapter) SaveEmbedding(_ context.Context, _ model.ID, vec embedding.Vector) (model.EmbeddingRefID, error) {
	return a.inner.Put(vec.Data)
}

// ============================================================
// IngestionPipeline
// ============================================================

// IngestionPipeline ingests content into the graph.
//
// Process is progressively committing — earlier stages are NOT rolled
// back if later stages fail. Partial readiness states are valid for v0.1.
//
// BM25Index in v0.1 is snapshot-based and rebuilt from ArtifactStore
// on each ingestion update. Each Process() loads all stored documents
// and calls Index() for a full rebuild. This is O(N) per ingestion
// and acceptable for v0.1 corpus sizes. Incremental indexing is
// deferred to future phases.
type IngestionPipeline struct {
	cas        CASWriter
	artifacts  ArtifactWriter
	embeddings EmbeddingWriter
	ownership  OwnershipWriter
	scopes     ScopeResolver
	embedder   embedding.Embedder
	bm25       retrieval.TextIndex
}

// NewIngestionPipeline creates an IngestionPipeline with explicit capability interfaces.
func NewIngestionPipeline(
	cas CASWriter,
	artifacts ArtifactWriter,
	embeddings EmbeddingWriter,
	ownership OwnershipWriter,
	scopes ScopeResolver,
	embedder embedding.Embedder,
	bm25 retrieval.TextIndex,
) *IngestionPipeline {
	return &IngestionPipeline{
		cas:        cas,
		artifacts:  artifacts,
		embeddings: embeddings,
		ownership:  ownership,
		scopes:     scopes,
		embedder:   embedder,
		bm25:       bm25,
	}
}

// NewIngestionPipelineFromStore creates an IngestionPipeline wrapping concrete store types.
func NewIngestionPipelineFromStore(
	cas *store.CAS,
	artifactStore *store.ArtifactStore,
	embeddingStore *store.EmbeddingStore,
	ownership OwnershipWriter,
	scopes ScopeResolver,
	embedder embedding.Embedder,
	bm25 retrieval.TextIndex,
) *IngestionPipeline {
	return NewIngestionPipeline(
		cas,
		artifactStore,
		&embeddingStoreAdapter{inner: embeddingStore},
		ownership,
		scopes,
		embedder,
		bm25,
	)
}

// Process ingests content into the graph and updates derived indexes.
func (p *IngestionPipeline) Process(
	ctx context.Context,
	content []byte,
	scopeID model.ScopeID,
	summary string,
) (model.ID, error) {
	// 1. Content hash.
	contentHash := model.NewContentHash(content)

	// 2. CAS store (dedup).
	_, err := p.cas.Store(ctx, bytes.NewReader(content))
	if err != nil {
		return model.NilID, fmt.Errorf("cas store: %w", err)
	}

	// 3. Save artifact.
	artifactID := model.NewID()
	art := &model.Artifact{
		ArtifactID:  artifactID,
		NodeType:    model.NodeTypeArtifact,
		Scope:       scopeID,
		ContentHash: contentHash,
		CreatedAt:   time.Now(),
	}
	if err := p.artifacts.SaveArtifact(ctx, art); err != nil {
		return model.NilID, fmt.Errorf("save artifact: %w", err)
	}

	// 4. Scope ownership edge.
	scopeNodeID, err := p.scopes.ResolveScope(ctx, scopeID)
	if err != nil {
		return model.NilID, fmt.Errorf("resolve scope: %w", err)
	}
	if err := p.ownership.AddOwnership(ctx, scopeNodeID, artifactID); err != nil {
		return model.NilID, fmt.Errorf("add ownership: %w", err)
	}

	// 5. Save initial projection (Stored).
	proj := &model.ArtifactProjection{
		ArtifactID: artifactID,
		Revision:   1,
		Summary:    summary,
		Readiness:  model.ReadinessStored,
		ValidFrom:  time.Now(),
	}
	if err := p.artifacts.SaveProjection(ctx, proj); err != nil {
		return model.NilID, fmt.Errorf("save projection: %w", err)
	}

	// 6. Embed content.
	batch, err := p.embedder.Embed(ctx, []string{string(content)})
	if err != nil {
		return model.NilID, fmt.Errorf("embed: %w", err)
	}
	if len(batch.Vectors) == 0 {
		return model.NilID, errors.New("embedder returned empty batch")
	}

	// 7. Persist embedding.
	refID, err := p.embeddings.SaveEmbedding(ctx, artifactID, batch.Vectors[0])
	if err != nil {
		return model.NilID, fmt.Errorf("save embedding: %w", err)
	}

	// 8. Update projection: Embedded + embedding ref.
	proj, err = p.artifacts.LoadProjection(ctx, model.ProjectionKey{ArtifactID: artifactID, Revision: 1})
	if err != nil {
		return model.NilID, fmt.Errorf("load projection: %w", err)
	}
	proj.Readiness.Add(model.ReadinessEmbedded)
	proj.EmbeddingRef = refID
	proj.ModelVersion = p.embedder.Model()
	if err := p.artifacts.SaveProjection(ctx, proj); err != nil {
		return model.NilID, fmt.Errorf("save projection embedded: %w", err)
	}

	// 9. Rebuild BM25 index.
	// Skip when there are no documents — empty corpus is valid.
	docs, err := p.loadAllIndexableDocuments(ctx)
	if err != nil {
		return model.NilID, fmt.Errorf("load indexable documents: %w", err)
	}
	if len(docs) > 0 {
		if err := p.bm25.Index(ctx, docs); err != nil {
			return model.NilID, fmt.Errorf("bm25 index: %w", err)
		}
	}

	// 10. Update projection: Indexed.
	proj.Readiness.Add(model.ReadinessIndexed)
	if err := p.artifacts.SaveProjection(ctx, proj); err != nil {
		return model.NilID, fmt.Errorf("save projection indexed: %w", err)
	}

	return artifactID, nil
}

// loadAllIndexableDocuments loads all stored documents for BM25 rebuild.
func (p *IngestionPipeline) loadAllIndexableDocuments(ctx context.Context) ([]retrieval.TextDocument, error) {
	ids, err := p.artifacts.ListArtifactsByType(ctx, model.NodeTypeArtifact)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}

	var docs []retrieval.TextDocument
	for _, id := range ids {
		proj, err := p.artifacts.LatestProjection(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("projection %s: %w", id, err)
		}
		if !proj.Readiness.Has(model.ReadinessStored) {
			continue
		}
		art, err := p.artifacts.LoadArtifact(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("artifact %s: %w", id, err)
		}
		rc, err := p.cas.Open(ctx, art.ContentHash)
		if err != nil {
			return nil, fmt.Errorf("cas open %s: %w", id, err)
		}
		content, err := readAllAndClose(rc)
		if err != nil {
			return nil, fmt.Errorf("read content %s: %w", id, err)
		}
		if strings.TrimSpace(string(content)) == "" {
			continue
		}
		docs = append(docs, retrieval.TextDocument{
			ID:      id,
			Content: string(content),
		})
	}
	return docs, nil
}

// readAllAndClose is a helper that reads all content from a reader and closes it.
func readAllAndClose(rc io.ReadCloser) ([]byte, error) {
	defer rc.Close()
	return io.ReadAll(rc)
}
