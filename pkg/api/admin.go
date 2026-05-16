package api

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/DotBlood/ioc/internal/model"
	"github.com/DotBlood/ioc/internal/pipeline"
	"github.com/DotBlood/ioc/internal/retrieval"
	"github.com/DotBlood/ioc/internal/store"
)

// CreateScope creates a new scope in the graph.
//
// worktree — ParentID must be empty (root scope).
// workspace — ParentID must reference a worktree.
// session — ParentID must reference a workspace.
//
// Returns ErrInvalidInput for invalid type/parent combinations.
func (r *Runtime) CreateScope(ctx context.Context, req CreateScopeRequest) (*ScopeInfo, error) {
	nodeType, err := parseNodeType(req.Type)
	if err != nil {
		return nil, fmt.Errorf("create scope: %w", err)
	}

	// Validate parent based on type.
	var parentID model.ID
	switch nodeType {
	case model.NodeTypeWorktree:
		if req.ParentID != "" {
			return nil, fmt.Errorf("create scope: worktree must not have a parent: %w", ErrInvalidInput)
		}
	case model.NodeTypeWorkspace, model.NodeTypeSession:
		if req.ParentID == "" {
			return nil, fmt.Errorf("create scope: %s requires a parent: %w", req.Type, ErrInvalidInput)
		}
		parentID, err = model.ParseID(req.ParentID)
		if err != nil {
			return nil, fmt.Errorf("create scope: invalid parent ID: %w", ErrInvalidInput)
		}
	default:
		return nil, fmt.Errorf("create scope: unsupported type %q: %w", req.Type, ErrInvalidInput)
	}

	// Validate parent type chain.
	if !parentID.IsZero() {
		parentType, err := r.disk.NodeType(parentID)
		if err != nil {
			return nil, fmt.Errorf("create scope: parent node: %w", err)
		}
		if err := validateParentPair(parentType, nodeType); err != nil {
			return nil, err
		}
	}

	id := model.NewID()
	scopeID := model.ScopeID(id.String())

	node := &model.Artifact{
		ArtifactID: id,
		NodeType:   nodeType,
		Scope:      scopeID,
		CreatedAt:  time.Now(),
	}
	if err := r.disk.SaveNode(node); err != nil {
		return nil, fmt.Errorf("create scope: save node: %w", err)
	}

	// Ownership edge: parent → child.
	if !parentID.IsZero() {
		edge := &model.Edge{
			EdgeID:    model.NewID(),
			Type:      model.EdgeOwnership,
			Direction: model.DirectionDirected,
			Source:    parentID,
			Target:    id,
			Valid:     true,
			ValidFrom: time.Now(),
		}
		if err := r.disk.SaveEdge(edge); err != nil {
			return nil, fmt.Errorf("create scope: save ownership edge: %w", err)
		}

		parentScopeID := model.ScopeID(parentID.String())
		if err := r.disk.SaveScopeChild(parentScopeID, scopeID); err != nil {
			return nil, fmt.Errorf("create scope: save scope child: %w", err)
		}
	}

	scopeState := &model.ScopeState{
		ScopeID:   scopeID,
		Type:      nodeType,
		State:     model.LifecycleDraft,
		ParentID:  parentID,
		CreatedAt: time.Now(),
	}
	if err := r.disk.SaveScopeState(scopeState); err != nil {
		return nil, fmt.Errorf("create scope: save scope state: %w", err)
	}

	return scopeStateToInfo(scopeState), nil
}

// ListScopes returns all scopes with their current lifecycle state.
func (r *Runtime) ListScopes(ctx context.Context) ([]ScopeInfo, error) {
	states, err := r.disk.ListAllScopeStates()
	if err != nil {
		return nil, fmt.Errorf("list scopes: %w", err)
	}

	sort.Slice(states, func(i, j int) bool {
		return string(states[i].ScopeID) < string(states[j].ScopeID)
	})

	infos := make([]ScopeInfo, len(states))
	for i, s := range states {
		infos[i] = *scopeStateToInfo(&s)
	}
	return infos, nil
}

// ArchiveScope freezes, snapshots, and transitions a scope to archived.
// Returns the anchor ID string on success.
func (r *Runtime) ArchiveScope(ctx context.Context, scopeID string) (string, error) {
	sid := model.ScopeID(scopeID)

	if _, err := r.disk.ScopeState(ctx, sid); err != nil {
		return "", fmt.Errorf("archive scope: %w", ErrNotFound)
	}

	ap, err := r.archivePipeline(ctx)
	if err != nil {
		return "", fmt.Errorf("archive scope: %w", err)
	}

	anchorID, err := ap.Archive(ctx, sid)
	if err != nil {
		return "", fmt.Errorf("archive scope: %w", err)
	}

	return anchorID.String(), nil
}

// RestoreScope restores a scope from an anchor snapshot.
func (r *Runtime) RestoreScope(ctx context.Context, anchorID string) error {
	id, err := model.ParseID(anchorID)
	if err != nil {
		return fmt.Errorf("restore scope: invalid anchor ID: %w", ErrInvalidInput)
	}

	ap, err := r.archivePipeline(ctx)
	if err != nil {
		return fmt.Errorf("restore scope: %w", err)
	}

	if err := ap.Restore(ctx, model.AnchorID(id)); err != nil {
		return fmt.Errorf("restore scope: %w", err)
	}

	return nil
}

// AddArtifact ingests content into a scope.
// Returns the new artifact ID string on success.
//
// content is stored deduplicated (SHA-256/CAS), embedded,
// BM25-indexed, and attributed to scopeID.
func (r *Runtime) AddArtifact(ctx context.Context, scopeID string, content []byte, summary string) (string, error) {
	if len(content) == 0 {
		return "", fmt.Errorf("add artifact: empty content: %w", ErrInvalidInput)
	}

	sid := model.ScopeID(scopeID)
	if _, err := r.disk.ScopeState(ctx, sid); err != nil {
		return "", fmt.Errorf("add artifact: %w", ErrNotFound)
	}

	bm25 := retrieval.NewBM25Index()
	pipe := pipeline.NewIngestionPipelineFromStore(
		r.cas,
		r.artifactStore(),
		r.embStore,
		&ownershipWriter{disk: r.disk},
		&scopeResolver{},
		r.getEmbedder(),
		bm25,
	)

	artifactID, err := pipe.Process(ctx, content, sid, summary)
	if err != nil {
		return "", fmt.Errorf("add artifact: %w", err)
	}

	return artifactID.String(), nil
}

// ============================================================
// Internal helpers
// ============================================================

func parseNodeType(s string) (model.NodeType, error) {
	switch s {
	case "worktree":
		return model.NodeTypeWorktree, nil
	case "workspace":
		return model.NodeTypeWorkspace, nil
	case "session":
		return model.NodeTypeSession, nil
	default:
		return 0, fmt.Errorf("unknown scope type %q: use worktree, workspace, or session: %w", s, ErrInvalidInput)
	}
}

func validateParentPair(parent, child model.NodeType) error {
	switch child {
	case model.NodeTypeWorkspace:
		if parent != model.NodeTypeWorktree {
			return fmt.Errorf("workspace parent must be worktree, got %s", parent)
		}
	case model.NodeTypeSession:
		if parent != model.NodeTypeWorkspace {
			return fmt.Errorf("session parent must be workspace, got %s", parent)
		}
	}
	return nil
}

func scopeStateToInfo(s *model.ScopeState) *ScopeInfo {
	parentID := ""
	if !s.ParentID.IsZero() {
		parentID = s.ParentID.String()
	}
	return &ScopeInfo{
		ScopeID:   string(s.ScopeID),
		Type:      s.Type.String(),
		State:     s.State.String(),
		ParentID:  parentID,
		CreatedAt: s.CreatedAt,
	}
}

// ============================================================
// Pipeline adapters
// ============================================================

type ownershipWriter struct {
	disk *store.DiskStore
}

func (w *ownershipWriter) AddOwnership(_ context.Context, parent model.ID, child model.ID) error {
	edge := &model.Edge{
		EdgeID:    model.NewID(),
		Type:      model.EdgeOwnership,
		Direction: model.DirectionDirected,
		Source:    parent,
		Target:    child,
		Valid:     true,
		ValidFrom: time.Now(),
	}
	return w.disk.SaveEdge(edge)
}

type scopeResolver struct{}

func (*scopeResolver) ResolveScope(_ context.Context, scopeID model.ScopeID) (model.ID, error) {
	return model.ParseID(string(scopeID))
}
