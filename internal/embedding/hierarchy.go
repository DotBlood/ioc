package embedding

import (
	"context"
	"sort"

	"github.com/DotBlood/ioc/internal/model"
)

// AggregationLevel identifies the level in the scope hierarchy.
type AggregationLevel uint8

const (
	LevelChunk    AggregationLevel = 0 // raw embedding from embedder
	LevelArtifact AggregationLevel = 1 // average of chunk embeddings
	LevelSession  AggregationLevel = 2 // average of artifact embeddings
	// LevelWorkspace deferred — too coarse for v0.1.
)

// AggregatedEmbedding is a computed centroid at a hierarchy level.
//
// Vector is immutable after creation.
// Callers MUST NOT mutate returned slices.
//
// Aggregates do NOT have Revision numbers — they are NOT graph entities.
// Future caches are advisory only. Aggregates are always recomputable from leaf embeddings.
// Cache loss MUST NOT affect correctness.
type AggregatedEmbedding struct {
	ParentScope model.ScopeID
	Level       AggregationLevel
	Vector      []float32
	ChildCount  int
	TotalTokens int
	Model       string
	Dimension   int
}

// ScopeHierarchy provides scope tree traversal for hierarchy computation.
//
// Children MUST return scopes in stable order (lexical ScopeID recommended).
// Aggregation topology MUST be acyclic (structural containment only).
type ScopeHierarchy interface {
	Children(ctx context.Context, parent model.ScopeID) ([]model.ScopeID, error)
	ArtifactsInScope(ctx context.Context, scopeID model.ScopeID) ([]model.ID, error)
}

// EmbeddingReader reads leaf embeddings from storage.
type EmbeddingReader interface {
	Get(ref model.EmbeddingRefID) ([]float32, error)
}

// ProjectionLoader loads artifact projections for embedding resolution.
type ProjectionLoader interface {
	LatestProjection(ctx context.Context, artifactID model.ID) (*model.ArtifactProjection, error)
}

// Hierarchy computes aggregated embeddings at each level of the scope tree.
//
// Algorithm:
//   For each artifact in scope:
//     load chunk embeddings
//     WeightedAverage → artifact centroid (via caller, not stored)
//   For each session:
//     collect artifact centroids in that session
//     WeightedAverage → session centroid
//
// Hierarchy does NOT do inference — it only reads existing embeddings.
type Hierarchy struct {
	scopeReader     ScopeHierarchy
	embeddingReader EmbeddingReader
	projLoader      ProjectionLoader
	model           string
	dims            int
}

// NewHierarchy creates a new hierarchy computer.
func NewHierarchy(
	scopeReader ScopeHierarchy,
	embeddingReader EmbeddingReader,
	projLoader ProjectionLoader,
	model string,
	dims int,
) *Hierarchy {
	return &Hierarchy{
		scopeReader:     scopeReader,
		embeddingReader: embeddingReader,
		projLoader:      projLoader,
		model:           model,
		dims:            dims,
	}
}

// ComputeAggregate computes aggregated embeddings for session-level centroids.
//
// Results are returned as AggregatedEmbedding slices (runtime-only, not persisted).
// Does NOT traverse scope graph recursively — direct children only.
// Does NOT store results in EmbeddingStore yet.
//
// Determinism: child scopes are sorted by lexical ScopeID order.
// Artifacts are sorted by lexical ID order.
func (h *Hierarchy) ComputeAggregate(ctx context.Context, scopeID model.ScopeID) ([]AggregatedEmbedding, error) {
	children, err := h.scopeReader.Children(ctx, scopeID)
	if err != nil {
		return nil, err
	}

	// MUST sort for determinism.
	sort.Slice(children, func(i, j int) bool {
		return string(children[i]) < string(children[j])
	})

	var results []AggregatedEmbedding

	for _, child := range children {
		artifacts, err := h.scopeReader.ArtifactsInScope(ctx, child)
		if err != nil {
			continue
		}

		// MUST sort for determinism.
		sort.Slice(artifacts, func(i, j int) bool {
			return artifacts[i].String() < artifacts[j].String()
		})

		var vectors [][]float32
		var weights []int

		for _, artID := range artifacts {
			proj, err := h.projLoader.LatestProjection(ctx, artID)
			if err != nil {
				continue
			}
			if proj.EmbeddingRef == 0 {
				continue // not computed yet
			}

			vec, err := h.embeddingReader.Get(proj.EmbeddingRef)
			if err != nil {
				continue
			}
			if len(vec) != h.dims {
				continue
			}

			vectors = append(vectors, vec)
			weights = append(weights, estimateWeight(proj))
		}

		if len(vectors) == 0 {
			continue
		}

		centroid, err := WeightedAverage(vectors, weights)
		if err != nil {
			continue
		}

		results = append(results, AggregatedEmbedding{
			ParentScope: child,
			Level:       LevelSession,
			Vector:      centroid,
			ChildCount:  len(vectors),
			TotalTokens: sumWeights(weights),
			Model:       h.model,
			Dimension:   h.dims,
		})
	}

	return results, nil
}

// estimateWeight returns an approximate token count for weighting.
func estimateWeight(proj *model.ArtifactProjection) int {
	if len(proj.Summary) > 0 {
		return len(proj.Summary) / 4
	}
	// Fallback: uniform weight if no text available.
	return 1
}

func sumWeights(w []int) int {
	s := 0
	for _, v := range w {
		s += v
	}
	return s
}
