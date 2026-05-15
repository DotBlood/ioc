package model

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"
)

func TestNewID(t *testing.T) {
	id1 := NewID()
	id2 := NewID()

	if id1.IsZero() {
		t.Error("NewID() returned zero ID")
	}

	if id1.String() == id2.String() {
		t.Error("two NewID() calls produced the same ID")
	}

	parsed, err := ParseID(id1.String())
	if err != nil {
		t.Fatalf("ParseID(%q): %v", id1.String(), err)
	}
	if parsed != id1 {
		t.Errorf("ParseID roundtrip: got %v, want %v", parsed, id1)
	}
}

func TestContentHash(t *testing.T) {
	c1 := NewContentHash([]byte("hello"))
	c2 := NewContentHash([]byte("hello"))
	c3 := NewContentHash([]byte("world"))

	if c1 != c2 {
		t.Error("same content produced different hashes")
	}
	if c1 == c3 {
		t.Error("different content produced same hash")
	}
	if c1.IsZero() {
		t.Error("NewContentHash returned zero hash")
	}

	encoded := c1.String()
	parsed, err := ParseContentHash(encoded)
	if err != nil {
		t.Fatalf("ParseContentHash(%q): %v", encoded, err)
	}
	if parsed != c1 {
		t.Errorf("ParseContentHash roundtrip: got %v, want %v", parsed, c1)
	}
}

func TestContentHash_Invalid(t *testing.T) {
	_, err := ParseContentHash("invalid")
	if err == nil {
		t.Error("ParseContentHash should fail on invalid hex")
	}

	_, err = ParseContentHash("abcdef") // too short
	if err == nil {
		t.Error("ParseContentHash should fail on short hash")
	}
}

func TestParseID_Invalid(t *testing.T) {
	_, err := ParseID("not-a-ulid")
	if err == nil {
		t.Error("ParseID should fail on invalid ULID")
	}
}

func TestProjectionKey(t *testing.T) {
	id := NewID()
	key := ProjectionKey{ArtifactID: id, Revision: 42}

	str := key.String()
	expected := fmt.Sprintf("%s:42", id.String())
	if str != expected {
		t.Errorf("ProjectionKey.String() = %q, want %q", str, expected)
	}

	if key.IsZero() {
		t.Error("non-zero ProjectionKey.IsZero() returned true")
	}

	var zero ProjectionKey
	if !zero.IsZero() {
		t.Error("zero ProjectionKey.IsZero() returned false")
	}
}

func TestEdgeType_String(t *testing.T) {
	tests := []struct {
		typ  EdgeType
		want string
	}{
		{EdgeLineage, "lineage"},
		{EdgeOwnership, "ownership"},
		{EdgeRetrieval, "retrieval"},
		{EdgeReference, "reference"},
		{EdgeCitation, "citation"},
		{EdgeDerivedFrom, "derived_from"},
		{EdgeRelatedTo, "related_to"},
		{EdgeTemporal, "temporal"},
		{EdgeType(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.typ.String(); got != tt.want {
				t.Errorf("EdgeType(%d).String() = %q, want %q", tt.typ, got, tt.want)
			}
		})
	}
}

func TestEdgeDirection_String(t *testing.T) {
	tests := []struct {
		dir  EdgeDirection
		want string
	}{
		{DirectionDirected, "directed"},
		{DirectionUndirected, "undirected"},
		{DirectionSymmetric, "symmetric"},
		{EdgeDirection(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.dir.String(); got != tt.want {
				t.Errorf("EdgeDirection(%d).String() = %q, want %q", tt.dir, got, tt.want)
			}
		})
	}
}

func TestEdge_IsValidAt(t *testing.T) {
	now := time.Now()

	t.Run("valid edge", func(t *testing.T) {
		e := &Edge{Valid: true, ValidFrom: now.Add(-time.Hour)}
		if !e.IsValidAt(now) {
			t.Error("valid edge should be valid at now")
		}
	})

	t.Run("invalidated edge", func(t *testing.T) {
		e := &Edge{Valid: false, ValidFrom: now.Add(-time.Hour)}
		if e.IsValidAt(now) {
			t.Error("invalidated edge should not be valid")
		}
	})

	t.Run("expired edge", func(t *testing.T) {
		to := now.Add(-time.Minute)
		e := &Edge{Valid: true, ValidFrom: now.Add(-time.Hour), ValidTo: &to}
		if e.IsValidAt(now) {
			t.Error("expired edge should not be valid")
		}
	})

	t.Run("future edge", func(t *testing.T) {
		e := &Edge{Valid: true, ValidFrom: now.Add(time.Hour)}
		if e.IsValidAt(now) {
			t.Error("future edge should not be valid now")
		}
	})
}

func TestDefaultEdgeConstraints(t *testing.T) {
	t.Run("lineage", func(t *testing.T) {
		c := DefaultEdgeConstraints(EdgeLineage)
		if c.CyclePolicy != CyclesForbidden {
			t.Error("lineage should forbid cycles")
		}
		if c.CrossScope {
			t.Error("lineage should not be cross-scope")
		}
	})

	t.Run("retrieval", func(t *testing.T) {
		c := DefaultEdgeConstraints(EdgeRetrieval)
		if c.CyclePolicy != CyclesAllowed {
			t.Error("retrieval should allow cycles")
		}
		if !c.CrossScope {
			t.Error("retrieval should be cross-scope")
		}
	})
}

func TestReadiness(t *testing.T) {
	var r Readiness

	if r.Has(ReadinessStored) {
		t.Error("zero readiness should not have Stored flag")
	}

	r.Add(ReadinessStored | ReadinessEmbedded)
	if !r.Has(ReadinessStored) {
		t.Error("should have Stored after Add")
	}
	if !r.Has(ReadinessEmbedded) {
		t.Error("should have Embedded after Add")
	}
	if r.Has(ReadinessIndexed) {
		t.Error("should not have Indexed")
	}

	r.Remove(ReadinessEmbedded)
	if r.Has(ReadinessEmbedded) {
		t.Error("should not have Embedded after Remove")
	}
}

func TestLifecycleTransitions(t *testing.T) {
	tests := []struct {
		from  LifecycleState
		to    LifecycleState
		valid bool
	}{
		{LifecycleDraft, LifecycleActive, true},
		{LifecycleActive, LifecycleArchived, true},
		{LifecycleActive, LifecycleDetached, true},
		{LifecycleArchived, LifecycleActive, true},
		{LifecycleArchived, LifecycleDeleted, true},
		{LifecycleDetached, LifecycleActive, true},
		{LifecycleDetached, LifecycleDeleted, true},
		{LifecycleDraft, LifecycleDeleted, false},
		{LifecycleDeleted, LifecycleActive, false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s→%s", tt.from, tt.to), func(t *testing.T) {
			got := IsValidTransition(tt.from, tt.to)
			if got != tt.valid {
				t.Errorf("IsValidTransition(%v, %v) = %v, want %v", tt.from, tt.to, got, tt.valid)
			}
		})
	}
}

func TestScore_Compute(t *testing.T) {
	s := Score{
		CosineSimilarity: 0.8,
		ScopePriority:    1.0,
		Recency:          0.5,
		IsActive:         1.0,
		LineageDepth:     1.0,
	}
	s.Compute()

	expected := 0.825
	if abs(s.Final-expected) > 1e-9 {
		t.Errorf("Score.Final = %v, want %v", s.Final, expected)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func TestProjection_IsValidAt(t *testing.T) {
	now := time.Now()

	p := &ArtifactProjection{
		ValidFrom: now.Add(-time.Hour),
	}
	if !p.IsValidAt(now) {
		t.Error("current projection should be valid at now")
	}

	to := now.Add(-time.Minute)
	p.ValidTo = &to
	if p.IsValidAt(now) {
		t.Error("expired projection should not be valid")
	}
}

func TestSHA256(t *testing.T) {
	sum := sha256.Sum256([]byte("test"))
	h := NewContentHash([]byte("test"))
	if ContentHash(sum) != h {
		t.Error("NewContentHash does not match sha256.Sum256")
	}
}

func TestArtifact_HasParent(t *testing.T) {
	a := &Artifact{}
	if a.HasParent() {
		t.Error("artifact without parent should return false")
	}

	a.Lineage.Parent = NewID()
	if !a.HasParent() {
		t.Error("artifact with parent should return true")
	}
}

func TestNodeType_String(t *testing.T) {
	tests := []struct {
		typ  NodeType
		want string
	}{
		{NodeTypeWorktree, "worktree"},
		{NodeTypeWorkspace, "workspace"},
		{NodeTypeSession, "session"},
		{NodeTypeArtifact, "artifact"},
		{NodeType(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.typ.String(); got != tt.want {
				t.Errorf("NodeType(%d).String() = %q, want %q", tt.typ, got, tt.want)
			}
		})
	}
}
