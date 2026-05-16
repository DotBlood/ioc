package store

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"go.etcd.io/bbolt"

	"github.com/DotBlood/ioc/internal/model"
)

// DiskStore persists graph state to disk using bbolt (embedded B+tree KV store).
//
// Bucket layout:
//
//	nodes        | ArtifactID.String() → gob(Artifact)
//	projections  | ProjectionKey.String() → gob(ArtifactProjection)
//	edges        | EdgeID.String() → gob(Edge)
//	adj_out      | SourceID|EdgeType → gob([]AdjEntry)
//	adj_in       | TargetID|EdgeType → gob([]AdjEntry)
//	anchors      | AnchorID.String() → gob(Anchor)
//	rev_dag      | ArtifactID.String() → gob(RevisionDAG)
//	idx_type     | NodeType → gob([]ID)
//	meta         | "version" → schema version
type DiskStore struct {
	mu   sync.RWMutex
	db   *bbolt.DB
	path string
}

// AdjEntry is a single adjacency list entry.
type AdjEntry struct {
	EdgeID   model.EdgeID
	TargetID model.ID
	SourceID model.ID
}

// Anchor is a structural snapshot of a scope.
type Anchor struct {
	AnchorID     model.ID
	ScopeID      model.ScopeID
	Revision     model.RevisionNumber
	Timestamp    time.Time
	ArtifactRefs []model.ID
	EdgeSnapshot []model.Edge
}

// RevisionDAG is a serializable revision DAG.
type RevisionDAG struct {
	ArtifactID model.ID
	Edges      []struct {
		From model.RevisionNumber
		To   model.RevisionNumber
	}
	BranchHeads map[string]model.RevisionNumber
}

// OpenOrCreate opens an existing bbolt database or creates a new one.
func OpenOrCreate(path string) (*DiskStore, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("open store: create dir: %w", err)
	}

	db, err := bbolt.Open(path, 0644, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	s := &DiskStore{db: db, path: path}
	if err := s.initBuckets(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open store: init buckets: %w", err)
	}
	return s, nil
}

// Close closes the database.
func (s *DiskStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

// Path returns the database file path.
func (s *DiskStore) Path() string { return s.path }

// initBuckets creates all required buckets if they don't exist.
func (s *DiskStore) initBuckets() error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		for _, name := range []string{
			"nodes", "projections", "edges",
			"adj_out", "adj_in",
			"anchors", "rev_dag", "idx_type", "meta",
			"scope_state", "scope_children",
		} {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		return nil
	})
}

// ============================================================
// Node operations
// ============================================================

// SaveNode persists a single artifact.
func (s *DiskStore) SaveNode(n *model.Artifact) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("nodes"))
		key := []byte(n.ArtifactID.String())
		val, err := encode(n)
		if err != nil {
			return fmt.Errorf("save node: %w", err)
		}
		return b.Put(key, val)
	})
}

// LoadNode retrieves an artifact by ID.
func (s *DiskStore) LoadNode(id model.ID) (*model.Artifact, error) {
	var n model.Artifact
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("nodes"))
		val := b.Get([]byte(id.String()))
		if val == nil {
			return model.ErrNotFound
		}
		return decode(val, &n)
	})
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// DeleteNode removes an artifact.
func (s *DiskStore) DeleteNode(id model.ID) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("nodes"))
		return b.Delete([]byte(id.String()))
	})
}

// ============================================================
// Projection operations
// ============================================================

// SaveProjection persists a single projection.
func (s *DiskStore) SaveProjection(p *model.ArtifactProjection) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("projections"))
		key := []byte(p.ProjectionKey().String())
		val, err := encode(p)
		if err != nil {
			return fmt.Errorf("save projection: %w", err)
		}
		return b.Put(key, val)
	})
}

// LoadProjection retrieves a projection by key.
func (s *DiskStore) LoadProjection(key model.ProjectionKey) (*model.ArtifactProjection, error) {
	var p model.ArtifactProjection
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("projections"))
		val := b.Get([]byte(key.String()))
		if val == nil {
			return model.ErrNotFound
		}
		return decode(val, &p)
	})
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ListProjectionKeys returns all projection keys for a given artifact.
func (s *DiskStore) ListProjectionKeys(artifactID model.ID) ([]model.ProjectionKey, error) {
	var keys []model.ProjectionKey
	prefix := []byte(artifactID.String() + ":")
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("projections"))
		c := b.Cursor()
		for k, _ := c.Seek(prefix); k != nil && len(k) >= len(prefix); k, _ = c.Next() {
			if string(k[:len(prefix)]) != string(prefix) {
				break
			}
			keyStr := string(k)
			// Parse "ArtifactID:RevisionNumber"
			var rev model.RevisionNumber
			if _, err := fmt.Sscanf(keyStr, artifactID.String()+":%d", &rev); err != nil {
				continue
			}
			keys = append(keys, model.ProjectionKey{ArtifactID: artifactID, Revision: rev})
		}
		return nil
	})
	return keys, err
}

// ============================================================
// Edge operations
// ============================================================

// SaveEdge persists a single edge.
func (s *DiskStore) SaveEdge(e *model.Edge) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("edges"))
		key := []byte(e.EdgeID.String())
		val, err := encode(e)
		if err != nil {
			return fmt.Errorf("save edge: %w", err)
		}
		if err := b.Put(key, val); err != nil {
			return err
		}

		// Update adjacency lists.
		if err := appendAdj(tx, "adj_out", e.Source, e.Type, AdjEntry{
			EdgeID: e.EdgeID, TargetID: e.Target, SourceID: e.Source,
		}); err != nil {
			return err
		}
		if err := appendAdj(tx, "adj_in", e.Target, e.Type, AdjEntry{
			EdgeID: e.EdgeID, TargetID: e.Target, SourceID: e.Source,
		}); err != nil {
			return err
		}
		return nil
	})
}

// LoadEdge retrieves an edge by ID.
func (s *DiskStore) LoadEdge(id model.EdgeID) (*model.Edge, error) {
	var e model.Edge
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("edges"))
		val := b.Get([]byte(id.String()))
		if val == nil {
			return model.ErrNotFound
		}
		return decode(val, &e)
	})
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// LoadEdgesOut returns all outgoing edges of a given type from a node.
func (s *DiskStore) LoadEdgesOut(sourceID model.ID, edgeType model.EdgeType) ([]model.Edge, error) {
	return s.loadAdjEdges("adj_out", sourceID, edgeType)
}

// LoadEdgesIn returns all incoming edges of a given type to a node.
func (s *DiskStore) LoadEdgesIn(targetID model.ID, edgeType model.EdgeType) ([]model.Edge, error) {
	return s.loadAdjEdges("adj_in", targetID, edgeType)
}

func (s *DiskStore) loadAdjEdges(bucket string, id model.ID, edgeType model.EdgeType) ([]model.Edge, error) {
	key := adjKey(id, edgeType)

	var entries []AdjEntry
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		val := b.Get(key)
		if val == nil {
			return nil
		}
		return decode(val, &entries)
	})
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}

	edges := make([]model.Edge, 0, len(entries))
	for _, entry := range entries {
		e, err := s.LoadEdge(entry.EdgeID)
		if err != nil {
			continue // skip dangling
		}
		edges = append(edges, *e)
	}
	return edges, nil
}

// ============================================================
// Snapshot operations
// ============================================================

// SaveSnapshot persists the full graph state to disk.
// Reads all RAM data and writes it to bbolt.
func (s *DiskStore) SaveSnapshot(g GraphSnapshot) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		// Nodes
		nb := tx.Bucket([]byte("nodes"))
		for _, n := range g.Nodes {
			val, err := encode(n)
			if err != nil {
				return err
			}
			if err := nb.Put([]byte(n.ArtifactID.String()), val); err != nil {
				return err
			}
		}
		// Edges
		eb := tx.Bucket([]byte("edges"))
		for _, e := range g.Edges {
			val, err := encode(e)
			if err != nil {
				return err
			}
			if err := eb.Put([]byte(e.EdgeID.String()), val); err != nil {
				return err
			}
		}
		// Projections
		pb := tx.Bucket([]byte("projections"))
		for _, p := range g.Projections {
			val, err := encode(p)
			if err != nil {
				return err
			}
			if err := pb.Put([]byte(p.ProjectionKey().String()), val); err != nil {
				return err
			}
		}
		// Meta
		mb := tx.Bucket([]byte("meta"))
		if err := mb.Put([]byte("version"), []byte("1")); err != nil {
			return err
		}
		ts := make([]byte, 8)
		binary.BigEndian.PutUint64(ts, uint64(time.Now().Unix()))
		if err := mb.Put([]byte("saved_at"), ts); err != nil {
			return err
		}
		return nil
	})
}

// GraphSnapshot is a serializable snapshot of graph state.
type GraphSnapshot struct {
	Nodes       []*model.Artifact
	Edges       []*model.Edge
	Projections []*model.ArtifactProjection
}

// LoadSnapshot reads all graph state from disk.
func (s *DiskStore) LoadSnapshot() (*GraphSnapshot, error) {
	gs := &GraphSnapshot{}
	err := s.db.View(func(tx *bbolt.Tx) error {
		// Nodes
		nb := tx.Bucket([]byte("nodes"))
		c := nb.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var n model.Artifact
			if err := decode(v, &n); err != nil {
				return err
			}
			gs.Nodes = append(gs.Nodes, &n)
		}

		// Edges
		eb := tx.Bucket([]byte("edges"))
		c2 := eb.Cursor()
		for k, v := c2.First(); k != nil; k, v = c2.Next() {
			var e model.Edge
			if err := decode(v, &e); err != nil {
				return err
			}
			gs.Edges = append(gs.Edges, &e)
		}

		// Projections
		pb := tx.Bucket([]byte("projections"))
		c3 := pb.Cursor()
		for k, v := c3.First(); k != nil; k, v = c3.Next() {
			var p model.ArtifactProjection
			if err := decode(v, &p); err != nil {
				return err
			}
			gs.Projections = append(gs.Projections, &p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return gs, nil
}

// ============================================================
// Batch edge loading
// ============================================================

// LoadEdgesByIDs loads multiple edges by ID in a single transaction.
// IDs are sorted before traversal for efficient bbolt cursor access.
func (s *DiskStore) LoadEdgesByIDs(ids []model.EdgeID) ([]*model.Edge, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	sorted := make([]model.EdgeID, len(ids))
	copy(sorted, ids)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].String() < sorted[j].String()
	})

	var edges []*model.Edge
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("edges"))
		for _, id := range sorted {
			val := b.Get([]byte(id.String()))
			if val == nil {
				continue // skip dangling
			}
			var e model.Edge
			if err := decode(val, &e); err != nil {
				return err
			}
			edges = append(edges, &e)
		}
		return nil
	})
	return edges, err
}

// DeleteProjection removes a specific projection revision.
func (s *DiskStore) DeleteProjection(key model.ProjectionKey) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte("projections")).Delete([]byte(key.String()))
	})
}

// DeleteEdgeRevision removes a specific edge revision.
func (s *DiskStore) DeleteEdgeRevision(key model.EdgeRevisionKey) error {
	revisionKey := key.EdgeID.String() + ":" + fmt.Sprint(key.Revision)
	return s.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte("edges")).Delete([]byte(revisionKey))
	})
}

// LoadEdgeRevision loads a specific edge revision.
func (s *DiskStore) LoadEdgeRevision(key model.EdgeRevisionKey) (*model.Edge, error) {
	var e model.Edge
	revisionKey := key.EdgeID.String() + ":" + fmt.Sprint(key.Revision)
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("edges"))
		val := b.Get([]byte(revisionKey))
		if val == nil {
			return model.ErrNotFound
		}
		return decode(val, &e)
	})
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// ListAllArtifactIDs returns all artifact IDs currently in the store.
func (s *DiskStore) ListAllArtifactIDs(ctx context.Context) ([]model.ID, error) {
	var ids []model.ID
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("nodes"))
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			id, err := model.ParseID(string(k))
			if err != nil {
				continue
			}
			ids = append(ids, id)
		}
		return nil
	})
	return ids, err
}

// ListEdgeRevisions returns all edge revision keys in the store.
func (s *DiskStore) ListEdgeRevisions(ctx context.Context) ([]model.EdgeRevisionKey, error) {
	var keys []model.EdgeRevisionKey
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("edges"))
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			// Parse "EdgeID:Revision" format.
			keyStr := string(k)
			// For now, store EdgeID:0 as placeholder.
			id, err := model.ParseID(keyStr)
			if err != nil {
				continue
			}
			keys = append(keys, model.EdgeRevisionKey{EdgeID: model.EdgeID(id), Revision: 0})
		}
		return nil
	})
	return keys, err
}

// LoadArtifactsByIDs loads multiple artifacts by ID in a single transaction.
// Returns loaded artifacts + IDs of artifacts not found (missing).
func (s *DiskStore) LoadArtifactsByIDs(ctx context.Context, ids []model.ID) ([]*model.Artifact, []model.ID, error) {
	var artifacts []*model.Artifact
	var missing []model.ID
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("nodes"))
		for _, id := range ids {
			val := b.Get([]byte(id.String()))
			if val == nil {
				missing = append(missing, id)
				continue
			}
			var art model.Artifact
			if err := decode(val, &art); err != nil {
				return err
			}
			artifacts = append(artifacts, &art)
		}
		return nil
	})
	return artifacts, missing, err
}

// ============================================================
// Type index
// ============================================================

func (s *DiskStore) addToTypeIndex(nodeType model.NodeType, id model.ID) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("idx_type"))
		key := []byte{byte(nodeType)}
		var ids []model.ID
		if val := b.Get(key); val != nil {
			if err := decode(val, &ids); err != nil {
				return err
			}
			for _, existing := range ids {
				if existing == id {
					return nil // already indexed
				}
			}
		}
		ids = append(ids, id)
		val, err := encode(ids)
		if err != nil {
			return err
		}
		return b.Put(key, val)
	})
}

func (s *DiskStore) removeFromTypeIndex(nodeType model.NodeType, id model.ID) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("idx_type"))
		key := []byte{byte(nodeType)}
		val := b.Get(key)
		if val == nil {
			return nil
		}
		var ids []model.ID
		if err := decode(val, &ids); err != nil {
			return err
		}
		filtered := ids[:0]
		for _, existing := range ids {
			if existing != id {
				filtered = append(filtered, existing)
			}
		}
		if len(filtered) == 0 {
			return b.Delete(key)
		}
		newVal, err := encode(filtered)
		if err != nil {
			return err
		}
		return b.Put(key, newVal)
	})
}

func (s *DiskStore) listTypeIndex(nodeType model.NodeType) ([]model.ID, error) {
	var ids []model.ID
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("idx_type"))
		key := []byte{byte(nodeType)}
		val := b.Get(key)
		if val == nil {
			return nil
		}
		return decode(val, &ids)
	})
	return ids, err
}

// ============================================================
// Revision DAG
// ============================================================

func (s *DiskStore) saveRevisionDAG(artifactID model.ID, dag *RevisionDAG) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("rev_dag"))
		val, err := encode(dag)
		if err != nil {
			return err
		}
		return b.Put([]byte(artifactID.String()), val)
	})
}

func (s *DiskStore) loadRevisionDAG(artifactID model.ID) (*RevisionDAG, error) {
	var dag RevisionDAG
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("rev_dag"))
		val := b.Get([]byte(artifactID.String()))
		if val == nil {
			return model.ErrNotFound
		}
		return decode(val, &dag)
	})
	if err != nil {
		return nil, err
	}
	return &dag, nil
}

// ============================================================
// ScopeState operations
// ============================================================

// SaveScopeState persists a scope state. Idempotent — overwrites existing.
func (s *DiskStore) SaveScopeState(state *model.ScopeState) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("scope_state"))
		val, err := encode(state)
		if err != nil {
			return fmt.Errorf("save scope state: %w", err)
		}
		return b.Put([]byte(string(state.ScopeID)), val)
	})
}

// ScopeState retrieves a scope state by ID. Implements LifecycleStoreWriter.
func (s *DiskStore) ScopeState(_ context.Context, scopeID model.ScopeID) (*model.ScopeState, error) {
	var state model.ScopeState
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("scope_state"))
		val := b.Get([]byte(string(scopeID)))
		if val == nil {
			return model.ErrNotFound
		}
		return decode(val, &state)
	})
	if err != nil {
		return nil, err
	}
	return &state, nil
}

// ListAllScopeStates returns all scope states.
func (s *DiskStore) ListAllScopeStates() ([]model.ScopeState, error) {
	var states []model.ScopeState
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("scope_state"))
		c := b.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var state model.ScopeState
			if err := decode(v, &state); err != nil {
				return err
			}
			states = append(states, state)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return states, nil
}

// SetScopeState updates the lifecycle state of an existing scope.
// Implements LifecycleStoreWriter.
func (s *DiskStore) SetScopeState(_ context.Context, scopeID model.ScopeID, state model.LifecycleState) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("scope_state"))
		val := b.Get([]byte(string(scopeID)))
		if val == nil {
			return model.ErrNotFound
		}
		var ss model.ScopeState
		if err := decode(val, &ss); err != nil {
			return err
		}
		ss.State = state
		newVal, err := encode(&ss)
		if err != nil {
			return err
		}
		return b.Put([]byte(string(scopeID)), newVal)
	})
}

// SaveScopeChild registers a child scope under a parent.
// Idempotent — existing parent/child mappings are overwritten.
func (s *DiskStore) SaveScopeChild(parent, child model.ScopeID) error {
	key := string(parent) + ":" + string(child)
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("scope_children"))
		return b.Put([]byte(key), []byte("1"))
	})
}

// ScopeChildren returns all child scope IDs for a given parent.
// Implements LifecycleStoreWriter.
func (s *DiskStore) ScopeChildren(_ context.Context, scopeID model.ScopeID) ([]model.ScopeID, error) {
	var children []model.ScopeID
	prefix := []byte(string(scopeID) + ":")
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("scope_children"))
		c := b.Cursor()
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			childStr := string(k[len(prefix):])
			children = append(children, model.ScopeID(childStr))
		}
		return nil
	})
	return children, err
}

// NodeType returns the NodeType for a given artifact ID.
func (s *DiskStore) NodeType(id model.ID) (model.NodeType, error) {
	var n model.Artifact
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("nodes"))
		val := b.Get([]byte(id.String()))
		if val == nil {
			return model.ErrNotFound
		}
		return decode(val, &n)
	})
	if err != nil {
		return 0, err
	}
	return n.NodeType, nil
}

// GetMeta retrieves a metadata value by key.
func (s *DiskStore) GetMeta(key string) (string, error) {
	var val string
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("meta"))
		v := b.Get([]byte(key))
		if v == nil {
			return model.ErrNotFound
		}
		val = string(v)
		return nil
	})
	return val, err
}

// SetMeta stores a metadata value by key.
func (s *DiskStore) SetMeta(key, value string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("meta"))
		return b.Put([]byte(key), []byte(value))
	})
}

// ScopeIDForNode resolves the ScopeID for a given artifact/node ID.
func (s *DiskStore) ScopeIDForNode(id model.ID) (model.ScopeID, error) {
	idStr := id.String()
	var found model.ScopeID
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte("scope_state"))
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			if string(k) == idStr {
				found = model.ScopeID(idStr)
				return nil
			}
		}
		return model.ErrNotFound
	})
	if err != nil {
		return "", err
	}
	return found, nil
}

// ============================================================
// Helpers
// ============================================================

func adjKey(id model.ID, edgeType model.EdgeType) []byte {
	return []byte(fmt.Sprintf("%s:%d", id.String(), edgeType))
}

func appendAdj(tx *bbolt.Tx, bucket string, id model.ID, edgeType model.EdgeType, entry AdjEntry) error {
	b := tx.Bucket([]byte(bucket))
	key := adjKey(id, edgeType)
	var entries []AdjEntry
	if val := b.Get(key); val != nil {
		if err := decode(val, &entries); err != nil {
			return err
		}
	}
	entries = append(entries, entry)
	val, err := encode(entries)
	if err != nil {
		return err
	}
	return b.Put(key, val)
}
