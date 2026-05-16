package knowledge

import (
	"context"
	"testing"

	"github.com/DotBlood/ioc/internal/model"
)

// danglingMocks implements both DanglingGraphReader and StructuralResolver.
type danglingMocks struct {
	exists     map[model.ID]bool
	scopeChildren map[model.ScopeID][]model.ID
	parents    map[model.ID]model.ID // child → parent
}

func newDanglingMocks() *danglingMocks {
	return &danglingMocks{
		exists:        make(map[model.ID]bool),
		scopeChildren: make(map[model.ScopeID][]model.ID),
		parents:       make(map[model.ID]model.ID),
	}
}

func (m *danglingMocks) Exists(_ context.Context, id model.ID) (bool, error) {
	exists, ok := m.exists[id]
	if !ok {
		return false, nil
	}
	return exists, nil
}

func (m *danglingMocks) Parent(_ context.Context, childID model.ID) (model.ID, error) {
	parent, ok := m.parents[childID]
	if !ok {
		return model.NilID, model.ErrNotFound
	}
	return parent, nil
}

func (m *danglingMocks) Children(_ context.Context, scopeID model.ScopeID) ([]model.ID, error) {
	children, ok := m.scopeChildren[scopeID]
	if !ok {
		return nil, nil
	}
	return children, nil
}

func TestDanglingChecker_Alive(t *testing.T) {
	m := newDanglingMocks()
	id := model.NewID()
	m.exists[id] = true

	dc := NewDanglingChecker(m)
	state, err := dc.ExistsReference(context.Background(), id)
	if err != nil {
		t.Fatalf("ExistsReference: %v", err)
	}
	if state != model.RefActive {
		t.Errorf("state = %v, want RefActive", state)
	}
}

func TestDanglingChecker_Dangling(t *testing.T) {
	m := newDanglingMocks()
	id := model.NewID()
	// Not added to exists map → doesn't exist.

	dc := NewDanglingChecker(m)
	state, err := dc.ExistsReference(context.Background(), id)
	if err != nil {
		t.Fatalf("ExistsReference: %v", err)
	}
	if state != model.RefDangling {
		t.Errorf("state = %v, want RefDangling", state)
	}
}

func TestDanglingChecker_CacheHit(t *testing.T) {
	m := newDanglingMocks()
	id := model.NewID()
	m.exists[id] = true

	dc := NewDanglingChecker(m)

	// First call — populates cache.
	state1, _ := dc.ExistsReference(context.Background(), id)

	// Remove from store (simulate deletion).
	delete(m.exists, id)

	// Second call — should return cached value, not re-check store.
	state2, _ := dc.ExistsReference(context.Background(), id)

	if state1 != state2 {
		t.Errorf("cache miss: state1=%v, state2=%v", state1, state2)
	}
}

func TestDanglingChecker_InvalidateCache(t *testing.T) {
	m := newDanglingMocks()
	id := model.NewID()
	m.exists[id] = true

	dc := NewDanglingChecker(m)
	dc.ExistsReference(context.Background(), id)

	// Remove from store.
	delete(m.exists, id)

	// Invalidate cache.
	dc.InvalidateCache([]model.ID{id})

	// Should re-check store.
	state, _ := dc.ExistsReference(context.Background(), id)
	if state != model.RefDangling {
		t.Errorf("after invalidate: state = %v, want RefDangling", state)
	}
}

func TestDanglingChecker_InvalidateAll(t *testing.T) {
	m := newDanglingMocks()
	id := model.NewID()
	m.exists[id] = true

	dc := NewDanglingChecker(m)
	dc.ExistsReference(context.Background(), id)
	delete(m.exists, id)

	dc.InvalidateCache(nil)

	state, _ := dc.ExistsReference(context.Background(), id)
	if state != model.RefDangling {
		t.Errorf("after invalidate all: state = %v, want RefDangling", state)
	}
}

func TestOrphanScanner_NoOrphans(t *testing.T) {
	m := newDanglingMocks()
	sid := model.ScopeID("test:scope")
	artID := model.NewID()
	parentID := model.NewID()

	m.exists[parentID] = true
	m.exists[artID] = true
	m.parents[artID] = parentID
	m.scopeChildren[sid] = []model.ID{artID}

	scanner := NewOrphanScanner(m)
	orphans, err := scanner.ScanOrphansFlat(context.Background(), sid)
	if err != nil {
		t.Fatalf("ScanOrphansFlat: %v", err)
	}
	if len(orphans) != 0 {
		t.Errorf("expected 0 orphans, got %d", len(orphans))
	}
}

func TestOrphanScanner_FindsOrphans(t *testing.T) {
	m := newDanglingMocks()
	sid := model.ScopeID("orphan:test")
	artID := model.NewID()

	m.exists[artID] = true
	m.scopeChildren[sid] = []model.ID{artID}
	// artID has no parent → orphan.

	scanner := NewOrphanScanner(m)
	orphans, err := scanner.ScanOrphansFlat(context.Background(), sid)
	if err != nil {
		t.Fatalf("ScanOrphansFlat: %v", err)
	}
	if len(orphans) != 1 {
		t.Errorf("expected 1 orphan, got %d", len(orphans))
	}
	if orphans[0] != artID {
		t.Errorf("orphan ID mismatch")
	}
}


