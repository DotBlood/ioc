package model

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

func (id ID) String() string {
	return id.ULID.String()
}

// IsZero returns true if this is the zero value.
func (id ID) IsZero() bool {
	return id == NilID
}

// MarshalText implements encoding.TextMarshaler.
func (id ID) MarshalText() ([]byte, error) {
	return id.ULID.MarshalText()
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (id ID) UnmarshalText(b []byte) error {
	return id.ULID.UnmarshalText(b)
}

// ContentHash is a SHA-256 content hash.
// Used for CAS deduplication and integrity, never as primary identity.
type ContentHash [32]byte

// NewContentHash computes a SHA-256 hash from content.
func NewContentHash(content []byte) ContentHash {
	return ContentHash(sha256.Sum256(content))
}

// ParseContentHash parses a hex-encoded SHA-256 hash.
func ParseContentHash(s string) (ContentHash, error) {
	var h ContentHash
	b, err := hex.DecodeString(s)
	if err != nil {
		return h, fmt.Errorf("parse content hash: %w", err)
	}
	if len(b) != 32 {
		return h, fmt.Errorf("content hash must be 32 bytes, got %d", len(b))
	}
	copy(h[:], b)
	return h, nil
}

func (h ContentHash) String() string {
	return hex.EncodeToString(h[:])
}

// IsZero returns true if this is the zero value.
func (h ContentHash) IsZero() bool {
	return h == ContentHash{}
}

// EdgeID is a stable ULID for edges.
type EdgeID = ID

// ProjectionKey identifies a specific revision of an artifact's projection.
type ProjectionKey struct {
	ArtifactID ID
	Revision   RevisionNumber
}

func (k ProjectionKey) String() string {
	return fmt.Sprintf("%s:%d", k.ArtifactID, k.Revision)
}

// IsZero returns true if the key is empty.
func (k ProjectionKey) IsZero() bool {
	return k.ArtifactID.IsZero() && k.Revision == 0
}

// RevisionNumber is a monotonic counter per Artifact.
type RevisionNumber uint64

// RevisionRef specifies how to resolve a revision for retrieval.
type RevisionRef struct {
	StableID ID
	Revision RevisionNumber // 0 = latest
	Branch   BranchName     // "" = default
	Filter   RevisionFilter
}

// RevisionFilter controls how revision resolution works.
type RevisionFilter uint8

const (
	RevFilterLatest      RevisionFilter = 0
	RevFilterPinned      RevisionFilter = 1
	RevFilterBranchLocal RevisionFilter = 2
	RevFilterAncestor    RevisionFilter = 3
)

// BranchName identifies a revision branch.
type BranchName string

const DefaultBranch BranchName = "main"

// ScopeID is the full path in the scope tree.
type ScopeID string
