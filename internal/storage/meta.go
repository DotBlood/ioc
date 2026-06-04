package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/DotBlood/ioc/internal/core"
)

// Meta is a bbolt-backed metadata store for scopes, artifacts, and traces.
// At slice scale, child/scope listings scan a bucket (no secondary indexes).
type Meta struct {
	db *bolt.DB
}

var (
	bkConfig    = []byte("config")
	bkScopes    = []byte("scopes")
	bkArtifacts = []byte("artifacts")
	bkTraces    = []byte("traces")
)

// OpenMeta opens or creates the metadata store at path.
func OpenMeta(path string) (*Meta, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("meta: mkdir: %w", err)
	}
	db, err := bolt.Open(path, 0o644, &bolt.Options{Timeout: time.Second})
	if err != nil {
		if errors.Is(err, bolt.ErrTimeout) {
			return nil, fmt.Errorf("meta: data dir %q is busy — locked by another ioc/ioc-mcp process (close it or use a different -dir): %w", path, err)
		}
		return nil, fmt.Errorf("meta: open: %w", err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bkConfig, bkScopes, bkArtifacts, bkTraces} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("meta: init buckets: %w", err)
	}
	return &Meta{db: db}, nil
}

// Close closes the database.
func (m *Meta) Close() error { return m.db.Close() }

// --- config ---

// PutConfig stores a small string config value.
func (m *Meta) PutConfig(key, val string) error {
	return m.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bkConfig).Put([]byte(key), []byte(val))
	})
}

// GetConfig reads a config value; returns ("", false) if absent.
func (m *Meta) GetConfig(key string) (string, bool) {
	var val string
	var ok bool
	_ = m.db.View(func(tx *bolt.Tx) error {
		if v := tx.Bucket(bkConfig).Get([]byte(key)); v != nil {
			val, ok = string(v), true
		}
		return nil
	})
	return val, ok
}

// --- scopes ---

// PutScope inserts or updates a scope.
func (m *Meta) PutScope(s core.Scope) error {
	return m.putJSON(bkScopes, s.ID.String(), s)
}

// GetScope loads a scope by ID.
func (m *Meta) GetScope(id core.ID) (core.Scope, error) {
	var s core.Scope
	err := m.getJSON(bkScopes, id.String(), &s)
	return s, err
}

// ListScopes returns all scopes.
func (m *Meta) ListScopes() ([]core.Scope, error) {
	var out []core.Scope
	err := m.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bkScopes).ForEach(func(_, v []byte) error {
			var s core.Scope
			if err := json.Unmarshal(v, &s); err != nil {
				return err
			}
			out = append(out, s)
			return nil
		})
	})
	return out, err
}

// ChildScopes returns scopes whose Parent == parent.
func (m *Meta) ChildScopes(parent core.ID) ([]core.Scope, error) {
	all, err := m.ListScopes()
	if err != nil {
		return nil, err
	}
	var out []core.Scope
	for _, s := range all {
		if s.Parent == parent {
			out = append(out, s)
		}
	}
	return out, nil
}

// --- artifacts ---

// PutArtifact inserts or updates an artifact.
func (m *Meta) PutArtifact(a core.Artifact) error {
	return m.putJSON(bkArtifacts, a.ID.String(), a)
}

// GetArtifact loads an artifact by ID.
func (m *Meta) GetArtifact(id core.ID) (core.Artifact, error) {
	var a core.Artifact
	err := m.getJSON(bkArtifacts, id.String(), &a)
	return a, err
}

// ArtifactsInScope returns all artifacts whose Scope == scope.
func (m *Meta) ArtifactsInScope(scope core.ID) ([]core.Artifact, error) {
	var out []core.Artifact
	err := m.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bkArtifacts).ForEach(func(_, v []byte) error {
			var a core.Artifact
			if err := json.Unmarshal(v, &a); err != nil {
				return err
			}
			if a.Scope == scope {
				out = append(out, a)
			}
			return nil
		})
	})
	return out, err
}

// ListArtifacts returns all artifacts (full scan; no secondary index).
func (m *Meta) ListArtifacts() ([]core.Artifact, error) {
	var out []core.Artifact
	err := m.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bkArtifacts).ForEach(func(_, v []byte) error {
			var a core.Artifact
			if err := json.Unmarshal(v, &a); err != nil {
				return err
			}
			out = append(out, a)
			return nil
		})
	})
	return out, err
}

// DeleteArtifact removes an artifact record. Its embedding stays in the
// append-only EmbeddingStore (dead weight; never re-surfaces in search, which
// builds its candidate set from live artifacts).
func (m *Meta) DeleteArtifact(id core.ID) error {
	return m.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bkArtifacts).Delete([]byte(id.String()))
	})
}

// DeleteScope removes a scope record. Callers must ensure the scope is empty
// (no artifacts, no children) first.
func (m *Meta) DeleteScope(id core.ID) error {
	return m.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bkScopes).Delete([]byte(id.String()))
	})
}

// --- traces ---

// PutTrace stores a trace record.
func (m *Meta) PutTrace(t core.TraceRecord) error {
	return m.putJSON(bkTraces, t.QueryID.String(), t)
}

// GetTrace loads a trace by query ID.
func (m *Meta) GetTrace(id core.ID) (core.TraceRecord, error) {
	var t core.TraceRecord
	err := m.getJSON(bkTraces, id.String(), &t)
	return t, err
}

// ListTraces returns all trace records.
func (m *Meta) ListTraces() ([]core.TraceRecord, error) {
	var out []core.TraceRecord
	err := m.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bkTraces).ForEach(func(_, v []byte) error {
			var t core.TraceRecord
			if err := json.Unmarshal(v, &t); err != nil {
				return err
			}
			out = append(out, t)
			return nil
		})
	})
	return out, err
}

// --- helpers ---

func (m *Meta) putJSON(bucket []byte, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("meta: marshal: %w", err)
	}
	return m.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Put([]byte(key), data)
	})
}

func (m *Meta) getJSON(bucket []byte, key string, dst any) error {
	return m.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucket).Get([]byte(key))
		if v == nil {
			return core.ErrNotFound
		}
		return json.Unmarshal(v, dst)
	})
}
