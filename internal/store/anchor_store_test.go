package store

import (
	"context"
	"testing"

	"github.com/DotBlood/ioc/internal/model"
)

func TestAnchorStore_CreateLoad(t *testing.T) {
	s := tempDB(t)
	astore := NewAnchorStore(s)
	ctx := context.Background()

	anchor := &model.Anchor{
		AnchorID: model.AnchorID(model.NewID()),
		Kind:     model.AnchorFull,
		ScopeID:  "test:scope",
		Revision: 1,
		ArtifactRefs: []model.ID{model.NewID(), model.NewID()},
		ArtifactCount: 2,
	}

	if err := astore.Create(ctx, anchor); err != nil {
		t.Fatalf("Create: %v", err)
	}

	loaded, err := astore.Load(ctx, anchor.AnchorID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ScopeID != anchor.ScopeID {
		t.Errorf("ScopeID = %q, want %q", loaded.ScopeID, anchor.ScopeID)
	}
	if loaded.ArtifactCount != 2 {
		t.Errorf("ArtifactCount = %d, want 2", loaded.ArtifactCount)
	}
}

func TestAnchorStore_List(t *testing.T) {
	s := tempDB(t)
	astore := NewAnchorStore(s)
	ctx := context.Background()
	scopeID := model.ScopeID("list-test")

	for i := 0; i < 3; i++ {
		a := &model.Anchor{
			AnchorID: model.AnchorID(model.NewID()),
			Kind:     model.AnchorFull,
			ScopeID:  scopeID,
			Revision: model.RevisionNumber(i + 1),
		}
		if err := astore.Create(ctx, a); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	anchors, err := astore.List(ctx, scopeID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(anchors) != 3 {
		t.Errorf("expected 3 anchors, got %d", len(anchors))
	}
}

func TestAnchorStore_ListRange(t *testing.T) {
	s := tempDB(t)
	astore := NewAnchorStore(s)
	ctx := context.Background()
	scopeID := model.ScopeID("range-test")

	for i := 0; i < 5; i++ {
		a := &model.Anchor{
			AnchorID: model.AnchorID(model.NewID()),
			Kind:     model.AnchorFull,
			ScopeID:  scopeID,
			Revision: model.RevisionNumber(i + 1),
		}
		astore.Create(ctx, a)
	}

	anchors, err := astore.ListRange(ctx, scopeID, 2, 4)
	if err != nil {
		t.Fatalf("ListRange: %v", err)
	}
	if len(anchors) != 3 {
		t.Errorf("expected 3 anchors in range 2-4, got %d", len(anchors))
	}
	for _, a := range anchors {
		if a.Revision < 2 || a.Revision > 4 {
			t.Errorf("anchor revision %d outside range", a.Revision)
		}
	}
}

func TestAnchorStore_Delete(t *testing.T) {
	s := tempDB(t)
	astore := NewAnchorStore(s)
	ctx := context.Background()

	a := &model.Anchor{
		AnchorID: model.AnchorID(model.NewID()),
		Kind:     model.AnchorFull,
		ScopeID:  "delete-test",
	}
	astore.Create(ctx, a)
	astore.Delete(ctx, a.AnchorID)

	_, err := astore.Load(ctx, a.AnchorID)
	if err == nil {
		t.Error("Load after Delete: expected error")
	}
}

func TestAnchorStore_DuplicateCreate(t *testing.T) {
	s := tempDB(t)
	astore := NewAnchorStore(s)
	ctx := context.Background()

	a := &model.Anchor{
		AnchorID: model.AnchorID(model.NewID()),
		Kind:     model.AnchorFull,
		ScopeID:  "dup-test",
	}
	if err := astore.Create(ctx, a); err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	if err := astore.Create(ctx, a); err == nil {
		t.Error("Create duplicate: expected error")
	}
}
