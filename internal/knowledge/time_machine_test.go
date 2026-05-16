package knowledge

import (
	"context"
	"testing"
	"time"

	"github.com/DotBlood/ioc/internal/model"
)

// timeMocks implements all three TimeMachine interfaces for testing.
type timeMocks struct {
	anchors     []model.Anchor
	artifacts   map[model.ID]*model.Artifact
	projections map[model.ProjectionKey]*model.ArtifactProjection
	projKeys    map[model.ID][]model.ProjectionKey
	edges       map[model.EdgeID]*model.Edge
}

func newTimeMocks() *timeMocks {
	return &timeMocks{
		artifacts:   make(map[model.ID]*model.Artifact),
		projections: make(map[model.ProjectionKey]*model.ArtifactProjection),
		projKeys:    make(map[model.ID][]model.ProjectionKey),
		edges:       make(map[model.EdgeID]*model.Edge),
	}
}

func (m *timeMocks) addArtifact(id model.ID) {
	m.artifacts[id] = &model.Artifact{
		ArtifactID: id,
		NodeType:   model.NodeTypeArtifact,
	}
}

func (m *timeMocks) addProjection(artifactID model.ID, rev model.RevisionNumber, validFrom time.Time, validTo *time.Time) {
	key := model.ProjectionKey{ArtifactID: artifactID, Revision: rev}
	m.projections[key] = &model.ArtifactProjection{
		ArtifactID: artifactID,
		Revision:   rev,
		ValidFrom:  validFrom,
		ValidTo:    validTo,
	}
	m.projKeys[artifactID] = append(m.projKeys[artifactID], key)
}

func (m *timeMocks) addEdge(id model.EdgeID, source, target model.ID, etype model.EdgeType, valid bool, validFrom time.Time, validTo *time.Time) {
	m.edges[id] = &model.Edge{
		EdgeID:    id,
		Source:    source,
		Target:    target,
		Type:      etype,
		Valid:     valid,
		ValidFrom: validFrom,
		ValidTo:   validTo,
	}
}

func (m *timeMocks) addAnchor(anchor model.Anchor) {
	m.anchors = append(m.anchors, anchor)
}

// TimeAnchorStore methods.
func (m *timeMocks) LatestFullAnchorBefore(_ context.Context, _ model.ScopeID, at time.Time) (*model.Anchor, error) {
	var best *model.Anchor
	for _, a := range m.anchors {
		if a.CreatedAt.After(at) {
			continue
		}
		if a.Kind != model.AnchorFull {
			continue
		}
		if best == nil || a.CreatedAt.After(best.CreatedAt) {
			a := a
			best = &a
		}
	}
	if best == nil {
		return nil, model.ErrNoHistoricalState
	}
	return best, nil
}

func (m *timeMocks) LatestAnchorsAfter(_ context.Context, _ model.ScopeID, after, to time.Time) ([]model.Anchor, error) {
	var matched []model.Anchor
	for _, a := range m.anchors {
		if !a.CreatedAt.After(after) {
			continue
		}
		if a.CreatedAt.After(to) {
			continue
		}
		matched = append(matched, a)
	}
	return matched, nil
}

// TimeArtifactStore methods.
func (m *timeMocks) LoadArtifactsByIDs(_ context.Context, ids []model.ID) ([]*model.Artifact, []model.ID, error) {
	var artifacts []*model.Artifact
	var missing []model.ID
	for _, id := range ids {
		art, ok := m.artifacts[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		artifacts = append(artifacts, art)
	}
	return artifacts, missing, nil
}

func (m *timeMocks) ListProjectionKeys(_ context.Context, artifactID model.ID) ([]model.ProjectionKey, error) {
	return m.projKeys[artifactID], nil
}

func (m *timeMocks) LoadProjection(_ context.Context, key model.ProjectionKey) (*model.ArtifactProjection, error) {
	p, ok := m.projections[key]
	if !ok {
		return nil, model.ErrNotFound
	}
	return p, nil
}

// TimeEdgeStore methods.
func (m *timeMocks) LoadEdgesByIDs(_ context.Context, ids []model.EdgeID) ([]*model.Edge, error) {
	var result []*model.Edge
	for _, id := range ids {
		e, ok := m.edges[id]
		if !ok {
			continue
		}
		result = append(result, e)
	}
	return result, nil
}

func newTimeMachine(m *timeMocks) *TimeMachine {
	return NewTimeMachine(m, m, m)
}

func TestScopeStateAt_ExactFullAnchor(t *testing.T) {
	m := newTimeMocks()
	tm := newTimeMachine(m)

	now := time.Now()
	artID := model.NewID()
	m.addArtifact(artID)
	m.addAnchor(model.Anchor{
		AnchorID:     model.AnchorID(model.NewID()),
		Kind:         model.AnchorFull,
		CreatedAt:    now,
		ArtifactRefs: []model.ID{artID},
		EdgeRefs:     nil,
	})

	scope, err := tm.ScopeStateAt(context.Background(), "test", now)
	if err != nil {
		t.Fatalf("ScopeStateAt: %v", err)
	}
	if len(scope.Artifacts) != 1 {
		t.Errorf("expected 1 artifact, got %d", len(scope.Artifacts))
	}
}

func TestScopeStateAt_WithDiffs(t *testing.T) {
	m := newTimeMocks()
	tm := newTimeMachine(m)

	t0 := time.Now().Add(-2 * time.Hour)
	t1 := time.Now().Add(-1 * time.Hour)
	t2 := time.Now()

	artID := model.NewID()
	m.addArtifact(artID)

	// Full anchor at t0.
	m.addAnchor(model.Anchor{
		AnchorID:     model.AnchorID(model.NewID()),
		Kind:         model.AnchorFull,
		CreatedAt:    t0,
		ArtifactRefs: []model.ID{artID},
	})
	// Diff at t1 adds another artifact.
	artID2 := model.NewID()
	m.addArtifact(artID2)
	m.addAnchor(model.Anchor{
		AnchorID:     model.AnchorID(model.NewID()),
		Kind:         model.AnchorDiff,
		CreatedAt:    t1,
		ArtifactRefs: []model.ID{artID2},
	})

	scope, err := tm.ScopeStateAt(context.Background(), "test", t2)
	if err != nil {
		t.Fatalf("ScopeStateAt: %v", err)
	}
	if len(scope.Artifacts) != 2 {
		t.Errorf("expected 2 artifacts (full + diff), got %d", len(scope.Artifacts))
	}
}

func TestScopeStateAt_NoAnchor(t *testing.T) {
	m := newTimeMocks()
	tm := newTimeMachine(m)

	_, err := tm.ScopeStateAt(context.Background(), "empty", time.Now())
	if err == nil {
		t.Fatal("expected ErrNoHistoricalState")
	}
}

func TestProjectionAt_MVCC(t *testing.T) {
	m := newTimeMocks()
	tm := newTimeMachine(m)

	artID := model.NewID()
	m.addArtifact(artID)

	now := time.Now()

	// Rev 1: valid from now-2h to now-1h (expired).
	m.addProjection(artID, 1, now.Add(-2*time.Hour), tp(now.Add(-1*time.Hour)))
	// Rev 2: valid from now-1h onward (still valid).
	m.addProjection(artID, 2, now.Add(-1*time.Hour), nil)

	// At time T = now-90min (between rev1's expiry and rev2's start).
	tMid := now.Add(-90 * time.Minute)
	proj, err := tm.ProjectionAt(context.Background(), artID, tMid)
	if err != nil {
		t.Fatalf("ProjectionAt: %v", err)
	}
	// Rev 1 should be valid at tMid, rev 2 should not (ValidFrom is later).
	if proj.Revision != 1 {
		t.Errorf("expected revision 1 at tMid, got %d", proj.Revision)
	}

	// At time T = now (rev 2 is valid).
	proj2, err := tm.ProjectionAt(context.Background(), artID, now)
	if err != nil {
		t.Fatalf("ProjectionAt now: %v", err)
	}
	if proj2.Revision != 2 {
		t.Errorf("expected revision 2 at now, got %d", proj2.Revision)
	}
}

func TestEdgesAt_ByNodeAndType(t *testing.T) {
	m := newTimeMocks()
	tm := newTimeMachine(m)

	now := time.Now()
	nodeID := model.NewID()
	targetID := model.NewID()

	eid := model.NewID()
	m.addEdge(eid, nodeID, targetID, model.EdgeLineage, true, now.Add(-time.Hour), nil)
	m.addAnchor(model.Anchor{
		AnchorID:  model.AnchorID(model.NewID()),
		Kind:      model.AnchorFull,
		CreatedAt: now.Add(-30 * time.Minute),
		EdgeRefs:  []model.EdgeID{eid},
	})

	edges, err := tm.EdgesAt(context.Background(), "test", nodeID, model.EdgeLineage, now)
	if err != nil {
		t.Fatalf("EdgesAt: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].EdgeID != eid {
		t.Errorf("wrong edge ID")
	}
}

func TestMissingArtifactsCollected(t *testing.T) {
	m := newTimeMocks()
	tm := newTimeMachine(m)

	now := time.Now()
	presentID := model.NewID()
	missingID := model.NewID()

	m.addArtifact(presentID)
	// missingID not added to artifacts map.
	m.addAnchor(model.Anchor{
		AnchorID:     model.AnchorID(model.NewID()),
		Kind:         model.AnchorFull,
		CreatedAt:    now,
		ArtifactRefs: []model.ID{presentID, missingID},
	})

	scope, err := tm.ScopeStateAt(context.Background(), "test", now)
	if err != nil {
		t.Fatalf("ScopeStateAt: %v", err)
	}
	if len(scope.MissingIDs) != 1 {
		t.Errorf("expected 1 missing artifact, got %d", len(scope.MissingIDs))
	}
	if scope.MissingIDs[0] != missingID {
		t.Errorf("expected missingID %v, got %v", missingID, scope.MissingIDs[0])
	}
}

func TestDeterministicReplay(t *testing.T) {
	m := newTimeMocks()
	tm := newTimeMachine(m)

	now := time.Now()
	artID := model.NewID()
	m.addArtifact(artID)
	m.addAnchor(model.Anchor{
		AnchorID:     model.AnchorID(model.NewID()),
		Kind:         model.AnchorFull,
		CreatedAt:    now,
		ArtifactRefs: []model.ID{artID},
	})

	s1, _ := tm.ScopeStateAt(context.Background(), "test", now)
	s2, _ := tm.ScopeStateAt(context.Background(), "test", now)

	if len(s1.Artifacts) != len(s2.Artifacts) {
		t.Error("determinism violated: different artifact counts")
	}
	if s1.FromAnchor != s2.FromAnchor {
		t.Error("determinism violated: different anchors")
	}
}

func tp(t time.Time) *time.Time { return &t }
