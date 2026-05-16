package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DotBlood/ioc/internal/model"
)

func tempDB(t *testing.T) *DiskStore {
	t.Helper()
	path := filepath.Join(os.TempDir(), "ioc-test-"+model.NewID().String()+".db")
	s, err := OpenOrCreate(path)
	if err != nil {
		t.Fatalf("OpenOrCreate: %v", err)
	}
	t.Cleanup(func() {
		s.Close()
		os.Remove(path)
	})
	return s
}

func TestDiskStore_NodeCRUD(t *testing.T) {
	s := tempDB(t)

	art := &model.Artifact{
		ArtifactID:  model.NewID(),
		NodeType:    model.NodeTypeArtifact,
		Scope:       "test:scope",
		ContentHash: model.NewContentHash([]byte("hello")),
	}

	// Create
	if err := s.SaveNode(art); err != nil {
		t.Fatalf("SaveNode: %v", err)
	}

	// Read
	got, err := s.LoadNode(art.ArtifactID)
	if err != nil {
		t.Fatalf("LoadNode: %v", err)
	}
	if got.ArtifactID != art.ArtifactID {
		t.Errorf("LoadNode: id mismatch")
	}
	if got.Scope != "test:scope" {
		t.Errorf("LoadNode: scope = %q, want %q", got.Scope, "test:scope")
	}

	// Delete
	if err := s.DeleteNode(art.ArtifactID); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if _, err := s.LoadNode(art.ArtifactID); err == nil {
		t.Error("LoadNode after Delete: expected error")
	}
}

func TestDiskStore_ProjectionCRUD(t *testing.T) {
	s := tempDB(t)

	artID := model.NewID()
	p := &model.ArtifactProjection{
		ArtifactID: artID,
		Revision:   1,
		Summary:    "test summary",
		Keywords:   []string{"go", "test"},
	}

	if err := s.SaveProjection(p); err != nil {
		t.Fatalf("SaveProjection: %v", err)
	}

	key := model.ProjectionKey{ArtifactID: artID, Revision: 1}
	got, err := s.LoadProjection(key)
	if err != nil {
		t.Fatalf("LoadProjection: %v", err)
	}
	if got.Summary != "test summary" {
		t.Errorf("summary = %q, want %q", got.Summary, "test summary")
	}

	// List projections
	keys, err := s.ListProjectionKeys(artID)
	if err != nil {
		t.Fatalf("ListProjectionKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("expected 1 projection key, got %d", len(keys))
	}
}

func TestDiskStore_EdgeCRUD(t *testing.T) {
	s := tempDB(t)

	e := &model.Edge{
		EdgeID:    model.NewID(),
		Type:      model.EdgeLineage,
		Direction: model.DirectionDirected,
		Source:    model.NewID(),
		Target:    model.NewID(),
		Valid:     true,
	}

	if err := s.SaveEdge(e); err != nil {
		t.Fatalf("SaveEdge: %v", err)
	}

	got, err := s.LoadEdge(e.EdgeID)
	if err != nil {
		t.Fatalf("LoadEdge: %v", err)
	}
	if got.Source != e.Source || got.Target != e.Target {
		t.Errorf("edge endpoint mismatch")
	}

	// Adjacency lists
	out, err := s.LoadEdgesOut(e.Source, model.EdgeLineage)
	if err != nil {
		t.Fatalf("LoadEdgesOut: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("expected 1 outgoing edge, got %d", len(out))
	}

	in, err := s.LoadEdgesIn(e.Target, model.EdgeLineage)
	if err != nil {
		t.Fatalf("LoadEdgesIn: %v", err)
	}
	if len(in) != 1 {
		t.Errorf("expected 1 incoming edge, got %d", len(in))
	}
}

func TestDiskStore_Snapshot(t *testing.T) {
	s := tempDB(t)

	art := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeArtifact}
	proj := &model.ArtifactProjection{ArtifactID: art.ArtifactID, Revision: 1, Summary: "snap"}
	e := &model.Edge{
		EdgeID: model.NewID(), Type: model.EdgeLineage,
		Source: model.NewID(), Target: art.ArtifactID, Valid: true,
	}

	gs := &GraphSnapshot{
		Nodes:       []*model.Artifact{art},
		Edges:       []*model.Edge{e},
		Projections: []*model.ArtifactProjection{proj},
	}

	if err := s.SaveSnapshot(*gs); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	loaded, err := s.LoadSnapshot()
	if err != nil {
		t.Fatalf("LoadSnapshot: %v", err)
	}

	if len(loaded.Nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(loaded.Nodes))
	}
	if len(loaded.Edges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(loaded.Edges))
	}
	if len(loaded.Projections) != 1 {
		t.Errorf("expected 1 projection, got %d", len(loaded.Projections))
	}
}

func TestDiskStore_NotFound(t *testing.T) {
	s := tempDB(t)

	_, err := s.LoadNode(model.NewID())
	if err == nil {
		t.Error("LoadNode nonexistent: expected error")
	}

	_, err = s.LoadEdge(model.NewID())
	if err == nil {
		t.Error("LoadEdge nonexistent: expected error")
	}
}

func TestDiskStore_OpenTwice(t *testing.T) {
	path := filepath.Join(os.TempDir(), "ioc-test-openclose-"+model.NewID().String()+".db")
	s1, err := OpenOrCreate(path)
	if err != nil {
		t.Fatalf("OpenOrCreate 1: %v", err)
	}
	s1.Close()

	// Reopen.
	s2, err := OpenOrCreate(path)
	if err != nil {
		t.Fatalf("OpenOrCreate 2: %v", err)
	}
	s2.Close()
	os.Remove(path)
}

func TestDiskStore_ProjectionMultiple(t *testing.T) {
	s := tempDB(t)
	artID := model.NewID()

	for i := model.RevisionNumber(1); i <= 5; i++ {
		p := &model.ArtifactProjection{
			ArtifactID: artID,
			Revision:   i,
			Summary:    fmt.Sprintf("rev %d", i),
		}
		if err := s.SaveProjection(p); err != nil {
			t.Fatalf("SaveProjection rev %d: %v", i, err)
		}
	}

	keys, err := s.ListProjectionKeys(artID)
	if err != nil {
		t.Fatalf("ListProjectionKeys: %v", err)
	}
	if len(keys) != 5 {
		t.Errorf("expected 5 projection keys, got %d", len(keys))
	}
}

func TestScopeState_CRUD(t *testing.T) {
	s := tempDB(t)

	scopeID := model.ScopeID(model.NewID().String())
	state := &model.ScopeState{
		ScopeID: scopeID,
		Type:    model.NodeTypeWorktree,
		State:   model.LifecycleDraft,
	}

	if err := s.SaveScopeState(state); err != nil {
		t.Fatalf("SaveScopeState: %v", err)
	}

	got, err := s.ScopeState(context.Background(), scopeID)
	if err != nil {
		t.Fatalf("ScopeState: %v", err)
	}
	if got.ScopeID != scopeID {
		t.Errorf("ScopeID = %q, want %q", got.ScopeID, scopeID)
	}
	if got.Type != model.NodeTypeWorktree {
		t.Errorf("Type = %v, want worktree", got.Type)
	}
	if got.State != model.LifecycleDraft {
		t.Errorf("State = %v, want draft", got.State)
	}

	// ListAll.
	all, err := s.ListAllScopeStates()
	if err != nil {
		t.Fatalf("ListAllScopeStates: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("ListAll: got %d, want 1", len(all))
	}

	// NotFound.
	_, err = s.ScopeState(context.Background(), "nonexistent")
	if err == nil {
		t.Error("ScopeState: expected error for missing scope")
	}
}

func TestScopeState_Children(t *testing.T) {
	s := tempDB(t)

	parentID := model.ScopeID(model.NewID().String())
	childID := model.ScopeID(model.NewID().String())

	if err := s.SaveScopeChild(parentID, childID); err != nil {
		t.Fatalf("SaveScopeChild: %v", err)
	}

	children, err := s.ScopeChildren(context.Background(), parentID)
	if err != nil {
		t.Fatalf("ScopeChildren: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(children))
	}
	if children[0] != childID {
		t.Errorf("child = %q, want %q", children[0], childID)
	}

	// Idempotent — second save does not duplicate.
	if err := s.SaveScopeChild(parentID, childID); err != nil {
		t.Fatalf("SaveScopeChild (dup): %v", err)
	}
	children, err = s.ScopeChildren(context.Background(), parentID)
	if err != nil {
		t.Fatalf("ScopeChildren after dup: %v", err)
	}
	if len(children) != 1 {
		t.Errorf("expected 1 child (idempotent), got %d", len(children))
	}

	// Unrelated parent has no children.
	other := model.ScopeID(model.NewID().String())
	children, err = s.ScopeChildren(context.Background(), other)
	if err != nil {
		t.Fatalf("ScopeChildren other: %v", err)
	}
	if len(children) != 0 {
		t.Errorf("expected 0 children for unrelated parent, got %d", len(children))
	}
}

func TestDiskStore_SetScopeState(t *testing.T) {
	s := tempDB(t)

	scopeID := model.ScopeID(model.NewID().String())
	state := &model.ScopeState{
		ScopeID: scopeID,
		Type:    model.NodeTypeWorkspace,
		State:   model.LifecycleDraft,
	}
	if err := s.SaveScopeState(state); err != nil {
		t.Fatalf("SaveScopeState: %v", err)
	}

	// Transition.
	if err := s.SetScopeState(context.Background(), scopeID, model.LifecycleActive); err != nil {
		t.Fatalf("SetScopeState: %v", err)
	}

	got, err := s.ScopeState(context.Background(), scopeID)
	if err != nil {
		t.Fatalf("ScopeState: %v", err)
	}
	if got.State != model.LifecycleActive {
		t.Errorf("State = %v, want active", got.State)
	}

	// SetScopeState on missing scope returns error.
	err = s.SetScopeState(context.Background(), "missing", model.LifecycleActive)
	if err == nil {
		t.Error("SetScopeState: expected error for missing scope")
	}
}
