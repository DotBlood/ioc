package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"

	"github.com/DotBlood/ioc/internal/core"
)

// Meta is a bbolt-backed metadata store for scopes, artifacts, and traces.
// At slice scale, child/scope listings scan a bucket (no secondary indexes).
type Meta struct {
	db  *bolt.DB
	box *Box // at-rest value encryption (nil = off)
}

var (
	bkConfig    = []byte("config")
	bkScopes    = []byte("scopes")
	bkArtifacts = []byte("artifacts")
	bkTraces    = []byte("traces")
	bkEdges     = []byte("edges")
)

// plaintextConfigKeys are config values NEVER encrypted — they must be readable
// without the key. "enc" is the cross-store sentinel the engine reads to detect a
// key/format mismatch before any encrypted read.
var plaintextConfigKeys = map[string]bool{"enc": true}

// OpenMeta opens or creates the metadata store at path. A non-nil box encrypts
// record VALUES at rest (bbolt KEYS stay plaintext — they are needed for lookup).
func OpenMeta(path string, box *Box) (*Meta, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("meta: mkdir: %w", err)
	}
	// 0o600: meta.db is bbolt plaintext (scopes, summaries, the runtime token's
	// neighbours) — keep it owner-only so other local users on a shared host can't
	// read the memory. bolt.Open's mode applies only when CREATING the file, so
	// also tighten an existing 0o644 db (best-effort; chmod is a no-op on Windows).
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		if errors.Is(err, bolterrors.ErrTimeout) {
			return nil, fmt.Errorf("meta: data dir %q is busy — locked by another ioc/ioc-mcp process (close it or use a different -dir): %w", path, err)
		}
		return nil, fmt.Errorf("meta: open: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bkConfig, bkScopes, bkArtifacts, bkTraces, bkEdges} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("meta: init buckets: %w", err)
	}
	return &Meta{db: db, box: box}, nil
}

// encodeValue/decodeValue apply at-rest value encryption (AAD = the bbolt key, so
// a value cannot be relocated to another key). No-ops when the box is off.
func (m *Meta) encodeValue(key string, data []byte) ([]byte, error) {
	if m.box.Enabled() {
		return m.box.Seal(data, []byte(key))
	}
	return data, nil
}

func (m *Meta) decodeValue(key string, blob []byte) ([]byte, error) {
	if m.box.Enabled() {
		return m.box.Open(blob, []byte(key))
	}
	return blob, nil
}

// Close closes the database.
func (m *Meta) Close() error { return m.db.Close() }

// IsEmpty reports whether the store has no scopes or artifacts — a brand-new
// store before any user data has been written. The config bucket is excluded
// because sentinels (encryption, schema version) are written there before any
// user data exists; including it would cause a freshly-stamped store to appear
// non-empty and break schema-version bootstrapping. It inspects key COUNTS only
// (no value decryption), so it is safe to call before the encryption box is
// validated.
func (m *Meta) IsEmpty() bool {
	empty := true
	_ = m.db.View(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bkScopes, bkArtifacts} {
			if tx.Bucket(b).Stats().KeyN > 0 {
				empty = false
			}
		}
		return nil
	})
	return empty
}

// --- config ---

// PutConfig stores a small string config value (encrypted unless in the
// always-plaintext allow-list).
func (m *Meta) PutConfig(key, val string) error {
	data := []byte(val)
	if !plaintextConfigKeys[key] {
		var err error
		if data, err = m.encodeValue(key, data); err != nil {
			return err
		}
	}
	return m.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bkConfig).Put([]byte(key), data)
	})
}

// DeleteConfig removes a config key. It is a no-op when the key is absent.
// Primarily used in tests and migration helpers; normal writes use PutConfig.
func (m *Meta) DeleteConfig(key string) error {
	return m.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bkConfig).Delete([]byte(key))
	})
}

// GetConfig reads a config value; returns ("", false) if absent (or undecryptable).
func (m *Meta) GetConfig(key string) (string, bool) {
	var raw []byte
	_ = m.db.View(func(tx *bolt.Tx) error {
		if v := tx.Bucket(bkConfig).Get([]byte(key)); v != nil {
			raw = append([]byte(nil), v...)
		}
		return nil
	})
	if raw == nil {
		return "", false
	}
	if plaintextConfigKeys[key] {
		return string(raw), true
	}
	dec, err := m.decodeValue(key, raw)
	if err != nil {
		return "", false
	}
	return string(dec), true
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
		return tx.Bucket(bkScopes).ForEach(func(k, v []byte) error {
			dec, err := m.decodeValue(string(k), v)
			if err != nil {
				return err
			}
			var s core.Scope
			if err := json.Unmarshal(dec, &s); err != nil {
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
		return tx.Bucket(bkArtifacts).ForEach(func(k, v []byte) error {
			dec, err := m.decodeValue(string(k), v)
			if err != nil {
				return err
			}
			var a core.Artifact
			if err := json.Unmarshal(dec, &a); err != nil {
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
		return tx.Bucket(bkArtifacts).ForEach(func(k, v []byte) error {
			dec, err := m.decodeValue(string(k), v)
			if err != nil {
				return err
			}
			var a core.Artifact
			if err := json.Unmarshal(dec, &a); err != nil {
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
		return tx.Bucket(bkTraces).ForEach(func(k, v []byte) error {
			dec, err := m.decodeValue(string(k), v)
			if err != nil {
				return err
			}
			var t core.TraceRecord
			if err := json.Unmarshal(dec, &t); err != nil {
				return err
			}
			out = append(out, t)
			return nil
		})
	})
	return out, err
}

// --- edges (author-declared relations between artifacts) ---

// edgeKey is the unique composite key for an edge: re-Putting the same
// (from, to, kind) overwrites in place, so edge creation is idempotent.
func edgeKey(e core.Edge) string {
	return e.From.String() + "|" + e.To.String() + "|" + string(e.Kind)
}

// PutEdge stores (or idempotently replaces) a directed edge.
func (m *Meta) PutEdge(e core.Edge) error {
	return m.putJSON(bkEdges, edgeKey(e), e)
}

// edgesWhere scans the edge bucket and returns those for which keep is true.
func (m *Meta) edgesWhere(keep func(core.Edge) bool) ([]core.Edge, error) {
	var out []core.Edge
	err := m.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bkEdges).ForEach(func(k, v []byte) error {
			dec, err := m.decodeValue(string(k), v)
			if err != nil {
				return err
			}
			var e core.Edge
			if err := json.Unmarshal(dec, &e); err != nil {
				return err
			}
			if keep(e) {
				out = append(out, e)
			}
			return nil
		})
	})
	return out, err
}

// EdgesFrom returns all edges originating at id.
func (m *Meta) EdgesFrom(id core.ID) ([]core.Edge, error) {
	return m.edgesWhere(func(e core.Edge) bool { return e.From == id })
}

// EdgesTo returns all edges pointing at id.
func (m *Meta) EdgesTo(id core.ID) ([]core.Edge, error) {
	return m.edgesWhere(func(e core.Edge) bool { return e.To == id })
}

// AllEdges returns every edge in one scan (used by graph-aware retrieval to build
// adjacency over a candidate set without a per-candidate scan).
func (m *Meta) AllEdges() ([]core.Edge, error) {
	return m.edgesWhere(func(core.Edge) bool { return true })
}

// DeleteEdgesFor removes every edge touching id (as From or To) — called when an
// artifact is deleted so no dangling edges remain. Keys are collected first, then
// deleted (mutating a bucket mid-ForEach is unsafe).
func (m *Meta) DeleteEdgesFor(id core.ID) error {
	return m.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bkEdges)
		var del [][]byte
		err := b.ForEach(func(k, v []byte) error {
			dec, derr := m.decodeValue(string(k), v)
			if derr != nil {
				return derr
			}
			var e core.Edge
			if derr := json.Unmarshal(dec, &e); derr != nil {
				return derr
			}
			if e.From == id || e.To == id {
				del = append(del, append([]byte(nil), k...))
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, k := range del {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// --- compaction ---

// RemapEmbeddings atomically rewrites every artifact's EmbRef and every scope's
// RollupEmbRef through remap, and applies configUpdates, in a SINGLE bbolt
// transaction — the commit is the atomic switch point for engine.Compact (it
// flips the emb_file pointer in the same txn). A non-zero ref absent from remap
// aborts the transaction (the store is left untouched), guarding against a remap
// built from a stale snapshot. Mutation is deferred until after each ForEach
// (mutating a bucket mid-iteration is unsafe). Returns the count of artifact and
// scope records actually rewritten (ref changed). Cost scales with the TOTAL
// number of artifact + scope records (each is decoded to inspect its ref), not
// just the orphan count — acceptable at slice scale, where compaction is rare.
func (m *Meta) RemapEmbeddings(remap map[core.EmbeddingRef]core.EmbeddingRef, configUpdates map[string]string) (nArtifacts, nScopes int, err error) {
	type kv struct{ k, v []byte }
	err = m.db.Update(func(tx *bolt.Tx) error {
		// Artifacts: remap EmbRef.
		ab := tx.Bucket(bkArtifacts)
		var writes []kv
		if ferr := ab.ForEach(func(k, v []byte) error {
			dec, derr := m.decodeValue(string(k), v)
			if derr != nil {
				return derr
			}
			var a core.Artifact
			if derr := json.Unmarshal(dec, &a); derr != nil {
				return derr
			}
			if a.EmbRef == 0 {
				return nil
			}
			nr, ok := remap[a.EmbRef]
			if !ok {
				return fmt.Errorf("meta: remap: artifact %s ref %d not in remap", a.ID, a.EmbRef)
			}
			if nr == a.EmbRef {
				return nil
			}
			a.EmbRef = nr
			data, merr := json.Marshal(a)
			if merr != nil {
				return merr
			}
			enc, eerr := m.encodeValue(string(k), data)
			if eerr != nil {
				return eerr
			}
			writes = append(writes, kv{append([]byte(nil), k...), enc})
			return nil
		}); ferr != nil {
			return ferr
		}
		for _, w := range writes {
			if perr := ab.Put(w.k, w.v); perr != nil {
				return perr
			}
		}
		nArtifacts = len(writes)

		// Scopes: remap RollupEmbRef.
		sb := tx.Bucket(bkScopes)
		writes = writes[:0]
		if ferr := sb.ForEach(func(k, v []byte) error {
			dec, derr := m.decodeValue(string(k), v)
			if derr != nil {
				return derr
			}
			var s core.Scope
			if derr := json.Unmarshal(dec, &s); derr != nil {
				return derr
			}
			if s.RollupEmbRef == 0 {
				return nil
			}
			nr, ok := remap[s.RollupEmbRef]
			if !ok {
				return fmt.Errorf("meta: remap: scope %s rollup ref %d not in remap", s.ID, s.RollupEmbRef)
			}
			if nr == s.RollupEmbRef {
				return nil
			}
			s.RollupEmbRef = nr
			data, merr := json.Marshal(s)
			if merr != nil {
				return merr
			}
			enc, eerr := m.encodeValue(string(k), data)
			if eerr != nil {
				return eerr
			}
			writes = append(writes, kv{append([]byte(nil), k...), enc})
			return nil
		}); ferr != nil {
			return ferr
		}
		for _, w := range writes {
			if perr := sb.Put(w.k, w.v); perr != nil {
				return perr
			}
		}
		nScopes = len(writes)

		// Config: flip emb_file / emb_gen (and any other supplied keys) in the
		// same txn so the pointer and the refs commit together.
		cb := tx.Bucket(bkConfig)
		for ck, cv := range configUpdates {
			data := []byte(cv)
			if !plaintextConfigKeys[ck] {
				enc, eerr := m.encodeValue(ck, data)
				if eerr != nil {
					return eerr
				}
				data = enc
			}
			if perr := cb.Put([]byte(ck), data); perr != nil {
				return perr
			}
		}
		return nil
	})
	return nArtifacts, nScopes, err
}

// --- helpers ---

func (m *Meta) putJSON(bucket []byte, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("meta: marshal: %w", err)
	}
	if data, err = m.encodeValue(key, data); err != nil {
		return err
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
		dec, err := m.decodeValue(key, v)
		if err != nil {
			return err
		}
		return json.Unmarshal(dec, dst)
	})
}
