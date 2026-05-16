package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/DotBlood/ioc/internal/model"
)

func artifactStore(t *testing.T) *ArtifactStore {
	t.Helper()
	return NewArtifactStore(tempDB(t))
}

func TestArtifactStore_SaveLoad(t *testing.T) {
	s := artifactStore(t)
	ctx := context.Background()

	art := &model.Artifact{
		ArtifactID: model.NewID(),
		NodeType:   model.NodeTypeArtifact,
		Scope:      "test:artifact",
	}

	if err := s.SaveArtifact(ctx, art); err != nil {
		t.Fatalf("SaveArtifact: %v", err)
	}

	got, err := s.LoadArtifact(ctx, art.ArtifactID)
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if got.ArtifactID != art.ArtifactID {
		t.Errorf("LoadArtifact: id mismatch")
	}
}

func TestArtifactStore_ProjectionCRUD(t *testing.T) {
	s := artifactStore(t)
	ctx := context.Background()

	artID := model.NewID()

	// Multiple projections.
	for i := model.RevisionNumber(1); i <= 3; i++ {
		p := &model.ArtifactProjection{
			ArtifactID: artID,
			Revision:   i,
			Summary:    fmt.Sprintf("rev %d", i),
		}
		if err := s.SaveProjection(ctx, p); err != nil {
			t.Fatalf("SaveProjection rev %d: %v", i, err)
		}
	}

	// List.
	keys, err := s.ListProjections(ctx, artID)
	if err != nil {
		t.Fatalf("ListProjections: %v", err)
	}
	if len(keys) != 3 {
		t.Errorf("expected 3 projections, got %d", len(keys))
	}

	// Latest.
	latest, err := s.LatestProjection(ctx, artID)
	if err != nil {
		t.Fatalf("LatestProjection: %v", err)
	}
	if latest.Revision != 3 {
		t.Errorf("LatestProjection: revision = %d, want 3", latest.Revision)
	}
	if latest.Summary != "rev 3" {
		t.Errorf("LatestProjection: summary = %q, want %q", latest.Summary, "rev 3")
	}
}

func TestArtifactStore_RevisionDAG(t *testing.T) {
	s := artifactStore(t)
	ctx := context.Background()

	artID := model.NewID()
	dag := &RevisionDAG{
		ArtifactID: artID,
		Edges: []struct {
			From model.RevisionNumber
			To   model.RevisionNumber
		}{
			{From: 1, To: 2},
			{From: 2, To: 3},
		},
		BranchHeads: map[string]model.RevisionNumber{"main": 3},
	}

	if err := s.SaveRevisionDAG(ctx, artID, dag); err != nil {
		t.Fatalf("SaveRevisionDAG: %v", err)
	}

	loaded, err := s.LoadRevisionDAG(ctx, artID)
	if err != nil {
		t.Fatalf("LoadRevisionDAG: %v", err)
	}
	if loaded.ArtifactID != artID {
		t.Errorf("ArtifactID mismatch")
	}
	if len(loaded.Edges) != 2 {
		t.Errorf("expected 2 edges, got %d", len(loaded.Edges))
	}
	if loaded.BranchHeads["main"] != 3 {
		t.Errorf("expected main branch head 3, got %d", loaded.BranchHeads["main"])
	}
}

func TestArtifactStore_AtomicBatch(t *testing.T) {
	s := artifactStore(t)
	ctx := context.Background()

	art := &model.Artifact{
		ArtifactID: model.NewID(),
		NodeType:   model.NodeTypeArtifact,
	}
	proj := &model.ArtifactProjection{
		ArtifactID: art.ArtifactID,
		Revision:   1,
		Summary:    "initial",
	}

	if err := s.SaveArtifactWithProjection(ctx, art, proj); err != nil {
		t.Fatalf("SaveArtifactWithProjection: %v", err)
	}

	gotArt, err := s.LoadArtifact(ctx, art.ArtifactID)
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if gotArt.ArtifactID != art.ArtifactID {
		t.Errorf("artifact mismatch")
	}

	gotProj, err := s.LoadProjection(ctx, proj.ProjectionKey())
	if err != nil {
		t.Fatalf("LoadProjection: %v", err)
	}
	if gotProj.Summary != "initial" {
		t.Errorf("projection summary = %q, want %q", gotProj.Summary, "initial")
	}
}

func TestArtifactStore_NotFound(t *testing.T) {
	s := artifactStore(t)
	ctx := context.Background()

	_, err := s.LoadArtifact(ctx, model.NewID())
	if err == nil {
		t.Error("LoadArtifact nonexistent: expected error")
	}

	_, err = s.LatestProjection(ctx, model.NewID())
	if err == nil {
		t.Error("LatestProjection nonexistent: expected error")
	}

	_, err = s.LoadRevisionDAG(ctx, model.NewID())
	if err == nil {
		t.Error("LoadRevisionDAG nonexistent: expected error")
	}
}

func TestArtifactStore_DeleteRecreate(t *testing.T) {
	s := artifactStore(t)
	ctx := context.Background()

	art := &model.Artifact{
		ArtifactID: model.NewID(),
		NodeType:   model.NodeTypeArtifact,
	}
	if err := s.SaveArtifact(ctx, art); err != nil {
		t.Fatalf("SaveArtifact: %v", err)
	}
	if err := s.DeleteArtifact(ctx, art.ArtifactID); err != nil {
		t.Fatalf("DeleteArtifact: %v", err)
	}
	if err := s.SaveArtifact(ctx, art); err != nil {
		t.Fatalf("SaveArtifact after delete: %v", err)
	}
}

func TestArtifactStore_ListByType(t *testing.T) {
	s := artifactStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		art := &model.Artifact{
			ArtifactID: model.NewID(),
			NodeType:   model.NodeTypeArtifact,
		}
		if err := s.SaveArtifact(ctx, art); err != nil {
			t.Fatalf("SaveArtifact %d: %v", i, err)
		}
	}
	ws := &model.Artifact{ArtifactID: model.NewID(), NodeType: model.NodeTypeWorkspace}
	if err := s.SaveArtifact(ctx, ws); err != nil {
		t.Fatalf("SaveArtifact workspace: %v", err)
	}

	arts, err := s.ListArtifactsByType(ctx, model.NodeTypeArtifact)
	if err != nil {
		t.Fatalf("ListArtifactsByType: %v", err)
	}
	if len(arts) != 5 {
		t.Errorf("expected 5 artifacts, got %d", len(arts))
	}

	wss, err := s.ListArtifactsByType(ctx, model.NodeTypeWorkspace)
	if err != nil {
		t.Fatalf("ListArtifactsByType workspace: %v", err)
	}
	if len(wss) != 1 {
		t.Errorf("expected 1 workspace, got %d", len(wss))
	}
}

func TestArtifactStore_LatestProjectionEmpty(t *testing.T) {
	s := artifactStore(t)
	ctx := context.Background()

	_, err := s.LatestProjection(ctx, model.NewID())
	if err == nil {
		t.Error("LatestProjection for unknown artifact: expected error")
	}
}
