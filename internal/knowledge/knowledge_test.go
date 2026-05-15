package knowledge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DotBlood/ioc/internal/model"
)

// stubStore implements all the knowledge store reader interfaces for testing.
type stubStore struct {
	nodes       map[model.ID]*model.Artifact
	projections map[model.ProjectionKey]*model.ArtifactProjection
	edges       map[model.EdgeID]*model.Edge
	adjOut      map[model.ID]map[model.EdgeType][]model.ID
	adjIn       map[model.ID]map[model.EdgeType][]model.ID
	scopeState  map[model.ScopeID]*model.ScopeState
	scopeChildren map[model.ScopeID][]model.ScopeID
	activeHeads map[model.BranchName]map[model.ID]model.RevisionNumber
	revChildren map[model.ID]map[model.RevisionNumber][]model.RevisionNumber
}

func newStubStore() *stubStore {
	return &stubStore{
		nodes:       make(map[model.ID]*model.Artifact),
		projections: make(map[model.ProjectionKey]*model.ArtifactProjection),
		edges:       make(map[model.EdgeID]*model.Edge),
		adjOut:      make(map[model.ID]map[model.EdgeType][]model.ID),
		adjIn:       make(map[model.ID]map[model.EdgeType][]model.ID),
		scopeState:  make(map[model.ScopeID]*model.ScopeState),
		scopeChildren: make(map[model.ScopeID][]model.ScopeID),
		activeHeads: make(map[model.BranchName]map[model.ID]model.RevisionNumber),
		revChildren: make(map[model.ID]map[model.RevisionNumber][]model.RevisionNumber),
	}
}

func (s *stubStore) Node(_ context.Context, id model.ID) (*model.Artifact, error) {
	n, ok := s.nodes[id]
	if !ok {
		return nil, model.ErrNotFound
	}
	return n, nil
}

func (s *stubStore) Edge(_ context.Context, id model.EdgeID) (*model.Edge, error) {
	e, ok := s.edges[id]
	if !ok {
		return nil, model.ErrNotFound
	}
	return e, nil
}

func (s *stubStore) EdgesOut(_ context.Context, sourceID model.ID, edgeType model.EdgeType) ([]model.Edge, error) {
	targets, ok := s.adjOut[sourceID][edgeType]
	if !ok {
		return nil, nil
	}
	var result []model.Edge
	for _, targetID := range targets {
		for _, e := range s.edges {
			if e.Source == sourceID && e.Target == targetID && e.Type == edgeType {
				result = append(result, *e)
			}
		}
	}
	return result, nil
}

func (s *stubStore) EdgesIn(_ context.Context, targetID model.ID, edgeType model.EdgeType) ([]model.Edge, error) {
	sources, ok := s.adjIn[targetID][edgeType]
	if !ok {
		return nil, nil
	}
	var result []model.Edge
	for _, sourceID := range sources {
		for _, e := range s.edges {
			if e.Source == sourceID && e.Target == targetID && e.Type == edgeType {
				result = append(result, *e)
			}
		}
	}
	return result, nil
}

func (s *stubStore) StoreEdge(_ context.Context, edge *model.Edge) error {
	if _, exists := s.edges[edge.EdgeID]; exists {
		return model.ErrDuplicate
	}
	s.edges[edge.EdgeID] = edge
	if s.adjOut[edge.Source] == nil {
		s.adjOut[edge.Source] = make(map[model.EdgeType][]model.ID)
	}
	s.adjOut[edge.Source][edge.Type] = append(s.adjOut[edge.Source][edge.Type], edge.Target)
	if s.adjIn[edge.Target] == nil {
		s.adjIn[edge.Target] = make(map[model.EdgeType][]model.ID)
	}
	s.adjIn[edge.Target][edge.Type] = append(s.adjIn[edge.Target][edge.Type], edge.Source)
	return nil
}

func (s *stubStore) Projection(_ context.Context, key model.ProjectionKey) (*model.ArtifactProjection, error) {
	p, ok := s.projections[key]
	if !ok {
		return nil, model.ErrNotFound
	}
	return p, nil
}

func (s *stubStore) StoreProjection(_ context.Context, proj *model.ArtifactProjection) error {
	key := proj.ProjectionKey()
	s.projections[key] = proj
	return nil
}

func (s *stubStore) ActiveHeads(_ context.Context) (map[model.BranchName]map[model.ID]model.RevisionNumber, error) {
	return s.activeHeads, nil
}

func (s *stubStore) RevisionChildren(_ context.Context, artifactID model.ID, rev model.RevisionNumber) ([]model.RevisionNumber, error) {
	byArtifact, ok := s.revChildren[artifactID]
	if !ok {
		return nil, nil
	}
	return byArtifact[rev], nil
}

func (s *stubStore) ScopeState(_ context.Context, scopeID model.ScopeID) (*model.ScopeState, error) {
	state, ok := s.scopeState[scopeID]
	if !ok {
		return nil, model.ErrNotFound
	}
	return state, nil
}

func (s *stubStore) SetScopeState(_ context.Context, scopeID model.ScopeID, state model.LifecycleState) error {
	if _, ok := s.scopeState[scopeID]; !ok {
		s.scopeState[scopeID] = &model.ScopeState{ScopeID: scopeID}
	}
	s.scopeState[scopeID].State = state
	return nil
}

func (s *stubStore) ScopeChildren(_ context.Context, scopeID model.ScopeID) ([]model.ScopeID, error) {
	return s.scopeChildren[scopeID], nil
}

func (s *stubStore) ProjectionRevisions(_ context.Context, _ model.ID) ([]model.RevisionNumber, error) {
	return nil, model.ErrNotImplemented
}

func (s *stubStore) NodeScope(_ context.Context, _ model.ID) (model.ScopeID, error) {
	return "", model.ErrNotImplemented
}

func TestScopeResolver_ResolveScope(t *testing.T) {
	s := newStubStore()
	ws := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeWorkspace}
	sess := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeSession}
	s.nodes[ws.ArtifactID] = ws
	s.nodes[sess.ArtifactID] = sess

	// Ownership: sess → ws
	s.StoreEdge(context.Background(), &model.Edge{
		EdgeID: model.NewID(), Type: model.EdgeOwnership,
		Source: sess.ArtifactID, Target: ws.ArtifactID, Valid: true,
	})

	r := NewScopeResolver(s)
	scope, err := r.ResolveScope(context.Background(), sess.ArtifactID)
	if err != nil {
		t.Fatalf("ResolveScope: %v", err)
	}
	if scope == "" {
		t.Error("ResolveScope returned empty scope")
	}
}

func TestScopeResolver_IsVisibleFrom(t *testing.T) {
	s := newStubStore()
	r := NewScopeResolver(s)

	t.Run("exact match", func(t *testing.T) {
		visible, err := r.IsVisibleFrom(context.Background(), "wt:ws:s1", "wt:ws:s1")
		if err != nil {
			t.Fatalf("IsVisibleFrom: %v", err)
		}
		if !visible {
			t.Error("same scope should be visible from itself")
		}
	})

	t.Run("parent sees child", func(t *testing.T) {
		visible, err := r.IsVisibleFrom(context.Background(), "wt:ws", "wt:ws:s1")
		if err != nil {
			t.Fatalf("IsVisibleFrom: %v", err)
		}
		if !visible {
			t.Error("parent scope should be visible from child")
		}
	})

	t.Run("sibling not visible", func(t *testing.T) {
		visible, err := r.IsVisibleFrom(context.Background(), "wt:ws:s1", "wt:ws:s2")
		if err != nil {
			t.Fatalf("IsVisibleFrom: %v", err)
		}
		if visible {
			t.Error("sibling scope should not be visible from another sibling")
		}
	})

	t.Run("unrelated scope not visible", func(t *testing.T) {
		visible, err := r.IsVisibleFrom(context.Background(), "wt:other:task", "wt:ws:s1")
		if err != nil {
			t.Fatalf("IsVisibleFrom: %v", err)
		}
		if visible {
			t.Error("unrelated scope should not be visible")
		}
	})
}

func TestRevisionManager_ResolveLatest(t *testing.T) {
	s := newStubStore()
	r := NewRevisionManager(s)

	artID := model.NewID()
	s.activeHeads[model.DefaultBranch] = map[model.ID]model.RevisionNumber{
		artID: 3,
	}
	s.projections[model.ProjectionKey{ArtifactID: artID, Revision: 3}] = &model.ArtifactProjection{
		ArtifactID: artID, Revision: 3, Summary: "latest",
	}

	proj, err := r.ResolveRevision(context.Background(), model.RevisionRef{
		StableID: artID,
		Revision: 0,
		Branch:   model.DefaultBranch,
		Filter:   model.RevFilterLatest,
	})
	if err != nil {
		t.Fatalf("ResolveRevision: %v", err)
	}
	if proj.Revision != 3 {
		t.Errorf("expected revision 3, got %d", proj.Revision)
	}
}

func TestRevisionManager_ResolvePinned(t *testing.T) {
	s := newStubStore()
	r := NewRevisionManager(s)

	artID := model.NewID()
	s.projections[model.ProjectionKey{ArtifactID: artID, Revision: 42}] = &model.ArtifactProjection{
		ArtifactID: artID, Revision: 42, Summary: "pinned",
	}

	proj, err := r.ResolveRevision(context.Background(), model.RevisionRef{
		StableID: artID,
		Revision: 42,
		Filter:   model.RevFilterPinned,
	})
	if err != nil {
		t.Fatalf("ResolveRevision: %v", err)
	}
	if proj.Revision != 42 {
		t.Errorf("expected revision 42, got %d", proj.Revision)
	}
}

func TestRevisionManager_IsActive(t *testing.T) {
	s := newStubStore()
	r := NewRevisionManager(s)

	artID := model.NewID()
	s.activeHeads[model.DefaultBranch] = map[model.ID]model.RevisionNumber{
		artID: 2,
	}

	t.Run("latest is active", func(t *testing.T) {
		proj := &model.ArtifactProjection{ArtifactID: artID, Revision: 2}
		active, err := r.IsActive(context.Background(), proj)
		if err != nil {
			t.Fatalf("IsActive: %v", err)
		}
		if !active {
			t.Error("head revision should be active")
		}
	})

	t.Run("old revision is not active", func(t *testing.T) {
		proj := &model.ArtifactProjection{ArtifactID: artID, Revision: 1}
		active, err := r.IsActive(context.Background(), proj)
		if err != nil {
			t.Fatalf("IsActive: %v", err)
		}
		if active {
			t.Error("old revision should not be active")
		}
	})
}

func TestRevisionManager_IsSuperseded(t *testing.T) {
	s := newStubStore()
	r := NewRevisionManager(s)

	artID := model.NewID()
	s.revChildren[artID] = map[model.RevisionNumber][]model.RevisionNumber{
		1: {2},
	}

	t.Run("revision with child is superseded", func(t *testing.T) {
		proj := &model.ArtifactProjection{ArtifactID: artID, Revision: 1}
		super, err := r.IsSuperseded(context.Background(), proj)
		if err != nil {
			t.Fatalf("IsSuperseded: %v", err)
		}
		if !super {
			t.Error("revision with child should be superseded")
		}
	})

	t.Run("revision without child is not superseded", func(t *testing.T) {
		proj := &model.ArtifactProjection{ArtifactID: artID, Revision: 2}
		super, err := r.IsSuperseded(context.Background(), proj)
		if err != nil {
			t.Fatalf("IsSuperseded: %v", err)
		}
		if super {
			t.Error("revision without child should not be superseded")
		}
	})
}

func TestLifecycleManager_Transition(t *testing.T) {
	s := newStubStore()
	r := NewLifecycleManager(s)

	scopeID := model.ScopeID("test:scope")
	s.scopeState[scopeID] = &model.ScopeState{ScopeID: scopeID, State: model.LifecycleDraft}

	t.Run("draft to active", func(t *testing.T) {
		if err := r.Transition(context.Background(), scopeID, model.LifecycleActive); err != nil {
			t.Fatalf("Transition draft→active: %v", err)
		}
	})

	t.Run("draft to deleted (invalid)", func(t *testing.T) {
		err2 := r.Transition(context.Background(), scopeID, model.LifecycleDeleted)
		if err2 == nil {
			t.Error("Transition draft→deleted: expected error, got nil")
		}
	})
}

func TestLifecycleManager_EnforceInvariants(t *testing.T) {
	s := newStubStore()
	r := NewLifecycleManager(s)

	scopeID := model.ScopeID("wt:ws")
	s.scopeState[scopeID] = &model.ScopeState{ScopeID: scopeID, State: model.LifecycleActive}
	s.scopeChildren[scopeID] = []model.ScopeID{
		"wt:ws:s1",
	}

	violations := r.EnforceInvariants(context.Background(), scopeID)
	for _, v := range violations {
		t.Logf("violation: %s", v)
	}
	if len(violations) > 0 {
		t.Errorf("expected 0 violations, got %d", len(violations))
	}
}

func TestLineageTracker_RecordLineage(t *testing.T) {
	s := newStubStore()
	r := NewLineageTracker(s)

	parent := &model.Artifact{ArtifactID: model.NewID(), CreatedAt: time.Now().Add(-time.Hour)}
	child := &model.Artifact{ArtifactID: model.NewID(), CreatedAt: time.Now()}
	s.nodes[parent.ArtifactID] = parent
	s.nodes[child.ArtifactID] = child

	if err := r.RecordLineage(context.Background(), parent.ArtifactID, child.ArtifactID); err != nil {
		t.Fatalf("RecordLineage: %v", err)
	}

	// Cycle check: should fail when trying to make parent a child of child
	if err := r.RecordLineage(context.Background(), child.ArtifactID, parent.ArtifactID); err == nil || !errors.Is(err, model.ErrCycleDetected) {
		t.Errorf("RecordLineage cycle: want ErrCycleDetected, got %v", err)
	}
}

func TestLineageTracker_Provenance(t *testing.T) {
	s := newStubStore()
	r := NewLineageTracker(s)

	n0 := &model.Artifact{ArtifactID: model.NewID(), CreatedAt: time.Now().Add(-3 * time.Hour)}
	n1 := &model.Artifact{ArtifactID: model.NewID(), CreatedAt: time.Now().Add(-2 * time.Hour)}
	n2 := &model.Artifact{ArtifactID: model.NewID(), CreatedAt: time.Now().Add(-time.Hour)}
	for _, n := range []*model.Artifact{n0, n1, n2} {
		s.nodes[n.ArtifactID] = n
	}

	r.RecordLineage(context.Background(), n0.ArtifactID, n1.ArtifactID)
	r.RecordLineage(context.Background(), n1.ArtifactID, n2.ArtifactID)

	chain, err := r.Provenance(context.Background(), n2.ArtifactID)
	if err != nil {
		t.Fatalf("Provenance: %v", err)
	}
	if len(chain) != 2 {
		t.Errorf("expected 2 provenance edges, got %d", len(chain))
	}
}

func TestLineageTracker_RecordOwnership(t *testing.T) {
	s := newStubStore()
	r := NewLineageTracker(s)

	node := model.NewID()
	parent := model.NewID()

	if err := r.RecordOwnership(context.Background(), node, parent); err != nil {
		t.Fatalf("RecordOwnership: %v", err)
	}

	edges, err := s.EdgesOut(context.Background(), node, model.EdgeOwnership)
	if err != nil {
		t.Fatalf("EdgesOut: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 ownership edge, got %d", len(edges))
	}
	if edges[0].Target != parent {
		t.Errorf("expected target %v, got %v", parent, edges[0].Target)
	}
}

func TestContextAssembler_Plan(t *testing.T) {
	s := newStubStore()
	scope := NewScopeResolver(s)
	revision := NewRevisionManager(s)
	lifecycle := NewLifecycleManager(s)
	a := NewContextAssembler(scope, revision, lifecycle, s)

	opts := model.RetrievalOpts{
		ScopeFilter: &model.ScopeFilter{MaxDepth: 3},
		TokenBudget: 4000,
	}

	plan, err := a.Plan(context.Background(), "test query", "wt:ws:s1", opts)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.PrimaryScope != "wt:ws:s1" {
		t.Errorf("expected primary scope wt:ws:s1, got %s", plan.PrimaryScope)
	}
	if plan.TokenBudget != 4000 {
		t.Errorf("expected token budget 4000, got %d", plan.TokenBudget)
	}
}

func TestPolicyEnforcer_DefaultRetrievalScope(t *testing.T) {
	s := newStubStore()
	p := NewPolicyEnforcer(s, DefaultArchivePolicy())

	scope := p.DefaultRetrievalScope("wt:ws")
	if scope.IncludeArchived {
		t.Error("default should not include archived")
	}
	if scope.IncludeDetached {
		t.Error("default should not include detached")
	}
	if scope.MaxDepth != 3 {
		t.Errorf("expected max depth 3, got %d", scope.MaxDepth)
	}
}

func TestPolicyEnforcer_ShouldArchive(t *testing.T) {
	s := newStubStore()
	p := NewPolicyEnforcer(s, DefaultArchivePolicy())

	scopeID := model.ScopeID("wt:ws:archivable")
	s.scopeState[scopeID] = &model.ScopeState{
		ScopeID: scopeID,
		State:   model.LifecycleActive,
	}

	should, err := p.ShouldArchive(context.Background(), scopeID)
	if err != nil {
		t.Fatalf("ShouldArchive: %v", err)
	}
	if should {
		t.Error("active scope should not be archived immediately")
	}
}
