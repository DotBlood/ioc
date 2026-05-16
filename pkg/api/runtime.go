package api

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/DotBlood/ioc/internal/embedding"
	"github.com/DotBlood/ioc/internal/graph"
	"github.com/DotBlood/ioc/internal/knowledge"
	"github.com/DotBlood/ioc/internal/model"
	"github.com/DotBlood/ioc/internal/pipeline"
	"github.com/DotBlood/ioc/internal/retrieval"
	"github.com/DotBlood/ioc/internal/store"
)

// Config configures an IOC runtime.
type Config struct {
	RootDir string
}

// Runtime provides an embeddable IOC runtime for programmatic use.
//
// Concurrency (v0.1):
//   - Concurrent Query/Trace operations are safe.
//   - Concurrent mutation (CreateScope, AddArtifact, ArchiveScope,
//     RestoreScope) is NOT safe — callers must serialize mutations.
//   - Full transactional concurrency is deferred to v0.2+.
type Runtime struct {
	rootDir  string
	disk     *store.DiskStore
	cas      *store.CAS
	embStore *store.EmbeddingStore

	emb     embedding.Embedder
	embedMu sync.Mutex
}

// Open opens an IOC repository and returns a Runtime handle.
// Returns ErrNotInitialized if the repository does not exist.
func Open(_ context.Context, cfg Config) (*Runtime, error) {
	if err := ensureInitialized(cfg.RootDir); err != nil {
		return nil, err
	}

	disk, err := store.OpenOrCreate(filepath.Join(cfg.RootDir, "db", "ioc.db"))
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	cas := store.NewCAS(filepath.Join(cfg.RootDir, "cas"))

	embStore, err := store.OpenEmbeddingStore(filepath.Join(cfg.RootDir, "emb", "default.emb"), 384)
	if err != nil {
		disk.Close()
		return nil, fmt.Errorf("open embeddings: %w", err)
	}

	return &Runtime{
		rootDir:  cfg.RootDir,
		disk:     disk,
		cas:      cas,
		embStore: embStore,
	}, nil
}

// Close shuts down the runtime and releases all resources.
func (r *Runtime) Close() error {
	r.embStore.Close()
	return r.disk.Close()
}

func ensureInitialized(rootDir string) error {
	if _, err := os.Stat(filepath.Join(rootDir, "db", "ioc.db")); err != nil {
		if os.IsNotExist(err) {
			return ErrNotInitialized
		}
		return err
	}
	if _, err := os.Stat(filepath.Join(rootDir, "emb", "default.emb")); err != nil {
		if os.IsNotExist(err) {
			return ErrNotInitialized
		}
		return err
	}
	if _, err := os.Stat(filepath.Join(rootDir, "cas")); err != nil {
		if os.IsNotExist(err) {
			return ErrNotInitialized
		}
		return err
	}
	return nil
}

// getEmbedder lazily creates a MockEmbedder on first use.
func (r *Runtime) getEmbedder() embedding.Embedder {
	r.embedMu.Lock()
	defer r.embedMu.Unlock()
	if r.emb == nil {
		r.emb = embedding.NewMockEmbedder(embedding.DefaultMockConfig())
	}
	return r.emb
}

// helpers mirror CLI patterns but are unexported.
func (r *Runtime) artifactStore() *store.ArtifactStore {
	return store.NewArtifactStore(r.disk)
}

func (r *Runtime) anchorStore() *store.AnchorStore {
	return store.NewAnchorStore(r.disk)
}

// retrievalEngine rebuilds indexes from persisted data.
// Retrieval indexes are process-local and rebuilt per invocation in v0.1.
func (r *Runtime) retrievalEngine(ctx context.Context) (*retrieval.Engine, error) {
	embedder := r.getEmbedder()
	dims := 384

	vectorIdx := retrieval.NewBruteForceIndex(dims)
	textIdx := retrieval.NewBM25Index()

	ids, err := r.disk.ListAllArtifactIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}

	var entries []retrieval.IndexEntry
	var docs []retrieval.TextDocument

	for _, id := range ids {
		proj, err := r.artifactStore().LatestProjection(ctx, id)
		if err != nil {
			continue
		}
		art, err := r.disk.LoadNode(id)
		if err != nil {
			continue
		}
		if proj.EmbeddingRef != 0 {
			vec, err := r.embStore.Get(proj.EmbeddingRef)
			if err == nil {
				entries = append(entries, retrieval.IndexEntry{ID: id, Vector: vec})
			}
		}
		if proj.Readiness.Has(model.ReadinessStored) {
			rc, err := r.cas.Open(ctx, art.ContentHash)
			if err != nil {
				continue
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				continue
			}
			if len(content) > 0 {
				docs = append(docs, retrieval.TextDocument{ID: id, Content: string(content)})
			}
		}
	}

	if len(entries) > 0 {
		if err := vectorIdx.Upsert(ctx, entries); err != nil {
			return nil, fmt.Errorf("build vector index: %w", err)
		}
	}
	if len(docs) > 0 {
		if err := textIdx.Index(ctx, docs); err != nil {
			return nil, fmt.Errorf("build text index: %w", err)
		}
	}

	return retrieval.NewEngine(embedder, vectorIdx, textIdx, retrieval.NewRRF(60)), nil
}

// buildGraph loads graph state from disk for archive operations.
// Archive operations rebuild an in-memory graph per invocation in v0.1.
func (r *Runtime) buildGraph(ctx context.Context) (*graph.StatefulGraph, error) {
	g := graph.NewStatefulGraph()
	snap, err := r.disk.LoadSnapshot()
	if err != nil {
		return nil, fmt.Errorf("load snapshot: %w", err)
	}
	for _, n := range snap.Nodes {
		if err := g.AddNode(ctx, n); err != nil {
			return nil, fmt.Errorf("add node: %w", err)
		}
	}
	for _, e := range snap.Edges {
		if err := g.AddEdge(ctx, e); err != nil {
			return nil, fmt.Errorf("add edge: %w", err)
		}
	}
	g.RebuildRuntimeState()
	return g, nil
}

// archivePipeline builds an archive pipeline for scope archive/restore.
func (r *Runtime) archivePipeline(ctx context.Context) (*pipeline.ArchivePipeline, error) {
	g, err := r.buildGraph(ctx)
	if err != nil {
		return nil, err
	}
	anchorCreator := knowledge.NewAnchorCreator(g, r.anchorStore())
	return pipeline.NewArchivePipeline(
		g,
		g,
		anchorCreator,
		r.anchorStore(),
		&archiveArtifactCreator{store: r.artifactStore(), disk: r.disk},
		&diskLifecycleAdapter{disk: r.disk},
	), nil
}

// archive adapters — same pattern as cmd/iocctl/root.go.
type archiveArtifactCreator struct {
	store *store.ArtifactStore
	disk  *store.DiskStore
}

func (a *archiveArtifactCreator) SaveSummaryArtifact(_ context.Context, summary string, scopeID model.ScopeID) (model.ID, error) {
	id := model.NewID()
	art := &model.Artifact{
		ArtifactID: id,
		NodeType:   model.NodeTypeSummary,
		Scope:      scopeID,
		CreatedAt:  time.Now(),
	}
	if err := a.disk.SaveNode(art); err != nil {
		return model.NilID, err
	}
	proj := &model.ArtifactProjection{
		ArtifactID: id,
		Revision:   1,
		Summary:    summary,
		Readiness:  model.ReadinessStored,
		ValidFrom:  time.Now(),
	}
	if err := a.disk.SaveProjection(proj); err != nil {
		return model.NilID, err
	}
	return id, nil
}

func (a *archiveArtifactCreator) StoreEdge(_ context.Context, edge *model.Edge) error {
	return a.disk.SaveEdge(edge)
}

type diskLifecycleAdapter struct {
	disk *store.DiskStore
}

func (a *diskLifecycleAdapter) Transition(ctx context.Context, scopeID model.ScopeID, to model.LifecycleState) error {
	return a.disk.SetScopeState(ctx, scopeID, to)
}
