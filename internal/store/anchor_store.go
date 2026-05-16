package store

import (
	"context"
	"fmt"
	"sort"

	"go.etcd.io/bbolt"

	"github.com/DotBlood/ioc/internal/model"
)

// AnchorStore provides bbolt-backed persistence for structural snapshots.
type AnchorStore struct {
	disk *DiskStore
}

// NewAnchorStore creates an anchor store backed by the given disk store.
func NewAnchorStore(disk *DiskStore) *AnchorStore {
	return &AnchorStore{disk: disk}
}

// Create persists a new anchor. Returns ErrDuplicate if anchor ID already exists.
func (s *AnchorStore) Create(ctx context.Context, anchor *model.Anchor) error {
	return s.disk.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("anchors"))
		key := []byte(anchor.AnchorID.String())
		if b.Get(key) != nil {
			return model.ErrDuplicate
		}
		val, err := encode(anchor)
		if err != nil {
			return fmt.Errorf("anchor store: encode: %w", err)
		}
		return b.Put(key, val)
	})
}

// Load retrieves an anchor by ID.
func (s *AnchorStore) Load(ctx context.Context, id model.AnchorID) (*model.Anchor, error) {
	var anchor model.Anchor
	err := s.disk.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("anchors"))
		val := b.Get([]byte(id.String()))
		if val == nil {
			return model.ErrNotFound
		}
		return decode(val, &anchor)
	})
	if err != nil {
		return nil, err
	}
	return &anchor, nil
}

// List returns all anchor IDs for a scope. If scopeID is empty, returns all.
func (s *AnchorStore) List(ctx context.Context, scopeID model.ScopeID) ([]model.Anchor, error) {
	var anchors []model.Anchor
	err := s.disk.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("anchors"))
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var a model.Anchor
			if err := decode(v, &a); err != nil {
				return err
			}
			if scopeID == "" || a.ScopeID == scopeID {
				anchors = append(anchors, a)
			}
		}
		return nil
	})
	return anchors, err
}

// ListRange returns anchors for a scope created between from and to (inclusive).
// Ordering is by Revision (not timestamp) for deterministic diff replay.
func (s *AnchorStore) ListRange(ctx context.Context, scopeID model.ScopeID, fromRev, toRev model.RevisionNumber) ([]model.Anchor, error) {
	all, err := s.List(ctx, scopeID)
	if err != nil {
		return nil, err
	}

	// Filter by revision range.
	var matched []model.Anchor
	for _, a := range all {
		if a.Revision >= fromRev && a.Revision <= toRev {
			matched = append(matched, a)
		}
	}

	// Sort by Revision for deterministic replay.
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Revision < matched[j].Revision
	})

	return matched, nil
}

// Delete removes an anchor by ID.
func (s *AnchorStore) Delete(ctx context.Context, id model.AnchorID) error {
	return s.disk.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte("anchors")).Delete([]byte(id.String()))
	})
}
