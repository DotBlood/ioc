package knowledge

import (
	"context"
	"testing"
	"time"

	"github.com/DotBlood/ioc/internal/model"
)

type retentionMocks struct {
	artifacts   []model.ID
	projections map[model.ID][]model.ProjectionKey
	projData    map[model.ProjectionKey]*model.ArtifactProjection
	anchors     []model.Anchor
}

func newRetentionMocks() *retentionMocks {
	return &retentionMocks{
		projections: make(map[model.ID][]model.ProjectionKey),
		projData:    make(map[model.ProjectionKey]*model.ArtifactProjection),
	}
}

func (m *retentionMocks) addProj(artifactID model.ID, rev model.RevisionNumber, validTo *time.Time) {
	key := model.ProjectionKey{ArtifactID: artifactID, Revision: rev}
	m.projections[artifactID] = append(m.projections[artifactID], key)
	m.projData[key] = &model.ArtifactProjection{
		ArtifactID: artifactID,
		Revision:   rev,
		ValidTo:    validTo,
	}
}

func (m *retentionMocks) ListAllArtifactIDs(_ context.Context) ([]model.ID, error)            { return m.artifacts, nil }
func (m *retentionMocks) ListProjections(_ context.Context, id model.ID) ([]model.ProjectionKey, error) {
	// Return a copy to prevent aliasing with the stored slice.
	orig := m.projections[id]
	if orig == nil {
		return nil, nil
	}
	result := make([]model.ProjectionKey, len(orig))
	copy(result, orig)
	return result, nil
}
func (m *retentionMocks) LoadProjection(_ context.Context, key model.ProjectionKey) (*model.ArtifactProjection, error) {
	// Search by Revision (ignore ArtifactID struct comparison).
	for pk, p := range m.projData {
		if pk.ArtifactID == key.ArtifactID && pk.Revision == key.Revision {
			return p, nil
		}
	}
	return nil, model.ErrNotFound
}
func (m *retentionMocks) DeleteProjection(key model.ProjectionKey) error {
	id := key.ArtifactID
	keys := m.projections[id]
	for i, k := range keys {
		if k.ArtifactID == key.ArtifactID && k.Revision == key.Revision {
			m.projections[id] = append(keys[:i], keys[i+1:]...)
			break
		}
	}
	// Delete from projData regardless — don't need to match key exactly,
	// just clear the data for this artifact+revision.
	for pk := range m.projData {
		if pk.ArtifactID == key.ArtifactID && pk.Revision == key.Revision {
			delete(m.projData, pk)
			break
		}
	}
	return nil
}
func (m *retentionMocks) ListEdgeRevisions(_ context.Context) ([]model.EdgeRevisionKey, error) { return nil, nil }
func (m *retentionMocks) LoadEdgeRevision(_ context.Context, _ model.EdgeRevisionKey) (*model.Edge, error) {
	return nil, model.ErrNotFound
}
func (m *retentionMocks) DeleteEdgeRevision(_ model.EdgeRevisionKey) error { return nil }
func (m *retentionMocks) ListAll(_ context.Context) ([]model.Anchor, error) { return m.anchors, nil }

func newPolicy(m *retentionMocks, maxVer int, ttl time.Duration) *RetentionPolicy {
	return NewRetentionPolicy(RetentionConfig{MaxVersions: maxVer, RetentionTTL: ttl}, m, m, m)
}

func TestRetention_UnderLimit(t *testing.T) {
	m := newRetentionMocks()
	artID := model.NewID()
	m.addProj(artID, 1, pastTime(time.Hour))
	m.addProj(artID, 2, pastTime(time.Hour))

	p := newPolicy(m, 10, time.Minute)
	pruned, err := p.PruneProjections(context.Background(), artID)
	if err != nil {
		t.Fatalf("PruneProjections: %v", err)
	}
	if pruned != 0 {
		t.Errorf("expected 0, got %d", pruned)
	}
}

func TestRetention_PruneExactCount(t *testing.T) {
	// 5 projections, MaxVersions=2, all expired → expect 3 pruned.
	m := newRetentionMocks()
	artID := model.NewID()
	for i := model.RevisionNumber(1); i <= 5; i++ {
		m.addProj(artID, i, pastTime(time.Hour))
	}

	p := newPolicy(m, 2, time.Nanosecond) // prune all past TTL
	pruned, _ := p.PruneProjections(context.Background(), artID)
	if pruned != 3 {
		t.Errorf("5 proj, keep 2: expected 3 pruned, got %d", pruned)
	}
}

func TestRetention_LatestNeverPruned(t *testing.T) {
	m := newRetentionMocks()
	artID := model.NewID()
	for i := model.RevisionNumber(1); i <= 5; i++ {
		m.addProj(artID, i, pastTime(time.Hour))
	}

	p := newPolicy(m, 0, time.Nanosecond) // 0 → keepAtLeast=1 → keep only latest
	pruned, _ := p.PruneProjections(context.Background(), artID)
	if pruned != 4 {
		t.Errorf("5 proj, keep 1 (Max=0): expected 4 pruned, got %d", pruned)
	}

	// MaxVersions=1 should behave the same (keep 1).
	m2 := newRetentionMocks()
	artID2 := model.NewID()
	for i := model.RevisionNumber(1); i <= 5; i++ {
		m2.addProj(artID2, i, pastTime(time.Hour))
	}
	p2 := newPolicy(m2, 1, time.Nanosecond)
	pruned2, _ := p2.PruneProjections(context.Background(), artID2)
	if pruned2 != 4 {
		t.Errorf("5 proj, keep 1 (Max=1): expected 4 pruned, got %d", pruned2)
	}
}

func TestRetention_ProtectedByAnchor(t *testing.T) {
	m := newRetentionMocks()
	artID := model.NewID()
	for i := model.RevisionNumber(1); i <= 5; i++ {
		m.addProj(artID, i, pastTime(time.Hour))
	}

	// Anchor protects revision 3.
	m.anchors = []model.Anchor{{
		ProjectionRefs: []model.ProjectionKey{{ArtifactID: artID, Revision: 3}},
	}}

	p := newPolicy(m, 2, time.Nanosecond) // keep 2, all expired
	pruned, _ := p.PruneProjections(context.Background(), artID)
	// MaxVer=2 → keep latest 2 (rev 4,5). Rev 2 unprotected → pruned.
	// Actually: keep 4,5. Prune candidates: 1,2,3. 3 protected → keep.
	// Result: 1,2 pruned = 2.
	if pruned != 2 {
		t.Errorf("expected 2 pruned (1 and 2 unprotected), got %d", pruned)
	}
}

func TestRetention_TTL_ValidTo(t *testing.T) {
	m := newRetentionMocks()
	artID := model.NewID()
	// 6 projections, keepAtLeast=3. Candidates: rev 1,2,3.
	// Rev 1,2,3: expired long ago (TTL 1h, actual 5h).
	// Rev 4,5: expired recently (within 1h TTL). Rev 6: no expiry.
	m.addProj(artID, 1, pastTime(5*time.Hour))
	m.addProj(artID, 2, pastTime(5*time.Hour))
	m.addProj(artID, 3, pastTime(5*time.Hour))
	m.addProj(artID, 4, pastTime(30*time.Minute))
	m.addProj(artID, 5, pastTime(30*time.Minute))
	m.addProj(artID, 6, nil)

	p := newPolicy(m, 3, time.Hour)
	pruned, _ := p.PruneProjections(context.Background(), artID)
	// keepAtLeast=3, cutoff=6-3=3. Candidates: 1,2,3.
	// All 3 have ValidTo = 5h ago, TTL = 1h → 5h > 1h → delete all 3.
	if pruned != 3 {
		t.Errorf("expected 3 pruned (1,2,3 past TTL), got %d", pruned)
	}
}

func TestRetention_TTL_WithinWindow(t *testing.T) {
	m := newRetentionMocks()
	artID := model.NewID()
	// 4 projections, MaxVersions=1, keep 1.
	// Rev 1: recently expired. Rev 2,3: long expired but also recently... 
	// Actually let's test: rev 1 is within TTL, rev 2,3 are past TTL.
	m.addProj(artID, 1, pastTime(10*time.Minute)) // within 1h TTL
	m.addProj(artID, 2, pastTime(5*time.Hour))   // past 1h TTL
	m.addProj(artID, 3, pastTime(5*time.Hour))   // past 1h TTL
	m.addProj(artID, 4, pastTime(5*time.Hour))   // latest, but shouldn't affect pruning

	p := newPolicy(m, 1, time.Hour)
	pruned, _ := p.PruneProjections(context.Background(), artID)
	// pruneUpTo = 4-1 = 3. candidates = 1,2,3.
	// Rev 1: within 1h TTL → kept. Rev 2,3: past 1h TTL → pruned.
	if pruned != 2 {
		t.Errorf("expected 2 pruned (rev 2,3 past TTL, rev 1 within), got %d", pruned)
	}
}

func TestRetention_ActiveNotPruned(t *testing.T) {
	m := newRetentionMocks()
	artID := model.NewID()
	m.addProj(artID, 1, nil) // no expiry → active → never pruned

	p := newPolicy(m, 0, time.Nanosecond)
	pruned, _ := p.PruneProjections(context.Background(), artID)
	if pruned != 0 {
		t.Errorf("expected 0 (active projection), got %d", pruned)
	}
}

func TestRetention_RunFullSweep(t *testing.T) {
	m := newRetentionMocks()
	artID := model.NewID()
	m.artifacts = []model.ID{artID}
	for i := model.RevisionNumber(1); i <= 5; i++ {
		m.addProj(artID, i, pastTime(5*time.Hour))
	}

	p := newPolicy(m, 2, time.Nanosecond)
	result, _ := p.Run(context.Background())
	if result.ProjectionsPruned != 3 {
		t.Errorf("expected 3 pruned (5 proj, keep 2), got %d", result.ProjectionsPruned)
	}
}

func pastTime(d time.Duration) *time.Time {
	t := time.Now().Add(-d)
	return &t
}
