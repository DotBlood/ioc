// Package core holds the pure domain types for the IOC slice.
// It has no dependencies on storage, embedding, search, or engine packages
// (those import core, never the reverse) — this keeps the dependency graph acyclic.
package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// ID is a stable, time-sortable unique identifier (ULID).
// It never changes for the lifetime of an entity.
type ID struct {
	ulid.ULID
}

// NilID is the zero-value ID.
var NilID = ID{}

// NewID creates a new time-sortable ID.
func NewID() ID {
	now := time.Now()
	entropy := ulid.Monotonic(rand.Reader, 0)
	return ID{ulid.MustNew(ulid.Timestamp(now), entropy)}
}

// ParseID parses a ULID string.
func ParseID(s string) (ID, error) {
	u, err := ulid.ParseStrict(s)
	if err != nil {
		return NilID, fmt.Errorf("parse id %q: %w", s, err)
	}
	return ID{u}, nil
}

func (id ID) String() string { return id.ULID.String() }

// IsZero reports whether this is the zero value.
func (id ID) IsZero() bool { return id == NilID }

// MarshalText implements encoding.TextMarshaler (used for JSON map keys / fields).
func (id ID) MarshalText() ([]byte, error) { return id.ULID.MarshalText() }

// UnmarshalText implements encoding.TextUnmarshaler.
func (id *ID) UnmarshalText(b []byte) error { return id.ULID.UnmarshalText(b) }

// ContentHash is a SHA-256 content hash.
// Used for CAS deduplication and integrity, never as primary identity.
type ContentHash [32]byte

// NewContentHash computes a SHA-256 hash from content.
func NewContentHash(content []byte) ContentHash {
	return ContentHash(sha256.Sum256(content))
}

func (h ContentHash) String() string { return hex.EncodeToString(h[:]) }

// IsZero reports whether this is the zero value.
func (h ContentHash) IsZero() bool { return h == ContentHash{} }

// EmbeddingRef is a 1-indexed handle into the vector store (0 = no embedding).
type EmbeddingRef uint64

// IsZero reports whether there is no embedding.
func (r EmbeddingRef) IsZero() bool { return r == 0 }
