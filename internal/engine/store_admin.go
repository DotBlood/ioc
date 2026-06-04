package engine

import (
	"context"
	"fmt"

	"github.com/DotBlood/ioc/internal/core"
)

// This file exposes thin store-administration wrappers so packages like
// internal/ingest can list/delete scopes and artifacts and persist small
// config values WITHOUT importing internal/storage directly (preserving the
// dependency direction: only engine touches storage).

// ListScopes returns all scopes (full scan).
func (e *Engine) ListScopes(_ context.Context) ([]core.Scope, error) {
	return e.meta.ListScopes()
}

// GetScope loads a scope by ID (core.ErrNotFound if absent).
func (e *Engine) GetScope(_ context.Context, id core.ID) (core.Scope, error) {
	return e.meta.GetScope(id)
}

// ListArtifacts returns all artifacts (full scan).
func (e *Engine) ListArtifacts(_ context.Context) ([]core.Artifact, error) {
	return e.meta.ListArtifacts()
}

// DeleteArtifact removes an artifact record. Its embedding remains in the
// append-only store but never re-surfaces in search (the candidate set is built
// from live artifacts).
func (e *Engine) DeleteArtifact(_ context.Context, id core.ID) error {
	return e.meta.DeleteArtifact(id)
}

// DeleteScope removes a scope, refusing if it still has artifacts or child
// scopes (guard against accidental data loss; callers empty it first).
func (e *Engine) DeleteScope(_ context.Context, id core.ID) error {
	arts, err := e.meta.ArtifactsInScope(id)
	if err != nil {
		return err
	}
	if len(arts) > 0 {
		return fmt.Errorf("engine: delete scope %s: %w: has %d artifacts", id, core.ErrInvalidInput, len(arts))
	}
	children, err := e.meta.ChildScopes(id)
	if err != nil {
		return err
	}
	if len(children) > 0 {
		return fmt.Errorf("engine: delete scope %s: %w: has %d children", id, core.ErrInvalidInput, len(children))
	}
	return e.meta.DeleteScope(id)
}

// Config reads a small persisted config value.
func (e *Engine) Config(key string) (string, bool) { return e.meta.GetConfig(key) }

// SetConfig persists a small config value.
func (e *Engine) SetConfig(key, val string) error { return e.meta.PutConfig(key, val) }

// Sync flushes buffered embeddings to disk. The embedding store keeps vectors in
// memory until Sync (Close also syncs); a long-lived runtime owner must call this
// after writes so a crash does not lose embeddings. No-op if the store is not yet
// opened (no embeddings written this run).
func (e *Engine) Sync() error {
	if e.emb == nil {
		return nil
	}
	return e.emb.Sync()
}
