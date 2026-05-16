package main

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
	"github.com/spf13/cobra"
)

type appState struct {
	rootDir  string
	disk     *store.DiskStore
	cas      *store.CAS
	embStore *store.EmbeddingStore

	embedder embedding.Embedder
	embedMu  sync.Mutex
}

func defaultRootDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".ioc")
	}
	return filepath.Join(home, ".ioc")
}

func openState(rootDir string) (*appState, error) {
	disk, err := store.OpenOrCreate(filepath.Join(rootDir, "db", "ioc.db"))
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	cas := store.NewCAS(filepath.Join(rootDir, "cas"))

	embStore, err := store.OpenEmbeddingStore(filepath.Join(rootDir, "emb", "default.emb"), 384)
	if err != nil {
		disk.Close()
		return nil, fmt.Errorf("open embeddings: %w", err)
	}

	return &appState{
		rootDir:  rootDir,
		disk:     disk,
		cas:      cas,
		embStore: embStore,
	}, nil
}

func (s *appState) Close() error {
	s.embStore.Close()
	return s.disk.Close()
}

func (s *appState) ArtifactStore() *store.ArtifactStore {
	return store.NewArtifactStore(s.disk)
}

func (s *appState) AnchorStore() *store.AnchorStore {
	return store.NewAnchorStore(s.disk)
}

func (s *appState) Embedder() embedding.Embedder {
	s.embedMu.Lock()
	defer s.embedMu.Unlock()
	if s.embedder == nil {
		s.embedder = embedding.NewMockEmbedder(embedding.DefaultMockConfig())
	}
	return s.embedder
}

// Retrieval indexes are process-local and rebuilt per CLI invocation in v0.1.
// Persistent incremental indexing is deferred to future runtime phases.
func (s *appState) RetrievalEngine(ctx context.Context) (*retrieval.Engine, error) {
	embedder := s.Embedder()
	dims := 384

	vectorIdx := retrieval.NewBruteForceIndex(dims)
	textIdx := retrieval.NewBM25Index()

	ids, err := s.disk.ListAllArtifactIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}

	var entries []retrieval.IndexEntry
	var docs []retrieval.TextDocument

	for _, id := range ids {
		proj, err := s.ArtifactStore().LatestProjection(ctx, id)
		if err != nil {
			continue
		}
		art, err := s.disk.LoadNode(id)
		if err != nil {
			continue
		}
		if proj.EmbeddingRef != 0 {
			vec, err := s.embStore.Get(proj.EmbeddingRef)
			if err == nil {
				entries = append(entries, retrieval.IndexEntry{ID: id, Vector: vec})
			}
		}
		if proj.Readiness.Has(model.ReadinessStored) {
			rc, err := s.cas.Open(ctx, art.ContentHash)
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

// Archive operations rebuild an in-memory graph snapshot per invocation in v0.1.
// Archive workflows are infrequent and prioritize deterministic reconstruction
// over persistent runtime residency.
func (s *appState) BuildGraph(ctx context.Context) (*graph.StatefulGraph, error) {
	g := graph.NewStatefulGraph()
	snap, err := s.disk.LoadSnapshot()
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

func (s *appState) ArchivePipeline(ctx context.Context) (*pipeline.ArchivePipeline, error) {
	g, err := s.BuildGraph(ctx)
	if err != nil {
		return nil, err
	}
	anchorCreator := knowledge.NewAnchorCreator(g, s.AnchorStore())
	return pipeline.NewArchivePipeline(
		g,
		g,
		anchorCreator,
		s.AnchorStore(),
		&archiveArtifactCreator{store: s.ArtifactStore(), disk: s.disk},
		&diskLifecycleAdapter{disk: s.disk},
	), nil
}

// archiveArtifactCreator adapts DiskStore + ArtifactStore for archive summary creation.
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

// diskLifecycleAdapter adapts DiskStore.SetScopeState to knowledge.LifecycleTransitioner.
type diskLifecycleAdapter struct {
	disk *store.DiskStore
}

func (a *diskLifecycleAdapter) Transition(ctx context.Context, scopeID model.ScopeID, to model.LifecycleState) error {
	return a.disk.SetScopeState(ctx, scopeID, to)
}

func requiresStore(cmd *cobra.Command) bool {
	if cmd == cmdInit {
		return false
	}
	for _, c := range cmdInit.Commands() {
		if cmd == c {
			return false
		}
	}
	return true
}

func ensureInitialized(rootDir string) error {
	if _, err := os.Stat(filepath.Join(rootDir, "db", "ioc.db")); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("repository not initialized; run `iocctl init`")
		}
		return err
	}
	if _, err := os.Stat(filepath.Join(rootDir, "emb", "default.emb")); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("embeddings not found; run `iocctl init`")
		}
		return err
	}
	if _, err := os.Stat(filepath.Join(rootDir, "cas")); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("CAS store not found; run `iocctl init`")
		}
		return err
	}
	return nil
}

func getState(cmd *cobra.Command) *appState {
	s, ok := cmd.Context().Value(ctxState{}).(*appState)
	if !ok {
		return nil
	}
	return s
}
